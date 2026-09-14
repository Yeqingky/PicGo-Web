package service

import (
	"context"
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// uploadOne 是「传一张图并返回其 UID」的便捷助手。
func (e *w5Env) uploadOne(user *model.User, name string, size int, storageUID string) string {
	e.t.Helper()
	f := e.writeTempFile(name, size)
	job := e.enqueueAndWait(user, []IncomingFile{f}, storageUID)
	if job.Status != model.JobStatusSucceeded {
		e.t.Fatalf("上传 %s 失败：status=%s err=%s", name, job.Status, job.Error)
	}
	return job.Items[0].UploadUID
}

// ---- 可见性（D33 + D71）----

// TestGalleryScopeDowngradedForNormalUser 断言普通用户传 Scope=all 会被**静默降级**为 mine。
//
// 不报错的原因：这不是攻击面（后端本就不会给他人数据），
// 报错反而让「角色变化」时前端出现莫名失败。
func TestGalleryScopeDowngradedForNormalUser(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	storage := e.makeStorage("默认", true)
	alice := e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)
	bob := e.makeUser("usr_bob", "bob@example.com", model.UserRoleUser, nil)

	e.uploadOne(alice, "alice.png", 10, storage.UID)
	e.uploadOne(bob, "bob.png", 10, storage.UID)

	// alice 传 Scope=all → 只能看到自己的 1 张
	items, total, err := e.gallery.List(GalleryListInput{Scope: "all"}, alice)
	wantOK(t, err)
	if total != 1 || len(items) != 1 {
		t.Fatalf("普通用户 Scope=all 应降级为 mine，实际 total=%d", total)
	}
	if items[0].UserUID != alice.UID {
		t.Fatalf("只能看到自己的图片，实际 %s", items[0].UserUID)
	}
	// UserEmail 只在 Scope=all 时返回；降级后不应有
	if items[0].UserEmail != "" {
		t.Fatalf("降级为 mine 时不应返回 UserEmail，实际 %q", items[0].UserEmail)
	}
}

// TestGalleryScopeAllForAdmin 断言管理员的 Scope=all 能看到全部，且带 UserEmail。
func TestGalleryScopeAllForAdmin(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	storage := e.makeStorage("默认", true)
	admin := e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)
	bob := e.makeUser("usr_bob", "bob@example.com", model.UserRoleUser, nil)

	e.uploadOne(admin, "admin.png", 10, storage.UID)
	e.uploadOne(bob, "bob.png", 10, storage.UID)

	// 管理员 Scope=all → 2 张
	items, total, err := e.gallery.List(GalleryListInput{Scope: "all"}, admin)
	wantOK(t, err)
	if total != 2 {
		t.Fatalf("管理员 Scope=all 应看到 2 张，实际 %d", total)
	}
	emails := map[string]bool{}
	for _, it := range items {
		if it.UserEmail == "" {
			t.Fatalf("Scope=all 时应返回 UserEmail：%+v", it)
		}
		emails[it.UserEmail] = true
	}
	if !emails["bob@example.com"] {
		t.Fatalf("应包含 bob 的邮箱，实际 %v", emails)
	}

	// 管理员默认 Scope=mine → 只看到自己的 1 张
	items, total, err = e.gallery.List(GalleryListInput{}, admin)
	wantOK(t, err)
	if total != 1 {
		t.Fatalf("管理员默认应落在 mine（1 张），实际 %d", total)
	}
	if items[0].UserUID != admin.UID {
		t.Fatal("管理员 mine 视图应只含自己的图片")
	}
}

// TestGalleryAccessControl 断言普通用户不能读/改/删他人的图片（D33）。
func TestGalleryAccessControl(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	storage := e.makeStorage("默认", true)
	alice := e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)
	bob := e.makeUser("usr_bob", "bob@example.com", model.UserRoleUser, nil)
	admin := e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)

	uid := e.uploadOne(alice, "alice.png", 10, storage.UID)

	// bob 看不到、改不了、删不了
	_, err := e.gallery.Get(uid, bob)
	wantCode(t, err, response.CodeForbidden)

	_, err = e.gallery.Update(context.Background(), uid, GalleryUpdateInput{}, bob, "", "")
	wantCode(t, err, response.CodeForbidden)

	_, err = e.gallery.Delete(context.Background(), uid, false, bob, "", "")
	wantCode(t, err, response.CodeForbidden)

	// admin 可以
	_, err = e.gallery.Get(uid, admin)
	wantOK(t, err)
	_, err = e.gallery.Update(context.Background(), uid, GalleryUpdateInput{}, admin, "", "")
	wantOK(t, err)
}

// ---- 外链格式化（D68）----

