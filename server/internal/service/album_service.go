package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/YeqingKy/PicGo-Web/server/internal/id"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// AlbumService 管理相册（D55：本项目唯一的整理维度）。
//
// ⚠️ `ImageCount` 是**冗余计数**，维护方式是「移动/删除后**重算**」而不是 ±1：
// 重算天然抗漂移（docs/OPERATIONS.md §3.5），代价只是一次带索引的 COUNT。
type AlbumService struct {
	log     *slog.Logger
	albums  *repository.AlbumRepo
	uploads *repository.UploadRepo
	audit   *AuditService
}

// NewAlbumService 构造。
func NewAlbumService(
	log *slog.Logger,
	albums *repository.AlbumRepo,
	uploads *repository.UploadRepo,
	audit *AuditService,
) *AlbumService {
	return &AlbumService{log: log, albums: albums, uploads: uploads, audit: audit}
}

// AlbumView 是对外的相册对象。
type AlbumView struct {
	UID       string `json:"UID"`
	UserUID   string `json:"UserUID"`
	ParentUID string `json:"ParentUID"`
	Name      string `json:"Name"`
	Intro     string `json:"Intro"`

	CoverUploadUID string `json:"CoverUploadUID"`
	// CoverURL 由 CoverUploadUID 解析出的直链，便于前端展示（封面被删时为空）。
	CoverURL string `json:"CoverURL"`

	ImageCount int64 `json:"ImageCount"`
	SortOrder  int   `json:"SortOrder"`

	Metadata  map[string]any `json:"Metadata"`
	CreatedAt int64          `json:"CreatedAt"`
	UpdatedAt int64          `json:"UpdatedAt"`
}

// AlbumListInput 是相册列表入参。
type AlbumListInput struct {
	Keyword string
	Sort    string
	Order   string
}

// List 返回当前用户的相册（不分页：相册数量天然有限）。
func (s *AlbumService) List(in AlbumListInput, viewer *model.User) ([]AlbumView, error) {
	if viewer == nil {
		return nil, Errorf(response.CodeUnauthorized, "未登录")
	}

	// 管理员看自己的相册（相册是「个人整理工具」，没有「看全部相册」的需求；
	// 若要管理他人图片，用图库的 Scope=all）
	rows, err := s.albums.List(repository.AlbumListFilter{
		UserUID: viewer.UID,
		Keyword: in.Keyword,
		Sort:    in.Sort,
		Order:   in.Order,
	})
	if err != nil {
		return nil, Wrap(response.CodeInternal, "查询相册失败", err)
	}

	out := make([]AlbumView, 0, len(rows))
	for i := range rows {
		out = append(out, s.toView(&rows[i]))
	}
	return out, nil
}

// Get 取单个相册。
func (s *AlbumService) Get(uid string, viewer *model.User) (*AlbumView, error) {
	a, err := s.albums.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "相册不存在")
	}
	if err := assertAlbumAccess(a, viewer); err != nil {
		return nil, err
	}
	v := s.toView(a)
	return &v, nil
}

// CreateAlbumInput 是新建相册入参。
type CreateAlbumInput struct {
	Name  string
	Intro string
}

// Create 新建相册。
func (s *AlbumService) Create(ctx context.Context, in CreateAlbumInput, viewer *model.User, clientIP, userAgent string) (*AlbumView, error) {
	if viewer == nil {
		return nil, Errorf(response.CodeUnauthorized, "未登录")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, Errorf(response.CodeInvalidParam, "相册名不能为空")
	}
	if len([]rune(name)) > 128 {
		return nil, Errorf(response.CodeInvalidParam, "相册名不能超过 128 字符")
	}

	exists, err := s.albums.NameExists(viewer.UID, name, "")
	if err != nil {
		return nil, Wrap(response.CodeInternal, "校验相册名失败", err)
	}
	if exists {
		return nil, Errorf(response.CodeConflict, "已存在名为「%s」的相册", name)
	}

	row := &model.Album{
		UID:       id.Album(),
		UserUID:   viewer.UID,
		ParentUID: "", // 预留字段：当前恒为空（DATA-MODEL §4.3）
		Name:      name,
		Intro:     strings.TrimSpace(in.Intro),
		Metadata:  "{}",
		CreatedAt: model.Now(),
		UpdatedAt: model.Now(),
	}
	if err := s.albums.Create(row); err != nil {
		// 并发下可能撞唯一索引，统一按冲突返回
		return nil, Errorf(response.CodeConflict, "创建相册失败（可能重名）")
	}

	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeImageUpdate, Status: model.LogStatusSuccess,
		UserUID: viewer.UID, TargetType: "album", TargetUID: row.UID,
		Detail:   map[string]any{"action": "album_create", "Name": name},
		ClientIP: clientIP, UserAgent: userAgent,
	})

	v := s.toView(row)
	return &v, nil
}

