package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/agent"
	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/events"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// GalleryService 负责图库的查询与整理（不改上传调度，那是 UploadService）。
//
// 职责边界：
//   - 列表 / 详情 / 重命名 / 删除 / 外链格式化 / 统计
//   - **权限判定**（D33 + D71）：普通用户只能碰自己的；管理员可碰全部，
//     但**默认落在 mine**（避免误操作他人图片）
type GalleryService struct {
	cfg      *config.Config
	log      *slog.Logger
	settings *settings.Service
	uploads  *repository.UploadRepo
	users    *repository.UserRepo
	storage  *StorageService
	ag       agent.Client
	hub      *events.Hub
	audit    *AuditService
}

// NewGalleryService 构造。
func NewGalleryService(
	cfg *config.Config,
	log *slog.Logger,
	settingsSvc *settings.Service,
	uploads *repository.UploadRepo,
	users *repository.UserRepo,
	storage *StorageService,
	ag agent.Client,
	hub *events.Hub,
	audit *AuditService,
) *GalleryService {
	return &GalleryService{
		cfg: cfg, log: log, settings: settingsSvc,
		uploads: uploads, users: users,
		storage: storage, ag: ag, hub: hub, audit: audit,
	}
}

// UploadView 是对外的图片对象（字段与 Uploads 表一一对应，另加两个只读辅助字段）。
type UploadView struct {
	UID        string `json:"UID"`
	UserUID    string `json:"UserUID"`
	StorageUID string `json:"StorageUID"`

	FileName     string `json:"FileName"`
	OriginalName string `json:"OriginalName"`
	AliasName    string `json:"AliasName"`

	Size      int64  `json:"Size"`
	MimeType  string `json:"MimeType"`
	Extension string `json:"Extension"`
	Width     int    `json:"Width"`
	Height    int    `json:"Height"`
	SHA256    string `json:"SHA256"`

	URL      string `json:"URL"`
	ThumbURL string `json:"ThumbURL"`

	Status string `json:"Status"`
	Error  string `json:"Error"`
	Source string `json:"Source"`
	JobUID string `json:"JobUID"`

	Metadata map[string]any `json:"Metadata"`

	// StorageName 便于前端直接展示存储名（免二次查询）。
	StorageName string `json:"StorageName"`
	// UserEmail **仅当 `Scope = all`** 时返回（管理员视图）。
	UserEmail string `json:"UserEmail,omitempty"`

	CreatedAt int64 `json:"CreatedAt"`
	UpdatedAt int64 `json:"UpdatedAt"`
}

// GalleryListInput 是图库列表入参。
type GalleryListInput struct {
	Keyword    string
	StorageUID string
	Status     string
	// Scope mine（默认）| all（仅管理员；普通用户会被**静默降级**为 mine）
	Scope string
	Sort  string
	Order string

	Page     int
	PageSize int
}

// List 分页查询图片。
//
// ⚠️ **Scope 的降级是「静默」的**（docs/API.md §4.2）：普通用户传 `all` 不会报错，
// 而是当作 `mine` 处理。理由：这不是攻击面（后端本就只给本人数据），
// 报错反而会让前端在「角色变化」时出现莫名其妙的失败。
func (s *GalleryService) List(in GalleryListInput, viewer *model.User) ([]UploadView, int64, error) {
	if viewer == nil {
		return nil, 0, Errorf(response.CodeUnauthorized, "未登录")
	}
	page, size := normalizePage(in.Page, in.PageSize)

	f := repository.UploadListFilter{
		Keyword:    in.Keyword,
		StorageUID: in.StorageUID,
		Status:     in.Status,
		Sort:       in.Sort,
		Order:      in.Order,
		Page:       page,
		PageSize:   size,
	}
	scope := strings.ToLower(strings.TrimSpace(in.Scope))
	if scope == "all" && viewer.IsAdmin() {
		// 管理员看全部：不按用户过滤
	} else {
		// 普通用户，或管理员选了「我的图片」
		f.UserUID = viewer.UID
		scope = "mine"
	}

	rows, total, err := s.uploads.List(f)
	if err != nil {
		return nil, 0, Wrap(response.CodeInternal, "查询图片失败", err)
	}

	views, err := s.toViews(rows, scope == "all")
	if err != nil {
		return nil, 0, err
	}
	return views, total, nil
}

