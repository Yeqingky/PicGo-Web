// Package server 负责 HTTP 服务的装配与生命周期。
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/agent"
	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/database"
	"github.com/YeqingKy/PicGo-Web/server/internal/handler"
	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
	"github.com/YeqingKy/PicGo-Web/server/internal/theme"
	"github.com/YeqingKy/PicGo-Web/server/internal/webfs"
)

// Version 是程序版本。发布时与 schema 版本一起记入 CHANGELOG。
const Version = "0.1.0"

// Deps 是 HTTP 层需要的依赖集合。
//
// 后续工作流（W5）会往这里加 agent 客户端等字段；
// 加字段不改变 Run 的签名，便于逐步扩展。
type Deps struct {
	Cfg      *config.Config
	Log      *slog.Logger
	DB       *database.DB
	Settings *settings.Service

	// AgentStatus 是 picgo-agent 的**缓存**健康状态（可为 nil，此时一律视为不可用）。
	//
	// 由后台探测协程刷新；handler 只读内存快照，
	// 从而让 /healthz 保持「不查库、不调 agent」的轻量语义（docs/API.md §10）。
	AgentStatus *agent.StatusHolder

	// SigningKey 是加密主密钥（D19）。
	//
	// 用于派生 JWT 签名密钥与 OAuth state 签名密钥，
	// **不另存 secret**：主密钥是唯一秘密源，轮换它即轮换所有派生密钥。
	SigningKey []byte
}

// App 持有 HTTP 服务与装配好的路由。
type App struct {
	deps   Deps
	engine *gin.Engine
	http   *http.Server
}

// New 装配路由。
func New(deps Deps) (*App, error) {
	if deps.Cfg == nil || deps.Log == nil || deps.DB == nil {
		return nil, errors.New("server: Deps 缺少必填项（Cfg / Log / DB）")
	}

	engine := gin.New()

	// 可信代理：默认**不信任**任何代理头，避免伪造 IP 绕过登录限流。
	// 仅当显式开启 PICGO_WEB_TRUST_PROXY 时才信任（容器/反代场景）。
	if deps.Cfg.TrustProxy {
		if err := engine.SetTrustedProxies(nil); err != nil {
			return nil, fmt.Errorf("设置可信代理失败: %w", err)
		}
	} else {
		if err := engine.SetTrustedProxies([]string{}); err != nil {
			return nil, fmt.Errorf("关闭可信代理失败: %w", err)
		}
	}

	// 全局中间件（顺序重要：RequestID 必须在最前，Recovery 要能记录 ID）
	engine.Use(
		middleware.RequestID(),
		middleware.Recovery(deps.Log),
		middleware.SecurityHeaders(),
		middleware.AccessLog(deps.Log, "/healthz", "/favicon.ico"),
	)

	app := &App{deps: deps, engine: engine}
	if err := app.registerRoutes(); err != nil {
		return nil, err
	}
	return app, nil
}

// Engine 暴露 gin 引擎（供 W3 继续注册路由与测试使用）。
func (a *App) Engine() *gin.Engine { return a.engine }

