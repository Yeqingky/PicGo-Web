package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"strings"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/id"
	"github.com/YeqingKy/PicGo-Web/server/internal/mail"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// EmailService 负责发信、邮件日志、以及「找回密码」的一次性令牌。
//
// 关键约定（D29 + docs/OPERATIONS.md §6）：
//
//   - **每次发信都写 EmailLogs**（收件人 / 主题 / 模板 / 结果，**不存正文**）
//   - **每次都写 OperationLogs（mail.send）**
//   - 「找回密码」对**不存在**的邮箱也返回成功（防账号枚举）
//   - 一次性令牌存 **UserSettings KV**（键 `auth.resetToken`），**不新建表**
//     —— 这是 docs/OPERATIONS.md 登记的待定项，采用 KV 方案
type EmailService struct {
	cfg          *config.Config
	log          *slog.Logger
	settings     *settings.Service
	users        *repository.UserRepo
	tokens       *repository.TokenRepo
	settingsRepo *repository.SettingRepo
	emails       *repository.EmailLogRepo
	audit        *AuditService
}

// NewEmailService 构造。
func NewEmailService(
	cfg *config.Config,
	log *slog.Logger,
	settingsSvc *settings.Service,
	users *repository.UserRepo,
	tokens *repository.TokenRepo,
	settingsRepo *repository.SettingRepo,
	emails *repository.EmailLogRepo,
	audit *AuditService,
) *EmailService {
	return &EmailService{
		cfg: cfg, log: log, settings: settingsSvc,
		users: users, tokens: tokens, settingsRepo: settingsRepo,
		emails: emails, audit: audit,
	}
}

// resetTokenKey 是存一次性令牌的 UserSettings 键。
const resetTokenKey = "auth.resetToken"

// resetTokenTTL 重置链接有效期。
const resetTokenTTL = 30 * time.Minute

// resetTokenPayload 是 `auth.resetToken` 的值结构。
type resetTokenPayload struct {
	TokenHash string `json:"TokenHash"`
	ExpiresAt int64  `json:"ExpiresAt"`
	UserUID   string `json:"UserUID"`
}

// smtpConfig 从设置里组装 SMTP 配置。
//
// ⚠️ 密码用 `GetRaw` 取明文（Get 返回的是掩码）。
func (s *EmailService) smtpConfig() mail.Config {
	return mail.Config{
		Enabled:     s.settings.GetBool("mail.enabled", false),
		Host:        s.settings.GetString("mail.host"),
		Port:        int(s.settings.GetInt("mail.port", 465)),
		Encryption:  s.settings.GetString("mail.encryption"),
		Username:    s.settings.GetString("mail.username"),
		Password:    s.settings.GetRaw("mail.password"),
		FromAddress: s.settings.GetString("mail.fromAddress"),
		FromName:    s.settings.GetString("mail.fromName"),
	}
}

// EmailEnabled 邮件功能是否已启用且配置完整（供系统信息展示）。
func (s *EmailService) EmailEnabled() bool {
	cfg := s.smtpConfig()
	return cfg.Validate() == nil
}

// siteName 取站点名（用于邮件标题与落款）。
func (s *EmailService) siteName() string {
	if n := strings.TrimSpace(s.settings.GetString("site.name")); n != "" {
		return n
	}
	return "PicGo Web"
}

// siteBaseURL 取对外访问地址（用于拼链接）；
// 未配置时返回相对路径（管理员应配置 site.baseUrl，见 DATA-MODEL §7.4）。
func (s *EmailService) siteBaseURL() string {
	return strings.TrimRight(strings.TrimSpace(s.settings.GetString("site.baseUrl")), "/")
}

// ---------------------------------------------------------------------------
// 发送
// ---------------------------------------------------------------------------

// Send 发送一封基于模板的邮件，并如实记录日志。
//
// 无论成功失败都会写 `EmailLogs` 与 `OperationLogs:mail.send`。
func (s *EmailService) Send(ctx context.Context, templateName, to string, data map[string]any, relatedUserUID string) error {
	subject, body, err := s.render(templateName, data)
	if err != nil {
		return err
	}

	cfg := s.smtpConfig()
	sender := mail.New(cfg)
	sendErr := sender.Send(mail.Message{To: to, Subject: subject, HTML: body})

	// ---- 邮件日志（不存正文）----
	status := model.LogStatusSuccess
	errMsg := ""
	if sendErr != nil {
		status = model.LogStatusFailed
		errMsg = sendErr.Error()
	}

	now := model.Now()
	if logErr := s.emails.Insert(&model.EmailLog{
		UID:            id.EmailLog(),
		ToAddress:      to,
		Subject:        subject,
		Template:       templateName,
		Status:         status,
		Error:          errMsg,
		RelatedUserUID: relatedUserUID,
		CreatedAt:      now,
	}); logErr != nil {
		// 日志写失败不影响发信结果，但要留痕
		s.log.Warn("写入邮件日志失败", "to", to, "template", templateName, "err", logErr)
	}

	// ---- 操作日志 ----
	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeMailSend, Status: status,
		UserUID: relatedUserUID, TargetType: "email", TargetUID: to,
		Detail: map[string]any{"Template": templateName, "Subject": subject},
		Cause:  sendErr,
	})
	return sendErr
}

