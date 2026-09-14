package handler

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
)

// OAuthHandler 处理 GitHub OAuth 登录与绑定（D26/D27/D28）。
type OAuthHandler struct {
	oauth  *service.OAuthService
	tokens *service.TokenService
	cfg    *config.Config
	log    *slog.Logger
}

// NewOAuthHandler 构造。
func NewOAuthHandler(oauth *service.OAuthService, tokens *service.TokenService, cfg *config.Config, log *slog.Logger) *OAuthHandler {
	return &OAuthHandler{oauth: oauth, tokens: tokens, cfg: cfg, log: log}
}

// Register 注册路由。
func (h *OAuthHandler) Register(g *gin.RouterGroup, mw *middleware.Auth) {
	g.GET("/auth/oauth/providers", h.Providers)

	// start：同时提供 GET 与 POST。
	// 契约（API.md §1）只写了 POST；额外提供 GET 是为了让前端可以直接
	// `window.location.href = "/api/web/v1/auth/oauth/github/start?Redirect=/..."`，
	// 免去 fetch + 手动跟随 302 的麻烦（不改变既有契约）。
	g.GET("/auth/oauth/github/start", h.Start)
	g.POST("/auth/oauth/github/start", h.Start)
	g.GET("/auth/oauth/github/callback", h.Callback)

	g.POST("/auth/oauth/github/bind", mw.RequireAuth(), mw.RequirePasswordChanged(), h.Bind)
	g.DELETE("/auth/oauth/github", mw.RequireAuth(), mw.RequirePasswordChanged(), h.Unbind)
}

// Providers 处理 GET /api/web/v1/auth/oauth/providers。
//
// 未配置的 provider 不出现在列表里，前端据此决定是否显示第三方登录按钮。
func (h *OAuthHandler) Providers(c *gin.Context) {
	response.OK(c, gin.H{"Providers": h.oauth.Providers()})
}

// Start 处理 /api/web/v1/auth/oauth/github/start。
//
// 302 到 GitHub 授权页；state 用 HMAC 签名并携带 Redirect（防 CSRF + 开放重定向）。
func (h *OAuthHandler) Start(c *gin.Context) {
	redirect := c.Query("Redirect")
	origin := requestOrigin(c, h.cfg.TrustProxy)

	authorizeURL, _, err := h.oauth.AuthorizeURL(auth.StateIntentLogin, redirect, "", origin)
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	c.Redirect(http.StatusFound, authorizeURL)
}

// Callback 处理 GET /api/web/v1/auth/oauth/github/callback。
//
// 分支逻辑见 API.md §1 与 D27：**不自动建号**，未绑定则拒绝。
func (h *OAuthHandler) Callback(c *gin.Context) {
	origin := requestOrigin(c, h.cfg.TrustProxy)

	// GitHub 在校验失败时会带 error 参数回调
	if e := strings.TrimSpace(c.Query("error")); e != "" {
		h.redirectError(c, "/", "exchange_failed")
		return
	}

	payload, err := h.oauth.VerifyState(c.Query("state"))
	if err != nil {
		h.log.Warn("OAuth state 校验失败", "err", err)
		h.redirectError(c, "/", "state_invalid")
		return
	}

	gh, err := h.oauth.ExchangeAndFetch(c.Request.Context(), c.Query("code"))
	if err != nil {
		h.log.Warn("OAuth 换取用户信息失败", "err", err, "origin", origin)
		h.redirectError(c, payload.Redirect, "exchange_failed")
		return
	}

	clientIP := middleware.ClientIP(c)
	userAgent := c.Request.UserAgent()

	// ---- 绑定分支（已登录用户绑定新身份）----
	if payload.Intent == auth.StateIntentBind {
		if payload.UserUID == "" {
			h.redirectError(c, payload.Redirect, "state_invalid")
			return
		}
		if err := h.oauth.Bind(c.Request.Context(), payload.UserUID, gh, clientIP, userAgent); err != nil {
			h.log.Warn("OAuth 绑定失败", "err", err, "user", payload.UserUID)
			h.redirectWithQuery(c, payload.Redirect, "oauth_error", "bind_failed")
			return
		}
		h.redirectWithQuery(c, payload.Redirect, "oauth_bound", model.ProviderGitHub)
		return
	}

	// ---- 登录分支 ----
	user, err := h.oauth.ResolveLogin(c.Request.Context(), gh, clientIP, userAgent)
	if err != nil {
		reason := "not_bound"
		switch service.CodeOf(err) {
		case response.CodeAccountDisabled:
			reason = "disabled"
		case response.CodeInternal:
			reason = "exchange_failed"
		}
		h.redirectError(c, payload.Redirect, reason)
		return
	}

	issued, err := h.tokens.Issue(c.Request.Context(), user, clientIP, userAgent)
	if err != nil {
		h.log.Error("OAuth 登录后签发令牌失败", "err", err, "uid", user.UID)
		h.redirectError(c, payload.Redirect, "token_failed")
		return
	}

	// 同时下发 Cookie（浏览器可直接用）与 URL 片段（前端 SPA 读取后自行处理）
	auth.SetAuthCookies(c, issued.AccessToken, issued.AccessTTL,
		issued.RefreshToken, issued.RefreshTTL, h.tokens.SecureCookies())

	target := auth.SanitizeRedirect(payload.Redirect, "/")
	c.Redirect(http.StatusFound, target+"#access_token="+url.QueryEscape(issued.AccessToken))
}

