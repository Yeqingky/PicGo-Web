package service

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// LogService 负责操作日志与邮件日志的**查询**。
//
// 写入侧是 `AuditService`（W3）；本服务只读，两者职责分明：
//
//	AuditService  写（且「写失败绝不影响主流程」）
//	LogService    读（列表 / 详情 / 类型清单 / 邮件日志）
//
// ⚠️ 权限（docs/API.md §9）：**全部端点需要 admin**。
// 因此本服务不做「按用户过滤」的业务逻辑，`UserUID` 只是可选的筛选条件。
type LogService struct {
	log    *slog.Logger
	logs   *repository.LogRepo
	emails *repository.EmailLogRepo
}

// NewLogService 构造。
func NewLogService(log *slog.Logger, logs *repository.LogRepo, emails *repository.EmailLogRepo) *LogService {
	return &LogService{log: log, logs: logs, emails: emails}
}

// OperationLogView 是对外的操作日志对象。
type OperationLogView struct {
	UID        string `json:"UID"`
	Type       string `json:"Type"`
	Status     string `json:"Status"`
	UserUID    string `json:"UserUID"`
	Username   string `json:"Username"`
	TargetType string `json:"TargetType"`
	TargetUID  string `json:"TargetUID"`

	Detail map[string]any `json:"Detail"`
	Error  string         `json:"Error"`

	ClientIP  string `json:"ClientIP"`
	UserAgent string `json:"UserAgent"`
	CreatedAt int64  `json:"CreatedAt"`
}

// LogListInput 是操作日志列表入参。
type LogListInput struct {
	// Types 精确匹配（多值 = OR）。
	Types      []string
	Status     string
	Keyword    string
	UserUID    string
	TargetType string
	TargetUID  string
	From       int64
	To         int64

	Sort  string
	Order string

	Page     int
	PageSize int
}

// List 分页查询操作日志。
func (s *LogService) List(in LogListInput) ([]OperationLogView, int64, error) {
	page, size := normalizePage(in.Page, in.PageSize)

	rows, total, err := s.logs.List(repository.LogListFilter{
		Types:      in.Types,
		Status:     in.Status,
		Keyword:    in.Keyword,
		UserUID:    in.UserUID,
		TargetType: in.TargetType,
		TargetUID:  in.TargetUID,
		From:       in.From,
		To:         in.To,
		Sort:       in.Sort,
		Order:      in.Order,
		Page:       page,
		PageSize:   size,
	})
	if err != nil {
		return nil, 0, Wrap(response.CodeInternal, "查询操作日志失败", err)
	}

	out := make([]OperationLogView, 0, len(rows))
	for i := range rows {
		out = append(out, toLogView(&rows[i]))
	}
	return out, total, nil
}

// Get 取单条日志详情。
func (s *LogService) Get(uid string) (*OperationLogView, error) {
	row, err := s.logs.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "日志不存在")
	}
	v := toLogView(row)
	return &v, nil
}

// LogTypeView 是「可过滤的类型清单」的一项。
//
// `Label` 取 `model.LogTypes()` 的中文描述；`TargetType` 是这类操作通常作用的对象类型，
// 便于前端在筛选后自动联动第二个筛选器。
type LogTypeView struct {
	Type       string `json:"Type"`
	Label      string `json:"Label"`
	TargetType string `json:"TargetType"`
}

// Types 返回全部可过滤的日志类型。
//
// 清单是**静态声明**（便于前端渲染筛选项）；写入时**不校验白名单**，
// 以便新功能直接写新类型而无需改接口（D77）。
func (s *LogService) Types() []LogTypeView {
	items := model.LogTypes()
	out := make([]LogTypeView, 0, len(items))
	for _, it := range items {
		out = append(out, LogTypeView{
			Type:       it.Type,
			Label:      it.Description,
			TargetType: defaultTargetTypeOf(it.Type),
		})
	}
	return out
}