// UpdateAlbumInput 是更新相册入参。
type UpdateAlbumInput struct {
	Name           *string
	Intro          *string
	CoverUploadUID *string
	SortOrder      *int
}

// Update 更新相册。
//
// `CoverUploadUID` 必须属于同一用户且 `Status = success`，否则 40001（docs/API.md §5）。
func (s *AlbumService) Update(ctx context.Context, uid string, in UpdateAlbumInput, viewer *model.User, clientIP, userAgent string) (*AlbumView, error) {
	a, err := s.albums.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "相册不存在")
	}
	if err := assertAlbumAccess(a, viewer); err != nil {
		return nil, err
	}

	fields := map[string]any{}

	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, Errorf(response.CodeInvalidParam, "相册名不能为空")
		}
		if name != a.Name {
			exists, err := s.albums.NameExists(a.UserUID, name, uid)
			if err != nil {
				return nil, Wrap(response.CodeInternal, "校验相册名失败", err)
			}
			if exists {
				return nil, Errorf(response.CodeConflict, "已存在名为「%s」的相册", name)
			}
		}
		fields["Name"] = name
	}
	if in.Intro != nil {
		fields["Intro"] = strings.TrimSpace(*in.Intro)
	}
	if in.SortOrder != nil {
		fields["SortOrder"] = *in.SortOrder
	}
	if in.CoverUploadUID != nil {
		cover := strings.TrimSpace(*in.CoverUploadUID)
		if cover == "" {
			fields["CoverUploadUID"] = ""
		} else {
			up, err := s.uploads.FindByUID(cover)
			if err != nil {
				return nil, Errorf(response.CodeInvalidParam, "封面图片不存在")
			}
			if up.UserUID != a.UserUID {
				return nil, Errorf(response.CodeInvalidParam, "封面图片必须属于相册所有者")
			}
			if up.Status != model.UploadStatusSuccess {
				return nil, Errorf(response.CodeInvalidParam, "封面图片尚未上传成功")
			}
			fields["CoverUploadUID"] = cover
		}
	}

	if len(fields) == 0 {
		v := s.toView(a)
		return &v, nil
	}

	if err := s.albums.UpdateFields(uid, fields); err != nil {
		return nil, notFoundOr(err, "相册不存在")
	}

	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeImageUpdate, Status: model.LogStatusSuccess,
		UserUID: viewer.UID, TargetType: "album", TargetUID: uid,
		Detail:   map[string]any{"action": "album_update", "ChangedFields": keysOf(fields)},
		ClientIP: clientIP, UserAgent: userAgent,
	})

	return s.Get(uid, viewer)
}

// AlbumDeleteResult 是删除相册的结果。
type AlbumDeleteResult struct {
	Deleted         bool  `json:"Deleted"`
	DetachedUploads int64 `json:"DetachedUploads"`
}

// Delete 删除相册。
//
//   - 相册内有图片且 `withUploads = false` → 40901（`Message` 含图片数）
//   - `withUploads = true` → 相册内图片**仅脱离相册**（`AlbumUID` 置空），**不删除图片**
func (s *AlbumService) Delete(ctx context.Context, uid string, withUploads bool, viewer *model.User, clientIP, userAgent string) (*AlbumDeleteResult, error) {
	a, err := s.albums.FindByUID(uid)
	if err != nil {
		return nil, notFoundOr(err, "相册不存在")
	}
	if err := assertAlbumAccess(a, viewer); err != nil {
		return nil, err
	}

	count, err := s.albums.CountImages(uid)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "统计相册图片数失败", err)
	}
	if count > 0 && !withUploads {
		return nil, Errorf(response.CodeConflict,
			"相册「%s」中还有 %d 张图片，请先移出图片或使用 WithUploads=true 删除", a.Name, count)
	}

	result := &AlbumDeleteResult{}
	if count > 0 {
		// 只脱离，不删图片（D46 的硬删除只针对图片本身）
		n, err := s.albums.DetachUploads(uid)
		if err != nil {
			return nil, Wrap(response.CodeInternal, "移出相册内图片失败", err)
		}
		result.DetachedUploads = n
	}

	if err := s.albums.Delete(uid); err != nil {
		return nil, notFoundOr(err, "相册不存在")
	}
	result.Deleted = true

	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeImageUpdate, Status: model.LogStatusSuccess,
		UserUID: viewer.UID, TargetType: "album", TargetUID: uid,
		Detail: map[string]any{
			"action": "album_delete", "Name": a.Name,
			"WithUploads": withUploads, "DetachedUploads": result.DetachedUploads,
		},
		ClientIP: clientIP, UserAgent: userAgent,
	})
	return result, nil
}

