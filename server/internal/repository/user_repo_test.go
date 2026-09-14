package repository

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/database"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// newTestRepo 建一个跑完迁移的临时库。
func newTestRepo(t *testing.T) *UserRepo {
	t.Helper()

	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{
		DBDriver:       config.DBDriverSQLite,
		SQLitePath:     filepath.Join(dir, "repo.db"),
		DBMaxOpenConns: 1,
		DBMaxIdleConns: 1,
	}
	db, err := database.Open(cfg, log)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := database.Migrate(db, log); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return NewUserRepo(db.DB)
}

// TestListKeywordAndRoleFiltersCombineWithAND 是一处**非显而易见的依赖**的回归测试。
//
// List 的关键词条件是一个含 OR 的原生 SQL 片段，随后又追加 Role / Status 过滤。
// 这在 SQL 里本会因 AND 优先级高于 OR 而变成「Role 只作用于最后一个 OR 分支」，
// 之所以正确，是因为 **GORM 会把原生字符串条件自动用括号包起来**。
//
// 若将来有人把条件改成手工拼接的字符串、或升级 GORM 改变了该行为，
// 本测试会立刻失败，避免出现「按角色过滤时把不匹配的用户也查出来」这种静默错误。
func TestListKeywordAndRoleFiltersCombineWithAND(t *testing.T) {
	repo := newTestRepo(t)

	now := model.Now()
	seed := []struct {
		uid, email, role, status, nickname string
	}{
		{"usr_1", "match@example.com", model.UserRoleUser, model.UserStatusActive, "命中关键词但角色不符"},
		{"usr_2", "other@example.com", model.UserRoleAdmin, model.UserStatusActive, "角色对但关键词不中"},
		{"usr_3", "match-admin@example.com", model.UserRoleAdmin, model.UserStatusActive, "BothMatch"},
		{"usr_4", "match-disabled@example.com", model.UserRoleAdmin, model.UserStatusDisabled, "已禁用"},
	}
	for _, s := range seed {
		if err := repo.Create(&model.User{
			UID: s.uid, Email: s.email, Role: s.role, Status: s.status,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("预置用户失败: %v", err)
		}
		if err := repo.SaveProfile(&model.UserProfile{
			UserUID: s.uid, Nickname: s.nickname, Locale: "zh-CN",
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("预置资料失败: %v", err)
		}
	}

	// 关键词 + 角色：必须两个条件同时满足（AND）。
	//
	// 命中关键词的有 usr_1(match@, role=user)、usr_3(match-admin@, role=admin)、
	// usr_4(match-disabled@, role=admin) —— 过滤 role=admin 后应只剩 usr_3 与 usr_4。
	// 若 OR 组没有被正确地用括号包起来（即 Role 只作用于最后一个 OR 分支），
	// usr_1 会被错误地包含进来，总数会变成 3。
	users, total, err := repo.List(UserListFilter{Keyword: "match", Role: model.UserRoleAdmin, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if total != 2 {
		t.Fatalf("关键词 + 角色过滤应 AND 组合，期望 2 条，实际 %d 条", total)
	}
	got := map[string]bool{}
	for _, u := range users {
		got[u.UID] = true
	}
	if got["usr_1"] {
		t.Error("usr_1 邮箱命中关键词但角色是 user，不应出现在 role=admin 的结果里（OR 组未加括号）")
	}
	if !got["usr_3"] || !got["usr_4"] {
		t.Errorf("应包含 usr_3 与 usr_4，实际 %v", got)
	}

	// 关键词 + 状态同理
	_, total, err = repo.List(UserListFilter{Keyword: "match", Status: model.UserStatusDisabled, Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("关键词 + 状态过滤应 AND 组合，期望 1 条，实际 %d 条", total)
	}

	// 只按关键词：3 条邮箱命中
	_, total, err = repo.List(UserListFilter{Keyword: "match", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("只按关键词应命中 3 条，实际 %d", total)
	}

	// 关键词命中「昵称」也要能查到（昵称在 UserProfiles，走子查询）
	_, total, err = repo.List(UserListFilter{Keyword: "BothMatch", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Errorf("按昵称匹配应命中 1 条，实际 %d", total)
	}

	// 关键词里的 LIKE 通配符必须被转义（不能被当作通配查询）
	_, total, err = repo.List(UserListFilter{Keyword: "%", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Errorf("字面量 %% 应被转义（否则会匹配所有用户），实际命中 %d 条", total)
	}
}

// TestCountByUserGroupQueries 回归测试 GORM 的两条引用规则：
//
//   - Select 里的原生片段要**手工加引号**
//   - Group 由 GORM 自行加引号，必须传**裸列名**（传带引号的会变成 “GROUP BY `"Col"` “ 而报错）
//
// 这两点搞错的症状是运行时 SQL 错误或统计恒为 0，因此值得锁住。
func TestCountByUserGroupQueries(t *testing.T) {
	repo := newTestRepo(t)
	now := model.Now()

	for _, uid := range []string{"usr_a", "usr_b"} {
		if err := repo.Create(&model.User{
			UID: uid, Email: uid + "@example.com", Role: model.UserRoleUser,
			Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// usr_a 有 2 张图、1 个相册；usr_b 有 1 张图、0 个相册
	uploads := []struct {
		uid, user string
		size      int64
	}{
		{"up_1", "usr_a", 100},
		{"up_2", "usr_a", 200},
		{"up_3", "usr_b", 300},
	}
	for _, u := range uploads {
		if err := repo.db.Create(&model.Upload{
			UID: u.uid, UserUID: u.user, StorageUID: "st_1", FileName: u.uid + ".png",
			Size: u.size, Status: model.UploadStatusSuccess, Source: model.UploadSourceWeb,
			CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.db.Create(&model.Album{
		UID: "al_1", UserUID: "usr_a", Name: "默认", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	counts, err := repo.CountUploadsByUser([]string{"usr_a", "usr_b"})
	if err != nil {
		t.Fatalf("统计图片数失败: %v", err)
	}
	if counts["usr_a"] != 2 {
		t.Errorf("usr_a 应有 2 张图片，实际 %d", counts["usr_a"])
	}
	if counts["usr_b"] != 1 {
		t.Errorf("usr_b 应有 1 张图片，实际 %d", counts["usr_b"])
	}
	if _, ok := counts["usr_c"]; ok {
		t.Error("没有图片的用户不应出现在结果里")
	}

	albums, err := repo.CountAlbumsByUser([]string{"usr_a", "usr_b"})
	if err != nil {
		t.Fatalf("统计相册数失败: %v", err)
	}
	if albums["usr_a"] != 1 {
		t.Errorf("usr_a 应有 1 个相册，实际 %d", albums["usr_a"])
	}
	if albums["usr_b"] != 0 {
		t.Errorf("usr_b 应有 0 个相册，实际 %d", albums["usr_b"])
	}

	// 空入参不应发起查询、也不应报错
	if m, err := repo.CountUploadsByUser(nil); err != nil || len(m) != 0 {
		t.Errorf("空入参应返回空 map 且无错误，实际 %v / %v", m, err)
	}
}

func TestUploadStats(t *testing.T) {
	repo := newTestRepo(t)
	now := model.Now()

	for _, u := range []struct {
		uid  string
		size int64
	}{
		{"up_a", 1024}, {"up_b", 2048},
	} {
		if err := repo.db.Create(&model.Upload{
			UID: u.uid, UserUID: "usr_x", StorageUID: "st_1", FileName: u.uid + ".png",
			Size: u.size, Status: model.UploadStatusSuccess, Source: model.UploadSourceWeb,
			CreatedAt: now, UpdatedAt: now,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	count, size, err := repo.UploadStats("usr_x")
	if err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != 2 {
		t.Errorf("图片数应为 2，实际 %d", count)
	}
	if size != 3072 {
		t.Errorf("总大小应为 3072，实际 %d", size)
	}

	// 没有图片的用户：应返回 0/0 而不是 SQL 错误（SUM 空集返回 NULL，靠 COALESCE 兜底）
	count, size, err = repo.UploadStats("usr_nobody")
	if err != nil {
		t.Fatalf("空集统计不应报错: %v", err)
	}
	if count != 0 || size != 0 {
		t.Errorf("空集应返回 0/0，实际 %d/%d", count, size)
	}
}

func TestCountActiveAdmins(t *testing.T) {
	repo := newTestRepo(t)
	now := model.Now()

	rows := []struct {
		uid, role, status string
	}{
		{"usr_1", model.UserRoleAdmin, model.UserStatusActive},
		{"usr_2", model.UserRoleAdmin, model.UserStatusDisabled},
		{"usr_3", model.UserRoleAdmin, model.UserStatusActive},
		{"usr_4", model.UserRoleUser, model.UserStatusActive},
	}
	for _, r := range rows {
		if err := repo.Create(&model.User{
			UID: r.uid, Email: r.uid + "@example.com", Role: r.role, Status: r.status,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := repo.CountAdmins()
	if err != nil {
		t.Fatal(err)
	}
	if all != 3 {
		t.Errorf("管理员总数应为 3，实际 %d", all)
	}

	active, err := repo.CountActiveAdmins()
	if err != nil {
		t.Fatal(err)
	}
	if active != 2 {
		t.Errorf("可用管理员应为 2，实际 %d", active)
	}

	total, err := repo.Count()
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Errorf("用户总数应为 4，实际 %d", total)
	}
}

// TestSaveProfileUpsert 展示信息走 upsert，不必先建再改。
func TestSaveProfileUpsert(t *testing.T) {
	repo := newTestRepo(t)
	now := model.Now()

	if err := repo.Create(&model.User{
		UID: "usr_p", Email: "p@example.com", Role: model.UserRoleUser,
		Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	// 第一次：应插入
	if err := repo.SaveProfile(&model.UserProfile{
		UserUID: "usr_p", Nickname: "旧名", Locale: "zh-CN", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("首次保存失败: %v", err)
	}

	// 第二次：应更新（而不是插入第二行）
	if err := repo.SaveProfile(&model.UserProfile{
		UserUID: "usr_p", Nickname: "新名", Homepage: "https://example.com", Locale: "zh-CN",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("二次保存失败: %v", err)
	}

	got, err := repo.FindProfile("usr_p")
	if err != nil {
		t.Fatal(err)
	}
	if got.Nickname != "新名" {
		t.Errorf("昵称应为「新名」，实际 %q", got.Nickname)
	}
	if got.Homepage != "https://example.com" {
		t.Errorf("主页应已更新，实际 %q", got.Homepage)
	}

	var n int64
	if err := repo.db.Model(&model.UserProfile{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("UserProfiles 应只有 1 行，实际 %d", n)
	}
}

// TestFindIdentityCompositeUnique (Provider, ProviderUserID) 唯一约束生效。
func TestFindIdentityCompositeUnique(t *testing.T) {
	repo := newTestRepo(t)
	now := model.Now()

	for _, uid := range []string{"usr_1", "usr_2"} {
		if err := repo.Create(&model.User{
			UID: uid, Email: uid + "@example.com", Role: model.UserRoleUser,
			Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := repo.CreateIdentity(&model.OAuthIdentity{
		UID: "oid_1", UserUID: "usr_1", Provider: model.ProviderGitHub,
		ProviderUserID: "1001", ProviderLogin: "octocat", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := repo.FindIdentity(model.ProviderGitHub, "1001")
	if err != nil {
		t.Fatalf("未找到绑定: %v", err)
	}
	if got.ProviderLogin != "octocat" {
		t.Errorf("provider login 不对: %s", got.ProviderLogin)
	}

	// 同一 (provider, providerUserID) 绑到另一个用户 → 唯一索引应拒绝
	if err := repo.CreateIdentity(&model.OAuthIdentity{
		UID: "oid_2", UserUID: "usr_2", Provider: model.ProviderGitHub,
		ProviderUserID: "1001", ProviderLogin: "octocat", CreatedAt: now, UpdatedAt: now,
	}); err == nil {
		t.Error("同一 GitHub 账号不应能绑定到两个用户")
	}

	// 同一用户重复绑同一 provider 也会撞唯一索引
	if err := repo.CreateIdentity(&model.OAuthIdentity{
		UID: "oid_3", UserUID: "usr_1", Provider: model.ProviderGitHub,
		ProviderUserID: "1001", ProviderLogin: "octocat", CreatedAt: now, UpdatedAt: now,
	}); err == nil {
		t.Error("重复绑定应被唯一索引拒绝")
	}

	// 查找不存在的绑定
	if _, err := repo.FindIdentity(model.ProviderGitHub, "9999"); !IsNotFound(err) {
		t.Errorf("不存在的绑定应返回 ErrNotFound，实际 %v", err)
	}
}
