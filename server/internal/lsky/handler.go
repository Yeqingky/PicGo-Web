package lsky

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/events"
	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// Deps 是 Lsky 兼容层的依赖。
//
// 全部复用内部 service —— **不重复实现任何业务逻辑**，只在 handler 层做
// 「信封 + 字段命名 + 时间格式 + 分页形状」的映射（docs/API.md §12.1）。
type Deps struct {
	Cfg      *config.Config
	Log      *slog.Logger
	Settings *settings.Service
	Hub      *events.Hub

	// UsersSvc 复用内部登录（限流/防枚举/审计全部生效）。
	UsersSvc *service.UserService
	Tokens   *service.TokenService
	Uploads  *service.UploadService
	Gallery  *service.GalleryService
	Storage  *service.StorageService

	Users *repository.UserRepo
	Logs  *repository.LogRepo
	// TokenRepo 用于 Lsky 专用鉴权（见 auth.go）。
	TokenRepo *repository.TokenRepo
	// UploadRepo 直连用于「按批次回查成功项」（Lsky 同步上传的收尾）。
	UploadRepo *repository.UploadRepo
}

// Handler 实现 Lsky v1 契约。
type Handler struct {
	deps Deps
}

// New 构造。
func New(deps Deps) *Handler { return &Handler{deps: deps} }

// syncUploadTimeout 是「同步上传」等待 job 完成的最长时长。
//
// 为什么需要它：lsky 契约要求 `POST /upload` **阻塞**到拿到 URL，
// 但内部上传是队列化的（`upload.concurrency` 可能为 1，多张图排队）。
// 客户端自身的 HTTP 超时通常 30~60 秒，这里取 60 秒并给出明确错误，
// 让客户端能区分「超时」与「失败」。
const syncUploadTimeout = 60 * time.Second

// Register 挂载 Lsky 路由到 **根级 `/api/v1`**（D80：与内部 `/api/web/v1` 隔离）。
//
// `enabled` 为假时**不注册任何路由**（`integration.lsky.enabled=false`）。
func (h *Handler) Register(engine *gin.Engine, enabled bool) {
	if !enabled {
		h.deps.Log.Info("Lsky 兼容层已禁用（integration.lsky.enabled=false）")
		return
	}

	root := engine.Group("/api/v1")

	// ---- 无需鉴权 ----
	root.POST("/tokens", h.CreateToken)
	root.GET("/strategies", h.ListStrategies)

	// ---- 需 API Token ----
	// ⚠️ 只认 API Token（pcw_...），**不认** 浏览器 Cookie 与内部 JWT：
	// Lsky 客户端是程序，用 Cookie 反而会让「浏览器已登录」意外影响 API 语义。
	auth := root.Group("", h.requireAPIToken())
	{
		auth.DELETE("/tokens", h.DeleteTokens)
		auth.GET("/profile", h.GetProfile)
		auth.POST("/upload", h.Upload)
		auth.GET("/images", h.ListImages)
		auth.DELETE("/images/:key", h.DeleteImage)
		auth.GET("/albums", h.ListAlbums)
		auth.DELETE("/albums/:id", h.DeleteAlbum)
	}

	h.deps.Log.Info("Lsky v1 兼容层已挂载", "prefix", "/api/v1",
		"endpoints", "tokens,profile,strategies,upload,images,albums")
}

// ---------------------------------------------------------------------------
// 令牌
// ---------------------------------------------------------------------------