// AlbumMoveResult 是移动图片的结果。
type AlbumMoveResult struct {
	Moved   int64 `json:"Moved"`
	Skipped int64 `json:"Skipped"`
}

// MoveUploads 把若干图片移动到目标相册（`targetAlbumUID` 为空 = 移出相册）。
//
// 只能移动自己的图片（管理员可移动任意图片）。
func (s *AlbumService) MoveUploads(ctx context.Context, uploadUIDs []string, targetAlbumUID string, viewer *model.User, clientIP, userAgent string) (*AlbumMoveResult, error) {
	if viewer == nil {
		return nil, Errorf(response.CodeUnauthorized, "未登录")
	}
	if len(uploadUIDs) == 0 {
		return nil, Errorf(response.CodeInvalidParam, "未指定要移动的图片")
	}
	if len(uploadUIDs) > maxBatchDelete {
		return nil, Errorf(response.CodeInvalidParam, "单次最多移动 %d 张图片", maxBatchDelete)
	}

	target := strings.TrimSpace(targetAlbumUID)
	if target != "" {
		a, err := s.albums.FindByUID(target)
		if err != nil {
			return nil, notFoundOr(err, "目标相册不存在")
		}
		if err := assertAlbumAccess(a, viewer); err != nil {
			return nil, err
		}
	}

	// 权限过滤：只移动查看者有权操作的图片（逐条跳过而不是整体失败）
	rows, err := s.uploads.FindManyByUIDs(uploadUIDs)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "查询图片失败", err)
	}

	allowed := make([]string, 0, len(rows))
	skipped := int64(0)
	for i := range rows {
		if assertCanAccess(&rows[i], viewer) != nil {
			skipped++
			continue
		}
		allowed = append(allowed, rows[i].UID)
	}
	// 请求里指向不存在图片的 UID 也算 skipped
	skipped += int64(len(uploadUIDs) - len(rows))

	if len(allowed) == 0 {
		return &AlbumMoveResult{Moved: 0, Skipped: skipped}, nil
	}

	moved, err := s.albums.MoveUploads(allowed, target)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "移动图片失败", err)
	}

	s.audit.Log(ctx, AuditEntry{
		Type: model.LogTypeImageUpdate, Status: model.LogStatusSuccess,
		UserUID: viewer.UID, TargetType: "album", TargetUID: target,
		Detail: map[string]any{
			"action": "move", "Moved": moved, "Skipped": skipped,
			"TargetAlbumUID": target,
		},
		ClientIP: clientIP, UserAgent: userAgent,
	})

	return &AlbumMoveResult{Moved: moved, Skipped: skipped}, nil
}

// ---------------------------------------------------------------------------
// 内部
// ---------------------------------------------------------------------------

func (s *AlbumService) toView(a *model.Album) AlbumView {
	coverURL, err := s.albums.CoverURL(a.CoverUploadUID)
	if err != nil {
		s.log.Debug("解析相册封面失败", "album", a.UID, "err", err)
	}
	return AlbumView{
		UID: a.UID, UserUID: a.UserUID, ParentUID: a.ParentUID,
		Name: a.Name, Intro: a.Intro,
		CoverUploadUID: a.CoverUploadUID, CoverURL: coverURL,
		ImageCount: a.ImageCount, SortOrder: a.SortOrder,
		Metadata:  parseJSONMap(a.Metadata),
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
}

// assertAlbumAccess 判断 viewer 是否能操作该相册。
func assertAlbumAccess(a *model.Album, viewer *model.User) error {
	if viewer == nil {
		return Errorf(response.CodeUnauthorized, "未登录")
	}
	if viewer.IsAdmin() {
		return nil
	}
	if a.UserUID != viewer.UID {
		return Errorf(response.CodeForbidden, "无权操作他人的相册")
	}
	return nil
}
