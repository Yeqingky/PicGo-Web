package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/agent"
	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/events"
	"github.com/YeqingKy/PicGo-Web/server/internal/id"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// UploadService 是上传队列。
//
// 设计（D35/D36/D38/D39/D41 + docs/OPERATIONS.md §1）：
//
//	POST /uploads ──► 校验 ──► 1 个 Job + N 个 JobItem（写库）──► 入内存队列
//	                                │
//	                    worker pool（并发度 = upload.concurrency，默认 1）
//	                                │  逐 item：
//	                                ├─ 调 agent POST /api/upload（单文件 + 同步）
//	                                ├─ 成功：写 Uploads/UploadResults + 累加配额（同事务）
//	                                └─ 失败：按 retryTimes 指数退避重试，耗尽则标失败
//
// 关键取舍：
//
//   - **一个批次一个驱动**（D38）：`StorageUID` 存在 Job 上，`JobItems` 不重复存
//   - **并发度作用于 item 层**（D36）：job 之间可并行，job 内部逐项
//   - **进度** = 已完成 item / 总 item；同时逐项发 SSE
//   - **【重要】逐项进度由 Go 自己发**，不依赖 agent 的全局 upload.progress
//     （agent 的事件无 UserUID，转发会给所有人看到别人的上传）。见 events/bridge.go
type UploadService struct {
	cfg      *config.Config
	log      *slog.Logger
	settings *settings.Service
	uploads  *repository.UploadRepo
	jobs     *repository.JobRepo
	users    *repository.UserRepo
	storage  *StorageService
	ag       agent.Client
	hub      *events.Hub
	audit    *AuditService

	// uploadsRoot 是本地暂存目录的**绝对路径**。
	//
	// 为什么必须是绝对路径：暂存文件路径要**跨进程**传给 picgo-agent
	// （`POST /api/upload` 的 `Path`），而 agent 以自己的目录为 cwd ——
	// 相对路径会被它解析到 picgo-agent/data/... 从而找不到文件（踩过）。
	uploadsRoot string

	// ---- 队列状态 ----
	queue chan *queuedUpload

	poolMu   sync.Mutex
	poolStop chan struct{}
	poolWG   sync.WaitGroup
	poolSize int

	// patchChecker 报告 picgo-core 的补丁是否齐备（可为 nil = 不检查）。
	//
	// 为什么需要：`UploadOptions.uploader` 是本项目给 PicGo-Core 打的补丁。
	// 若依赖被换成上游原版，并发时两个批次会互相覆盖 `picBed.uploader`，
	// **静默把图传到错误的图床**（不报错、链接可用，只是落错地方）。
	// 因此补丁缺失时必须把并发度强制降到 1（安全但慢）。
	patchChecker func() bool
	// patchWarned 保证「补丁缺失」的告警只打一次（避免刷日志）。
	patchWarned atomic.Bool

	// accepting 为假时拒绝新任务（优雅关闭中）。
	accepting atomic.Bool
	// started 标记 Start 是否已被调用（未启动时不允许入队）。
	started atomic.Bool

	// inflight 记录正在被 worker 处理的 item 数，供优雅关闭判断。
	inflight sync.WaitGroup

	ctx    context.Context
	cancel context.CancelFunc
}

// queuedUpload 是队列里的一项（一个文件）。
type queuedUpload struct {
	JobUID string
	Seq    int

	UploadUID string
	UserUID   string

	// FilePath 本地暂存文件的绝对路径。
	FilePath string
	// OriginalName 用户看到的原始文件名。
	OriginalName string
	Size         int64
	KeepLocal    bool

	// Target 是入队时**快照**下来的存储目标与模板。
	//
	// 为什么快照而不是出队时再查：用户可能在队列推进过程中改配置；
	// 快照保证「这一批用哪个图床、什么模板」在提交时就已确定，行为可预期。
	Target UploadTarget
}

// NewUploadService 构造。
func NewUploadService(
	cfg *config.Config,
	log *slog.Logger,
	settingsSvc *settings.Service,
	uploads *repository.UploadRepo,
	jobs *repository.JobRepo,
	users *repository.UserRepo,
	storage *StorageService,
	ag agent.Client,
	hub *events.Hub,
	audit *AuditService,
) *UploadService {
	// 暂存目录的绝对路径：跨进程传给 agent 时必须是绝对的（见 uploadsRoot 注释）
	root := cfg.UploadsDir()
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}

	return &UploadService{
		cfg:         cfg,
		log:         log,
		settings:    settingsSvc,
		uploads:     uploads,
		jobs:        jobs,
		users:       users,
		storage:     storage,
		ag:          ag,
		hub:         hub,
		audit:       audit,
		uploadsRoot: root,
	}
}

// ---------------------------------------------------------------------------
// 生命周期
// ---------------------------------------------------------------------------

// Start 做启动恢复并拉起 worker。
//
// 恢复语义（docs/OPERATIONS.md §2）：把「上次进程崩溃时留下的 running」复位成 queued，
// 然后重新入队。这样用户不会因为服务重启而丢掉上传。
func (s *UploadService) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	// 1. 复位在途状态
	affected, err := s.jobs.RecoverInterrupted()
	if err != nil {
		return Wrap(response.CodeInternal, "恢复中断的上传任务失败", err)
	}
	if affected > 0 {
		s.log.Info("已复位被中断的上传项", "items", affected)
	}

	// 2. 队列容量取设置（上限夹紧，避免异常配置导致巨量内存）
	capacity := int(s.settings.GetInt("upload.queueMaxLength", 1000))
	if capacity < 64 {
		capacity = 64
	}
	if capacity > 100000 {
		capacity = 100000
	}
	s.queue = make(chan *queuedUpload, capacity)

	// 3. 拉起 worker
	s.poolStop = make(chan struct{})
	s.poolSize = s.concurrency()
	s.startWorkersLocked(s.poolSize, s.poolStop)

	s.accepting.Store(true)
	s.started.Store(true)

	// 4. 重新入队未完成的项
	requeued, dropped := s.requeuePending()
	s.log.Info("上传队列已启动",
		"concurrency", s.poolSize, "capacity", capacity,
		"requeued", requeued, "dropped_missing_file", dropped)

	// 5. 配置变更时动态调整并发度
	s.settings.OnChanged(func(ev settings.ChangedEvent) {
		if ev.Key == "upload.concurrency" {
			s.RefreshConcurrency()
		}
	})
	return nil
}

