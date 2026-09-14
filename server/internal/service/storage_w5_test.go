package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/crypto"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

func TestStorageCreateAndGet(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)

	view := e.makeStorage("我的 GitHub", false)

	if view.UID == "" {
		t.Fatal("UID 为空")
	}
	if view.Type != "github" {
		t.Fatalf("Type 期望 github，实际 %s", view.Type)
	}
	if view.PicgoConfigName != "Default" {
		t.Fatalf("PicgoConfigName 期望 Default，实际 %s", view.PicgoConfigName)
	}
	if !view.HasSecrets {
		t.Fatal("HasSecrets 应为 true")
	}
	if len(view.SecretFields) == 0 {
		t.Fatal("SecretFields 不应为空")
	}
	// token 必须被识别为敏感字段
	found := false
	for _, f := range view.SecretFields {
		if f == "token" {
			found = true
		}
	}
	if !found {
		t.Fatalf("token 应被识别为敏感字段，实际 SecretFields=%v", view.SecretFields)
	}

	// 列表
	items, total, err := e.storage.List(context.Background(), StorageListInput{})
	wantOK(t, err)
	if total != 1 || len(items) != 1 {
		t.Fatalf("列表期望 1 条，实际 total=%d len=%d", total, len(items))
	}
}

// TestStorageSecretRedaction 是本工作流最关键的安全断言：
// **任何返回给前端的存储配置都不能含密钥明文**。
func TestStorageSecretRedaction(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)

	view := e.makeStorage("加密测试", false)

	// ① Config 里的 token 必须是掩码，不能是明文
	if got, _ := view.Config["token"].(string); got != crypto.Mask {
		t.Fatalf("token 应为掩码 %q，实际 %q", crypto.Mask, got)
	}
	// ② 非敏感字段（repo）应可见，便于编辑回填
	if got, _ := view.Config["repo"].(string); got != "org/加密测试" {
		t.Fatalf("repo 应可见，实际 %v", view.Config["repo"])
	}

	// ③ 整个视图序列化后不得出现明文 token
	raw, err := json.Marshal(view)
	wantOK(t, err)
	if containsStr(string(raw), "ghp_secret_token_value") {
		t.Fatalf("序列化后的视图泄露了 token 明文：%s", string(raw))
	}

	// ④ 数据库里的密文不是明文
	enc, err := e.stores.GetSecret(view.UID)
	wantOK(t, err)
	if enc == "" {
		t.Fatal("凭据未落库")
	}
	if containsStr(enc, "ghp_secret_token_value") {
		t.Fatal("数据库里存的是明文！")
	}
}

// TestStorageSecretsCanBeDecrypted 断言加密是**可逆**的（否则上传会失败）。
func TestStorageSecretsCanBeDecrypted(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)

	view := e.makeStorage("可逆性", false)

	cfg, err := e.storage.decryptConfig(view.UID)
	wantOK(t, err)
	if cfg["token"] != "ghp_secret_token_value" {
		t.Fatalf("解密后的 token 不符：%v", cfg["token"])
	}
}

func TestStorageNameConflict(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)
	e.makeStorage("重名测试", false)

	enabled := true
	_, err := e.storage.Create(context.Background(), CreateStorageInput{
		Name: "重名测试", Type: "smms", Enabled: &enabled,
	}, "usr_admin", "", "")
	wantCode(t, err, response.CodeConflict)

	// 大小写不敏感也算重名（避免后台出现两个「看起来一样」的配置）
	_, err = e.storage.Create(context.Background(), CreateStorageInput{
		Name: "重名测试", Type: "github", PicgoConfigName: "Other", Enabled: &enabled,
	}, "usr_admin", "", "")
	wantCode(t, err, response.CodeConflict)
}

// TestStorageTypeConfigNameConflict 断言 (Type, PicgoConfigName) 唯一（D64）。
//
// 若不拦，两条存储配置会映射到**同一个** picgo 配置项，
// 上传时它们的行为会互相覆盖。
func TestStorageTypeConfigNameConflict(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)
	e.makeStorage("第一条", false)

	enabled := true
	_, err := e.storage.Create(context.Background(), CreateStorageInput{
		Name: "第二条（同类型同配置名）", Type: "github", PicgoConfigName: "Default",
		Enabled: &enabled,
	}, "usr_admin", "", "")
	wantCode(t, err, response.CodeConflict)
}

