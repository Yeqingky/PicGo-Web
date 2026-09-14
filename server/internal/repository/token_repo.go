package repository

import (
	"gorm.io/gorm"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// TokenRepo 是 RefreshTokens / APITokens 的数据访问（两者都是「令牌」，同属会话域）。
type TokenRepo struct{ db *gorm.DB }

// NewTokenRepo 构造。
func NewTokenRepo(db *gorm.DB) *TokenRepo { return &TokenRepo{db: db} }

// ---- RefreshTokens（本机会话）----

// CreateRefresh 插入一条 refresh token（只存哈希，D30）。
func (r *TokenRepo) CreateRefresh(t *model.RefreshToken) error {
	return wrap(r.db.Create(t).Error)
}

// FindRefreshByHash 按哈希查 refresh token；不存在返回 (nil, ErrNotFound)。
func (r *TokenRepo) FindRefreshByHash(hash string) (*model.RefreshToken, error) {
	var t model.RefreshToken
	if err := r.db.Where(map[string]any{"TokenHash": hash}).First(&t).Error; err != nil {
		return nil, wrap(err)
	}
	return &t, nil
}

// RevokeRefreshByUID 按 UID 吊销（幂等：已吊销时 RowsAffected 为 0，不报错）。
func (r *TokenRepo) RevokeRefreshByUID(uid string, at int64) error {
	res := r.db.Model(&model.RefreshToken{}).
		Where(map[string]any{"UID": uid, "RevokedAt": 0}).
		Updates(map[string]any{"RevokedAt": at})
	return wrap(res.Error)
}

// RevokeAllRefreshForUser 吊销某用户全部有效 refresh token（改密 / 重置密码时用）。
func (r *TokenRepo) RevokeAllRefreshForUser(userUID string, at int64) (int64, error) {
	res := r.db.Model(&model.RefreshToken{}).
		Where(map[string]any{"UserUID": userUID, "RevokedAt": 0}).
		Updates(map[string]any{"RevokedAt": at})
	return res.RowsAffected, wrap(res.Error)
}

// DeleteExpiredRefresh 清理过期或已吊销的 refresh token（定时维护用）。
func (r *TokenRepo) DeleteExpiredRefresh(before int64) (int64, error) {
	res := r.db.Where(col("ExpiresAt")+` < ? OR `+col("RevokedAt")+` > 0`, before).
		Delete(&model.RefreshToken{})
	return res.RowsAffected, wrap(res.Error)
}

// ---- APITokens（长期令牌）----

// CreateAPIToken 插入一条 API token（只存哈希，D31）。
func (r *TokenRepo) CreateAPIToken(t *model.APIToken) error {
	return wrap(r.db.Create(t).Error)
}

// FindAPITokenByHash 按哈希查 API token；不存在返回 (nil, ErrNotFound)。
func (r *TokenRepo) FindAPITokenByHash(hash string) (*model.APIToken, error) {
	var t model.APIToken
	if err := r.db.Where(map[string]any{"TokenHash": hash}).First(&t).Error; err != nil {
		return nil, wrap(err)
	}
	return &t, nil
}

// ListAPITokens 列出某用户的全部 API token（**不含明文**）。
func (r *TokenRepo) ListAPITokens(userUID string) ([]model.APIToken, error) {
	var out []model.APIToken
	if err := r.db.Where(map[string]any{"UserUID": userUID}).
		Order(col("CreatedAt") + " DESC").Find(&out).Error; err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// GetAPITokenForUser 取某用户的指定 token（防止越权操作他人令牌）。
func (r *TokenRepo) GetAPITokenForUser(userUID, uid string) (*model.APIToken, error) {
	var t model.APIToken
	if err := r.db.Where(map[string]any{"UserUID": userUID, "UID": uid}).First(&t).Error; err != nil {
		return nil, wrap(err)
	}
	return &t, nil
}

// DeleteAPITokenForUser 删除某用户的指定 token。
func (r *TokenRepo) DeleteAPITokenForUser(userUID, uid string) (int64, error) {
	res := r.db.Where(map[string]any{"UserUID": userUID, "UID": uid}).Delete(&model.APIToken{})
	return res.RowsAffected, wrap(res.Error)
}

// TouchAPIToken 更新最后使用时间。
//
// 鉴权中间件按 60 秒节流调用，避免每个请求都写库。
func (r *TokenRepo) TouchAPIToken(id uint64, at int64) error {
	res := r.db.Model(&model.APIToken{}).
		Where(map[string]any{"ID": id}).
		Updates(map[string]any{"LastUsedAt": at})
	return wrap(res.Error)
}
