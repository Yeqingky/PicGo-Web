package repository

import (
	"strings"

	"gorm.io/gorm"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// AlbumRepo 是 Albums 的数据访问。
//
// ⚠️ `ImageCount` 是**冗余计数**：图片移动/删除时必须同步维护。
// 维护逻辑集中在 AlbumService，且**都在事务内**（避免出现「相册显示 5 张，
// 实际 3 张」这类需要人工对账的漂移）。
type AlbumRepo struct{ db *gorm.DB }

// NewAlbumRepo 构造。
func NewAlbumRepo(db *gorm.DB) *AlbumRepo { return &AlbumRepo{db: db} }

// AlbumListFilter 是相册列表的查询条件。
type AlbumListFilter struct {
	UserUID string
	Keyword string
	Sort    string // SortOrder | CreatedAt | ImageCount
	Order   string
}

// List 查询相册（不分页：相册数量天然有限，见 docs/API.md §5）。
func (r *AlbumRepo) List(f AlbumListFilter) ([]model.Album, error) {
	q := r.db.Model(&model.Album{})
	if f.UserUID != "" {
		q = q.Where(map[string]any{"UserUID": f.UserUID})
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := "%" + escapeLike(kw) + "%"
		q = q.Where(`(`+col("Name")+` LIKE ? ESCAPE '\' OR `+col("Intro")+` LIKE ? ESCAPE '\')`, like, like)
	}

	order := col("SortOrder")
	switch strings.ToLower(strings.TrimSpace(f.Sort)) {
	case "createdat":
		order = col("CreatedAt")
	case "imagecount":
		order = col("ImageCount")
	case "sortorder", "":
		order = col("SortOrder")
	}
	if strings.EqualFold(strings.TrimSpace(f.Order), "desc") {
		order += " DESC"
	} else {
		order += " ASC"
	}
	// 次级排序保证稳定（SortOrder 大量为 0 时顺序不会随机）
	order += ", " + col("CreatedAt") + " ASC"

	var out []model.Album
	if err := q.Order(order).Find(&out).Error; err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// FindByUID 按 UID 查；不存在返回 ErrNotFound。
func (r *AlbumRepo) FindByUID(uid string) (*model.Album, error) {
	var a model.Album
	if err := r.db.Where(map[string]any{"UID": uid}).First(&a).Error; err != nil {
		return nil, wrap(err)
	}
	return &a, nil
}

// NameExists 判断同用户下相册名是否已存在（唯一约束 `(UserUID, Name)`）。
func (r *AlbumRepo) NameExists(userUID, name, excludeUID string) (bool, error) {
	q := r.db.Model(&model.Album{}).
		Where(map[string]any{"UserUID": userUID}).
		Where(`LOWER(`+col("Name")+`) = LOWER(?)`, strings.TrimSpace(name))
	if excludeUID != "" {
		q = q.Where(col("UID")+" <> ?", excludeUID)
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return false, wrap(err)
	}
	return n > 0, nil
}

// Create 插入相册。
func (r *AlbumRepo) Create(a *model.Album) error { return wrap(r.db.Create(a).Error) }

// UpdateFields 更新指定列。
func (r *AlbumRepo) UpdateFields(uid string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	fields["UpdatedAt"] = model.Now()
	res := r.db.Model(&model.Album{}).Where(map[string]any{"UID": uid}).Updates(fields)
	if res.Error != nil {
		return wrap(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete 删除相册。调用方需先确认相册内无图片（或用 Detach 先脱离）。
func (r *AlbumRepo) Delete(uid string) error {
	res := r.db.Where(map[string]any{"UID": uid}).Delete(&model.Album{})
	if res.Error != nil {
		return wrap(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DetachUploads 把相册内的图片全部脱离相册（`AlbumUID` 置空），返回受影响行数。
//
// 用途：`DELETE /albums/{Uid}?WithUploads=true` —— **只脱离，不删除图片**（D46 硬删除仅针对图片本身）。
func (r *AlbumRepo) DetachUploads(albumUID string) (int64, error) {
	res := r.db.Model(&model.Upload{}).
		Where(map[string]any{"AlbumUID": albumUID}).
		Updates(map[string]any{"AlbumUID": "", "UpdatedAt": model.Now()})
	if res.Error != nil {
		return 0, wrap(res.Error)
	}
	return res.RowsAffected, nil
}

// MoveUploads 批量把若干图片移到目标相册（目标为空 = 移出相册）。
//
// ⚠️ 同时维护**源相册与目标相册**的 ImageCount（单事务）。
// 用「先查实际受影响的行」而不是「按请求条数」计数：请求里可能含有
// 不属于该用户 / 已在目标相册的图片，必须按实际变更算。
func (r *AlbumRepo) MoveUploads(uploadUIDs []string, targetAlbumUID string) (moved int64, err error) {
	if len(uploadUIDs) == 0 {
		return 0, nil
	}

	err = r.db.Transaction(func(tx *gorm.DB) error {
		// 1. 查出这些图片当前的归属（用于计算源相册的计数变化）
		var before []model.Upload
		if e := tx.Where(col("UID")+" IN ?", uploadUIDs).Find(&before).Error; e != nil {
			return e
		}

		sourceCounts := map[string]int64{}
		for _, u := range before {
			if u.AlbumUID != "" && u.AlbumUID != targetAlbumUID {
				sourceCounts[u.AlbumUID]++
			}
		}

		// 2. 执行移动（只更新 AlbumUID 不同的那些，避免把「已在目标相册」也算作 moved）
		res := tx.Model(&model.Upload{}).
			Where(col("UID")+" IN ?", uploadUIDs).
			Where(`NOT (`+col("AlbumUID")+` = ? AND ? <> '')`, targetAlbumUID, targetAlbumUID).
			Updates(map[string]any{"AlbumUID": targetAlbumUID, "UpdatedAt": model.Now()})
		if res.Error != nil {
			return res.Error
		}
		if targetAlbumUID != "" {
			moved = res.RowsAffected
		} else {
			// 移出相册时也以实际变更行数为准
			moved = res.RowsAffected
		}

		// 3. 重算受影响相册的冗余计数（直接重算而不是 ±1，天然抗漂移）
		affected := make([]string, 0, len(sourceCounts)+1)
		for uid := range sourceCounts {
			affected = append(affected, uid)
		}
		if targetAlbumUID != "" {
			affected = append(affected, targetAlbumUID)
		}
		for _, albumUID := range affected {
			if err := recountAlbum(tx, albumUID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, wrap(err)
	}
	return moved, nil
}

// RecountImages 重算某相册的 ImageCount（导出的对账入口，供运维/测试使用）。
func (r *AlbumRepo) RecountImages(albumUID string) error {
	return wrap(recountAlbum(r.db, albumUID))
}

// recountAlbum 用一条 COUNT 重算 ImageCount（比 ±1 更抗漂移）。
func recountAlbum(tx *gorm.DB, albumUID string) error {
	if albumUID == "" {
		return nil
	}
	var n int64
	if err := tx.Model(&model.Upload{}).
		Where(map[string]any{"AlbumUID": albumUID}).Count(&n).Error; err != nil {
		return err
	}
	return tx.Model(&model.Album{}).
		Where(map[string]any{"UID": albumUID}).
		Updates(map[string]any{"ImageCount": n, "UpdatedAt": model.Now()}).Error
}

// CountImages 返回相册内图片数（用于「有图片则拒绝删除」）。
func (r *AlbumRepo) CountImages(albumUID string) (int64, error) {
	var n int64
	err := r.db.Model(&model.Upload{}).Where(map[string]any{"AlbumUID": albumUID}).Count(&n).Error
	if err != nil {
		return 0, wrap(err)
	}
	return n, nil
}

// CoverURL 由 CoverUploadUID 解析出直链（供前端展示封面）。
func (r *AlbumRepo) CoverURL(coverUploadUID string) (string, error) {
	if strings.TrimSpace(coverUploadUID) == "" {
		return "", nil
	}
	var u model.Upload
	err := r.db.Select(col("URL")).Where(map[string]any{"UID": coverUploadUID}).First(&u).Error
	if err != nil {
		// 封面图被删了不是错误：返回空字符串即可（前端显示占位）
		return "", nil
	}
	return u.URL, nil
}