// Shutdown 停止接受新任务并等待在途项完成（最多等待 ctx 的超时）。
//
// 超时后：未完成的项保持 `queued`（下次启动会被恢复），不会丢数据。
func (s *UploadService) Shutdown(ctx context.Context) {
	if !s.started.Load() {
		return
	}
	s.accepting.Store(false)
	s.log.Info("上传队列开始优雅关闭")

	// 让 worker 处理完当前项后退出
	s.poolMu.Lock()
	if s.poolStop != nil {
		close(s.poolStop)
		s.poolStop = nil
	}
	s.poolMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.poolWG.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.log.Info("上传队列已全部停止")
	case <-ctx.Done():
		// 超时：worker 会继续跑完当前项（不强制中断，避免半途写坏状态），
		// 但进程随后退出，剩下的项留给下次启动恢复。
		s.log.Warn("上传队列优雅关闭超时，剩余项将在下次启动时恢复")
	}

	if s.cancel != nil {
		s.cancel()
	}
	s.started.Store(false)
}

// SetPatchChecker 注入「picgo-core 补丁是否齐备」的判据。
//
// 由组合根（main）接上 `agent.StatusHolder.PatchesComplete`。
// 传 nil 表示不检查（测试与 MOCK 模式）。
func (s *UploadService) SetPatchChecker(fn func() bool) { s.patchChecker = fn }

// concurrency 返回当前应使用的并发度（至少 1）。
//
// ⚠️ **补丁缺失时强制返回 1**：并发需要 `UploadOptions.uploader` 才能把
// 「本批用哪个图床」隔离到各自的 context；没有它就只能靠全局 `picBed.uploader`，
// 并发必然互相覆盖。宁可慢，也不能把图传到错误的图床。
func (s *UploadService) concurrency() int {
	n := int(s.settings.GetInt("upload.concurrency", 1))
	if n < 1 {
		n = 1
	}
	if n > 64 {
		n = 64
	}

	if n > 1 && s.patchChecker != nil && !s.patchChecker() {
		if s.patchWarned.CompareAndSwap(false, true) {
			s.log.Warn("picgo-core 补丁缺失，并发上传不安全 —— 已强制降级为单并发"+
				"（请确认 agent 依赖是 @yeqingky/picgo-core 而非上游 picgo）",
				"requested", n, "effective", 1)
		}
		return 1
	}
	return n
}

// RefreshConcurrency 按当前设置与补丁状态重新计算并发度并调整 worker 池。
//
// 调用时机：
//   - `upload.concurrency` 设置变更（Start 里已注册）
//   - **agent 状态变化**（启动时 agent 尚未就绪 → 补丁状态未知 → 保守用 1；
//     agent 就绪且补丁齐备后需要升到配置值）
func (s *UploadService) RefreshConcurrency() {
	if !s.started.Load() {
		return
	}
	n := s.concurrency()
	s.poolMu.Lock()
	current := s.poolSize
	s.poolMu.Unlock()
	if n == current {
		return
	}
	s.Resize(n)
	s.log.Info("上传并发度已调整", "concurrency", n)
}

// Resize 调整 worker 数量（配置变更时调用）。
//
// 停旧启新，**不打断正在处理的项**：worker 只在「处理完当前项」后才检查退出信号。
func (s *UploadService) Resize(n int) {
	s.poolMu.Lock()
	defer s.poolMu.Unlock()

	if n < 1 {
		n = 1
	}
	if n > 64 {
		n = 64
	}
	if s.poolSize == n || s.poolStop == nil {
		return
	}

	close(s.poolStop)
	s.poolWG.Wait() // 等旧 worker 跑完当前项

	s.poolStop = make(chan struct{})
	s.poolSize = n
	s.startWorkersLocked(n, s.poolStop)
}

// startWorkersLocked 拉起 n 个 worker（调用方需持有 poolMu）。
//
// ⚠️ **stop 必须作为参数传入，不能让 worker 自己去 s.poolStop 读**：
// `Resize` 会持 `poolMu` 调 `poolWG.Wait()`，若 worker 在启动时也要拿
// `poolMu`，就形成「持锁等 worker 退出、worker 等锁退出」的**死锁** ——
// 表现为运行期一改 `upload.concurrency` 服务就卡死（已由并发闸门测试覆盖）。
func (s *UploadService) startWorkersLocked(n int, stop <-chan struct{}) {
	for i := 0; i < n; i++ {
		s.poolWG.Add(1)
		go s.worker(i, stop)
	}
}

// worker 是单个执行单元。
func (s *UploadService) worker(idx int, stop <-chan struct{}) {
	defer s.poolWG.Done()

	for {
		select {
		case <-stop:
			return
		case item, ok := <-s.queue:
			if !ok {
				return
			}
			if item == nil {
				continue
			}
			s.inflight.Add(1)
			s.processItem(s.ctx, item)
			s.inflight.Done()
		}
	}
}

// InflightCount 返回正在处理的项数（测试与健康检查用）。
func (s *UploadService) InflightCount() int {
	// sync.WaitGroup 没有计数读取；用队列长度近似表达「等待中」
	return len(s.queue)
}

// ---------------------------------------------------------------------------
// 入队
// ---------------------------------------------------------------------------

// IncomingFile 是一个已经落到本地暂存区的待上传文件。
//
// 由 handler 负责 multipart 解析与落盘（HTTP 关注点），service 负责校验与调度。
type IncomingFile struct {
	Path         string
	OriginalName string
	Size         int64
}

// BatchResult 是入队结果。
type BatchResult struct {
	JobUID     string      `json:"JobUID"`
	StorageUID string      `json:"StorageUID"`
	Items      []BatchItem `json:"Items"`
}

// BatchItem 是批次内的一项。
type BatchItem struct {
	Seq       int    `json:"Seq"`
	FileName  string `json:"FileName"`
	UploadUID string `json:"UploadUID"`
	Status    string `json:"Status"`
}

// EnqueueBatchInput 是入队入参。
type EnqueueBatchInput struct {
	Files      []IncomingFile
	StorageUID string
	// KeepLocal 覆盖全局 `upload.keepLocalCopy`。
	KeepLocal *bool
	// Source 记录来源（web / api / lsky），默认 web。
	Source string
}

