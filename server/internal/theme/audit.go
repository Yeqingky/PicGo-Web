package theme

import "context"

// AuditEntry 是一次主题相关操作的审计记录。
//
// 这里**不复用 internal/service.AuditEntry**，是为了让 theme 包不依赖 service 包：
// 主题系统只关心「写一条操作日志」，具体由 server 层把 *service.AuditService 适配进来。
// 字段与 model.OperationLog 对齐。
type AuditEntry struct {
	// Type 见 model.LogTypeTheme*（theme.install / theme.uninstall / theme.activate /
	// theme.rescan / theme.settings.update / theme.settings.clear / theme.error）。
	Type   string
	Status string // model.LogStatusSuccess | model.LogStatusFailed

	// TargetType 固定为 "theme"；TargetUID 为 ThemeID。
	TargetType string
	TargetUID  string

	// Detail 成功时的上下文（会 JSON 序列化）。
	Detail any
	// Cause 失败原因（写入 OperationLogs.Error）。
	Cause error
}

// Auditor 写操作日志。实现必须**不阻塞、不报错**（写日志失败不能影响主流程）。
type Auditor interface {
	Log(ctx context.Context, e AuditEntry)
}

// nopAuditor 在未注入 Auditor 时兜底，避免到处判空。
type nopAuditor struct{}

func (nopAuditor) Log(context.Context, AuditEntry) {}

// ensureNoop 返回可用的 Auditor。
func ensureNoop(a Auditor) Auditor {
	if a == nil {
		return nopAuditor{}
	}
	return a
}
