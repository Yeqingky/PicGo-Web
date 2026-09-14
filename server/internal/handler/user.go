package handler

import (
	"log/slog"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
)

// UserHandler 处理管理员用户管理端点（/users/**，D25 唯一建号入口）。
type UserHandler struct {
	users *service.UserService
	log   *slog.Logger
}

// NewUserHandler 构造。
func NewUserHandler(users *service.UserService, log *slog.Logger) *UserHandler {
	return &UserHandler{users: users, log: log}
}

// Register 注册路由。
//
// 调用方需保证已挂 RequireAuth + RequirePasswordChanged + RequireAdmin
// （本 handler 的所有端点都是管理员操作）。
func (h *UserHandler) Register(g *gin.RouterGroup) {
	g.GET("", h.List)
	g.POST("", h.Create)
	g.GET("/:UID", h.Get)
	g.PATCH("/:UID", h.Update)
	g.DELETE("/:UID", h.Delete)
	g.POST("/:UID/reset-password", h.ResetPassword)
}

// List 处理 GET /api/web/v1/users。
func (h *UserHandler) List(c *gin.Context) {
	filter := repository.UserListFilter{
		Keyword:  strings.TrimSpace(c.Query("Keyword")),
		Role:     strings.TrimSpace(c.Query("Role")),
		Status:   strings.TrimSpace(c.Query("Status")),
		Sort:     strings.TrimSpace(c.Query("Sort")),
		Order:    strings.TrimSpace(c.Query("Order")),
		Page:     queryInt(c, "Page", service.DefaultPage),
		PageSize: queryInt(c, "PageSize", service.DefaultPageSize),
	}

	// 分页参数越界时直接纠正，而不是报错（对调用方更友好）
	if filter.Page <= 0 {
		filter.Page = service.DefaultPage
	}
	if filter.PageSize <= 0 {
		filter.PageSize = service.DefaultPageSize
	}
	if filter.PageSize > service.MaxPageSize {
		filter.PageSize = service.MaxPageSize
	}

	items, total, err := h.users.List(filter)
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.Page(c, items, total, filter.Page, filter.PageSize)
}

// Get 处理 GET /api/web/v1/users/:UID。
func (h *UserHandler) Get(c *gin.Context) {
	view, err := h.users.Get(c.Param("UID"))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, view)
}

// Create 处理 POST /api/web/v1/users。
func (h *UserHandler) Create(c *gin.Context) {
	var req struct {
		Email         string `json:"Email"`
		Password      string `json:"Password"`
		Role          string `json:"Role"`
		Nickname      string `json:"Nickname"`
		CapacityBytes *int64 `json:"CapacityBytes"` // 省略 → 用 user.defaultCapacityBytes（D21）
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	view, err := h.users.Create(c.Request.Context(), service.CreateUserInput{
		Email:         req.Email,
		Password:      req.Password,
		Role:          req.Role,
		Nickname:      req.Nickname,
		CapacityBytes: req.CapacityBytes,
	}, middleware.CurrentUserUID(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, view)
}

// Update 处理 PATCH /api/web/v1/users/:UID。
func (h *UserHandler) Update(c *gin.Context) {
	var req struct {
		Email              *string `json:"Email"`
		Nickname           *string `json:"Nickname"`
		AvatarURL          *string `json:"AvatarURL"`
		Homepage           *string `json:"Homepage"`
		Role               *string `json:"Role"`
		Status             *string `json:"Status"`
		CapacityBytes      *int64  `json:"CapacityBytes"`
		NewPassword        *string `json:"NewPassword"`
		MustChangePassword *bool   `json:"MustChangePassword"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	view, hint, err := h.users.Update(c.Request.Context(), c.Param("UID"), service.UpdateUserInput{
		Email:              req.Email,
		Nickname:           req.Nickname,
		AvatarURL:          req.AvatarURL,
		Homepage:           req.Homepage,
		Role:               req.Role,
		Status:             req.Status,
		CapacityBytes:      req.CapacityBytes,
		NewPassword:        req.NewPassword,
		MustChangePassword: req.MustChangePassword,
	}, middleware.CurrentUserUID(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}

	if hint != "" {
		response.OKMsg(c, hint, view)
		return
	}
	response.OK(c, view)
}

// Delete 处理 DELETE /api/web/v1/users/:UID（注销账号，D34/D46）。
func (h *UserHandler) Delete(c *gin.Context) {
	result, err := h.users.Delete(c.Request.Context(), c.Param("UID"),
		middleware.CurrentUserUID(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, result)
}

// ResetPassword 处理 POST /api/web/v1/users/:UID/reset-password。
//
// ⚠️ 返回的 NewPassword 明文**只出现这一次**，请管理员立即转交用户。
func (h *UserHandler) ResetPassword(c *gin.Context) {
	password, err := h.users.ResetPassword(c.Request.Context(), c.Param("UID"),
		middleware.CurrentUserUID(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, gin.H{
		"UID":                c.Param("UID"),
		"NewPassword":        password,
		"MustChangePassword": true,
	})
}

// queryInt 读整数 query 参数；缺失或非法时返回 def。
func queryInt(c *gin.Context, name string, def int) int {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}
