package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/id"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// 默认有效期。对应 settings 的缺省值（config.Keys 中已注册）。
const (
	defaultAccessTTLMinutes = 15
	defaultRefreshTTLHours  = 168
)

// Issued 是一次成功签发的结果。
type Issued struct {
	AccessToken  string
	AccessTTL    time.Duration
	ExpiresIn    int64 // access token 有效秒数（响应里的 ExpiresIn）
	RefreshToken string
	RefreshTTL   time.Duration
}

// APITokenView 是 API token 的对外视图（**永不包含明文**）。
type APITokenView struct {
	UID        string `json:"UID"`
	Name       string `json:"Name"`
	Prefix     string `json:"Prefix"`
	LastUsedAt int64  `json:"LastUsedAt"`
	ExpiresAt  int64  `json:"ExpiresAt"` // 0 = 永不过期
	CreatedAt  int64  `json:"CreatedAt"`
}

// APITokenCreated 是创建 API token 时的响应。
//
// ⚠️ Token 字段的明文**只在此响应中出现一次**（D31）。
type APITokenCreated struct {
	UID       string `json:"UID"`
	Name      string `json:"Name"`
	Token     string `json:"Token"`
	Prefix    string `json:"Prefix"`
	ExpiresAt int64  `json:"ExpiresAt"`
	CreatedAt int64  `json:"CreatedAt"`
}

// TokenService 负责 access / refresh / API token 的签发、轮换与吊销。
type TokenService struct {
	settings *settings.Service
	users    *repository.UserRepo
	tokens   *repository.TokenRepo
	jwt      *auth.JWTManager
	audit    *AuditService
	log      *slog.Logger
}

// NewTokenService 构造。
func NewTokenService(
	st *settings.Service,
	users *repository.UserRepo,
	tokens *repository.TokenRepo,
	jwtMgr *auth.JWTManager,
	audit *AuditService,
	log *slog.Logger,
) *TokenService {
	return &TokenService{settings: st, users: users, tokens: tokens, jwt: jwtMgr, audit: audit, log: log}
}

// AccessTTL 返回 access token 有效期（settings:security.accessTokenTtlMinutes）。
func (s *TokenService) AccessTTL() time.Duration {
	minutes := s.settings.GetInt("security.accessTokenTtlMinutes", defaultAccessTTLMinutes)
	if minutes <= 0 {
		minutes = defaultAccessTTLMinutes
	}
	return time.Duration(minutes) * time.Minute
}

// RefreshTTL 返回 refresh token 有效期（settings:security.sessionTtlHours）。
func (s *TokenService) RefreshTTL() time.Duration {
	hours := s.settings.GetInt("security.sessionTtlHours", defaultRefreshTTLHours)
	if hours <= 0 {
		hours = defaultRefreshTTLHours
	}
	return time.Duration(hours) * time.Hour
}

// SecureCookies 判断是否给 Cookie 加 Secure 标志。
//
// 依据 site.baseUrl 是否为 https。未配置时按「同源 HTTP」处理（本地开发）。
func (s *TokenService) SecureCookies() bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s.settings.GetString("site.baseUrl"))), "https://")
}

// Issue 为用户签发一对新令牌（access + refresh）。
//
// refresh token 只把 SHA-256 落库（D30）。
func (s *TokenService) Issue(ctx context.Context, u *model.User, clientIP, userAgent string) (*Issued, error) {
	accessTTL := s.AccessTTL()
	access, expiresIn, err := s.jwt.Sign(u.UID, u.Role, accessTTL)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "签发访问令牌失败", err)
	}

	refreshPlain, refreshHash, err := auth.GenerateRefreshToken()
	if err != nil {
		return nil, Wrap(response.CodeInternal, "生成会话令牌失败", err)
	}

	refreshTTL := s.RefreshTTL()
	now := model.Now()
	row := &model.RefreshToken{
		UID:       id.RefreshToken(),
		UserUID:   u.UID,
		TokenHash: refreshHash,
		UserAgent: truncate(userAgent, 255),
		ClientIP:  truncate(clientIP, 64),
		ExpiresAt: now + int64(refreshTTL.Seconds()),
		CreatedAt: now,
	}
	if err := s.tokens.CreateRefresh(row); err != nil {
		return nil, Wrap(response.CodeInternal, "保存会话失败", err)
	}

	return &Issued{
		AccessToken:  access,
		AccessTTL:    accessTTL,
		ExpiresIn:    expiresIn,
		RefreshToken: refreshPlain,
		RefreshTTL:   refreshTTL,
	}, nil
}

