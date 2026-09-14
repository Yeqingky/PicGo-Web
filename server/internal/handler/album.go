package handler

import (
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
)

// AlbumHandler 处理相册端点（/albums/**）。
//
// ⚠️ 本项目**没有标签（Tags）功能**（D55）：整理手段为「相册 + 重命名」。
type AlbumHandler struct {
	albums *service.AlbumService
	log    *slog.Logger
}

// NewAlbumHandler 构造。
func NewAlbumHandler(albums *service.AlbumService, log *slog.Logger) *AlbumHandler {
	return &AlbumHandler{albums: albums, log: log}
}

// Register 注册路由。
//
// ⚠️ `/move-uploads` **必须注册在 `/:UID/...` 之前**，
// 否则 gin 会把 "move-uploads" 当成 `:UID` 而命中错误的路由。
func (h *AlbumHandler) Register(g *gin.RouterGroup) {
	g.GET("", h.List)
	g.POST("", h.Create)
	g.POST("/move-uploads", h.MoveUploads)

	g.GET("/:UID", h.Get)
	g.PATCH("/:UID", h.Update)
	g.DELETE("/:UID", h.Delete)
	g.POST("/:UID/move-uploads", h.MoveUploadsTo)
}

// List 处理 GET /albums。
func (h *AlbumHandler) List(c *gin.Context) {
	items, err := h.albums.List(service.AlbumListInput{
		Keyword: strings.TrimSpace(c.Query("Keyword")),
		Sort:    strings.TrimSpace(c.Query("Sort")),
		Order:   strings.TrimSpace(c.Query("Order")),
	}, middleware.CurrentUser(c))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, gin.H{"Items": items})
}

// Get 处理 GET /albums/:UID。
func (h *AlbumHandler) Get(c *gin.Context) {
	view, err := h.albums.Get(c.Param("UID"), middleware.CurrentUser(c))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, view)
}

// Create 处理 POST /albums。
func (h *AlbumHandler) Create(c *gin.Context) {
	var req struct {
		Name  string `json:"Name"`
		Intro string `json:"Intro"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	view, err := h.albums.Create(c.Request.Context(), service.CreateAlbumInput{
		Name:  req.Name,
		Intro: req.Intro,
	}, middleware.CurrentUser(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, view)
}

// Update 处理 PATCH /albums/:UID。
func (h *AlbumHandler) Update(c *gin.Context) {
	var req struct {
		Name           *string `json:"Name"`
		Intro          *string `json:"Intro"`
		CoverUploadUID *string `json:"CoverUploadUID"`
		SortOrder      *int    `json:"SortOrder"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	view, err := h.albums.Update(c.Request.Context(), c.Param("UID"), service.UpdateAlbumInput{
		Name:           req.Name,
		Intro:          req.Intro,
		CoverUploadUID: req.CoverUploadUID,
		SortOrder:      req.SortOrder,
	}, middleware.CurrentUser(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, view)
}

// Delete 处理 DELETE /albums/:UID。
//
// `WithUploads=true` 时相册内图片**只脱离相册，不删除**（docs/API.md §5）。
func (h *AlbumHandler) Delete(c *gin.Context) {
	withUploads := parseBoolQuery(c.Query("WithUploads"))

	result, err := h.albums.Delete(c.Request.Context(), c.Param("UID"), withUploads,
		middleware.CurrentUser(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, result)
}

// MoveUploadsTo 处理 POST /albums/:UID/move-uploads（目标相册来自路径）。
func (h *AlbumHandler) MoveUploadsTo(c *gin.Context) {
	var req struct {
		UploadUIDs []string `json:"UploadUIDs"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	result, err := h.albums.MoveUploads(c.Request.Context(), req.UploadUIDs, c.Param("UID"),
		middleware.CurrentUser(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, result)
}

// MoveUploads 处理 POST /albums/move-uploads（目标相册来自请求体；空 = 移出相册）。
func (h *AlbumHandler) MoveUploads(c *gin.Context) {
	var req struct {
		UploadUIDs     []string `json:"UploadUIDs"`
		TargetAlbumUID string   `json:"TargetAlbumUID"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	result, err := h.albums.MoveUploads(c.Request.Context(), req.UploadUIDs, req.TargetAlbumUID,
		middleware.CurrentUser(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, result)
}
