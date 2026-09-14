// Package scheduler 提供进程内的定时清理任务。
//
// 当前只有一个任务：**每日日志清理**（docs/OPERATIONS.md §5）。
//
//   - `OperationLogs` 超过 `log.retentionDays`（默认 180 天；**0 = 永久保留，跳过**）
//   - `JobLogs` 超过 `log.jobRetentionDays`（默认 7 天）
//   - **清理动作自身也写一条 `OperationLogs`（system.log.cleanup）**，
//     并在 `Detail` 里记录删除条数（否则「日志变少了」无从追溯）
//
// 实现用 `time.Ticker` + goroutine，**不引入 cron 库**：
// 需求就是「每天跑一次」，cron 表达式带来的依赖与表达能力都过剩。
package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// Logger 是本包对日志的最小依赖。
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// AuditWriter 是「写审计日志」的最小依赖（避免依赖整个 service 包）。
type AuditWriter interface {
	WriteCleanupLog(ctx context.Context, detail map[string]any, cause error)
}

// Scheduler 是定时任务管理器。
type Scheduler struct {
	log      Logger
	settings *settings.Service
	logs     *repository.LogRepo
	jobs     *repository.JobRepo
	audit    AuditWriter

	stop chan struct{}
	done chan struct{}
}

// Config 是构造参数。
type Config struct {
	Log      Logger
	Settings *settings.Service
	Logs     *repository.LogRepo
	Jobs     *repository.JobRepo
	Audit    AuditWriter
}

// New 构造。
func New(cfg Config) *Scheduler {
	return &Scheduler{
		log:      cfg.Log,
		settings: cfg.Settings,
		logs:     cfg.Logs,
		jobs:     cfg.Jobs,
		audit:    cfg.Audit,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// 清理间隔与启动补跑延迟。
const (
	// cleanInterval 每 24 小时跑一次。
	cleanInterval = 24 * time.Hour
	// startupDelay 启动后多久补跑一次。
	//
	// 之所以补跑：服务可能不是一直在跑（自用场景常见的是「用时才启动」），
	// 只在启动时跑一次能保证「即使每天只开 1 小时，日志也会被清理」。
	// 延迟 1 分钟是为了不与启动流程抢资源。
	startupDelay = time.Minute
)

// Start 启动定时任务（非阻塞）。
func (s *Scheduler) Start(ctx context.Context) {
	go s.loop(ctx)
	s.log.Info("定时任务已启动",
		"interval", cleanInterval.String(),
		"startup_delay", startupDelay.String())
}

// Stop 停止定时任务并等待当前轮结束。
func (s *Scheduler) Stop() {
	select {
	case <-s.done:
		return // 已经停过
	default:
	}
	close(s.stop)
	<-s.done
	s.log.Info("定时任务已停止")
}

func (s *Scheduler) loop(ctx context.Context) {
	defer close(s.done)

	// 启动补跑
	select {
	case <-ctx.Done():
		return
	case <-s.stop:
		return
	case <-time.After(startupDelay):
	}

	ticker := time.NewTicker(cleanInterval)
	defer ticker.Stop()

	s.RunOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-ticker.C:
			s.RunOnce(ctx)
		}
	}
}

// CleanupResult 是一次清理的结果（供测试断言与运维观察）。
type CleanupResult struct {
	// OperationLogsDeleted 被删除的操作日志条数。
	OperationLogsDeleted int64
	// JobLogsDeleted 被删除的任务日志条数。
	JobLogsDeleted int64
	// SkippedOperationLogs 为真表示 operationRetentionDays == 0（永久保留，跳过）。
	SkippedOperationLogs bool
	// SkippedJobLogs 为真表示 jobRetentionDays == 0（永久保留，跳过）。
	SkippedJobLogs bool
	// Err 清理过程中的错误（非致命：两个清理各自独立）。
	Err error
}

// RunOnce 执行一次清理（导出以便测试与「手动触发」）。
//
// 两个清理**互相独立**：一个失败不影响另一个（否则「任务日志表坏了」会导致
// 「操作日志永远不清理」）。
func (s *Scheduler) RunOnce(ctx context.Context) CleanupResult {
	var res CleanupResult
	now := model.Now()

	// ---- 1. 操作日志（log.retentionDays）----
	opDays := s.settings.GetInt("log.retentionDays", 180)
	if opDays <= 0 {
		// 0 = 永久保留：**显式跳过**（见 docs/OPERATIONS.md §5）
		res.SkippedOperationLogs = true
	} else {
		before := now - opDays*86400
		n, err := s.logs.PurgeBefore(before)
		if err != nil {
			s.log.Warn("清理操作日志失败", "err", err)
			res.Err = err
		}
		res.OperationLogsDeleted = n
	}

	// ---- 2. 任务日志（log.jobRetentionDays）----
	jobDays := s.settings.GetInt("log.jobRetentionDays", 7)
	if jobDays <= 0 {
		res.SkippedJobLogs = true
	} else {
		before := now - jobDays*86400
		n, err := s.jobs.PurgeLogsBefore(before)
		if err != nil {
			s.log.Warn("清理任务日志失败", "err", err)
			if res.Err == nil {
				res.Err = err
			}
		}
		res.JobLogsDeleted = n
	}

	// 没有删除任何东西时**不写日志**：否则每天都会产生一条无意义的记录
	// （180 天保留期内，绝大多数日子的删除数都是 0）。
	if res.OperationLogsDeleted > 0 || res.JobLogsDeleted > 0 || res.Err != nil {
		if s.audit != nil {
			s.audit.WriteCleanupLog(ctx, map[string]any{
				"OperationLogsDeleted":   res.OperationLogsDeleted,
				"JobLogsDeleted":         res.JobLogsDeleted,
				"OperationRetentionDays": opDays,
				"JobRetentionDays":       jobDays,
				"SkippedOperationLogs":   res.SkippedOperationLogs,
				"SkippedJobLogs":         res.SkippedJobLogs,
			}, res.Err)
		}
	}

	if res.OperationLogsDeleted > 0 || res.JobLogsDeleted > 0 {
		s.log.Info("日志清理完成",
			"operation_logs_deleted", res.OperationLogsDeleted,
			"job_logs_deleted", res.JobLogsDeleted)
	} else {
		s.log.Debug("日志清理完成（无需删除）",
			"skipped_operation_logs", res.SkippedOperationLogs,
			"skipped_job_logs", res.SkippedJobLogs)
	}
	return res
}

// Description 返回当前清理策略的可读描述（供系统信息展示）。
func (s *Scheduler) Description() string {
	opDays := s.settings.GetInt("log.retentionDays", 180)
	jobDays := s.settings.GetInt("log.jobRetentionDays", 7)
	return fmt.Sprintf("操作日志保留 %s，任务日志保留 %s",
		retentionText(opDays), retentionText(jobDays))
}

func retentionText(days int64) string {
	if days <= 0 {
		return "永久"
	}
	return fmt.Sprintf("%d 天", days)
}