// SendTest 发送测试邮件（admin）。
func (s *EmailService) SendTest(ctx context.Context, to, by, clientIP, userAgent string) error {
	to = strings.TrimSpace(to)
	if to == "" {
		return Errorf(response.CodeInvalidParam, "收件人地址不能为空")
	}
	if !strings.Contains(to, "@") {
		return Errorf(response.CodeInvalidParam, "收件人地址格式不正确")
	}

	err := s.Send(ctx, model.MailTemplateTest, to, map[string]any{
		"SiteName": s.siteName(),
		"BaseURL":  s.siteBaseURL(),
		"Time":     time.Now().Format("2006-01-02 15:04:05"),
	}, by)
	if err != nil {
		// 测试邮件失败要把原因原样告诉管理员（否则没法排查 SMTP）
		return Wrap(response.CodeInternal, "发送测试邮件失败："+err.Error(), err)
	}
	return nil
}

// SendInvite 发送「邀请/建号通知」邮件（admin 建号后可调用）。
//
// 注意：本项目**不做自助注册**（D25），因此这封邮件是「告知 + 引导登录」，
// 而不是「注册确认」。
func (s *EmailService) SendInvite(ctx context.Context, to, by, clientIP, userAgent string) error {
	to = strings.TrimSpace(to)
	if to == "" {
		return Errorf(response.CodeInvalidParam, "收件人地址不能为空")
	}
	return s.Send(ctx, model.MailTemplateInvite, to, map[string]any{
		"SiteName": s.siteName(),
		"BaseURL":  s.siteBaseURL(),
		"Inviter":  by,
	}, "")
}

// ---------------------------------------------------------------------------
// 找回密码
// ---------------------------------------------------------------------------

// RequestPasswordReset 发起找回密码。
//
// ⚠️ **防账号枚举**：无论邮箱是否存在、邮件是否发成功，**都返回 nil**。
// 调用方（handler）据此统一返回「如果该邮箱存在，我们已发送重置邮件」。
//
// 唯一例外是「邮件功能未启用」—— 那是**配置问题**，必须让用户知道
// （否则用户会一直等一封永远不会来的邮件）。
func (s *EmailService) RequestPasswordReset(ctx context.Context, email, clientIP, userAgent string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return Errorf(response.CodeInvalidParam, "邮箱不能为空")
	}

	if !s.EmailEnabled() {
		return Errorf(response.CodeInternal, "站点尚未配置邮件服务，请联系管理员")
	}

	user, err := s.users.FindByEmail(email)
	if err != nil || user == nil {
		// 不存在：静默返回（防枚举）。为了让耗时接近，这里不做额外处理 ——
		// 真正的等时性由 bcrypt 在登录路径保证，找回密码路径不涉及密码比对。
		s.log.Info("找回密码请求：邮箱不存在（静默忽略）")
		return nil
	}
	if !user.IsActive() {
		// 禁用的账号也静默忽略（避免暴露「这个邮箱存在但被禁用」）
		return nil
	}

	plain, hash, err := auth.GenerateRefreshToken()
	if err != nil {
		s.log.Warn("生成重置令牌失败", "err", err)
		return nil
	}

	payload := resetTokenPayload{
		TokenHash: hash,
		ExpiresAt: model.Now() + int64(resetTokenTTL.Seconds()),
		UserUID:   user.UID,
	}
	raw, _ := json.Marshal(payload)

	now := model.Now()
	if err := s.settingsRepo.UpsertUser(&model.UserSetting{
		UserUID:   user.UID,
		Key:       resetTokenKey,
		Value:     string(raw),
		ValueType: "json",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		s.log.Warn("保存重置令牌失败", "user", user.UID, "err", err)
		return nil
	}

	// 发信失败也不外传（否则「发信失败」就成了邮箱存在的信号）
	if err := s.Send(ctx, model.MailTemplateResetPassword, user.Email, map[string]any{
		"SiteName":   s.siteName(),
		"BaseURL":    s.siteBaseURL(),
		"Token":      plain,
		"TTLMinutes": int(resetTokenTTL.Minutes()),
	}, user.UID); err != nil {
		s.log.Warn("发送重置密码邮件失败", "user", user.UID, "err", err)
	}
	return nil
}

