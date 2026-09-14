package service

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/crypto"
	"github.com/YeqingKy/PicGo-Web/server/internal/database"
	"github.com/YeqingKy/PicGo-Web/server/internal/id"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// testEnv 是一套完整的 service 层测试环境（临时 SQLite + 真实仓储 + 真实服务）。
//
// 刻意不 mock 仓储：本工作流的重点就是「业务规则 + 事务 + 限流窗口」这类
// 与数据库行为强相关的逻辑，用真库才能测出真问题（临时库因而是文件级的，
// 但每个测试一个 TempDir，互不干扰）。
type testEnv struct {
	cfg      *config.Config
	log      *slog.Logger
	db       *database.DB
	settings *settings.Service

	users    *repository.UserRepo
	tokens   *repository.TokenRepo
	attempts *repository.LoginAttemptRepo
	logs     *repository.LogRepo

	audit    *AuditService
	tokenSvc *TokenService
	userSvc  *UserService
	jwt      *auth.JWTManager
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := &config.Config{
		DBDriver:       config.DBDriverSQLite,
		SQLitePath:     filepath.Join(dir, "service.db"),
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

	// 主密钥固定，便于断言
	masterKey := make([]byte, crypto.KeySize)
	for i := range masterKey {
		masterKey[i] = byte(i + 11)
	}
	cipher, err := crypto.New(masterKey)
	if err != nil {
		t.Fatalf("构造加密器失败: %v", err)
	}

	settingRepo := repository.NewSettingRepo(db.DB)
	settingsSvc, err := settings.New(settingRepo, cipher, log)
	if err != nil {
		t.Fatalf("构造配置服务失败: %v", err)
	}

	env := &testEnv{
		cfg:      cfg,
		log:      log,
		db:       db,
		settings: settingsSvc,
		users:    repository.NewUserRepo(db.DB),
		tokens:   repository.NewTokenRepo(db.DB),
		attempts: repository.NewLoginAttemptRepo(db.DB),
		logs:     repository.NewLogRepo(db.DB),
	}
	env.jwt = auth.NewJWTManager(masterKey)
	env.audit = NewAuditService(env.logs, log)
	env.tokenSvc = NewTokenService(settingsSvc, env.users, env.tokens, env.jwt, env.audit, log)
	env.userSvc = NewUserService(settingsSvc, env.users, env.tokens, env.attempts, env.audit, log)
	return env
}

// createUser 直接建一个用户（绕过 service，用于准备测试数据）。
func (e *testEnv) createUser(t *testing.T, email, password, role, status string) *model.User {
	t.Helper()

	hash := ""
	if password != "" {
		h, err := auth.HashPassword(password)
		if err != nil {
			t.Fatalf("哈希密码失败: %v", err)
		}
		hash = h
	}

	now := model.Now()
	u := &model.User{
		UID:           id.User(),
		Email:         email,
		PasswordHash:  hash,
		Role:          role,
		Status:        status,
		CapacityBytes: 1 << 30,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	profile := &model.UserProfile{
		UserUID:   u.UID,
		Nickname:  email,
		Locale:    "zh-CN",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := e.users.CreateWithProfile(u, profile); err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	return u
}

// logCount 统计某类型的操作日志条数（用于断言审计确实写入）。
func (e *testEnv) logCount(t *testing.T, logType string) int64 {
	t.Helper()
	var n int64
	if err := e.db.Model(&model.OperationLog{}).Where(map[string]any{"Type": logType}).Count(&n).Error; err != nil {
		t.Fatalf("统计操作日志失败: %v", err)
	}
	return n
}

// assertCode 断言错误码。
func assertCode(t *testing.T, err error, want response.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误码 %d，实际无错误", int(want))
	}
	if got := CodeOf(err); got != want {
		t.Fatalf("错误码不匹配：期望 %d，实际 %d（%v）", int(want), int(got), err)
	}
}
