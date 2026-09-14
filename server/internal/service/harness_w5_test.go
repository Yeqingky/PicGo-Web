package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/agent"
	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/crypto"
	"github.com/YeqingKy/PicGo-Web/server/internal/database"
	"github.com/YeqingKy/PicGo-Web/server/internal/events"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// w5Env 是 W5/W6 的完整测试环境（临时 SQLite + 真实仓储 + 真实服务 + mock agent）。
//
// 刻意**不 mock 仓储**：本工作流的重点就是「业务规则 + 事务 + 并发队列」，
// 用真库才能测出真问题（每个测试一个 TempDir，互不干扰）。
type w5Env struct {
	t   *testing.T
	cfg *config.Config
	log *slog.Logger
	db  *database.DB

	settings *settings.Service
	cipher   *crypto.Cipher
	hub      *events.Hub
	agent    *agent.MockClient

	users   *repository.UserRepo
	tokens  *repository.TokenRepo
	uploads *repository.UploadRepo
	albums  *repository.AlbumRepo
	jobs    *repository.JobRepo
	logs    *repository.LogRepo
	emails  *repository.EmailLogRepo
	stores  *repository.StorageConfigRepo

	audit    *AuditService
	storage  *StorageService
	upload   *UploadService
	gallery  *GalleryService
	albumSvc *AlbumService
	jobSvc   *JobService
	logSvc   *LogService
	emailSvc *EmailService
}

// w5Options 用于按测试需要调整 mock agent 的行为。
type w5Options struct {
	failPaths             []string
	failTargets           []string
	remoteDeleteSupported bool
	uploadDelay           time.Duration
}

// newW5Env 构造测试环境。
func newW5Env(t *testing.T, opts ...func(*w5Options)) *w5Env {
	t.Helper()

	o := &w5Options{}
	for _, fn := range opts {
		fn(o)
	}

	storageSeq = 0

	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := &config.Config{
		DBDriver:       config.DBDriverSQLite,
		SQLitePath:     filepath.Join(dir, "w5.db"),
		DataDir:        dir,
		ThemesDir:      filepath.Join(dir, "themes"),
		DBMaxOpenConns: 1,
		DBMaxIdleConns: 1,
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("创建数据目录失败: %v", err)
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

	// 固定主密钥，便于断言
	masterKey := make([]byte, crypto.KeySize)
	for i := range masterKey {
		masterKey[i] = byte(i + 29)
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

	mockAgent := agent.NewMock(agent.MockConfig{
		TempDir:               filepath.Join(dir, "agent"),
		FailPaths:             o.failPaths,
		FailTargets:           o.failTargets,
		RemoteDeleteSupported: o.remoteDeleteSupported,
		UploadDelay:           o.uploadDelay,
	})

	hub := events.New(nil)

	env := &w5Env{
		t: t, cfg: cfg, log: log, db: db,
		settings: settingsSvc, cipher: cipher, hub: hub, agent: mockAgent,
		users:   repository.NewUserRepo(db.DB),
		tokens:  repository.NewTokenRepo(db.DB),
		uploads: repository.NewUploadRepo(db.DB),
		albums:  repository.NewAlbumRepo(db.DB),
		jobs:    repository.NewJobRepo(db.DB),
		logs:    repository.NewLogRepo(db.DB),
		emails:  repository.NewEmailLogRepo(db.DB),
		stores:  repository.NewStorageConfigRepo(db.DB),
	}
	env.audit = NewAuditService(env.logs, log)
	env.storage = NewStorageService(cfg, log, settingsSvc, cipher, env.stores, mockAgent, env.audit)
	env.albumSvc = NewAlbumService(log, env.albums, env.uploads, env.audit)
	env.upload = NewUploadService(cfg, log, settingsSvc, env.uploads, env.jobs, env.users,
		env.albums, env.storage, mockAgent, hub, env.audit)
	env.gallery = NewGalleryService(cfg, log, settingsSvc, env.uploads, env.albums, env.users,
		env.storage, mockAgent, hub, env.audit)
	env.jobSvc = NewJobService(log, env.jobs, env.uploads, env.audit)
	env.logSvc = NewLogService(log, env.logs, env.emails)
	env.emailSvc = NewEmailService(cfg, log, settingsSvc, env.users, env.tokens, settingRepo,
		env.emails, env.audit)

	t.Cleanup(func() {
		hub.Close()
	})
	return env
}

// startQueue 启动上传队列（测试里显式调用，避免每个用例都拉起 goroutine）。
func (e *w5Env) startQueue() {
	e.t.Helper()
	if err := e.upload.Start(context.Background()); err != nil {
		e.t.Fatalf("启动上传队列失败: %v", err)
	}
	e.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		e.upload.Shutdown(ctx)
	})
}

