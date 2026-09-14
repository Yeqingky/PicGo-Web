package repository

import (
	"strings"

	"gorm.io/gorm"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// col 给标识符加双引号，用于**手工拼写的原生 SQL 片段**。
//
// ⚠️ 必须这么做的原因（D81.4）：表名列名是 PascalCase，而 PostgreSQL 会把
// **未加引号**的标识符折叠成小写，`WHERE UserUID = ?` 会报 column "useruid" does not exist。
// SQLite 对标识符大小写不敏感，所以这个错在本地开发时**看不见**，只能靠约定防住。
//
// 【使用规则】GORM 对各子句的处理**并不一致**，实测结论如下
// （由 identifier_quote_test.go 的 TestRepositorySQLQuotesIdentifiers 守住）：
//
//	原样透传、必须自己加引号 ：Where(原生字符串) / Select / Order / Exec / Raw
//	GORM 自行加引号、传裸列名：Group(字符串) / struct|map 条件 / Updates|Delete(map)
//
// ⚠️ 特别注意 **Order 与 Group 相反**：
//   - `Order("X")`  原样透传 → 必须传 col("X")
//   - `Group("X")`  GORM 自行加引号 → 必须传裸名 "X"（传 col() 会变成 """X"""）
//
// 这一对是最容易写错的地方，`identifier_quote_test.go` 有回归覆盖。
//
// ⚠️⚠️ **绝不能用 col() 当 Updates/Update 的列名**：
// `Updates(map[string]any{col("X"): v})` 会让 GORM 再包一层引号，
// 生成 `SET """X"""=v` → SQLite/PgSQL 报 no such column。
// 更新时一律传**裸列名**：`Update("X", v)` / `Updates(map[string]any{"X": v})`。
func col(name string) string { return `"` + name + `"` }

// escapeLike 转义 LIKE 模式里的特殊字符（配合 `ESCAPE '\'` 使用）。
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// UserListFilter 是用户列表的查询条件。
type UserListFilter struct {
	Keyword  string // 匹配 Email / UID / UserProfiles.Nickname
	Role     string
	Status   string
	Page     int
	PageSize int
	Sort     string // email | lastLoginAt | createdAt（默认 createdAt）
	Order    string // asc | desc（默认 desc）
}

// UserRepo 是 Users / UserProfiles / OAuthIdentities 的数据访问。
//
// 三个类型同属「身份」域，放同一个 repo 便于用户相关的删除级联在一个事务里完成。
type UserRepo struct{ db *gorm.DB }

// NewUserRepo 构造。
func NewUserRepo(db *gorm.DB) *UserRepo { return &UserRepo{db: db} }

// ---- Users ----

// Create 插入用户。
func (r *UserRepo) Create(u *model.User) error { return wrap(r.db.Create(u).Error) }

// CreateWithProfile 在**单个事务**内插入用户与其 Profile（D21：建号必须原子）。
func (r *UserRepo) CreateWithProfile(u *model.User, p *model.UserProfile) error {
	return wrap(r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(u).Error; err != nil {
			return err
		}
		if p != nil {
			if p.UserUID == "" {
				p.UserUID = u.UID
			}
			if err := tx.Create(p).Error; err != nil {
				return err
			}
		}
		return nil
	}))
}

// FindByUID 按 UID 查询；不存在返回 (nil, ErrNotFound)。
func (r *UserRepo) FindByUID(uid string) (*model.User, error) {
	if uid == "" {
		return nil, ErrNotFound
	}
	var u model.User
	if err := r.db.Where(map[string]any{"UID": uid}).First(&u).Error; err != nil {
		return nil, wrap(err)
	}
	return &u, nil
}

// FindByEmail 按邮箱查询（调用方应传入已规范化的邮箱）；不存在返回 (nil, ErrNotFound)。
func (r *UserRepo) FindByEmail(email string) (*model.User, error) {
	if email == "" {
		return nil, ErrNotFound
	}
	var u model.User
	if err := r.db.Where(map[string]any{"Email": email}).First(&u).Error; err != nil {
		return nil, wrap(err)
	}
	return &u, nil
}

// Update 全量保存用户（含 UpdatedAt）。
func (r *UserRepo) Update(u *model.User) error {
	u.UpdatedAt = model.Now()
	return wrap(r.db.Save(u).Error)
}