// CreateToken 处理 `POST /api/v1/tokens`（邮箱 + 密码换令牌）。
func (h *Handler) CreateToken(c *gin.Context) {
	var req TokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(422, Envelope{Status: false, Message: "The given data was invalid."})
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || req.Password == "" {
		c.JSON(422, Envelope{Status: false, Message: "The given data was invalid."})
		return
	}

	// 复用内部登录逻辑：限流、防枚举、写 LoginAttempts / OperationLogs 全部生效。
	res, err := h.deps.UsersSvc.Login(
		c.Request.Context(), req.Email, req.Password, middleware.ClientIP(c), c.GetHeader("User-Agent"),
	)
	if err != nil {
		FailMsg(c, serviceCodeOf(err), mapAuthMessage(err))
		return
	}
	user := res.User

	days := int(h.deps.Settings.GetInt("integration.lsky.tokenTtlDays", 365))

	// Lsky 的 `POST /tokens` 语义是「**给我一个新的可用令牌**」，客户端会反复调用
	// （换设备、令牌丢了、重装插件）。而内部 `CreateAPIToken` 拒绝**同名**令牌
	// （防用户混淆），直接调用会在第二次就返回 409 Conflict —— **实测踩到**。
	//
	// 因此这里先吊销同名旧令牌再签发新的：
	//   · 行为可预期：「最后登录的那个令牌有效」
	//   · 不会无限累积（同一客户端反复登录只留一条）
	//   · 符合用户直觉：重新登录 = 换一把新钥匙
	const lskyTokenName = "lsky"

	if err := h.revokeTokensByName(c, user.UID, lskyTokenName); err != nil {
		h.deps.Log.Error("Lsky 令牌签发：清理同名旧令牌失败", "user_uid", user.UID, "err", err)
		FailInternal(c)
		return
	}

	created, err := h.deps.Tokens.CreateAPIToken(
		c.Request.Context(), user.UID, lskyTokenName, days,
		middleware.ClientIP(c), c.GetHeader("User-Agent"),
	)
	if err != nil {
		h.deps.Log.Error("Lsky 令牌签发失败", "user_uid", user.UID, "err", err)
		Fail(c, serviceCodeOf(err))
		return
	}

	OK(c, TokenData{Token: created.Token, Name: lskyTokenName})
}

// revokeTokensByName 吊销某用户名下指定名称的**全部**令牌（幂等）。
func (h *Handler) revokeTokensByName(c *gin.Context, userUID, name string) error {
	tokens, err := h.deps.Tokens.ListAPITokens(userUID)
	if err != nil {
		return err
	}
	// 收集待删的 UID（先收集再删：避免边遍历边改底层切片）
	var toDelete []string
	for _, t := range tokens {
		if strings.EqualFold(strings.TrimSpace(t.Name), name) {
			toDelete = append(toDelete, t.UID)
		}
	}
	for _, uid := range toDelete {
		if err := h.deps.Tokens.DeleteAPIToken(
			c.Request.Context(), userUID, uid,
			middleware.ClientIP(c), c.GetHeader("User-Agent"),
		); err != nil {
			return err
		}
	}
	return nil
}

// mapAuthMessage 给鉴权失败一个 lsky 风格的文案。
//
// ⚠️ 无论「邮箱不存在」还是「密码错」，文案必须一致（防账号枚举）。
func mapAuthMessage(err error) string {
	if serviceCodeOf(err) == response.CodeTooMany {
		return "Too Many Requests."
	}
	return "These credentials do not match our records."
}

// DeleteTokens 处理 `DELETE /api/v1/tokens`（清空当前用户的**全部**令牌）。
func (h *Handler) DeleteTokens(c *gin.Context) {
	viewer := middleware.CurrentUser(c)
	if viewer == nil {
		Fail(c, response.CodeUnauthorized)
		return
	}

	tokens, err := h.deps.Tokens.ListAPITokens(viewer.UID)
	if err != nil {
		h.deps.Log.Error("列出令牌失败", "user_uid", viewer.UID, "err", err)
		FailInternal(c)
		return
	}
	for _, t := range tokens {
		if err := h.deps.Tokens.DeleteAPIToken(
			c.Request.Context(), viewer.UID, t.UID, middleware.ClientIP(c), c.GetHeader("User-Agent"),
		); err != nil {
			h.deps.Log.Error("删除令牌失败", "token_uid", t.UID, "err", err)
			FailInternal(c)
			return
		}
	}
	OK(c, nil)
}

// ---------------------------------------------------------------------------
// 用户资料
// ---------------------------------------------------------------------------