// EmailLogView 是对外的邮件日志对象（**不含正文**，本来就没存）。
type EmailLogView struct {
	UID            string `json:"UID"`
	ToAddress      string `json:"ToAddress"`
	Subject        string `json:"Subject"`
	Template       string `json:"Template"`
	Status         string `json:"Status"`
	Error          string `json:"Error"`
	RelatedUserUID string `json:"RelatedUserUID"`
	CreatedAt      int64  `json:"CreatedAt"`
}

// EmailListInput 是邮件日志列表入参。
type EmailListInput struct {
	ToAddress string
	Template  string
	Status    string
	From      int64
	To        int64

	Page     int
	PageSize int
}

// ListEmails 分页查询邮件日志。
func (s *LogService) ListEmails(in EmailListInput) ([]EmailLogView, int64, error) {
	page, size := normalizePage(in.Page, in.PageSize)

	rows, total, err := s.emails.List(repository.EmailListFilter{
		ToAddress: in.ToAddress,
		Template:  in.Template,
		Status:    in.Status,
		From:      in.From,
		To:        in.To,
		Page:      page,
		PageSize:  size,
	})
	if err != nil {
		return nil, 0, Wrap(response.CodeInternal, "查询邮件日志失败", err)
	}

	out := make([]EmailLogView, 0, len(rows))
	for _, r := range rows {
		out = append(out, EmailLogView{
			UID: r.UID, ToAddress: r.ToAddress, Subject: r.Subject,
			Template: r.Template, Status: r.Status, Error: r.Error,
			RelatedUserUID: r.RelatedUserUID, CreatedAt: r.CreatedAt,
		})
	}
	return out, total, nil
}

// GetEmail 取单封邮件记录详情。
func (s *LogService) GetEmail(uid string) (*EmailLogView, error) {
	row, err := s.emails.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "邮件记录不存在")
	}
	return &EmailLogView{
		UID: row.UID, ToAddress: row.ToAddress, Subject: row.Subject,
		Template: row.Template, Status: row.Status, Error: row.Error,
		RelatedUserUID: row.RelatedUserUID, CreatedAt: row.CreatedAt,
	}, nil
}

// PutEmails 供 email service 写入（保持「写」集中在 AuditService/repo 的约定）。
func (s *LogService) PutEmails(l *model.EmailLog) error {
	return s.emails.Insert(l)
}

// ---------------------------------------------------------------------------
// 内部
// ---------------------------------------------------------------------------

func toLogView(l *model.OperationLog) OperationLogView {
	v := OperationLogView{
		UID: l.UID, Type: l.Type, Status: l.Status,
		UserUID: l.UserUID, Username: l.Username,
		TargetType: l.TargetType, TargetUID: l.TargetUID,
		Error:    l.Error,
		ClientIP: l.ClientIP, UserAgent: l.UserAgent,
		CreatedAt: l.CreatedAt,
		Detail:    map[string]any{},
	}
	if strings.TrimSpace(l.Detail) != "" {
		var d map[string]any
		if err := json.Unmarshal([]byte(l.Detail), &d); err == nil {
			v.Detail = d
		}
	}
	return v
}

// defaultTargetTypeOf 给出某日志类型「通常作用的对象类型」。
//
// 纯展示辅助（前端筛选联动），**不参与任何校验** ——
// 因此这里的映射不完整也不影响正确性，未知类型返回空串即可。
func defaultTargetTypeOf(logType string) string {
	switch {
	case logType == model.LogTypeUpload, logType == model.LogTypeImageDelete, logType == model.LogTypeImageUpdate:
		return "upload"
	case logType == model.LogTypeMailSend:
		return "email"
	case strings.HasPrefix(logType, "user."), strings.HasPrefix(logType, "auth."):
		return "user"
	case strings.HasPrefix(logType, "storage."):
		return "storage"
	case strings.HasPrefix(logType, "plugin."):
		return "plugin"
	case strings.HasPrefix(logType, "theme."):
		return "theme"
	case logType == model.LogTypeSettingUpdate:
		return "setting"
	default:
		return ""
	}
}
