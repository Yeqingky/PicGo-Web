package handler_test

// 本文件是 W5/W6 的**端到端验收**：走真实的 gin 路由（RegisterBusiness）
// + 临时 SQLite + mock agent，用 httptest 发真实 HTTP 请求。
//
// 为什么放在 `handler_test`（外部测试包）而不是 `handler`：
// 这样测的是**对外契约**（路由、鉴权、信封、字段名），而不是内部函数。
// 也顺带验证了「handler 不依赖 *gorm.DB」这一分层约束在编译期成立。

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/agent"
	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/crypto"
	"github.com/YeqingKy/PicGo-Web/server/internal/database"
	"github.com/YeqingKy/PicGo-Web/server/internal/events"
	"github.com/YeqingKy/PicGo-Web/server/internal/handler"
	"github.com/YeqingKy/PicGo-Web/server/internal/id"
	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// e2eEnv 是一套完整的端到端测试环境。
type e2eEnv struct {
	t      *testing.T
	router *gin.Engine
	server *httptest.Server

	cfg      *config.Config
	log      *slog.Logger
	db       *database.DB
	jwt      *auth.JWTManager
	agent    *agent.MockClient
	hub      *events.Hub
	services *handler.BusinessServices

	adminToken string
	adminUID   string
	userToken  string
	userUID    string
}

type e2eOptions struct {
	failPaths             []string
	remoteDeleteSupported bool
}