// EnqueueBatch 校验并接受一批上传。
//
// 校验顺序（docs/API.md §4.1，任一失败即整体拒绝、**不产生 job**）：
//
//  1. 扩展名白名单（blockSvg 默认开启，额外拒 svg）
//  2. 单文件大小
//  3. 存储配置存在且启用
//  4. 配额（非管理员）
//  5. 上传限流（默认禁用；管理员跳过）
//  6. 队列长度
//
// ⚠️ **失败时本函数会清理已落盘的暂存文件**（调用方无需再处理）。
func (s *UploadService) EnqueueBatch(ctx context.Context, user *model.User, in EnqueueBatchInput) (*BatchResult, error) {
	if user == nil {
		return nil, Errorf(response.CodeUnauthorized, "未登录")
	}
	if !s.accepting.Load() {
		return nil, Errorf(response.CodeAgentUnavailable, "服务正在关闭，暂时无法接受上传")
	}
	if len(in.Files) == 0 {
		return nil, Errorf(response.CodeInvalidParam, "没有可上传的文件")
	}

	// 失败路径统一清理暂存文件
	cleanup := func() {
		for _, f := range in.Files {
			s.removeTemp(f.Path)
		}
	}

	// ---- 1/2. 文件级校验 ----
	maxSize := s.settings.GetInt("upload.maxSizeBytes", 20<<20)
	allowed := s.allowedExtensions()
	blockSvg := s.settings.GetBool("upload.blockSvg", true)

	var totalSize int64
	for _, f := range in.Files {
		ext := normalizeExt(filepath.Ext(f.OriginalName))
		if ext == "" {
			cleanup()
			return nil, Errorf(response.CodeInvalidParam, "文件「%s」没有扩展名", f.OriginalName)
		}
		if !allowed[ext] {
			cleanup()
			return nil, Errorf(response.CodeInvalidParam, "不支持的文件类型 .%s（允许：%s）",
				ext, strings.Join(sortedKeys(allowed), ", "))
		}
		if blockSvg && ext == "svg" {
			cleanup()
			return nil, Errorf(response.CodeInvalidParam, "站点已禁用 SVG 上传")
		}
		if f.Size > maxSize {
			cleanup()
			return nil, Errorf(response.CodeInvalidParam,
				"文件「%s」超过大小上限（%.1f MiB）", f.OriginalName, float64(maxSize)/(1<<20))
		}
		if f.Size <= 0 {
			cleanup()
			return nil, Errorf(response.CodeInvalidParam, "文件「%s」为空", f.OriginalName)
		}
		totalSize += f.Size
	}

	// ---- 3. 存储配置 ----
	target, err := s.storage.ResolveUploadTarget(in.StorageUID)
	if err != nil {
		cleanup()
		return nil, err
	}

	// ---- 4. 配额（D20/D21，管理员跳过）----
	if err := s.checkQuota(ctx, user, totalSize); err != nil {
		cleanup()
		return nil, err
	}

	// ---- 5. 上传限流（D73，默认禁用；管理员跳过）----
	if err := s.checkRateLimit(ctx, user, len(in.Files)); err != nil {
		cleanup()
		return nil, err
	}

	// ---- 6. 队列长度 ----
	if err := s.checkQueueCapacity(ctx); err != nil {
		cleanup()
		return nil, err
	}

	// ---- 创建 job + items ----
	jobUID := id.Job()
	now := model.Now()

	source := in.Source
	if source == "" {
		source = model.UploadSourceWeb
	}
	keepLocal := s.settings.GetBool("upload.keepLocalCopy", false)
	if in.KeepLocal != nil {
		keepLocal = *in.KeepLocal
	}

	payload := map[string]any{
		"StorageUID": target.StorageUID,
		"KeepLocal":  keepLocal,
		"Source":     source,
	}
	payloadJSON, _ := json.Marshal(payload)

	job := &model.Job{
		UID:        jobUID,
		Kind:       model.JobKindUpload,
		Status:     model.JobStatusQueued,
		Progress:   0,
		UserUID:    user.UID,
		StorageUID: target.StorageUID,
		TotalItems: len(in.Files),
		Payload:    string(payloadJSON),
		CreatedAt:  now,
	}

	items := make([]model.JobItem, 0, len(in.Files))
	uploads := make([]model.Upload, 0, len(in.Files))
	resultItems := make([]BatchItem, 0, len(in.Files))

	for i, f := range in.Files {
		seq := i + 1
		uploadUID := id.Upload()

		up := model.Upload{
			UID:          uploadUID,
			UserUID:      user.UID,
			StorageUID:   target.StorageUID,
			FileName:     filepath.Base(f.OriginalName),
			OriginalName: f.OriginalName,
			Size:         f.Size,
			Extension:    normalizeExt(filepath.Ext(f.OriginalName)),
			Status:       model.UploadStatusPending,
			Source:       source,
			JobUID:       jobUID,
			Metadata:     "{}",
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		uploads = append(uploads, up)

		items = append(items, model.JobItem{
			JobUID:    jobUID,
			Seq:       seq,
			UploadUID: uploadUID,
			FileName:  f.OriginalName,
			Status:    model.JobStatusQueued,
		})

		resultItems = append(resultItems, BatchItem{
			Seq:       seq,
			FileName:  f.OriginalName,
			UploadUID: uploadUID,
			Status:    model.JobStatusQueued,
		})
	}

	// 三张表一起写：job + items + uploads（单事务）
	if err := s.createBatch(job, items, uploads); err != nil {
		cleanup()
		return nil, err
	}

	// 记录暂存路径（供启动恢复找到源文件）；RawOutput 先占位为 "[]"
	for i, f := range in.Files {
		if err := s.uploads.SaveResult(uploads[i].UID, "[]", f.Path); err != nil {
			s.log.Warn("记录暂存路径失败（影响重启恢复）", "upload_uid", uploads[i].UID, "err", err)
		}
	}

	// ---- 入队 ----
	for i, f := range in.Files {
		q := &queuedUpload{
			JobUID:       jobUID,
			Seq:          i + 1,
			UploadUID:    uploads[i].UID,
			UserUID:      user.UID,
			FilePath:     f.Path,
			OriginalName: f.OriginalName,
			Size:         f.Size,
			KeepLocal:    keepLocal,
			Target:       *target,
		}
		select {
		case s.queue <- q:
		default:
			// 理论上到不了这里（入队前已检查队列长度）；真到了就如实标失败
			s.failItemOnEnqueue(jobUID, q.Seq, q.UploadUID, "队列已满")
		}
	}

	s.hub.PublishJobStarted(jobUID, model.JobKindUpload, user.UID)

	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeUpload, Status: model.LogStatusSuccess,
		UserUID: user.UID, TargetType: "storage", TargetUID: target.StorageUID,
		Detail: map[string]any{
			"action": "enqueue", "JobUID": jobUID,
			"Files": len(in.Files), "TotalBytes": totalSize,
			"UploaderType": target.Type, "ConfigName": target.ConfigName,
		},
	})

	return &BatchResult{JobUID: jobUID, StorageUID: target.StorageUID, Items: resultItems}, nil
}

