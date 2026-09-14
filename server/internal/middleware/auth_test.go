package middleware

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/crypto"
	"github.com/YeqingKy/PicGo-Web/server/internal/database"
	"github.com/YeqingKy/PicGo-Web/server/internal/id"
	"github.com/YeqingKy/PicGo-Web/server/internal/logger"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

type authTestEnv struct {
	users  *repository.UserRepo
	tokens *repository.TokenRepo
	jwt    *auth.JWTManager
	mw     *Auth
	router *gin.Engine
}

func newAuthTestEnv(t *testing.T) *authTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := &config.Config{
		DBDriver:       config.DBDriverSQLite,
		SQLitePath:     filepath.Join(dir, "mw.db"),
		DataDir:        dir,
		DBMaxOpenConns: 1,
		DBMaxIdleConns: 1,
	}
	db, err := database.Open(cfg, log)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := database.Migrate(db, log); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	masterKey := make([]byte, crypto.KeySize)
	for i := range masterKey {
		masterKey[i] = byte(i + 3)
	}

	env := &authTestEnv{
		users:  repository.NewUserRepo(db.DB),
		tokens: repository.NewTokenRepo(db.DB),
		jwt:    auth.NewJWTManager(masterKey),
	}
	env.mw = NewAuth(env.users, env.tokens, env.jwt, log)

	r := gin.New()
	api := r.Group("/api")

	me := api.Group("", env.mw.RequireAuth())
	me.GET("/me", func(c *gin.Context) {
		response.OK(c, gin.H{"UID": CurrentUserUID(c), "Role": CurrentRole(c)})
	})

	admin := api.Group("/admin", env.mw.RequireAuth(), env.mw.RequireAdmin())
	admin.GET("/only", func(c *gin.Context) { response.OK(c, nil) })

	pc := api.Group("/pc", env.mw.RequireAuth(), env.mw.RequirePasswordChanged())
	pc.GET("/only", func(c *gin.Context) { response.OK(c, nil) })

	env.router = r
	return env
}

func (e *authTestEnv) createUser(t *testing.T, role, status string, mustChange bool) *model.User {
	t.Helper()
	now := model.Now()
	u := &model.User{
		UID:                id.User(),
		Email:              "u" + id.New() + "@example.com",
		PasswordHash:       "$2a$12$abcdefghijklmnopqrstuv",
		Role:               role,
		Status:             status,
		MustChangePassword: mustChange,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := e.users.Create(u); err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	return u
}

// do 发一个请求并返回状态码与解开的信封。
func (e *authTestEnv) do(t *testing.T, method, path string, mutate func(*http.Request)) (int, response.Envelope) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)

	var env response.Envelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	return w.Code, env
}

func TestRequireAuthAcceptsJWTHeader(t *testing.T) {
	env := newAuthTestEnv(t)
	u := env.createUser(t, model.UserRoleUser, model.UserStatusActive, false)

	token, _, err := env.jwt.Sign(u.UID, u.Role, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	code, body := env.do(t, http.MethodGet, "/api/me", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if code != http.StatusOK {
		t.Fatalf("应通过鉴权，实际 %d (%v)", code, body)
	}
	data, _ := body.Data.(map[string]any)
	if data["UID"] != u.UID {
		t.Errorf("CurrentUserUID 不正确: %v", data["UID"])
	}
}

func TestRequireAuthAcceptsCookie(t *testing.T) {
	env := newAuthTestEnv(t)
	u := env.createUser(t, model.UserRoleUser, model.UserStatusActive, false)

	token, _, err := env.jwt.Sign(u.UID, u.Role, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	code, body := env.do(t, http.MethodGet, "/api/me", func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: auth.CookieAccess, Value: token})
	})
	if code != http.StatusOK {
		t.Fatalf("Cookie 鉴权应通过，实际 %d (%v)", code, body)
	}
}