// ResetPassword 用一次性令牌重置密码。
//
// 成功后**立即失效该令牌**（单次使用），并要求下次登录后改密（这里直接已改，无需再标记）。
func (s *EmailService) ResetPassword(ctx context.Context, token, newPassword, clientIP, userAgent string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return Errorf(response.CodeInvalidParam, "重置令牌不能为空")
	}
	if len(newPassword) < minPasswordLength {
		return Errorf(response.CodeInvalidParam, "新密码至少 %d 位", minPasswordLength)
	}

	hash := hashResetToken(token)
	now := model.Now()

	// 令牌存在哪个用户下？需要遍历有该键的用户设置。
	// 由于 UserSettings 的键不带用户信息，这里用「按 Key 查」再校验哈希。
	rows, err := s.settingsRepo.ListUserByKey(resetTokenKey)
	if err != nil {
		return Wrap(response.CodeInternal, "查询重置令牌失败", err)
	}

	for _, row := range rows {
		var payload resetTokenPayload
		if err := json.Unmarshal([]byte(row.Value), &payload); err != nil {
			continue
		}
		if payload.TokenHash != hash {
			continue
		}
		if payload.ExpiresAt < now {
			// 过期：清掉它，并给出明确提示
			_ = s.settingsRepo.DeleteUser(row.UserUID, resetTokenKey)
			return Errorf(response.CodeInvalidParam, "重置链接已过期，请重新发起找回密码")
		}

		user, err := s.users.FindByUID(payload.UserUID)
		if err != nil {
			return Errorf(response.CodeNotFound, "账号不存在")
		}
		if !user.IsActive() {
			return Errorf(response.CodeAccountDisabled, "账号已被禁用")
		}

		pwdHash, err := auth.HashPassword(newPassword)
		if err != nil {
			return Wrap(response.CodeInternal, "密码加密失败", err)
		}

		if err := s.users.UpdateFields(user.UID, map[string]any{
			"PasswordHash":       pwdHash,
			"MustChangePassword": false,
			"UpdatedAt":          now,
		}); err != nil {
			return Wrap(response.CodeInternal, "保存新密码失败", err)
		}

		// 单次使用：立即失效
		if err := s.settingsRepo.DeleteUser(user.UID, resetTokenKey); err != nil {
			s.log.Warn("清理重置令牌失败", "user", user.UID, "err", err)
		}

		// 吊销所有会话：密码改了，旧登录态必须失效（防「被盗号后改密也没用」）
		if n, err := s.tokens.RevokeAllRefreshForUser(user.UID, now); err != nil {
			s.log.Warn("吊销会话失败", "user", user.UID, "err", err)
		} else if n > 0 {
			s.log.Info("重置密码后已吊销会话", "user", user.UID, "sessions", n)
		}

		s.audit.Log(ctx, AuditEntry{
			Type: model.LogTypeUserUpdate, Status: model.LogStatusSuccess,
			UserUID: user.UID, TargetType: "user", TargetUID: user.UID,
			Detail:   map[string]any{"action": "reset_password_by_token"},
			ClientIP: clientIP, UserAgent: userAgent,
		})
		return nil
	}

	return Errorf(response.CodeInvalidParam, "重置链接无效或已使用")
}

// hashResetToken 对一次性令牌做 SHA-256（**库里只存哈希**，防库泄露即被改密）。
func hashResetToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// minPasswordLength 是新密码的最小长度（与 W3 的口径保持一致）。
const minPasswordLength = 8

// ---------------------------------------------------------------------------
// 模板
// ---------------------------------------------------------------------------

// 邮件模板用 Go 内嵌字符串（不引外部文件），中文，含站点名。
//
// 样式尽量内联：多数邮件客户端会剥离 <style>，内联最稳。
const emailLayout = `<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>{{.Subject}}</title></head>
<body style="margin:0;padding:24px;background:#f5f5f7;font-family:-apple-system,'PingFang SC','Microsoft YaHei',sans-serif;color:#18181b;">
  <div style="max-width:560px;margin:0 auto;background:#ffffff;border-radius:12px;padding:32px;">
    <h1 style="margin:0 0 8px;font-size:18px;">{{.SiteName}}</h1>
    <div style="height:1px;background:#e4e4e7;margin:16px 0 24px;"></div>
    {{.Body}}
    <div style="margin-top:32px;padding-top:16px;border-top:1px solid #e4e4e7;color:#71717a;font-size:12px;">
      本邮件由 {{.SiteName}} 自动发送，请勿直接回复。
    </div>
  </div>
</body></html>`

