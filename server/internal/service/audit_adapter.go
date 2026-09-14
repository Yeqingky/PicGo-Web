package service

import (
	"context"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// 本文件提供 service 层对外部组件（scheduler）的最小适配。
//
// 为什么用「小接口 + 适配器」而不是让 scheduler 直接依赖 AuditService：
// scheduler 只需要「写一条清理日志」这一个能力；直接依赖整个 AuditService
// 会把 service 包的依赖图拖进 scheduler（也让 scheduler 的单测必须构造 repo）。

// CleanupAuditWriter 让 AuditService 满足 scheduler.AuditWriter。
//
// 用法：`scheduler.New(scheduler.Config{..., Audit: service.NewCleanupAuditWriter(auditSvc)})`
type CleanupAuditWriter struct {
	audit *AuditService
}

// NewCleanupAuditWriter 构造适配器。
func NewCleanupAuditWriter(audit *AuditService) *CleanupAuditWriter {
	return &CleanupAuditWriter{audit: audit}
}

// WriteCleanupLog 写一条 `system.log.cleanup` 日志。
//
// 语义：**清理动作本身也要留痕**（docs/OPERATIONS.md §5）。
// 调用方只在「确实删了东西」或「出错了」时才调用，避免每天产生噪音记录。
func (w *CleanupAuditWriter) WriteCleanupLog(ctx context.Context, detail map[string]any, cause error) {
	if w == nil || w.audit == nil {
		return
	}
	status := model.LogStatusSuccess
	if cause != nil {
		status = model.LogStatusFailed
	}
	w.audit.Log(ctx, AuditEntry{
		Type:       model.LogTypeLogCleanup,
		Status:     status,
		TargetType: "system",
		Detail:     detail,
		Cause:      cause,
	})
}