// Refresh 用 refresh token **轮换**出新的一对令牌。
//
// 轮换语义：旧 refresh token 立即吊销（防重放，D30）。
//
// 注意：这里**不做**「检测到已吊销令牌就吊销该用户全部会话」的处理。
// 原因是前端在并发请求下可能同时触发两次刷新，第二次会用到刚被轮换掉的旧令牌；
// 若据此判定为攻击会把正常用户踢下线。改为返回 40103 让前端跳登录即可。
func (s *TokenService) Refresh(ctx context.Context, refreshPlain, clientIP, userAgent string) (*Issued, *model.User, error) {
	if strings.TrimSpace(refreshPlain) == "" {
		return nil, nil, NewError(response.CodeUnauthorized, "缺少会话凭据")
	}

	row, err := s.tokens.FindRefreshByHash(auth.HashToken(refreshPlain))
	if err != nil {
		if repository.IsNotFound(err) {
			return nil, nil, NewError(response.CodeUnauthorized, "会话不存在或已失效")
		}
		return nil, nil, Wrap(response.CodeInternal, "查询会话失败", err)
	}

	if row.RevokedAt != 0 {
		return nil, nil, NewError(response.CodeTokenExpired, "会话已失效，请重新登录")
	}
	if row.ExpiresAt <= model.Now() {
		return nil, nil, NewError(response.CodeTokenExpired, "会话已过期，请重新登录")
	}

	u, err := s.users.FindByUID(row.UserUID)
	if err != nil {
		if repository.IsNotFound(err) {
			return nil, nil, NewError(response.CodeUnauthorized, "账号不存在")
		}
		return nil, nil, Wrap(response.CodeInternal, "加载账号失败", err)
	}
	if !u.IsActive() {
		return nil, nil, NewError(response.CodeAccountDisabled, "")
	}

	// 先吊销旧的，再签发新的
	if err := s.tokens.RevokeRefreshByUID(row.UID, model.Now()); err != nil {
		return nil, nil, Wrap(response.CodeInternal, "轮换会话失败", err)
	}

	issued, err := s.Issue(ctx, u, clientIP, userAgent)
	if err != nil {
		return nil, nil, err
	}
	return issued, u, nil
}

// Logout 吊销给定 refresh token（幂等）。
func (s *TokenService) Logout(ctx context.Context, refreshPlain, clientIP, userAgent string) {
	if strings.TrimSpace(refreshPlain) == "" {
		return
	}
	row, err := s.tokens.FindRefreshByHash(auth.HashToken(refreshPlain))
	if err != nil {
		// 令牌不存在也算登出成功（幂等），只在内部错误时记日志
		if !repository.IsNotFound(err) {
			s.log.Warn("登出时查询会话失败", "err", err)
		}
		return
	}
	if err := s.tokens.RevokeRefreshByUID(row.UID, model.Now()); err != nil {
		s.log.Warn("吊销会话失败", "uid", row.UID, "err", err)
	}

	s.audit.Log(ctx, AuditEntry{
		Type:       model.LogTypeAuthLogout,
		Status:     model.LogStatusSuccess,
		UserUID:    row.UserUID,
		TargetType: "user",
		TargetUID:  row.UserUID,
		ClientIP:   clientIP,
		UserAgent:  userAgent,
	})
}

// RevokeAllForUser 吊销某用户全部有效会话（改密 / 重置密码 / 注销时用）。
func (s *TokenService) RevokeAllForUser(userUID string) error {
	if _, err := s.tokens.RevokeAllRefreshForUser(userUID, model.Now()); err != nil {
		return Wrap(response.CodeInternal, "吊销会话失败", err)
	}
	return nil
}

// ---- API Token（长期令牌，D31）----

// ListAPITokens 列出某用户的 API token（按创建时间倒序）。
func (s *TokenService) ListAPITokens(userUID string) ([]APITokenView, error) {
	rows, err := s.tokens.ListAPITokens(userUID)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "查询令牌失败", err)
	}
	out := make([]APITokenView, 0, len(rows))
	for _, r := range rows {
		out = append(out, toAPITokenView(r))
	}
	return out, nil
}