// UpdateFields 只更新指定字段。fields 的键是**模型字段名**（GORM 会转成列名并正确加引号）。
func (r *UserRepo) UpdateFields(uid string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	if _, ok := fields["UpdatedAt"]; !ok {
		fields["UpdatedAt"] = model.Now()
	}
	res := r.db.Model(&model.User{}).Where(map[string]any{"UID": uid}).Updates(fields)
	if res.Error != nil {
		return wrap(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// List 分页查询用户。
func (r *UserRepo) List(f UserListFilter) ([]model.User, int64, error) {
	build := func() *gorm.DB {
		q := r.db.Model(&model.User{})

		if kw := strings.TrimSpace(f.Keyword); kw != "" {
			like := "%" + escapeLike(kw) + "%"
			// 昵称在 UserProfiles（D78 拆表），用子查询匹配，避免 JOIN 影响 COUNT。
			q = q.Where(
				col("Email")+` LIKE ? ESCAPE '\' OR `+col("UID")+` LIKE ? ESCAPE '\' OR `+
					col("UID")+` IN (SELECT `+col("UserUID")+` FROM `+col("UserProfiles")+
					` WHERE `+col("Nickname")+` LIKE ? ESCAPE '\')`,
				like, like, like)
		}
		if f.Role != "" {
			q = q.Where(map[string]any{"Role": f.Role})
		}
		if f.Status != "" {
			q = q.Where(map[string]any{"Status": f.Status})
		}
		return q
	}

	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, wrap(err)
	}

	orderCol := "CreatedAt"
	switch f.Sort {
	case "email":
		orderCol = "Email"
	case "lastLoginAt":
		orderCol = "LastLoginAt"
	case "createdAt", "":
		orderCol = "CreatedAt"
	}
	dir := "DESC"
	if strings.EqualFold(f.Order, "asc") {
		dir = "ASC"
	}

	var out []model.User
	err := build().
		Order(col(orderCol) + " " + dir).
		Offset((f.Page - 1) * f.PageSize).
		Limit(f.PageSize).
		Find(&out).Error
	if err != nil {
		return nil, 0, wrap(err)
	}
	return out, total, nil
}

// Count 返回用户总数（首启引导用来判断是否需要创建管理员，D32）。
func (r *UserRepo) Count() (int64, error) {
	var n int64
	if err := r.db.Model(&model.User{}).Count(&n).Error; err != nil {
		return 0, wrap(err)
	}
	return n, nil
}

// CountAdmins 返回全部管理员数量（用于「不可删除最后一个 admin」）。
func (r *UserRepo) CountAdmins() (int64, error) {
	var n int64
	err := r.db.Model(&model.User{}).Where(map[string]any{"Role": model.UserRoleAdmin}).Count(&n).Error
	return n, wrap(err)
}

// CountActiveAdmins 返回**启用状态**的管理员数量
// （用于「不可把最后一个可用 admin 降权或禁用」）。
func (r *UserRepo) CountActiveAdmins() (int64, error) {
	var n int64
	err := r.db.Model(&model.User{}).
		Where(map[string]any{"Role": model.UserRoleAdmin, "Status": model.UserStatusActive}).
		Count(&n).Error
	return n, wrap(err)
}

// UploadStats 返回某用户的图片数量与占用字节数（删除账号时用于回报统计）。
func (r *UserRepo) UploadStats(userUID string) (count int64, size int64, err error) {
	var row struct {
		Cnt   int64
		Total int64
	}
	err = r.db.Raw(
		`SELECT COUNT(*) AS `+col("Cnt")+`, COALESCE(SUM(`+col("Size")+`), 0) AS `+col("Total")+
			` FROM `+col("Uploads")+` WHERE `+col("UserUID")+` = ?`, userUID).Scan(&row).Error
	if err != nil {
		return 0, 0, wrap(err)
	}
	return row.Cnt, row.Total, nil
}

// PurgeStats 是账号注销时实际清理的数据量。
type PurgeStats struct {
	Uploads    int64
	FreedBytes int64
	Albums     int64
	Identities int64
	Tokens     int64
}

// Purge 硬删除用户及其全部从属数据（D46），返回清理统计。
//
// ⚠️ 本函数**只动数据库**。「删除图床上的远端文件」（D47）需要 agent 参与，
// 由上层在调用本函数**之前**处理；见 UserService.Delete 的说明。
//
// 注意：不建外键约束（D77），所以级联必须手工执行。
func (r *UserRepo) Purge(uid string) (PurgeStats, error) {
	var stats PurgeStats

	err := r.db.Transaction(func(tx *gorm.DB) error {
		var row struct {
			Cnt   int64
			Total int64
		}
		if err := tx.Raw(
			`SELECT COUNT(*) AS `+col("Cnt")+`, COALESCE(SUM(`+col("Size")+`), 0) AS `+col("Total")+
				` FROM `+col("Uploads")+` WHERE `+col("UserUID")+` = ?`, uid).Scan(&row).Error; err != nil {
			return err
		}
		stats.Uploads = row.Cnt
		stats.FreedBytes = row.Total

		// 先删依赖 Uploads 的表，再删 Uploads 本身
		stmts := []string{
			`DELETE FROM ` + col("UploadResults") + ` WHERE ` + col("UploadUID") + ` IN (` +
				`SELECT ` + col("UID") + ` FROM ` + col("Uploads") + ` WHERE ` + col("UserUID") + ` = ?)`,
		}
		for _, sql := range stmts {
			if err := tx.Exec(sql, uid).Error; err != nil {
				return err
			}
		}

		var albums int64
		if err := tx.Model(&model.Album{}).Where(map[string]any{"UserUID": uid}).Count(&albums).Error; err != nil {
			return err
		}
		stats.Albums = albums

		var identities int64
		if err := tx.Model(&model.OAuthIdentity{}).Where(map[string]any{"UserUID": uid}).Count(&identities).Error; err != nil {
			return err
		}
		stats.Identities = identities

		var refresh, api int64
		if err := tx.Model(&model.RefreshToken{}).Where(map[string]any{"UserUID": uid}).Count(&refresh).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.APIToken{}).Where(map[string]any{"UserUID": uid}).Count(&api).Error; err != nil {
			return err
		}
		stats.Tokens = refresh + api

		for _, table := range []string{"Uploads", "Albums", "UserSettings", "UserProfiles", "OAuthIdentities", "RefreshTokens", "APITokens"} {
			if err := tx.Exec(`DELETE FROM `+col(table)+` WHERE `+col("UserUID")+` = ?`, uid).Error; err != nil {
				return err
			}
		}
		return tx.Exec(`DELETE FROM `+col("Users")+` WHERE `+col("UID")+` = ?`, uid).Error
	})
	if err != nil {
		return PurgeStats{}, wrap(err)
	}
	return stats, nil
}

// CountUploadsByUser 批量统计图片数（列表页用，避免 N+1）。
func (r *UserRepo) CountUploadsByUser(uids []string) (map[string]int64, error) {
	if len(uids) == 0 {
		return map[string]int64{}, nil
	}
	var rows []struct {
		UserUID string
		Cnt     int64
	}
	// Select 是原样透传 → 这里要手工加引号；Group 由 GORM 自行加引号 → 传裸列名。
	err := r.db.Model(&model.Upload{}).
		Select(col("UserUID")+` AS `+col("UserUID")+`, COUNT(*) AS `+col("Cnt")).
		Where(col("UserUID")+` IN ?`, uids).
		Group("UserUID").
		Scan(&rows).Error
	if err != nil {
		return nil, wrap(err)
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.UserUID] = row.Cnt
	}
	return out, nil
}