// GetProfile 处理 `GET /api/v1/profile`。
func (h *Handler) GetProfile(c *gin.Context) {
	viewer := middleware.CurrentUser(c)
	if viewer == nil {
		Fail(c, response.CodeUnauthorized)
		return
	}

	// 昵称取自 UserProfile（可能不存在，此时用邮箱兜底）
	nickname := ""
	if p, err := h.deps.Users.FindProfile(viewer.UID); err == nil && p != nil {
		nickname = p.Nickname
	}

	imageNum, _, err := h.deps.Users.UploadStats(viewer.UID)
	if err != nil {
		h.deps.Log.Error("统计图片数失败", "user_uid", viewer.UID, "err", err)
		FailInternal(c)
		return
	}

	OK(c, toProfileData(viewer, nickname, imageNum))
}

// ---------------------------------------------------------------------------
// 存储策略
// ---------------------------------------------------------------------------

// ListStrategies 处理 `GET /api/v1/strategies`（**无需鉴权**，对齐 lsky）。
func (h *Handler) ListStrategies(c *gin.Context) {
	views, _, err := h.deps.Storage.List(c.Request.Context(), service.StorageListInput{
		Enabled:  boolPtr(true),
		Page:     1,
		PageSize: 200,
	})
	if err != nil {
		h.deps.Log.Error("列出存储策略失败", "err", err)
		FailInternal(c)
		return
	}

	out := make([]StrategyData, 0, len(views))
	for _, v := range views {
		out = append(out, StrategyData{
			ID:      v.UID,
			Name:    v.Name,
			Intro:   v.Type,
			Key:     v.Type,
			Default: v.IsDefault,
		})
	}
	OK(c, out)
}

// ---------------------------------------------------------------------------
// 图片
// ---------------------------------------------------------------------------

// ListImages 处理 `GET /api/v1/images`。
func (h *Handler) ListImages(c *gin.Context) {
	viewer := middleware.CurrentUser(c)
	if viewer == nil {
		Fail(c, response.CodeUnauthorized)
		return
	}

	page := atoiDefault(c.Query("page"), 1)
	if page < 1 {
		page = 1
	}
	perPage := atoiDefault(c.Query("per_page"), 20)
	if perPage < 1 || perPage > 200 {
		perPage = 20
	}

	sortField, direction := parseLskyOrder(c.Query("order"))

	views, total, err := h.deps.Gallery.List(service.GalleryListInput{
		// ⚠️ Lsky 契约没有「看全部」的能力，**一律只返回本人图片**
		Scope:    "mine",
		Sort:     sortField,
		Order:    direction,
		Page:     page,
		PageSize: perPage,
	}, viewer)
	if err != nil {
		Fail(c, serviceCodeOf(err))
		return
	}

	items := make([]ImageData, 0, len(views))
	for i := range views {
		items = append(items, toImageData(&views[i]))
	}
	OKPage(c, items, total, page, perPage)
}

// DeleteImage 处理 `DELETE /api/v1/images/{key}`。
//
// `{key}` = `Uploads.UID`。
//
// ⚠️ **是否删远端**：Lsky 契约**没有**这个参数，因此默认 `deleteRemote=false`（保守，
// 避免客户端一个误删把图床上的文件也删掉）；管理员可在设置里开启
// `integration.lsky.deleteRemoteOnDelete` 改为同步远端删除（docs/API.md §12.3）。
func (h *Handler) DeleteImage(c *gin.Context) {
	viewer := middleware.CurrentUser(c)
	if viewer == nil {
		Fail(c, response.CodeUnauthorized)
		return
	}

	key := strings.TrimSpace(c.Param("key"))
	if key == "" {
		Fail(c, response.CodeNotFound)
		return
	}

	deleteRemote := h.deps.Settings.GetBool("integration.lsky.deleteRemoteOnDelete", false)

	if _, err := h.deps.Gallery.Delete(
		c.Request.Context(), key, deleteRemote, viewer,
		middleware.ClientIP(c), c.GetHeader("User-Agent"),
	); err != nil {
		Fail(c, serviceCodeOf(err))
		return
	}
	OKMsgOK(c, "删除成功")
}

