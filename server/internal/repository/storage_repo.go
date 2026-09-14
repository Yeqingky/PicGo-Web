package repository

import (
	"errors"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// StorageConfigRepo 是 StorageConfigs / StorageSecrets 的数据访问。
//
// 两张表同属「存储配置」域，放同一个 repo 是因为**凭据与其元数据必须成对操作**：
// 删除配置时必须同时删凭据，否则会留下无主的密文行。
//
// ⚠️ 密钥与元数据**分表**（D78 敏感级不同）：任何针对 StorageConfigs 的列表/统计
// 查询都**不可能**误带出密钥。凭据只在 GetSecret 里显式读取。
type StorageConfigRepo struct{ db *gorm.DB }

// NewStorageConfigRepo 构造。
func NewStorageConfigRepo(db *gorm.DB) *StorageConfigRepo { return &StorageConfigRepo{db: db} }

// StorageListFilter 是存储配置列表的查询条件。
type StorageListFilter struct {
	Keyword  string // 匹配 Name / UID / Type
	Type     string
	Enabled  *bool
	Page     int
	PageSize int
}

// List 分页查询存储配置。
func (r *StorageConfigRepo) List(f StorageListFilter) ([]model.StorageConfig, int64, error) {
	q := r.db.Model(&model.StorageConfig{})

	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := "%" + escapeLike(kw) + "%"
		q = q.Where(
			`(`+col("Name")+` LIKE ? ESCAPE '\' OR `+col("UID")+` LIKE ? ESCAPE '\' OR `+
				col("Type")+` LIKE ? ESCAPE '\')`,
			like, like, like,
		)
	}
	if t := strings.TrimSpace(f.Type); t != "" {
		q = q.Where(map[string]any{"Type": t})
	}
	if f.Enabled != nil {
		q = q.Where(map[string]any{"Enabled": *f.Enabled})
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, wrap(err)
	}

	var out []model.StorageConfig
	// 默认配置排最前，其余按创建时间倒序，便于后台一眼看到当前生效的
	err := q.Order(col("IsDefault") + " DESC, " + col("CreatedAt") + " ASC").
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		Find(&out).Error
	if err != nil {
		return nil, 0, wrap(err)
	}
	return out, total, nil
}