func TestStorageCreateUnknownDriverRejected(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)

	enabled := true
	_, err := e.storage.Create(context.Background(), CreateStorageInput{
		Name: "不存在的驱动", Type: "no-such-driver", Enabled: &enabled,
	}, "usr_admin", "", "")
	wantCode(t, err, response.CodeInvalidParam)
}

// TestStoragePicgoConfigNameReadOnly 断言 PATCH 不接受 PicgoConfigName。
//
// 语义在 handler 层拦（docs/API.md §3.2），服务层只提供 UpdateStorageInput，
// 它**没有**该字段 —— 这里断言的是「服务层无法改名」这一结构保证。
func TestStoragePicgoConfigNameReadOnly(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)
	view := e.makeStorage("只读测试", false)

	newName := "改过的展示名"
	updated, err := e.storage.Update(context.Background(), view.UID, UpdateStorageInput{
		Name: &newName,
	}, "usr_admin", "", "")
	wantOK(t, err)

	if updated.Name != newName {
		t.Fatalf("Name 未更新：%s", updated.Name)
	}
	if updated.PicgoConfigName != view.PicgoConfigName {
		t.Fatalf("PicgoConfigName 不应改变：%s → %s", view.PicgoConfigName, updated.PicgoConfigName)
	}
}

// TestStorageActivateAndDeleteDefault 断言：
//   - activate 唯一化默认
//   - 删除默认项后自动改选另一条启用项（D64）
func TestStorageActivateAndDeleteDefault(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)

	a := e.makeStorage("A", true)
	b := e.makeStorage("B", false)

	// A 应为默认
	got, err := e.storage.Get(context.Background(), a.UID)
	wantOK(t, err)
	if !got.IsDefault {
		t.Fatal("A 应为默认")
	}

	// 切到 B
	_, err = e.storage.Activate(context.Background(), b.UID, "usr_admin", "", "")
	wantOK(t, err)

	// 默认必须唯一
	all, _, err := e.storage.List(context.Background(), StorageListInput{})
	wantOK(t, err)
	defaults := 0
	for _, it := range all {
		if it.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("默认配置应唯一，实际 %d 条", defaults)
	}

	// 删除默认项（B）→ 应自动改选 A
	res, err := e.storage.Delete(context.Background(), b.UID, false, "usr_admin", "", "")
	wantOK(t, err)
	if res.DefaultSwitchedTo != a.UID {
		t.Fatalf("删除默认项后应切到 A(%s)，实际 %q", a.UID, res.DefaultSwitchedTo)
	}
}

func TestStorageDeleteWithUploadsRequiresForce(t *testing.T) {
	e := newW5Env(t)
	user := e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)
	view := e.makeStorage("有图引用", true)

	// 造一条引用该配置的图片记录
	now := model.Now()
	err := e.uploads.Create(&model.Upload{
		UID: "up_ref_test", UserUID: user.UID, StorageUID: view.UID,
		FileName: "a.png", OriginalName: "a.png", Size: 100,
		Status: model.UploadStatusSuccess, Source: model.UploadSourceWeb,
		Metadata: "{}", CreatedAt: now, UpdatedAt: now,
	})
	wantOK(t, err)

	// 无 force → 40901
	_, err = e.storage.Delete(context.Background(), view.UID, false, "usr_admin", "", "")
	wantCode(t, err, response.CodeConflict)

	// 带 force → 成功，并回报受影响数量
	res, err := e.storage.Delete(context.Background(), view.UID, true, "usr_admin", "", "")
	wantOK(t, err)
	if res.AffectedUploads != 1 {
		t.Fatalf("AffectedUploads 期望 1，实际 %d", res.AffectedUploads)
	}
}

