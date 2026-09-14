package theme

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// ---- 主题配置：三级兜底（D95）----

// themeWithConfig 是一份声明了多种类型配置项的主题，用于测三级兜底与类型校验。
const themeWithConfigExtraJSON = `"Pages":["/"],
"Configuration":{"Type":"managed","Items":[
  {"Key":"BackgroundURL","Name":"背景图","Type":"string","Default":"https://default/bg.png"},
  {"Key":"Count","Name":"数量","Type":"number","Default":3},
  {"Key":"Enabled","Name":"开关","Type":"switch","Default":true},
  {"Key":"Mode","Name":"模式","Type":"select","Options":"a,b,c","Default":"b"},
  {"Key":"Items","Name":"列表","Type":"json","Default":[{"k":"v"}]},
  {"Key":"Note","Name":"说明","Type":"text","Default":"hello"}
]}`

func TestSettingsThreeLevelFallback(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", themeWithConfigExtraJSON, themeIndexMarker)

	// ---- 第 2 层：manifest 的 Default ----
	view, err := env.Svc.SettingsView("demo", "zh-CN")
	if err != nil {
		t.Fatalf("取设置失败: %v", err)
	}
	if len(view.Schema) != 6 {
		t.Fatalf("schema 应有 6 项，实际 %d", len(view.Schema))
	}

	wantDefaults := map[string]any{
		"BackgroundURL": "https://default/bg.png",
		"Count":         float64(3),
		"Enabled":       true,
		"Mode":          "b",
		"Note":          "hello",
	}
	for key, want := range wantDefaults {
		got, ok := view.Values[key]
		if !ok {
			t.Fatalf("缺少配置项 %s", key)
		}
		if got.Value != want {
			t.Errorf("%s 的值应为 %v，实际 %v", key, want, got.Value)
		}
		if got.Source != SettingsSourceDefault {
			t.Errorf("%s 的来源应为 default，实际 %q", key, got.Source)
		}
		if got.HasValue {
			t.Errorf("%s 未落库时 HasValue 应为 false", key)
		}
	}

	// json 类型的默认值应是数组
	items := view.Values["Items"]
	arr, ok := items.Value.([]any)
	if !ok || len(arr) != 1 {
		t.Errorf("Items 默认值应为长度 1 的数组，实际 %#v", items.Value)
	}

	// ---- 第 1 层：DB 值覆盖 ----
	if _, err := env.Svc.UpdateSettings(t.Context(), "demo", map[string]any{
		"BackgroundURL": "https://db/bg.png",
		"Enabled":       false,
		"Count":         float64(9),
	}, "usr_admin"); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}

	view2, err := env.Svc.SettingsView("demo", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if v := view2.Values["BackgroundURL"]; v.Value != "https://db/bg.png" || v.Source != SettingsSourceDB || !v.HasValue {
		t.Errorf("BackgroundURL 应来自 DB，实际 %#v", v)
	}
	if v := view2.Values["Enabled"]; v.Value != false || v.Source != SettingsSourceDB {
		t.Errorf("Enabled 应来自 DB 且为 false，实际 %#v", v)
	}
	if v := view2.Values["Count"]; v.Value != float64(9) {
		t.Errorf("Count 应为 9，实际 %#v", v.Value)
	}
	// 未改的键仍来自默认值
	if v := view2.Values["Mode"]; v.Source != SettingsSourceDefault {
		t.Errorf("Mode 应仍来自默认值，实际 %q", v.Source)
	}
	// Default 字段始终回显 manifest 的默认值（供前端"恢复默认"用）
	if v := view2.Values["BackgroundURL"]; v.Default != "https://default/bg.png" {
		t.Errorf("Default 应回显 manifest 默认值，实际 %#v", v.Default)
	}
}

