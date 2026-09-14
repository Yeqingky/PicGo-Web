package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/id"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// GitHub 端点。声明为变量以便测试时替换（不引入额外配置项）。
var (
	githubAuthorizeEndpoint = "https://github.com/login/oauth/authorize"
	githubTokenEndpoint     = "https://github.com/login/oauth/access_token"
	githubAPIBase           = "https://api.github.com"
)

// ProviderInfo 是「已启用的登录方式」，供前端决定是否显示第三方登录按钮。
type ProviderInfo struct {
	Name        string `json:"Name"`
	DisplayName string `json:"DisplayName"`
}

// GitHubUser 是 GitHub 返回的用户信息。
//
// ⚠️ 字段名来自 GitHub API，**保持原样**（不适用 D81 的 PascalCase 规则：
// 它们是外部契约，改名会导致反序列化失败）。
type GitHubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

// OAuthService 实现 GitHub OAuth 登录与绑定（D26/D27/D28）。
type OAuthService struct {
	settings *settings.Service
	users    *repository.UserRepo
	signer   *auth.StateSigner
	audit    *AuditService
	log      *slog.Logger

	// httpClient / 端点可在测试中替换
	httpClient   *http.Client
	authorizeURL string
	tokenURL     string
	apiBase      string
}

// NewOAuthService 构造。
func NewOAuthService(
	st *settings.Service,
	users *repository.UserRepo,
	signer *auth.StateSigner,
	audit *AuditService,
	log *slog.Logger,
) *OAuthService {
	return &OAuthService{
		settings:     st,
		users:        users,
		signer:       signer,
		audit:        audit,
		log:          log,
		httpClient:   &http.Client{Timeout: 15 * time.Second},
		authorizeURL: githubAuthorizeEndpoint,
		tokenURL:     githubTokenEndpoint,
		apiBase:      githubAPIBase,
	}
}

// Enabled 判断 GitHub 登录是否已配置启用（D26：未配置则前端不显示按钮）。
func (s *OAuthService) Enabled() bool {
	if !s.settings.GetBool("oauth.github.enabled", false) {
		return false
	}
	return s.clientID() != "" && s.clientSecret() != ""
}

// Providers 返回已启用的登录方式列表。
func (s *OAuthService) Providers() []ProviderInfo {
	out := []ProviderInfo{}
	if s.Enabled() {
		out = append(out, ProviderInfo{Name: model.ProviderGitHub, DisplayName: "GitHub"})
	}
	return out
}

// CallbackURL 返回 GitHub 回调地址。
//
// 优先用 site.baseUrl（管理员应显式配置，避免反代下推断错误）；
// 未配置时用请求推断出来的 fallbackOrigin。
func (s *OAuthService) CallbackURL(fallbackOrigin string) string {
	base := strings.TrimRight(strings.TrimSpace(s.settings.GetString("site.baseUrl")), "/")
	if base == "" {
		base = strings.TrimRight(fallbackOrigin, "/")
	}
	return base + "/api/web/v1/auth/oauth/" + model.ProviderGitHub + "/callback"
}

// AuthorizeURL 生成 GitHub 授权页地址，并返回本次的 state。
//
// intent 为 login 或 bind；bind 时必须带 userUID（要绑定到哪个账号）。
func (s *OAuthService) AuthorizeURL(intent auth.StateIntent, redirect, userUID, fallbackOrigin string) (authorizeURL, state string, err error) {
	if !s.Enabled() {
		return "", "", NewError(response.CodeNotFound, "未启用 GitHub 登录")
	}
	if intent != auth.StateIntentLogin && intent != auth.StateIntentBind {
		return "", "", NewError(response.CodeInvalidParam, "非法的 OAuth 意图")
	}
	if intent == auth.StateIntentBind && userUID == "" {
		return "", "", NewError(response.CodeUnauthorized, "绑定操作需要先登录")
	}

	// 防开放重定向：只允许站内路径
	safeRedirect := auth.SanitizeRedirect(redirect, "/")

	state, err = s.signer.Sign(auth.StatePayload{
		Redirect: safeRedirect,
		Intent:   intent,
		UserUID:  userUID,
		Provider: model.ProviderGitHub,
	})
	if err != nil {
		return "", "", Wrap(response.CodeInternal, "生成 OAuth state 失败", err)
	}

	q := url.Values{}
	q.Set("client_id", s.clientID())
	q.Set("redirect_uri", s.CallbackURL(fallbackOrigin))
	q.Set("scope", "read:user user:email")
	q.Set("state", state)
	q.Set("allow_signup", "false")

	return s.authorizeURL + "?" + q.Encode(), state, nil
}

