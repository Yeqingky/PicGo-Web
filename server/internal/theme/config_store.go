package theme

import (
	"errors"
	"fmt"
	"sort"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// configStore 读写 `ThemeConfigs` 表（D95：每行一个键）。
//
// # 为什么主题包自己持有这份持久化代码
//
// 上游 `repository.ThemeConfigRepo` 的 Where 子句用了**未加引号的小写标识符**
// （`WHERE theme_id = ? AND key = ?`）。SQLite 对标识符大小写不敏感所以能跑，
// 但 PostgreSQL 会把未加引号的标识符折叠成小写，而实际列名是 `"ThemeID"` / `"Key"`，
// 于是报 `column "theme_id" does not exist`（见 DATA-MODEL.md §0.2 / AGENTS.md §3.3）。
//
// 本包在**本轮不允许修改 `internal/repository/` 已有文件**的前提下，
// 用 GORM 的**结构体条件**（GORM 会为模型字段生成正确带引号的列名）实现同一张表的读写，
// 从而保证两方言都能工作。仓储层的那处问题已在本轮汇报中单独提出。
//
// 另外：本表按主题维度一次只取几十行，排序在 Go 侧完成 —— 这样就不必在
// `ORDER BY` 里手写标识符（GORM 的 Order 字符串是**原样透传**的，容易漏引号）。
type configStore struct {
	db *gorm.DB
}

// list 返回某主题的全部配置行（按 Key 升序，保证输出稳定）。
func (s *configStore) list(themeID string) ([]model.ThemeConfig, error) {
	var rows []model.ThemeConfig
	if err := s.db.Where(&model.ThemeConfig{ThemeID: themeID}).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("读取主题配置失败: %w", err)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows, nil
}

// get 取某主题的一个配置项；不存在返回 (nil, nil)。
func (s *configStore) get(themeID, key string) (*model.ThemeConfig, error) {
	var row model.ThemeConfig
	err := s.db.Where(&model.ThemeConfig{ThemeID: themeID, Key: key}).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取主题配置项 %s 失败: %w", key, err)
	}
	return &row, nil
}

// upsert 插入或更新一个配置项。
//
// 每键独立行 → 两个管理员改**不同键**互不覆盖（D95）。
func (s *configStore) upsert(row *model.ThemeConfig) error {
	err := s.db.Clauses(clause.OnConflict{
		// clause.Column 的列名会被 GORM 按方言正确加引号
		Columns: []clause.Column{{Name: "ThemeID"}, {Name: "Key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"Value", "ValueType", "UpdatedBy", "UpdatedAt",
		}),
	}).Create(row).Error
	if err != nil {
		return fmt.Errorf("保存主题配置项 %s 失败: %w", row.Key, err)
	}
	return nil
}

// delete 删除某主题的一个配置项（恢复 manifest 默认值）。
func (s *configStore) delete(themeID, key string) error {
	tx := s.db.Where(&model.ThemeConfig{ThemeID: themeID, Key: key}).Delete(&model.ThemeConfig{})
	if tx.Error != nil {
		return fmt.Errorf("删除主题配置项 %s 失败: %w", key, tx.Error)
	}
	return nil
}

// deleteByTheme 清理某主题的全部配置值，返回删除行数。
func (s *configStore) deleteByTheme(themeID string) (int64, error) {
	tx := s.db.Where(&model.ThemeConfig{ThemeID: themeID}).Delete(&model.ThemeConfig{})
	if tx.Error != nil {
		return 0, fmt.Errorf("清理主题 %s 的配置失败: %w", themeID, tx.Error)
	}
	return tx.RowsAffected, nil
}

// newThemeConfigRow 构造一行待写入的主题配置。
func newThemeConfigRow(themeID, key, valueJSON, valueType, by string, now int64) *model.ThemeConfig {
	return &model.ThemeConfig{
		ThemeID:   themeID,
		Key:       key,
		Value:     valueJSON,
		ValueType: valueType,
		UpdatedBy: by,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// nowFn 便于测试替换时间源（默认 model.Now）。
var nowFn = func() int64 { return model.Now() }

// ensureNow 返回当前 Unix 秒（走可替换的时间源）。
func ensureNow() int64 { return nowFn() }
