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
	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
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

	// ---- 9. picgo-agent 客户端（W4）----
	//
	// `PICGO_WEB_AGENT_MOCK=true` 时用内存 mock（无 Node 环境也能跑通全链路：
	// 前端联调、CI、以及本项目的端到端自测）。
	agentClient, err := buildAgentClient(cfg, settingsSvc, log)
	if err != nil {
		return err
	}

	// ---- 10. 事件总线（W5）----
	//
	// Hub 是进程内广播；agent 的 SSE 由 events.AgentBridge 桥接进来 ——
	// 但**只桥接不可归属的事件类型**（system.notice / ping / job.log）。
	// 上传进度由 Go 自己发（带 UserUID 才能正确按用户投递），因此不上桥。
	hub := events.New(log)

	// ---- 11. HTTP 服务 ----
	app, err := server.New(server.Deps{
		Cfg:        cfg,
		Log:        log,
		DB:         db,
		Settings:   settingsSvc,
		SigningKey: key,
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
	authMW := middleware.NewAuth(userRepo, repository.NewTokenRepo(db.DB), jwtMgrForRun(key), log)
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

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	// ---- 13. agent 健康探测 + 事件桥（后台）----
	go probeAgentLoop(runCtx, agentClient, settingsSvc, hub, log)
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
	hub.Close()
	cancelRun()

	return runErr
}

// buildAgentClient 按配置构造 agent 客户端（真实 HTTP 或内存 mock）。
func buildAgentClient(cfg *config.Config, settingsSvc *settings.Service, log *slog.Logger) (agent.Client, error) {
	if cfg.AgentMock {
		log.Warn("agent 使用 MOCK 模式（仅用于联调/测试，不会真正上传）")
		return agent.NewMock(agent.MockConfig{
			BaseURL: cfg.AgentURL,
			Token:   cfg.AgentToken,
			Log:     log,
			TempDir: cfg.PicgoConfigDir(),
		}), nil
	}

	if strings.TrimSpace(cfg.AgentToken) == "" {
		log.Warn("PICGO_WEB_AGENT_TOKEN 为空：agent 会拒绝所有请求（请检查侧车配置）")
	}

	return agent.New(agent.Config{
		BaseURL:        cfg.AgentURL,
		Token:          cfg.AgentToken,
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
func probeAgentLoop(ctx context.Context, ag agent.Client, settingsSvc *settings.Service, hub *events.Hub, log *slog.Logger) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	wasUp := false
	check := func() {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()

		_, err := ag.Healthz(probeCtx)
		up := err == nil
		switch {
		case up && !wasUp:
			log.Info("picgo-agent 已就绪")
			if hub != nil && wasUp != up {
				hub.PublishNotice("info", "内核已就绪")
			}
		case !up && wasUp:
			log.Warn("picgo-agent 不可用", "err", err)
			if hub != nil {
				hub.PublishNotice("error", "内核不可用：上传与插件功能暂时失效")
			}
		case !up && !wasUp:
			log.Debug("picgo-agent 仍不可用", "err", err)
		}
		wasUp = up
	}

	check()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}
