package repository

import (
	"errors"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// UploadRepo 是 Uploads / UploadResults 的数据访问。
//
// 两张表同属「一张图片」的两种信息（热点元数据 vs 大体量原始返回值，D78 拆表），
// 且**删除时必须成对**（否则留下无主的大 JSON），因此放同一个 repo。
type UploadRepo struct{ db *gorm.DB }

// NewUploadRepo 构造。
func NewUploadRepo(db *gorm.DB) *UploadRepo { return &UploadRepo{db: db} }

// UploadListFilter 是图库列表的查询条件。
//
// Scope 的语义见 docs/API.md §4.2（D33 + D71）：
// 普通用户只能看自己的；管理员可看全部，但**默认落在 mine**（避免误操作他人图片）。
type UploadListFilter struct {
	// UserUID 非空时只返回该用户的图片（`Scope = mine`）。
	// 为空表示不按用户过滤（仅管理员可用，`Scope = all`）。
	UserUID string

	Keyword    string
	StorageUID string
	Status     string

	Sort     string // CreatedAt | Size | FileName
	Order    string // asc | desc
	Page     int
	PageSize int
}

// List 分页查询图片。
func (r *UploadRepo) List(f UploadListFilter) ([]model.Upload, int64, error) {
	q := r.db.Model(&model.Upload{})

	if f.UserUID != "" {
		q = q.Where(map[string]any{"UserUID": f.UserUID})
	}

	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := "%" + escapeLike(kw) + "%"
		q = q.Where(
			`(`+col("FileName")+` LIKE ? ESCAPE '\' OR `+col("OriginalName")+` LIKE ? ESCAPE '\' OR `+
				col("AliasName")+` LIKE ? ESCAPE '\')`,
			like, like, like,
		)
	}
	if s := strings.TrimSpace(f.StorageUID); s != "" {
		q = q.Where(map[string]any{"StorageUID": s})
	}
	if st := strings.TrimSpace(f.Status); st != "" {
		q = q.Where(map[string]any{"Status": st})
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, wrap(err)
	}

	order := col("CreatedAt")
	switch strings.ToLower(strings.TrimSpace(f.Sort)) {
	case "size":
		order = col("Size")
	case "filename":
		order = col("FileName")
	case "createdat", "":
		order = col("CreatedAt")
	}
	if strings.EqualFold(strings.TrimSpace(f.Order), "asc") {
		order += " ASC"
	} else {
		order += " DESC"
	}

	var out []model.Upload
	err := q.Order(order).
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		Find(&out).Error
	if err != nil {
		return nil, 0, wrap(err)
	}
	return out, total, nil
}