// Get 取单张图片（含权限校验）。
func (s *GalleryService) Get(uid string, viewer *model.User) (*UploadView, error) {
	up, err := s.uploads.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "图片不存在")
	}
	if err := assertCanAccess(up, viewer); err != nil {
		return nil, err
	}
	views, err := s.toViews([]model.Upload{*up}, viewer.IsAdmin() && up.UserUID != viewer.UID)
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

// GalleryUpdateInput 是图片更新入参。
type GalleryUpdateInput struct {
	AliasName *string
}

// Update 重命名。
//
// ⚠️ 重命名只改 `AliasName`，**不改远端文件名**：远端 URL 由图床决定（D42/D66 边界），
// 我们改了本地字段却改不了远端，反而会造成「名字对不上」的困惑。
func (s *GalleryService) Update(ctx context.Context, uid string, in GalleryUpdateInput, viewer *model.User, clientIP, userAgent string) (*UploadView, error) {
	up, err := s.uploads.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "图片不存在")
	}
	if err := assertCanAccess(up, viewer); err != nil {
		return nil, err
	}

	fields := map[string]any{}
	detail := map[string]any{"action": "update"}

	if in.AliasName != nil {
		alias := strings.TrimSpace(*in.AliasName)
		if len(alias) > 255 {
			return nil, Errorf(response.CodeInvalidParam, "别名不能超过 255 字符")
		}
		fields["AliasName"] = alias
		detail["AliasName"] = alias
	}

	if len(fields) == 0 {
		return s.Get(uid, viewer)
	}

	if err := s.uploads.UpdateFields(uid, fields); err != nil {
		return nil, notFoundOr(err, "图片不存在")
	}

	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeImageUpdate, Status: model.LogStatusSuccess,
		UserUID: viewer.UID, TargetType: "upload", TargetUID: uid,
		Detail: detail, ClientIP: clientIP, UserAgent: userAgent,
	})
	return s.Get(uid, viewer)
}

// UploadDeleteResult 是删除单张图片的结果（docs/API.md §4.2）。
type UploadDeleteResult struct {
	Deleted               bool   `json:"Deleted"`
	RemoteDeleted         bool   `json:"RemoteDeleted"`
	RemoteDeleteSupported bool   `json:"RemoteDeleteSupported"`
	RemoteDeleteError     string `json:"RemoteDeleteError"`
	FreedBytes            int64  `json:"FreedBytes"`
}

