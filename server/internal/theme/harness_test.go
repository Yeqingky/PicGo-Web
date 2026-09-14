package theme

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/crypto"
	"github.com/YeqingKy/PicGo-Web/server/internal/database"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// testEnv 是一套完整的主题测试环境（临时目录 + 真 SQLite + 真配置服务）。
type testEnv struct {
	t         *testing.T
	DataDir   string
	ThemesDir string
	Svc       *Service
	Settings  *settings.Service
	Auditor   *recordingAuditor
}

// recordingAuditor 记录审计调用，供断言「是否写了 theme.error」等。
type recordingAuditor struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (r *recordingAuditor) Log(_ context.Context, e AuditEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
}

// all 返回全部已记录的条目。
func (r *recordingAuditor) all() []AuditEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]AuditEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

// byType 返回某类型的条目。
func (r *recordingAuditor) byType(typ string) []AuditEntry {
	var out []AuditEntry
	for _, e := range r.all() {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// count 统计某类型的条目数。
func (r *recordingAuditor) count(typ string) int { return len(r.byType(typ)) }

// reset 清空记录（便于「只断言本轮产生」的用例）。
func (r *recordingAuditor) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = nil
}

// newTestEnv 建好临时数据目录、SQLite、配置服务与主题服务。
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	dataDir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := &config.Config{
		DBDriver:       config.DBDriverSQLite,
		SQLitePath:     filepath.Join(dataDir, "test.db"),
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
		t.Fatalf("数据库迁移失败: %v", err)
	}

	key := make([]byte, crypto.KeySize)
	for i := range key {
		key[i] = byte(i + 11)
	}
	cipher, err := crypto.New(key)
	if err != nil {
		t.Fatalf("构造加密器失败: %v", err)
	}

	settingsSvc, err := settings.New(repository.NewSettingRepo(db.DB), cipher, log)
	if err != nil {
		t.Fatalf("构造配置服务失败: %v", err)
	}

	themesDir := filepath.Join(dataDir, "themes")
	auditor := &recordingAuditor{}

	svc, err := New(Options{
		ThemesDir: themesDir,
		DB:        db.DB,
		Settings:  settingsSvc,
		Log:       log,
		Auditor:   auditor,
	})
	if err != nil {
		t.Fatalf("构造主题服务失败: %v", err)
	}

	return &testEnv{
		t:         t,
		DataDir:   dataDir,
		ThemesDir: themesDir,
		Svc:       svc,
		Settings:  settingsSvc,
		Auditor:   auditor,
	}
}

// writeTheme 在主题目录下写一个主题（自动生成 manifest.json 与 index.html）。
//
// manifestBody 为额外的 JSON 字段（不含 ID/Name/Pages 时由本函数补齐）。
// 传 indexHTML 为空串表示**故意不写** index.html（用于测「缺 index.html」）。
func (e *testEnv) writeTheme(id string, extraJSON string, indexHTML string) string {
	e.t.Helper()

	dir := filepath.Join(e.ThemesDir, id)
	if err := os.MkdirAll(filepath.Join(dir, AssetsDir), 0o755); err != nil {
		e.t.Fatalf("创建主题目录失败: %v", err)
	}

	body := `{"ID":"` + id + `","Name":"` + id + ` 主题","Version":"1.0.0"`
	if extraJSON != "" {
		body += "," + extraJSON
	}
	body += `}`

	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(body), 0o644); err != nil {
		e.t.Fatalf("写 manifest 失败: %v", err)
	}
	if indexHTML != "" {
		if err := os.WriteFile(filepath.Join(dir, IndexFile), []byte(indexHTML), 0o644); err != nil {
			e.t.Fatalf("写 index.html 失败: %v", err)
		}
	}
	return dir
}

// writeAsset 在主题的 assets/ 下写一个文件。
func (e *testEnv) writeAsset(themeID, name, content string) {
	e.t.Helper()
	target := filepath.Join(e.ThemesDir, themeID, AssetsDir, name)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		e.t.Fatalf("创建资源目录失败: %v", err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		e.t.Fatalf("写资源失败: %v", err)
	}
}

// themeIndexMarker 是测试用主题 index.html 的特征串。
//
// 用它区分「响应来自主题」与「响应来自内置 SPA」（SPA 的 index.html 里没有它）。
const themeIndexMarker = "THEME-INDEX-MARKER"

// seedDefault 跑一次 seed 并返回内嵌默认主题在磁盘上的目录。
func (e *testEnv) seedDefault() string {
	e.t.Helper()
	seeded, err := e.Svc.Seed()
	if err != nil {
		e.t.Fatalf("seed 失败: %v", err)
	}
	if !seeded {
		e.t.Fatal("首次 seed 应当执行（目录为空）")
	}
	dir := filepath.Join(e.ThemesDir, embeddedDefaultID)
	if !dirExists(dir) {
		e.t.Fatalf("seed 后目录不存在: %s", dir)
	}
	return dir
}