// Bind 处理 POST /api/web/v1/auth/oauth/github/bind。
//
// 返回授权跳转信息；前端 `window.location` 过去，回调时完成真正的绑定。
func (h *OAuthHandler) Bind(c *gin.Context) {
	if !h.oauth.Enabled() {
		response.Fail(c, response.CodeNotFound)
		return
	}

	var req struct {
		Redirect string `json:"Redirect"`
	}
	// body 可省略
	_ = c.ShouldBindJSON(&req)

	authorizeURL, state, err := h.oauth.AuthorizeURL(
		auth.StateIntentBind, req.Redirect, middleware.CurrentUserUID(c),
		requestOrigin(c, h.cfg.TrustProxy))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, gin.H{"AuthorizeURL": authorizeURL, "State": state})
}

// Unbind 处理 DELETE /api/web/v1/auth/oauth/github。
func (h *OAuthHandler) Unbind(c *gin.Context) {
	err := h.oauth.Unbind(c.Request.Context(), middleware.CurrentUserUID(c),
		middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, nil)
}

// redirectError 把错误跳回登录页（登录流程）或原页面（绑定流程）。
func (h *OAuthHandler) redirectError(c *gin.Context, redirect, reason string) {
	target := auth.SanitizeRedirect(redirect, "/")
	if target == "/" {
		// 登录流程：按契约跳到 /login?error=<reason>
		c.Redirect(http.StatusFound, "/login?error="+url.QueryEscape(reason))
		return
	}
	h.redirectWithQuery(c, target, "oauth_error", reason)
}

// redirectWithQuery 往目标路径追加一个查询参数后跳转。
func (h *OAuthHandler) redirectWithQuery(c *gin.Context, redirect, key, value string) {
	target := auth.SanitizeRedirect(redirect, "/")
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	c.Redirect(http.StatusFound, target+sep+key+"="+url.QueryEscape(value))
}

// requestOrigin 推断本站 origin，用于在未配置 site.baseUrl 时拼回调地址。
//
// ⚠️ 只有显式开启可信代理时才采信 X-Forwarded-Proto，
// 否则客户端可伪造协议头让回调地址变成 https（或被诱导到错误域名）。
func requestOrigin(c *gin.Context, trustProxy bool) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if trustProxy {
		if proto := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")); proto != "" {
			if i := strings.Index(proto, ","); i >= 0 {
				proto = proto[:i]
			}
			if p := strings.ToLower(strings.TrimSpace(proto)); p == "https" || p == "http" {
				scheme = p
			}
		}
	}
	return scheme + "://" + c.Request.Host
}
