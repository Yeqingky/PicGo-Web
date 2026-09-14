package handler

import (
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
)

// PluginHandler 处理插件端点（/plugins/**）。
//
// 权限：**全部需要 admin**（插件是服务器上的任意代码，D45 的 plugin.* 日志）。
type PluginHandler struct {
	plugins *service.PluginService
	log     *slog.Logger
}

// NewPluginHandler 构造。
func NewPluginHandler(plugins *service.PluginService, log *slog.Logger) *PluginHandler {
	return &PluginHandler{plugins: plugins, log: log}
}

// Register 注册路由。
func (h *PluginHandler) Register(g *gin.RouterGroup) {
	g.GET("", h.List)
	g.POST("/install", h.Install)
	g.POST("/uninstall", h.Uninstall)
	g.POST("/update", h.Update)
	g.GET("/:name/readme", h.Readme)
	g.PATCH("/:name", h.SetEnabled)
}

// List 处理 GET /plugins。
func (h *PluginHandler) List(c *gin.Context) {
	result, err := h.plugins.List(c.Request.Context())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, result)
}

// Readme 处理 GET /plugins/:name/readme。
func (h *PluginHandler) Readme(c *gin.Context) {
	data, err := h.plugins.Readme(c.Request.Context(), c.Param("name"))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, data)
}

// pluginNamesRequest 是安装/卸载/更新的请求体。
type pluginNamesRequest struct {
	Names []string `json:"Names"`
}

// Install 处理 POST /plugins/install。
func (h *PluginHandler) Install(c *gin.Context) {
	var req pluginNamesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}
	if len(req.Names) == 0 {
		response.InvalidParam(c, "必须指定要安装的插件名")
		return
	}

	result, err := h.plugins.Install(c.Request.Context(), req.Names,
		middleware.CurrentUserUID(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OKMsg(c, "已开始安装，请在任务面板查看进度", result)
}

// Uninstall 处理 POST /plugins/uninstall。
func (h *PluginHandler) Uninstall(c *gin.Context) {
	var req pluginNamesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	result, err := h.plugins.Uninstall(c.Request.Context(), req.Names,
		middleware.CurrentUserUID(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OKMsg(c, "已开始卸载，请在任务面板查看进度", result)
}

// Update 处理 POST /plugins/update（Names 为空 = 全部）。
func (h *PluginHandler) Update(c *gin.Context) {
	var req pluginNamesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	result, err := h.plugins.Update(c.Request.Context(), req.Names,
		middleware.CurrentUserUID(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OKMsg(c, "已开始更新，请在任务面板查看进度", result)
}

// SetEnabled 处理 PATCH /plugins/:name。
func (h *PluginHandler) SetEnabled(c *gin.Context) {
	var req struct {
		Enabled *bool `json:"Enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}
	if req.Enabled == nil {
		response.InvalidParam(c, "缺少 Enabled")
		return
	}

	name := strings.TrimSpace(c.Param("name"))
	if err := h.plugins.SetEnabled(c.Request.Context(), name, *req.Enabled,
		middleware.CurrentUserUID(c), middleware.ClientIP(c), c.Request.UserAgent()); err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, gin.H{"Name": name, "Enabled": *req.Enabled})
}
