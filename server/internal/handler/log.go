package handler

import (
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
)

// LogHandler 处理操作日志端点（/logs/**）。
//
// ⚠️ **全部端点需要 admin**（docs/API.md §9）：日志里有全站的操作轨迹，
// 普通用户看到他人行为属于越权。由 RegisterBusiness 统一挂 RequireAdmin。
type LogHandler struct {
	logs *service.LogService
	log  *slog.Logger
}

// NewLogHandler 构造。
func NewLogHandler(logs *service.LogService, log *slog.Logger) *LogHandler {
	return &LogHandler{logs: logs, log: log}
}

// Register 注册路由。
//
// ⚠️ 静态路径（`/types`、`/emails`）必须注册在 `/:UID` 之前，
// 否则 gin 会把 "types" 当成 UID。
func (h *LogHandler) Register(g *gin.RouterGroup) {
	g.GET("", h.List)
	g.GET("/types", h.Types)
	g.GET("/emails", h.ListEmails)
	g.GET("/emails/:UID", h.GetEmail)
	g.GET("/:UID", h.Get)
}

// List 处理 GET /logs。
func (h *LogHandler) List(c *gin.Context) {
	items, total, err := h.logs.List(service.LogListInput{
		Types:      queryStrings(c, "Type"),
		Status:     strings.TrimSpace(c.Query("Status")),
		Keyword:    strings.TrimSpace(c.Query("Keyword")),
		UserUID:    strings.TrimSpace(c.Query("UserUID")),
		TargetType: strings.TrimSpace(c.Query("TargetType")),
		TargetUID:  strings.TrimSpace(c.Query("TargetUID")),
		From:       queryInt64(c, "From", 0),
		To:         queryInt64(c, "To", 0),
		Sort:       strings.TrimSpace(c.Query("Sort")),
		Order:      strings.TrimSpace(c.Query("Order")),
		Page:       queryInt(c, "Page", service.DefaultPage),
		PageSize:   queryInt(c, "PageSize", service.DefaultPageSize),
	})
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.Page(c, items, total,
		clampPage(queryInt(c, "Page", service.DefaultPage)),
		clampPageSize(queryInt(c, "PageSize", service.DefaultPageSize)))
}

// Get 处理 GET /logs/:UID。
func (h *LogHandler) Get(c *gin.Context) {
	view, err := h.logs.Get(c.Param("UID"))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, view)
}

// Types 处理 GET /logs/types（可过滤的类型清单）。
func (h *LogHandler) Types(c *gin.Context) {
	response.OK(c, gin.H{"Types": h.logs.Types()})
}

// ListEmails 处理 GET /logs/emails。
//
// ⚠️ **不返回邮件正文**（本来也没存，D29）。
func (h *LogHandler) ListEmails(c *gin.Context) {
	items, total, err := h.logs.ListEmails(service.EmailListInput{
		ToAddress: strings.TrimSpace(c.Query("ToAddress")),
		Template:  strings.TrimSpace(c.Query("Template")),
		Status:    strings.TrimSpace(c.Query("Status")),
		From:      queryInt64(c, "From", 0),
		To:        queryInt64(c, "To", 0),
		Page:      queryInt(c, "Page", service.DefaultPage),
		PageSize:  queryInt(c, "PageSize", service.DefaultPageSize),
	})
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.Page(c, items, total,
		clampPage(queryInt(c, "Page", service.DefaultPage)),
		clampPageSize(queryInt(c, "PageSize", service.DefaultPageSize)))
}

// GetEmail 处理 GET /logs/emails/:UID。
func (h *LogHandler) GetEmail(c *gin.Context) {
	view, err := h.logs.GetEmail(c.Param("UID"))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, view)
}

// queryStrings 读可重复的 query 参数（如 `?Type=a&Type=b`）。
func queryStrings(c *gin.Context, name string) []string {
	raw := c.QueryArray(name)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