// VerifyState 校验回调携带的 state。
func (s *OAuthService) VerifyState(state string) (*auth.StatePayload, error) {
	payload, err := s.signer.Verify(state, auth.StateTTL)
	if err != nil {
		if errors.Is(err, auth.ErrStateExpired) {
			return nil, NewError(response.CodeInvalidParam, "授权已超时，请重新发起")
		}
		return nil, NewError(response.CodeInvalidParam, "授权校验失败，请重新发起")
	}
	return payload, nil
}

// ExchangeAndFetch 用授权码换 token，并拉取 GitHub 用户信息（含主邮箱）。
func (s *OAuthService) ExchangeAndFetch(ctx context.Context, code string) (*GitHubUser, error) {
	if strings.TrimSpace(code) == "" {
		return nil, NewError(response.CodeInvalidParam, "缺少授权码")
	}

	token, err := s.exchangeToken(ctx, code)
	if err != nil {
		return nil, err
	}

	gh, err := s.fetchUser(ctx, token)
	if err != nil {
		return nil, err
	}
	if gh.ID == 0 {
		// D28：必须有稳定唯一标识（GitHub 的数字 id），否则无法安全绑定
		return nil, NewError(response.CodeInternal, "GitHub 未返回稳定的用户标识")
	}

	// GitHub 的 /user.email 可能为空（用户把邮箱设为私有），此时查 /user/emails 取主邮箱
	if gh.Email == "" {
		if email, err := s.fetchPrimaryEmail(ctx, token); err == nil {
			gh.Email = email
		}
	}
	return gh, nil
}

// ResolveLogin 按 D27 决定这次 OAuth 回调能否登录。
//
// 规则（严格，不自动建号）：
//
//	① OAuthIdentities 命中 → 直接登录
//	② 未命中，但 oauth.autoBindByEmail = true 且邮箱命中已有用户 → 自动绑定并登录
//	③ 其余 → 拒绝
func (s *OAuthService) ResolveLogin(ctx context.Context, gh *GitHubUser, clientIP, userAgent string) (*model.User, error) {
	providerUserID := fmt.Sprintf("%d", gh.ID)

	identity, err := s.users.FindIdentity(model.ProviderGitHub, providerUserID)
	if err != nil && !repository.IsNotFound(err) {
		return nil, Wrap(response.CodeInternal, "查询绑定失败", err)
	}

	if identity != nil {
		u, err := s.users.FindByUID(identity.UserUID)
		if err != nil {
			if repository.IsNotFound(err) {
				return nil, NewError(response.CodeUnauthorized, "绑定的账号已不存在")
			}
			return nil, Wrap(response.CodeInternal, "查询账号失败", err)
		}
		if !u.IsActive() {
			return nil, NewError(response.CodeAccountDisabled, "")
		}

		// 顺手同步一下 GitHub 侧可能变化的展示信息
		s.refreshIdentity(identity, gh)

		s.audit.Log(ctx, AuditEntry{
			Type:       model.LogTypeAuthLogin,
			Status:     model.LogStatusSuccess,
			UserUID:    u.UID,
			Username:   u.Email,
			TargetType: "user",
			TargetUID:  u.UID,
			Detail:     map[string]any{"Via": model.ProviderGitHub, "ProviderLogin": gh.Login},
			ClientIP:   clientIP,
			UserAgent:  userAgent,
		})
		return u, nil
	}

	// ② 按邮箱自动绑定（默认关闭，D 里的 oauth.autoBindByEmail）
	if s.settings.GetBool("oauth.autoBindByEmail", false) {
		email := normalizeEmail(gh.Email)
		if email != "" {
			if u, err := s.users.FindByEmail(email); err == nil {
				if !u.IsActive() {
					return nil, NewError(response.CodeAccountDisabled, "")
				}
				if err := s.bindIdentity(ctx, u.UID, gh, "auto_bind_by_email", clientIP, userAgent); err != nil {
					return nil, err
				}
				s.audit.Log(ctx, AuditEntry{
					Type:       model.LogTypeAuthLogin,
					Status:     model.LogStatusSuccess,
					UserUID:    u.UID,
					Username:   u.Email,
					TargetType: "user",
					TargetUID:  u.UID,
					Detail:     map[string]any{"Via": model.ProviderGitHub, "AutoBound": true},
					ClientIP:   clientIP,
					UserAgent:  userAgent,
				})
				return u, nil
			} else if !repository.IsNotFound(err) {
				return nil, Wrap(response.CodeInternal, "按邮箱查询账号失败", err)
			}
		}
	}

	// ③ 拒绝
	s.audit.Log(ctx, AuditEntry{
		Type:       model.LogTypeAuthFailed,
		Status:     model.LogStatusFailed,
		Username:   gh.Login,
		TargetType: "user",
		Detail:     map[string]any{"Reason": "not_bound", "ProviderLogin": gh.Login, "Via": model.ProviderGitHub},
		Cause:      NewError(response.CodeUnauthorized, ""),
		ClientIP:   clientIP,
		UserAgent:  userAgent,
	})
	return nil, NewError(response.CodeUnauthorized, "该 GitHub 账号尚未绑定任何本站账号")
}