// templateDef 是一个邮件模板。
type templateDef struct {
	Subject string
	Body    string
}

var emailTemplates = map[string]templateDef{
	model.MailTemplateTest: {
		Subject: "【{{.SiteName}}】测试邮件",
		Body: `<p style="margin:0 0 16px;line-height:1.6;">这是一封测试邮件，说明 SMTP 配置可用。</p>
<p style="margin:0;color:#71717a;font-size:13px;">发送时间：{{.Time}}</p>`,
	},
	model.MailTemplateResetPassword: {
		Subject: "【{{.SiteName}}】重置密码",
		Body: `<p style="margin:0 0 16px;line-height:1.6;">你（或他人）请求重置该账号的密码。</p>
<p style="margin:0 0 16px;line-height:1.6;">请点击下面的链接设置新密码，链接 {{.TTLMinutes}} 分钟内有效且只能使用一次：</p>
<p style="margin:0 0 16px;"><a href="{{.BaseURL}}/reset-password?token={{.Token}}" style="display:inline-block;padding:10px 20px;background:#007AFF;color:#fff;border-radius:8px;text-decoration:none;">重置密码</a></p>
<p style="margin:0;color:#71717a;font-size:13px;">如果不是你本人操作，请忽略本邮件，你的密码不会被修改。</p>`,
	},
	model.MailTemplateInvite: {
		Subject: "【{{.SiteName}}】你的图床账号已创建",
		Body: `<p style="margin:0 0 16px;line-height:1.6;">管理员已为你创建 {{.SiteName}} 的账号。</p>
<p style="margin:0 0 16px;line-height:1.6;">请使用你的邮箱与管理员提供的初始密码登录，登录后系统会要求你立即修改密码。</p>
<p style="margin:0 0 16px;"><a href="{{.BaseURL}}/login" style="display:inline-block;padding:10px 20px;background:#007AFF;color:#fff;border-radius:8px;text-decoration:none;">前往登录</a></p>`,
	},
	model.MailTemplateVerify: {
		Subject: "【{{.SiteName}}】邮箱验证",
		Body: `<p style="margin:0 0 16px;line-height:1.6;">请点击下面的链接完成邮箱验证：</p>
<p style="margin:0 0 16px;"><a href="{{.BaseURL}}/login?verify={{.Token}}">验证邮箱</a></p>`,
	},
}

// render 渲染邮件主题与 HTML 正文。
func (s *EmailService) render(templateName string, data map[string]any) (subject string, body string, err error) {
	def, ok := emailTemplates[templateName]
	if !ok {
		return "", "", Errorf(response.CodeInvalidParam, "未知的邮件模板「%s」", templateName)
	}

	vars := map[string]any{"SiteName": s.siteName()}
	for k, v := range data {
		vars[k] = v
	}
	if _, ok := vars["BaseURL"]; !ok {
		vars["BaseURL"] = s.siteBaseURL()
	}

	subj, err := renderTemplate("subject:"+templateName, def.Subject, vars)
	if err != nil {
		return "", "", err
	}
	inner, err := renderTemplate("body:"+templateName, def.Body, vars)
	if err != nil {
		return "", "", err
	}
	// 内层模板产物是可信的（我们自己写的），用 template.HTML 包一层送给外层布局
	full, err := renderTemplate("layout:"+templateName, emailLayout, map[string]any{
		"Subject":  subj,
		"SiteName": vars["SiteName"],
		"Body":     template.HTML(inner), //nolint:gosec // 内容由本包的模板常量产生，非用户输入
	})
	if err != nil {
		return "", "", err
	}
	return subj, full, nil
}

func renderTemplate(name, tpl string, vars map[string]any) (string, error) {
	t, err := template.New(name).Option("missingkey=zero").Parse(tpl)
	if err != nil {
		return "", Wrap(response.CodeInternal, fmt.Sprintf("渲染邮件模板 %s 失败", name), err)
	}
	var b strings.Builder
	if err := t.Execute(&b, vars); err != nil {
		return "", Wrap(response.CodeInternal, fmt.Sprintf("渲染邮件模板 %s 失败", name), err)
	}
	return b.String(), nil
}
