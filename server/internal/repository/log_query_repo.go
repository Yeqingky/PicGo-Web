package repository

import (
	"strings"

	"gorm.io/gorm"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// 本文件给 LogRepo 追加**查询侧**方法（写入侧在 log_repo.go）。
//
// Go 允许把同一类型的方法分散在同包的不同文件里，因此 W6 无需改动 W3 的文件。

// LogListFilter 是操作日志的查询条件（docs/API.md §9）。
//
// UserUID 为空表示不按操作者过滤 —— **仅管理员**可使用（普通用户由 handler 强制
// 填入自己的 UID，见 log service 的说明）。
type LogListFilter struct {
	// Types 精确匹配（多值 = OR）。
	Types      []string
	Status     string
	Keyword    string
	UserUID    string
	TargetType string
	TargetUID  string
	From       int64
	To         int64

	Sort     string // CreatedAt（默认）
	Order    string // desc（默认）
	Page     int
	PageSize int
}

// List 分页查询操作日志。
func (r *LogRepo) List(f LogListFilter) ([]model.OperationLog, int64, error) {
	q := r.db.Model(&model.OperationLog{})

	if len(f.Types) > 0 {
		q = q.Where(col("Type")+" IN ?", f.Types)
	}
	if s := strings.TrimSpace(f.Status); s != "" {
		q = q.Where(map[string]any{"Status": s})
	}
	if uid := strings.TrimSpace(f.UserUID); uid != "" {
		q = q.Where(map[string]any{"UserUID": uid})
	}
	if tt := strings.TrimSpace(f.TargetType); tt != "" {
		q = q.Where(map[string]any{"TargetType": tt})
	}
	if tu := strings.TrimSpace(f.TargetUID); tu != "" {
		q = q.Where(map[string]any{"TargetUID": tu})
	}
	if f.From > 0 {
		q = q.Where(col("CreatedAt")+" >= ?", f.From)
	}
	if f.To > 0 {
		q = q.Where(col("CreatedAt")+" <= ?", f.To)
	}

	// 关键词匹配 Username / TargetUID / Detail / Error（docs/API.md §9）
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := "%" + escapeLike(kw) + "%"
		q = q.Where(
			`(`+col("Username")+` LIKE ? ESCAPE '\' OR `+col("TargetUID")+` LIKE ? ESCAPE '\' OR `+
				col("Detail")+` LIKE ? ESCAPE '\' OR `+col("Error")+` LIKE ? ESCAPE '\')`,
			like, like, like, like,
		)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, wrap(err)
	}

	order := col("CreatedAt")
	switch strings.ToLower(strings.TrimSpace(f.Sort)) {
	case "type":
		order = col("Type")
	case "status":
		order = col("Status")
	case "createdat", "":
		order = col("CreatedAt")
	}
	if strings.EqualFold(strings.TrimSpace(f.Order), "asc") {
		order += " ASC"
	} else {
		order += " DESC"
	}

	var out []model.OperationLog
	err := q.Order(order).
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		Find(&out).Error
	if err != nil {
		return nil, 0, wrap(err)
	}
	return out, total, nil
}

// FindByUID 查单条日志；不存在返回 ErrNotFound。
func (r *LogRepo) FindByUID(uid string) (*model.OperationLog, error) {
	var l model.OperationLog
	if err := r.db.Where(map[string]any{"UID": uid}).First(&l).Error; err != nil {
		return nil, wrap(err)
	}
	return &l, nil
}

// PurgeBefore 删除早于 before 的操作日志，返回删除行数（D74 定时清理）。
func (r *LogRepo) PurgeBefore(before int64) (int64, error) {
	res := r.db.Where(col("CreatedAt")+" < ?", before).Delete(&model.OperationLog{})
	if res.Error != nil {
		return 0, wrap(res.Error)
	}
	return res.RowsAffected, nil
}

// EmailLogRepo 是 EmailLogs 的数据访问（D29：**不存邮件正文**）。
type EmailLogRepo struct{ db *gorm.DB }

// NewEmailLogRepo 构造。
func NewEmailLogRepo(db *gorm.DB) *EmailLogRepo { return &EmailLogRepo{db: db} }

// EmailListFilter 是邮件日志的查询条件。
type EmailListFilter struct {
	ToAddress string
	Template  string
	Status    string
	From      int64
	To        int64
	Page      int
	PageSize  int
}

// Insert 写入一条邮件日志。
func (r *EmailLogRepo) Insert(l *model.EmailLog) error { return wrap(r.db.Create(l).Error) }

// List 分页查询邮件日志。
func (r *EmailLogRepo) List(f EmailListFilter) ([]model.EmailLog, int64, error) {
	q := r.db.Model(&model.EmailLog{})
	if a := strings.TrimSpace(f.ToAddress); a != "" {
		like := "%" + escapeLike(a) + "%"
		q = q.Where(col("ToAddress")+" LIKE ? ESCAPE '\\'", like)
	}
	if t := strings.TrimSpace(f.Template); t != "" {
		q = q.Where(map[string]any{"Template": t})
	}
	if s := strings.TrimSpace(f.Status); s != "" {
		q = q.Where(map[string]any{"Status": s})
	}
	if f.From > 0 {
		q = q.Where(col("CreatedAt")+" >= ?", f.From)
	}
	if f.To > 0 {
		q = q.Where(col("CreatedAt")+" <= ?", f.To)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, wrap(err)
	}

	var out []model.EmailLog
	err := q.Order(col("CreatedAt") + " DESC").
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		Find(&out).Error
	if err != nil {
		return nil, 0, wrap(err)
	}
	return out, total, nil
}

// FindByUID 查单封邮件记录。
func (r *EmailLogRepo) FindByUID(uid string) (*model.EmailLog, error) {
	var l model.EmailLog
	if err := r.db.Where(map[string]any{"UID": uid}).First(&l).Error; err != nil {
		return nil, wrap(err)
	}
	return &l, nil
}