// Bind 把 GitHub 身份绑定到指定用户（对应 POST /auth/oauth/github/bind 的回调部分）。
func (s *OAuthService) Bind(ctx context.Context, userUID string, gh *GitHubUser, clientIP, userAgent string) error {
	if _, err := s.users.FindByUID(userUID); err != nil {
		if repository.IsNotFound(err) {
			return NewError(response.CodeNotFound, "用户不存在")
		}
		return Wrap(response.CodeInternal, "查询账号失败", err)
	}

	// 已绑定同 provider → 冲突
	if existing, err := s.users.FindIdentityForUser(userUID, model.ProviderGitHub); err == nil && existing != nil {
		return NewError(response.CodeConflict, "当前账号已绑定 GitHub")
	} else if err != nil && !repository.IsNotFound(err) {
		return Wrap(response.CodeInternal, "查询绑定失败", err)
	}

	// 该 GitHub 账号已被**别人**绑定 → 冲突（防身份劫持）
	providerUserID := fmt.Sprintf("%d", gh.ID)
	if taken, err := s.users.FindIdentity(model.ProviderGitHub, providerUserID); err == nil && taken != nil {
		return NewError(response.CodeConflict, "该 GitHub 账号已绑定到其他账号")
	} else if err != nil && !repository.IsNotFound(err) {
		return Wrap(response.CodeInternal, "查询绑定失败", err)
	}

	return s.bindIdentity(ctx, userUID, gh, "user_bind", clientIP, userAgent)
}

// Unbind 解绑。
//
// 安全约束：若用户**既没有密码、也没有其它绑定**，解绑会导致永久无法登录 → 拒绝（防锁死账号）。
func (s *OAuthService) Unbind(ctx context.Context, userUID, clientIP, userAgent string) error {
	u, err := s.users.FindByUID(userUID)
	if err != nil {
		if repository.IsNotFound(err) {
			return NewError(response.CodeNotFound, "用户不存在")
		}
		return Wrap(response.CodeInternal, "查询账号失败", err)
	}

	if _, err := s.users.FindIdentityForUser(userUID, model.ProviderGitHub); err != nil {
		if repository.IsNotFound(err) {
			return NewError(response.CodeNotFound, "尚未绑定 GitHub")
		}
		return Wrap(response.CodeInternal, "查询绑定失败", err)
	}

	others, err := s.users.CountIdentities(userUID)
	if err != nil {
		return Wrap(response.CodeInternal, "统计绑定失败", err)
	}
	if u.PasswordHash == "" && others <= 1 {
		return NewError(response.CodeConflict, "解绑后将无法登录，请先设置密码")
	}

	if _, err := s.users.DeleteIdentity(userUID, model.ProviderGitHub); err != nil {
		return Wrap(response.CodeInternal, "解绑失败", err)
	}

	s.audit.Log(ctx, AuditEntry{
		Type:       model.LogTypeUserUpdate,
		Status:     model.LogStatusSuccess,
		UserUID:    userUID,
		Username:   u.Email,
		TargetType: "user",
		TargetUID:  userUID,
		Detail:     map[string]any{"Action": "oauth_unbind", "Provider": model.ProviderGitHub},
		ClientIP:   clientIP,
		UserAgent:  userAgent,
	})
	return nil
}

// ---- 内部 ----

func (s *OAuthService) clientID() string {
	return strings.TrimSpace(s.settings.GetRaw("oauth.github.clientId"))
}

func (s *OAuthService) clientSecret() string {
	// clientSecret 是 TypeSecret：GetRaw 才能拿到明文
	return strings.TrimSpace(s.settings.GetRaw("oauth.github.clientSecret"))
}

