package handler

import (
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
)

// EmailHandler 处理邮件相关端点。
//
// 分两类：
//
//   - **公开**（`RegisterPublic`）：`/auth/forgot-password`、`/auth/reset-password`
//     —— 忘记密码的用户还没登录，必须免鉴权（W3 未实现这两条，故由本工作流补上）
//   - **admin**（`RegisterAdmin`）：`/settings/mail/test`
type EmailHandler struct {
	emails *service.EmailService
	log    *slog.Logger
}

// NewEmailHandler 构造。
func NewEmailHandler(emails *service.EmailService, log *slog.Logger) *EmailHandler {
	return &EmailHandler{emails: emails, log: log}
}

// RegisterPublic 注册**免鉴权**的找回密码端点。
//
// 路由与 W3 的 auth 端点同前缀（`/auth/*`），但它们不冲突：
// W3 注册的是 login/refresh/logout/me/password/identities，这里是 forgot/reset。
func (h *EmailHandler) RegisterPublic(g *gin.RouterGroup) {
	g.POST("/auth/forgot-password", h.ForgotPassword)
	g.POST("/auth/reset-password", h.ResetPassword)
}

// RegisterAdmin 注册需管理员的路由（由调用方挂 RequireAdmin）。
func (h *EmailHandler) RegisterAdmin(g *gin.RouterGroup) {
	g.POST("/settings/mail/test", h.SendTest)
}

// ForgotPassword 处理 POST /auth/forgot-password。
//
// ⚠️ **防账号枚举**：无论邮箱是否存在，**响应完全一致**（都是成功 + 同一句话）。
// 这是刻意的（docs/OPERATIONS.md §6）：
// 若「邮箱不存在」返回 404，攻击者就能用它枚举站内账号。
//
// 唯一例外是「邮件服务未配置」—— 那是站点级问题，如实告知才不会让用户干等。
func (h *EmailHandler) ForgotPassword(c *gin.Context) {
	var req struct {
		Email string `json:"Email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}
	if strings.TrimSpace(req.Email) == "" {
		response.InvalidParam(c, "邮箱不能为空")
		return
	}

	if err := h.emails.RequestPasswordReset(c.Request.Context(), req.Email,
		middleware.ClientIP(c), c.Request.UserAgent()); err != nil {
		respondServiceError(c, h.log, err)
		return
	}

	// 统一话术：不区分「已发送」与「邮箱不存在」
	response.OKMsg(c, "如果该邮箱对应站内账号，我们已发送重置邮件，请查收", nil)
}

// ResetPassword 处理 POST /auth/reset-password。
//
// 令牌来自邮件里的链接；成功后该令牌**立即失效**（单次使用）。
func (h *EmailHandler) ResetPassword(c *gin.Context) {
	var req struct {
		Token       string `json:"Token"`
		NewPassword string `json:"NewPassword"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	if err := h.emails.ResetPassword(c.Request.Context(), req.Token, req.NewPassword,
		middleware.ClientIP(c), c.Request.UserAgent()); err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OKMsg(c, "密码已重置，请使用新密码登录", nil)
}

// SendTest 处理 POST /settings/mail/test（admin）。
//
// 测试失败时**把 SMTP 的真实错误原样返回**：管理员就是要靠它排查配置，
// 藏起来反而无法定位（这与其他端点「不泄露内部细节」的原则不同，
// 因为这里的调用方是管理员，且错误内容是管理员自己的 SMTP 配置问题）。
func (h *EmailHandler) SendTest(c *gin.Context) {
	var req struct {
		To string `json:"To"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	err := h.emails.SendTest(c.Request.Context(), req.To,
		middleware.CurrentUserUID(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OKMsg(c, "测试邮件已发送", gin.H{"To": strings.TrimSpace(req.To)})
}
