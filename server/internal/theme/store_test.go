package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// ---- seed（D94.4 / OPERATIONS §8.3）----

func TestSeedWritesEmbeddedDefaultTheme(t *testing.T) {
	env := newTestEnv(t)
	dir := env.seedDefault()

	// manifest.json 与 index.html 必须齐全
	for _, name := range []string{ManifestFile, IndexFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("seed 后缺少 %s: %v", name, err)
		}
	}

	// 能被扫描并识别为合法主题，且 ID 与目录名一致
	doc := readManifestFile(t, filepath.Join(dir, ManifestFile))
	if doc.ID != embeddedDefaultID {
		t.Errorf("ID = %q，期望 %q", doc.ID, embeddedDefaultID)
	}
	if err := doc.Validate(embeddedDefaultID, true); err != nil {
		t.Errorf("内嵌默认主题应当合法: %v", err)
	}

	// 默认主题只注册首页（D94）
	pages := doc.EffectivePages()
	if len(pages) != 1 || pages[0] != "/" {
		t.Errorf("内嵌默认主题的 Pages 应为 [\"/\"]，实际 %v", pages)
	}
}

func TestSeedIsIdempotentAndNeverOverwrites(t *testing.T) {
	env := newTestEnv(t)
	env.seedDefault()

	// 用户改了自己的 default 主题
	custom := "USER-CUSTOMISED"
	if err := os.WriteFile(filepath.Join(env.ThemesDir, embeddedDefaultID, IndexFile),
		[]byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	// 再次 seed：目录非空 → 什么都不做
	seeded, err := env.Svc.Seed()
	if err != nil {
		t.Fatalf("第二次 seed 报错: %v", err)
	}
	if seeded {
		t.Error("目录非空时不应再 seed（升级不得覆盖用户主题）")
	}

	got, err := os.ReadFile(filepath.Join(env.ThemesDir, embeddedDefaultID, IndexFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != custom {
		t.Error("用户对 default 主题的修改被覆盖了")
	}
}

func TestSeedSkipsWhenOtherThemeExists(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("mine", `"Pages":["/"]`, "<html>mine</html>")

	seeded, err := env.Svc.Seed()
	if err != nil {
		t.Fatal(err)
	}
	if seeded {
		t.Error("目录里已有其他主题时不应 seed")
	}
}

func TestSeedCreatesThemesDirIfMissing(t *testing.T) {
	env := newTestEnv(t)
	if dirExists(env.ThemesDir) {
		t.Fatal("前置条件：主题目录初始不存在")
	}
	env.seedDefault()
	if !dirExists(env.ThemesDir) {
		t.Error("seed 应创建主题目录")
	}
}

// ---- 扫描（OPERATIONS §8.4）----

func TestScanFindsValidThemes(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("alpha", `"Pages":["/","/gallery"]`, "<html>A</html>")
	env.writeTheme("beta", `"Pages":["/upload"]`, "<html>B</html>")

	res := env.Svc.List("zh-CN")

	byID := map[string]ListItem{}
	for _, it := range res.Items {
		byID[it.ID] = it
	}

	for _, id := range []string{"alpha", "beta"} {
		it, ok := byID[id]
		if !ok {
			t.Fatalf("未扫到主题 %s", id)
		}
		if !it.Valid {
			t.Errorf("主题 %s 应合法: %s", id, it.Error)
		}
		if it.SettingCount == 0 && id == "alpha" {
			// alpha 没声明配置项，SettingCount 为 0 正常
		}
		if !it.CanUninstall {
			t.Errorf("主题 %s 应可卸载", id)
		}
	}

	// 内嵌 default 应当总是出现在列表里（兜底锚点）
	def, ok := byID[embeddedDefaultID]
	if !ok {
		t.Fatal("列表里应有内嵌默认主题（兜底锚点）")
	}
	if !def.IsBuiltin {
		t.Error("default 应标记为内置")
	}
	if def.CanUninstall {
		t.Error("内置主题不可卸载")
	}

	if byID["alpha"].Pages[1] != "/gallery" {
		t.Errorf("alpha 的 Pages 应为 [/, /gallery]，实际 %v", byID["alpha"].Pages)
	}
}

func TestScanReportsInvalidThemesWithError(t *testing.T) {
	env := newTestEnv(t)

	// 1) ID 与目录名不一致
	dir := env.writeTheme("mismatch", ``, "<html>x</html>")
	body := `{"ID":"other","Name":"n"}`
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2) 缺 index.html
	env.writeTheme("noindex", ``, "")

	// 3) manifest 不是合法 JSON
	broken := filepath.Join(env.ThemesDir, "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, ManifestFile), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, IndexFile), []byte("<html/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 4) Pages 命中认证页
	env.writeTheme("hijack", `"Pages":["/login"]`, "<html>evil</html>")

	env.Auditor.reset()
	res := env.Svc.List("zh-CN")

	byID := map[string]ListItem{}
	for _, it := range res.Items {
		byID[it.ID] = it
	}

	for _, id := range []string{"mismatch", "noindex", "broken", "hijack"} {
		it, ok := byID[id]
		if !ok {
			t.Fatalf("不合法主题 %s 也应出现在列表里（否则后台无法发现）", id)
		}
		if it.Valid {
			t.Errorf("主题 %s 应标记不合法", id)
		}
		if it.Error == "" {
			t.Errorf("主题 %s 应带错误原因", id)
		}
		if it.CanUninstall == false {
			t.Errorf("不合法主题 %s 应允许卸载（这是管理员的主修复手段）", id)
		}
	}

	// 每条不合法都应留下 theme.error 审计
	if got := env.Auditor.count(model.LogTypeThemeError); got != 4 {
		t.Errorf("应有 4 条 theme.error，实际 %d", got)
	}

	// 尤其：尝试接管 /login 的主题必须被拒（安全底线）
	if it := byID["hijack"]; !strings.Contains(it.Error, "/login") {
		t.Errorf("hijack 的错误应指出冲突路径，实际 %q", it.Error)
	}
}

func TestScanIgnoresFilesAndHiddenDirs(t *testing.T) {
	env := newTestEnv(t)
	if err := os.MkdirAll(env.ThemesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 普通文件（非目录）不应被当成主题
	if err := os.WriteFile(filepath.Join(env.ThemesDir, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// .tmp-* 是安装过程中的临时目录，不应出现在列表里
	if err := os.MkdirAll(filepath.Join(env.ThemesDir, ".tmp-abc"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 点开头的隐藏目录同样忽略
	if err := os.MkdirAll(filepath.Join(env.ThemesDir, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}

	res := env.Svc.List("zh-CN")
	for _, it := range res.Items {
		if it.ID == "readme.txt" || it.ID == ".tmp-abc" || it.ID == ".hidden" {
			t.Errorf("不应出现在列表：%s", it.ID)
		}
	}
}

// ---- 装载与兜底（OPERATIONS §8.7）----

func TestLoadValidTheme(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("alpha", `"Pages":["/","/gallery"]`, themeIndexMarker)

	th, err := env.Svc.Store().Load("alpha")
	if err != nil {
		t.Fatalf("装载失败: %v", err)
	}
	if th.ID != "alpha" || th.Builtin || th.Fallback || th.Embedded {
		t.Errorf("装载结果异常: %+v", th)
	}
	if got := th.Pages(); len(got) != 2 {
		t.Errorf("Pages = %v", got)
	}

	data, err := th.ReadFile(IndexFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != themeIndexMarker {
		t.Errorf("index.html 内容不符: %q", string(data))
	}
}

func TestLoadMissingTheme(t *testing.T) {
	env := newTestEnv(t)
	if _, err := env.Svc.Store().Load("nope"); err == nil {
		t.Fatal("不存在的主题应当报错")
	}
	// 非法的 ID 形态同样拒绝
	for _, bad := range []string{"../etc", "a/b", "a\\b"} {
		if _, err := env.Svc.Store().Load(bad); err == nil {
			t.Errorf("非法 ID %q 应当被拒绝", bad)
		}
	}
}

func TestResolveFallsBackToEmbedded(t *testing.T) {
	env := newTestEnv(t)

	// 磁盘上有一个坏掉的 default（manifest 非法）
	env.writeTheme(embeddedDefaultID, `"Pages":["/login"]`, "<html>broken</html>")

	// 当前主题 = default（分辨率下应回退内嵌副本）
	th := env.Svc.Current()
	if th == nil {
		t.Fatal("Current() 不应返回 nil")
	}
	if !th.Fallback {
		t.Error("应当标记为回退")
	}
	if th.FallbackReason == "" {
		t.Error("回退时应带原因")
	}

	// 仍然能拿到可用的 index.html（永不白屏）
	if _, err := env.Svc.CurrentIndex(); err != nil {
		t.Errorf("回退后应能读到 index.html: %v", err)
	}
}

func TestResolveFallsBackWhenThemeMissing(t *testing.T) {
	env := newTestEnv(t)

	// 把 theme.active 指到一个不存在的主题
	if err := env.Settings.Set(KeyActiveTheme, "ghost", "test"); err != nil {
		t.Fatal(err)
	}

	th := env.Svc.Current()
	if !th.Fallback || !th.Embedded {
		t.Errorf("不存在的主题应回退内嵌副本，实际 fallback=%v embedded=%v", th.Fallback, th.Embedded)
	}

	// 首页仍可渲染
	if _, err := env.Svc.CurrentIndex(); err != nil {
		t.Errorf("兜底后 index.html 应可用: %v", err)
	}
}

func TestFallbackLogsThemeErrorOnce(t *testing.T) {
	env := newTestEnv(t)
	if err := env.Settings.Set(KeyActiveTheme, "ghost", "test"); err != nil {
		t.Fatal(err)
	}
	env.Auditor.reset()

	// 分发路径会频繁调用 Current：多次调用只应写一条 theme.error（防日志刷屏）
	for i := 0; i < 5; i++ {
		_ = env.Svc.Current()
	}
	if got := env.Auditor.count(model.LogTypeThemeError); got != 1 {
		t.Errorf("同一原因只应写一条 theme.error，实际 %d", got)
	}
}

func TestEmbeddedThemeAvailable(t *testing.T) {
	env := newTestEnv(t)
	th, err := env.Svc.Store().Embedded()
	if err != nil {
		t.Fatalf("内嵌默认主题不可用（构建异常）: %v", err)
	}
	if !th.Builtin || !th.Embedded {
		t.Errorf("内嵌主题应标记 Builtin+Embedded: %+v", th)
	}
	data, err := th.ReadFile(IndexFile)
	if err != nil {
		t.Fatalf("内嵌主题缺 index.html: %v", err)
	}
	if len(data) == 0 {
		t.Error("内嵌主题 index.html 为空")
	}
}

// ---- 分发（D99.1）----

func TestDecide(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", `"Pages":["/","/gallery"]`, themeIndexMarker)
	if err := env.Svc.SetActive(t.Context(), "demo", "test"); err != nil {
		t.Fatalf("切换主题失败: %v", err)
	}

	cases := []struct {
		path string
		want Target
	}{
		// 主题注册的路径
		{"/", TargetTheme},
		{"/gallery", TargetTheme},
		{"/gallery/up_123", TargetTheme}, // 前缀匹配覆盖子路径
		{"/gallery/", TargetTheme},

		// 未注册的业务页面 → 内置 SPA
		{"/upload", TargetSPA},
		{"/albums", TargetSPA},
		{"/jobs", TargetSPA},
		{"/logs", TargetSPA},
		{"/settings", TargetSPA},

		// 认证页与后台 → **永远**内置 SPA（安全底线）
		{"/login", TargetSPA},
		{"/login/sub", TargetSPA},
		{"/first-login", TargetSPA},
		{"/forgot-password", TargetSPA},
		{"/reset-password", TargetSPA},
		{"/logout", TargetSPA},
		{"/admin", TargetSPA},
		{"/admin/users", TargetSPA},
		{"/admin/themes", TargetSPA},

		// 边界：不该被前缀误伤
		{"/galleryx", TargetSPA},
		{"/administrator", TargetSPA},
	}
	for _, tc := range cases {
		if got := env.Svc.Decide(tc.path); got.Target != tc.want {
			t.Errorf("Decide(%q) = %v，期望 %v", tc.path, got.Target, tc.want)
		}
	}
}

func TestDecideIgnoresQueryAndTrailingSlash(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", `"Pages":["/gallery"]`, themeIndexMarker)
	if err := env.Svc.SetActive(t.Context(), "demo", "test"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/gallery?page=2", "/gallery/?x=1", "/gallery/", "//gallery"} {
		if got := env.Svc.Decide(p); got.Target != TargetTheme {
			t.Errorf("Decide(%q) 应命中主题，实际 %v", p, got.Target)
		}
	}
}

func TestDecideAuthStaysSPAEvenIfPagesAttempted(t *testing.T) {
	env := newTestEnv(t)

	// 手工把 default 主题的 Pages 改成 ["/login"]（绕过校验，模拟「文件被直接编辑」）
	env.seedDefault()
	dir := filepath.Join(env.ThemesDir, embeddedDefaultID)
	evil := `{"ID":"default","Name":"evil","Pages":["/login"]}`
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(evil), 0o644); err != nil {
		t.Fatal(err)
	}

	// default 主题因此不合法 → 回退内嵌副本（Pages=["/"]）
	// /login 无论如何都必须是 SPA
	if got := env.Svc.Decide("/login"); got.Target != TargetSPA {
		t.Errorf("/login 必须由内置 SPA 渲染，实际 %v", got.Target)
	}
	if got := env.Svc.Decide("/admin/users"); got.Target != TargetSPA {
		t.Errorf("/admin/users 必须由内置 SPA 渲染，实际 %v", got.Target)
	}
	// 而首页仍由（回退后的）主题渲染
	if got := env.Svc.Decide("/"); got.Target != TargetTheme {
		t.Errorf("/ 应由主题渲染，实际 %v", got.Target)
	}
}

// ---- 主题资源与路径穿越 ----

func TestCurrentAsset(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", `"Pages":["/"]`, themeIndexMarker)
	env.writeAsset("demo", "index-abc.js", "console.log(1)")
	env.writeAsset("demo", "nested/deep.css", "body{}")
	if err := env.Svc.SetActive(t.Context(), "demo", "test"); err != nil {
		t.Fatal(err)
	}

	data, ctype, err := env.Svc.CurrentAsset("index-abc.js")
	if err != nil {
		t.Fatalf("读取资源失败: %v", err)
	}
	if string(data) != "console.log(1)" {
		t.Errorf("内容不符: %q", string(data))
	}
	if ctype != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q", ctype)
	}

	// 子目录
	if _, ctype, err := env.Svc.CurrentAsset("nested/deep.css"); err != nil {
		t.Errorf("子目录资源应可读: %v", err)
	} else if ctype != "text/css; charset=utf-8" {
		t.Errorf("Content-Type = %q", ctype)
	}

	// 不存在 → ErrNotFound（**不**回退内嵌主题的同名文件）
	if _, _, err := env.Svc.CurrentAsset("nope.js"); err == nil {
		t.Error("不存在的资源应报错")
	}
}

func TestCurrentAssetRejectsTraversal(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", `"Pages":["/"]`, themeIndexMarker)
	env.writeAsset("demo", "ok.js", "x")
	if err := env.Svc.SetActive(t.Context(), "demo", "test"); err != nil {
		t.Fatal(err)
	}

	// 在主题目录外放一个「机密」文件，确认读不到
	secret := filepath.Join(env.ThemesDir, "..", "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP-SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}

	bad := []string{
		"../manifest.json",
		"../../secret.txt",
		"..%2fmanifest.json", // 不做 URL 解码，按字面处理 → 路径里没有 .. 段，但也不存在该文件
		"/etc/passwd",
		"a/../../secret.txt",
		"./../secret.txt",
		"nested/../../secret.txt",
	}
	for _, rel := range bad {
		data, _, err := env.Svc.CurrentAsset(rel)
		if err == nil {
			t.Errorf("路径 %q 应当被拒绝，但读到了 %q", rel, string(data))
			continue
		}
		if strings.Contains(string(data), "TOP-SECRET") {
			t.Errorf("路径 %q 泄露了主题目录外的文件！", rel)
		}
	}
}

func TestCurrentAssetRejectsAbsoluteAndEmpty(t *testing.T) {
	env := newTestEnv(t)
	env.seedDefault()

	for _, rel := range []string{"", "/", "/index.html", "index.html\x00.png"} {
		if _, _, err := env.Svc.CurrentAsset(rel); err == nil {
			t.Errorf("路径 %q 应当被拒绝", rel)
		}
	}
}

func TestCurrentFavicon(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", `"Pages":["/"]`, themeIndexMarker)
	env.writeAsset("demo", "favicon.ico", "ICO-BYTES")
	if err := env.Svc.SetActive(t.Context(), "demo", "test"); err != nil {
		t.Fatal(err)
	}

	data, ctype, err := env.Svc.CurrentFavicon()
	if err != nil {
		t.Fatalf("应能读到主题 favicon: %v", err)
	}
	if string(data) != "ICO-BYTES" || ctype != "image/x-icon" {
		t.Errorf("favicon 内容/类型不符: %q %q", string(data), ctype)
	}

	// 主题没有 favicon 时应报错（由调用方回退内置 SPA）
	env.writeTheme("plain", `"Pages":["/"]`, themeIndexMarker)
	if err := env.Svc.SetActive(t.Context(), "plain", "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.Svc.CurrentFavicon(); err == nil {
		t.Error("没有 favicon 时应报错，让调用方回退内置")
	}
}

// ---- Screenshot ----

func TestScreenshot(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", `"Pages":["/"]`, themeIndexMarker)
	dir := filepath.Join(env.ThemesDir, "demo")
	if err := os.WriteFile(filepath.Join(dir, ScreenshotFile), []byte("PNG"), 0o644); err != nil {
		t.Fatal(err)
	}

	data, ctype, err := env.Svc.Screenshot("demo")
	if err != nil {
		t.Fatalf("读取预览图失败: %v", err)
	}
	if string(data) != "PNG" || ctype != "image/png" {
		t.Errorf("预览图内容/类型不符: %q %q", string(data), ctype)
	}

	// 没有预览图 → ErrNotFound
	env.writeTheme("nopic", `"Pages":["/"]`, themeIndexMarker)
	if _, _, err := env.Svc.Screenshot("nopic"); err == nil {
		t.Error("没有预览图时应返回错误")
	}
}

func TestScreenshotRejectsTraversalInPreview(t *testing.T) {
	env := newTestEnv(t)
	// manifest 里把 Preview 指到主题目录之外
	env.writeTheme("demo", `"Preview":"../../../etc/passwd"`, themeIndexMarker)

	if _, _, err := env.Svc.Screenshot("demo"); err == nil {
		t.Error("越界的 Preview 应当被拒绝")
	}
}

// ---- 辅助 ----

// readManifestFile 读并解析一个 manifest 文件（测试助手）。
func readManifestFile(t *testing.T, path string) *Manifest {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	m, err := ParseManifest(raw, defaultMaxManifestBytes)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", path, err)
	}
	return m
}
