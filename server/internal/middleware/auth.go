package middleware

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/logger"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// ginKeyUser 是在 gin.Context 中存放当前用户的键。
const ginKeyUser = "auth.currentUser"

// apiTokenTouchIntervalSeconds 限制 API token 的 LastUsedAt 写库频率。
//
// 每个请求都写库会放大写压力（SQLite 更是单写），因此只在距上次更新
// 超过该间隔时才落库。代价是最新的「最后使用时间」最多滞后 1 分钟，可接受。
const apiTokenTouchIntervalSeconds = 60

// Auth 是鉴权中间件工厂。
//
// 每次请求都会**按 UID 从数据库重新加载用户**，而不是只信 JWT 里的 claim：
// 这样改角色、禁用账号、改密后强制重登都能立即生效（见 API.md §1 的约定）。
type Auth struct {
	users  *repository.UserRepo
	tokens *repository.TokenRepo
	jwt    *auth.JWTManager
	log    *slog.Logger
}

// NewAuth 构造鉴权中间件。
func NewAuth(users *repository.UserRepo, tokens *repository.TokenRepo, jwtMgr *auth.JWTManager, log *slog.Logger) *Auth {
	return &Auth{users: users, tokens: tokens, jwt: jwtMgr, log: log}
}

// RequireAuth 要求已登录。失败时以 401 系列错误码中断请求。
func (a *Auth) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		u, code := a.authenticate(c)
		if u == nil {
			if code == response.CodeOK {
				code = response.CodeUnauthorized
			}
			response.Abort(c, code)
			return
		}

		c.Set(ginKeyUser, u)
		// 把 UID 放进 request context，便于 service/logger 关联日志
		ctx := logger.WithUserUID(c.Request.Context(), u.UID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// RequireAdmin 要求当前用户是管理员。
//
// 必须放在 RequireAuth 之后（依赖其写入的当前用户）。
func (a *Auth) RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		u := CurrentUser(c)
		if u == nil {
			response.Abort(c, response.CodeUnauthorized)
			return
		}
		if !u.IsAdmin() {
			response.Abort(c, response.CodeForbidden)
			return
		}
		c.Next()
	}
}

// RequirePasswordChanged 拦截「必须改密」的账号（D32）。
//
// 只放行 /auth/me、/auth/password、/auth/logout（这三条路由不挂本中间件），
// 其余一律 40301 + 「请先修改密码」，前端据此跳 /first-login。
func (a *Auth) RequirePasswordChanged() gin.HandlerFunc {
	return func(c *gin.Context) {
		if u := CurrentUser(c); u != nil && u.MustChangePassword {
			response.AbortMsg(c, response.CodeForbidden, "请先修改密码")
			return
		}
		c.Next()
	}
}

// authenticate 按 D30 的顺序解析身份：
//
//  1. `Authorization: Bearer <jwt>`
//  2. `Authorization: Bearer pcw_<token>`（API token）
//  3. Cookie `pcw_at`
//
// 返回值 code 为 CodeOK(0) 表示成功；否则是应返回给客户端的错误码。
func (a *Auth) authenticate(c *gin.Context) (*model.User, response.Code) {
	if bearer := auth.BearerToken(c); bearer != "" {
		if auth.IsAPIToken(bearer) {
			return a.userByAPIToken(bearer)
		}
		return a.userByJWT(bearer)
	}

	if cookie := auth.AccessCookie(c); cookie != "" {
		return a.userByJWT(cookie)
	}

	return nil, response.CodeUnauthorized
}

// userByJWT 校验 access token 并加载用户。
func (a *Auth) userByJWT(token string) (*model.User, response.Code) {
	claims, err := a.jwt.Verify(token)
	if err != nil {
		if errors.Is(err, auth.ErrTokenExpired) {
			return nil, response.CodeTokenExpired
		}
		return nil, response.CodeUnauthorized
	}
	return a.loadUser(claims.Subject)
}

// userByAPIToken 校验长期令牌并加载用户。
func (a *Auth) userByAPIToken(plain string) (*model.User, response.Code) {
	t, err := a.tokens.FindAPITokenByHash(auth.HashToken(plain))
	if err != nil {
		if repository.IsNotFound(err) {
			return nil, response.CodeUnauthorized
		}
		a.log.Error("查询 API token 失败", "err", err)
		return nil, response.CodeInternal
	}
	if !t.Valid() {
		// 过期与已吊销都归为「令牌已过期」，前端据此静默刷新或跳登录
		return nil, response.CodeTokenExpired
	}

	u, code := a.loadUser(t.UserUID)
	if u == nil {
		return nil, code
	}

	// 节流更新 LastUsedAt；失败不影响鉴权
	if now := model.Now(); now-t.LastUsedAt >= apiTokenTouchIntervalSeconds {
		if err := a.tokens.TouchAPIToken(t.ID, now); err != nil {
			a.log.Debug("更新 API token 最后使用时间失败", "uid", t.UID, "err", err)
		}
	}
	return u, response.CodeOK
}

// loadUser 加载用户并做状态校验。
func (a *Auth) loadUser(uid string) (*model.User, response.Code) {
	u, err := a.users.FindByUID(uid)
	if err != nil {
		if repository.IsNotFound(err) {
			// 令牌有效但账号已不存在（被删除）→ 视为未登录
			return nil, response.CodeUnauthorized
		}
		a.log.Error("加载当前用户失败", "uid", uid, "err", err)
		return nil, response.CodeInternal
	}
	if !u.IsActive() {
		return nil, response.CodeAccountDisabled
	}
	return u, response.CodeOK
}

// CurrentUser 返回当前登录用户；未登录返回 nil。
func CurrentUser(c *gin.Context) *model.User {
	v, ok := c.Get(ginKeyUser)
	if !ok {
		return nil
	}
	u, _ := v.(*model.User)
	return u
}

// CurrentUserUID 返回当前用户 UID；未登录返回空串。
func CurrentUserUID(c *gin.Context) string {
	if u := CurrentUser(c); u != nil {
		return u.UID
	}
	return ""
}

// CurrentRole 返回当前用户角色；未登录返回空串。
func CurrentRole(c *gin.Context) string {
	if u := CurrentUser(c); u != nil {
		return u.Role
	}
	return ""
}

// NormalizeEmail 规范邮箱：去空白 + 转小写。
//
// Users.Email 上有唯一索引，若不规范化会出现 `A@b.c` 与 `a@b.c` 两个账号。
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