// createBatch 在单事务内创建 job / items / uploads。
func (s *UploadService) createBatch(job *model.Job, items []model.JobItem, uploads []model.Upload) error {
	if err := s.jobs.CreateWithItems(job, items); err != nil {
		return Wrap(response.CodeInternal, "创建上传任务失败", err)
	}
	for i := range uploads {
		if err := s.uploads.Create(&uploads[i]); err != nil {
			// 已经建了 job/items；把 job 标失败，避免留下永不推进的任务
			_ = s.jobs.UpdateFields(job.UID, map[string]any{
				"Status":     model.JobStatusFailed,
				"Error":      "创建图片记录失败",
				"FinishedAt": model.Now(),
			})
			return Wrap(response.CodeInternal, "创建图片记录失败", err)
		}
	}
	return nil
}

func (s *UploadService) failItemOnEnqueue(jobUID string, seq int, uploadUID, reason string) {
	now := model.Now()
	_ = s.jobs.UpdateItem(jobUID, seq, map[string]any{
		"Status": model.JobStatusFailed, "Error": reason, "FinishedAt": now,
	})
	_ = s.uploads.UpdateFields(uploadUID, map[string]any{
		"Status": model.UploadStatusFailed, "Error": reason,
	})
}

// ---------------------------------------------------------------------------
// 校验
// ---------------------------------------------------------------------------

// checkQuota 校验配额（D20/D21）。
//
//	CapacityBytes == 0 → 不限额
//	管理员              → 跳过
//	UsedBytes + 本批 > Capacity → 40302
func (s *UploadService) checkQuota(ctx context.Context, user *model.User, totalSize int64) error {
	if user.IsAdmin() {
		return nil
	}
	// 以**数据库中的当前值**为准，而不是 token 里的快照
	fresh, err := s.users.FindByUID(user.UID)
	if err != nil {
		return Wrap(response.CodeInternal, "读取用户配额失败", err)
	}
	if fresh.Unlimited() {
		return nil
	}
	if fresh.UsedBytes+totalSize > fresh.CapacityBytes {
		return Errorf(response.CodeQuotaExceeded,
			"存储配额不足：已用 %.1f MiB / 上限 %.1f MiB，本次需要 %.1f MiB",
			float64(fresh.UsedBytes)/(1<<20),
			float64(fresh.CapacityBytes)/(1<<20),
			float64(totalSize)/(1<<20),
		)
	}
	return nil
}

// checkRateLimit 校验上传限流（D73）。
//
// **默认禁用**（`upload.rateLimit.enabled = false`）；启用后按**张数**、按用户计。
// 管理员跳过。
//
// 计数直接用 Uploads 表按时间窗 COUNT（D78：不为限流单独建表）。
func (s *UploadService) checkRateLimit(ctx context.Context, user *model.User, incoming int) error {
	if user.IsAdmin() {
		return nil
	}
	if !s.settings.GetBool("upload.rateLimit.enabled", false) {
		return nil
	}

	now := model.Now()
	perHour := s.settings.GetInt("upload.rateLimit.perHour", 100)
	perDay := s.settings.GetInt("upload.rateLimit.perDay", 500)
	action := strings.ToLower(strings.TrimSpace(s.settings.GetString("upload.rateLimit.action")))

	hourCount, err := s.uploads.CountSince(user.UID, now-3600)
	if err != nil {
		// 统计失败不拦截（限流是保护措施，不该因为统计故障而阻断业务）
		s.log.Warn("统计每小时上传张数失败，跳过限流校验", "err", err)
		return nil
	}
	dayCount, err := s.uploads.CountSince(user.UID, now-86400)
	if err != nil {
		s.log.Warn("统计每天上传张数失败，跳过限流校验", "err", err)
		return nil
	}

	var exceeded string
	if perHour > 0 && hourCount+int64(incoming) > perHour {
		exceeded = fmt.Sprintf("每小时最多 %d 张（本小时已传 %d 张）", perHour, hourCount)
	} else if perDay > 0 && dayCount+int64(incoming) > perDay {
		exceeded = fmt.Sprintf("每天最多 %d 张（今天已传 %d 张）", perDay, dayCount)
	}
	if exceeded == "" {
		return nil
	}

	// action = log：只记录不拦截（供管理员观察真实用量后再决定是否收紧）
	if action == "log" {
		s.audit.Log(ctx, AuditEntry{
			Type: model.LogTypeUpload, Status: model.LogStatusFailed,
			UserUID: user.UID, TargetType: "upload",
			Detail: map[string]any{"action": "rate_limit_would_block", "Reason": exceeded, "Incoming": incoming},
			Cause:  fmt.Errorf("%s", exceeded),
		})
		s.log.Warn("上传限流命中（action=log，仅记录）", "user", user.UID, "reason", exceeded)
		return nil
	}

	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeUpload, Status: model.LogStatusFailed,
		UserUID: user.UID, TargetType: "upload",
		Detail: map[string]any{"action": "rate_limited", "Reason": exceeded, "Incoming": incoming},
		Cause:  fmt.Errorf("%s", exceeded),
	})
	return Errorf(response.CodeTooMany, "上传过于频繁：%s", exceeded)
}

// checkQueueCapacity 校验队列长度上限（D41）。
func (s *UploadService) checkQueueCapacity(ctx context.Context) error {
	maxLen := s.settings.GetInt("upload.queueMaxLength", 1000)
	if maxLen <= 0 {
		return nil
	}
	active, err := s.jobs.CountActive()
	if err != nil {
		s.log.Warn("统计活动任务数失败，跳过队列上限校验", "err", err)
		return nil
	}
	if active >= maxLen {
		return Errorf(response.CodeTooMany,
			"上传队列已满（%d/%d），请等待当前任务完成", active, maxLen)
	}
	return nil
}