// Delete 硬删除一张图片（D46），可选同步删除远端文件（D47），并退还配额（D72）。
//
// 流程严格按 docs/API.md §4.2：
//
//  1. 权限校验
//  2. DeleteRemote && 驱动支持 → 调 agent（走 remove 事件）
//  3. 硬删 Uploads + UploadResults
//  4. 退还配额（**与远端是否删成功无关**，D72）
//  5. 写 OperationLogs（Detail 含 RemoteDeleted 与失败原因）
//
// ⚠️ 远端删除失败**不阻断**本地删除：图床残留属预期（自用场景可接受），
// 而「删不掉记录」会让用户无法清理自己的图库。
func (s *GalleryService) Delete(ctx context.Context, uid string, deleteRemote bool, viewer *model.User, clientIP, userAgent string) (*UploadDeleteResult, error) {
	up, err := s.uploads.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "图片不存在")
	}
	if err := assertCanAccess(up, viewer); err != nil {
		return nil, err
	}

	result := &UploadDeleteResult{}

	// ---- 2. 远端删除 ----
	if deleteRemote {
		remoteResult, remoteErr := s.deleteRemote(ctx, up)
		switch {
		case remoteErr != nil:
			result.RemoteDeleteSupported = true
			result.RemoteDeleted = false
			result.RemoteDeleteError = MessageOf(remoteErr)
		case remoteResult != nil:
			result.RemoteDeleteSupported = remoteResult.Supported
			result.RemoteDeleted = remoteResult.RemoteDeleted
			if !remoteResult.RemoteDeleted {
				result.RemoteDeleteError = remoteResult.Message
			}
		}
	} else {
		// 未要求远端删除时，把驱动能力如实回报（前端据此显示「不支持」提示）
		result.RemoteDeleteSupported = s.storage.SupportsRemoteDeleteOf(up.StorageUID)
	}

	// ---- 3/4. 硬删除 + 退还配额（同事务）----
	freed, ownerUID, err := s.uploads.DeleteWithQuotaRefund(uid)
	if err != nil {
		return nil, notFoundOr(err, "图片不存在")
	}
	result.Deleted = true
	result.FreedBytes = freed

	// ---- 5. 审计 ----
	detail := map[string]any{
		"FileName":        up.DisplayName(),
		"URL":             up.URL,
		"StorageUID":      up.StorageUID,
		"RequestedRemote": deleteRemote,
		"RemoteDeleted":   result.RemoteDeleted,
		"RemoteSupported": result.RemoteDeleteSupported,
		"FreedBytes":      freed,
	}
	if result.RemoteDeleteError != "" {
		detail["RemoteDeleteError"] = result.RemoteDeleteError
	}

	status := model.LogStatusSuccess
	if deleteRemote && !result.RemoteDeleted && result.RemoteDeleteSupported {
		// 远端删失败：本地删成功，但记为 failed 以便管理员追查图床残留
		status = model.LogStatusFailed
	}
	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeImageDelete, Status: status,
		UserUID: viewer.UID, TargetType: "upload", TargetUID: uid,
		Detail: detail,
		// 成功的本地删除不填 Cause；远端失败时把原因带上便于检索
		Cause:    causeIf(status == model.LogStatusFailed, result.RemoteDeleteError),
		ClientIP: clientIP, UserAgent: userAgent,
	})

	// 配额变化后前端要刷新用量：借 system.notice 提示（不区分用户，避免暴露他人信息）
	s.hub.PublishNotice("info", fmt.Sprintf("图片已删除，释放 %d 字节", freed))
	_ = ownerUID // ownerUID 已在事务内用于退款；此处仅保留可读性
	return result, nil
}

// BatchDeleteResult 是批量删除结果。
type BatchDeleteResult struct {
	Total              int               `json:"Total"`
	Deleted            int               `json:"Deleted"`
	Skipped            int               `json:"Skipped"`
	FailedRemoteDelete int               `json:"FailedRemoteDelete"`
	FreedBytes         int64             `json:"FreedBytes"`
	Items              []BatchDeleteItem `json:"Items"`
}

// BatchDeleteItem 是批量删除中单条的结果。
type BatchDeleteItem struct {
	UID           string `json:"UID"`
	Deleted       bool   `json:"Deleted"`
	RemoteDeleted bool   `json:"RemoteDeleted"`
	Error         string `json:"Error"`
}

// maxBatchDelete 单次批量删除上限（docs/API.md §4.2）。
const maxBatchDelete = 200

