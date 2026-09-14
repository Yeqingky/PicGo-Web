package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/YeqingKy/PicGo-Web/server/internal/id"
	"github.com/YeqingKy/PicGo-Web/server/internal/logger"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
)

// auditTimeout 是写审计日志的超时。
const auditTimeout = 5 * time.Second

// auditDetailMaxBytes 限制 Detail 的 JSON 长度，避免单行过大。
const auditDetailMaxBytes = 8 * 1024

// AuditEntry 是一次操作日志的输入（D45）。
//
// 用结构体而不是 7 个位置参数：字段多且可选，结构体更不易写错，
// 也便于后续增字段而不破坏调用方。
type AuditEntry struct {
	// Type 见 model.LogType*（如 "upload" / "auth.login" / "user.create"）。
	Type string
	// Status model.LogStatusSuccess | model.LogStatusFailed。
	Status string

	// UserUID / Username 操作者；系统操作留空。
	// 若留空且 ctx 中带有 UserUID（由鉴权中间件注入），会自动补上。
	UserUID  string
	Username string

	// TargetType / TargetUID 操作对象（upload / user / storage / plugin / theme ...）。
	TargetType string
	TargetUID  string

	// Detail 成功时的上下文，任意可 JSON 序列化的值。
	Detail any
	// Cause 失败时的原因（失败日志会把它的文本写进 Error 列）。
	Cause error

	ClientIP  string
	UserAgent string
}

// AuditService 写统一操作日志（D45）。
//
// 设计要点：**写日志失败绝不能影响主流程**。因此：
//   - 不在请求的 context 上写（请求取消/超时会导致写不进去）
//   - 内部自带超时
//   - 任何错误只记进程日志
type AuditService struct {
	repo *repository.LogRepo
	log  *slog.Logger
}

// NewAuditService 构造。
func NewAuditService(repo *repository.LogRepo, log *slog.Logger) *AuditService {
	return &AuditService{repo: repo, log: log}
}

// Log 写入一条操作日志。
//
// 参数用结构体便于扩展；另见 Record 提供的位置参数形式。
func (s *AuditService) Log(ctx context.Context, e AuditEntry) {
	if s == nil || s.repo == nil {
		return
	}

	// 补全操作者：优先调用方显式传入，否则取 ctx 中的（中间件注入）
	if e.UserUID == "" && ctx != nil {
		e.UserUID = logger.UserUID(ctx)
	}
	if e.Status == "" {
		e.Status = model.LogStatusSuccess
	}

	row := &model.OperationLog{
		UID:        id.Log(),
		Type:       e.Type,
		Status:     e.Status,
		UserUID:    e.UserUID,
		Username:   e.Username,
		TargetType: e.TargetType,
		TargetUID:  e.TargetUID,
		Detail:     marshalDetail(e.Detail),
		ClientIP:   e.ClientIP,
		UserAgent:  truncate(e.UserAgent, 255),
		CreatedAt:  model.Now(),
	}
	if e.Cause != nil {
		row.Error = truncate(e.Cause.Error(), 2000)
	}

	// 独立 context + 超时：请求被取消也照常落库
	writeCtx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()

	if err := s.repo.Insert(row); err != nil {
		// 审计失败不影响主流程，但要留下痕迹
		s.log.Warn("写入操作日志失败",
			"type", e.Type, "status", e.Status,
			"target_type", e.TargetType, "target_uid", e.TargetUID,
			"err", err, "ctx_err", writeCtx.Err(),
		)
	}
}

// Record 是 Log 的位置参数形式，便于简单场景调用。
func (s *AuditService) Record(ctx context.Context, logType, status, targetType, targetUID string, detail any, cause error) {
	s.Log(ctx, AuditEntry{
		Type:       logType,
		Status:     status,
		TargetType: targetType,
		TargetUID:  targetUID,
		Detail:     detail,
		Cause:      cause,
	})
}

// AuditOK 是「成功」日志的快捷方式。
func (s *AuditService) AuditOK(ctx context.Context, logType, targetType, targetUID string, detail any) {
	s.Log(ctx, AuditEntry{Type: logType, Status: model.LogStatusSuccess, TargetType: targetType, TargetUID: targetUID, Detail: detail})
}

// AuditFail 是「失败」日志的快捷方式。
func (s *AuditService) AuditFail(ctx context.Context, logType, targetType, targetUID string, detail any, cause error) {
	s.Log(ctx, AuditEntry{Type: logType, Status: model.LogStatusFailed, TargetType: targetType, TargetUID: targetUID, Detail: detail, Cause: cause})
}

// marshalDetail 把 Detail 序列化成 JSON 字符串；nil 返回空串。
func marshalDetail(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return truncate(s, auditDetailMaxBytes)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return truncate(string(b), auditDetailMaxBytes)
}

// truncate 按字节上限截断，并保证结果仍是合法 UTF-8（不会切出半个汉字）。
func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := s[:max]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
