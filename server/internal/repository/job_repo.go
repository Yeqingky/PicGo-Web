package repository

import (
	"strings"

	"gorm.io/gorm"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// JobRepo 是 Jobs / JobItems / JobLogs 的数据访问。
//
// 三张表同属「一次批次任务」，放同一个 repo 是因为它们的生命周期完全绑定：
// 删除一个 job 必须同时删掉它的 items 与 logs（否则留下无主数据）。
type JobRepo struct{ db *gorm.DB }

// NewJobRepo 构造。
func NewJobRepo(db *gorm.DB) *JobRepo { return &JobRepo{db: db} }

// JobListFilter 是任务列表的查询条件。
//
// UserUID 为空表示不按用户过滤（仅管理员，`Scope = all`）。
type JobListFilter struct {
	UserUID  string
	Kind     string
	Status   string
	Page     int
	PageSize int
}

// CreateWithItems 在**单事务**内创建 job 与其全部子项。
//
// 一次性创建的理由：半成品 job（有 job 没 items）会让 worker 无从下手，
// 而 job 与 items 之间没有「先创建 job 后补 items」的合法中间态。
func (r *JobRepo) CreateWithItems(job *model.Job, items []model.JobItem) error {
	return wrap(r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(job).Error; err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		// JobItem 无 UID，身份是「父 job 的 UID + Seq」（docs/API.md §8）
		return tx.Create(&items).Error
	}))
}

// FindByUID 按 UID 查 job；不存在返回 ErrNotFound。
func (r *JobRepo) FindByUID(uid string) (*model.Job, error) {
	var j model.Job
	if err := r.db.Where(map[string]any{"UID": uid}).First(&j).Error; err != nil {
		return nil, wrap(err)
	}
	return &j, nil
}

// List 分页查询 job。
func (r *JobRepo) List(f JobListFilter) ([]model.Job, int64, error) {
	q := r.db.Model(&model.Job{})
	if f.UserUID != "" {
		q = q.Where(map[string]any{"UserUID": f.UserUID})
	}
	if k := strings.TrimSpace(f.Kind); k != "" {
		q = q.Where(map[string]any{"Kind": k})
	}
	if s := strings.TrimSpace(f.Status); s != "" {
		q = q.Where(map[string]any{"Status": s})
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, wrap(err)
	}

	var out []model.Job
	err := q.Order(col("CreatedAt") + " DESC").
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		Find(&out).Error
	if err != nil {
		return nil, 0, wrap(err)
	}
	return out, total, nil
}