// makeUser 建一个用户（可选 capacity：nil = 不限额）。
func (e *w5Env) makeUser(uid, email, role string, capacity *int64) *model.User {
	e.t.Helper()

	var capBytes int64
	if capacity != nil {
		capBytes = *capacity
	}
	now := model.Now()
	u := &model.User{
		UID:           uid,
		Email:         email,
		PasswordHash:  "$2a$10$fakefakefakefakefakefa",
		Role:          role,
		Status:        model.UserStatusActive,
		CapacityBytes: capBytes,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := e.users.Create(u); err != nil {
		e.t.Fatalf("创建用户失败: %v", err)
	}
	if err := e.users.SaveProfile(&model.UserProfile{
		UserUID: uid, Nickname: email, Locale: "zh-CN",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		e.t.Fatalf("创建用户 Profile 失败: %v", err)
	}
	return u
}

// storageSeq 保证同一个测试里多次调用 makeStorage 时 PicgoConfigName 唯一
// （D64：(Type, PicgoConfigName) 必须唯一；同一个 picgo 配置项不能被两条记录映射）。
var storageSeq int

// makeStorage 建一条存储配置（Type=github，带凭据）。
//
// PicgoConfigName 自动递增（Default / Default-1 / Default-2 …），
// 与 picgo 自己的配置命名规则一致。
func (e *w5Env) makeStorage(name string, isDefault bool) *StorageConfigView {
	e.t.Helper()

	storageSeq++
	picgoName := "Default"
	if storageSeq > 1 {
		picgoName = fmt.Sprintf("Default-%d", storageSeq-1)
	}

	enabled := true
	v, err := e.storage.Create(context.Background(), CreateStorageInput{
		Name:            name,
		Type:            "github",
		PicgoConfigName: picgoName,
		Enabled:         &enabled,
		IsDefault:       isDefault,
		PathTemplate:    "img/{Y}/{m}",
		FileTemplate:    "{filename}-{md5-8}{extname}",
		Config: map[string]any{
			"repo":   "org/" + name,
			"branch": "main",
			"token":  "ghp_secret_token_value",
			"path":   "img/",
		},
	}, "usr_admin", "127.0.0.1", "test-agent")
	if err != nil {
		e.t.Fatalf("创建存储配置失败: %v", err)
	}
	return v
}

// writeTempFile 在暂存区写一个假图片文件。
func (e *w5Env) writeTempFile(name string, size int) IncomingFile {
	e.t.Helper()

	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	f, err := e.upload.SaveIncomingFile(name, data)
	if err != nil {
		e.t.Fatalf("写暂存文件失败: %v", err)
	}
	return f
}

// enqueueAndWait 提交一批上传并等待队列处理完（返回最终 job 视图）。
func (e *w5Env) enqueueAndWait(user *model.User, files []IncomingFile, storageUID string) *JobView {
	e.t.Helper()

	res, err := e.upload.EnqueueBatch(context.Background(), user, EnqueueBatchInput{
		Files:      files,
		StorageUID: storageUID,
	})
	if err != nil {
		e.t.Fatalf("入队失败: %v", err)
	}
	return e.waitJob(res.JobUID, 10*time.Second)
}

// waitJob 轮询直到 job 结束或超时。
func (e *w5Env) waitJob(jobUID string, timeout time.Duration) *JobView {
	e.t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		view, err := e.jobSvc.Get(jobUID, adminViewer())
		if err == nil && view.Status != model.JobStatusQueued && view.Status != model.JobStatusRunning {
			return view
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("等待任务 %s 结束超时", jobUID)
	return nil
}

// adminViewer 返回一个管理员视角的用户对象（用于服务层调用）。
func adminViewer() *model.User {
	return &model.User{
		UID: "usr_admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive,
	}
}

// ---- 常用断言助手 ----

// wantOK 断言成功。
func wantOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，实际错误: %v", err)
	}
}

// wantCode 断言错误码（err 为 nil 或码不符都会失败）。
func wantCode(t *testing.T, err error, want response.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误码 %d，实际成功", want)
	}
	if got := CodeOf(err); got != want {
		t.Fatalf("错误码不符：期望 %d(%s)，实际 %d(%s) — %v",
			want, want.Message(), got, got.Message(), err)
	}
}

// wantNoErr 是 wantOK 的别名（语义更直观处使用）。
func wantNoErr(t *testing.T, err error) { wantOK(t, err) }

// findUpload 从图库列表里按 UID 找一条。
func (e *w5Env) findUpload(uid string) *UploadView {
	e.t.Helper()
	v, err := e.gallery.Get(uid, adminViewer())
	if err != nil {
		e.t.Fatalf("查询图片 %s 失败: %v", uid, err)
	}
	return v
}