// Upload 处理 `POST /api/v1/upload`（multipart，**同步语义**）。
//
// 字段名是 Lsky 契约（snake_case）：`file` / `strategy_id` / `album_id` / `permission`。
// `permission` 本项目**忽略**（图片公开性由图床决定，D33/D66）；
// `album_id` 也**忽略**（本项目已移除相册功能，仅收下字段不处理）。
//
// # 同步语义
//
// lsky 客户端期待**立即拿到 URL**，而内部上传是队列化的。
// 做法：入队后订阅事件总线，等 `job.finished` 事件里匹配的 JobUID
// （带超时，避免客户端无限挂起）。内部仍受 `upload.concurrency` 约束（D35）。
func (h *Handler) Upload(c *gin.Context) {
	viewer := middleware.CurrentUser(c)
	if viewer == nil {
		Fail(c, response.CodeUnauthorized)
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(422, Envelope{Status: false, Message: "The file field is required."})
		return
	}

	// 读取到内存再落盘：lsky 契约是单文件，大小受 upload.maxSizeBytes 限制
	f, err := fileHeader.Open()
	if err != nil {
		h.deps.Log.Error("打开上传文件失败", "err", err)
		c.JSON(422, Envelope{Status: false, Message: "The file field is required."})
		return
	}
	defer func() { _ = f.Close() }()

	maxBytes := h.deps.Settings.GetInt("upload.maxSizeBytes", 20<<20)
	// +1 用于探测「超出上限」
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		h.deps.Log.Error("读取上传文件失败", "err", err)
		FailInternal(c)
		return
	}
	if int64(len(data)) > maxBytes {
		c.JSON(422, Envelope{Status: false,
			Message: fmt.Sprintf("The file may not be greater than %d kilobytes.", maxBytes/1024)})
		return
	}

	incoming, err := h.deps.Uploads.SaveIncomingFile(fileHeader.Filename, data)
	if err != nil {
		Fail(c, serviceCodeOf(err))
		return
	}

	// 入队前先订阅：避免 job 极快完成时错过事件
	eventsCh, cancel := h.deps.Hub.Subscribe(viewer.UID, viewer.IsAdmin())
	defer cancel()

	batch, err := h.deps.Uploads.EnqueueBatch(c.Request.Context(), viewer, service.EnqueueBatchInput{
		Files:      []service.IncomingFile{incoming},
		StorageUID: strings.TrimSpace(c.PostForm("strategy_id")),
		// Source 标记为 lsky，便于在操作日志里区分来源
		Source: model.UploadSourceLsky,
	})
	if err != nil {
		// EnqueueBatch 失败时已自行清理暂存文件（见其文档）
		Fail(c, serviceCodeOf(err))
		return
	}

	uploadUID, waitErr := h.waitForJob(c.Request.Context(), eventsCh, batch.JobUID, batch.Items)
	if waitErr != nil {
		// 超时/失败：如实告知，并说明「后台可能仍在跑，可用内部接口查任务」
		h.deps.Log.Warn("Lsky 同步上传未在期限内完成",
			"job_uid", batch.JobUID, "user_uid", viewer.UID, "err", waitErr)
		FailMsg(c, response.CodeTooMany, "Upload is still in progress. Please check the job list later.")
		return
	}

	up, err := h.deps.Gallery.Get(uploadUID, viewer)
	if err != nil {
		Fail(c, serviceCodeOf(err))
		return
	}

	OKMsg(c, "上传成功", toImageData(up))
}

