// Package handler 是 HTTP 处理层。
//
// 分层铁律（D77.2）：handler 只负责
//   - 解析与校验入参
//   - 调用 service
//   - 写统一响应 {Code, Message, Data}
//
// **handler 里不得出现 *gorm.DB**。
package handler

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// AuthHandler 处理认证相关端点（/auth/** 与 /settings/api-tokens/**）。
//
// api-token 端点虽然路径在 /settings 下，但同属「鉴权域」，
// 放同一 handler 有利于 Cookie / 令牌逻辑内聚。
type AuthHandler struct {
	tokens   *service.TokenService
	users    *service.UserService
	settings *settings.Service
	log      *slog.Logger
}

// NewAuthHandler 构造。
func NewAuthHandler(
	tokens *service.TokenService,
	users *service.UserService,
	st *settings.Service,
	log *slog.Logger,
) *AuthHandler {
	return &AuthHandler{tokens: tokens, users: users, settings: st, log: log}
}

// Register 注册本 handler 的全部路由。
func (h *AuthHandler) Register(g *gin.RouterGroup, mw *middleware.Auth) {
	// ---- 公开 ----
	g.POST("/auth/login", h.Login)
	g.POST("/auth/refresh", h.Refresh)

	// ---- 需登录 ----
	// ⚠️ 这三条**不挂** RequirePasswordChanged：必须改密的用户要能查自己、改密、登出
	g.POST("/auth/logout", mw.RequireAuth(), h.Logout)
	g.GET("/auth/me", mw.RequireAuth(), h.Me)
	g.PATCH("/auth/password", mw.RequireAuth(), h.ChangePassword)
	g.GET("/auth/identities", mw.RequireAuth(), h.Identities)

	// ---- API Token（个人设置，但同属鉴权域）----
	// 必须改密的用户不允许创建长期令牌，故挂 RequirePasswordChanged
	g.GET("/settings/api-tokens", mw.RequireAuth(), mw.RequirePasswordChanged(), h.ListAPITokens)
	g.POST("/settings/api-tokens", mw.RequireAuth(), mw.RequirePasswordChanged(), h.CreateAPIToken)
	g.DELETE("/settings/api-tokens/:UID", mw.RequireAuth(), mw.RequirePasswordChanged(), h.DeleteAPIToken)
}

