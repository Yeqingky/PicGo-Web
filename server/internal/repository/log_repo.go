package repository

import (
	"gorm.io/gorm"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// LogRepo 是 OperationLogs（统一操作日志，D45）的写入侧。
//
// W3 只用到写入；查询/过滤（GET /logs）属于 W6，届时在本类型上追加方法即可。
type LogRepo struct{ db *gorm.DB }

// NewLogRepo 构造。
func NewLogRepo(db *gorm.DB) *LogRepo { return &LogRepo{db: db} }

// Insert 写入一条操作日志。
func (r *LogRepo) Insert(l *model.OperationLog) error {
	return wrap(r.db.Create(l).Error)
}