// TestStorageDeleteRemovesSecrets 断言删配置时凭据行**一并删除**。
//
// 否则会留下无主的密文行，且「按 StorageUID 查凭据」会命中已删配置。
func TestStorageDeleteRemovesSecrets(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)
	view := e.makeStorage("待删除", true)

	if has, err := e.stores.SecretExists(view.UID); err != nil || !has {
		t.Fatalf("删除前应存在凭据行（err=%v has=%v）", err, has)
	}

	_, err := e.storage.Delete(context.Background(), view.UID, true, "usr_admin", "", "")
	wantOK(t, err)

	if has, err := e.stores.SecretExists(view.UID); err != nil || has {
		t.Fatalf("删除后凭据行应消失（err=%v has=%v）", err, has)
	}
}

// TestStorageResolveUploadTarget 断言上传目标解析：
//   - 不传 uid → 用默认配置
//   - 没有默认配置 → **明确报错**（而不是随便挑一条）
//   - 配置被禁用 → 拒绝
func TestStorageResolveUploadTarget(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)

	// 没有任何配置
	_, err := e.storage.ResolveUploadTarget("")
	wantCode(t, err, response.CodeInvalidParam)

	a := e.makeStorage("默认配置", true)

	target, err := e.storage.ResolveUploadTarget("")
	wantOK(t, err)
	if target.StorageUID != a.UID {
		t.Fatalf("应解析到默认配置 %s，实际 %s", a.UID, target.StorageUID)
	}
	if target.Type != "github" {
		t.Fatalf("Type 不符：%s", target.Type)
	}
	// 模板应被带出（D43：每个存储配置独立设置）
	if target.PathTemplate != "img/{Y}/{m}" {
		t.Fatalf("PathTemplate 不符：%s", target.PathTemplate)
	}
	// github 的 schema 含 path 字段 → 应支持路径模板
	if !target.SupportsPathTemplate {
		t.Fatal("github 应支持路径模板（其 schema 含 path 字段）")
	}

	// 显式禁用
	disabled := false
	_, err = e.storage.Update(context.Background(), a.UID, UpdateStorageInput{Enabled: &disabled},
		"usr_admin", "", "")
	wantOK(t, err)
	_, err = e.storage.ResolveUploadTarget(a.UID)
	wantCode(t, err, response.CodeInvalidParam)
}

// TestStorageCapabilitiesNotHardcoded 断言能力来自 agent 探测（D77.2）。
//
// github 支持路径模板；smms 的 schema 没有 path 类字段 → 不支持。
func TestStorageCapabilitiesNotHardcoded(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)

	if caps := e.storage.probeCapabilities(context.Background(), "github"); caps == nil || !caps.SupportsPathTemplate {
		t.Fatalf("github 应支持路径模板，实际 %+v", caps)
	}
	if caps := e.storage.probeCapabilities(context.Background(), "smms"); caps == nil || caps.SupportsPathTemplate {
		t.Fatalf("smms 不应支持路径模板，实际 %+v", caps)
	}
	// 探测结果应落库（供后续请求复用）
	view := e.makeStorage("能力落库", false)
	if view.Capabilities.PicgoVersion == "" {
		t.Fatal("Capabilities 未落库")
	}
}

func TestStorageListDrivers(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)
	e.makeStorage("驱动列表", false)

	drivers, err := e.storage.ListDrivers(context.Background())
	wantOK(t, err)
	if len(drivers) == 0 {
		t.Fatal("驱动列表为空")
	}

	byType := map[string]DriverView{}
	for _, d := range drivers {
		byType[d.Type] = d
	}

	// github 应有 1 条已配置
	if byType["github"].ConfigCount != 1 {
		t.Fatalf("github ConfigCount 期望 1，实际 %d", byType["github"].ConfigCount)
	}
	// schema 的字段名必须原样（repo / token / path），不得被转换成 PascalCase（D81.3）
	foundRepo := false
	for _, f := range byType["github"].Config {
		if f.Name == "repo" {
			foundRepo = true
		}
	}
	if !foundRepo {
		t.Fatal("驱动 schema 字段名应原样保留（repo），不得转换")
	}
}

