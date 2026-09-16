package handler

import (
	"io"
	"log/slog"
	"mime/multipart"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
)

// GalleryHandler 处理图库端点（/uploads/**）。
//
// 权限：需登录（`RequireAuth + RequirePasswordChanged`）。
// **不要求 admin**：普通用户也能上传与管理自己的图片（D33：各自私有）。
type GalleryHandler struct {
	gallery *service.GalleryService
	upload  *service.UploadService
	log     *slog.Logger
}

// NewGalleryHandler 构造。
func NewGalleryHandler(gallery *service.GalleryService, upload *service.UploadService, log *slog.Logger) *GalleryHandler {
	return &GalleryHandler{gallery: gallery, upload: upload, log: log}
}

// Register 注册路由。
func (h *GalleryHandler) Register(g *gin.RouterGroup) {
	g.POST("", h.Upload)
	g.POST("/from-url", h.UploadFromURL)
	g.GET("", h.List)
	g.GET("/stats", h.Stats)
	g.POST("/batch-delete", h.BatchDelete)
	g.POST("/links", h.BatchLink)
	g.GET("/:UID", h.Get)
	g.PATCH("/:UID", h.Update)
	g.DELETE("/:UID", h.Delete)
	g.GET("/:UID/link", h.Link)
}

// maxFromURLCount 是「从 URL 上传」的单次上限（docs/API.md §4.1）。
const maxFromURLCount = 20

// Upload 处理 POST /uploads（multipart/form-data）。
//
// 表单字段（**PascalCase**，与 JSON 字段命名规则一致）：
//
//	Files      可多值（同一字段名重复）
//	StorageUID 目标存储配置
//	KeepLocal  可选（"true"/"1"）
func (h *GalleryHandler) Upload(c *gin.Context) {
	form, err := c.MultipartForm()
	if err != nil {
		response.InvalidParam(c, "请求不是合法的 multipart 表单")
		return
	}

	fileHeaders := form.File["Files"]
	if len(fileHeaders) == 0 {
		response.InvalidParam(c, "缺少 Files 字段（可多值）")
		return
	}

	// 逐个流式落盘：避免把整批文件读进内存
	files, cleanup, err := h.saveUploadedFiles(fileHeaders)
	if err != nil {
		cleanup()
		respondServiceError(c, h.log, err)
		return
	}

	keepLocal := parseBoolPtr(firstFormValue(form.Value, "KeepLocal"))

	result, err := h.upload.EnqueueBatch(c.Request.Context(), middleware.CurrentUser(c), service.EnqueueBatchInput{
		Files:      files,
		StorageUID: firstFormValue(form.Value, "StorageUID"),
		KeepLocal:  keepLocal,
		Source:     model.UploadSourceWeb,
	})
	if err != nil {
		// EnqueueBatch 在失败时**已自行清理**暂存文件；这里只需清理未被它接管的
		cleanup()
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, result)
}

// saveUploadedFiles 把 multipart 文件流式写入本地暂存区。
//
// 返回 (已落盘的文件, 清理函数, error)。
// 出错时调用方**必须**调用 cleanup（已落盘的部分文件要删掉）。
func (h *GalleryHandler) saveUploadedFiles(headers []*multipart.FileHeader) ([]service.IncomingFile, func(), error) {
	out := make([]service.IncomingFile, 0, len(headers))
	written := make([]string, 0, len(headers))

	cleanup := func() {
		h.upload.CleanupTempPaths(written)
	}

	for _, fh := range headers {
		// 只取 base name，防「客户端传 ../../etc/passwd 当文件名」的路径穿越
		name := sanitizeFileName(fh.Filename)
		if name == "" {
			cleanup()
			return nil, cleanup, service.Errorf(response.CodeInvalidParam, "上传的文件缺少文件名")
		}

		path, err := h.upload.TempPathFor(name)
		if err != nil {
			cleanup()
			return nil, cleanup, err
		}

		src, err := fh.Open()
		if err != nil {
			cleanup()
			return nil, cleanup, service.Wrap(response.CodeInternal, "读取上传文件失败", err)
		}

		dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			_ = src.Close()
			cleanup()
			return nil, cleanup, service.Wrap(response.CodeInternal, "创建暂存文件失败", err)
		}

		// 读满 fh.Size 再多读 1 字节：能区分「刚好」与「被截断/超限」
		n, copyErr := io.Copy(dst, io.LimitReader(src, fh.Size+1))
		_ = dst.Close()
		_ = src.Close()

		if copyErr != nil {
			cleanup()
			return nil, cleanup, service.Wrap(response.CodeInternal, "写入暂存文件失败", copyErr)
		}
		if n > fh.Size {
			// 实际字节数多于声明：保守拒绝（可能是恶意的超大文件）
			cleanup()
			return nil, cleanup, service.Errorf(response.CodeInvalidParam,
				"文件「%s」的实际大小与声明不一致", name)
		}

		written = append(written, path)
		out = append(out, service.IncomingFile{
			Path:         path,
			OriginalName: name,
			Size:         n,
		})
	}
	return out, cleanup, nil
}