// CountAlbumsByUser 批量统计相册数（列表页用）。
func (r *UserRepo) CountAlbumsByUser(uids []string) (map[string]int64, error) {
	if len(uids) == 0 {
		return map[string]int64{}, nil
	}
	var rows []struct {
		UserUID string
		Cnt     int64
	}
	err := r.db.Model(&model.Album{}).
		Select(col("UserUID")+` AS `+col("UserUID")+`, COUNT(*) AS `+col("Cnt")).
		Where(col("UserUID")+` IN ?`, uids).
		Group("UserUID").
		Scan(&rows).Error
	if err != nil {
		return nil, wrap(err)
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.UserUID] = row.Cnt
	}
	return out, nil
}

// ---- UserProfiles ----

// FindProfile 按用户 UID 查 Profile；不存在返回 (nil, ErrNotFound)。
func (r *UserRepo) FindProfile(userUID string) (*model.UserProfile, error) {
	var p model.UserProfile
	if err := r.db.Where(map[string]any{"UserUID": userUID}).First(&p).Error; err != nil {
		return nil, wrap(err)
	}
	return &p, nil
}

// FindProfiles 批量查 Profile（列表页用）。
func (r *UserRepo) FindProfiles(uids []string) (map[string]model.UserProfile, error) {
	if len(uids) == 0 {
		return map[string]model.UserProfile{}, nil
	}
	var list []model.UserProfile
	if err := r.db.Where(col("UserUID")+` IN ?`, uids).Find(&list).Error; err != nil {
		return nil, wrap(err)
	}
	out := make(map[string]model.UserProfile, len(list))
	for _, p := range list {
		out[p.UserUID] = p
	}
	return out, nil
}