func TestLinkFormats(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	storage := e.makeStorage("默认", true)
	alice := e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)
	uid := e.uploadOne(alice, "photo.png", 10, storage.UID)

	up := e.findUpload(uid)
	if up.URL == "" {
		t.Fatal("URL 为空")
	}

	// url
	res, err := e.gallery.Link(uid, "url", alice)
	wantOK(t, err)
	if res.Text != up.URL {
		t.Fatalf("url 格式应为纯链接，实际 %q", res.Text)
	}

	// markdown（默认）
	res, err = e.gallery.Link(uid, "markdown", alice)
	wantOK(t, err)
	if !containsStr(res.Text, "![") || !containsStr(res.Text, "]("+up.URL+")") {
		t.Fatalf("markdown 格式不对：%q", res.Text)
	}

	// html
	res, err = e.gallery.Link(uid, "html", alice)
	wantOK(t, err)
	if !containsStr(res.Text, `<img src="`+up.URL+`"`) {
		t.Fatalf("html 格式不对：%q", res.Text)
	}

	// 未知格式回退 markdown
	res, err = e.gallery.Link(uid, "whatever", alice)
	wantOK(t, err)
	if res.Format != LinkFormatMarkdown {
		t.Fatalf("未知格式应回退 markdown，实际 %s", res.Format)
	}
}

// TestBatchLinkSkipsInaccessible 断言批量复制时**静默跳过**无权项。
//
// 理由：多选里可能混入不属于自己的图片，直接失败会让用户连自己的都复制不了。
func TestBatchLinkSkipsInaccessible(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	storage := e.makeStorage("默认", true)
	alice := e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)
	bob := e.makeUser("usr_bob", "bob@example.com", model.UserRoleUser, nil)

	mine := e.uploadOne(alice, "mine.png", 10, storage.UID)
	others := e.uploadOne(bob, "others.png", 10, storage.UID)

	res, err := e.gallery.BatchLink([]string{mine, others, "up_not_exist"}, "url", alice)
	wantOK(t, err)
	if len(res.Items) != 1 {
		t.Fatalf("应只包含 1 项（自己的），实际 %d", len(res.Items))
	}
	if res.Items[0].UID != mine {
		t.Fatalf("应为 %s，实际 %s", mine, res.Items[0].UID)
	}
	if containsStr(res.Text, "others.png") {
		t.Fatal("不应包含他人图片")
	}
}

// ---- 删除（D46 + D47 + D72）----

// TestDeleteRefundsQuota 断言删除**退还配额**（D72）。
func TestDeleteRefundsQuota(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	storage := e.makeStorage("默认", true)
	alice := e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)

	uid := e.uploadOne(alice, "a.png", 1234, storage.UID)

	fresh, err := e.users.FindByUID(alice.UID)
	wantOK(t, err)
	if fresh.UsedBytes != 1234 {
		t.Fatalf("上传后 UsedBytes 应为 1234，实际 %d", fresh.UsedBytes)
	}

	res, err := e.gallery.Delete(context.Background(), uid, false, alice, "", "")
	wantOK(t, err)
	if !res.Deleted {
		t.Fatal("应删除成功")
	}
	if res.FreedBytes != 1234 {
		t.Fatalf("FreedBytes 应为 1234，实际 %d", res.FreedBytes)
	}

	fresh, err = e.users.FindByUID(alice.UID)
	wantOK(t, err)
	if fresh.UsedBytes != 0 {
		t.Fatalf("删除后 UsedBytes 应归零，实际 %d", fresh.UsedBytes)
	}

	// 记录确实没了
	_, err = e.uploads.FindByUID(uid)
	if !isNotFound(err) {
		t.Fatalf("图片记录应已删除，实际 err=%v", err)
	}
}

// TestDeleteRemoteUnsupportedStillDeletesLocal 断言 D47：
// 驱动不支持远端删除时**仍删本地记录**，并如实回报。
func TestDeleteRemoteUnsupportedStillDeletesLocal(t *testing.T) {
	e := newW5Env(t) // RemoteDeleteSupported 默认 false
	e.startQueue()

	storage := e.makeStorage("默认", true)
	alice := e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)
	uid := e.uploadOne(alice, "a.png", 100, storage.UID)

	res, err := e.gallery.Delete(context.Background(), uid, true, alice, "", "")
	wantOK(t, err)

	if !res.Deleted {
		t.Fatal("本地记录必须删除（不因远端不支持而阻断）")
	}
	if res.RemoteDeleteSupported {
		t.Fatal("mock 驱动应报告不支持远端删除")
	}
	if res.RemoteDeleted {
		t.Fatal("不支持时 RemoteDeleted 必须为 false")
	}
	if res.RemoteDeleteError == "" {
		t.Fatal("应给出原因（便于 UI 提示用户）")
	}
	if res.FreedBytes != 100 {
		t.Fatalf("配额应照退（与远端是否删除无关），实际 %d", res.FreedBytes)
	}
}

