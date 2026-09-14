package database

import (
	"errors"
	"fmt"
	"log/slog"

	"gorm.io/gorm"
)

// Migration 是一条版本化迁移。
//
// ⚠️ **只追加，不修改已发布条目**（D77）：
// 一旦某个 Version 被发布出去，它的 Up 函数与语义就不能再改，
// 否则已升级的库与新库会不一致。
type Migration struct {
	Version int64
	Name    string
	Up      func(tx *gorm.DB) error
}

// schemaMigrations 是迁移表，必须按 Version 升序声明。
var schemaMigrations = []Migration{
	{Version: 1, Name: "init", Up: migrateV1},
}

// Migrations 返回迁移表副本（供测试与运维展示）。
func Migrations() []Migration {
	out := make([]Migration, len(schemaMigrations))
	copy(out, schemaMigrations)
	return out
}

// Migrate 把数据库升级到当前代码期望的版本。
//
// 行为：
//   - 逐条执行 Version > 当前版本 的迁移，**每条在独立事务内**
//   - 全部成功后才把该版本写入 SchemaMeta
//   - 任一条失败 → 该条回滚 + 返回错误（调用方决定是否终止启动）
//
// 幂等：已是最新版本时不做任何事（连跑两次 Version 不变）。
func Migrate(db *DB, log *slog.Logger) error {
	current, err := CurrentVersion(db.DB)
	if err != nil {
		return err
	}

	if current >= SchemaVersion {
		log.Info("数据库结构已是最新", "version", current)
		return nil
	}

	log.Info("开始数据库迁移", "from", current, "to", SchemaVersion, "pending", len(pendingFrom(current)))

	for _, m := range schemaMigrations {
		if m.Version <= current {
			continue
		}

		log.Info("执行迁移", "version", m.Version, "name", m.Name)

		err := db.WithContext(db.Statement.Context).Transaction(func(tx *gorm.DB) error {
			if err := m.Up(tx); err != nil {
				return fmt.Errorf("迁移 v%d(%s) 失败: %w", m.Version, m.Name, err)
			}
			return writeVersion(tx, m.Version)
		})
		if err != nil {
			return err
		}

		current = m.Version
		log.Info("迁移完成", "version", m.Version, "name", m.Name)
	}

	// 防御：迁移表里最大版本必须等于 SchemaVersion
	max := int64(0)
	for _, m := range schemaMigrations {
		if m.Version > max {
			max = m.Version
		}
	}
	if max != SchemaVersion {
		return fmt.Errorf("迁移表最大版本(%d)与 SchemaVersion(%d) 不一致：请同步修改两处", max, SchemaVersion)
	}

	log.Info("数据库迁移结束", "version", current)
	return nil
}

func pendingFrom(current int64) []Migration {
	var out []Migration
	for _, m := range schemaMigrations {
		if m.Version > current {
			out = append(out, m)
		}
	}
	return out
}

// InferLegacyVersion 为「已存在表结构但没有 SchemaMeta」的历史库反推版本。
//
// 目前没有历史包袱（项目从 v1 开始），保留该函数作为将来接管的入口：
// 若检测到业务表已存在但 SchemaMeta 缺失，返回 0 让迁移重新跑一遍
// （所有建表语句都是 IF NOT EXISTS 语义，可安全重入）。
func InferLegacyVersion(db *gorm.DB) (int64, error) {
	hasMeta := db.Migrator().HasTable("SchemaMeta")
	if hasMeta {
		return 0, nil
	}
	return 0, errors.New("无法反推历史库版本：项目从 v1 开始，请确认是否误用了其他项目的数据库")
}