// registerRoutes 装配依赖并注册路由。
//
// 路由前缀约定（D80）：
//   - `/api/web/v1/**`  PicGo-Web 内部 API
//   - `/api/v1/**`      Lsky 兼容层（W9 注册，**本包不得占用**）
//   - `/healthz`        健康检查（无信封、字段小写，供容器探针）
//
// 依赖的装配（repository → service → handler → middleware）放在这里，
// 这样部署入口 main.go 只需构造基础依赖（config/db/settings/key）。
func (a *App) registerRoutes() error {
	if len(a.deps.SigningKey) == 0 {
		return errors.New("server: Deps.SigningKey 为空，无法派生 JWT / OAuth state 签名密钥")
	}

	gdb := a.deps.DB.DB

	// ---- 数据访问层 ----
	userRepo := repository.NewUserRepo(gdb)
	tokenRepo := repository.NewTokenRepo(gdb)
	attemptRepo := repository.NewLoginAttemptRepo(gdb)
	logRepo := repository.NewLogRepo(gdb)

	// ---- 密码学原语 ----
	jwtMgr := auth.NewJWTManager(a.deps.SigningKey)
	stateSigner := auth.NewStateSigner(a.deps.SigningKey)

	// ---- 业务服务层 ----
	auditSvc := service.NewAuditService(logRepo, a.deps.Log)
	tokenSvc := service.NewTokenService(a.deps.Settings, userRepo, tokenRepo, jwtMgr, auditSvc, a.deps.Log)
	userSvc := service.NewUserService(a.deps.Settings, userRepo, tokenRepo, attemptRepo, auditSvc, a.deps.Log)
	oauthSvc := service.NewOAuthService(a.deps.Settings, userRepo, stateSigner, auditSvc, a.deps.Log)

	// ---- 鉴权中间件 ----
	authMW := middleware.NewAuth(userRepo, tokenRepo, jwtMgr, a.deps.Log)

	// ---- 健康检查（不使用信封、字段小写）----
	a.engine.GET("/healthz", a.handleHealthz)

	api := a.engine.Group("/api/web/v1")
	api.GET("/system/info", a.handleSystemInfo)

	// ---- 认证 / OAuth / API Token ----
	handler.NewAuthHandler(tokenSvc, userSvc, a.deps.Settings, a.deps.Log).Register(api, authMW)
	handler.NewOAuthHandler(oauthSvc, tokenSvc, a.deps.Cfg, a.deps.Log).Register(api, authMW)

	// ---- 用户管理（全部要求 admin；且必须改密的账号不得进入，D32）----
	usersGroup := api.Group("/users",
		authMW.RequireAuth(),
		authMW.RequirePasswordChanged(),
		authMW.RequireAdmin(),
	)
	handler.NewUserHandler(userSvc, a.deps.Log).Register(usersGroup)

	// ---- 主题系统（D94–D99）----
	//
	// 模型：内置 SPA（webfs，go:embed web/dist）提供**全部页面**的默认实现；
	// 主题是可选的页面覆盖层，通过 manifest.Pages 自行注册要接管的页面。
	// 因此这里要做三件事：① 造主题服务并 seed；② 注册主题的 API；
	// ③ 注册静态资源与页面分发（原 handleNoRoute 已被真正的分发取代）。
	//
	// 注意：路由注册代码放在 theme 包内（垂直切片），server.go 只负责
	// 注入依赖与鉴权中间件。
	themeSvc, err := theme.New(theme.Options{
		ThemesDir: a.deps.Cfg.ThemesDir,
		SeedFrom:  a.deps.Cfg.ThemeSeedFrom,
		DB:        gdb,
		Settings:  a.deps.Settings,
		Log:       a.deps.Log,
		Auditor:   themeAuditor{svc: auditSvc},
	})
	if err != nil {
		return fmt.Errorf("初始化主题系统失败: %w", err)
	}

	// 首启 seed：主题目录为空时才写出内嵌默认主题（升级不覆盖用户主题，D94.4）。
	// seed 失败不阻断启动 —— 运行时还有内嵌兜底，首页不会白屏。
	if seeded, seedErr := themeSvc.Seed(); seedErr != nil {
		a.deps.Log.Warn("主题目录 seed 失败（不影响启动，将使用内嵌兜底）", "err", seedErr)
	} else if seeded {
		a.deps.Log.Info("已从内嵌默认主题完成种子写入", "dir", a.deps.Cfg.ThemesDir)
	}

	// 公开站点信息（免鉴权，前端首屏调用一次）
	themeSvc.RegisterSiteRoutes(api, theme.SiteRoutesOptions{Version: Version})

	// 后台主题管理（全部需要 admin；必须改密的账号不得进入）
	themeSvc.RegisterAdminRoutes(api, theme.AdminRoutesOptions{
		Middlewares: []gin.HandlerFunc{
			authMW.RequireAuth(),
			authMW.RequirePasswordChanged(),
			authMW.RequireAdmin(),
		},
	})

	// ---- 静态资源与页面分发（D99）----
	//
	// 两个资源前缀必须严格分离：
	//   /assets/**        永远属于**内置 SPA**（主题不得占用，否则会覆盖登录页与后台的资源）
	//   /theme-assets/**  属于**当前主题**的 assets/（带路径穿越防护）
	a.engine.GET("/assets/*filepath", webfs.ServeAssets())
	themeSvc.RegisterAssetRoutes(a.engine)

	// /themes/** 一律 404：不暴露主题目录、manifest.json 与主题源码（D99.2）。
	// 必须显式注册，否则会被 NoRoute 当成页面而回退成 index.html。
	a.engine.GET("/themes/*filepath", func(c *gin.Context) {
		response.Fail(c, response.CodeNotFound)
	})

	// 未匹配的路径交给分发器：认证页/后台 → 内置 SPA；命中主题 Pages → 主题；
	// 其余 → 内置 SPA（SPA 回退）。`/api/**` 仍未匹配则返回 40401 JSON。
	a.engine.NoRoute(theme.ServePage(themeSvc))
	return nil
}

