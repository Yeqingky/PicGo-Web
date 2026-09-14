// Command picgo-web 是 PicGo-Web 的主程序。
//
// 装配顺序：
//
//	config → logger → data dirs → 主密钥 → database → migrations → 首启引导
//	  → settings → agent 客户端 → 事件总线 → HTTP server → 业务路由
//	  → 存储配置 reconcile → 上传队列 → 定时任务
//
// 退出顺序（优雅关闭）：
//
//	停止接受新上传 → 等在途项完成 → 停定时任务 → HTTP Shutdown → 关数据库
//
// 启动失败一律以非零退出码终止，并把原因写日志（便于容器编排定位）。
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"log/slog"

	"github.com/YeqingKy/PicGo-Web/server/internal/agent"
	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/crypto"
	"github.com/YeqingKy/PicGo-Web/server/internal/database"
	"github.com/YeqingKy/PicGo-Web/server/internal/events"
	"github.com/YeqingKy/PicGo-Web/server/internal/handler"
	"github.com/YeqingKy/PicGo-Web/server/internal/logger"
	"github.com/YeqingKy/PicGo-Web/server/internal/lsky"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/scheduler"
	"github.com/YeqingKy/PicGo-Web/server/internal/server"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

func main() {
	if err := run(); err != nil {
		// 此时 slog 可能还没初始化，直接写 stderr 保证可见
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// ---- 1. 配置 ----
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}

	// ---- 2. 日志 ----
	log := logger.New(cfg.LogLevel, cfg.DevMode)
	if cfg.DevMode {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	log.Info("PicGo-Web 启动中",
		"version", server.Version,
		"listen", cfg.Listen,
		"data_dir", cfg.DataDir,
		"db_driver", string(cfg.DBDriver),
		"dev", cfg.DevMode,
	)

	// ---- 3. 目录 ----
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	// ---- 4. 主密钥（绝不进数据库，D19） ----
	key, generated, err := crypto.LoadOrCreateKey(cfg.SecretKey, cfg.SecretKeyFile())
	if err != nil {
		return fmt.Errorf("准备加密主密钥失败: %w", err)
	}
	cipher, err := crypto.New(key)
	if err != nil {
		return fmt.Errorf("初始化加密器失败: %w", err)
	}
	if generated {
		log.Warn("已生成新的加密主密钥，请立即备份（丢失后已加密的配置将无法解密）",
			"path", cfg.SecretKeyFile(),
		)
	}

	// ---- 5. 数据库 ----
	db, err := database.Open(cfg, log)
	if err != nil {
		return err
	}
	log.Info("数据库已连接", "driver", string(db.Driver))

	// ---- 6. 迁移 ----
	if cfg.DBAutoMigrate {
		if err := database.Migrate(db, log); err != nil {
			return fmt.Errorf("数据库迁移失败: %w", err)
		}
	} else {
		version, err := database.CurrentVersion(db.DB)
		if err != nil {
			return fmt.Errorf("读取数据库版本失败: %w", err)
		}
		if version < database.SchemaVersion {
			log.Warn("已跳过自动迁移，但数据库版本落后",
				"current", version, "expected", database.SchemaVersion,
			)
		}
	}

	// ---- 7. 首启引导：表里没有用户时创建一个初始管理员（D32）----
	//
	// 必须在 HTTP 服务启动前完成，否则系统里没有任何账号可登录。
	// 内部幂等：已有用户时直接返回（重复启动不会重建）。
	userRepo := repository.NewUserRepo(db.DB)
	if err := auth.Bootstrap(userRepo, cfg, log); err != nil {
		return fmt.Errorf("首启引导失败: %w", err)
	}

	// ---- 8. 配置服务 ----
	settingRepo := repository.NewSettingRepo(db.DB)
	settingsSvc, err := settings.New(settingRepo, cipher, log)
	if err != nil {
		return fmt.Errorf("初始化配置服务失败: %w", err)
	}
	log.Debug("配置服务已就绪", "keys", len(settingsSvc.All()))

	// 配置变更回调（后续 W4/W5 会在此处推送 agent、刷新限流器）
	settingsSvc.OnChanged(func(ev settings.ChangedEvent) {
		log.Debug("配置项已更新",
			"key", ev.Key, "source", string(ev.Source), "by", ev.By,
		)
	})

	// ---- 9. picgo-agent（W4）----
	//
	// 先建 Supervisor（解析并落盘共享令牌），再用同一令牌建 Client：
	// 令牌不一致会导致 agent 拒绝所有请求（这正是"内核不可用"的常见原因）。
	//
	// `PICGO_WEB_AGENT_MOCK=true` 时用内存 mock（无 Node 环境也能跑通全链路：
	// 前端联调、CI、以及本项目的端到端自测）。
	supervisor, err := buildAgentSupervisor(cfg, log)
	if err != nil {
		return fmt.Errorf("准备 picgo-agent 共享令牌失败: %w", err)
	}
	agentClient, err := buildAgentClient(cfg, settingsSvc, supervisor.Token(), log)
	if err != nil {
		return err
	}

	// ---- 10. 事件总线（W5）----
	//
	// Hub 是进程内广播；agent 的 SSE 由 events.AgentBridge 桥接进来 ——
	// 但**只桥接不可归属的事件类型**（system.notice / ping / job.log）。
	// 上传进度由 Go 自己发（带 UserUID 才能正确按用户投递），因此不上桥。
	hub := events.New(log)

	// agent 状态的**缓存快照**：后台刷新，handler 只读内存（保持 /healthz 轻量）。
	agentStatus := agent.NewStatusHolder()

	// ---- 10.5 拉起 agent 子进程（D7）----
	//
	// Autostart=true 时由 Go 拉起并注入令牌；进程退出会按退避策略重启，
	// 连续失败超上限后放弃（避免无限重启把日志刷爆）。
	if !cfg.AgentMock {
		// 关键：supervisor 是**唯一**知道「进程刚就绪」的组件。
		// 若只靠 probeAgentLoop 的 30s 轮询，用户会看到长达 30 秒的
		// 「内核不可用」误报。因此在这里立刻刷新状态快照。
		supervisor.OnStateChange(func(up bool, err error) {
			probeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			agentStatus.Refresh(probeCtx, agentClient)
			cancel()

			if up {
				hub.PublishNotice("info", "内核已就绪")
				return
			}
			msg := "内核不可用：上传与插件功能暂时失效"
			if err != nil {
				msg += "（" + err.Error() + "）"
			}
			hub.PublishNotice("error", msg)
		})
		if err := supervisor.Start(context.Background()); err != nil {
			// 不阻断启动：其余功能（登录、图库浏览）仍可用，只是上传/插件不可用
			log.Error("拉起 picgo-agent 失败，上传与插件功能将不可用", "err", err)
			hub.PublishNotice("error", "内核启动失败："+err.Error())
		}
	}

	// ---- 11. HTTP 服务 ----
	app, err := server.New(server.Deps{
		Cfg:         cfg,
		Log:         log,
		DB:          db,
		Settings:    settingsSvc,
		AgentStatus: agentStatus,
		SigningKey:  key,
	})
	if err != nil {
		return fmt.Errorf("装配 HTTP 服务失败: %w", err)
	}

	// ---- 12. 业务路由（W5/W6）----
	//
	// 说明：`server.Deps` 目前不含 Agent / Hub（W2/W3 的契约），
	// 因此这里**在 `server.New` 之后**往同一个 gin 引擎补挂业务路由。
	// gin 的 NoRoute 与路由树是分开的，后注册路由完全安全。
	//
	// 若后续把 Agent / Hub 收进 server.Deps，只需把下面两块搬进 registerRoutes()。
	// 复用 server.New() 里装配好的鉴权中间件（同一套 repo/jwt，避免重复实例）
	authMW := app.AuthMW
	biz, err := handler.RegisterBusiness(app.Engine().Group("/api/web/v1"), handler.BusinessDeps{
		DB:       db,
		Cfg:      cfg,
		Log:      log,
		Settings: settingsSvc,
		Cipher:   cipher,
		AuthMW:   authMW,
		Agent:    agentClient,
		Hub:      hub,
	})
	if err != nil {
		return fmt.Errorf("装配业务路由失败: %w", err)
	}

	// ---- 12.5 Lsky v1 兼容层（W9 / D52）----
	//
	// 挂在**根级 /api/v1**（与内部 /api/web/v1 前缀隔离，D80），
	// 让 PicGo 桌面端 / PicList / uPic / ShareX 直接把本站当图床。
	//
	// `integration.lsky.enabled=false` 时不注册任何路由。
	lskyEnabled := settingsSvc.GetBool("integration.lsky.enabled", true)
	lskyHandler := lsky.New(lsky.Deps{
		Cfg:        cfg,
		Log:        log,
		Settings:   settingsSvc,
		Hub:        hub,
		UsersSvc:   app.UserSvc,
		Tokens:     app.TokenSvc,
		Uploads:    biz.Upload,
		Gallery:    biz.Gallery,
		Albums:     biz.Album,
		Storage:    biz.Storage,
		Users:      app.UserRepo,
		TokenRepo:  repository.NewTokenRepo(db.DB),
		Logs:       repository.NewLogRepo(db.DB),
		UploadRepo: repository.NewUploadRepo(db.DB),
	})
	lskyHandler.Register(app.Engine(), lskyEnabled)

	// 启动自检：① 保留区无闯入 ② 契约路径全部注册（fail fast）
	if lskyEnabled {
		if conflicts := lsky.DetectConflicts(app.Engine()); len(conflicts) > 0 {
			for _, c := range conflicts {
				log.Error("Lsky 保留区被闯入", "route", c.Method+" "+c.Path)
			}
			return fmt.Errorf("检测到 %d 处路由冲突（详见上方日志）：内部 API 必须挂 /api/web/v1/**", len(conflicts))
		}
		if missing := lsky.VerifyContract(app.Engine()); len(missing) > 0 {
			// 不 fail：少了某条契约路径只影响对应功能，不该阻止整个服务启动
			log.Warn("Lsky 契约路径未全部注册（对应功能会返回 404）", "missing", missing)
		} else {
			log.Info("Lsky 契约自检通过", "endpoints", 9)
		}
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	// ---- 13. agent 健康探测 + 事件桥（后台）----
	go probeAgentLoop(runCtx, agentClient, settingsSvc, hub, agentStatus, log)
	go events.NewAgentBridge(events.AgentBridgeConfig{
		EventsURL: agentClient.EventsURL(),
		Token:     agentClient.Token(),
	}, hub, log).Run(runCtx)

	// ---- 14. 存储配置 reconcile（以 DB 为真相源投影到 agent，D22）----
	if err := biz.Storage.Reconcile(runCtx); err != nil {
		// 不阻断启动：agent 可能尚未就绪，稍后用户改配置时会再次同步
		log.Warn("存储配置 reconcile 未完全成功（将在后续变更时重试）", "err", err)
	}

	// ---- 15. 上传队列（启动恢复 + worker）----
	if err := biz.Upload.Start(runCtx); err != nil {
		return fmt.Errorf("启动上传队列失败: %w", err)
	}

	// ---- 16. 定时任务（日志清理，D74）----
	sched := scheduler.New(scheduler.Config{
		Log:      log,
		Settings: settingsSvc,
		Logs:     repository.NewLogRepo(db.DB),
		Jobs:     repository.NewJobRepo(db.DB),
		Audit:    service.NewCleanupAuditWriter(biz.Audit),
	})
	sched.Start(runCtx)
	log.Info("日志清理策略", "desc", sched.Description())

	// ---- 17. 运行 HTTP（阻塞至收到退出信号）----
	shutdownGrace := time.Duration(settingsSvc.GetInt("upload.shutdownGraceSeconds", 30)) * time.Second

	runErr := app.Run(context.Background(), shutdownGrace)

	// ---- 18. 优雅关闭后台组件 ----
	// 顺序有讲究：先停队列（等正在传的项跑完），再停定时任务，最后关 hub。
	stopCtx, stopCancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer stopCancel()
	biz.Upload.Shutdown(stopCtx)
	sched.Stop()
	// 关停 agent：先请求其优雅退出，超时再强杀
	supervisor.Stop(stopCtx)
	hub.Close()
	cancelRun()

	return runErr
}

// buildAgentClient 按配置构造 agent 客户端（真实 HTTP 或内存 mock）。
// buildAgentSupervisor 解析共享令牌并构造侧车进程管理器。
//
// 它在 `buildAgentClient` **之前**调用，因为：
//   - 令牌的最终值由 Supervisor 决定（env → 文件 → 新生成）
//   - 生成的令牌要落盘（0600），并注入给子进程
//   - Client 必须用**同一个**令牌，否则 agent 会 401
func buildAgentSupervisor(cfg *config.Config, log *slog.Logger) (*agent.Supervisor, error) {
	// configPath 必须是**绝对路径**：agent 以自身 cwd 解析相对路径，
	// 相对路径会落到 picgo-agent/ 下而不是 dataDir（踩过）。
	configPath := cfg.PicgoConfigPath()
	if abs, err := filepath.Abs(configPath); err == nil {
		configPath = abs
	}

	extra := map[string]string{
		"PICGO_AGENT_CONFIG_PATH": configPath,
	}
	if v := strings.TrimSpace(cfg.AgentNpmRegistry); v != "" {
		extra["PICGO_AGENT_NPM_REGISTRY"] = v
	}
	if v := strings.TrimSpace(cfg.AgentNpmProxy); v != "" {
		extra["PICGO_AGENT_NPM_PROXY"] = v
	}
	if v := strings.TrimSpace(cfg.AgentUploadProxy); v != "" {
		extra["PICGO_AGENT_UPLOAD_PROXY"] = v
	}

	var command []string
	if raw := strings.TrimSpace(cfg.AgentCommand); raw != "" {
		command = strings.Fields(raw)
	}

	return agent.NewSupervisor(agent.SupervisorConfig{
		Autostart: cfg.AgentAutostart,
		BaseURL:   cfg.AgentURL,
		Token:     cfg.AgentToken,
		TokenFile: cfg.AgentTokenFile(),
		AgentDir:  cfg.AgentDir,
		Command:   command,
		ExtraEnv:  extra,
		Log:       log,
	})
}

// buildAgentClient 构造 agent HTTP 客户端。
//
// token 必须来自 Supervisor（保证与子进程一致）。
func buildAgentClient(cfg *config.Config, settingsSvc *settings.Service, token string, log *slog.Logger) (agent.Client, error) {
	if cfg.AgentMock {
		log.Warn("agent 使用 MOCK 模式（仅用于联调/测试，不会真正上传）")
		return agent.NewMock(agent.MockConfig{
			BaseURL: cfg.AgentURL,
			Token:   token,
			Log:     log,
			TempDir: cfg.PicgoConfigDir(),
		}), nil
	}

	return agent.New(agent.Config{
		BaseURL:        cfg.AgentURL,
		Token:          token,
		Log:            log,
		RequestTimeout: 30 * time.Second,
		// 上传超时每次现取，使 `upload.itemTimeoutSeconds` 的改动即时生效
		UploadTimeoutFn: func() time.Duration {
			sec := settingsSvc.GetInt("upload.itemTimeoutSeconds", 300)
			if sec <= 0 {
				sec = 300
			}
			return time.Duration(sec) * time.Second
		},
	}), nil
}

// jwtMgrForRun 复用鉴权所需的 JWT 管理器（与 W3 的接线保持同一密钥来源）。
func jwtMgrForRun(signingKey []byte) *auth.JWTManager {
	return auth.NewJWTManager(signingKey)
}

// probeAgentLoop 周期性探测 agent 健康，并在状态**发生变化**时发系统通知。
//
// 只报「变化」：每 30 秒推一次「内核正常」会变成噪音。
// agentStatusProbeFastWindow 是「启动快速探测期」。
//
// 为什么要它：agent 子进程启动需要 1~3 秒（Node 冷启动），
// 而稳定期的探测间隔是 30 秒。若第一次探测恰好在就绪前，前端就要等 30 秒
// 才看到「内核已就绪」。前 60 秒用 2 秒间隔可以把这个误报窗口压到最小。
const agentStatusProbeFastWindow = 60 * time.Second

// agentStatusProbeFastInterval / agentStatusProbeInterval 见上方说明。
const (
	agentStatusProbeFastInterval = 2 * time.Second
	agentStatusProbeInterval     = 30 * time.Second
)

func probeAgentLoop(
	ctx context.Context,
	ag agent.Client,
	settingsSvc *settings.Service,
	hub *events.Hub,
	status *agent.StatusHolder,
	log *slog.Logger,
) {
	startedAtProbe := time.Now()
	ticker := time.NewTicker(agentStatusProbeFastInterval)
	defer ticker.Stop()

	wasUp := false
	check := func() {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()

		// Refresh 同时更新缓存快照（/healthz 与 /system/info 读它）
		snap := status.Refresh(probeCtx, ag)
		up := snap.Up

		switch {
		case up && !wasUp:
			log.Info("picgo-agent 已就绪",
				"version", snap.Version, "plugins", snap.PluginCount)
			if hub != nil {
				hub.PublishNotice("info", "内核已就绪")
			}
		case !up && wasUp:
			log.Warn("picgo-agent 不可用", "err", snap.Error)
			if hub != nil {
				hub.PublishNotice("error", "内核不可用：上传与插件功能暂时失效")
			}
		case !up && !wasUp:
			log.Debug("picgo-agent 仍不可用", "err", snap.Error)
		}
		wasUp = up
	}

	check()
	for {
		// 快速期结束 → 换成 30s 间隔（省资源；此时 agent 状态已稳定）
		if time.Since(startedAtProbe) > agentStatusProbeFastWindow {
			ticker.Reset(agentStatusProbeInterval)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}
