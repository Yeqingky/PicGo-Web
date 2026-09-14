package handler

import (
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
)

// StorageHandler 处理存储驱动配置端点（/storage/**）。
//
// 权限：**全部端点需要 admin**（D4：存储驱动只有管理员能配置）。
// 由 RegisterBusiness 统一挂 `RequireAuth + RequirePasswordChanged + RequireAdmin`。
type StorageHandler struct {
	storage *service.StorageService
	log     *slog.Logger
}

// NewStorageHandler 构造。
func NewStorageHandler(storage *service.StorageService, log *slog.Logger) *StorageHandler {
	return &StorageHandler{storage: storage, log: log}
}

// Register 注册路由。
func (h *StorageHandler) Register(g *gin.RouterGroup) {
	g.GET("/drivers", h.ListDrivers)
	g.POST("/drivers/schema", h.DriverSchema)

	g.GET("/configs", h.List)
	g.POST("/configs", h.Create)
	g.GET("/configs/:UID", h.Get)
	g.PATCH("/configs/:UID", h.Update)
	g.PUT("/configs/:UID/secrets", h.UpdateSecrets)
	g.DELETE("/configs/:UID", h.Delete)
	g.POST("/configs/:UID/activate", h.Activate)
	g.POST("/configs/:UID/test", h.Test)
}

// ListDrivers 处理 GET /storage/drivers。
func (h *StorageHandler) ListDrivers(c *gin.Context) {
	drivers, err := h.storage.ListDrivers(c.Request.Context())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, gin.H{"Drivers": drivers})
}

// DriverSchema 处理 POST /storage/drivers/schema（DependsOn 联动重求值）。
func (h *StorageHandler) DriverSchema(c *gin.Context) {
	var req struct {
		Type    string         `json:"Type"`
		Answers map[string]any `json:"Answers"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}
	if strings.TrimSpace(req.Type) == "" {
		response.InvalidParam(c, "缺少 Type")
		return
	}

	driver, err := h.storage.DriverSchema(c.Request.Context(), req.Type, req.Answers)
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, driver)
}

// List 处理 GET /storage/configs。
func (h *StorageHandler) List(c *gin.Context) {
	var enabled *bool
	if raw := strings.TrimSpace(c.Query("Enabled")); raw != "" {
		v := raw == "1" || strings.EqualFold(raw, "true")
		enabled = &v
	}

	items, total, err := h.storage.List(c.Request.Context(), service.StorageListInput{
		Keyword:  strings.TrimSpace(c.Query("Keyword")),
		Type:     strings.TrimSpace(c.Query("Type")),
		Enabled:  enabled,
		Page:     queryInt(c, "Page", service.DefaultPage),
		PageSize: queryInt(c, "PageSize", service.DefaultPageSize),
	})
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.Page(c, items, total,
		clampPage(queryInt(c, "Page", service.DefaultPage)),
		clampPageSize(queryInt(c, "PageSize", service.DefaultPageSize)))
}

// Get 处理 GET /storage/configs/:UID。
func (h *StorageHandler) Get(c *gin.Context) {
	view, err := h.storage.Get(c.Request.Context(), c.Param("UID"))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, view)
}

// Create 处理 POST /storage/configs。
func (h *StorageHandler) Create(c *gin.Context) {
	var req struct {
		Name            string         `json:"Name"`
		Type            string         `json:"Type"`
		PicgoConfigName string         `json:"PicgoConfigName"`
		Enabled         *bool          `json:"Enabled"`
		IsDefault       bool           `json:"IsDefault"`
		PathTemplate    string         `json:"PathTemplate"`
		FileTemplate    string         `json:"FileTemplate"`
		Config          map[string]any `json:"Config"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	view, err := h.storage.Create(c.Request.Context(), service.CreateStorageInput{
		Name:            req.Name,
		Type:            req.Type,
		PicgoConfigName: req.PicgoConfigName,
		Enabled:         req.Enabled,
		IsDefault:       req.IsDefault,
		PathTemplate:    req.PathTemplate,
		FileTemplate:    req.FileTemplate,
		Config:          req.Config,
	}, middleware.CurrentUserUID(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, view)
}

// Update 处理 PATCH /storage/configs/:UID。
//
// ⚠️ **不接受 `PicgoConfigName`**（创建后只读，D64 推论）：
// picgo 的 `createOrUpdate` 是「未命中即新建」语义，允许改名会把同一条配置裂成两条。
// 展示名要改就改 `Name`。
func (h *StorageHandler) Update(c *gin.Context) {
	var req struct {
		Name         *string `json:"Name"`
		Enabled      *bool   `json:"Enabled"`
		IsDefault    *bool   `json:"IsDefault"`
		PathTemplate *string `json:"PathTemplate"`
		FileTemplate *string `json:"FileTemplate"`

		// 显式声明以便**明确忽略**并给出提示（而不是让调用方以为改成功了）
		PicgoConfigName *string `json:"PicgoConfigName"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}
	if req.PicgoConfigName != nil {
		response.InvalidParam(c, "PicgoConfigName 创建后不可修改（如需改名请改 Name）")
		return
	}

	view, err := h.storage.Update(c.Request.Context(), c.Param("UID"), service.UpdateStorageInput{
		Name:         req.Name,
		Enabled:      req.Enabled,
		IsDefault:    req.IsDefault,
		PathTemplate: req.PathTemplate,
		FileTemplate: req.FileTemplate,
	}, middleware.CurrentUserUID(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, view)
}

// UpdateSecrets 处理 PUT /storage/configs/:UID/secrets（单独更新凭据）。
func (h *StorageHandler) UpdateSecrets(c *gin.Context) {
	var req struct {
		Config map[string]any `json:"Config"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	view, err := h.storage.UpdateSecrets(c.Request.Context(), c.Param("UID"), req.Config,
		middleware.CurrentUserUID(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, gin.H{
		"UID":          view.UID,
		"HasSecrets":   view.HasSecrets,
		"SecretFields": view.SecretFields,
	})
}

// Delete 处理 DELETE /storage/configs/:UID。
func (h *StorageHandler) Delete(c *gin.Context) {
	force := strings.EqualFold(strings.TrimSpace(c.Query("Force")), "true") ||
		strings.TrimSpace(c.Query("Force")) == "1"

	result, err := h.storage.Delete(c.Request.Context(), c.Param("UID"), force,
		middleware.CurrentUserUID(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, result)
}

// Activate 处理 POST /storage/configs/:UID/activate。
func (h *StorageHandler) Activate(c *gin.Context) {
	view, err := h.storage.Activate(c.Request.Context(), c.Param("UID"),
		middleware.CurrentUserUID(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, view)
}

// Test 处理 POST /storage/configs/:UID/test。
//
// ⚠️ 测试失败时 HTTP 仍为 200（「测试本身执行成功」），
// 结果用 `Ok` / `Message` 表达 —— 见 docs/API.md §3.2。
func (h *StorageHandler) Test(c *gin.Context) {
	result, err := h.storage.Test(c.Request.Context(), c.Param("UID"),
		middleware.CurrentUserUID(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, result)
}