// themeAuditor 把 *service.AuditService 适配成 theme.Auditor。
//
// 用适配器而不是让 theme 包 import service：主题模块不该依赖业务服务包，
// 这样主题系统的编译与测试也能独立于业务层。
type themeAuditor struct{ svc *service.AuditService }

func (a themeAuditor) Log(ctx context.Context, e theme.AuditEntry) {
	if a.svc == nil {
		return
	}
	a.svc.Log(ctx, service.AuditEntry{
		Type:       e.Type,
		Status:     e.Status,
		TargetType: e.TargetType,
		TargetUID:  e.TargetUID,
		Detail:     e.Detail,
		Cause:      e.Cause,
	})
}

// handleHealthz 是唯一不使用统一信封的端点（容器探针）。
func (a *App) handleHealthz(c *gin.Context) {
	// 注意：这里**不调 agent**（healthz 必须轻量：容器与前端都会高频轮询），
	// 只读后台探测协程刷新的内存快照。
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"version": Version,
		"agent":   a.deps.AgentStatus.Get().Label(),
		"uptime":  int64(time.Since(startedAt).Seconds()),
	})
}

// handleSystemInfo 返回版本与运行信息。
func (a *App) handleSystemInfo(c *gin.Context) {
	st := a.deps.AgentStatus.Get()

	// Picgo 段：仅在 agent 可用时才有意义；不可用时给出 LastError 便于排查
	picgo := gin.H{}
	switch {
	case st.Up:
		picgo = gin.H{
			"Version":     st.Version,
			"ConfigPath":  st.ConfigPath,
			"PluginCount": st.PluginCount,
			"PID":         st.PID,
		}
	case st.Error != "":
		picgo = gin.H{"LastError": st.Error}
	}

	response.OK(c, gin.H{
		"Version":        Version,
		"SchemaVersion":  database.SchemaVersion,
		"DatabaseDriver": string(a.deps.DB.Driver),
		// AgentStatus：up | down（前端据此显示「内核不可用」提示）
		"AgentStatus":    st.Label(),
		"AgentCheckedAt": st.CheckedAt,
		"Picgo":          picgo,
		"SiteName":       a.deps.Settings.GetString("site.name"),
		"ThemeActive":    a.deps.Settings.GetString("theme.active"),
		"Uptime":         int64(time.Since(startedAt).Seconds()),
	})
}

var startedAt = time.Now()

// Run 启动 HTTP 服务并阻塞直到收到退出信号。
//
// 优雅关闭：
//  1. 收到 SIGINT / SIGTERM → 停止接受新连接（http.Server.Shutdown）
//  2. 等待在途请求完成，最多 shutdownGrace
//  3. 超时则强制关闭
//
// 上传任务队列的优雅关闭由 W5 的队列负责（这里只处理 HTTP 层）。
func (a *App) Run(ctx context.Context, shutdownGrace time.Duration) error {
	a.http = &http.Server{
		Addr:              a.deps.Cfg.Listen,
		Handler:           a.engine,
		ReadHeaderTimeout: 15 * time.Second,
		// 不设 WriteTimeout：上传/SSE 是长连接，由各 handler 自己控制
		IdleTimeout: 120 * time.Second,
	}

	// 监听退出信号
	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		a.deps.Log.Info("HTTP 服务已启动",
			"listen", a.deps.Cfg.Listen,
			"version", Version,
			"driver", string(a.deps.DB.Driver),
		)
		if err := a.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("HTTP 服务异常退出: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-sigCtx.Done():
		a.deps.Log.Info("收到退出信号，开始优雅关闭", "grace", shutdownGrace.String())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()

	if err := a.http.Shutdown(shutdownCtx); err != nil {
		a.deps.Log.Warn("优雅关闭超时，强制关闭", "err", err)
		if closeErr := a.http.Close(); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) {
			return fmt.Errorf("强制关闭失败: %w", closeErr)
		}
	}

	// 关闭数据库连接
	if sqlDB, err := a.deps.DB.DB.DB(); err == nil {
		if err := sqlDB.Close(); err != nil {
			a.deps.Log.Warn("关闭数据库连接失败", "err", err)
		}
	}

	a.deps.Log.Info("服务已停止")
	return nil
}

// IsDevMode 判断是否开发模式（影响 gin 的运行模式与日志格式）。
func IsDevMode(cfg *config.Config) bool {
	if cfg.DevMode {
		return true
	}
	return os.Getenv("GIN_MODE") == gin.DebugMode
}