// SaveProfile 插入或更新展示信息。
func (r *UserRepo) SaveProfile(p *model.UserProfile) error {
	p.UpdatedAt = model.Now()
	// 以 UserUID 为业务主键做 upsert（表上有 uniqueIndex）。
	res := r.db.Model(&model.UserProfile{}).
		Where(map[string]any{"UserUID": p.UserUID}).
		Updates(map[string]any{
			"Nickname":  p.Nickname,
			"AvatarURL": p.AvatarURL,
			"Homepage":  p.Homepage,
			"Locale":    p.Locale,
			"UpdatedAt": p.UpdatedAt,
		})
	if res.Error != nil {
		return wrap(res.Error)
	}
	if res.RowsAffected > 0 {
		return nil
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = p.UpdatedAt
	}
	return wrap(r.db.Create(p).Error)
}

// ---- OAuthIdentities ----

// CreateIdentity 绑定一个第三方身份。
func (r *UserRepo) CreateIdentity(i *model.OAuthIdentity) error {
	return wrap(r.db.Create(i).Error)
}

// FindIdentity 按 (Provider, ProviderUserID) 查绑定；不存在返回 (nil, ErrNotFound)。
//
// ProviderUserID 用平台的数字 ID（D28：必须是稳定唯一标识）。
func (r *UserRepo) FindIdentity(provider, providerUserID string) (*model.OAuthIdentity, error) {
	var i model.OAuthIdentity
	err := r.db.Where(map[string]any{"Provider": provider, "ProviderUserID": providerUserID}).
		First(&i).Error
	if err != nil {
		return nil, wrap(err)
	}
	return &i, nil
}

// FindIdentityForUser 查某用户在某 provider 下的绑定。
func (r *UserRepo) FindIdentityForUser(userUID, provider string) (*model.OAuthIdentity, error) {
	var i model.OAuthIdentity
	err := r.db.Where(map[string]any{"UserUID": userUID, "Provider": provider}).First(&i).Error
	if err != nil {
		return nil, wrap(err)
	}
	return &i, nil
}

// ListIdentities 列出某用户的全部绑定。
func (r *UserRepo) ListIdentities(userUID string) ([]model.OAuthIdentity, error) {
	var out []model.OAuthIdentity
	if err := r.db.Where(map[string]any{"UserUID": userUID}).
		Order(col("CreatedAt") + " ASC").Find(&out).Error; err != nil {
		return nil, wrap(err)
	}
	return out, nil
}

// CountIdentities 统计某用户的绑定数量（解绑时判断「是否会锁死账号」）。
func (r *UserRepo) CountIdentities(userUID string) (int64, error) {
	var n int64
	err := r.db.Model(&model.OAuthIdentity{}).Where(map[string]any{"UserUID": userUID}).Count(&n).Error
	return n, wrap(err)
}

// DeleteIdentity 解绑某 provider。
func (r *UserRepo) DeleteIdentity(userUID, provider string) (int64, error) {
	res := r.db.Where(map[string]any{"UserUID": userUID, "Provider": provider}).
		Delete(&model.OAuthIdentity{})
	return res.RowsAffected, wrap(res.Error)
}

// UpdateIdentityDisplay 同步第三方侧可能变化的展示信息（仅这三个字段）。
//
// 登录成功时顺手调用，让「已绑定身份」列表里的头像/昵称不会长期过期。
func (r *UserRepo) UpdateIdentityDisplay(uid, providerLogin, providerEmail, avatarURL string) error {
	res := r.db.Model(&model.OAuthIdentity{}).
		Where(map[string]any{"UID": uid}).
		Updates(map[string]any{
			"ProviderLogin": providerLogin,
			"ProviderEmail": providerEmail,
			"AvatarURL":     avatarURL,
			"UpdatedAt":     model.Now(),
		})
	return wrap(res.Error)
}
