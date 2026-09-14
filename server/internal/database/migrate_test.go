package database

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// newTestDB 建一个临时 SQLite 库并跑完迁移。
func newTestDB(t *testing.T) *DB {
	t.Helper()

	dir := t.TempDir()
	cfg := &config.Config{
		DBDriver:          config.DBDriverSQLite,
		SQLitePath:        filepath.Join(dir, "test.db"),
		DBMaxOpenConns:    1,
		DBMaxIdleConns:    1,
		DBConnMaxLifetime: 0,
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := Open(cfg, log)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	if err := Migrate(db, log); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return db
}

func TestMigrateCreatesAllTables(t *testing.T) {
	db := newTestDB(t)

	want := model.TableNames()
	if len(want) == 0 {
		t.Fatal("模型清单为空")
	}

	for _, name := range want {
		if !db.Migrator().HasTable(name) {
			t.Errorf("表 %s 未创建", name)
		}
	}
}

// TestTableNamesArePascalCase 是**命名回归测试**（D81.4）。
//
// 若 GORM 漏配 NamingStrategy{NoLowerCase: true}，表名会被折成
// users / storage_configs 这类 snake_case，此测试会立刻失败。
func TestTableNamesArePascalCase(t *testing.T) {
	db := newTestDB(t)

	var names []string
	err := db.Raw(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`,
	).Pluck("name", &names).Error
	if err != nil {
		t.Fatalf("查询表名失败: %v", err)
	}

	if len(names) == 0 {
		t.Fatal("未查到任何表")
	}

	for _, name := range names {
		if name == "" {
			continue
		}
		if name[0] < 'A' || name[0] > 'Z' {
			t.Errorf("表名 %q 不是 PascalCase（首字母应为大写）", name)
		}
		if contains(name, '_') {
			t.Errorf("表名 %q 含下划线，说明命名策略未生效（D81.4）", name)
		}
	}
}

// TestColumnNamesArePascalCase 校验列名同样保持 PascalCase。
func TestColumnNamesArePascalCase(t *testing.T) {
	db := newTestDB(t)

	var cols []string
	err := db.Raw(`SELECT name FROM pragma_table_info('Users')`).Pluck("name", &cols).Error
	if err != nil {
		t.Fatalf("查询列名失败: %v", err)
	}

	names := make(map[string]bool, len(cols))
	for _, c := range cols {
		names[c] = true
	}

	for _, want := range []string{"UID", "Email", "PasswordHash", "CapacityBytes", "UsedBytes", "CreatedAt"} {
		if !names[want] {
			t.Errorf("Users 表缺少列 %s（实际列：%v）", want, names)
		}
	}
	for _, bad := range []string{"created_at", "capacity_bytes", "used_bytes", "user_uid"} {
		if names[bad] {
			t.Errorf("Users 表出现了 snake_case 列 %s（D81.4 命名策略未生效）", bad)
		}
	}
}

// TestMigrateIsIdempotent 验证迁移幂等：连跑两次版本不变、不报错。
func TestMigrateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{
		DBDriver:       config.DBDriverSQLite,
		SQLitePath:     filepath.Join(dir, "idem.db"),
		DBMaxOpenConns: 1,
		DBMaxIdleConns: 1,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	db, err := Open(cfg, log)
	if err != nil {
		t.Fatalf("打开库失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	// 第一次
	if err := Migrate(db, log); err != nil {
		t.Fatalf("第一次迁移失败: %v", err)
	}
	v1, err := CurrentVersion(db.DB)
	if err != nil {
		t.Fatalf("读取版本失败: %v", err)
	}
	if v1 != SchemaVersion {
		t.Fatalf("第一次迁移后版本应为 %d，实际 %d", SchemaVersion, v1)
	}

	// 第二次（必须无副作用）
	if err := Migrate(db, log); err != nil {
		t.Fatalf("第二次迁移失败: %v", err)
	}
	v2, err := CurrentVersion(db.DB)
	if err != nil {
		t.Fatalf("读取版本失败: %v", err)
	}
	if v2 != v1 {
		t.Errorf("幂等性破坏：第二次迁移后版本由 %d 变为 %d", v1, v2)
	}

	// SchemaMeta 只能有一行
	var count int64
	if err := db.Model(&model.SchemaMeta{}).Count(&count).Error; err != nil {
		t.Fatalf("统计 SchemaMeta 失败: %v", err)
	}
	if count != 1 {
		t.Errorf("SchemaMeta 应只有 1 行，实际 %d", count)
	}
}

// TestMigrationsTableIsOrdered 校验迁移表按 Version 升序且无重复。
func TestMigrationsTableIsOrdered(t *testing.T) {
	ms := Migrations()
	if len(ms) == 0 {
		t.Fatal("迁移表为空")
	}

	prev := int64(0)
	for _, m := range ms {
		if m.Version <= prev {
			t.Errorf("迁移版本未严格升序：%d 出现在 %d 之后", m.Version, prev)
		}
		if m.Name == "" {
			t.Errorf("迁移 v%d 缺少名称", m.Version)
		}
		if m.Up == nil {
			t.Errorf("迁移 v%d 缺少 Up 函数", m.Version)
		}
		prev = m.Version
	}

	max := ms[len(ms)-1].Version
	if max != SchemaVersion {
		t.Errorf("迁移表最大版本 %d 与 SchemaVersion %d 不一致", max, SchemaVersion)
	}
}

// TestCompositeIndexesExist 校验 DATA-MODEL §10 里的组合索引都已建立。
func TestCompositeIndexesExist(t *testing.T) {
	db := newTestDB(t)

	var idxNames []string
	if err := db.Raw(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND name LIKE 'idx_%'`,
	).Pluck("name", &idxNames).Error; err != nil {
		t.Fatalf("查询索引失败: %v", err)
	}

	got := make(map[string]bool, len(idxNames))
	for _, n := range idxNames {
		got[n] = true
	}

	want := []string{
		"idx_oauth_provider_uid",
		"idx_login_attempts_window",
		"idx_storage_type_config",
		"idx_storage_enabled_default",
		"idx_theme_configs_theme_key",
		"idx_uploads_user_created",
		"idx_albums_user_name",
		"idx_jobs_status_created",
		"idx_job_items_job_seq",
		"idx_job_logs_job_seq",
		"idx_operation_logs_type_created",
		"idx_operation_logs_target",
		"idx_user_settings_user_key",
	}

	for _, name := range want {
		if !got[name] {
			t.Errorf("索引 %s 未创建", name)
		}
	}
}

func contains(s string, r rune) bool {
	for _, ch := range s {
		if ch == r {
			return true
		}
	}
	return false
}