// allowedExtensions 返回允许上传的扩展名集合（小写、不含点）。
func (s *UploadService) allowedExtensions() map[string]bool {
	list := s.settings.GetStringSlice("upload.allowedExts",
		[]string{"jpg", "jpeg", "png", "gif", "webp", "bmp", "svg", "ico", "avif"})
	out := make(map[string]bool, len(list))
	for _, e := range list {
		if e = normalizeExt(e); e != "" {
			out[e] = true
		}
	}
	if len(out) == 0 {
		// 配置被清空时给出兜底，避免「什么都传不了」这种无解状态
		out = map[string]bool{"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true}
	}
	return out
}

// ---------------------------------------------------------------------------
// worker：处理单项
// ---------------------------------------------------------------------------

// processItem 处理一个文件（含重试）。
func (s *UploadService) processItem(ctx context.Context, item *queuedUpload) {
	now := model.Now()

	// 源文件必须存在 —— 服务重启后暂存可能已被清理
	if _, err := os.Stat(item.FilePath); err != nil {
		s.finishItemFailed(ctx, item, fmt.Sprintf("源文件已清理（%s）", item.FilePath), 1)
		return
	}

	// 标记 running
	if err := s.jobs.UpdateItem(item.JobUID, item.Seq, map[string]any{
		"Status": model.JobStatusRunning, "StartedAt": now,
	}); err != nil {
		s.log.Warn("标记上传项为运行中失败", "job", item.JobUID, "seq", item.Seq, "err", err)
	}
	_ = s.uploads.UpdateFields(item.UploadUID, map[string]any{"Status": model.UploadStatusPending})

	// 逐项进度：起点
	s.hub.PublishUploadProgress(item.JobUID, item.UploadUID, item.Seq, item.OriginalName, 0, item.UserUID)

	maxAttempts := 1 + int(s.settings.GetInt("upload.retryTimes", 1))
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	backoffMs := s.settings.GetInt("upload.retryBackoffMs", 2000)

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			// 进程正在退出：保持 queued，交给下次启动恢复
			_ = s.jobs.UpdateItem(item.JobUID, item.Seq, map[string]any{
				"Status": model.JobStatusQueued, "StartedAt": 0,
			})
			return
		}

		res, err := s.ag.Upload(ctx, agent.UploadRequest{
			Path:                 item.FilePath,
			Uploader:             &agent.UploadTarget{Type: item.Target.Type, ConfigName: item.Target.ConfigName},
			JobUID:               item.JobUID,
			Seq:                  item.Seq,
			PathTemplate:         item.Target.PathTemplate,
			FileTemplate:         item.Target.FileTemplate,
			UserUID:              item.UserUID,
			SupportsPathTemplate: boolPtr(item.Target.SupportsPathTemplate),
		})
		if err == nil {
			s.finishItemSucceeded(ctx, item, res, attempt)
			return
		}

		lastErr = err

		// 目标非法 / 参数错**不重试**：重试多少次都是同样的错
		if agent.CodeOf(err) == response.CodeInvalidParam {
			s.finishItemFailed(ctx, item, agent.MessageOf(err), attempt)
			return
		}
		// 内核不可用也不重试：整个 agent 都联系不上，重试只会拖长队列
		if agent.IsUnavailable(err) {
			s.finishItemFailed(ctx, item, agent.MessageOf(err), attempt)
			return
		}

		if attempt < maxAttempts {
			wait := time.Duration(backoffMs) * time.Millisecond * time.Duration(1<<(attempt-1))
			if wait > 30*time.Second {
				wait = 30 * time.Second
			}
			s.log.Info("上传失败将重试",
				"job", item.JobUID, "seq", item.Seq, "attempt", attempt,
				"wait", wait.String(), "err", err)
			select {
			case <-ctx.Done():
				_ = s.jobs.UpdateItem(item.JobUID, item.Seq, map[string]any{
					"Status": model.JobStatusQueued, "StartedAt": 0,
				})
				return
			case <-time.After(wait):
			}
		}
	}

	s.finishItemFailed(ctx, item, agent.MessageOf(lastErr), maxAttempts)
}

// finishItemSucceeded 处理成功：写记录 + 累加配额（同事务）+ 发事件 + 收尾 job。
func (s *UploadService) finishItemSucceeded(ctx context.Context, item *queuedUpload, res *agent.UploadData, attempts int) {
	now := model.Now()

	// 把 agent 返回的原始 IImgInfo **原样**存起来（D47：删除远端时必须交回）
	rawJSON, err := json.Marshal([]agent.RawImgInfo{res.Raw})
	if err != nil {
		rawJSON = []byte("[]")
	}
	if err := s.uploads.SaveResult(item.UploadUID, string(rawJSON), item.FilePath); err != nil {
		s.log.Warn("保存上传原始返回值失败（将影响远端删除）",
			"upload_uid", item.UploadUID, "err", err)
	}

	fields := map[string]any{
		"Status":    model.UploadStatusSuccess,
		"Error":     "",
		"URL":       res.URL,
		"ThumbURL":  res.ThumbURL,
		"FileName":  nonEmpty(res.FileName, filepath.Base(item.FilePath)),
		"Width":     res.Width,
		"Height":    res.Height,
		"Size":      res.Size,
		"MimeType":  res.ContentType,
		"Extension": strings.TrimPrefix(normalizeExt(res.Extname), "."),
	}
	if fields["Size"].(int64) == 0 {
		fields["Size"] = item.Size
	}
	if fields["Extension"] == "" {
		fields["Extension"] = normalizeExt(filepath.Ext(item.OriginalName))
	}

	// 图片记录 + 配额累加**同事务**（D72）
	if err := s.uploads.MarkSuccessAndChargeQuota(item.UploadUID, fields, item.UserUID, fields["Size"].(int64)); err != nil {
		s.log.Error("写入上传结果失败", "upload_uid", item.UploadUID, "err", err)
	}

	// 运行时探测「服务端改名」（PicList 同款处理）：URL 文件名 ≠ 期望名
	// ⇒ 该图床无视用户的魔法文件名（如 NodeImage 强制短链 ID）。
	// 写进存储配置的 Capabilities JSON（D77 运行时探测原则），
	// UI 据此在「魔法文件名」旁提示，避免用户以为模板没生效。
	// 尽力而为：失败只记 debug，不影响上传结果。
	s.storage.MarkServerRenameDetected(item.Target.StorageUID, fields["FileName"].(string), res.URL)

	_ = s.jobs.UpdateItem(item.JobUID, item.Seq, map[string]any{
		"Status": model.JobStatusSucceeded, "Attempts": attempts,
		"Error": "", "FinishedAt": now,
	})

	s.hub.PublishUploadProgress(item.JobUID, item.UploadUID, item.Seq, item.OriginalName, 100, item.UserUID)
	s.hub.PublishUploadFinished(item.JobUID, item.UploadUID, item.Seq,
		fields["FileName"].(string), res.URL, res.ThumbURL, item.UserUID)

	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeUpload, Status: model.LogStatusSuccess,
		UserUID: item.UserUID, TargetType: "upload", TargetUID: item.UploadUID,
		Detail: map[string]any{
			"JobUID": item.JobUID, "Seq": item.Seq,
			"FileName": fields["FileName"], "URL": res.URL,
			"UploaderType": res.UploaderType, "Attempts": attempts,
			"Size": fields["Size"],
		},
	})

	if !item.KeepLocal {
		s.removeTemp(item.FilePath)
	}

	s.finalizeJobIfDone(ctx, item.JobUID)
}