// BatchDelete 批量删除（逐条独立处理，部分失败不整体回滚）。
func (s *GalleryService) BatchDelete(ctx context.Context, uids []string, deleteRemote bool, viewer *model.User, clientIP, userAgent string) (*BatchDeleteResult, error) {
	if len(uids) == 0 {
		return nil, Errorf(response.CodeInvalidParam, "未指定要删除的图片")
	}
	if len(uids) > maxBatchDelete {
		return nil, Errorf(response.CodeInvalidParam, "单次最多删除 %d 张图片", maxBatchDelete)
	}

	out := &BatchDeleteResult{
		Total: len(uids),
		Items: make([]BatchDeleteItem, 0, len(uids)),
	}

	for _, uid := range uids {
		item := BatchDeleteItem{UID: uid}

		res, err := s.Delete(ctx, uid, deleteRemote, viewer, clientIP, userAgent)
		if err != nil {
			item.Deleted = false
			item.Error = MessageOf(err)
			out.Skipped++
			out.Items = append(out.Items, item)
			continue
		}

		item.Deleted = res.Deleted
		item.RemoteDeleted = res.RemoteDeleted
		if res.RemoteDeleteError != "" {
			item.Error = res.RemoteDeleteError
			out.FailedRemoteDelete++
		}
		out.Deleted++
		out.FreedBytes += res.FreedBytes
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// deleteRemote 调 agent 做远端删除。
//
// ⚠️ 需要 `UploadResults.RawOutput`（上传时的完整 picgo IImgInfo）：
// 插件（如 github-plus）依赖它里面回写的 `sha` 等字段才能删除。
// 没有它 → **无法删除**，只能如实告诉用户。
func (s *GalleryService) deleteRemote(ctx context.Context, up *model.Upload) (*agent.RemoveData, error) {
	res, err := s.uploads.GetResult(up.UID)
	if err != nil || res == nil || strings.TrimSpace(res.RawOutput) == "" {
		return &agent.RemoveData{
			Supported:     false,
			RemoteDeleted: false,
			Message:       "缺少上传时的原始返回值，无法删除远端文件（可能是历史数据）",
		}, nil
	}

	// RawOutput 的字段名是 picgo 原生的（fileName / imgUrl / sha …），必须原样交回
	var raw []agent.RawImgInfo
	if err := json.Unmarshal([]byte(res.RawOutput), &raw); err != nil {
		return &agent.RemoveData{
			Supported:     false,
			RemoteDeleted: false,
			Message:       "原始返回值格式损坏，无法删除远端文件",
		}, nil
	}
	if len(raw) == 0 {
		return &agent.RemoveData{
			Supported:     false,
			RemoteDeleted: false,
			Message:       "原始返回值为空，无法删除远端文件",
		}, nil
	}

	storageRow, err := s.storage.FindByUID(up.StorageUID)
	if err != nil {
		return nil, notFoundOr(err, "存储配置不存在")
	}

	data, err := s.ag.DeleteRemote(ctx, agent.RemoveRequest{
		UploaderType: storageRow.Type,
		Items:        raw,
	})
	if err != nil {
		return nil, fromAgent(err)
	}
	return data, nil
}

// ---------------------------------------------------------------------------
// 外链格式化（D68）
// ---------------------------------------------------------------------------

// 支持的格式。
const (
	LinkFormatURL      = "url"
	LinkFormatMarkdown = "markdown"
	LinkFormatHTML     = "html"
)

// LinkResult 是外链结果。
type LinkResult struct {
	UID    string `json:"UID"`
	Format string `json:"Format"`
	Text   string `json:"Text"`
	URL    string `json:"URL"`
}

// Link 生成单张图片的外链。
func (s *GalleryService) Link(uid, format string, viewer *model.User) (*LinkResult, error) {
	up, err := s.uploads.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "图片不存在")
	}
	if err := assertCanAccess(up, viewer); err != nil {
		return nil, err
	}
	f := normalizeLinkFormat(format)
	return &LinkResult{
		UID:    uid,
		Format: f,
		Text:   formatLink(f, up.DisplayName(), up.URL),
		URL:    up.URL,
	}, nil
}

// BatchLinkResult 是批量外链结果。
type BatchLinkResult struct {
	Format string          `json:"Format"`
	Text   string          `json:"Text"`
	Items  []BatchLinkItem `json:"Items"`
}

// BatchLinkItem 是批量外链中的单项。
type BatchLinkItem struct {
	UID  string `json:"UID"`
	URL  string `json:"URL"`
	Text string `json:"Text"`
}

