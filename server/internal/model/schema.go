package model

// SchemaMeta 记录数据库结构版本（D16）。
//
// 与程序版本**解耦**：它是整数序列，只增不减。
// 发布时在 CHANGELOG.md 记录「程序版本 ↔ schema 版本」对应关系。
type SchemaMeta struct {
	ID        uint64 `gorm:"primaryKey"`
	Version   int64  `gorm:"not null"`
	UpdatedAt int64  `gorm:"not null"`
}

func (SchemaMeta) TableName() string { return "SchemaMeta" }

// SchemaMetaSingletonID 该表只存一行，固定 ID = 1。
const SchemaMetaSingletonID uint64 = 1

// AllModels 返回需要建表 / 迁移的全部模型，顺序即依赖顺序。
//
// ⚠️ 新增模型必须加到这里，否则迁移不会建表。
func AllModels() []any {
	return []any{
		// 身份鉴权
		&User{},
		&UserProfile{},
		&OAuthIdentity{},
		&RefreshToken{},
		&APIToken{},
		&LoginAttempt{},
		// 存储配置
		&StorageConfig{},
		&StorageSecret{},
		// 主题配置
		&ThemeConfig{},
		// 媒体资源
		&Upload{},
		&UploadResult{},
		// 任务执行
		&Job{},
		&JobItem{},
		&JobLog{},
		// 审计记录
		&OperationLog{},
		&EmailLog{},
		// 系统配置
		&SystemSetting{},
		&UserSetting{},
		// 插件缓存
		&Plugin{},
		// 迁移版本
		&SchemaMeta{},
	}
}

// TableNames 返回全部业务表名（不含 SchemaMeta），用于测试与运维核对。
func TableNames() []string {
	models := AllModels()
	out := make([]string, 0, len(models))
	for _, m := range models {
		if t, ok := m.(interface{ TableName() string }); ok {
			out = append(out, t.TableName())
		}
	}
	return out
}
