package repository

import (
	"gorm.io/gorm"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// 本文件给 SettingRepo 追加 W6 需要的查询方法。
//
// Go 允许把同一类型的方法分散在同包的不同文件里，因此无需改动 W2 的 setting_repo.go。

// ListUserByKey 按**键名**查所有用户的该键设置。
//
// 用途：一次性令牌（如 `auth.resetToken`）只给到哈希与用户 UID，
// 无法反查「哪个用户持有这个哈希」，因此需要按键遍历。
//
// ⚠️ 该方法会扫全表（UserSettings 通常很小：只有少量用户、每人少量键），
// 因此可接受；**不要**用它做高频查询。
func (r *SettingRepo) ListUserByKey(key string) ([]model.UserSetting, error) {
	var out []model.UserSetting
	if err := r.db.Where(map[string]any{"Key": key}).Find(&out).Error; err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// DeleteUserByKey 删除全部用户的某个键（清理过期一次性令牌用）。
func (r *SettingRepo) DeleteUserByKey(key string) (int64, error) {
	res := r.db.Where(map[string]any{"Key": key}).Delete(&model.UserSetting{})
	if res.Error != nil {
		return 0, wrap(res.Error)
	}
	return res.RowsAffected, nil
}

// TouchUserSetting 在值不存在时才写入（避免覆盖已有令牌）。
//
// 保留给将来可能需要的「幂等初始化」语义；当前未被调用。
func (r *SettingRepo) TouchUserSetting(s *model.UserSetting) error {
	return wrap(r.db.Where(map[string]any{"UserUID": s.UserUID, "Key": s.Key}).
		FirstOrCreate(s).Error)
}

// 显式引用 gorm，保持与同包其它文件一致的依赖声明。
var _ = gorm.ErrRecordNotFound