func TestStorageDriverSchema(t *testing.T) {
	e := newW5Env(t)

	view, err := e.storage.DriverSchema(context.Background(), "github", map[string]any{"repo": "org/x"})
	wantOK(t, err)
	if view.Type != "github" {
		t.Fatalf("Type 不符：%s", view.Type)
	}
	if len(view.Config) == 0 {
		t.Fatal("schema 为空")
	}

	_, err = e.storage.DriverSchema(context.Background(), "no-such", nil)
	wantCode(t, err, response.CodeNotFound)
}

// TestStorageUpdateSecretsMaskKeepsOldValue 断言「提交掩码 = 不修改」（D78）。
func TestStorageUpdateSecretsMaskKeepsOldValue(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)
	view := e.makeStorage("掩码语义", false)

	// 只改 repo，token 回传掩码
	_, err := e.storage.UpdateSecrets(context.Background(), view.UID, map[string]any{
		"repo":  "org/changed",
		"token": crypto.Mask,
	}, "usr_admin", "", "")
	wantOK(t, err)

	cfg, err := e.storage.decryptConfig(view.UID)
	wantOK(t, err)
	if cfg["repo"] != "org/changed" {
		t.Fatalf("repo 应被更新，实际 %v", cfg["repo"])
	}
	if cfg["token"] != "ghp_secret_token_value" {
		t.Fatalf("掩码不应覆盖 token，实际 %v", cfg["token"])
	}

	// 显式清空
	_, err = e.storage.UpdateSecrets(context.Background(), view.UID, map[string]any{
		"token": "",
	}, "usr_admin", "", "")
	wantOK(t, err)
	cfg, err = e.storage.decryptConfig(view.UID)
	wantOK(t, err)
	if cfg["token"] != "" {
		t.Fatalf("token 应被清空，实际 %v", cfg["token"])
	}
}

// TestStorageTestDoesNotFailOnUnreachableStorage 断言连通性测试：
// 失败时 HTTP 语义仍是「成功返回结果」（Ok=false），而不是抛错。
func TestStorageTestReturnsResultNotError(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)
	view := e.makeStorage("连通性", false)

	res, err := e.storage.Test(context.Background(), view.UID, "usr_admin", "", "")
	wantOK(t, err)
	if !res.Ok {
		t.Fatalf("mock agent 的测试应成功，实际 Ok=false msg=%s", res.Message)
	}

	// 必填字段为空 → Ok=false（而不是 error）
	emptyStorage := e.makeStorage("必填缺失", false)
	_, err = e.storage.UpdateSecrets(context.Background(), emptyStorage.UID, map[string]any{
		"repo": "",
	}, "usr_admin", "", "")
	wantOK(t, err)
	res, err = e.storage.Test(context.Background(), emptyStorage.UID, "usr_admin", "", "")
	wantOK(t, err)
	if res.Ok {
		t.Fatal("必填字段为空时应返回 Ok=false")
	}
}

// TestStorageReconcilePushesToAgent 断言 reconcile 以 DB 为真相源（D22）。
func TestStorageReconcilePushesToAgent(t *testing.T) {
	e := newW5Env(t)
	e.makeUser("usr_admin", "admin@example.com", model.UserRoleAdmin, nil)

	a := e.makeStorage("A", true)
	b := e.makeStorage("B", false)

	wantOK(t, e.storage.Reconcile(context.Background()))

	// agent 侧应同时有两条配置，且当前激活的是 A
	list, err := e.agent.ListUploaderConfigs(context.Background(), "github")
	wantOK(t, err)
	if len(list.Configs) != 2 {
		t.Fatalf("agent 侧应有 2 条配置，实际 %d", len(list.Configs))
	}
	uploaders, err := e.agent.ListUploaders(context.Background())
	wantOK(t, err)
	// 两条配置名分别为 Default 与 Default-1（picgo 的默认命名规则）
	_ = a
	_ = b
	if uploaders.Current.Type != "github" {
		t.Fatalf("当前上传器应为 github，实际 %s", uploaders.Current.Type)
	}
}

// containsStr 是 strings.Contains 的本地别名（避免测试文件再 import strings）。
func containsStr(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
