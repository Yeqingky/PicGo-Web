package repository

import (
	"gorm.io/gorm"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// 本文件给 UserRepo 追加 W5/W6 需要但 W3 未提供的批量查询方法。
//
// Go 允许把同一类型的方法分散在同包的不同文件里，因此无需改动 W3 的 user_repo.go。

// FindManyByUIDs 批量按 UID 查用户（图库 `Scope=all` 时补 `UserEmail` 用）。
//
// 返回的顺序与入参无关；调用方自行建 map。
// 刻意**不返回 PasswordHash 之外的多余字段**：本方法只用于展示场景，
// 但 GORM 的 Find 会带出整行 —— 因此调用方**只能取 UID / Email 这类展示字段**。
func (r *UserRepo) FindManyByUIDs(uids []string) ([]model.User, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	// 去重，避免 IN 列表里出现重复值
	seen := make(map[string]struct{}, len(uids))
	dedup := make([]string, 0, len(uids))
	for _, uid := range uids {
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		dedup = append(dedup, uid)
	}
	if len(dedup) == 0 {
		return nil, nil
	}

	var out []model.User
	if err := r.db.Where(col("UID")+" IN ?", dedup).Find(&out).Error; err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// RecalcUsedBytes 用实际图片体积重算某用户的已用字节，返回新值。
//
// 用途：配额对账（docs/OPERATIONS.md §3.5 的「冗余计数漂移」）。
// 冗余计数在「删除/上传事务失败」等异常路径下可能漂移，
// 定时任务或管理操作可调用本方法修正。
func (r *UserRepo) RecalcUsedBytes(uid string) (int64, error) {
	var total *int64
	err := r.db.Model(&model.Upload{}).
		Where(map[string]any{"UserUID": uid, "Status": model.UploadStatusSuccess}).
		Select("COALESCE(SUM(" + col("Size") + "), 0)").
		Scan(&total).Error
	if err != nil {
		return 0, wrap(err)
	}
	var value int64
	if total != nil {
		value = *total
	}
	if err := r.db.Model(&model.User{}).Where(map[string]any{"UID": uid}).
		Update("UsedBytes", value).Error; err != nil {
		return 0, wrap(err)
	}
	return value, nil
}

// RecalcAllUsedBytes 重算**全部用户**的已用字节，返回受影响用户数。
func (r *UserRepo) RecalcAllUsedBytes() (int64, error) {
	var users []model.User
	if err := r.db.Select(col("UID")).Find(&users).Error; err != nil {
		return 0, wrap(err)
	}
	var n int64
	for _, u := range users {
		if _, err := r.RecalcUsedBytes(u.UID); err != nil {
			return n, wrap(err)
		}
		n++
	}
	return n, nil
}

// FindEmailsByUIDs 只取 UID → Email 映射（比整行查询更省）。
func (r *UserRepo) FindEmailsByUIDs(uids []string) (map[string]string, error) {
	out := make(map[string]string, len(uids))
	if len(uids) == 0 {
		return out, nil
	}
	var rows []struct {
		UID   string
		Email string
	}
	if err := r.db.Model(&model.User{}).
		Select(col("UID")+", "+col("Email")).
		Where(col("UID")+" IN ?", uids).
		Scan(&rows).Error; err != nil {
		return nil, wrap(err)
	}
	for _, row := range rows {
		out[row.UID] = row.Email
	}
	return out, nil
}

// UsedBytesOf 读取某用户的已用字节（配额展示用）。
func (r *UserRepo) UsedBytesOf(uid string) (int64, error) {
	var u model.User
	if err := r.db.Select(col("UsedBytes")).Where(map[string]any{"UID": uid}).First(&u).Error; err != nil {
		return 0, wrap(err)
	}
	return u.UsedBytes, nil
}

// 确保 gorm 被引用（本文件只用 col / wrap 与 model，保留显式依赖以便将来加原生 SQL）。
var _ = gorm.Expr