// finishItemFailed 处理最终失败。
func (s *UploadService) finishItemFailed(ctx context.Context, item *queuedUpload, reason string, attempts int) {
	now := model.Now()
	if strings.TrimSpace(reason) == "" {
		reason = "上传失败"
	}

	if err := s.uploads.UpdateFields(item.UploadUID, map[string]any{
		"Status": model.UploadStatusFailed, "Error": reason,
	}); err != nil {
		s.log.Warn("标记图片为失败时出错", "upload_uid", item.UploadUID, "err", err)
	}
	_ = s.jobs.UpdateItem(item.JobUID, item.Seq, map[string]any{
		"Status": model.JobStatusFailed, "Attempts": attempts,
		"Error": reason, "FinishedAt": now,
	})

	s.hub.PublishUploadFailed(item.JobUID, item.UploadUID, item.Seq,
		item.OriginalName, reason, attempts, item.UserUID)

	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeUpload, Status: model.LogStatusFailed,
		UserUID: item.UserUID, TargetType: "upload", TargetUID: item.UploadUID,
		Detail: map[string]any{"JobUID": item.JobUID, "Seq": item.Seq, "FileName": item.OriginalName, "Attempts": attempts},
		Cause:  fmt.Errorf("%s", reason),
	})

	s.finalizeJobIfDone(ctx, item.JobUID)
}

// finalizeJobIfDone 在全部子项结束后结算 job。
//
// 语义（D37）：**只要有 item 失败，job 就是 failed**；
// 但成功项的 URL 照样写进 Result，不丢数据。
//
// 实现细节：**一次查出 items，在 Go 里计数**，而不是发一条 GROUP BY。
// 理由：
//   - 一次查询即可拿到计数与明细（结算本就需要明细来拼 Result）
//   - 避免依赖 GROUP BY + 列别名在不同方言/命名策略下的映射差异
//   - 批量任务的文件数天然有限（一次请求的量级），内存开销可忽略
func (s *UploadService) finalizeJobIfDone(ctx context.Context, jobUID string) {
	items, err := s.jobs.ListItems(jobUID)
	if err != nil {
		s.log.Error("查询任务子项失败，无法结算任务", "job", jobUID, "err", err)
		return
	}

	var succeeded, failed, running, queued int64
	for _, it := range items {
		switch it.Status {
		case model.JobStatusSucceeded:
			succeeded++
		case model.JobStatusFailed:
			failed++
		case model.JobStatusRunning:
			running++
		default:
			queued++
		}
	}

	total := int64(len(items))
	if running > 0 || queued > 0 {
		// 还没跑完：只更新进度
		s.updateJobProgress(jobUID, succeeded, failed, total)
		return
	}

	job, err := s.jobs.FindByUID(jobUID)
	if err != nil {
		s.log.Warn("读取任务失败，无法结算", "job", jobUID, "err", err)
		return
	}
	if job.Finished() {
		// 已经结算过（并发下两个 worker 同时到达时可能发生）
		return
	}

	resultItems := make([]map[string]any, 0, len(items))
	for _, it := range items {
		entry := map[string]any{
			"Seq": it.Seq, "FileName": it.FileName, "Status": it.Status,
		}
		if it.UploadUID != "" {
			if up, err := s.uploads.FindByUID(it.UploadUID); err == nil {
				entry["URL"] = up.URL
				// 展示名优先用别名（用户重命名过的）
				entry["FileName"] = up.DisplayName()
			}
		}
		if it.Error != "" {
			entry["Error"] = it.Error
		}
		resultItems = append(resultItems, entry)
	}

	status := model.JobStatusSucceeded
	errMsg := ""
	if failed > 0 {
		status = model.JobStatusFailed
		// 取第一条失败原因作为 job 摘要（细节在各 item 上）
		for _, it := range items {
			if it.Status == model.JobStatusFailed && it.Error != "" {
				errMsg = it.Error
				break
			}
		}
	}

	progress := progressOf(succeeded, failed, total)

	resultJSON, _ := json.Marshal(map[string]any{
		"Total": total, "Succeeded": succeeded, "Failed": failed,
		"Skipped": 0, // D66 取消去重后恒为 0（保留字段，D77）
		"Items":   resultItems,
	})

	now := model.Now()
	if err := s.jobs.UpdateFields(jobUID, map[string]any{
		"Status":         status,
		"Progress":       progress,
		"SucceededItems": succeeded,
		"FailedItems":    failed,
		"Result":         string(resultJSON),
		"Error":          errMsg,
		"FinishedAt":     now,
	}); err != nil {
		s.log.Error("结算任务失败", "job", jobUID, "err", err)
		return
	}

	s.hub.PublishJobFinished(events.JobFinishedPayload{
		JobUID:         jobUID,
		Kind:           model.JobKindUpload,
		Status:         status,
		Progress:       progress,
		TotalItems:     int(total),
		SucceededItems: int(succeeded),
		FailedItems:    int(failed),
		SkippedItems:   0,
	}, job.UserUID)

	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeUpload, Status: logStatusOf(status),
		UserUID: job.UserUID, TargetType: "job", TargetUID: jobUID,
		Detail: map[string]any{
			"action": "job_finished", "Total": total,
			"Succeeded": succeeded, "Failed": failed,
		},
	})
}

// updateJobProgress 只刷新进度与计数（job 仍在跑）。
func (s *UploadService) updateJobProgress(jobUID string, succeeded, failed, total int64) {
	job, err := s.jobs.FindByUID(jobUID)
	if err != nil {
		return
	}
	if err := s.jobs.UpdateFields(jobUID, map[string]any{
		"Progress":       progressOf(succeeded, failed, total),
		"SucceededItems": succeeded,
		"FailedItems":    failed,
		"StartedAt":      nonZero(job.StartedAt),
		"Status":         model.JobStatusRunning,
	}); err != nil {
		s.log.Debug("刷新任务进度失败", "job", jobUID, "err", err)
	}
}

// ---------------------------------------------------------------------------
// 启动恢复
// ---------------------------------------------------------------------------

