package settings

import (
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/crypto"
	"github.com/YeqingKy/PicGo-Web/server/internal/database"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
)

func newTestService(t *testing.T) (*Service, *repository.SettingRepo) {
	t.Helper()

	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := &config.Config{
		DBDriver:       config.DBDriverSQLite,
		SQLitePath:     filepath.Join(dir, "settings.db"),
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

	key := make([]byte, crypto.KeySize)
	for i := range key {
		key[i] = byte(i + 7)
	}
	cipher, err := crypto.New(key)
	if err != nil {
		t.Fatalf("构造加密器失败: %v", err)
	}

	repo := repository.NewSettingRepo(db.DB)
	svc, err := New(repo, cipher, log)
	if err != nil {
		t.Fatalf("构造配置服务失败: %v", err)
	}
	return svc, repo
}

// TestThreeLevelFallback 是 D18 三级兜底的核心验收：
// 未落库 → 代码默认值；落库 → DB 值；重置 → 回到默认值。
func TestThreeLevelFallback(t *testing.T) {
	svc, _ := newTestService(t)

	// ---- 第 3 层：代码默认值 ----
	if got := svc.GetString("site.name"); got != "PicGo Web" {
		t.Errorf("site.name 默认值应为 PicGo Web，实际 %q", got)
	}
	if got := svc.GetInt("upload.concurrency", -1); got != 1 {
		t.Errorf("upload.concurrency 默认值应为 1，实际 %d", got)
	}
	if got := svc.GetBool("upload.rateLimit.enabled", true); got != false {
		t.Errorf("upload.rateLimit.enabled 默认应为 false（D73 默认禁用），实际 %v", got)
	}
	if got := svc.GetInt("user.defaultCapacityBytes", 0); got != 5<<30 {
		t.Errorf("user.defaultCapacityBytes 默认应为 5GiB，实际 %d", got)
	}
	if got := svc.GetInt("log.retentionDays", 0); got != 180 {
		t.Errorf("log.retentionDays 默认应为 180，实际 %d", got)
	}
	if got := svc.GetString("theme.active"); got != "default" {
		t.Errorf("theme.active 默认应为 default，实际 %q", got)
	}

	// 未注册键 → 零值
	if got := svc.Get("no.such.key"); got != nil {
		t.Errorf("未注册键应返回 nil，实际 %#v", got)
	}

	// ---- 第 2 层：DB 值覆盖 ----
	if err := svc.Set("site.name", "我的图床", "usr_admin"); err != nil {
		t.Fatalf("写入 site.name 失败: %v", err)
	}
	if got := svc.GetString("site.name"); got != "我的图床" {
		t.Errorf("DB 值未生效，实际 %q", got)
	}

	if err := svc.Set("upload.concurrency", 4, "usr_admin"); err != nil {
		t.Fatalf("写入 upload.concurrency 失败: %v", err)
	}
	if got := svc.GetInt("upload.concurrency", 0); got != 4 {
		t.Errorf("整数配置未生效，实际 %d", got)
	}

	// ---- 重置 → 回到默认值 ----
	if err := svc.Reset("site.name", "usr_admin"); err != nil {
		t.Fatalf("重置失败: %v", err)
	}
	if got := svc.GetString("site.name"); got != "PicGo Web" {
		t.Errorf("重置后应回到默认值，实际 %q", got)
	}
}

// TestSourceTracking 校验 Item.Source 能区分 db / default（供后台徽章）。
func TestSourceTracking(t *testing.T) {
	svc, _ := newTestService(t)

	find := func(key string) Item {
		for _, it := range svc.All() {
			if it.Key == key {
				return it
			}
		}
		t.Fatalf("配置项 %s 不存在于 All()", key)
		return Item{}
	}

	if it := find("site.name"); it.Source != SourceDefault {
		t.Errorf("未落库时 Source 应为 default，实际 %q", it.Source)
	}

	if err := svc.Set("site.name", "X", ""); err != nil {
		t.Fatal(err)
	}
	if it := find("site.name"); it.Source != SourceDB {
		t.Errorf("落库后 Source 应为 db，实际 %q", it.Source)
	}
}

// TestSecretEncryption 校验 secret 类型：加密入库、返回掩码、GetRaw 拿明文。
func TestSecretEncryption(t *testing.T) {
	svc, repo := newTestService(t)
	const key = "mail.password"
	const plain = "super-secret-smtp-password"

	if err := svc.Set(key, plain, "usr_admin"); err != nil {
		t.Fatalf("写入 secret 失败: %v", err)
	}

	// ① 数据库里绝不能是明文
	row, err := repo.GetSystem(key)
	if err != nil || row == nil {
		t.Fatalf("读取 DB 行失败: %v", err)
	}
	if !row.Encrypted {
		t.Error("secret 行未标记 Encrypted")
	}
	if row.Value == plain {
		t.Error("数据库里存的是明文！")
	}
	if len(row.Value) < 20 {
		t.Errorf("密文长度异常: %q", row.Value)
	}

	// ② 对外取值必须是掩码
	if got := svc.GetString(key); got != crypto.Mask {
		t.Errorf("对外值应为掩码，实际 %q", got)
	}

	// ③ GetRaw 能拿到明文（内部使用）
	if got := svc.GetRaw(key); got != plain {
		t.Errorf("GetRaw 应返回明文，实际 %q", got)
	}

	// ④ HasValue 为真
	if !svc.HasValue(key) {
		t.Error("HasValue 应为 true")
	}

	// ⑤ API 视图里的 Value 是掩码
	for _, it := range svc.All() {
		if it.Key == key {
			if it.Value != crypto.Mask {
				t.Errorf("Item.Value 应为掩码，实际 %#v", it.Value)
			}
			if !it.Secret {
				t.Error("Item.Secret 应为 true")
			}
			if !it.HasValue {
				t.Error("Item.HasValue 应为 true")
			}
		}
	}

	// ⑥ 提交掩码 = 不修改
	if err := svc.Set(key, crypto.Mask, "usr_admin"); err != nil {
		t.Fatalf("提交掩码不应报错: %v", err)
	}
	if got := svc.GetRaw(key); got != plain {
		t.Errorf("提交掩码后明文被改写：%q", got)
	}
}

// TestUnregisteredKeyRejected 校验未注册键被拒绝（防脏写，D95 原则）。
func TestUnregisteredKeyRejected(t *testing.T) {
	svc, _ := newTestService(t)

	if err := svc.Set("site.homepage.title", "旧键已迁移", ""); err == nil {
		t.Error("未注册的键 site.homepage.title 应被拒绝（已迁到主题配置，D95）")
	}
	if err := svc.Set("site.background.mode", "acg", ""); err == nil {
		t.Error("未注册的键 site.background.mode 应被拒绝（已废除，D97）")
	}
	if err := svc.Set("completely.unknown", 1, ""); err == nil {
		t.Error("完全未知的键应被拒绝")
	}
}

// TestOnChangedFired 校验写入与重置都会触发回调。
func TestOnChangedFired(t *testing.T) {
	svc, _ := newTestService(t)

	var count int64
	var lastKey atomic.Value
	svc.OnChanged(func(ev ChangedEvent) {
		atomic.AddInt64(&count, 1)
		lastKey.Store(ev.Key)
	})

	if err := svc.Set("site.icp", "京ICP备1号", "usr_admin"); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt64(&count); n != 1 {
		t.Errorf("Set 后回调次数应为 1，实际 %d", n)
	}
	if k, _ := lastKey.Load().(string); k != "site.icp" {
		t.Errorf("回调收到的事件键错误: %q", k)
	}

	if err := svc.Reset("site.icp", "usr_admin"); err != nil {
		t.Fatal(err)
	}
	if n := atomic.LoadInt64(&count); n != 2 {
		t.Errorf("Reset 后回调次数应为 2，实际 %d", n)
	}
}

// TestPersistenceAcrossReload 校验配置落库并在重启后仍生效。
func TestPersistenceAcrossReload(t *testing.T) {
	svc, repo := newTestService(t)

	if err := svc.Set("site.notice", "**维护通知**", "usr_admin"); err != nil {
		t.Fatal(err)
	}

	// 模拟重启：用同一 repo 新建一个 service
	key := make([]byte, crypto.KeySize)
	for i := range key {
		key[i] = byte(i + 7)
	}
	cipher, _ := crypto.New(key)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc2, err := New(repo, cipher, log)
	if err != nil {
		t.Fatalf("重建服务失败: %v", err)
	}
	if got := svc2.GetString("site.notice"); got != "**维护通知**" {
		t.Errorf("重启后配置未保留，实际 %q", got)
	}
}

// TestTypeCoercion 校验类型转换的健壮性（DB 里存字符串也能正确解析）。
func TestTypeCoercion(t *testing.T) {
	svc, repo := newTestService(t)

	now := model.Now()
	// 手工写入「字符串形式的整数」与「字符串形式的布尔」（模拟历史数据 / 手工改写）
	if err := repo.UpsertSystem(&model.SystemSetting{
		Key: "upload.concurrency", Value: "8", ValueType: "int",
		Category: "upload", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := repo.UpsertSystem(&model.SystemSetting{
		Key: "upload.blockSvg", Value: "true", ValueType: "bool",
		Category: "upload", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	if err := svc.Reload(); err != nil {
		t.Fatalf("重载失败: %v", err)
	}

	if got := svc.GetInt("upload.concurrency", 0); got != 8 {
		t.Errorf("字符串整数未正确解析，实际 %d", got)
	}
	if got := svc.GetBool("upload.blockSvg", false); got != true {
		t.Errorf("字符串布尔未正确解析，实际 %v", got)
	}
}

// TestCategoryGroupingAndMeta 校验分类分组与元数据查询。
func TestCategoryGroupingAndMeta(t *testing.T) {
	svc, _ := newTestService(t)

	cats := svc.Categories()
	if len(cats) == 0 {
		t.Fatal("分类列表为空")
	}

	uploadItems := svc.ByCategory(config.CategoryUpload)
	if len(uploadItems) == 0 {
		t.Fatal("upload 分类为空")
	}
	for _, it := range uploadItems {
		if it.Category != string(config.CategoryUpload) {
			t.Errorf("分类过滤错误：%s 出现在 upload 分组", it.Key)
		}
	}

	if meta, ok := svc.Meta("upload.rateLimit.enabled"); !ok {
		t.Error("Meta 未找到 upload.rateLimit.enabled")
	} else if meta.Type != config.TypeBool {
		t.Errorf("类型应为 bool，实际 %s", meta.Type)
	}

	if _, ok := svc.Meta("no.such.key"); ok {
		t.Error("Meta 不应找到未注册键")
	}
}
