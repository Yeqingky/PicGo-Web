package database

import (
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// SchemaVersion 是当前代码期望的 schema 版本。
//
// ⚠️ 新增迁移时**同时**把这个数字 +1，并把条目追加到 schemaMigrations 末尾。
// 已发布的条目**永不修改**（D77）。
const SchemaVersion int64 = 1

// CurrentVersion 读取当前数据库的 schema 版本。
//
// 表不存在（全新库）时返回 0，不报错。
func CurrentVersion(db *gorm.DB) (int64, error) {
	migrator := db.Migrator()
	hasTable := migrator.HasTable(&model.SchemaMeta{})
	if !hasTable {
		// 兼容：某些方言下大小写敏感，再显式查一次表名
		hasTable = migrator.HasTable(model.SchemaMeta{}.TableName())
	}
	if !hasTable {
		return 0, nil
	}

	var meta model.SchemaMeta
	err := db.Where("id = ?", model.SchemaMetaSingletonID).First(&meta).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("读取 SchemaMeta 失败: %w", err)
	}
	return meta.Version, nil
}

// writeVersion 在给定事务内写回 schema 版本。
func writeVersion(tx *gorm.DB, version int64) error {
	now := model.Now()

	var meta model.SchemaMeta
	err := tx.Where("id = ?", model.SchemaMetaSingletonID).First(&meta).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		meta = model.SchemaMeta{
			ID:        model.SchemaMetaSingletonID,
			Version:   version,
			UpdatedAt: now,
		}
		if err := tx.Create(&meta).Error; err != nil {
			return fmt.Errorf("写入 SchemaMeta 失败: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 SchemaMeta 失败: %w", err)
	}

	meta.Version = version
	meta.UpdatedAt = now
	if err := tx.Save(&meta).Error; err != nil {
		return fmt.Errorf("更新 SchemaMeta 失败: %w", err)
	}
	return nil
}