func TestRequireAuthAcceptsAPIToken(t *testing.T) {
	env := newAuthTestEnv(t)
	u := env.createUser(t, model.UserRoleUser, model.UserStatusActive, false)

	plain, hash, prefix, err := auth.GenerateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := env.tokens.CreateAPIToken(&model.APIToken{
		UID: id.APIToken(), UserUID: u.UID, Name: "ci",
		TokenHash: hash, Prefix: prefix, CreatedAt: model.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	code, body := env.do(t, http.MethodGet, "/api/me", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+plain)
	})
	if code != http.StatusOK {
		t.Fatalf("API token 鉴权应通过，实际 %d (%v)", code, body)
	}

	// 命中后应更新 LastUsedAt
	row, err := env.tokens.FindAPITokenByHash(hash)
	if err != nil {
		t.Fatal(err)
	}
	if row.LastUsedAt == 0 {
		t.Error("使用 API token 后应更新 LastUsedAt")
	}
}

func TestRequireAuthRejectsMissingCredentials(t *testing.T) {
	env := newAuthTestEnv(t)
	code, body := env.do(t, http.MethodGet, "/api/me", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("无凭据应 401，实际 %d", code)
	}
	if body.Code != response.CodeUnauthorized {
		t.Errorf("错误码应为 %d，实际 %d", response.CodeUnauthorized, body.Code)
	}
}

func TestRequireAuthRejectsBadCredentials(t *testing.T) {
	env := newAuthTestEnv(t)

	cases := []struct {
		name string
		auth string
		want response.Code
	}{
		{"垃圾 JWT", "Bearer not-a-jwt", response.CodeUnauthorized},
		{"未知 API token", "Bearer " + auth.APITokenPrefix + "deadbeef", response.CodeUnauthorized},
		{"非 Bearer 方案", "Basic abc", response.CodeUnauthorized},
		{"空 Bearer", "Bearer ", response.CodeUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := env.do(t, http.MethodGet, "/api/me", func(r *http.Request) {
				r.Header.Set("Authorization", tc.auth)
			})
			if code != http.StatusUnauthorized {
				t.Fatalf("应 401，实际 %d", code)
			}
			if body.Code != tc.want {
				t.Errorf("错误码应为 %d，实际 %d", tc.want, body.Code)
			}
		})
	}
}

func TestRequireAuthRejectsExpiredJWT(t *testing.T) {
	env := newAuthTestEnv(t)
	u := env.createUser(t, model.UserRoleUser, model.UserStatusActive, false)

	// 用一个已过期的令牌。
	//
	// 技巧：JWT 的 exp 精度到「秒」，Sign 会把 now+1ns 截断到当前秒，
	// 于是 exp <= 校验时刻，令牌必然已过期 —— 不必 sleep 也不依赖内部密钥。
	expired, _, err := env.jwt.Sign(u.UID, u.Role, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}

	code, body := env.do(t, http.MethodGet, "/api/me", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+expired)
	})
	if code != http.StatusUnauthorized {
		t.Fatalf("过期令牌应 401，实际 %d", code)
	}
	if body.Code != response.CodeTokenExpired {
		t.Errorf("错误码应为 %d（令牌过期），实际 %d", response.CodeTokenExpired, body.Code)
	}
}