// BatchLink 批量生成外链（D68：支持多选后一次性复制，多行拼接）。
//
// 权限不足的项**静默跳过**（而不是整体失败）：多选里可能混入不属于自己的图片，
// 直接失败会让用户无法复制其余项。
func (s *GalleryService) BatchLink(uids []string, format string, viewer *model.User) (*BatchLinkResult, error) {
	if len(uids) == 0 {
		return nil, Errorf(response.CodeInvalidParam, "未指定图片")
	}
	if len(uids) > maxBatchDelete {
		return nil, Errorf(response.CodeInvalidParam, "单次最多处理 %d 张图片", maxBatchDelete)
	}

	f := normalizeLinkFormat(format)
	out := &BatchLinkResult{Format: f, Items: make([]BatchLinkItem, 0, len(uids))}
	lines := make([]string, 0, len(uids))

	for _, uid := range uids {
		up, err := s.uploads.FindByUID(uid)
		if err != nil {
			continue
		}
		if assertCanAccess(up, viewer) != nil {
			continue
		}
		text := formatLink(f, up.DisplayName(), up.URL)
		out.Items = append(out.Items, BatchLinkItem{UID: uid, URL: up.URL, Text: text})
		lines = append(lines, text)
	}
	out.Text = strings.Join(lines, "\n")
	return out, nil
}

// normalizeLinkFormat 归一化格式名（未知值回退 markdown）。
func normalizeLinkFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case LinkFormatURL, "plain", "direct":
		return LinkFormatURL
	case LinkFormatHTML:
		return LinkFormatHTML
	default:
		return LinkFormatMarkdown
	}
}

// formatLink 按格式生成文本（D68 的三种格式）。
func formatLink(format, name, url string) string {
	switch format {
	case LinkFormatURL:
		return url
	case LinkFormatHTML:
		return fmt.Sprintf(`<img src="%s" alt="%s" />`, url, escapeHTMLAttr(name))
	default:
		return fmt.Sprintf("![%s](%s)", escapeMarkdownText(name), url)
	}
}

// ---------------------------------------------------------------------------
// 统计
// ---------------------------------------------------------------------------

// GalleryStats 是图库统计（docs/API.md §4.2）。
type GalleryStats struct {
	Scope        string              `json:"Scope"`
	Total        int64               `json:"Total"`
	TotalSize    int64               `json:"TotalSize"`
	SuccessCount int64               `json:"SuccessCount"`
	FailedCount  int64               `json:"FailedCount"`
	PendingCount int64               `json:"PendingCount"`
	TodayCount   int64               `json:"TodayCount"`
	WeekCount    int64               `json:"WeekCount"`
	ByStorage    []StorageStatItem   `json:"ByStorage"`
	ByExtension  []ExtensionStatItem `json:"ByExtension"`
}

// StorageStatItem 是按存储的统计项。
type StorageStatItem struct {
	StorageUID string `json:"StorageUID"`
	Name       string `json:"Name"`
	Count      int64  `json:"Count"`
}

// ExtensionStatItem 是按扩展名的统计项。
type ExtensionStatItem struct {
	Extension string `json:"Extension"`
	Count     int64  `json:"Count"`
}