// UpdateFields 更新 job 的指定列。
func (r *JobRepo) UpdateFields(uid string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	res := r.db.Model(&model.Job{}).Where(map[string]any{"UID": uid}).Updates(fields)
	if res.Error != nil {
		return wrap(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateItem 更新一个子项的指定列。
func (r *JobRepo) UpdateItem(jobUID string, seq int, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	res := r.db.Model(&model.JobItem{}).
		Where(map[string]any{"JobUID": jobUID, "Seq": seq}).
		Updates(fields)
	if res.Error != nil {
		return wrap(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListItems 返回某 job 的全部子项（按 Seq 升序，保证展示稳定）。
func (r *JobRepo) ListItems(jobUID string) ([]model.JobItem, error) {
	var out []model.JobItem
	err := r.db.Where(map[string]any{"JobUID": jobUID}).
		Order(col("Seq") + " ASC").Find(&out).Error
	if err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// DeleteWithChildren 删除已结束的 job 及其 items / logs。
//
// 调用方必须先确认 job 已结束（运行中的删除由 service 拒绝，见 docs/API.md §8）。
func (r *JobRepo) DeleteWithChildren(uid string) error {
	return wrap(r.db.Transaction(func(tx *gorm.DB) error {
		for _, table := range []any{&model.JobItem{}, &model.JobLog{}} {
			if err := tx.Where(map[string]any{"JobUID": uid}).Delete(table).Error; err != nil {
				return err
			}
		}
		res := tx.Where(map[string]any{"UID": uid}).Delete(&model.Job{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	}))
}

// ---- JobLogs ----

// AppendLog 追加一行任务日志，返回该行的 Seq（从 1 开始，批内递增）。
//
// Seq 的取法：查当前最大 Seq + 1。这里**不做加锁**，因为同一 job 的日志
// 由单线程（该 job 的 worker）写入；跨 job 之间互不影响。
func (r *JobRepo) AppendLog(jobUID, line string) (int, error) {
	var maxSeq *int
	err := r.db.Model(&model.JobLog{}).
		Where(map[string]any{"JobUID": jobUID}).
		Select("MAX(" + col("Seq") + ")").Scan(&maxSeq).Error
	if err != nil {
		return 0, wrap(err)
	}
	seq := 1
	if maxSeq != nil {
		seq = *maxSeq + 1
	}

	entry := &model.JobLog{
		JobUID:    jobUID,
		Seq:       seq,
		Line:      line,
		CreatedAt: model.Now(),
	}
	if err := r.db.Create(entry).Error; err != nil {
		return 0, wrap(err)
	}
	return seq, nil
}

// ListLogs 增量拉取任务日志（返回 Seq > afterSeq 的行）。
func (r *JobRepo) ListLogs(jobUID string, afterSeq, limit int) ([]model.JobLog, error) {
	if limit <= 0 {
		limit = 500
	}
	var out []model.JobLog
	err := r.db.Where(map[string]any{"JobUID": jobUID}).
		Where(col("Seq")+" > ?", afterSeq).
		Order(col("Seq") + " ASC").
		Limit(limit).
		Find(&out).Error
	if err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// ---- 队列与恢复 ----

// CountActive 统计未结束的 job 数（queued + running），用于队列长度上限（D41）。
func (r *JobRepo) CountActive() (int64, error) {
	var n int64
	err := r.db.Model(&model.Job{}).
		Where(col("Status")+" IN ?", []string{model.JobStatusQueued, model.JobStatusRunning}).
		Count(&n).Error
	if err != nil {
		return 0, wrap(err)
	}
	return n, nil
}

// ListActive 返回全部未结束的 job（启动恢复与「服务重启后重新入队」用）。
func (r *JobRepo) ListActive() ([]model.Job, error) {
	var out []model.Job
	err := r.db.Where(col("Status")+" IN ?", []string{model.JobStatusQueued, model.JobStatusRunning}).
		Order(col("CreatedAt") + " ASC").Find(&out).Error
	if err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// RecoverInterrupted 在启动时把「进程崩溃时留下的在途状态」复位。
//
// 语义（docs/OPERATIONS.md §2）：
//   - 所有 `JobItems.Status = running` → 重置为 `queued`，`StartedAt` 清零
//   - 所有 `Jobs.Status = running`     → 重置为 `queued`，`StartedAt`/`FinishedAt` 清零
//
// 返回被复位的 item 行数。
//
// 之所以不「直接标失败」：上传是可以重试的，而用户并不知道进程崩过；
// 直接判失败会造成「明明还能传却显示失败」。若源文件真的没了，
// worker 重新拾起时会失败并写明「源文件已清理」。
func (r *JobRepo) RecoverInterrupted() (int64, error) {
	var affected int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.JobItem{}).
			Where(map[string]any{"Status": model.JobStatusRunning}).
			Updates(map[string]any{"Status": model.JobStatusQueued, "StartedAt": 0})
		if res.Error != nil {
			return res.Error
		}
		affected = res.RowsAffected

		return tx.Model(&model.Job{}).
			Where(map[string]any{"Status": model.JobStatusRunning}).
			Updates(map[string]any{
				"Status":     model.JobStatusQueued,
				"StartedAt":  0,
				"FinishedAt": 0,
			}).Error
	})
	if err != nil {
		return 0, wrap(err)
	}
	return affected, nil
}

// ListQueuedItems 返回某 job 中待处理的子项（按 Seq 升序）。
func (r *JobRepo) ListQueuedItems(jobUID string) ([]model.JobItem, error) {
	var out []model.JobItem
	err := r.db.Where(map[string]any{"JobUID": jobUID, "Status": model.JobStatusQueued}).
		Order(col("Seq") + " ASC").Find(&out).Error
	if err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// CountItemsByStatus 统计某 job 的子项状态分布（用于算 job 进度与终态）。
func (r *JobRepo) CountItemsByStatus(jobUID string) (map[string]int64, error) {
	var rows []struct {
		Status string
		Cnt    int64
	}
	err := r.db.Model(&model.JobItem{}).
		Select(col("Status") + ", COUNT(*) AS " + col("Cnt")).
		Where(map[string]any{"JobUID": jobUID}).
		Group("Status").
		Scan(&rows).Error
	if err != nil {
		return nil, wrap(err)
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.Status] = row.Cnt
	}
	return out, nil
}

// ---- 定时清理（W6） ----

// PurgeLogsBefore 删除早于 before 的任务日志，返回删除行数。
func (r *JobRepo) PurgeLogsBefore(before int64) (int64, error) {
	res := r.db.Where(col("CreatedAt")+" < ?", before).Delete(&model.JobLog{})
	if res.Error != nil {
		return 0, wrap(res.Error)
	}
	return res.RowsAffected, nil
}

// FailOrphanedPlugins 把插件类（`plugin.%`）非终结任务置为 failed。
//
// 用于启动恢复：agent 的插件 job 是内存态，Go 重启（或 agent 换进程）后
// 既没有 worker 会拾取它，也无法再得知真实结果 —— 与其永远显示
// 「进行中」，不如明确标失败并说明原因。上传 job 不在此列：
// RecoverInterrupted 已把它们重置为 queued 交给 worker 重试。
func (r *JobRepo) FailOrphanedPlugins() (int64, error) {
	res := r.db.Model(&model.Job{}).
		Where(map[string]any{"Status": []string{model.JobStatusQueued, model.JobStatusRunning}}).
		Where(col("Kind")+" LIKE ?", "plugin.%").
		Updates(map[string]any{
			"Status":     model.JobStatusFailed,
			"Error":      "服务或内核重启导致任务中断，结果未知",
			"FinishedAt": model.Now(),
		})
	if res.Error != nil {
		return 0, wrap(res.Error)
	}
	return res.RowsAffected, nil
}

// PurgeFinishedJobsBefore 删除早于 before 的**已结束** job 及其子项与日志。
func (r *JobRepo) PurgeFinishedJobsBefore(before int64) (int64, error) {
	var deleted int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var uids []string
		err := tx.Model(&model.Job{}).
			Select(col("UID")).
			Where(col("Status")+" IN ?", []string{model.JobStatusSucceeded, model.JobStatusFailed}).
			Where(col("CreatedAt")+" < ?", before).
			Pluck("UID", &uids).Error
		if err != nil {
			return err
		}
		if len(uids) == 0 {
			return nil
		}

		for _, table := range []any{&model.JobItem{}, &model.JobLog{}} {
			if err := tx.Where(col("JobUID")+" IN ?", uids).Delete(table).Error; err != nil {
				return err
			}
		}
		res := tx.Where(col("UID")+" IN ?", uids).Delete(&model.Job{})
		if res.Error != nil {
			return res.Error
		}
		deleted = res.RowsAffected
		return nil
	})
	if err != nil {
		return 0, wrap(err)
	}
	return deleted, nil
}
