package repository

import (
	"gorm.io/gorm"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// LoginAttemptRepo 是 LoginAttempts 的数据访问。
//
// 该表独立存在的原因（D78）：写入频繁、需要按时间窗聚合、可独立清理。
type LoginAttemptRepo struct{ db *gorm.DB }

// NewLoginAttemptRepo 构造。
func NewLoginAttemptRepo(db *gorm.DB) *LoginAttemptRepo { return &LoginAttemptRepo{db: db} }

// Record 记录一次登录尝试（成功或失败都记）。
func (r *LoginAttemptRepo) Record(a *model.LoginAttempt) error {
	return wrap(r.db.Create(a).Error)
}

// CountFailuresSince 统计 (Email, ClientIP) 在 since（Unix 秒，含）之后的**失败**次数。
//
// 命中 LoginAttempts(Email, ClientIP, CreatedAt) 组合索引。
func (r *LoginAttemptRepo) CountFailuresSince(email, clientIP string, since int64) (int64, error) {
	var n int64
	err := r.db.Model(&model.LoginAttempt{}).
		Where(map[string]any{
			"Email":    email,
			"ClientIP": clientIP,
			"Success":  false,
		}).
		Where(col("CreatedAt")+` >= ?`, since).
		Count(&n).Error
	return n, wrap(err)
}

// ClearFailures 删除 (Email, ClientIP) 的失败记录。
//
// 登录成功后调用，让正常用户不必等下个窗口就能恢复。
func (r *LoginAttemptRepo) ClearFailures(email, clientIP string) (int64, error) {
	res := r.db.Where(map[string]any{
		"Email":    email,
		"ClientIP": clientIP,
		"Success":  false,
	}).Delete(&model.LoginAttempt{})
	return res.RowsAffected, wrap(res.Error)
}

// DeleteOlderThan 清理历史记录（定时维护用）。
func (r *LoginAttemptRepo) DeleteOlderThan(before int64) (int64, error) {
	res := r.db.Where(col("CreatedAt")+` < ?`, before).Delete(&model.LoginAttempt{})
	return res.RowsAffected, wrap(res.Error)
}