// Login 处理 POST /api/web/v1/auth/login。
func (h *AuthHandler) Login(c *gin.Context) {
	var req struct {
		Email    string `json:"Email"`
		Password string `json:"Password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	clientIP := middleware.ClientIP(c)
	userAgent := c.Request.UserAgent()

	result, err := h.users.Login(c.Request.Context(), req.Email, req.Password, clientIP, userAgent)
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}

	issued, err := h.tokens.Issue(c.Request.Context(), result.User, clientIP, userAgent)
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}

	auth.SetAuthCookies(c, issued.AccessToken, issued.AccessTTL,
		issued.RefreshToken, issued.RefreshTTL, h.tokens.SecureCookies())

	view, err := h.users.Get(result.User.UID)
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}

	response.OK(c, gin.H{
		"AccessToken": issued.AccessToken,
		"ExpiresIn":   issued.ExpiresIn,
		"User":        view,
	})
}

// Refresh 处理 POST /api/web/v1/auth/refresh（用 pcw_rt Cookie 轮换）。
func (h *AuthHandler) Refresh(c *gin.Context) {
	refresh := auth.RefreshCookie(c)
	if refresh == "" {
		// 也允许 body 里带 RefreshToken，方便非浏览器客户端（CLI/脚本）
		var req struct {
			RefreshToken string `json:"RefreshToken"`
		}
		if err := c.ShouldBindJSON(&req); err == nil {
			refresh = req.RefreshToken
		}
	}

	issued, _, err := h.tokens.Refresh(c.Request.Context(), refresh,
		middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		// 刷新失败必须清掉 Cookie，避免前端拿着坏凭据反复重试
		auth.ClearAuthCookies(c, h.tokens.SecureCookies())
		respondServiceError(c, h.log, err)
		return
	}

	auth.SetAuthCookies(c, issued.AccessToken, issued.AccessTTL,
		issued.RefreshToken, issued.RefreshTTL, h.tokens.SecureCookies())

	response.OK(c, gin.H{
		"AccessToken": issued.AccessToken,
		"ExpiresIn":   issued.ExpiresIn,
	})
}

// Logout 处理 POST /api/web/v1/auth/logout。
func (h *AuthHandler) Logout(c *gin.Context) {
	refresh := auth.RefreshCookie(c)
	if refresh == "" {
		var req struct {
			RefreshToken string `json:"RefreshToken"`
		}
		_ = c.ShouldBindJSON(&req)
		if req.RefreshToken != "" {
			refresh = req.RefreshToken
		}
	}

	h.tokens.Logout(c.Request.Context(), refresh,
		middleware.ClientIP(c), c.Request.UserAgent())
	auth.ClearAuthCookies(c, h.tokens.SecureCookies())

	response.OK(c, nil)
}

// Me 处理 GET /api/web/v1/auth/me。
func (h *AuthHandler) Me(c *gin.Context) {
	uid := middleware.CurrentUserUID(c)

	view, err := h.users.Get(uid)
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}

	identities, hasPassword, err := h.users.Identities(uid)
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	// HasPassword 以 Identities 的判定为准（它直接读 PasswordHash）
	view.HasPassword = hasPassword

	// 展开到同一层：前端拿到 /me 即可渲染「账号绑定」区块，无需二次请求
	response.OK(c, gin.H{
		"UID":                view.UID,
		"Email":              view.Email,
		"Role":               view.Role,
		"Status":             view.Status,
		"MustChangePassword": view.MustChangePassword,
		"Nickname":           view.Nickname,
		"AvatarURL":          view.AvatarURL,
		"Homepage":           view.Homepage,
		"CapacityBytes":      view.CapacityBytes,
		"UsedBytes":          view.UsedBytes,
		"ImageCount":         view.ImageCount,
		"HasPassword":        view.HasPassword,
		"Identities":         identities,
		"LastLoginAt":        view.LastLoginAt,
		"CreatedAt":          view.CreatedAt,
		"UpdatedAt":          view.UpdatedAt,
	})
}

// ChangePassword 处理 PATCH /api/web/v1/auth/password。
func (h *AuthHandler) ChangePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"OldPassword"`
		NewPassword string `json:"NewPassword"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	uid := middleware.CurrentUserUID(c)
	err := h.users.ChangePassword(c.Request.Context(), uid, req.OldPassword, req.NewPassword,
		middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}

	// 改密会吊销全部 refresh token（D24 语义：强制重新登录），因此同步清 Cookie
	auth.ClearAuthCookies(c, h.tokens.SecureCookies())
	response.OK(c, nil)
}

// Identities 处理 GET /api/web/v1/auth/identities。
func (h *AuthHandler) Identities(c *gin.Context) {
	uid := middleware.CurrentUserUID(c)

	identities, hasPassword, err := h.users.Identities(uid)
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, gin.H{
		"HasPassword": hasPassword,
		"Identities":  identities,
	})
}

// ListAPITokens 处理 GET /api/web/v1/settings/api-tokens。
func (h *AuthHandler) ListAPITokens(c *gin.Context) {
	items, err := h.tokens.ListAPITokens(middleware.CurrentUserUID(c))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, gin.H{"Items": items})
}

// CreateAPIToken 处理 POST /api/web/v1/settings/api-tokens。
func (h *AuthHandler) CreateAPIToken(c *gin.Context) {
	var req struct {
		Name          string `json:"Name"`
		ExpiresInDays int    `json:"ExpiresInDays"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	created, err := h.tokens.CreateAPIToken(c.Request.Context(),
		middleware.CurrentUserUID(c), req.Name, req.ExpiresInDays,
		middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, created)
}

// DeleteAPIToken 处理 DELETE /api/web/v1/settings/api-tokens/:UID。
func (h *AuthHandler) DeleteAPIToken(c *gin.Context) {
	err := h.tokens.DeleteAPIToken(c.Request.Context(),
		middleware.CurrentUserUID(c), c.Param("UID"),
		middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, nil)
}

// respondServiceError 把 service 层错误映射成统一响应。
//
// 规则：
//   - 业务错误（*service.Error）→ 用其错误码与消息
//   - 其它错误 → 一律 50001 + 通用文案（**不泄露内部细节**），并写进程日志
func respondServiceError(c *gin.Context, log *slog.Logger, err error) {
	code := service.CodeOf(err)
	if code == response.CodeOK || code == response.CodeInternal {
		if code == response.CodeInternal {
			log.Error("请求处理失败",
				"path", c.Request.URL.Path,
				"method", c.Request.Method,
				"err", err,
			)
		}
		response.Fail(c, response.CodeInternal)
		return
	}
	response.FailMsg(c, code, service.MessageOf(err))
}
