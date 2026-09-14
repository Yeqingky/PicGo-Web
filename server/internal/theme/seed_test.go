package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// ---- seed 源（PICGO_WEB_THEME_SEED）----

// seedSourceEnv 造一个「外部 seed 源目录」，模拟 `make theme` 的产物。
func seedSourceEnv(t *testing.T, env *testEnv) string {
	t.Helper()

	src := filepath.Join(env.DataDir, "themes-build", "default")
	if err := os.MkdirAll(filepath.Join(src, AssetsDir), 0o755); err != nil {
		t.Fatal(err)
	}

	// 一个「完整版」默认主题：manifest 与内嵌副本同 ID，但内容不同
	manifest := `{"ID":"default","Name":"完整版默认主题","Version":"9.9.9","Pages":["/"],
	  "Configuration":{"Type":"managed","Items":[{"Key":"BackgroundURL","Name":"背景图","Type":"string","Default":"https://seed/bg.png"}]}}`
	if err := os.WriteFile(filepath.Join(src, ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, IndexFile), []byte("<html>SEED-SOURCE-BUILD</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, AssetsDir, "app.js"), []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}
	return src
}

func TestSeedUsesConfiguredSeedSource(t *testing.T) {
	env := newTestEnv(t)
	src := seedSourceEnv(t, env)

	store := NewStore(StoreOptions{
		ThemesDir: env.ThemesDir,
		SeedFrom:  src,
		Log:       env.Svc.log,
		Auditor:   env.Auditor,
	})

	seeded, err := store.Seed()
	if err != nil {
		t.Fatalf("seed 失败: %v", err)
	}
	if !seeded {
		t.Fatal("空目录应执行 seed")
	}

	// 内容应来自 seed 源（而不是内嵌副本）
	got, err := os.ReadFile(filepath.Join(env.ThemesDir, embeddedDefaultID, IndexFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "<html>SEED-SOURCE-BUILD</html>" {
		t.Errorf("seed 应使用配置的源目录，实际内容 %q", string(got))
	}

	// assets 也要一起复制
	if _, err := os.Stat(filepath.Join(env.ThemesDir, embeddedDefaultID, AssetsDir, "app.js")); err != nil {
		t.Errorf("seed 源里的 assets 未被复制: %v", err)
	}

	// 版本应来自 seed 源
	th, err := store.Load(embeddedDefaultID)
	if err != nil {
		t.Fatal(err)
	}
	if th.Manifest.Version != "9.9.9" {
		t.Errorf("Version = %q，期望 9.9.9", th.Manifest.Version)
	}
}

func TestSeedSourceForcesPermissions(t *testing.T) {
	env := newTestEnv(t)
	src := seedSourceEnv(t, env)

	// 源文件故意给宽权限
	loose := filepath.Join(src, IndexFile)
	if err := os.Chmod(loose, 0o777); err != nil {
		t.Fatal(err)
	}

	store := NewStore(StoreOptions{ThemesDir: env.ThemesDir, SeedFrom: src, Log: env.Svc.log})
	if _, err := store.Seed(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(env.ThemesDir, embeddedDefaultID, IndexFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("seed 后的文件权限应为 0644，实际 %o", perm)
	}
}

func TestSeedMissingSourceFailsLoudly(t *testing.T) {
	env := newTestEnv(t)

	store := NewStore(StoreOptions{
		ThemesDir: env.ThemesDir,
		SeedFrom:  filepath.Join(env.DataDir, "does-not-exist"),
		Log:       env.Svc.log,
	})

	// 配置了但不存在的 seed 源：必须明确报错，而不是静默改用内嵌副本 ——
	// 否则运维会以为自己的主题生效了。
	if _, err := store.Seed(); err == nil {
		t.Fatal("seed 源不存在时应报错")
	}
}

func TestSeedSourceIgnoredWhenThemesExist(t *testing.T) {
	env := newTestEnv(t)
	src := seedSourceEnv(t, env)
	env.writeTheme("mine", `"Pages":["/"]`, "<html>mine</html>")

	store := NewStore(StoreOptions{ThemesDir: env.ThemesDir, SeedFrom: src, Log: env.Svc.log})
	seeded, err := store.Seed()
	if err != nil {
		t.Fatal(err)
	}
	if seeded {
		t.Error("目录非空时不应 seed（即使配了 seed 源）")
	}
	if dirExists(filepath.Join(env.ThemesDir, embeddedDefaultID)) {
		t.Error("不应写入 default 目录")
	}
}

// ---- 回退提示（嵌入式首页的横幅）----

// TestEmbeddedIndexShowsFallbackBanner 断言内嵌首页在收到 ThemeError 时会提示「内置兜底」。
//
// 为什么不写死一句「这是内置兜底首页」：本文件同时是 themes/default/ 的 seed 源，
// 写死会让默认部署下每个用户都看到无意义的警告。因此提示由 ThemeError 驱动。
func TestEmbeddedIndexShowsFallbackBanner(t *testing.T) {
	raw, err := embeddedFile(IndexFile)
	if err != nil {
		t.Fatalf("读取内嵌首页失败: %v", err)
	}
	html := string(raw)

	for _, want := range []string{"fallbackWrap", "fallbackNote", "ThemeError", "内置默认首页"} {
		if !strings.Contains(html, want) {
			t.Errorf("内嵌首页应包含回退提示相关代码 %q", want)
		}
	}
}

// TestEmbeddedIndexFetchesSiteConfig 断言首页数据来自 /site/config（不硬编码站点信息）。
func TestEmbeddedIndexFetchesSiteConfig(t *testing.T) {
	raw, err := embeddedFile(IndexFile)
	if err != nil {
		t.Fatal(err)
	}
	html := string(raw)

	if !strings.Contains(html, "/api/web/v1/site/config") {
		t.Error("内嵌首页应调用 /api/web/v1/site/config 取数据")
	}
	// D97：背景图直接作为 <img src>，不做任何判断
	if !strings.Contains(html, "BackgroundURL") {
		t.Error("内嵌首页应使用 BackgroundURL")
	}
	// 不应引入外部资源（无 assets/ 也要能渲染）
	for _, bad := range []string{"<link rel=\"stylesheet\"", "cdn.", "googleapis"} {
		if strings.Contains(html, bad) {
			t.Errorf("内嵌首页不应依赖外部资源：%q", bad)
		}
	}
}

// TestEmbeddedThemeIsSelfContained 断言内嵌主题不依赖 assets/（删掉也能渲染）。
func TestEmbeddedThemeIsSelfContained(t *testing.T) {
	env := newTestEnv(t)
	env.seedDefault()

	// 删掉 assets 目录（模拟 seed 不完整 / 用户误删）
	if err := os.RemoveAll(filepath.Join(env.ThemesDir, embeddedDefaultID, AssetsDir)); err != nil {
		t.Fatal(err)
	}

	// 主题仍应合法（校验只要求 manifest + index.html）
	if _, err := env.Svc.Store().Load(embeddedDefaultID); err != nil {
		t.Errorf("缺 assets 目录不应让主题不合法: %v", err)
	}
	// 首页仍可渲染
	if _, err := env.Svc.CurrentIndex(); err != nil {
		t.Errorf("首页应仍可渲染: %v", err)
	}
}

// TestEmbeddedThemeConfigSchema 断言内嵌默认主题声明的配置项与文档一致。
func TestEmbeddedThemeConfigSchema(t *testing.T) {
	env := newTestEnv(t)
	th, err := env.Svc.Store().Embedded()
	if err != nil {
		t.Fatal(err)
	}

	byKey := map[string]ConfigItem{}
	for _, it := range th.ConfigItems() {
		byKey[it.Key] = it
	}

	// DATA-MODEL.md §7.4 里登记的默认主题配置项
	for _, key := range []string{"BackgroundURL", "ShowHomeFeatures", "HomepageFeatures", "HomepageScenarios", "HomepageFaq"} {
		if _, ok := byKey[key]; !ok {
			t.Errorf("内嵌默认主题应声明配置项 %s", key)
		}
	}

	// D97：BackgroundURL 的默认值必须是上游自适应端点
	bg := byKey["BackgroundURL"]
	if got := defaultOf(bg); got != "https://api.yppp.net/api.php" {
		t.Errorf("BackgroundURL 默认值 = %v，期望 https://api.yppp.net/api.php", got)
	}
	if normalizeConfigType(bg.Type) != TypeString {
		t.Errorf("BackgroundURL 类型 = %q，期望 %q", bg.Type, TypeString)
	}

	// ShowHomeFeatures 是 switch，默认为真
	show := byKey["ShowHomeFeatures"]
	if normalizeConfigType(show.Type) != TypeSwitch {
		t.Errorf("ShowHomeFeatures 类型 = %q", show.Type)
	}
	if defaultOf(show) != true {
		t.Errorf("ShowHomeFeatures 默认值 = %v，期望 true", defaultOf(show))
	}

	// 三个 json 类型带 ItemSchema（供前端渲染 repeater）
	for _, key := range []string{"HomepageFeatures", "HomepageScenarios", "HomepageFaq"} {
		item := byKey[key]
		if normalizeConfigType(item.Type) != TypeJSON {
			t.Errorf("%s 类型 = %q，期望 json", key, item.Type)
		}
		if len(item.ItemSchema) == 0 {
			t.Errorf("%s 应声明 ItemSchema（repeater 子字段）", key)
		}
	}
}

// TestEmbeddedThemeEnterpriseGatelessConfig 断言内嵌主题能被后台读写配置（打通链路）。
func TestEmbeddedThemeConfigRoundTrip(t *testing.T) {
	env := newTestEnv(t)
	env.seedDefault()

	// 默认值来自 manifest
	view, err := env.Svc.SettingsView(embeddedDefaultID, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if v := view.Values["BackgroundURL"]; v.Source != SettingsSourceDefault {
		t.Errorf("初始 Source 应为 default，实际 %q", v.Source)
	}

	// 写一个自定义背景图
	if _, err := env.Svc.UpdateSettings(t.Context(), embeddedDefaultID, map[string]any{
		"BackgroundURL": "https://mine/bg.png",
		"HomepageFaq":   []any{map[string]any{"Question": "Q", "Answer": "A"}},
	}, "admin"); err != nil {
		t.Fatalf("写入默认主题配置失败: %v", err)
	}

	// /site/config 应反映出新值
	info, err := env.Svc.PublicTheme("zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if info.Settings["BackgroundURL"] != "https://mine/bg.png" {
		t.Errorf("公开信息未反映新值: %#v", info.Settings["BackgroundURL"])
	}
	faq, ok := info.Settings["HomepageFaq"].([]any)
	if !ok || len(faq) != 1 {
		t.Errorf("HomepageFaq 应为 1 项，实际 %#v", info.Settings["HomepageFaq"])
	}

	// 审计类型正确
	if got := env.Auditor.count(model.LogTypeThemeSettingsUpdate); got != 1 {
		t.Errorf("应写一条 theme.settings.update，实际 %d", got)
	}
}
