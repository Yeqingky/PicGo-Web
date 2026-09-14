package repository

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// SettingRepo 负责 SystemSettings 与 UserSettings 两张 KV 表。
type SettingRepo struct{ db *gorm.DB }

// NewSettingRepo 构造。
func NewSettingRepo(db *gorm.DB) *SettingRepo { return &SettingRepo{db: db} }

// ---- SystemSettings ----

// GetSystem 按 key 取一条；不存在返回 (nil, nil)。
func (r *SettingRepo) GetSystem(key string) (*model.SystemSetting, error) {
	var s model.SystemSetting
	err := r.db.Where("key = ?", key).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, wrap(err)
	}
	return &s, nil
}

// ListSystem 返回全部（或按分类过滤的）站点配置。
func (r *SettingRepo) ListSystem(category string) ([]model.SystemSetting, error) {
	var out []model.SystemSetting
	q := r.db.Model(&model.SystemSetting{})
	if category != "" {
		q = q.Where("category = ?", category)
	}
	if err := q.Order("key ASC").Find(&out).Error; err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// UpsertSystem 按 key 插入或更新。
func (r *SettingRepo) UpsertSystem(s *model.SystemSetting) error {
	return wrap(r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "Key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"Value", "ValueType", "Encrypted", "Category", "UpdatedBy", "UpdatedAt",
		}),
	}).Create(s).Error)
}

// DeleteSystem 删除一个站点配置（通常不需要：恢复默认值用 Upsert 写空即可）。
func (r *SettingRepo) DeleteSystem(key string) error {
	return wrap(r.db.Where("key = ?", key).Delete(&model.SystemSetting{}).Error)
}

// ---- UserSettings ----

// GetUser 取某用户的一个偏好；不存在返回 (nil, nil)。
func (r *SettingRepo) GetUser(userUID, key string) (*model.UserSetting, error) {
	var s model.UserSetting
	err := r.db.Where("user_uid = ? AND key = ?", userUID, key).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, wrap(err)
	}
	return &s, nil
}

// ListUser 返回某用户的全部偏好。
func (r *SettingRepo) ListUser(userUID string) ([]model.UserSetting, error) {
	var out []model.UserSetting
	if err := r.db.Where("user_uid = ?", userUID).Order("key ASC").Find(&out).Error; err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// UpsertUser 插入或更新某用户的偏好。
func (r *SettingRepo) UpsertUser(s *model.UserSetting) error {
	return wrap(r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "UserUID"}, {Name: "Key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"Value", "ValueType", "UpdatedAt",
		}),
	}).Create(s).Error)
}

// DeleteUser 删除某用户的一个偏好（恢复默认）。
func (r *SettingRepo) DeleteUser(userUID, key string) error {
	return wrap(r.db.Where("user_uid = ? AND key = ?", userUID, key).
		Delete(&model.UserSetting{}).Error)
}

// ---- ThemeConfigs（D95：每行一个键，独立表） ----

// ThemeConfigRepo 负责主题配置。
type ThemeConfigRepo struct{ db *gorm.DB }

// NewThemeConfigRepo 构造。
func NewThemeConfigRepo(db *gorm.DB) *ThemeConfigRepo { return &ThemeConfigRepo{db: db} }

// ListByTheme 返回某主题的全部配置值。
func (r *ThemeConfigRepo) ListByTheme(themeID string) ([]model.ThemeConfig, error) {
	var out []model.ThemeConfig
	if err := r.db.Where("theme_id = ?", themeID).Order("key ASC").Find(&out).Error; err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// Get 取某主题的一个配置项；不存在返回 (nil, nil)。
func (r *ThemeConfigRepo) Get(themeID, key string) (*model.ThemeConfig, error) {
	var c model.ThemeConfig
	err := r.db.Where("theme_id = ? AND key = ?", themeID, key).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, wrap(err)
	}
	return &c, nil
}

// Upsert 插入或更新一个配置项（每键独立行，两个管理员改不同键不互相覆盖）。
func (r *ThemeConfigRepo) Upsert(c *model.ThemeConfig) error {
	return wrap(r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "ThemeID"}, {Name: "Key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"Value", "ValueType", "UpdatedBy", "UpdatedAt",
		}),
	}).Create(c).Error)
}

// Delete 删除某主题的一个配置项（恢复 manifest 默认值）。
func (r *ThemeConfigRepo) Delete(themeID, key string) error {
	return wrap(r.db.Where("theme_id = ? AND key = ?", themeID, key).
		Delete(&model.ThemeConfig{}).Error)
}

// DeleteByTheme 清理某主题的全部配置值（D95：卸载主题时由管理员显式触发）。
// 返回删除的行数。
func (r *ThemeConfigRepo) DeleteByTheme(themeID string) (int64, error) {
	tx := r.db.Where("theme_id = ?", themeID).Delete(&model.ThemeConfig{})
	return tx.RowsAffected, wrap(tx.Error)
}