func TestUpdateSettingsRejectsUnknownKey(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", themeWithConfigExtraJSON, themeIndexMarker)

	// 未声明的键 → ErrSettingUnknown（防脏写）
	_, err := env.Svc.UpdateSettings(t.Context(), "demo", map[string]any{
		"NotDeclared": "x",
	}, "usr_admin")
	if !errors.Is(err, ErrSettingUnknown) {
		t.Fatalf("未声明的键应被拒绝，实际 %v", err)
	}

	// 而且不能「写一半」：已声明的键也不应落库
	view, err := env.Svc.SettingsView("demo", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if view.Values["BackgroundURL"].Source != SettingsSourceDefault {
		t.Error("校验失败时不应写入任何键（避免半写状态）")
	}
}

func TestUpdateSettingsRejectsTypeMismatch(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", themeWithConfigExtraJSON, themeIndexMarker)

	cases := []struct {
		name   string
		values map[string]any
	}{
		{"string 收到数字", map[string]any{"BackgroundURL": 123}},
		{"number 收到字符串", map[string]any{"Count": "abc"}},
		{"switch 收到字符串", map[string]any{"Enabled": "true"}},
		{"select 收到非法选项", map[string]any{"Mode": "z"}},
		{"text 收到布尔", map[string]any{"Note": true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.Svc.UpdateSettings(t.Context(), "demo", tc.values, "usr_admin")
			if !errors.Is(err, ErrSettingType) {
				t.Errorf("应当类型校验失败，实际 %v", err)
			}
		})
	}
}

func TestUpdateSettingsAcceptsValidTypes(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", themeWithConfigExtraJSON, themeIndexMarker)

	n, err := env.Svc.UpdateSettings(t.Context(), "demo", map[string]any{
		"BackgroundURL": "https://x/y.png",
		"Count":         7, // int 也应被接受并归一为 float64
		"Enabled":       true,
		"Mode":          "c",
		"Items":         []any{map[string]any{"a": 1}},
		"Note":          "多行\n文本",
	}, "usr_admin")
	if err != nil {
		t.Fatalf("合法类型应当写入成功: %v", err)
	}
	if n != 6 {
		t.Errorf("应更新 6 项，实际 %d", n)
	}

	view, err := env.Svc.SettingsView("demo", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if v := view.Values["Count"]; v.Value != float64(7) {
		t.Errorf("int 应被归一为 float64(7)，实际 %#v", v.Value)
	}
	if v := view.Values["Note"]; v.Value != "多行\n文本" {
		t.Errorf("多行文本应原样保存，实际 %#v", v.Value)
	}
}

func TestUpdateSettingsAuditDoesNotRecordValues(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", themeWithConfigExtraJSON, themeIndexMarker)
	env.Auditor.reset()

	secretish := "super-secret-token-value"
	if _, err := env.Svc.UpdateSettings(t.Context(), "demo", map[string]any{
		"BackgroundURL": secretish,
	}, "usr_admin"); err != nil {
		t.Fatal(err)
	}

	entries := env.Auditor.byType(model.LogTypeThemeSettingsUpdate)
	if len(entries) != 1 {
		t.Fatalf("应写一条 theme.settings.update，实际 %d", len(entries))
	}
	detail := entries[0].Detail
	// Detail 只记 keys/count，不记值内容（可能含敏感串）
	if s, ok := detail.(map[string]any); ok {
		if _, has := s["Values"]; has {
			t.Error("审计不应记录值内容")
		}
		for _, v := range s {
			if str, ok := v.(string); ok && str == secretish {
				t.Error("审计泄露了配置值")
			}
		}
	}
}

func TestSettingsViewShape(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", themeWithConfigExtraJSON, themeIndexMarker)

	view, err := env.Svc.SettingsView("demo", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if view.ThemeID != "demo" {
		t.Errorf("ThemeID = %q", view.ThemeID)
	}
	if view.Name == "" {
		t.Error("Name 不应为空")
	}

	byKey := map[string]SettingSchemaItem{}
	for _, it := range view.Schema {
		byKey[it.Key] = it
	}

	// 身份用 Key；Name/Alias 都是展示名（让前端可复用插件表单渲染器）
	bg := byKey["BackgroundURL"]
	if bg.Key != "BackgroundURL" || bg.Name == "" || bg.Alias != bg.Name {
		t.Errorf("schema 项结构不符: %#v", bg)
	}
	if bg.Type != TypeString {
		t.Errorf("Type = %q", bg.Type)
	}

	// select 带 Options
	if byKey["Mode"].Options != "a,b,c" {
		t.Errorf("Options = %q", byKey["Mode"].Options)
	}
}

func TestSettingsViewMissingTheme(t *testing.T) {
	env := newTestEnv(t)
	if _, err := env.Svc.SettingsView("ghost", "zh-CN"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的主题应返回 ErrNotFound，实际 %v", err)
	}
}

func TestClearSettings(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", themeWithConfigExtraJSON, themeIndexMarker)

	if _, err := env.Svc.UpdateSettings(t.Context(), "demo", map[string]any{
		"BackgroundURL": "https://db/bg.png",
		"Note":          "n",
	}, "usr_admin"); err != nil {
		t.Fatal(err)
	}

	// 把 demo 设为当前主题 → 不允许清理它的配置
	if err := env.Svc.SetActive(t.Context(), "demo", "usr_admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Svc.ClearSettings(t.Context(), "demo", "usr_admin"); !errors.Is(err, ErrSettingsInUse) {
		t.Errorf("当前启用主题的配置不应可清理，实际 %v", err)
	}

	// 切走后可以清理
	env.writeTheme("other", `"Pages":["/"]`, themeIndexMarker)
	if err := env.Svc.SetActive(t.Context(), "other", "usr_admin"); err != nil {
		t.Fatal(err)
	}
	env.Auditor.reset()
	deleted, err := env.Svc.ClearSettings(t.Context(), "demo", "usr_admin")
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if deleted != 2 {
		t.Errorf("应删除 2 行，实际 %d", deleted)
	}
	if got := env.Auditor.count(model.LogTypeThemeSettingsClear); got != 1 {
		t.Errorf("应写一条 theme.settings.clear，实际 %d", got)
	}

	// 清理后回到 manifest 默认值
	view, err := env.Svc.SettingsView("demo", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if v := view.Values["BackgroundURL"]; v.Source != SettingsSourceDefault || v.Value != "https://default/bg.png" {
		t.Errorf("清理后应回到默认值，实际 %#v", v)
	}
}

func TestSwitchThemeKeepsOldValues(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("a", themeWithConfigExtraJSON, themeIndexMarker)
	env.writeTheme("b", themeWithConfigExtraJSON, themeIndexMarker)

	if _, err := env.Svc.UpdateSettings(t.Context(), "a", map[string]any{"Note": "A值"}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Svc.UpdateSettings(t.Context(), "b", map[string]any{"Note": "B值"}, "admin"); err != nil {
		t.Fatal(err)
	}

	if err := env.Svc.SetActive(t.Context(), "a", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := env.Svc.SetActive(t.Context(), "b", "admin"); err != nil {
		t.Fatal(err)
	}
	// 切回 a：旧值必须还在（D95「换主题不丢值」）
	if err := env.Svc.SetActive(t.Context(), "a", "admin"); err != nil {
		t.Fatal(err)
	}

	viewA, _ := env.Svc.SettingsView("a", "zh-CN")
	viewB, _ := env.Svc.SettingsView("b", "zh-CN")
	if viewA.Values["Note"].Value != "A值" {
		t.Errorf("切回后 a 的值应保留，实际 %#v", viewA.Values["Note"].Value)
	}
	if viewB.Values["Note"].Value != "B值" {
		t.Errorf("b 的值应保留，实际 %#v", viewB.Values["Note"].Value)
	}
}

// ---- 切换主题 ----

func TestSetActive(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", `"Pages":["/"]`, themeIndexMarker)
	env.Auditor.reset()

	if err := env.Svc.SetActive(t.Context(), "demo", "usr_admin"); err != nil {
		t.Fatalf("切换失败: %v", err)
	}
	if got := env.Svc.Active(); got != "demo" {
		t.Errorf("Active() = %q", got)
	}

	entries := env.Auditor.byType(model.LogTypeThemeActivate)
	if len(entries) != 1 {
		t.Fatalf("应写一条 theme.activate，实际 %d", len(entries))
	}
	detail, _ := entries[0].Detail.(map[string]any)
	if detail["Active"] != "demo" || detail["Previous"] != "default" {
		t.Errorf("审计 Detail 不符: %#v", detail)
	}
}

func TestSetActiveRejectsUnknownAndInvalid(t *testing.T) {
	env := newTestEnv(t)

	// 不存在的主题
	if err := env.Svc.SetActive(t.Context(), "ghost", "admin"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的主题应返回 ErrNotFound，实际 %v", err)
	}

	// 空 ID
	if err := env.Svc.SetActive(t.Context(), "  ", "admin"); err == nil {
		t.Error("空 ThemeID 应被拒绝")
	}

	// 非法 ID 形态
	if err := env.Svc.SetActive(t.Context(), "../etc", "admin"); err == nil {
		t.Error("非法 ID 应被拒绝")
	}

	// 主题存在但不合法（Pages 命中认证页）→ 不允许启用
	env.writeTheme("hijack", `"Pages":["/login"]`, "<html>evil</html>")
	if err := env.Svc.SetActive(t.Context(), "hijack", "admin"); !errors.Is(err, ErrThemeInvalid) {
		t.Errorf("不合法主题不应可启用，实际 %v", err)
	}
	// 且不应该真的改掉 theme.active
	if got := env.Svc.Active(); got == "hijack" {
		t.Error("失败时不应改动 theme.active")
	}
}

func TestActiveDefaultIsFallbackName(t *testing.T) {
	env := newTestEnv(t)
	// 未设置 theme.active 时应回落为 default
	if got := env.Svc.Active(); got != embeddedDefaultID {
		t.Errorf("缺省应为 default，实际 %q", got)
	}
	if err := env.Settings.Set(KeyActiveTheme, "   ", "t"); err != nil {
		t.Fatal(err)
	}
	if got := env.Svc.Active(); got != embeddedDefaultID {
		t.Errorf("空白值应回落为 default，实际 %q", got)
	}
}

// ---- 卸载 ----

func TestUninstall(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", `"Pages":["/"]`, themeIndexMarker)
	dir := filepath.Join(env.ThemesDir, "demo")
	env.Auditor.reset()

	if err := env.Svc.Uninstall(t.Context(), "demo", "usr_admin"); err != nil {
		t.Fatalf("卸载失败: %v", err)
	}
	if dirExists(dir) {
		t.Error("目录应被删除")
	}
	if got := env.Auditor.count(model.LogTypeThemeUninstall); got != 1 {
		t.Errorf("应写一条 theme.uninstall，实际 %d", got)
	}
}

func TestUninstallRefusesActiveAndBuiltin(t *testing.T) {
	env := newTestEnv(t)
	env.seedDefault()
	env.writeTheme("demo", `"Pages":["/"]`, themeIndexMarker)

	// 当前启用中 → 40901 语义（ErrThemeActive）
	if err := env.Svc.SetActive(t.Context(), "demo", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := env.Svc.Uninstall(t.Context(), "demo", "admin"); !errors.Is(err, ErrThemeActive) {
		t.Errorf("启用中的主题不应可卸载，实际 %v", err)
	}
	if !dirExists(filepath.Join(env.ThemesDir, "demo")) {
		t.Error("失败时不应删除目录")
	}

	// default 是兜底锚点 → 永远不可卸载
	if err := env.Svc.Uninstall(t.Context(), embeddedDefaultID, "admin"); !errors.Is(err, ErrBuiltinTheme) {
		t.Errorf("default 不应可卸载，实际 %v", err)
	}
	if !dirExists(filepath.Join(env.ThemesDir, embeddedDefaultID)) {
		t.Error("default 目录不应被删除")
	}
}

func TestUninstallMissingAndTraversal(t *testing.T) {
	env := newTestEnv(t)
	if err := os.MkdirAll(env.ThemesDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := env.Svc.Uninstall(t.Context(), "ghost", "admin"); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的主题应返回 ErrNotFound，实际 %v", err)
	}

	// 目录穿越必须被拒（且不能删掉主题目录之外的东西）
	outside := filepath.Join(env.DataDir, "keepme.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"..", "../..", "../keepme.txt", "a/../../x"} {
		if err := env.Svc.Uninstall(t.Context(), bad, "admin"); err == nil {
			t.Errorf("路径 %q 应被拒绝", bad)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Error("主题目录外的文件被删除了！")
	}
}

func TestUninstallInvalidThemeDirIsAllowed(t *testing.T) {
	env := newTestEnv(t)
	// 一个不合法的主题目录（缺 manifest）——管理员最需要能删掉它
	dir := filepath.Join(env.ThemesDir, "broken")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := env.Svc.Uninstall(t.Context(), "broken", "admin"); err != nil {
		t.Fatalf("不合法主题目录也应可删除（修复手段）: %v", err)
	}
	if dirExists(dir) {
		t.Error("目录应被删除")
	}
}

// ---- 重新扫描 ----

func TestRescanWritesAudit(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("ok", `"Pages":["/"]`, themeIndexMarker)
	env.writeTheme("bad", `"Pages":["/admin"]`, themeIndexMarker)
	env.Auditor.reset()

	res := env.Svc.Rescan(t.Context(), "zh-CN", "admin")
	if res == nil || len(res.Items) < 2 {
		t.Fatalf("扫描结果异常: %#v", res)
	}

	entries := env.Auditor.byType(model.LogTypeThemeRescan)
	if len(entries) != 1 {
		t.Fatalf("应写一条 theme.rescan，实际 %d", len(entries))
	}
	detail, _ := entries[0].Detail.(map[string]any)
	if detail["Valid"] == nil || detail["Invalid"] == nil {
		t.Errorf("审计 Detail 应含 Valid/Invalid，实际 %#v", detail)
	}
}

// ---- 公开信息（供 /site/config）----

func TestPublicTheme(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", themeWithConfigExtraJSON, themeIndexMarker)
	if err := env.Svc.SetActive(t.Context(), "demo", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Svc.UpdateSettings(t.Context(), "demo", map[string]any{
		"BackgroundURL": "https://cdn/bg.png",
	}, "admin"); err != nil {
		t.Fatal(err)
	}

	info, err := env.Svc.PublicTheme("zh-CN")
	if err != nil {
		t.Fatalf("取公开信息失败: %v", err)
	}
	if info.ID != "demo" || info.AssetBase != AssetBasePath {
		t.Errorf("公开信息不符: %#v", info)
	}
	if len(info.Pages) != 1 || info.Pages[0] != "/" {
		t.Errorf("Pages = %v", info.Pages)
	}
	// Settings 是「三级兜底后的最终值」
	if info.Settings["BackgroundURL"] != "https://cdn/bg.png" {
		t.Errorf("BackgroundURL 应为 DB 值，实际 %#v", info.Settings["BackgroundURL"])
	}
	if info.Settings["Enabled"] != true {
		t.Errorf("Enabled 应为默认值 true，实际 %#v", info.Settings["Enabled"])
	}
}

func TestPublicThemeWhenFallback(t *testing.T) {
	env := newTestEnv(t)
	if err := env.Settings.Set(KeyActiveTheme, "ghost", "t"); err != nil {
		t.Fatal(err)
	}

	// 即使配置的主题不存在，也要能拿到可用的公开信息（永不白屏）
	info, err := env.Svc.PublicTheme("zh-CN")
	if err != nil {
		t.Fatalf("兜底时也应返回公开信息: %v", err)
	}
	if info.ID != embeddedDefaultID {
		t.Errorf("应回退到内嵌默认主题，实际 %q", info.ID)
	}
	if info.Settings["BackgroundURL"] == nil {
		t.Error("内嵌默认主题应带 BackgroundURL 默认值")
	}
}

// ---- 阈值键（D96 / config/defaults.go 已登记）----

func TestLimitsFromSettings(t *testing.T) {
	env := newTestEnv(t)

	// 默认值来自代码默认值（config.Keys 已登记这些键）
	l := readLimits(env.Settings)
	if l.MaxPackageBytes != defaultMaxPackageBytes {
		t.Errorf("maxPackageBytes 默认应为 %d，实际 %d", defaultMaxPackageBytes, l.MaxPackageBytes)
	}
	if l.MaxManifestBytes != defaultMaxManifestBytes {
		t.Errorf("maxManifestBytes 默认应为 %d，实际 %d", defaultMaxManifestBytes, l.MaxManifestBytes)
	}

	// 后台改小后立即生效
	if err := env.Settings.Set(KeyMaxPackageBytes, int64(1234), "admin"); err != nil {
		t.Fatalf("设置阈值失败（键应在 config.Keys 中登记）: %v", err)
	}
	if got := readLimits(env.Settings).MaxPackageBytes; got != 1234 {
		t.Errorf("阈值应改为 1234，实际 %d", got)
	}

	// 被写成 0 / 负数时回落到默认值（避免「0 = 拒绝一切」）
	if err := env.Settings.Set(KeyMaxPackageBytes, int64(0), "admin"); err != nil {
		t.Fatal(err)
	}
	if got := readLimits(env.Settings).MaxPackageBytes; got != defaultMaxPackageBytes {
		t.Errorf("0 应回落默认值，实际 %d", got)
	}
}