// TestDeleteRemoteSupportedPassesRawImgInfo 断言 D47 的核心：
// 删除时交回插件的 IImgInfo **字段名必须是 picgo 原生的**（含插件回写的 sha）。
func TestDeleteRemoteSupportedPassesRawImgInfo(t *testing.T) {
	e := newW5Env(t, func(o *w5Options) { o.remoteDeleteSupported = true })
	e.startQueue()

	storage := e.makeStorage("默认", true)
	alice := e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)
	uid := e.uploadOne(alice, "withsha.png", 50, storage.UID)

	res, err := e.gallery.Delete(context.Background(), uid, true, alice, "", "")
	wantOK(t, err)
	if !res.RemoteDeleteSupported || !res.RemoteDeleted {
		t.Fatalf("应报告已远端删除：%+v", res)
	}

	items := e.agent.LastRemovedItems()
	if len(items) != 1 {
		t.Fatalf("应把 1 个 IImgInfo 交回插件，实际 %d", len(items))
	}
	if _, ok := items[0]["fileName"]; !ok {
		t.Fatalf("交回的 IImgInfo 应含 picgo 原生字段 fileName：%v", keysOfAny(items[0]))
	}
	if _, ok := items[0]["sha"]; !ok {
		t.Fatal("交回的 IImgInfo 必须含插件回写的 sha（否则 github 等插件删不掉）")
	}
}

// TestDeleteRemoteFailureKeepsLocal 断言远端删除失败**不阻断**本地删除。
func TestDeleteRemoteFailureKeepsLocal(t *testing.T) {
	e := newW5Env(t, func(o *w5Options) { o.remoteDeleteSupported = true })
	e.startQueue()

	storage := e.makeStorage("默认", true)
	alice := e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)
	uid := e.uploadOne(alice, "a.png", 30, storage.UID)

	// 抹掉 RawOutput，制造「无法删远端」的场景
	wantOK(t, e.uploads.SaveResult(uid, "", ""))

	res, err := e.gallery.Delete(context.Background(), uid, true, alice, "", "")
	wantOK(t, err)
	if !res.Deleted {
		t.Fatal("本地删除必须成功")
	}
	if res.RemoteDeleted {
		t.Fatal("缺少原始返回值时不应谎报已删除")
	}
	if res.RemoteDeleteError == "" {
		t.Fatal("应给出原因")
	}
}

func TestBatchDelete(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	storage := e.makeStorage("默认", true)
	alice := e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)
	bob := e.makeUser("usr_bob", "bob@example.com", model.UserRoleUser, nil)

	a := e.uploadOne(alice, "a.png", 10, storage.UID)
	b := e.uploadOne(alice, "b.png", 20, storage.UID)
	others := e.uploadOne(bob, "c.png", 30, storage.UID)

	res, err := e.gallery.BatchDelete(context.Background(),
		[]string{a, b, others, "up_nope"}, false, alice, "", "")
	wantOK(t, err)

	if res.Total != 4 {
		t.Fatalf("Total 应为 4，实际 %d", res.Total)
	}
	if res.Deleted != 2 {
		t.Fatalf("应删除自己的 2 张，实际 %d", res.Deleted)
	}
	if res.Skipped != 2 {
		t.Fatalf("应跳过 2 条（他人的 + 不存在的），实际 %d", res.Skipped)
	}
	if res.FreedBytes != 30 {
		t.Fatalf("FreedBytes 应为 30，实际 %d", res.FreedBytes)
	}

	// 他人的图片仍在
	if _, err := e.uploads.FindByUID(others); !isNotFound(err) && err != nil {
		t.Fatalf("他人图片不应受影响：%v", err)
	}
	_, err = e.gallery.Get(others, bob)
	wantOK(t, err)
}

func TestBatchDeleteLimit(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	alice := e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)
	many := make([]string, maxBatchDelete+1)
	for i := range many {
		many[i] = "up_x"
	}
	_, err := e.gallery.BatchDelete(context.Background(), many, false, alice, "", "")
	wantCode(t, err, response.CodeInvalidParam)
}

// ---- 重命名与移动相册 ----

// TestRenameOnlyChangesAlias 断言 PATCH 只改 `AliasName`，**不动远端文件名**。
//
// 理由（D42/D66 边界）：远端 URL 由图床决定，我们改了本地字段也改不了远端，
// 反而造成「名字对不上」的困惑。
func TestRenameOnlyChangesAlias(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	storage := e.makeStorage("默认", true)
	alice := e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)
	uid := e.uploadOne(alice, "orig.png", 10, storage.UID)

	before := e.findUpload(uid)

	newAlias := "封面图"
	after, err := e.gallery.Update(context.Background(), uid,
		GalleryUpdateInput{AliasName: &newAlias}, alice, "", "")
	wantOK(t, err)

	if after.AliasName != newAlias {
		t.Fatalf("别名未更新：%q", after.AliasName)
	}
	// 远端 URL 与真实文件名都不该变
	if after.URL != before.URL {
		t.Fatalf("URL 不应改变：%s → %s", before.URL, after.URL)
	}
	if after.FileName != before.FileName {
		t.Fatalf("远端文件名不应改变：%s → %s", before.FileName, after.FileName)
	}
}