// ListAll 返回全部配置（不分页），供 reconcile 与统计使用。
func (r *StorageConfigRepo) ListAll() ([]model.StorageConfig, error) {
	var out []model.StorageConfig
	if err := r.db.Order(col("CreatedAt") + " ASC").Find(&out).Error; err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// ListEnabled 返回全部启用的配置（按 UpdatedAt 升序 —— reconcile 的顺序依据）。
func (r *StorageConfigRepo) ListEnabled() ([]model.StorageConfig, error) {
	var out []model.StorageConfig
	err := r.db.Where(map[string]any{"Enabled": true}).
		Order(col("UpdatedAt") + " ASC").Find(&out).Error
	if err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// FindByUID 按 UID 查一条；不存在返回 ErrNotFound。
func (r *StorageConfigRepo) FindByUID(uid string) (*model.StorageConfig, error) {
	var c model.StorageConfig
	if err := r.db.Where(map[string]any{"UID": uid}).First(&c).Error; err != nil {
		return nil, wrap(err)
	}
	return &c, nil
}

// FindDefault 返回当前全局默认配置；没有则 nil。
func (r *StorageConfigRepo) FindDefault() (*model.StorageConfig, error) {
	var c model.StorageConfig
	err := r.db.Where(map[string]any{"IsDefault": true, "Enabled": true}).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, wrap(err)
	}
	return &c, nil
}

// NameExists 判断展示名是否已被占用（excludeUID 用于「更新时排除自己」）。
//
// ⚠️ 大小写不敏感：`GitHub` 与 `github` 视为同一个名字，
// 避免用户在后台看到两个「看起来一样」的配置。
func (r *StorageConfigRepo) NameExists(name, excludeUID string) (bool, error) {
	q := r.db.Model(&model.StorageConfig{}).
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

// TypeConfigNameExists 判断 (Type, PicgoConfigName) 是否已存在。
//
// 这是 D64 的硬约束：picgo 的 `createOrUpdate` 是「未命中即新建」语义，
// 同类型同配置名重复会让两条存储配置映射到**同一个** picgo 配置项。
func (r *StorageConfigRepo) TypeConfigNameExists(typ, picgoConfigName, excludeUID string) (bool, error) {
	q := r.db.Model(&model.StorageConfig{}).
		Where(map[string]any{"Type": typ, "PicgoConfigName": picgoConfigName})
	if excludeUID != "" {
		q = q.Where(col("UID")+" <> ?", excludeUID)
	}
	var n int64
	if err := q.Count(&n).Error; err != nil {
		return false, wrap(err)
	}
	return n > 0, nil
}

// Create 插入配置。调用方需先做唯一性检查。
func (r *StorageConfigRepo) Create(c *model.StorageConfig) error {
	return wrap(r.db.Create(c).Error)
}

// UpdateFields 更新指定列。
func (r *StorageConfigRepo) UpdateFields(uid string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	fields["UpdatedAt"] = model.Now()
	res := r.db.Model(&model.StorageConfig{}).Where(map[string]any{"UID": uid}).Updates(fields)
	if res.Error != nil {
		return wrap(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete 删除配置**及其凭据**（单事务）。
func (r *StorageConfigRepo) Delete(uid string) error {
	return wrap(r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where(map[string]any{"StorageUID": uid}).Delete(&model.StorageSecret{}).Error; err != nil {
			return err
		}
		res := tx.Where(map[string]any{"UID": uid}).Delete(&model.StorageConfig{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	}))
}

// SetDefault 把 uid 置为唯一默认（同事务清掉其他行）。
//
// 传空字符串表示「清空默认」（删除最后一条配置时用）。
func (r *StorageConfigRepo) SetDefault(uid string) error {
	return wrap(r.db.Transaction(func(tx *gorm.DB) error {
		// 先全部置 false（含自己），再把自己置 true —— 避免顺序导致短暂的双默认
		if err := tx.Model(&model.StorageConfig{}).
			Where(col("IsDefault")+" = ?", true).
			Update("IsDefault", false).Error; err != nil {
			return err
		}
		if uid == "" {
			return nil
		}
		res := tx.Model(&model.StorageConfig{}).Where(map[string]any{"UID": uid}).
			Updates(map[string]any{"IsDefault": true, "UpdatedAt": model.Now()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	}))
}

// CreateWithSecret 在**单事务**内创建配置与凭据（D21 的「成对原子」原则）。
func (r *StorageConfigRepo) CreateWithSecret(c *model.StorageConfig, encPayload string) error {
	return wrap(r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(c).Error; err != nil {
			return err
		}
		if encPayload == "" {
			return nil
		}
		secret := &model.StorageSecret{
			StorageUID:       c.UID,
			EncryptedPayload: encPayload,
			KeyVersion:       1,
			CreatedAt:        model.Now(),
			UpdatedAt:        model.Now(),
		}
		return tx.Create(secret).Error
	}))
}

// GetSecret 读取凭据（**密文**）。不存在返回 ("", nil)。
//
// 解密与脱敏都是上层（storage service）的职责 —— repo 不持有主密钥。
func (r *StorageConfigRepo) GetSecret(uid string) (string, error) {
	var s model.StorageSecret
	err := r.db.Where(map[string]any{"StorageUID": uid}).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", wrap(err)
	}
	return s.EncryptedPayload, nil
}

// UpsertSecret 写入/覆盖凭据。
func (r *StorageConfigRepo) UpsertSecret(uid, encPayload string) error {
	now := model.Now()
	return wrap(r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "StorageUID"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"EncryptedPayload", "KeyVersion", "UpdatedAt",
		}),
	}).Create(&model.StorageSecret{
		StorageUID:       uid,
		EncryptedPayload: encPayload,
		KeyVersion:       1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}).Error)
}

// SecretExists 判断某配置是否已有凭据行。
func (r *StorageConfigRepo) SecretExists(uid string) (bool, error) {
	var n int64
	err := r.db.Model(&model.StorageSecret{}).
		Where(map[string]any{"StorageUID": uid}).Count(&n).Error
	if err != nil {
		return false, wrap(err)
	}
	return n > 0, nil
}

// CountUploads 统计引用某配置的图片数（删除前提示、以及「有图片则拒绝删除」）。
func (r *StorageConfigRepo) CountUploads(uid string) (int64, error) {
	var n int64
	err := r.db.Model(&model.Upload{}).Where(map[string]any{"StorageUID": uid}).Count(&n).Error
	if err != nil {
		return 0, wrap(err)
	}
	return n, nil
}

// CountUploadsByStorage 批量统计各存储配置的图片数（列表页用，避免 N+1）。
func (r *StorageConfigRepo) CountUploadsByStorage() (map[string]int64, error) {
	var rows []struct {
		StorageUID string
		Cnt        int64
	}
	err := r.db.Model(&model.Upload{}).
		Select(col("StorageUID") + ", COUNT(*) AS " + col("Cnt")).
		Group("StorageUID").
		Scan(&rows).Error
	if err != nil {
		return nil, wrap(err)
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.StorageUID] = row.Cnt
	}
	return out, nil
}