// bindIdentity 写绑定记录并审计。
func (s *OAuthService) bindIdentity(ctx context.Context, userUID string, gh *GitHubUser, reason, clientIP, userAgent string) error {
	now := model.Now()
	row := &model.OAuthIdentity{
		UID:            id.OAuthIdentity(),
		UserUID:        userUID,
		Provider:       model.ProviderGitHub,
		ProviderUserID: fmt.Sprintf("%d", gh.ID),
		ProviderLogin:  truncate(gh.Login, 191),
		ProviderEmail:  truncate(gh.Email, 255),
		AvatarURL:      truncate(gh.AvatarURL, 512),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.users.CreateIdentity(row); err != nil {
		if isDuplicateKey(err) {
			return NewError(response.CodeConflict, "该 GitHub 账号已绑定")
		}
		return Wrap(response.CodeInternal, "保存绑定失败", err)
	}

	s.audit.Log(ctx, AuditEntry{
		Type:       model.LogTypeUserUpdate,
		Status:     model.LogStatusSuccess,
		UserUID:    userUID,
		TargetType: "user",
		TargetUID:  userUID,
		Detail: map[string]any{
			"Action":        "oauth_bind",
			"Provider":      model.ProviderGitHub,
			"ProviderLogin": gh.Login,
			"ProviderEmail": gh.Email,
			"BindReason":    reason,
		},
		ClientIP:  clientIP,
		UserAgent: userAgent,
	})
	return nil
}

// refreshIdentity 同步 GitHub 侧可能变化的展示信息（登录时顺手更新）。
func (s *OAuthService) refreshIdentity(identity *model.OAuthIdentity, gh *GitHubUser) {
	if identity.ProviderLogin == gh.Login &&
		identity.ProviderEmail == gh.Email &&
		identity.AvatarURL == gh.AvatarURL {
		return
	}
	// 走 raw DB 更新，避免为此再加一个 repo 方法（绑定信息只在这里被刷新）
	s.users.UpdateIdentityDisplay(identity.UID, gh.Login, gh.Email, gh.AvatarURL)
}

// exchangeToken 用 code 换 access token。
func (s *OAuthService) exchangeToken(ctx context.Context, code string) (string, error) {
	form := url.Values{}
	form.Set("client_id", s.clientID())
	form.Set("client_secret", s.clientSecret())
	form.Set("code", code)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", Wrap(response.CodeInternal, "构造 GitHub 令牌请求失败", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "PicGo-Web")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", Wrap(response.CodeInternal, "请求 GitHub 令牌接口失败", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return "", Errorf(response.CodeInternal, "GitHub 令牌接口返回 %d", resp.StatusCode)
	}

	var payload struct {
		AccessToken      string `json:"access_token"`
		TokenType        string `json:"token_type"`
		Scope            string `json:"scope"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", Wrap(response.CodeInternal, "解析 GitHub 令牌响应失败", err)
	}
	if payload.Error != "" {
		return "", Errorf(response.CodeInternal, "GitHub 授权失败：%s", payload.Error)
	}
	if payload.AccessToken == "" {
		return "", NewError(response.CodeInternal, "GitHub 未返回访问令牌")
	}
	return payload.AccessToken, nil
}

// fetchUser 拉取 GitHub 用户信息。
func (s *OAuthService) fetchUser(ctx context.Context, token string) (*GitHubUser, error) {
	var gh GitHubUser
	if err := s.githubGet(ctx, token, "/user", &gh); err != nil {
		return nil, err
	}
	return &gh, nil
}

// fetchPrimaryEmail 拉取主邮箱（/user.email 为空时的兜底）。
func (s *OAuthService) fetchPrimaryEmail(ctx context.Context, token string) (string, error) {
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := s.githubGet(ctx, token, "/user/emails", &emails); err != nil {
		return "", err
	}
	// 优先「主邮箱且已验证」
	for _, e := range emails {
		if e.Primary && e.Verified {
			return e.Email, nil
		}
	}
	for _, e := range emails {
		if e.Verified {
			return e.Email, nil
		}
	}
	return "", errors.New("GitHub 未返回可用邮箱")
}

func (s *OAuthService) githubGet(ctx context.Context, token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiBase+path, nil)
	if err != nil {
		return Wrap(response.CodeInternal, "构造 GitHub 请求失败", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "PicGo-Web")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return Wrap(response.CodeInternal, "请求 GitHub 接口失败", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Errorf(response.CodeInternal, "GitHub 接口 %s 返回 %d", path, resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err := json.Unmarshal(body, out); err != nil {
		return Wrap(response.CodeInternal, "解析 GitHub 响应失败", err)
	}
	return nil
}
