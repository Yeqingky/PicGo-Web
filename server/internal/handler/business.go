package handler

import (
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/agent"
	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/crypto"
	"github.com/YeqingKy/PicGo-Web/server/internal/database"
	"github.com/YeqingKy/PicGo-Web/server/internal/events"
	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// BusinessDeps 是业务层（W5/W6）的路由接线依赖。
//
// 由 `internal/server` 在 `registerRoutes()` 里构造并调用 `RegisterBusiness`。
// 这样 W5/W6 的多个子模块（存储 / 上传 / 图库 / 相册 / 任务 / 日志 / 邮件 / 插件）
// 只需改动本文件，`server.go` 只有一个调用点。
type BusinessDeps struct {
	DB       *database.DB
	Cfg      *config.Config
	Log      *slog.Logger
	Settings *settings.Service
	Cipher   *crypto.Cipher
	AuthMW   *middleware.Auth

	// Agent 是 picgo-agent 客户端（真实 HTTP 客户端或 mock，见 agent.New / agent.NewMock）。
	Agent agent.Client
	// Hub 是进程内 SSE 事件总线（events.New(nil) 即可）。
	Hub *events.Hub
}

// BusinessServices 是 RegisterBusiness 构造出的服务集合。
//
// 返回它是为了让 **main.go 能启动上传队列与定时任务**：
// 队列需要 Start(ctx)，定时任务需要 Start(ctx) —— 这两件事不属于路由接线。
type BusinessServices struct {
	Storage *service.StorageService
	Upload  *service.UploadService
	Gallery *service.GalleryService
	Album   *service.AlbumService
	Job     *service.JobService
	Log     *service.LogService
	Email   *service.EmailService
	Plugin  *service.PluginService
	Audit   *service.AuditService
}

// RegisterBusiness 挂载 W5/W6 的全部路由到 api（即 `/api/web/v1`）。
//
// 路由分组与权限（D80 + docs/API.md）：
//
//	/storage/**    admin（D4：存储驱动只有管理员能配置）
//	/uploads/**    需登录（普通用户管自己的图）
//	/albums/**     需登录
//	/jobs/**       需登录（Scope=all 仅管理员）
//	/events        需登录（SSE；允许 ?Token=）
//	/logs/**       admin（全站操作轨迹）
//	/plugins/**    admin（插件 = 服务器上的任意代码）
//	/settings/mail/test  admin
//	/auth/forgot-password、/auth/reset-password  **免鉴权**
//
// 返回 BusinessServices 供 main.go 启动后台组件。
func RegisterBusiness(api *gin.RouterGroup, d BusinessDeps) (*BusinessServices, error) {
	if d.DB == nil || d.Cfg == nil || d.Log == nil || d.Settings == nil {
		return nil, service.Errorf(response.CodeInternal, "RegisterBusiness: Deps 缺少必填项（DB / Cfg / Log / Settings）")
	}
	if d.Agent == nil {
		return nil, service.Errorf(response.CodeInternal, "RegisterBusiness: Deps.Agent 为空（需要 agent.New 或 agent.NewMock）")
	}

	gdb := d.DB.DB

	// ---- 数据访问层 ----
	userRepo := repository.NewUserRepo(gdb)
	tokenRepo := repository.NewTokenRepo(gdb)
	logRepo := repository.NewLogRepo(gdb)
	emailLogRepo := repository.NewEmailLogRepo(gdb)
	settingRepo := repository.NewSettingRepo(gdb)
	storageRepo := repository.NewStorageConfigRepo(gdb)
	uploadRepo := repository.NewUploadRepo(gdb)
	albumRepo := repository.NewAlbumRepo(gdb)
	jobRepo := repository.NewJobRepo(gdb)

	// ---- 审计（W3 已有写入侧）----
	auditSvc := service.NewAuditService(logRepo, d.Log)

	// ---- 服务层 ----
	storageSvc := service.NewStorageService(d.Cfg, d.Log, d.Settings, d.Cipher, storageRepo, d.Agent, auditSvc)

	uploadSvc := service.NewUploadService(
		d.Cfg, d.Log, d.Settings,
		uploadRepo, jobRepo, userRepo, albumRepo,
		storageSvc, d.Agent, d.Hub, auditSvc,
	)

	gallerySvc := service.NewGalleryService(
		d.Cfg, d.Log, d.Settings,
		uploadRepo, albumRepo, userRepo, storageSvc, d.Agent, d.Hub, auditSvc,
	)

	albumSvc := service.NewAlbumService(d.Log, albumRepo, uploadRepo, auditSvc)
	jobSvc := service.NewJobService(d.Log, jobRepo, uploadRepo, auditSvc)
	logSvc := service.NewLogService(d.Log, logRepo, emailLogRepo)
	emailSvc := service.NewEmailService(
		d.Cfg, d.Log, d.Settings, userRepo, tokenRepo, settingRepo, emailLogRepo, auditSvc,
	)
	pluginSvc := service.NewPluginService(d.Log, d.Agent, d.Hub, auditSvc)

	// 让 storage service 能在「已无可用驱动」时发系统通知（解耦：用回调注入 Hub）
	if d.Hub != nil {
		storageSvc.SetNotice(func(level, message string) {
			d.Hub.PublishNotice(level, message)
		})
	}

	// ---- 路由 ----
	requireLogin := []gin.HandlerFunc{
		d.AuthMW.RequireAuth(),
		d.AuthMW.RequirePasswordChanged(),
	}
	requireAdmin := append(append([]gin.HandlerFunc{}, requireLogin...), d.AuthMW.RequireAdmin())

	// 免鉴权：找回密码（未登录的用户的唯一自救通道）
	emailHandler := NewEmailHandler(emailSvc, d.Log)
	emailHandler.RegisterPublic(api)

	// 存储驱动（admin）
	NewStorageHandler(storageSvc, d.Log).Register(api.Group("/storage", requireAdmin...))

	// 图库（需登录）
	NewGalleryHandler(gallerySvc, uploadSvc, d.Log).Register(api.Group("/uploads", requireLogin...))

	// 相册（需登录）
	NewAlbumHandler(albumSvc, d.Log).Register(api.Group("/albums", requireLogin...))

	// 任务（需登录）
	NewJobHandler(jobSvc, d.Hub, d.Log).Register(api.Group("/jobs", requireLogin...))

	// SSE：需登录；额外允许 `?Token=` 传令牌（EventSource 无法自定义请求头）
	api.GET("/events",
		PromoteQueryToken(),
		d.AuthMW.RequireAuth(),
		d.AuthMW.RequirePasswordChanged(),
		NewJobHandler(jobSvc, d.Hub, d.Log).Events,
	)

	// 操作日志（admin）
	NewLogHandler(logSvc, d.Log).Register(api.Group("/logs", requireAdmin...))

	// 插件（admin）
	NewPluginHandler(pluginSvc, d.Log).Register(api.Group("/plugins", requireAdmin...))

	// 邮件测试（admin）
	emailHandler.RegisterAdmin(api.Group("", requireAdmin...))

	return &BusinessServices{
		Storage: storageSvc,
		Upload:  uploadSvc,
		Gallery: gallerySvc,
		Album:   albumSvc,
		Job:     jobSvc,
		Log:     logSvc,
		Email:   emailSvc,
		Plugin:  pluginSvc,
		Audit:   auditSvc,
	}, nil
}

// PromoteQueryToken 把 `?Token=<jwt|pcw_...>` 提升为 `Authorization: Bearer ...` 头。
//
// 用途：**仅 SSE**（`GET /events`）。浏览器的 `EventSource` 无法自定义请求头，
// 因此「非浏览器客户端（CLI / 脚本）订阅事件」只能靠 query 传令牌。
//
// 安全：docs/API.md §8.1 明确「**仅此场景允许 query 传令牌**」。
// 之所以可接受：SSE 只推送该用户自己的任务进度，且令牌在日志里会被 accesslog 的
// path 记录 —— 因此**只对本中间件挂载的那条路由生效**，不要复用。
func PromoteQueryToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 已有 Authorization 时不覆盖（Bearer 优先）
		if strings.TrimSpace(c.GetHeader("Authorization")) == "" {
			if token := strings.TrimSpace(c.Query("Token")); token != "" {
				c.Request.Header.Set("Authorization", "Bearer "+token)
			}
		}
		c.Next()
	}
}