// requeuePending 把所有 `queued` 的 item 重新入内存队列。
//
// 返回 (成功入队数, 因源文件缺失而直接标失败数)。
func (s *UploadService) requeuePending() (requeued, dropped int) {
	active, err := s.jobs.ListActive()
	if err != nil {
		s.log.Warn("查询未完成任务失败，跳过恢复", "err", err)
		return 0, 0
	}

	for i := range active {
		job := &active[i]
		if job.Kind != model.JobKindUpload {
			continue
		}

		target, err := s.storage.ResolveUploadTarget(job.StorageUID)
		if err != nil {
			// 存储配置被删了：把整个 job 的待处理项标失败，并如实写明原因
			s.log.Warn("恢复任务时无法解析存储目标", "job", job.UID, "err", err)
			s.failPendingItemsOfJob(job.UID, "存储配置不可用："+MessageOf(err))
			continue
		}

		payload := map[string]any{}
		_ = json.Unmarshal([]byte(job.Payload), &payload)
		keepLocal, _ := payload["KeepLocal"].(bool)

		items, err := s.jobs.ListQueuedItems(job.UID)
		if err != nil {
			s.log.Warn("查询待处理上传项失败", "job", job.UID, "err", err)
			continue
		}

		for _, it := range items {
			up, err := s.uploads.FindByUID(it.UploadUID)
			if err != nil {
				_ = s.jobs.UpdateItem(job.UID, it.Seq, map[string]any{
					"Status": model.JobStatusFailed, "Error": "图片记录已丢失", "FinishedAt": model.Now(),
				})
				dropped++
				continue
			}

			// 源文件路径存在 UploadResults.FilePath
			var filePath, originalName string
			if res, err := s.uploads.GetResult(it.UploadUID); err == nil {
				filePath = res.FilePath
			}
			originalName = up.OriginalName
			if filePath == "" {
				filePath = filepath.Join(s.uploadsRoot, filepath.Base(up.FileName))
			}

			if _, statErr := os.Stat(filePath); statErr != nil {
				s.finishItemFailed(context.Background(), &queuedUpload{
					JobUID: job.UID, Seq: it.Seq, UploadUID: it.UploadUID,
					UserUID: job.UserUID, FilePath: filePath,
					OriginalName: originalName, Size: up.Size,
				}, fmt.Sprintf("源文件已清理（%s）", filePath), 1)
				dropped++
				continue
			}

			q := &queuedUpload{
				JobUID: job.UID, Seq: it.Seq, UploadUID: it.UploadUID,
				UserUID: job.UserUID, FilePath: filePath,
				OriginalName: originalName, Size: up.Size, KeepLocal: keepLocal,
				Target: *target,
			}
			select {
			case s.queue <- q:
				requeued++
			default:
				s.log.Warn("恢复时队列已满，剩余项留待下次启动", "job", job.UID)
				return requeued, dropped
			}
		}
	}
	return requeued, dropped
}

// failPendingItemsOfJob 把一个 job 的所有待处理项直接标失败。
func (s *UploadService) failPendingItemsOfJob(jobUID, reason string) {
	items, err := s.jobs.ListQueuedItems(jobUID)
	if err != nil {
		return
	}
	for _, it := range items {
		s.finishItemFailed(context.Background(), &queuedUpload{
			JobUID: jobUID, Seq: it.Seq, UploadUID: it.UploadUID,
			OriginalName: it.FileName,
		}, reason, 1)
	}
}

// ---------------------------------------------------------------------------
// 组合上传（FromURL 的落盘部分）
// ---------------------------------------------------------------------------

// SaveIncomingFile 把一段字节写入本地暂存区，返回可入队的 IncomingFile。
//
// 路径规则：`<dataDir>/uploads/<yyyy>/<mm>/<随机目录>/<原始文件名>`
//
// 两个关键设计：
//
//  1. **保留原始文件名**（而不是用 UID 命名文件）：因为 agent 的魔法路径/文件名
//     模板变量 `{filename}` 取自源文件的 base name。若把暂存文件叫 `01HXX.png`，
//     用户的模板 `{filename}-{md5-8}` 就会产出 `01HXX-xxxx.png` ——
//     完全不是用户期望的结果。
//  2. **用随机目录保证唯一**：不同文件同名时靠父目录区分，既保留了好名字，
//     又不会互相覆盖。
//
// ⚠️ 这与「魔法路径」（对外的远端路径，由 StorageConfigs.PathTemplate 决定）无关：
// 本地暂存路径纯属内部实现（D43）。
func (s *UploadService) SaveIncomingFile(originalName string, data []byte) (IncomingFile, error) {
	path, err := s.TempPathFor(originalName)
	if err != nil {
		return IncomingFile{}, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return IncomingFile{}, Wrap(response.CodeInternal, "写入暂存文件失败", err)
	}
	return IncomingFile{
		Path:         path,
		OriginalName: filepath.Base(path),
		Size:         int64(len(data)),
	}, nil
}

// TempPathFor 为即将写入的暂存文件生成路径（供 handler 流式落盘，避免整文件进内存）。
//
// 形如 `<dataDir>/uploads/2026/02/01HXX.../photo.png`；调用方负责创建父目录
// （本函数已创建）。
func (s *UploadService) TempPathFor(originalName string) (string, error) {
	safe := sanitizeLocalFileName(originalName)
	if safe == "" {
		safe = "upload.bin"
	}

	now := time.Now()
	// 随机目录让同名文件互不覆盖，同时保留可读的原始文件名
	dir := filepath.Join(s.uploadsRoot, now.Format("2006"), now.Format("01"), id.New())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", Wrap(response.CodeInternal, "创建暂存目录失败", err)
	}
	return filepath.Join(dir, safe), nil
}

// sanitizeLocalFileName 清洗落到本地磁盘的文件名。
//
// 只允许「安全的文件名字符」：字母数字、点、连字符、下划线，以及**中日韩等
// 非 ASCII 字符**（用户上传中文文件名很常见，不该被抹掉）。
// 路径分隔符、控制字符、Windows 保留字符一律替换为下划线。
func sanitizeLocalFileName(name string) string {
	// 先取 base name，防路径穿越（"../../etc/passwd" → "passwd"）
	normalized := strings.ReplaceAll(name, "\\", "/")
	base := path.Base(normalized)
	if base == "." || base == "/" || base == ".." || base == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range base {
		switch {
		case r < 0x20 || r == 0x7f:
			// 控制字符丢弃
			continue
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' ||
			r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}

	out := strings.TrimSpace(b.String())
	if out == "" || strings.Trim(out, ".") == "" {
		return ""
	}
	// 长度限制：保留扩展名
	if len(out) > 180 {
		ext := path.Ext(out)
		if len(ext) > 20 {
			ext = ""
		}
		stemLen := 180 - len(ext)
		if stemLen < 1 {
			stemLen = 1
		}
		out = out[:stemLen] + ext
	}
	return out
}

// removeTemp 删除暂存文件（失败只记 debug：残留文件有定时清理兜底）。
func (s *UploadService) removeTemp(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		s.log.Debug("删除暂存文件失败", "path", path, "err", err)
	}
}