// waitForJob 等待批次完成，返回**第一个成功项的 UploadUID**。
//
// 实现说明：Hub 的 `job.finished` 事件带 `JobUID`，但 Lsky 契约只需单文件
// （`file` 字段只有一个），因此取第一个成功的 item。
func (h *Handler) waitForJob(
	ctx context.Context,
	eventsCh <-chan events.Event,
	jobUID string,
	items []service.BatchItem,
) (string, error) {
	// 先检查是否已经完成（入队到订阅之间可能有间隙）
	if uid, done := h.pollJobOnce(jobUID); done {
		return uid, nil
	}

	timeout := time.NewTimer(syncUploadTimeout)
	defer timeout.Stop()

	// 兜底轮询：Hub 的 `job.finished` 可能在极短时间内发出而错过
	poll := time.NewTicker(500 * time.Millisecond)
	defer poll.Stop()

	for {
		select {
		case ev, ok := <-eventsCh:
			if !ok {
				return "", errors.New("事件通道已关闭")
			}
			if ev.Name != events.EventJobFinished {
				continue
			}
			payload, ok := ev.Data.(events.JobFinishedPayload)
			if !ok || payload.JobUID != jobUID {
				continue
			}
			return h.finishResult(jobUID, items)

		case <-poll.C:
			if uid, done := h.pollJobOnce(jobUID); done {
				return uid, nil
			}

		case <-timeout.C:
			return "", errors.New("等待上传完成超时")

		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// pollJobOnce 查一次该批次下是否已有成功项（**不查 Jobs 表**）。
//
// 为什么不查 job 状态：Lsky 只需「拿到 URL」，而 `Uploads.Status` 一旦变
// `success` 就说明该文件已经可用。只看图片状态少一次查询，也避免
// 「job 仍在跑但第一张已成功」时白白多等。
func (h *Handler) pollJobOnce(jobUID string) (string, bool) {
	uid, err := h.finishResult(jobUID, nil)
	return uid, err == nil
}

// finishResult 从 items 或 DB 里取出第一个成功项的 UploadUID。
func (h *Handler) finishResult(jobUID string, items []service.BatchItem) (string, error) {
	// 优先用入队时返回的 items（含 UploadUID 与最终状态）
	for _, it := range items {
		if it.Status == model.UploadStatusSuccess && it.UploadUID != "" {
			return it.UploadUID, nil
		}
	}
	// 入队时的状态可能还是 queued，此时回查 DB
	rows, err := h.deps.UploadRepo.ListByJob(jobUID)
	if err != nil {
		return "", err
	}
	for _, r := range rows {
		if r.Status == model.UploadStatusSuccess {
			return r.UID, nil
		}
	}
	return "", errors.New("上传未成功")
}

// ---------------------------------------------------------------------------
// 相册（伪造响应）
// ---------------------------------------------------------------------------
//
// 本项目已移除相册功能，但 `/api/v1/albums` 属于 Lsky 保留路径（conflict.go）。
// 为了让桌面端客户端（PicGo / PicList 会拉相册列表填充下拉框）不因 404 报错，
// 这里返回**伪造的契约响应**：列表恒为空、删除恒成功，不落库、不审计。

// ListAlbums 处理 `GET /api/v1/albums`：恒返回空列表。
func (h *Handler) ListAlbums(c *gin.Context) {
	OK(c, []any{})
}

// DeleteAlbum 处理 `DELETE /api/v1/albums/{id}`：恒返回成功（无实际对象可删）。
func (h *Handler) DeleteAlbum(c *gin.Context) {
	OKMsgOK(c, "删除成功")
}

// ---------------------------------------------------------------------------
// 助手
// ---------------------------------------------------------------------------

// OKMsgOK 是「成功 + 自定义消息」的快捷方式。
func OKMsgOK(c *gin.Context, message string) {
	c.JSON(200, Envelope{Status: true, Message: message, Data: nil})
}

// OKMsg 返回成功响应（带自定义消息）。
func OKMsg(c *gin.Context, message string, data any) {
	c.JSON(200, Envelope{Status: true, Message: message, Data: data})
}

// serviceCodeOf 提取 service 层的业务错误码；非 service 错误返回 50001。
func serviceCodeOf(err error) response.Code {
	if err == nil {
		return response.CodeOK
	}
	var se *service.Error
	if errors.As(err, &se) {
		return se.Code
	}
	return response.CodeInternal
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func boolPtr(b bool) *bool { return &b }