// UploadFromURL 处理 POST /uploads/from-url。
//
// **SSRF 防护**（docs/API.md §4.1）：解析真实 IP，拒绝回环/私有/链路本地网段，
// 除非 `PICGO_WEB_ALLOW_PRIVATE_FETCH=true`。
// 校验与下载都放在 service 里（含重定向目标的二次校验），handler 只做编排。
func (h *GalleryHandler) UploadFromURL(c *gin.Context) {
	var req struct {
		URLs       []string `json:"URLs"`
		StorageUID string   `json:"StorageUID"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}
	if len(req.URLs) == 0 {
		response.InvalidParam(c, "缺少 URLs")
		return
	}
	if len(req.URLs) > maxFromURLCount {
		response.InvalidParam(c, "单次最多 20 个 URL")
		return
	}

	files := make([]service.IncomingFile, 0, len(req.URLs))
	written := make([]string, 0, len(req.URLs))
	cleanup := func() { h.upload.CleanupTempPaths(written) }

	for _, rawURL := range req.URLs {
		f, err := h.upload.DownloadToTemp(c.Request.Context(), rawURL)
		if err != nil {
			cleanup()
			respondServiceError(c, h.log, err)
			return
		}
		written = append(written, f.Path)
		files = append(files, *f)
	}

	result, err := h.upload.EnqueueBatch(c.Request.Context(), middleware.CurrentUser(c), service.EnqueueBatchInput{
		Files:      files,
		StorageUID: req.StorageUID,
		Source:     model.UploadSourceWeb,
	})
	if err != nil {
		cleanup()
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, result)
}

// List 处理 GET /uploads。
//
// `Scope` 语义见 docs/API.md §4.2：普通用户传 `all` 会被**静默降级**为 `mine`。
func (h *GalleryHandler) List(c *gin.Context) {
	items, total, err := h.gallery.List(service.GalleryListInput{
		Keyword:    strings.TrimSpace(c.Query("Keyword")),
		StorageUID: strings.TrimSpace(c.Query("StorageUID")),
		Status:     strings.TrimSpace(c.Query("Status")),
		Scope:      strings.TrimSpace(c.Query("Scope")),
		Sort:       strings.TrimSpace(c.Query("Sort")),
		Order:      strings.TrimSpace(c.Query("Order")),
		Page:       queryInt(c, "Page", service.DefaultPage),
		PageSize:   queryInt(c, "PageSize", service.DefaultPageSize),
	}, middleware.CurrentUser(c))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.Page(c, items, total,
		clampPage(queryInt(c, "Page", service.DefaultPage)),
		clampPageSize(queryInt(c, "PageSize", service.DefaultPageSize)))
}

// Get 处理 GET /uploads/:UID。
func (h *GalleryHandler) Get(c *gin.Context) {
	view, err := h.gallery.Get(c.Param("UID"), middleware.CurrentUser(c))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, view)
}

// Update 处理 PATCH /uploads/:UID。
func (h *GalleryHandler) Update(c *gin.Context) {
	var req struct {
		AliasName *string `json:"AliasName"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	view, err := h.gallery.Update(c.Request.Context(), c.Param("UID"), service.GalleryUpdateInput{
		AliasName: req.AliasName,
	}, middleware.CurrentUser(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, view)
}

// Delete 处理 DELETE /uploads/:UID。
func (h *GalleryHandler) Delete(c *gin.Context) {
	deleteRemote := parseBoolQuery(c.Query("DeleteRemote"))

	result, err := h.gallery.Delete(c.Request.Context(), c.Param("UID"), deleteRemote,
		middleware.CurrentUser(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}

	// 驱动不支持远端删除时，用 Message 明确告知（而不是让用户以为删干净了）
	if deleteRemote && !result.RemoteDeleted {
		msg := "已删除本地记录"
		if !result.RemoteDeleteSupported {
			msg = "该存储驱动不支持远端删除，已仅删除本地记录"
		} else if result.RemoteDeleteError != "" {
			msg = "远端删除失败（" + result.RemoteDeleteError + "），已删除本地记录"
		}
		response.OKMsg(c, msg, result)
		return
	}
	response.OK(c, result)
}

// BatchDelete 处理 POST /uploads/batch-delete。
func (h *GalleryHandler) BatchDelete(c *gin.Context) {
	var req struct {
		UIDs         []string `json:"UIDs"`
		DeleteRemote bool     `json:"DeleteRemote"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	result, err := h.gallery.BatchDelete(c.Request.Context(), req.UIDs, req.DeleteRemote,
		middleware.CurrentUser(c), middleware.ClientIP(c), c.Request.UserAgent())
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, result)
}

// Stats 处理 GET /uploads/stats。
func (h *GalleryHandler) Stats(c *gin.Context) {
	stats, err := h.gallery.Stats(c.Query("Scope"), middleware.CurrentUser(c))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, stats)
}

// Link 处理 GET /uploads/:UID/link（外链格式化，D68）。
func (h *GalleryHandler) Link(c *gin.Context) {
	result, err := h.gallery.Link(c.Param("UID"), c.Query("Format"), middleware.CurrentUser(c))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, result)
}

// BatchLink 处理 POST /uploads/links（批量复制外链）。
func (h *GalleryHandler) BatchLink(c *gin.Context) {
	var req struct {
		UIDs   []string `json:"UIDs"`
		Format string   `json:"Format"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体格式不正确")
		return
	}

	result, err := h.gallery.BatchLink(req.UIDs, req.Format, middleware.CurrentUser(c))
	if err != nil {
		respondServiceError(c, h.log, err)
		return
	}
	response.OK(c, result)
}
