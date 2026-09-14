package database

import (
	"fmt"

	"gorm.io/gorm"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// migrateV1 是初始迁移：建 21 张表 + 全部索引。
//
// 约定：
//   - **表与单列约束**由模型的 GORM tag 声明，用 AutoMigrate 建立
//   - **组合索引**在本文件里用原生 SQL 显式建立（便于对照 DATA-MODEL.md §10 逐项核对）
//   - 所有标识符**必须加双引号**：PostgreSQL 会把未加引号的标识符折成小写，
//     写 `CREATE INDEX ... ON Uploads (...)` 在 PgSQL 下会失败（D81.4）
func migrateV1(tx *gorm.DB) error {
	// ---- 1. 建表（含 tag 里声明的单列索引与唯一约束） ----
	if err := tx.AutoMigrate(model.AllModels()...); err != nil {
		return fmt.Errorf("建立基础表失败: %w", err)
	}

	// ---- 2. 组合索引 ----
	// 索引名统一 idx_<表>_<列...> 风格，便于运维辨认。
	indexes := []struct {
		name   string
		table  string
		unique bool
		cols   []string
	}{
		// 身份鉴权
		{name: "idx_oauth_provider_uid", table: "OAuthIdentities", unique: true,
			cols: []string{"Provider", "ProviderUserID"}},
		{name: "idx_login_attempts_window", table: "LoginAttempts",
			cols: []string{"Email", "ClientIP", "CreatedAt"}},

		// 存储配置
		{name: "idx_storage_type_config", table: "StorageConfigs", unique: true,
			cols: []string{"Type", "PicgoConfigName"}},
		{name: "idx_storage_enabled_default", table: "StorageConfigs",
			cols: []string{"Enabled", "IsDefault"}},

		// 主题配置
		{name: "idx_theme_configs_theme_key", table: "ThemeConfigs", unique: true,
			cols: []string{"ThemeID", "Key"}},

		// 媒体资源
		{name: "idx_uploads_user_created", table: "Uploads",
			cols: []string{"UserUID", "CreatedAt DESC"}},
		{name: "idx_albums_user_name", table: "Albums", unique: true,
			cols: []string{"UserUID", "Name"}},

		// 任务执行
		{name: "idx_jobs_status_created", table: "Jobs",
			cols: []string{"Status", "CreatedAt"}},
		{name: "idx_job_items_job_seq", table: "JobItems", unique: true,
			cols: []string{"JobUID", "Seq"}},
		{name: "idx_job_logs_job_seq", table: "JobLogs", unique: true,
			cols: []string{"JobUID", "Seq"}},

		// 审计记录
		{name: "idx_operation_logs_type_created", table: "OperationLogs",
			cols: []string{"Type", "CreatedAt"}},
		{name: "idx_operation_logs_target", table: "OperationLogs",
			cols: []string{"TargetType", "TargetUID"}},

		// 用户设置
		{name: "idx_user_settings_user_key", table: "UserSettings", unique: true,
			cols: []string{"UserUID", "Key"}},
	}

	for _, idx := range indexes {
		if err := createIndex(tx, idx.name, idx.table, idx.unique, idx.cols); err != nil {
			return err
		}
	}

	return nil
}

// createIndex 用方言无关的写法建立索引。
//
//   - 标识符统一加双引号（D81.4：PgSQL 未加引号会折成小写）
//   - 使用 IF NOT EXISTS，使迁移可安全重入
//   - 列名可带 " DESC" 后缀（本函数会把它放在引号**外面**）
func createIndex(tx *gorm.DB, name, table string, unique bool, cols []string) error {
	quotedCols := make([]string, 0, len(cols))
	for _, c := range cols {
		quotedCols = append(quotedCols, quoteColumnExpr(c))
	}

	kind := "INDEX"
	if unique {
		kind = "UNIQUE INDEX"
	}

	sql := fmt.Sprintf(
		`CREATE %s IF NOT EXISTS "%s" ON "%s" (%s)`,
		kind, name, table, joinComma(quotedCols),
	)
	if err := tx.Exec(sql).Error; err != nil {
		return fmt.Errorf("创建索引 %s（表 %s）失败: %w", name, table, err)
	}
	return nil
}

// quoteColumnExpr 把列表达式转成 `"Col"` 或 `"Col" DESC`。
//
// 输入形如 `Col` 或 `Col DESC`；只支持这两种形式
// （不接受任意 SQL 片段，避免注入与方言差异）。
func quoteColumnExpr(expr string) string {
	const suffix = " DESC"
	if len(expr) > len(suffix) && expr[len(expr)-len(suffix):] == suffix {
		return `"` + expr[:len(expr)-len(suffix)] + `"` + suffix
	}
	return `"` + expr + `"`
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