func TestRequireAuthRejectsDisabledAccount(t *testing.T) {
	env := newAuthTestEnv(t)
	u := env.createUser(t, model.UserRoleUser, model.UserStatusActive, false)

	token, _, err := env.jwt.Sign(u.UID, u.Role, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// 签发后禁用账号：中间件按 UID 重新加载用户，应立即失效
	if err := env.users.UpdateFields(u.UID, map[string]any{"Status": model.UserStatusDisabled}); err != nil {
		t.Fatal(err)
	}

	code, body := env.do(t, http.MethodGet, "/api/me", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if code != http.StatusUnauthorized {
		t.Fatalf("禁用账号应 401，实际 %d", code)
	}
	if body.Code != response.CodeAccountDisabled {
		t.Errorf("错误码应为 %d（账号禁用），实际 %d", response.CodeAccountDisabled, body.Code)
	}
}

// TestRequireAuthReflectsRoleChangeImmediately 角色变更应立即生效（不复用 JWT 里的旧 claim）。
func TestRequireAuthReflectsRoleChangeImmediately(t *testing.T) {
	env := newAuthTestEnv(t)
	u := env.createUser(t, model.UserRoleUser, model.UserStatusActive, false)

	// 令牌里写的是 user
	token, _, err := env.jwt.Sign(u.UID, model.UserRoleUser, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	// 先访问 admin 路由 → 403
	code, body := env.do(t, http.MethodGet, "/api/admin/only", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if code != http.StatusForbidden || body.Code != response.CodeForbidden {
		t.Fatalf("普通用户应 403，实际 %d (%v)", code, body)
	}

	// 提权为 admin（令牌未变）
	if err := env.users.UpdateFields(u.UID, map[string]any{"Role": model.UserRoleAdmin}); err != nil {
		t.Fatal(err)
	}
	code, body = env.do(t, http.MethodGet, "/api/admin/only", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if code != http.StatusOK {
		t.Fatalf("提权后应立即生效（40301 说明错误地信任了 JWT 里的旧 role），实际 %d (%v)", code, body)
	}
}

func TestRequireAdminRejectsNonAdmin(t *testing.T) {
	env := newAuthTestEnv(t)
	u := env.createUser(t, model.UserRoleUser, model.UserStatusActive, false)

	token, _, err := env.jwt.Sign(u.UID, u.Role, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	code, body := env.do(t, http.MethodGet, "/api/admin/only", func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	if code != http.StatusForbidden {
		t.Fatalf("普通用户访问 admin 路由应 403，实际 %d", code)
	}
	if body.Code != response.CodeForbidden {
		t.Errorf("错误码应为 %d（权限不足，注意不是 40302 配额不足），实际 %d", response.CodeForbidden, body.Code)
	}
}

func TestRequirePasswordChangedGate(t *testing.T) {
	env := newAuthTestEnv(t)
	u := env.createUser(t, model.UserRoleAdmin, model.UserStatusActive, true)

	token, _, err := env.jwt.Sign(u.UID, u.Role, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	withToken := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }

	// 必须改密的账号：普通受保护接口放行（/api/me），业务接口拦截
	if code, _ := env.do(t, http.MethodGet, "/api/me", withToken); code != http.StatusOK {
		t.Errorf("/auth/me 应放行，实际 %d", code)
	}
	code, body := env.do(t, http.MethodGet, "/api/pc/only", withToken)
	if code != http.StatusForbidden {
		t.Fatalf("必须改密的账号访问业务接口应 403，实际 %d", code)
	}
	if body.Code != response.CodeForbidden {
		t.Errorf("错误码应为 %d，实际 %d", response.CodeForbidden, body.Code)
	}
	if body.Message == "" {
		t.Error("应给出可读提示（请先修改密码）")
	}

	// 改密后放行
	if err := env.users.UpdateFields(u.UID, map[string]any{"MustChangePassword": false}); err != nil {
		t.Fatal(err)
	}
	if code, _ := env.do(t, http.MethodGet, "/api/pc/only", withToken); code != http.StatusOK {
		t.Errorf("改密后应放行，实际 %d", code)
	}
}

func TestCurrentUserNilWhenUnauthenticated(t *testing.T) {
	// 未挂鉴权中间件的路由上，CurrentUser 应返回 nil 而不是 panic
	var got *model.User
	r := gin.New()
	r.GET("/open", func(c *gin.Context) {
		got = CurrentUser(c)
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/open", nil))

	if got != nil {
		t.Error("未鉴权时 CurrentUser 应返回 nil")
	}
}

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"  A@B.COM ": "a@b.com",
		"a@b.com":    "a@b.com",
		"":           "",
	}
	for in, want := range cases {
		if got := NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestContextCarriesUserUID 鉴权后应把 UID 注入 request context，供 service/日志关联。
func TestContextCarriesUserUID(t *testing.T) {
	env := newAuthTestEnv(t)
	u := env.createUser(t, model.UserRoleUser, model.UserStatusActive, false)

	token, _, err := env.jwt.Sign(u.UID, u.Role, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	var seen string
	r := gin.New()
	r.GET("/ctx", env.mw.RequireAuth(), func(c *gin.Context) {
		seen = logger.UserUID(c.Request.Context())
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ctx", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)

	if seen != u.UID {
		t.Errorf("request context 里的 UID 应为 %s，实际 %q", u.UID, seen)
	}
}