// CleanupTempPaths 供 handler 在校验失败后清理（避免泄漏）。
func (s *UploadService) CleanupTempPaths(paths []string) {
	for _, p := range paths {
		s.removeTemp(p)
	}
}

// ---------------------------------------------------------------------------
// 安全：SSRF 防护（FromURL）
// ---------------------------------------------------------------------------

// ValidateFetchURL 校验「从 URL 上传」的目标地址，防 SSRF。
//
// 规则：解析域名到**真实 IP**，拒绝回环 / 私有 / 链路本地 / 组播等地址。
// 可用 `PICGO_WEB_ALLOW_PRIVATE_FETCH=true` 关闭该限制（内网图床场景）。
func (s *UploadService) ValidateFetchURL(rawURL string) error {
	if s.cfg.AllowPrivateFetch {
		return nil
	}

	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return Errorf(response.CodeInvalidParam, "URL 格式不正确")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Errorf(response.CodeInvalidParam, "只支持 http / https 协议")
	}
	host := u.Hostname()
	if host == "" {
		return Errorf(response.CodeInvalidParam, "URL 缺少主机名")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return Errorf(response.CodeInvalidParam, "无法解析主机名「%s」", host)
	}
	if len(addrs) == 0 {
		return Errorf(response.CodeInvalidParam, "主机名「%s」没有解析结果", host)
	}

	for _, addr := range addrs {
		if isDisallowedIP(addr.IP) {
			return Errorf(response.CodeInvalidParam,
				"出于安全考虑，禁止访问内网地址（%s）；如确需内网图床请设置 PICGO_WEB_ALLOW_PRIVATE_FETCH=true",
				addr.IP.String())
		}
	}
	return nil
}

// isDisallowedIP 判断 IP 是否属于应拒绝的网段。
func isDisallowedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

func normalizeExt(ext string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func progressOf(succeeded, failed, total int64) int {
	if total <= 0 {
		return 0
	}
	done := succeeded + failed
	if done >= total {
		return 100
	}
	return int(done * 100 / total)
}

func logStatusOf(jobStatus string) string {
	if jobStatus == model.JobStatusSucceeded {
		return model.LogStatusSuccess
	}
	return model.LogStatusFailed
}

func nonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func nonZero(v int64) int64 {
	if v == 0 {
		return model.Now()
	}
	return v
}

func boolPtr(b bool) *bool { return &b }

// ---------------------------------------------------------------------------
// 从 URL 上传（服务端拉取）
// ---------------------------------------------------------------------------

// maxFetchRedirects 允许的最大重定向次数。
const maxFetchRedirects = 5

// DownloadToTemp 下载一个远程图片到本地暂存区。
//
// 安全要点（docs/API.md §4.1）：
//
//  1. 下载前校验目标 URL（SSRF 防护）
//  2. **重定向的每一跳都重新校验**：否则攻击者可以用一个公网地址
//     302 到 169.254.169.254 绕过首次校验
//  3. 限制响应体大小（超过 `upload.maxSizeBytes` 即中止）
//  4. 只接受图片类 Content-Type（避免把任意二进制当图片上传）
func (s *UploadService) DownloadToTemp(ctx context.Context, rawURL string) (*IncomingFile, error) {
	if err := s.ValidateFetchURL(rawURL); err != nil {
		return nil, err
	}

	maxSize := s.settings.GetInt("upload.maxSizeBytes", 20<<20)

	client := &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxFetchRedirects {
				return fmt.Errorf("重定向次数超过 %d", maxFetchRedirects)
			}
			// 每一跳都重校验：防「公网地址 302 到内网」
			if err := s.ValidateFetchURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, Errorf(response.CodeInvalidParam, "URL 不合法")
	}
	req.Header.Set("User-Agent", "PicGo-Web/"+versionString())

	resp, err := client.Do(req)
	if err != nil {
		return nil, Errorf(response.CodeInvalidParam, "下载失败：%v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, Errorf(response.CodeInvalidParam, "下载失败：远端返回 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxSize {
		return nil, Errorf(response.CodeInvalidParam,
			"远端文件过大（%.1f MiB，上限 %.1f MiB）",
			float64(resp.ContentLength)/(1<<20), float64(maxSize)/(1<<20))
	}

	// 文件名从 URL 路径推断；推断不出时用内容类型决定扩展名
	name := fileNameFromURL(rawURL)
	if name == "" {
		ext := extFromContentType(resp.Header.Get("Content-Type"))
		name = "download" + ext
	}

	// 多读 1 字节用于判断「是否超限」
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, Errorf(response.CodeInvalidParam, "读取远端内容失败：%v", err)
	}
	if int64(len(data)) > maxSize {
		return nil, Errorf(response.CodeInvalidParam,
			"远端文件超过大小上限（%.1f MiB）", float64(maxSize)/(1<<20))
	}
	if len(data) == 0 {
		return nil, Errorf(response.CodeInvalidParam, "远端文件为空")
	}

	f, err := s.SaveIncomingFile(name, data)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// fileNameFromURL 从 URL 路径里推断文件名（只取 base，防路径穿越）。
func fileNameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	base := path.Base(u.Path)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	// URL 编码的文件名要解码（否则会带着 %20 之类）
	if decoded, err := url.PathUnescape(base); err == nil {
		base = decoded
	}
	if path.Ext(base) == "" {
		return "" // 没有扩展名，交给内容类型推断
	}
	return filepath.Base(base)
}

// extFromContentType 从 Content-Type 推断扩展名（含点）。
func extFromContentType(ct string) string {
	ct = strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	switch ct {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	case "image/bmp":
		return ".bmp"
	case "image/x-icon", "image/vnd.microsoft.icon":
		return ".ico"
	case "image/avif":
		return ".avif"
	default:
		return ".bin"
	}
}

// versionString 返回用于 User-Agent 的版本串。
//
// 单独抽出来是为了避免 service 包 import server 包（会形成环：
// server → handler → service）。这里是「够用即可」的常量。
func versionString() string { return appVersion }
