package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// JobService 负责任务的查询与清理。
//
// 说明：**任务的推进**属于 UploadService（它才是队列的执行者）。
// 本服务只做「读」与「清理」，这样职责清晰：
//
//	UploadService  写 job（推进状态、结算）
//	JobService     读 job（列表/详情/日志）+ 删除已结束的任务
type JobService struct {
	log     *slog.Logger
	jobs    *repository.JobRepo
	uploads *repository.UploadRepo
	audit   *AuditService
}

// NewJobService 构造。
func NewJobService(
	log *slog.Logger,
	jobs *repository.JobRepo,
	uploads *repository.UploadRepo,
	audit *AuditService,
) *JobService {
	return &JobService{log: log, jobs: jobs, uploads: uploads, audit: audit}
}

// JobView 是对外的任务对象（字段与 Jobs 表一一对应）。
type JobView struct {
	UID      string `json:"UID"`
	Kind     string `json:"Kind"`
	Status   string `json:"Status"`
	Progress int    `json:"Progress"`

	UserUID    string `json:"UserUID"`
	StorageUID string `json:"StorageUID"`

	TotalItems     int `json:"TotalItems"`
	SucceededItems int `json:"SucceededItems"`
	FailedItems    int `json:"FailedItems"`
	SkippedItems   int `json:"SkippedItems"`

	Payload map[string]any `json:"Payload"`
	Result  any            `json:"Result"`
	Error   string         `json:"Error"`

	CreatedAt  int64 `json:"CreatedAt"`
	StartedAt  int64 `json:"StartedAt"`
	FinishedAt int64 `json:"FinishedAt"`

	// Items 只在详情接口返回（列表不带，避免响应过大）。
	Items []JobItemView `json:"Items,omitempty"`
}

// JobItemView 是任务内的单个子项。
//
// 身份是「父 job 的 UID + Seq」—— `JobItems` 表没有 UID 列（docs/API.md §8）。
type JobItemView struct {
	Seq       int    `json:"Seq"`
	UploadUID string `json:"UploadUID"`
	FileName  string `json:"FileName"`
	Status    string `json:"Status"`
	Attempts  int    `json:"Attempts"`
	Error     string `json:"Error"`

	StartedAt  int64 `json:"StartedAt"`
	FinishedAt int64 `json:"FinishedAt"`
}

// JobListInput 是任务列表入参。
type JobListInput struct {
	Kind   string
	Status string
	// Scope mine（默认）| all（仅管理员；普通用户静默降级）
	Scope string

	Page     int
	PageSize int
}

// List 分页查询任务。
func (s *JobService) List(in JobListInput, viewer *model.User) ([]JobView, int64, error) {
	if viewer == nil {
		return nil, 0, Errorf(response.CodeUnauthorized, "未登录")
	}
	page, size := normalizePage(in.Page, in.PageSize)

	f := repository.JobListFilter{
		Kind:     in.Kind,
		Status:   in.Status,
		Page:     page,
		PageSize: size,
	}
	if strings.EqualFold(strings.TrimSpace(in.Scope), "all") && viewer.IsAdmin() {
		// 管理员看全部
	} else {
		f.UserUID = viewer.UID
	}

	rows, total, err := s.jobs.List(f)
	if err != nil {
		return nil, 0, Wrap(response.CodeInternal, "查询任务失败", err)
	}

	out := make([]JobView, 0, len(rows))
	for i := range rows {
		out = append(out, toJobView(&rows[i], false))
	}
	return out, total, nil
}

// Get 取任务详情（含子项）。
func (s *JobService) Get(uid string, viewer *model.User) (*JobView, error) {
	job, err := s.jobs.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "任务不存在")
	}
	if err := assertJobAccess(job, viewer); err != nil {
		return nil, err
	}

	view := toJobView(job, true)

	items, err := s.jobs.ListItems(uid)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "查询任务子项失败", err)
	}
	view.Items = make([]JobItemView, 0, len(items))
	for _, it := range items {
		view.Items = append(view.Items, JobItemView{
			Seq: it.Seq, UploadUID: it.UploadUID, FileName: it.FileName,
			Status: it.Status, Attempts: it.Attempts, Error: it.Error,
			StartedAt: it.StartedAt, FinishedAt: it.FinishedAt,
		})
	}
	return &view, nil
}