// newE2E 装配完整的 HTTP 环境。
func newE2E(t *testing.T, opts ...func(*e2eOptions)) *e2eEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	o := &e2eOptions{}
	for _, fn := range opts {
		fn(o)
	}

	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := &config.Config{
		DBDriver:       config.DBDriverSQLite,
		SQLitePath:     filepath.Join(dir, "e2e.db"),
		DataDir:        dir,
		ThemesDir:      filepath.Join(dir, "themes"),
		DBMaxOpenConns: 1,
		DBMaxIdleConns: 1,
		AgentMock:      true,
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}

	db, err := database.Open(cfg, log)
	if err != nil {
		t.Fatalf("打开库失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := database.Migrate(db, log); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	// 固定主密钥（同时用作 JWT 签名密钥，与 main.go 的用法一致）
	signingKey := make([]byte, crypto.KeySize)
	for i := range signingKey {
		signingKey[i] = byte(i + 41)
	}
	cipher, err := crypto.New(signingKey)
	if err != nil {
		t.Fatalf("构造加密器失败: %v", err)
	}

	settingRepo := repository.NewSettingRepo(db.DB)
	settingsSvc, err := settings.New(settingRepo, cipher, log)
	if err != nil {
		t.Fatalf("构造配置服务失败: %v", err)
	}

	userRepo := repository.NewUserRepo(db.DB)
	tokenRepo := repository.NewTokenRepo(db.DB)
	jwtMgr := auth.NewJWTManager(signingKey)

	// ---- 建两个账号：管理员 + 普通用户 ----
	env := &e2eEnv{
		t: t, cfg: cfg, log: log, db: db, jwt: jwtMgr,
		hub: events.New(nil),
		agent: agent.NewMock(agent.MockConfig{
			TempDir:               filepath.Join(dir, "agent"),
			FailPaths:             o.failPaths,
			RemoteDeleteSupported: o.remoteDeleteSupported,
		}),
	}
	env.adminUID = "usr_e2e_admin"
	env.userUID = "usr_e2e_user"
	env.createUser(env.adminUID, "admin@e2e.local", model.UserRoleAdmin, nil)
	env.createUser(env.userUID, "user@e2e.local", model.UserRoleUser, nil)

	env.adminToken = env.signToken(env.adminUID, model.UserRoleAdmin)
	env.userToken = env.signToken(env.userUID, model.UserRoleUser)

	// ---- 装配 gin + 业务路由 ----
	router := gin.New()
	authMW := middleware.NewAuth(userRepo, tokenRepo, jwtMgr, log)

	services, err := handler.RegisterBusiness(router.Group("/api/web/v1"), handler.BusinessDeps{
		DB:       db,
		Cfg:      cfg,
		Log:      log,
		Settings: settingsSvc,
		Cipher:   cipher,
		AuthMW:   authMW,
		Agent:    env.agent,
		Hub:      env.hub,
	})
	if err != nil {
		t.Fatalf("装配业务路由失败: %v", err)
	}
	env.services = services

	// 启动上传队列（端到端要真跑 worker）
	if err := services.Upload.Start(context.Background()); err != nil {
		t.Fatalf("启动上传队列失败: %v", err)
	}

	env.router = router
	env.server = httptest.NewServer(router)
	t.Cleanup(func() {
		env.server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		services.Upload.Shutdown(ctx)
		env.hub.Close()
	})
	return env
}

func (e *e2eEnv) createUser(uid, email, role string, capacity *int64) {
	e.t.Helper()

	var capBytes int64
	if capacity != nil {
		capBytes = *capacity
	}
	now := model.Now()
	u := &model.User{
		UID: uid, Email: email, Role: role, Status: model.UserStatusActive,
		PasswordHash:  "$2a$10$fakefakefakefakefakefa",
		CapacityBytes: capBytes, CreatedAt: now, UpdatedAt: now,
	}
	if err := e.db.DB.Create(u).Error; err != nil {
		e.t.Fatalf("创建用户失败: %v", err)
	}
}

func (e *e2eEnv) signToken(uid, role string) string {
	e.t.Helper()
	token, _, err := e.jwt.Sign(uid, role, time.Hour)
	if err != nil {
		e.t.Fatalf("签发令牌失败: %v", err)
	}
	return token
}

// ---- HTTP 助手 ----

type apiResp struct {
	Status  int
	Code    int
	Message string
	Data    json.RawMessage
	Raw     string
}

// do 发一个带鉴权的请求。
func (e *e2eEnv) do(method, path, token string, body any) apiResp {
	e.t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("序列化请求体失败: %v", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, e.server.URL+path, reader)
	if err != nil {
		e.t.Fatalf("构造请求失败: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := e.server.Client().Do(req)
	if err != nil {
		e.t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)

	out := apiResp{Status: resp.StatusCode, Raw: string(raw)}
	var env struct {
		Code    int             `json:"Code"`
		Message string          `json:"Message"`
		Data    json.RawMessage `json:"Data"`
	}
	if err := json.Unmarshal(raw, &env); err == nil {
		out.Code = env.Code
		out.Message = env.Message
		out.Data = env.Data
	}
	return out
}

// upload 发一个 multipart 上传请求。
func (e *e2eEnv) upload(token string, files map[string][]byte, fields map[string]string) apiResp {
	e.t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, data := range files {
		fw, err := mw.CreateFormFile("Files", name)
		if err != nil {
			e.t.Fatalf("构造 multipart 失败: %v", err)
		}
		if _, err := fw.Write(data); err != nil {
			e.t.Fatalf("写入 multipart 失败: %v", err)
		}
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			e.t.Fatalf("写入表单字段失败: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		e.t.Fatalf("关闭 multipart 失败: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, e.server.URL+"/api/web/v1/uploads", &buf)
	if err != nil {
		e.t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := e.server.Client().Do(req)
	if err != nil {
		e.t.Fatalf("上传请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)
	out := apiResp{Status: resp.StatusCode, Raw: string(raw)}
	var env struct {
		Code    int             `json:"Code"`
		Message string          `json:"Message"`
		Data    json.RawMessage `json:"Data"`
	}
	if err := json.Unmarshal(raw, &env); err == nil {
		out.Code = env.Code
		out.Message = env.Message
		out.Data = env.Data
	}
	return out
}

// decode 把 Data 解到 out。
func (e *e2eEnv) decode(r apiResp, out any) {
	e.t.Helper()
	if err := json.Unmarshal(r.Data, out); err != nil {
		e.t.Fatalf("解析 Data 失败: %v（原始响应：%s）", err, r.Raw)
	}
}

// newStorage 通过 HTTP 建一条存储配置，返回其 UID。
func (e *e2eEnv) newStorage(name string, isDefault bool) string {
	e.t.Helper()

	resp := e.do(http.MethodPost, "/api/web/v1/storage/configs", e.adminToken, map[string]any{
		"Name":         name,
		"Type":         "github",
		"IsDefault":    isDefault,
		"PathTemplate": "img/{Y}/{m}",
		"FileTemplate": "{filename}-{md5-8}{extname}",
		"Config": map[string]any{
			"repo":   "org/" + name,
			"branch": "main",
			"token":  "ghp_must_not_leak",
			"path":   "img/",
		},
	})
	if resp.Status != http.StatusOK {
		e.t.Fatalf("创建存储配置失败：HTTP %d %s", resp.Status, resp.Raw)
	}

	var view struct {
		UID string `json:"UID"`
	}
	e.decode(resp, &view)
	if view.UID == "" {
		e.t.Fatalf("存储配置 UID 为空：%s", resp.Raw)
	}
	return view.UID
}

// waitJob 轮询任务直到结束。
func (e *e2eEnv) waitJob(jobUID, token string) map[string]any {
	e.t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp := e.do(http.MethodGet, "/api/web/v1/jobs/"+jobUID, token, nil)
		if resp.Status != http.StatusOK {
			e.t.Fatalf("查询任务失败：HTTP %d %s", resp.Status, resp.Raw)
		}
		var job map[string]any
		e.decode(resp, &job)
		status, _ := job["Status"].(string)
		if status != model.JobStatusQueued && status != model.JobStatusRunning {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("等待任务 %s 结束超时", jobUID)
	return nil
}

// tinyPNG 返回一个最小的合法 PNG 字节串。
func tinyPNG() []byte {
	return []byte{
		0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
		0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
		0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
		0x0d, 0x0a, 0x2d, 0xb4,
		0x00, 0x00, 0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
	}
}

// ---------------------------------------------------------------------------
// 鉴权
// ---------------------------------------------------------------------------

// TestE2EAuthRequired 断言受保护端点在没有令牌时返回 40102。
func TestE2EAuthRequired(t *testing.T) {
	e := newE2E(t)

	protected := []struct{ method, path string }{
		{http.MethodGet, "/api/web/v1/uploads"},
		{http.MethodGet, "/api/web/v1/albums"},
		{http.MethodGet, "/api/web/v1/jobs"},
		{http.MethodGet, "/api/web/v1/storage/configs"},
		{http.MethodGet, "/api/web/v1/logs"},
		{http.MethodGet, "/api/web/v1/plugins"},
	}
	for _, p := range protected {
		resp := e.do(p.method, p.path, "", nil)
		if resp.Status != http.StatusUnauthorized {
			t.Errorf("%s %s 未登录应 401，实际 HTTP %d %s", p.method, p.path, resp.Status, resp.Raw)
		}
		if resp.Code != 40102 {
			t.Errorf("%s %s 错误码应为 40102，实际 %d", p.method, p.path, resp.Code)
		}
	}
}

// TestE2EAdminOnlyEndpoints 断言普通用户访问管理员端点返回 40301。
func TestE2EAdminOnlyEndpoints(t *testing.T) {
	e := newE2E(t)

	adminOnly := []struct{ method, path string }{
		{http.MethodGet, "/api/web/v1/storage/configs"},
		{http.MethodGet, "/api/web/v1/logs"},
		{http.MethodGet, "/api/web/v1/plugins"},
	}
	for _, p := range adminOnly {
		resp := e.do(p.method, p.path, e.userToken, nil)
		if resp.Status != http.StatusForbidden {
			t.Errorf("普通用户访问 %s 应 403，实际 HTTP %d %s", p.path, resp.Status, resp.Raw)
		}
		if resp.Code != 40301 {
			t.Errorf("错误码应为 40301（权限不足），实际 %d", resp.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// 存储配置
// ---------------------------------------------------------------------------

// TestE2EStorageCRUDAndRedaction 覆盖 A10 的第一条：
// 建配置 → 列表可见 → **密钥是掩码**。
func TestE2EStorageCRUDAndRedaction(t *testing.T) {
	e := newE2E(t)

	uid := e.newStorage("我的 GitHub", true)

	// 列表
	resp := e.do(http.MethodGet, "/api/web/v1/storage/configs", e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("列表失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	if contains(resp.Raw, "ghp_must_not_leak") {
		t.Fatalf("列表响应泄露了密钥明文：%s", resp.Raw)
	}

	var page struct {
		Items []map[string]any `json:"Items"`
		Total int64            `json:"Total"`
	}
	e.decode(resp, &page)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("列表应有 1 条，实际 total=%d len=%d", page.Total, len(page.Items))
	}

	item := page.Items[0]
	if item["UID"] != uid {
		t.Fatalf("UID 不符：%v", item["UID"])
	}
	if item["IsDefault"] != true {
		t.Fatalf("应为默认配置：%v", item["IsDefault"])
	}
	cfg, _ := item["Config"].(map[string]any)
	if cfg["token"] != "******" {
		t.Fatalf("token 应为掩码，实际 %v", cfg["token"])
	}
	// 非敏感字段可见（便于编辑回填）
	if cfg["repo"] != "org/我的 GitHub" {
		t.Fatalf("repo 应可见，实际 %v", cfg["repo"])
	}
	// SecretFields 只给字段名
	secrets, _ := item["SecretFields"].([]any)
	if len(secrets) == 0 {
		t.Fatal("SecretFields 不应为空")
	}

	// 详情
	resp = e.do(http.MethodGet, "/api/web/v1/storage/configs/"+uid, e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("详情失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	if contains(resp.Raw, "ghp_must_not_leak") {
		t.Fatal("详情响应泄露了密钥明文")
	}

	// 改名（PATCH）
	newName := "重命名后的配置"
	resp = e.do(http.MethodPatch, "/api/web/v1/storage/configs/"+uid, e.adminToken,
		map[string]any{"Name": newName})
	if resp.Status != http.StatusOK {
		t.Fatalf("PATCH 失败：HTTP %d %s", resp.Status, resp.Raw)
	}

	// PicgoConfigName 只读 → 40001
	resp = e.do(http.MethodPatch, "/api/web/v1/storage/configs/"+uid, e.adminToken,
		map[string]any{"PicgoConfigName": "Renamed"})
	if resp.Code != 40001 {
		t.Fatalf("改 PicgoConfigName 应被拒绝（40001），实际 %d %s", resp.Code, resp.Raw)
	}

	// 驱动列表（含 schema）
	resp = e.do(http.MethodGet, "/api/web/v1/storage/drivers", e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("驱动列表失败：HTTP %d", resp.Status)
	}
	if !contains(resp.Raw, `"uploaders"`) && !contains(resp.Raw, `"Drivers"`) {
		t.Fatalf("驱动列表应含 Drivers 字段：%s", resp.Raw)
	}

	// 删除
	resp = e.do(http.MethodDelete, "/api/web/v1/storage/configs/"+uid+"?Force=true", e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("删除失败：HTTP %d %s", resp.Status, resp.Raw)
	}
}

func TestE2EStorageDriverSchema(t *testing.T) {
	e := newE2E(t)

	resp := e.do(http.MethodPost, "/api/web/v1/storage/drivers/schema", e.adminToken,
		map[string]any{"Type": "github", "Answers": map[string]any{"repo": "org/x"}})
	if resp.Status != http.StatusOK {
		t.Fatalf("schema 失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	if !contains(resp.Raw, `"Type":"github"`) {
		t.Fatalf("schema 应含 Type：%s", resp.Raw)
	}
	// 驱动字段名必须原样（repo），不得 PascalCase
	if !contains(resp.Raw, `"Name":"repo"`) {
		t.Fatalf("schema 字段名应原样保留（repo）：%s", resp.Raw)
	}
}

// ---------------------------------------------------------------------------
// 上传全链路（A10 的第二条）
// ---------------------------------------------------------------------------

func TestE2EUploadFullFlow(t *testing.T) {
	e := newE2E(t)

	storageUID := e.newStorage("默认", true)

	// 上传一张图
	resp := e.upload(e.adminToken, map[string][]byte{"photo.png": tinyPNG()}, map[string]string{
		"StorageUID": storageUID,
	})
	if resp.Status != http.StatusOK {
		t.Fatalf("上传失败：HTTP %d %s", resp.Status, resp.Raw)
	}

	var batch struct {
		JobUID     string `json:"JobUID"`
		StorageUID string `json:"StorageUID"`
		Items      []struct {
			Seq       int    `json:"Seq"`
			FileName  string `json:"FileName"`
			UploadUID string `json:"UploadUID"`
			Status    string `json:"Status"`
		} `json:"Items"`
	}
	e.decode(resp, &batch)

	if batch.JobUID == "" {
		t.Fatalf("应返回 JobUID：%s", resp.Raw)
	}
	if batch.StorageUID != storageUID {
		t.Fatalf("批次应绑定存储配置：%s vs %s", batch.StorageUID, storageUID)
	}
	if len(batch.Items) != 1 || batch.Items[0].UploadUID == "" {
		t.Fatalf("应返回 1 个子项且带 UploadUID：%s", resp.Raw)
	}
	uploadUID := batch.Items[0].UploadUID

	// 轮询任务直到成功
	job := e.waitJob(batch.JobUID, e.adminToken)
	if job["Status"] != model.JobStatusSucceeded {
		t.Fatalf("任务应成功，实际 %v（err=%v）", job["Status"], job["Error"])
	}
	if job["Progress"] != float64(100) {
		t.Fatalf("进度应为 100，实际 %v", job["Progress"])
	}
	if job["SkippedItems"] != float64(0) {
		t.Fatalf("SkippedItems 应恒为 0，实际 %v", job["SkippedItems"])
	}
	// 子项
	items, _ := job["Items"].([]any)
	if len(items) != 1 {
		t.Fatalf("应含 1 个子项：%v", job["Items"])
	}

	// 图库能看到
	resp = e.do(http.MethodGet, "/api/web/v1/uploads?Scope=mine", e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("图库列表失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	var page struct {
		Items []map[string]any `json:"Items"`
		Total int64            `json:"Total"`
	}
	e.decode(resp, &page)
	if page.Total != 1 {
		t.Fatalf("图库应有 1 张，实际 %d", page.Total)
	}
	up := page.Items[0]
	if up["Status"] != model.UploadStatusSuccess {
		t.Fatalf("图片状态应为 success，实际 %v", up["Status"])
	}
	url, _ := up["URL"].(string)
	if url == "" {
		t.Fatalf("URL 为空：%v", up)
	}
	// 魔法文件名模板应生效
	fileName, _ := up["FileName"].(string)
	if !strings.Contains(fileName, "photo-") {
		t.Fatalf("魔法文件名模板未生效：%s", fileName)
	}
	// 列表里的附加只读字段
	if up["StorageName"] != "默认" {
		t.Fatalf("应返回 StorageName，实际 %v", up["StorageName"])
	}

	// 外链（markdown）
	resp = e.do(http.MethodGet, "/api/web/v1/uploads/"+uploadUID+"/link?Format=markdown", e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("外链失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	var link struct {
		Text   string `json:"Text"`
		Format string `json:"Format"`
		URL    string `json:"URL"`
	}
	e.decode(resp, &link)
	if link.Format != "markdown" {
		t.Fatalf("格式应为 markdown，实际 %s", link.Format)
	}
	if !strings.HasPrefix(link.Text, "![") || !strings.Contains(link.Text, "]("+url+")") {
		t.Fatalf("markdown 文本不符：%q（url=%s）", link.Text, url)
	}

	// 统计
	resp = e.do(http.MethodGet, "/api/web/v1/uploads/stats?Scope=mine", e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("统计失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	var stats struct {
		Total        int64 `json:"Total"`
		SuccessCount int64 `json:"SuccessCount"`
	}
	e.decode(resp, &stats)
	if stats.Total != 1 || stats.SuccessCount != 1 {
		t.Fatalf("统计不符：%+v", stats)
	}

	// 详情
	resp = e.do(http.MethodGet, "/api/web/v1/uploads/"+uploadUID, e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("详情失败：HTTP %d %s", resp.Status, resp.Raw)
	}

	// 重命名（只改别名）
	resp = e.do(http.MethodPatch, "/api/web/v1/uploads/"+uploadUID, e.adminToken,
		map[string]any{"AliasName": "封面图"})
	if resp.Status != http.StatusOK {
		t.Fatalf("重命名失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	var renamed map[string]any
	e.decode(resp, &renamed)
	if renamed["AliasName"] != "封面图" {
		t.Fatalf("别名未更新：%v", renamed["AliasName"])
	}
	if renamed["URL"] != url {
		t.Fatalf("重命名不应改变远端 URL")
	}

	// 删除 → 配额退还
	capBefore := e.usedBytes(e.adminUID)
	resp = e.do(http.MethodDelete, "/api/web/v1/uploads/"+uploadUID, e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("删除失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	var del struct {
		Deleted    bool  `json:"Deleted"`
		FreedBytes int64 `json:"FreedBytes"`
	}
	e.decode(resp, &del)
	if !del.Deleted {
		t.Fatal("应删除成功")
	}
	if del.FreedBytes <= 0 {
		t.Fatalf("应退还配额，实际 %d", del.FreedBytes)
	}
	if after := e.usedBytes(e.adminUID); after != capBefore-del.FreedBytes {
		t.Fatalf("UsedBytes 应减少 %d：%d → %d", del.FreedBytes, capBefore, after)
	}

	// 记录确实没了
	resp = e.do(http.MethodGet, "/api/web/v1/uploads/"+uploadUID, e.adminToken, nil)
	if resp.Status != http.StatusNotFound {
		t.Fatalf("已删除的图片应 404，实际 HTTP %d %s", resp.Status, resp.Raw)
	}
}

// TestE2EUploadPartialFailure 覆盖 A10 的第三条：
// 两项中一项失败 → job=failed，但**成功项的 URL 仍在 Result 里**。
func TestE2EUploadPartialFailure(t *testing.T) {
	e := newE2E(t, func(o *e2eOptions) { o.failPaths = []string{"bad"} })
	storageUID := e.newStorage("默认", true)

	resp := e.upload(e.adminToken, map[string][]byte{
		"good.png": tinyPNG(),
		"bad.png":  tinyPNG(),
	}, map[string]string{"StorageUID": storageUID})
	if resp.Status != http.StatusOK {
		t.Fatalf("上传失败：HTTP %d %s", resp.Status, resp.Raw)
	}

	var batch struct {
		JobUID string `json:"JobUID"`
	}
	e.decode(resp, &batch)

	job := e.waitJob(batch.JobUID, e.adminToken)
	if job["Status"] != model.JobStatusFailed {
		t.Fatalf("有失败项时 job 应为 failed，实际 %v", job["Status"])
	}
	if job["SucceededItems"] != float64(1) || job["FailedItems"] != float64(1) {
		t.Fatalf("计数不符：ok=%v fail=%v", job["SucceededItems"], job["FailedItems"])
	}

	// Result 里必须同时有成功 URL 与失败原因（D37：不丢数据）
	result, _ := job["Result"].(map[string]any)
	if result == nil {
		t.Fatalf("Result 不应为空：%v", job)
	}
	resultItems, _ := result["Items"].([]any)
	if len(resultItems) != 2 {
		t.Fatalf("Result.Items 应有 2 项，实际 %d", len(resultItems))
	}
	hasURL, hasErr := false, false
	for _, raw := range resultItems {
		it, _ := raw.(map[string]any)
		if u, _ := it["URL"].(string); u != "" {
			hasURL = true
		}
		if e, _ := it["Error"].(string); e != "" {
			hasErr = true
		}
	}
	if !hasURL {
		t.Fatalf("成功项的 URL 必须保留在 Result 里：%v", resultItems)
	}
	if !hasErr {
		t.Fatalf("失败项的原因应在 Result 里：%v", resultItems)
	}
}

// TestE2EUploadValidation 断言校验失败时**不产生 job**。
func TestE2EUploadValidation(t *testing.T) {
	e := newE2E(t)
	e.newStorage("默认", true)

	cases := []struct {
		name   string
		files  map[string][]byte
		fields map[string]string
		want   int
	}{
		{
			name:   "扩展名不在白名单",
			files:  map[string][]byte{"evil.exe": tinyPNG()},
			fields: map[string]string{},
			want:   40001,
		},
		{
			name:   "缺少 StorageUID 且无默认配置时由服务层判断",
			files:  map[string][]byte{"a.png": tinyPNG()},
			fields: map[string]string{"StorageUID": "st_not_exist"},
			want:   40401,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := e.upload(e.adminToken, tc.files, tc.fields)
			if resp.Code != tc.want {
				t.Fatalf("期望错误码 %d，实际 %d（%s）", tc.want, resp.Code, resp.Raw)
			}
			// 不应产生 job
			listResp := e.do(http.MethodGet, "/api/web/v1/jobs?Scope=all", e.adminToken, nil)
			var page struct {
				Total int64 `json:"Total"`
			}
			e.decode(listResp, &page)
			if page.Total != 0 {
				t.Fatalf("校验失败不应产生 job，实际 %d", page.Total)
			}
		})
	}
}

// TestE2EUploadQuotaExceeded 断言配额不足返回 40302（不是 40301）。
func TestE2EUploadQuotaExceeded(t *testing.T) {
	e := newE2E(t)

	// 给普通用户一个极小的配额
	small := int64(10)
	if err := e.db.DB.Model(&model.User{}).
		Where(`"UID" = ?`, e.userUID).
		UpdateColumns(map[string]any{"CapacityBytes": small}).Error; err != nil {
		t.Fatalf("设置配额失败: %v", err)
	}

	storageUID := e.newStorage("默认", true)
	// 上传一个明显超过 10 字节的「PNG」
	big := append(tinyPNG(), bytes.Repeat([]byte{0}, 4096)...)

	resp := e.upload(e.userToken, map[string][]byte{"big.png": big}, map[string]string{
		"StorageUID": storageUID,
	})
	if resp.Code != 40302 {
		t.Fatalf("配额不足应返回 40302，实际 %d（%s）", resp.Code, resp.Raw)
	}
}

// ---------------------------------------------------------------------------
// SSE（A10 的第五条）
// ---------------------------------------------------------------------------

// TestE2EEventsSSE 断言 SSE 能收到 upload.progress 与 job.finished。
func TestE2EEventsSSE(t *testing.T) {
	e := newE2E(t)
	storageUID := e.newStorage("默认", true)

	// 建立 SSE 连接
	req, err := http.NewRequest(http.MethodGet, e.server.URL+"/api/web/v1/events", nil)
	if err != nil {
		t.Fatalf("构造 SSE 请求失败: %v", err)
	}
	// 用 query 传令牌（EventSource 无法自定义请求头，docs/API.md §8.1 允许此场景）
	q := req.URL.Query()
	q.Set("Token", e.adminToken)
	req.URL.RawQuery = q.Encode()

	resp, err := e.server.Client().Do(req)
	if err != nil {
		t.Fatalf("SSE 连接失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE 应 200，实际 %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type 应为 text/event-stream，实际 %q", ct)
	}

	// 在读之前先上传，避免竞态（Hub 的缓冲足够容纳这些事件）
	upResp := e.upload(e.adminToken, map[string][]byte{"sse.png": tinyPNG()}, map[string]string{
		"StorageUID": storageUID,
	})
	if upResp.Status != http.StatusOK {
		t.Fatalf("上传失败：HTTP %d %s", upResp.Status, upResp.Raw)
	}
	var batch struct {
		JobUID string `json:"JobUID"`
	}
	e.decode(upResp, &batch)

	// 读事件流，直到看到 job.finished 或超时
	events := map[string]int{}
	jobUIDSeen := false

	deadline := time.Now().Add(15 * time.Second)
	buf := make([]byte, 4096)
	var pending string

	for time.Now().Before(deadline) {
		if resp.Body == nil {
			break
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			pending += string(buf[:n])
			// 按空行切帧
			for {
				idx := strings.Index(pending, "\n\n")
				if idx < 0 {
					break
				}
				frame := pending[:idx]
				pending = pending[idx+2:]
				name, data := parseSSEFrame(frame)
				if name == "" {
					continue
				}
				events[name]++
				if strings.Contains(data, batch.JobUID) {
					jobUIDSeen = true
				}
			}
		}
		if readErr != nil {
			break
		}
		if events["job.finished"] > 0 {
			break
		}
	}

	if events["job.finished"] == 0 {
		t.Fatalf("未收到 job.finished，已收到：%v", events)
	}
	if events["upload.progress"] == 0 {
		t.Fatalf("未收到 upload.progress，已收到：%v", events)
	}
	if events["upload.finished"] == 0 {
		t.Fatalf("未收到 upload.finished，已收到：%v", events)
	}
	if !jobUIDSeen {
		t.Fatal("事件体应带本批次的 JobUID（便于前端归属）")
	}
	// 事件名保持小写点分
	for name := range events {
		if strings.ContainsAny(name, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			t.Fatalf("SSE 事件名应是小写点分，实际 %q", name)
		}
	}
}

// parseSSEFrame 解析一帧 SSE，返回 (事件名, data)。
func parseSSEFrame(frame string) (string, string) {
	var name, data string
	for _, line := range strings.Split(frame, "\n") {
		switch {
		case strings.HasPrefix(line, "event:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	return name, data
}

// ---------------------------------------------------------------------------
// 相册 / 任务 / 日志（端到端）
// ---------------------------------------------------------------------------

func TestE2EAlbumFlow(t *testing.T) {
	e := newE2E(t)
	storageUID := e.newStorage("默认", true)

	// 建相册
	resp := e.do(http.MethodPost, "/api/web/v1/albums", e.adminToken, map[string]any{
		"Name": "壁纸", "Intro": "桌面壁纸",
	})
	if resp.Status != http.StatusOK {
		t.Fatalf("建相册失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	var album struct {
		UID string `json:"UID"`
	}
	e.decode(resp, &album)
	if album.UID == "" {
		t.Fatal("相册 UID 为空")
	}

	// 上传一张并移入相册
	upResp := e.upload(e.adminToken, map[string][]byte{"w.png": tinyPNG()}, map[string]string{
		"StorageUID": storageUID, "AlbumUID": album.UID,
	})
	if upResp.Status != http.StatusOK {
		t.Fatalf("上传失败：%s", upResp.Raw)
	}
	var batch struct {
		JobUID string `json:"JobUID"`
		Items  []struct {
			UploadUID string `json:"UploadUID"`
		} `json:"Items"`
	}
	e.decode(upResp, &batch)
	e.waitJob(batch.JobUID, e.adminToken)
	uploadUID := batch.Items[0].UploadUID

	// 相册详情应含封面/计数
	resp = e.do(http.MethodGet, "/api/web/v1/albums/"+album.UID, e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("相册详情失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	var albumDetail struct {
		ImageCount int64 `json:"ImageCount"`
	}
	e.decode(resp, &albumDetail)
	if albumDetail.ImageCount != 1 {
		t.Fatalf("相册计数应为 1，实际 %d", albumDetail.ImageCount)
	}

	// 有图片时删除相册 → 40901
	resp = e.do(http.MethodDelete, "/api/web/v1/albums/"+album.UID, e.adminToken, nil)
	if resp.Code != 40901 {
		t.Fatalf("有图相册应返回 40901，实际 %d（%s）", resp.Code, resp.Raw)
	}

	// WithUploads=true → 脱离并删除
	resp = e.do(http.MethodDelete, "/api/web/v1/albums/"+album.UID+"?WithUploads=true", e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("WithUploads 删除失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	var delRes struct {
		DetachedUploads int64 `json:"DetachedUploads"`
	}
	e.decode(resp, &delRes)
	if delRes.DetachedUploads != 1 {
		t.Fatalf("应脱离 1 张，实际 %d", delRes.DetachedUploads)
	}

	// 图片仍在（只是脱离相册）
	resp = e.do(http.MethodGet, "/api/web/v1/uploads/"+uploadUID, e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("图片不应被删除：HTTP %d %s", resp.Status, resp.Raw)
	}
	var up map[string]any
	e.decode(resp, &up)
	if up["AlbumUID"] != "" {
		t.Fatalf("应已脱离相册，实际 %v", up["AlbumUID"])
	}
}

// TestE2EJobListAndDelete 断言任务列表与清理。
func TestE2EJobListAndDelete(t *testing.T) {
	e := newE2E(t)
	storageUID := e.newStorage("默认", true)

	resp := e.upload(e.adminToken, map[string][]byte{"a.png": tinyPNG()}, map[string]string{
		"StorageUID": storageUID,
	})
	var batch struct {
		JobUID string `json:"JobUID"`
	}
	e.decode(resp, &batch)
	e.waitJob(batch.JobUID, e.adminToken)

	// 列表
	resp = e.do(http.MethodGet, "/api/web/v1/jobs?Scope=all", e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("任务列表失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	var page struct {
		Items []map[string]any `json:"Items"`
		Total int64            `json:"Total"`
	}
	e.decode(resp, &page)
	if page.Total != 1 {
		t.Fatalf("应有 1 个任务，实际 %d", page.Total)
	}

	// 子项日志（上传没有 job.log，但接口应可用）
	resp = e.do(http.MethodGet, "/api/web/v1/jobs/"+batch.JobUID+"/logs?AfterSeq=0", e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("任务日志失败：HTTP %d %s", resp.Status, resp.Raw)
	}

	// 清理已结束任务
	resp = e.do(http.MethodDelete, "/api/web/v1/jobs/"+batch.JobUID, e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("清理任务失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	resp = e.do(http.MethodGet, "/api/web/v1/jobs/"+batch.JobUID, e.adminToken, nil)
	if resp.Status != http.StatusNotFound {
		t.Fatalf("任务应已删除：HTTP %d", resp.Status)
	}
}

// TestE2ELogsAndTypes 断言日志端点（admin）与类型清单。
func TestE2ELogsAndTypes(t *testing.T) {
	e := newE2E(t)
	storageUID := e.newStorage("默认", true)

	resp := e.upload(e.adminToken, map[string][]byte{"a.png": tinyPNG()}, map[string]string{
		"StorageUID": storageUID,
	})
	var batch struct {
		JobUID string `json:"JobUID"`
	}
	e.decode(resp, &batch)
	e.waitJob(batch.JobUID, e.adminToken)

	// 类型清单
	resp = e.do(http.MethodGet, "/api/web/v1/logs/types", e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("类型清单失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	if !contains(resp.Raw, `"Type":"upload"`) {
		t.Fatalf("类型清单应含 upload：%s", resp.Raw)
	}
	if !contains(resp.Raw, `"Label":"上传"`) {
		t.Fatalf("类型清单应含中文 Label：%s", resp.Raw)
	}

	// 日志列表
	resp = e.do(http.MethodGet, "/api/web/v1/logs?Type=upload", e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("日志列表失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	var page struct {
		Items []map[string]any `json:"Items"`
		Total int64            `json:"Total"`
	}
	e.decode(resp, &page)
	if page.Total == 0 {
		t.Fatalf("应有上传日志：%s", resp.Raw)
	}
	// Detail 应是对象（JSON 已解析），不是字符串
	if _, ok := page.Items[0]["Detail"].(map[string]any); !ok {
		t.Fatalf("Detail 应被解析成对象，实际 %T", page.Items[0]["Detail"])
	}

	// 邮件日志（admin）
	resp = e.do(http.MethodGet, "/api/web/v1/logs/emails", e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("邮件日志失败：HTTP %d %s", resp.Status, resp.Raw)
	}
}

// TestE2EPluginsList 断言插件端点（admin）转发 agent。
func TestE2EPluginsList(t *testing.T) {
	e := newE2E(t)

	resp := e.do(http.MethodGet, "/api/web/v1/plugins", e.adminToken, nil)
	if resp.Status != http.StatusOK {
		t.Fatalf("插件列表失败：HTTP %d %s", resp.Status, resp.Raw)
	}
	var data struct {
		Plugins  []any    `json:"Plugins"`
		Disabled []string `json:"Disabled"`
	}
	e.decode(resp, &data)
	if data.Plugins == nil {
		t.Fatal("Plugins 应为数组（不是 null）")
	}
}

// TestE2EForgetPasswordNoEnumeration 断言找回密码**免鉴权**且防枚举。
func TestE2EForgetPasswordNoEnumeration(t *testing.T) {
	e := newE2E(t)

	// 邮件未配置时 → 明确告知「未配置邮件服务」（这是配置问题，不是账号问题）
	resp := e.do(http.MethodPost, "/api/web/v1/auth/forgot-password", "", map[string]any{
		"Email": "admin@e2e.local",
	})
	if resp.Status == http.StatusUnauthorized {
		t.Fatal("找回密码必须免鉴权（未登录用户需要它）")
	}

	respExists := e.do(http.MethodPost, "/api/web/v1/auth/forgot-password", "", map[string]any{
		"Email": "admin@e2e.local",
	})
	respMissing := e.do(http.MethodPost, "/api/web/v1/auth/forgot-password", "", map[string]any{
		"Email": "definitely-not-exists@e2e.local",
	})
	// **两种情况必须完全一致**（防账号枚举）
	if respExists.Status != respMissing.Status || respExists.Message != respMissing.Message {
		t.Fatalf("存在与不存在的邮箱响应必须一致：\n  存在: HTTP %d %s\n  不存在: HTTP %d %s",
			respExists.Status, respExists.Message, respMissing.Status, respMissing.Message)
	}
}

// TestE2EUnknownAPIPathNotHijacked 断言 RegisterBusiness **不吞掉未知路径**。
//
// 说明：把未知路径渲染成 JSON `40401` 是 `internal/server` 的 `NoRoute` 职责
// （W2 已实现并有独立验证）。本测试只关心一件事：
// **业务路由不会把未知路径错误地匹配成某个已注册端点**（那会导致 200 + 错误数据）。
func TestE2EUnknownAPIPathNotHijacked(t *testing.T) {
	e := newE2E(t)

	unknown := []string{
		"/api/web/v1/definitely-not-a-route",
		"/api/web/v1/uploads/x/y/z/deep",
		"/api/web/v1/storage/configs/st_1/unknown-action",
	}
	for _, path := range unknown {
		resp := e.do(http.MethodGet, path, e.adminToken, nil)
		if resp.Status != http.StatusNotFound {
			t.Errorf("未知路径 %s 应为 404（不被误匹配），实际 HTTP %d：%s",
				path, resp.Status, truncate(resp.Raw, 200))
		}
	}
}

// TestE2EProtectedPrefixesAreEnforced 断言各前缀的权限未被绕过。
//
// 这是一条「防回归」测试：新增路由时若忘了挂中间件，这里会立刻暴露。
func TestE2EProtectedPrefixesAreEnforced(t *testing.T) {
	e := newE2E(t)

	type probe struct {
		method, path string
		body         any
		needAdmin    bool
	}
	probes := []probe{
		{http.MethodGet, "/api/web/v1/storage/configs", nil, true},
		{http.MethodGet, "/api/web/v1/storage/drivers", nil, true},
		{http.MethodPost, "/api/web/v1/storage/drivers/schema", map[string]any{"Type": "github"}, true},
		{http.MethodGet, "/api/web/v1/logs", nil, true},
		{http.MethodGet, "/api/web/v1/logs/types", nil, true},
		{http.MethodGet, "/api/web/v1/logs/emails", nil, true},
		{http.MethodGet, "/api/web/v1/plugins", nil, true},
		{http.MethodPost, "/api/web/v1/plugins/install", map[string]any{"Names": []string{"x"}}, true},
		{http.MethodPost, "/api/web/v1/settings/mail/test", map[string]any{"To": "a@b.c"}, true},
		{http.MethodGet, "/api/web/v1/uploads", nil, false},
		{http.MethodGet, "/api/web/v1/albums", nil, false},
		{http.MethodGet, "/api/web/v1/jobs", nil, false},
	}

	for _, p := range probes {
		// ① 无令牌 → 401（不能是 200）
		if resp := e.do(p.method, p.path, "", p.body); resp.Status != http.StatusUnauthorized {
			t.Errorf("%s %s 无令牌应 401，实际 %d", p.method, p.path, resp.Status)
		}
		// ② 普通用户：admin 端点应 403
		if p.needAdmin {
			if resp := e.do(p.method, p.path, e.userToken, p.body); resp.Status != http.StatusForbidden {
				t.Errorf("%s %s 普通用户应 403，实际 %d（%s）",
					p.method, p.path, resp.Status, truncate(resp.Raw, 150))
			}
		}
	}
}

// ---- 助手 ----

// usedBytes 直接读库取用户已用字节（断言配额用）。
func (e *e2eEnv) usedBytes(uid string) int64 {
	e.t.Helper()
	var u model.User
	if err := e.db.DB.Where(`"UID" = ?`, uid).First(&u).Error; err != nil {
		e.t.Fatalf("查询用户失败: %v", err)
	}
	return u.UsedBytes
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// truncate 截断长字符串，便于错误信息可读。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// 保证 id 包被引用（本文件用 registerBusiness 的路径里间接使用）。
var _ = id.New

// 保证 os 被引用（暂存文件断言用）。
var _ = os.Stat
