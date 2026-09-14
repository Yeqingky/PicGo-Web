package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/events"
	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
)

// JobHandler 处理任务端点（/jobs/**）与 SSE（/events）。
//
// 权限：需登录。`Scope=all` 仅管理员（普通用户静默降级为 `mine`）。
type JobHandler struct {
	jobs *service.JobService
	hub  *events.Hub
	log  *slog.Logger
}

// NewJobHandler 构造。
func NewJobHandler(jobs *service.JobService, hub *events.Hub, log *slog.Logger) *JobHandler {
	return &JobHandler{jobs: jobs, hub: hub, log: log}
}

// Register 注册路由（不含 SSE，SSE 需要额外的鉴权处理，见 RegisterEvents）。
func (h *JobHandler) Register(g *gin.RouterGroup) {
	g.GET("", h.List)
	g.GET("/:UID", h.Get)
	g.GET("/:UID/logs", h.Logs)
	g.DELETE("/:UID", h.Delete)
}

// List 处理 GET /jobs。
func (h *JobHandler) List(c *gin.Context) {
	items, total, err := h.jobs.List(service.JobListInput{
		Kind:     strings.TrimSpace(c.Query("Kind")),
		Status:   strings.TrimSpace(c.Query("Status")),
		Scope:    strings.TrimSpace(c.Query("Scope")),
		Page:     queryInt(c, "Page", service.DefaultPage),
		PageSize: queryInt(c, "PageSize", service.DefaultPageSize),
	}, middleware.CurrentUser(c))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.Page(c, items, total,
		clampPage(queryInt(c, "Page", service.DefaultPage)),
		clampPageSize(queryInt(c, "PageSize", service.DefaultPageSize)))
}

// Get 处理 GET /jobs/:UID（含子项）。
func (h *JobHandler) Get(c *gin.Context) {
	view, err := h.jobs.Get(c.Param("UID"), middleware.CurrentUser(c))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, view)
}

// Logs 处理 GET /jobs/:UID/logs（增量拉取）。
func (h *JobHandler) Logs(c *gin.Context) {
	result, err := h.jobs.Logs(
		c.Param("UID"),
		int(queryInt64(c, "AfterSeq", 0)),
		queryInt(c, "Limit", 0),
		middleware.CurrentUser(c),
	)
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, result)
}

// Delete 处理 DELETE /jobs/:UID（仅清理已结束的任务）。
func (h *JobHandler) Delete(c *gin.Context) {
	err := h.jobs.Delete(c.Request.Context(), c.Param("UID"),
		middleware.CurrentUser(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, nil)
}

// ---------------------------------------------------------------------------
// SSE（docs/API.md §8.1）
// ---------------------------------------------------------------------------

// ssePingInterval 保活间隔（与 events.PingIntervalSeconds 一致）。
const ssePingInterval = events.PingIntervalSeconds * time.Second

// sseWriteTimeout 单次写超时（客户端卡住时不该把连接一直挂着）。
const sseWriteTimeout = 10 * time.Second

// Events 处理 GET /events（SSE）。
//
// 鉴权：浏览器走 Cookie（`pcw_at`）；非浏览器可用 `?Token=<jwt|pcw_...>`
// —— **仅此场景允许 query 传令牌**（EventSource 无法自定义请求头）。
// 由 RegisterEvents 里的 PromoteQueryToken 中间件把 query 提升为 Authorization 头。
//
// 投递范围（docs/API.md §8.1）：**只推送给任务所属者与管理员**。
// 过滤在**订阅端**完成（Hub 只广播，带 UserUID 供这里判断）。
func (h *JobHandler) Events(c *gin.Context) {
	viewer := middleware.CurrentUser(c)
	if viewer == nil {
		response.Abort(c, response.CodeUnauthorized)
		return
	}
	if h.hub == nil {
		response.Abort(c, response.CodeInternal)
		return
	}

	requestCtx := c.Request.Context()

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		response.AbortMsg(c, response.CodeInternal, "当前服务器不支持 SSE")
		return
	}

	// SSE 响应头
	headers := c.Writer.Header()
	headers.Set("Content-Type", "text/event-stream")
	headers.Set("Cache-Control", "no-cache, no-transform")
	headers.Set("Connection", "keep-alive")
	// 关掉 nginx 等反代的缓冲，否则事件会被攒着一起发
	headers.Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, cancel := h.hub.Subscribe(viewer.UID, viewer.IsAdmin())
	defer cancel()

	// 首帧：告诉客户端「已连接」，顺便让前端能立刻确认连接可用
	writeSSE(c, flusher, "system.notice",
		map[string]any{"Level": "info", "Message": "已连接到事件流"})

	ticker := time.NewTicker(ssePingInterval)
	defer ticker.Stop()

	h.log.Debug("SSE 客户端已连接", "user", viewer.UID, "admin", viewer.IsAdmin())

	for {
		select {
		case <-requestCtx.Done():
			h.log.Debug("SSE 客户端已断开", "user", viewer.UID)
			return

		case ev, ok := <-ch:
			if !ok {
				// Hub 被关闭（进程退出中）
				return
			}
			if !visibleTo(ev, viewer.UID, viewer.IsAdmin()) {
				continue
			}
			writeSSE(c, flusher, ev.Name, ev.Data)

		case <-ticker.C:
			writeSSE(c, flusher, events.EventPing, map[string]any{"Ts": time.Now().Unix()})
		}
	}
}

// visibleTo 判断某事件是否应推给该订阅者。
//
// 规则：
//   - `AdminOnly` 事件只给管理员
//   - 带 `UserUID` 的事件只给该用户（与管理员）
//   - 不带 `UserUID` 的事件（系统通知）给所有人 —— 它们不含任何用户数据
func visibleTo(ev events.Event, uid string, isAdmin bool) bool {
	if ev.AdminOnly && !isAdmin {
		return false
	}
	if ev.UserUID == "" {
		return true
	}
	return isAdmin || ev.UserUID == uid
}

// writeSSE 写一帧 SSE。
//
// 写失败时**返回 false 且不 panic**：客户端可能已断开（写 ErrClosedPipe），
// 这属于正常情况，由上层循环检测 ctx 结束退出。
func writeSSE(c *gin.Context, flusher http.Flusher, name string, data any) bool {
	payload, err := json.Marshal(data)
	if err != nil {
		// 序列化失败：发一条可读的替代帧而不是让连接静默卡住
		payload = []byte(`{"Error":"事件负载序列化失败"}`)
	}

	// SSE 规范：event 名后跟 data，空行结束一帧
	if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", name, payload); err != nil {
		return false
	}
	flusher.Flush()
	return true
}