// FindByUID 按 UID 查一条；不存在返回 ErrNotFound。
// ListByJob 返回某批次（job）下的全部图片，按 Seq 升序。
//
// 用途：Lsky 同步上传在 job 完成后回查成功项（入队时 item 状态可能还是 queued）。
func (r *UploadRepo) ListByJob(jobUID string) ([]model.Upload, error) {
	if jobUID == "" {
		return nil, nil
	}
	var out []model.Upload
	if err := r.db.Where("JobUID = ?", jobUID).Order("UID ASC").Find(&out).Error; err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

func (r *UploadRepo) FindByUID(uid string) (*model.Upload, error) {
	var u model.Upload
	if err := r.db.Where(map[string]any{"UID": uid}).First(&u).Error; err != nil {
		return nil, wrap(err)
	}
	return &u, nil
}

// FindManyByUIDs 批量查询（顺序与入参无关，调用方自行映射）。
func (r *UploadRepo) FindManyByUIDs(uids []string) ([]model.Upload, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	var out []model.Upload
	if err := r.db.Where(col("UID")+" IN ?", uids).Find(&out).Error; err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// Create 插入一条图片记录。
func (r *UploadRepo) Create(u *model.Upload) error { return wrap(r.db.Create(u).Error) }

// UpdateFields 更新指定列。
func (r *UploadRepo) UpdateFields(uid string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	fields["UpdatedAt"] = model.Now()
	res := r.db.Model(&model.Upload{}).Where(map[string]any{"UID": uid}).Updates(fields)
	if res.Error != nil {
		return wrap(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteWithResult 在**同一事务**内删除图片与其原始返回值，并返回是否会退还的字节数。
//
// 返回值 freedBytes 取自被删行的 Size；配额退还由上层在同一事务外处理
// （因为还涉及 Users 表，而配额口径属业务规则，见 D72）。
func (r *UploadRepo) DeleteWithResult(uid string) (freedBytes int64, err error) {
	err = r.db.Transaction(func(tx *gorm.DB) error {
		var u model.Upload
		if e := tx.Where(map[string]any{"UID": uid}).First(&u).Error; e != nil {
			return e
		}
		freedBytes = u.Size

		if e := tx.Where(map[string]any{"UploadUID": uid}).Delete(&model.UploadResult{}).Error; e != nil {
			return e
		}
		res := tx.Where(map[string]any{"UID": uid}).Delete(&model.Upload{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return 0, wrap(err)
	}
	return freedBytes, nil
}

// ---- UploadResults ----

// GetResult 读取上传的完整原始返回值（**密文之外的原文**，即 picgo 的 IImgInfo JSON）。
//
// ⚠️ 这段 JSON 的字段名是 picgo 原生的（`fileName` / `imgUrl` / `sha` …），
// 必须**原样**交回 agent 用于远端删除（D47）。
func (r *UploadRepo) GetResult(uploadUID string) (*model.UploadResult, error) {
	var res model.UploadResult
	err := r.db.Where(map[string]any{"UploadUID": uploadUID}).First(&res).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, wrap(err)
	}
	return &res, nil
}

// SaveResult 写入/覆盖原始返回值。
func (r *UploadRepo) SaveResult(uploadUID, rawOutput, filePath string) error {
	now := model.Now()
	return wrap(r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "UploadUID"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"RawOutput", "FilePath",
		}),
	}).Create(&model.UploadResult{
		UploadUID: uploadUID,
		RawOutput: rawOutput,
		FilePath:  filePath,
		CreatedAt: now,
	}).Error)
}

// ---- 统计与配额 ----

// UploadStats 是图库统计。
type UploadStats struct {
	Total        int64
	TotalSize    int64
	SuccessCount int64
	FailedCount  int64
	PendingCount int64
	TodayCount   int64
	WeekCount    int64
}

// Stats 按（可选）用户过滤统计。
//
// userUID 为空表示统计全站（仅管理员视图）。
func (r *UploadRepo) Stats(userUID string, todayStart, weekStart int64) (UploadStats, error) {
	var s UploadStats

	base := func() *gorm.DB {
		q := r.db.Model(&model.Upload{})
		if userUID != "" {
			q = q.Where(map[string]any{"UserUID": userUID})
		}
		return q
	}

	var agg struct {
		Cnt   int64
		Total int64
	}
	if err := base().
		Select(`COUNT(*) AS ` + col("Cnt") + `, COALESCE(SUM(` + col("Size") + `), 0) AS ` + col("Total")).
		Scan(&agg).Error; err != nil {
		return s, wrap(err)
	}
	s.Total = agg.Cnt
	s.TotalSize = agg.Total

	countByStatus := func(status string) (int64, error) {
		var n int64
		err := base().Where(map[string]any{"Status": status}).Count(&n).Error
		return n, err
	}
	var err error
	if s.SuccessCount, err = countByStatus(model.UploadStatusSuccess); err != nil {
		return s, wrap(err)
	}
	if s.FailedCount, err = countByStatus(model.UploadStatusFailed); err != nil {
		return s, wrap(err)
	}
	if s.PendingCount, err = countByStatus(model.UploadStatusPending); err != nil {
		return s, wrap(err)
	}

	if todayStart > 0 {
		if err := base().Where(col("CreatedAt")+" >= ?", todayStart).Count(&s.TodayCount).Error; err != nil {
			return s, wrap(err)
		}
	}
	if weekStart > 0 {
		if err := base().Where(col("CreatedAt")+" >= ?", weekStart).Count(&s.WeekCount).Error; err != nil {
			return s, wrap(err)
		}
	}
	return s, nil
}

// CountSince 统计某用户在某时间点之后**成功上传**的张数。
//
// 用途：上传限流（D73）。刻意**不另建计数表**（D78）：
// 张数统计直接用 Uploads 现算，代价是一次带索引的 COUNT。
//
// ⚠️ 只统计 success：失败的上传不应该消耗用户的配额/额度。
func (r *UploadRepo) CountSince(userUID string, since int64) (int64, error) {
	var n int64
	err := r.db.Model(&model.Upload{}).
		Where(map[string]any{"UserUID": userUID, "Status": model.UploadStatusSuccess}).
		Where(col("CreatedAt")+" >= ?", since).
		Count(&n).Error
	if err != nil {
		return 0, wrap(err)
	}
	return n, nil
}

// CountByStorage 按存储配置统计（图库统计的 ByStorage）。
func (r *UploadRepo) CountByStorage(userUID string) ([]struct {
	StorageUID string
	Cnt        int64
}, error) {
	q := r.db.Model(&model.Upload{}).
		Select(col("StorageUID") + ", COUNT(*) AS " + col("Cnt"))
	if userUID != "" {
		q = q.Where(map[string]any{"UserUID": userUID})
	}
	var rows []struct {
		StorageUID string
		Cnt        int64
	}
	if err := q.Group("StorageUID").Scan(&rows).Error; err != nil {
		return nil, wrap(err)
	}
	return rows, nil
}

// CountByExtension 按扩展名统计（图库统计的 ByExtension）。
func (r *UploadRepo) CountByExtension(userUID string) ([]struct {
	Extension string
	Cnt       int64
}, error) {
	q := r.db.Model(&model.Upload{}).
		Select(col("Extension") + ", COUNT(*) AS " + col("Cnt"))
	if userUID != "" {
		q = q.Where(map[string]any{"UserUID": userUID})
	}
	var rows []struct {
		Extension string
		Cnt       int64
	}
	if err := q.Group("Extension").Scan(&rows).Error; err != nil {
		return nil, wrap(err)
	}
	return rows, nil
}

// ---- 上传队列相关 ----

// CountUnfinished 统计未完成的 job 数（queued + running）。
//
// 用于队列长度上限（`upload.queueMaxLength`，D41）。
func (r *UploadRepo) CountUnfinished() (int64, error) {
	var n int64
	err := r.db.Model(&model.Upload{}).
		Where(map[string]any{"Status": model.UploadStatusPending}).
		Count(&n).Error
	if err != nil {
		return 0, wrap(err)
	}
	return n, nil
}

// ---- 与配额联动的原子操作 ----
//
// 为什么把「改图片记录」与「改用户已用字节」放在**同一个 repo 方法**里：
// 两者必须原子（D72）。若分两次事务，中间崩溃就会留下「图片删了但配额没退」
// 或「配额退了但图片还在」—— 这类账不平需要人工对账，必须从设计上排除。
// 事务边界属于数据一致性职责，因此放在数据访问层；service 只表达业务语义。

// MarkSuccessAndChargeQuota 在同一事务内把图片置为成功并累加用户的已用字节。
//
// 入参 fields 由 service 决定（URL / FileName / Width …）；
// size 为本次实际占用字节（通常就是文件的 Size）。
func (r *UploadRepo) MarkSuccessAndChargeQuota(uploadUID string, fields map[string]any, ownerUID string, size int64) error {
	return wrap(r.db.Transaction(func(tx *gorm.DB) error {
		if len(fields) > 0 {
			fields["UpdatedAt"] = model.Now()
			res := tx.Model(&model.Upload{}).Where(map[string]any{"UID": uploadUID}).Updates(fields)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return ErrNotFound
			}
		}
		if ownerUID == "" || size == 0 {
			return nil
		}
		// 用 SQL 表达式自增，避免「读-改-写」的丢失更新
		return tx.Model(&model.User{}).
			Where(map[string]any{"UID": ownerUID}).
			Update("UsedBytes", gorm.Expr(col("UsedBytes")+" + ?", size)).Error
	}))
}

// DeleteWithQuotaRefund 在同一事务内删除图片（含原始返回值）并退还配额（D72）。
//
// 返回 freedBytes（退还的字节数）与 ownerUID（配额所属用户）。
//
// ⚠️ 退还与「是否真的删掉远端文件」**无关**（D72）：
// 配额口径是「当前持有多少张图的体积」，远端残留属预期（自用场景可接受）。
// 用 SQL 表达式做减法并把下限夹在 0，防历史数据异常导致负数配额。
func (r *UploadRepo) DeleteWithQuotaRefund(uploadUID string) (freedBytes int64, ownerUID string, err error) {
	err = r.db.Transaction(func(tx *gorm.DB) error {
		var u model.Upload
		if e := tx.Where(map[string]any{"UID": uploadUID}).First(&u).Error; e != nil {
			return e
		}
		freedBytes = u.Size
		ownerUID = u.UserUID

		if e := tx.Where(map[string]any{"UploadUID": uploadUID}).Delete(&model.UploadResult{}).Error; e != nil {
			return e
		}
		res := tx.Where(map[string]any{"UID": uploadUID}).Delete(&model.Upload{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}

		if ownerUID == "" || freedBytes == 0 {
			return nil
		}
		// UsedBytes = CASE WHEN UsedBytes - n < 0 THEN 0 ELSE UsedBytes - n END
		//
		// 用 CASE 而不是 GREATEST/MAX：CASE 是标准 SQL，两方言行为一致；
		// 而 GREATEST（PgSQL）与 MAX（SQLite 标量）名字与边界行为都不同。
		return tx.Model(&model.User{}).
			Where(map[string]any{"UID": ownerUID}).
			Update("UsedBytes", gorm.Expr(
				"CASE WHEN "+col("UsedBytes")+" - ? < 0 THEN 0 ELSE "+col("UsedBytes")+" - ? END",
				freedBytes, freedBytes,
			)).Error
	})
	if err != nil {
		return 0, "", wrap(err)
	}
	return freedBytes, ownerUID, nil
}

// SumSizesByUIDs 汇总若干图片的体积（批量删除前预估要退还的字节）。
func (r *UploadRepo) SumSizesByUIDs(uids []string) (int64, error) {
	if len(uids) == 0 {
		return 0, nil
	}
	var total *int64
	err := r.db.Model(&model.Upload{}).
		Where(col("UID")+" IN ?", uids).
		Select("COALESCE(SUM(" + col("Size") + "), 0)").
		Scan(&total).Error
	if err != nil {
		return 0, wrap(err)
	}
	if total == nil {
		return 0, nil
	}
	return *total, nil
}