// ---- 统计 ----

func TestGalleryStats(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	storage := e.makeStorage("默认", true)
	alice := e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)

	e.uploadOne(alice, "a.png", 100, storage.UID)
	e.uploadOne(alice, "b.jpg", 200, storage.UID)

	stats, err := e.gallery.Stats("mine", alice)
	wantOK(t, err)

	if stats.Total != 2 || stats.TotalSize != 300 {
		t.Fatalf("统计不符：total=%d size=%d", stats.Total, stats.TotalSize)
	}
	if stats.TodayCount != 2 {
		t.Fatalf("今日上传应为 2，实际 %d", stats.TodayCount)
	}
	if stats.SuccessCount != 2 || stats.FailedCount != 0 {
		t.Fatalf("状态计数不符：ok=%d fail=%d", stats.SuccessCount, stats.FailedCount)
	}
	if len(stats.ByStorage) != 1 || stats.ByStorage[0].Count != 2 {
		t.Fatalf("ByStorage 不符：%+v", stats.ByStorage)
	}
	if len(stats.ByExtension) != 2 {
		t.Fatalf("ByExtension 应有 2 项，实际 %+v", stats.ByExtension)
	}
	// 存储名应可读（不是 UID）
	if stats.ByStorage[0].Name != "默认" {
		t.Fatalf("ByStorage.Name 应为存储展示名，实际 %q", stats.ByStorage[0].Name)
	}
}

// ---- 筛选 ----

func TestGalleryFilters(t *testing.T) {
	e := newW5Env(t)
	e.startQueue()

	a := e.makeStorage("A", true)
	b := e.makeStorage("B", false)
	alice := e.makeUser("usr_alice", "alice@example.com", model.UserRoleUser, nil)

	e.uploadOne(alice, "cat.png", 10, a.UID)
	e.uploadOne(alice, "dog.png", 10, b.UID)

	// 按存储筛选
	_, total, err := e.gallery.List(GalleryListInput{StorageUID: a.UID}, alice)
	wantOK(t, err)
	if total != 1 {
		t.Fatalf("按存储筛选应为 1，实际 %d", total)
	}

	// 按关键词
	_, total, err = e.gallery.List(GalleryListInput{Keyword: "dog"}, alice)
	wantOK(t, err)
	if total != 1 {
		t.Fatalf("按关键词应为 1，实际 %d", total)
	}

	// AlbumUID=none（未归入相册）
	_, total, err = e.gallery.List(GalleryListInput{AlbumUID: "none"}, alice)
	wantOK(t, err)
	if total != 2 {
		t.Fatalf("两张都未归入相册，应为 2，实际 %d", total)
	}

	// 按状态
	_, total, err = e.gallery.List(GalleryListInput{Status: model.UploadStatusSuccess}, alice)
	wantOK(t, err)
	if total != 2 {
		t.Fatalf("成功状态应为 2，实际 %d", total)
	}
	_, total, err = e.gallery.List(GalleryListInput{Status: model.UploadStatusFailed}, alice)
	wantOK(t, err)
	if total != 0 {
		t.Fatalf("失败状态应为 0，实际 %d", total)
	}
}

// ---- SSRF 防护（from-url）----

func TestValidateFetchURLBlocksPrivate(t *testing.T) {
	e := newW5Env(t) // AllowPrivateFetch = false

	blocked := []string{
		"http://127.0.0.1/a.png",
		"http://localhost/a.png",
		"http://10.0.0.1/a.png",
		"http://192.168.1.1/a.png",
		"http://169.254.169.254/latest/meta-data/", // 云元数据端点
		"http://[::1]/a.png",
		"ftp://example.com/a.png", // 非 http(s)
		"not-a-url",
	}
	for _, u := range blocked {
		if err := e.upload.ValidateFetchURL(u); err == nil {
			t.Errorf("应拒绝 %s", u)
		}
	}
}

func TestValidateFetchURLAllowsWhenConfigured(t *testing.T) {
	e := newW5Env(t)
	e.cfg.AllowPrivateFetch = true // 内网图床场景

	if err := e.upload.ValidateFetchURL("http://192.168.1.10/a.png"); err != nil {
		t.Fatalf("开启允许内网后不应拒绝：%v", err)
	}
}

// isNotFound 判断错误是否为「记录不存在」（仓储层归一化后的错误）。
func isNotFound(err error) bool { return repository.IsNotFound(err) }