// JobLogView 是一行任务日志。
type JobLogView struct {
	Seq       int    `json:"Seq"`
	Line      string `json:"Line"`
	CreatedAt int64  `json:"CreatedAt"`
}

// JobLogsResult 是任务日志的增量拉取结果。
type JobLogsResult struct {
	Items   []JobLogView `json:"Items"`
	HasMore bool         `json:"HasMore"`
	LastSeq int          `json:"LastSeq"`
}

// listLogsLimitDefault / Max 是日志拉取的分页口径（docs/API.md §8）。
const (
	listLogsLimitDefault = 500
	listLogsLimitMax     = 2000
)

// Logs 增量拉取任务日志（`AfterSeq` 之后的行）。
func (s *JobService) Logs(uid string, afterSeq, limit int, viewer *model.User) (*JobLogsResult, error) {
	job, err := s.jobs.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "任务不存在")
	}
	if err := assertJobAccess(job, viewer); err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = listLogsLimitDefault
	}
	if limit > listLogsLimitMax {
		limit = listLogsLimitMax
	}
	if afterSeq < 0 {
		afterSeq = 0
	}

	// 多取一条用于判断 HasMore（避免再发一次 COUNT）
	rows, err := s.jobs.ListLogs(uid, afterSeq, limit+1)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "查询任务日志失败", err)
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	out := &JobLogsResult{Items: make([]JobLogView, 0, len(rows)), HasMore: hasMore, LastSeq: afterSeq}
	for _, r := range rows {
		out.Items = append(out.Items, JobLogView{Seq: r.Seq, Line: r.Line, CreatedAt: r.CreatedAt})
		out.LastSeq = r.Seq
	}
	return out, nil
}

// Delete 清理**已结束**的任务及其子项与日志。
//
// 运行中的任务拒绝删除（40901）—— 否则 worker 会在写回状态时找不到 job。
func (s *JobService) Delete(ctx context.Context, uid string, viewer *model.User, clientIP, userAgent string) error {
	job, err := s.jobs.FindByUID(uid)
	if err != nil {
		return notFoundOr(err, "任务不存在")
	}
	if err := assertJobAccess(job, viewer); err != nil {
		return err
	}
	if !job.Finished() {
		return Errorf(response.CodeConflict, "任务尚未结束，无法清理")
	}

	if err := s.jobs.DeleteWithChildren(uid); err != nil {
		return notFoundOr(err, "任务不存在")
	}

	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeUpload, Status: model.LogStatusSuccess,
		UserUID: viewer.UID, TargetType: "job", TargetUID: uid,
		Detail:   map[string]any{"action": "delete"},
		ClientIP: clientIP, UserAgent: userAgent,
	})
	return nil
}

// CountActive 返回未结束的任务数（供系统信息展示）。
func (s *JobService) CountActive() int64 {
	n, err := s.jobs.CountActive()
	if err != nil {
		return 0
	}
	return n
}

// ---------------------------------------------------------------------------
// 内部
// ---------------------------------------------------------------------------

func toJobView(j *model.Job, withItems bool) JobView {
	view := JobView{
		UID: j.UID, Kind: j.Kind, Status: j.Status, Progress: j.Progress,
		UserUID: j.UserUID, StorageUID: j.StorageUID,
		TotalItems: j.TotalItems, SucceededItems: j.SucceededItems,
		FailedItems: j.FailedItems, SkippedItems: j.SkippedItems,
		Payload:   parseJSONMap(j.Payload),
		Error:     j.Error,
		CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt,
	}
	if strings.TrimSpace(j.Result) != "" {
		var res any
		if err := json.Unmarshal([]byte(j.Result), &res); err == nil {
			view.Result = res
		}
	}
	if withItems {
		view.Items = []JobItemView{}
	}
	return view
}

// assertJobAccess 判断 viewer 是否能看该任务。
func assertJobAccess(job *model.Job, viewer *model.User) error {
	if viewer == nil {
		return Errorf(response.CodeUnauthorized, "未登录")
	}
	if viewer.IsAdmin() {
		return nil
	}
	// 系统任务（UserUID 为空）只对管理员可见
	if job.UserUID == "" || job.UserUID != viewer.UID {
		return Errorf(response.CodeForbidden, "无权查看他人的任务")
	}
	return nil
}