// CreateAPIToken 创建长期令牌，**明文只在返回值里出现一次**。
//
// expiresInDays <= 0 表示永不过期（ExpiresAt = 0）。
func (s *TokenService) CreateAPIToken(ctx context.Context, userUID, name string, expiresInDays int, clientIP, userAgent string) (*APITokenCreated, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, Errorf(response.CodeInvalidParam, "请填写令牌名称")
	}
	if len(name) > 64 {
		return nil, Errorf(response.CodeInvalidParam, "令牌名称过长（最多 64 字符）")
	}

	// 同一用户下的重名没有硬性危害，但会让用户难以区分，这里直接拒绝。
	existing, err := s.tokens.ListAPITokens(userUID)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "查询令牌失败", err)
	}
	for _, e := range existing {
		if strings.EqualFold(e.Name, name) {
			return nil, Errorf(response.CodeConflict, "已存在同名令牌：%s", name)
		}
	}

	plain, hash, prefix, err := auth.GenerateAPIToken()
	if err != nil {
		return nil, Wrap(response.CodeInternal, "生成令牌失败", err)
	}

	now := model.Now()
	var expiresAt int64
	if expiresInDays > 0 {
		expiresAt = now + int64(expiresInDays)*86400
	}

	row := &model.APIToken{
		UID:       id.APIToken(),
		UserUID:   userUID,
		Name:      name,
		TokenHash: hash,
		Prefix:    prefix,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}
	if err := s.tokens.CreateAPIToken(row); err != nil {
		return nil, Wrap(response.CodeInternal, "保存令牌失败", err)
	}

	// 审计里**绝不记录明文令牌**
	s.audit.Log(ctx, AuditEntry{
		Type:       model.LogTypeSettingUpdate,
		Status:     model.LogStatusSuccess,
		UserUID:    userUID,
		TargetType: "api_token",
		TargetUID:  row.UID,
		Detail:     map[string]any{"Action": "create", "Name": name, "Prefix": prefix, "ExpiresAt": expiresAt},
		ClientIP:   clientIP,
		UserAgent:  userAgent,
	})

	return &APITokenCreated{
		UID:       row.UID,
		Name:      row.Name,
		Token:     plain,
		Prefix:    row.Prefix,
		ExpiresAt: row.ExpiresAt,
		CreatedAt: row.CreatedAt,
	}, nil
}

// DeleteAPIToken 吊销某用户自己的令牌。
func (s *TokenService) DeleteAPIToken(ctx context.Context, userUID, tokenUID, clientIP, userAgent string) error {
	if strings.TrimSpace(tokenUID) == "" {
		return Errorf(response.CodeInvalidParam, "缺少令牌标识")
	}
	// 先查出来（用于审计与「存在性」判断），且限定在本人范围内防越权
	row, err := s.tokens.GetAPITokenForUser(userUID, tokenUID)
	if err != nil {
		if repository.IsNotFound(err) {
			return NewError(response.CodeNotFound, "令牌不存在")
		}
		return Wrap(response.CodeInternal, "查询令牌失败", err)
	}

	if _, err := s.tokens.DeleteAPITokenForUser(userUID, tokenUID); err != nil {
		return Wrap(response.CodeInternal, "删除令牌失败", err)
	}

	s.audit.Log(ctx, AuditEntry{
		Type:       model.LogTypeSettingUpdate,
		Status:     model.LogStatusSuccess,
		UserUID:    userUID,
		TargetType: "api_token",
		TargetUID:  row.UID,
		Detail:     map[string]any{"Action": "delete", "Name": row.Name, "Prefix": row.Prefix},
		ClientIP:   clientIP,
		UserAgent:  userAgent,
	})
	return nil
}

func toAPITokenView(r model.APIToken) APITokenView {
	return APITokenView{
		UID:        r.UID,
		Name:       r.Name,
		Prefix:     r.Prefix,
		LastUsedAt: r.LastUsedAt,
		ExpiresAt:  r.ExpiresAt,
		CreatedAt:  r.CreatedAt,
	}
}