// Stats 返回图库统计。
func (s *GalleryService) Stats(scope string, viewer *model.User) (*GalleryStats, error) {
	if viewer == nil {
		return nil, Errorf(response.CodeUnauthorized, "未登录")
	}

	all := strings.EqualFold(strings.TrimSpace(scope), "all") && viewer.IsAdmin()
	userUID := viewer.UID
	if all {
		userUID = "" // 空 = 全站
	}

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	// 本周从周一算起（中国习惯）
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	weekStart := todayStart - int64(weekday-1)*86400

	stats, err := s.uploads.Stats(userUID, todayStart, weekStart)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "统计图库失败", err)
	}

	out := &GalleryStats{
		Scope:        pickScope(all),
		Total:        stats.Total,
		TotalSize:    stats.TotalSize,
		SuccessCount: stats.SuccessCount,
		FailedCount:  stats.FailedCount,
		PendingCount: stats.PendingCount,
		TodayCount:   stats.TodayCount,
		WeekCount:    stats.WeekCount,
		ByStorage:    []StorageStatItem{},
		ByExtension:  []ExtensionStatItem{},
	}

	nameMap := s.storage.NameMap()
	if rows, err := s.uploads.CountByStorage(userUID); err == nil {
		for _, r := range rows {
			name := nameMap[r.StorageUID]
			if name == "" {
				name = r.StorageUID
			}
			out.ByStorage = append(out.ByStorage, StorageStatItem{
				StorageUID: r.StorageUID, Name: name, Count: r.Cnt,
			})
		}
	}
	if rows, err := s.uploads.CountByExtension(userUID); err == nil {
		for _, r := range rows {
			out.ByExtension = append(out.ByExtension, ExtensionStatItem{
				Extension: r.Extension, Count: r.Cnt,
			})
		}
	}
	return out, nil
}

func pickScope(all bool) string {
	if all {
		return "all"
	}
	return "mine"
}

// ---------------------------------------------------------------------------
// 内部
// ---------------------------------------------------------------------------

// toViews 批量转视图（一次性解析存储名与用户邮箱，避免 N+1）。
func (s *GalleryService) toViews(rows []model.Upload, withUser bool) ([]UploadView, error) {
	if len(rows) == 0 {
		return []UploadView{}, nil
	}

	nameMap := s.storage.NameMap()

	// 用户邮箱（仅 Scope=all 时返回）
	userEmails := map[string]string{}
	if withUser {
		uids := make([]string, 0, len(rows))
		for _, r := range rows {
			uids = append(uids, r.UserUID)
		}
		if list, err := s.users.FindManyByUIDs(uids); err == nil {
			for _, u := range list {
				userEmails[u.UID] = u.Email
			}
		}
	}

	out := make([]UploadView, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		v := UploadView{
			UID: r.UID, UserUID: r.UserUID, StorageUID: r.StorageUID,
			FileName: r.FileName, OriginalName: r.OriginalName, AliasName: r.AliasName,
			Size: r.Size, MimeType: r.MimeType, Extension: r.Extension,
			Width: r.Width, Height: r.Height, SHA256: r.SHA256,
			URL: r.URL, ThumbURL: r.ThumbURL,
			Status: r.Status, Error: r.Error, Source: r.Source, JobUID: r.JobUID,
			Metadata:    parseJSONMap(r.Metadata),
			StorageName: nameMap[r.StorageUID],
			CreatedAt:   r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}
		if withUser {
			v.UserEmail = userEmails[r.UserUID]
		}
		out = append(out, v)
	}
	return out, nil
}

// assertCanAccess 判断 viewer 是否能操作该图片（D33：各自私有；管理员全权）。
func assertCanAccess(up *model.Upload, viewer *model.User) error {
	if viewer == nil {
		return Errorf(response.CodeUnauthorized, "未登录")
	}
	if viewer.IsAdmin() {
		return nil
	}
	if up.UserUID != viewer.UID {
		return Errorf(response.CodeForbidden, "无权操作他人的图片")
	}
	return nil
}

func causeIf(cond bool, msg string) error {
	if !cond || strings.TrimSpace(msg) == "" {
		return nil
	}
	return fmt.Errorf("%s", msg)
}

// escapeMarkdownText 转义 Markdown 链接文本里的方括号（否则会破坏语法）。
func escapeMarkdownText(s string) string {
	return strings.NewReplacer(`[`, `\[`, `]`, `\]`).Replace(s)
}

// escapeHTMLAttr 转义 HTML 属性里的引号与尖括号（防注入）。
func escapeHTMLAttr(s string) string {
	return strings.NewReplacer(
		`&`, `&amp;`, `"`, `&quot;`, `<`, `&lt;`, `>`, `&gt;`,
	).Replace(s)
}
