package theme

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/webfs"
)

// 响应特征串：用来判断一个 HTML 响应来自「主题」还是「内置 SPA」。
const (
	// embeddedThemeMarker 内嵌默认主题的 index.html 里的特征（见 embedded/index.html）。
	embeddedThemeMarker = `id="heroTitle"`
)

// spaMarker 内置 SPA 的 index.html 里的特征（见 web/dist/index.html）。
const spaMarker = `id="root"`

// placeholderMarker 是「前端尚未构建」提示页的特征。
//
// 测试必须在**两种状态下都通过**：
//   - 已构建前端（`make build-web` 之后）→ SPA 响应含 id="root"
//   - 未构建（全新 clone，dist 里只有 .gitkeep）→ ServeSPA 返回提示页
//
// 若只认 SPA 的特征串，CI 上先跑 `go test ./...` 而没构建前端时就会误报失败。
const placeholderMarker = "前端尚未构建"

// isSPAResponse 判断响应是否来自「内置 SPA 侧」（构建产物或未构建提示页）。
func isSPAResponse(body string) bool {
	return strings.Contains(body, spaMarker) || strings.Contains(body, placeholderMarker)
}

func init() { gin.SetMode(gin.TestMode) }

// newTestRouter 按 server.go 的接线方式装配一个测试路由器。
//
// 刻意复用**与生产完全相同**的 Register* 函数与 ServePage，
// 只把鉴权中间件换成可注入的假实现（真实的鉴权由 W3 的中间件单独测试）。
func newTestRouter(env *testEnv, middlewares ...gin.HandlerFunc) *gin.Engine {
	engine := gin.New()

	api := engine.Group("/api/web/v1")
	env.Svc.RegisterSiteRoutes(api, SiteRoutesOptions{Version: "0.1.0-test"})
	env.Svc.RegisterAdminRoutes(api, AdminRoutesOptions{Middlewares: middlewares})

	// 与 server.go 完全一致的静态资源接线
	engine.GET("/assets/*filepath", webfs.ServeAssets())
	env.Svc.RegisterAssetRoutes(engine)

	// /themes/** 一律 404（不暴露主题目录）
	engine.GET("/themes/*filepath", func(c *gin.Context) {
		response.Fail(c, response.CodeNotFound)
	})

	engine.NoRoute(ServePage(env.Svc))
	return engine
}

// do 发一个请求并返回响应。
func do(t *testing.T, engine *gin.Engine, method, target string, body []byte, contentType string) *httptest.ResponseRecorder {
	t.Helper()

	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, r)
	return w
}

// decodeEnvelope 解析统一信封。
func decodeEnvelope(t *testing.T, w *httptest.ResponseRecorder) (int, string, map[string]any) {
	t.Helper()

	var env struct {
		Code    int            `json:"Code"`
		Message string         `json:"Message"`
		Data    map[string]any `json:"Data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应不是合法 JSON: %v\n%s", err, w.Body.String())
	}
	return env.Code, env.Message, env.Data
}

// ---- 公开端点：GET /site/config ----

func TestRouteSiteConfig(t *testing.T) {
	env := newTestEnv(t)
	env.seedDefault()
	if err := env.Settings.Set("site.name", "我的图床", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := env.Settings.Set("site.icp", "京ICP备1号", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := env.Settings.Set("oauth.github.enabled", true, "admin"); err != nil {
		t.Fatal(err)
	}

	engine := newTestRouter(env)
	w := do(t, engine, http.MethodGet, "/api/web/v1/site/config", nil, "")

	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	code, _, data := decodeEnvelope(t, w)
	if code != 0 {
		t.Fatalf("Code = %d", code)
	}

	site, _ := data["Site"].(map[string]any)
	if site["Name"] != "我的图床" {
		t.Errorf("Site.Name = %v", site["Name"])
	}
	if site["Icp"] != "京ICP备1号" {
		t.Errorf("Site.Icp = %v", site["Icp"])
	}
	if site["Version"] != "0.1.0-test" {
		t.Errorf("Site.Version = %v", site["Version"])
	}

	feat, _ := data["Features"].(map[string]any)
	if feat["GitHubOAuthEnabled"] != true {
		t.Errorf("GitHubOAuthEnabled = %v", feat["GitHubOAuthEnabled"])
	}
	// D25：不做自助注册，恒为 false
	if feat["AllowSelfRegistration"] != false {
		t.Errorf("AllowSelfRegistration 必须为 false（D25），实际 %v", feat["AllowSelfRegistration"])
	}

	themeInfo, ok := data["Theme"].(map[string]any)
	if !ok {
		t.Fatalf("Theme 段缺失: %#v", data["Theme"])
	}
	if themeInfo["ID"] != embeddedDefaultID {
		t.Errorf("Theme.ID = %v", themeInfo["ID"])
	}
	if themeInfo["AssetBase"] != AssetBasePath {
		t.Errorf("Theme.AssetBase = %v，期望 %v", themeInfo["AssetBase"], AssetBasePath)
	}
	pages, _ := themeInfo["Pages"].([]any)
	if len(pages) != 1 || pages[0] != "/" {
		t.Errorf("Theme.Pages = %v", themeInfo["Pages"])
	}

	settingsMap, _ := themeInfo["Settings"].(map[string]any)
	if settingsMap["BackgroundURL"] == nil {
		t.Errorf("Theme.Settings.BackgroundURL 缺失: %#v", settingsMap)
	}
	if settingsMap["ShowHomeFeatures"] != true {
		t.Errorf("ShowHomeFeatures 默认应为 true，实际 %v", settingsMap["ShowHomeFeatures"])
	}

	// 不得泄露敏感信息
	body := w.Body.String()
	for _, leak := range []string{"clientSecret", "password", "smtp"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(leak)) {
			t.Errorf("响应疑似泄露敏感字段 %q", leak)
		}
	}
}

func TestRouteSiteConfigWhenThemeFallback(t *testing.T) {
	env := newTestEnv(t)
	if err := env.Settings.Set(KeyActiveTheme, "ghost", "t"); err != nil {
		t.Fatal(err)
	}

	engine := newTestRouter(env)
	w := do(t, engine, http.MethodGet, "/api/web/v1/site/config", nil, "")

	code, _, data := decodeEnvelope(t, w)
	if code != 0 {
		t.Fatalf("Code = %d", code)
	}
	// 兜底时仍返回**内嵌主题**的信息（让首页能渲染），并用 ThemeError 说明
	if data["Theme"] == nil {
		t.Fatal("兜底时也应返回内嵌主题信息（永不白屏）")
	}
	if data["ThemeError"] == "" {
		t.Error("兜底时应给出 ThemeError 说明原因")
	}
}

// ---- 后台端点 ----

func TestRouteThemesList(t *testing.T) {
	env := newTestEnv(t)
	env.seedDefault()
	env.writeTheme("demo", `"Pages":["/"]`, themeIndexMarker)

	engine := newTestRouter(env)
	w := do(t, engine, http.MethodGet, "/api/web/v1/themes", nil, "")

	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	code, _, data := decodeEnvelope(t, w)
	if code != 0 {
		t.Fatalf("Code = %d", code)
	}
	if data["Active"] != embeddedDefaultID {
		t.Errorf("Active = %v", data["Active"])
	}

	items, _ := data["Items"].([]any)
	if len(items) < 2 {
		t.Fatalf("应至少列出 default 与 demo，实际 %d 项", len(items))
	}

	found := map[string]map[string]any{}
	for _, it := range items {
		m, _ := it.(map[string]any)
		found[m["ID"].(string)] = m
	}

	def := found[embeddedDefaultID]
	if def == nil {
		t.Fatal("列表里应有 default")
	}
	if def["IsActive"] != true {
		t.Error("default 应为当前启用")
	}
	if def["IsBuiltin"] != true {
		t.Error("default 应标记为内置")
	}
	if def["CanUninstall"] != false {
		t.Error("内置主题不可卸载")
	}
	if def["Valid"] != true {
		t.Errorf("default 应合法: %v", def["Error"])
	}
	if def["ScreenshotURL"] == "" {
		t.Error("应给出 ScreenshotURL")
	}

	if found["demo"]["IsActive"] != false {
		t.Error("demo 不是当前启用")
	}
	if found["demo"]["CanUninstall"] != true {
		t.Error("demo 应可卸载")
	}
}

func TestRouteThemeActiveSwitchTakesEffectImmediately(t *testing.T) {
	env := newTestEnv(t)
	env.seedDefault()
	env.writeTheme("demo", `"Pages":["/"]`, themeIndexMarker)
	engine := newTestRouter(env)

	// 初始：/ 由内嵌默认主题渲染
	before := do(t, engine, http.MethodGet, "/", nil, "")
	if !strings.Contains(before.Body.String(), embeddedThemeMarker) {
		t.Fatalf("初始首页应来自内嵌默认主题:\n%s", truncate(before.Body.String()))
	}

	// 切换（立即生效，无需重启）
	body, _ := json.Marshal(map[string]string{"ThemeID": "demo"})
	w := do(t, engine, http.MethodPut, "/api/web/v1/themes/active", body, "application/json")
	if w.Code != http.StatusOK {
		t.Fatalf("切换失败 HTTP %d: %s", w.Code, w.Body.String())
	}
	_, _, data := decodeEnvelope(t, w)
	if data["Active"] != "demo" || data["Previous"] != embeddedDefaultID {
		t.Errorf("切换响应不符: %#v", data)
	}

	// 立即生效
	after := do(t, engine, http.MethodGet, "/", nil, "")
	if !strings.Contains(after.Body.String(), themeIndexMarker) {
		t.Fatalf("切换后首页应立刻来自 demo 主题:\n%s", truncate(after.Body.String()))
	}
}

func TestRouteThemeSettings(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", themeWithConfigExtraJSON, themeIndexMarker)
	engine := newTestRouter(env)

	// GET
	w := do(t, engine, http.MethodGet, "/api/web/v1/themes/demo/settings", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	_, _, data := decodeEnvelope(t, w)
	if data["ThemeID"] != "demo" {
		t.Errorf("ThemeID = %v", data["ThemeID"])
	}
	schema, _ := data["Schema"].([]any)
	if len(schema) != 6 {
		t.Fatalf("Schema 应有 6 项，实际 %d", len(schema))
	}
	values, _ := data["Values"].(map[string]any)
	bg, _ := values["BackgroundURL"].(map[string]any)
	if bg["Source"] != SettingsSourceDefault {
		t.Errorf("初始 Source 应为 default，实际 %v", bg["Source"])
	}

	// PUT
	putBody, _ := json.Marshal(map[string]any{
		"Values": map[string]any{"BackgroundURL": "https://cdn/new.png"},
	})
	w2 := do(t, engine, http.MethodPut, "/api/web/v1/themes/demo/settings", putBody, "application/json")
	if w2.Code != http.StatusOK {
		t.Fatalf("写入失败 HTTP %d: %s", w2.Code, w2.Body.String())
	}
	_, _, data2 := decodeEnvelope(t, w2)
	if data2["Updated"] != float64(1) {
		t.Errorf("Updated = %v", data2["Updated"])
	}

	// 落库生效
	w3 := do(t, engine, http.MethodGet, "/api/web/v1/themes/demo/settings", nil, "")
	_, _, data3 := decodeEnvelope(t, w3)
	values3, _ := data3["Values"].(map[string]any)
	bg3, _ := values3["BackgroundURL"].(map[string]any)
	if bg3["Value"] != "https://cdn/new.png" || bg3["Source"] != SettingsSourceDB {
		t.Errorf("写入未生效: %#v", bg3)
	}

	// 未声明的键 → 40001
	badBody, _ := json.Marshal(map[string]any{"Values": map[string]any{"Nope": 1}})
	w4 := do(t, engine, http.MethodPut, "/api/web/v1/themes/demo/settings", badBody, "application/json")
	if w4.Code != http.StatusBadRequest {
		t.Errorf("未声明键应返回 400，实际 %d: %s", w4.Code, w4.Body.String())
	}
	if code, _, _ := decodeEnvelope(t, w4); code != 40001 {
		t.Errorf("错误码应为 40001，实际 %d", code)
	}

	// 类型不符 → 40001
	badType, _ := json.Marshal(map[string]any{"Values": map[string]any{"Count": "not-a-number"}})
	w5 := do(t, engine, http.MethodPut, "/api/web/v1/themes/demo/settings", badType, "application/json")
	if code, _, _ := decodeEnvelope(t, w5); code != 40001 {
		t.Errorf("类型不符应为 40001，实际 %d", code)
	}
}

func TestRouteThemeSettingsClear(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", themeWithConfigExtraJSON, themeIndexMarker)
	engine := newTestRouter(env)

	if _, err := env.Svc.UpdateSettings(t.Context(), "demo", map[string]any{"Note": "x"}, "t"); err != nil {
		t.Fatal(err)
	}

	// demo 不是当前启用主题 → 可以清理
	w := do(t, engine, http.MethodDelete, "/api/web/v1/themes/demo/settings", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("清理失败 HTTP %d: %s", w.Code, w.Body.String())
	}

	// 切到 demo 后再清理 → 40901
	body, _ := json.Marshal(map[string]string{"ThemeID": "demo"})
	if w := do(t, engine, http.MethodPut, "/api/web/v1/themes/active", body, "application/json"); w.Code != http.StatusOK {
		t.Fatal("切换失败")
	}
	w2 := do(t, engine, http.MethodDelete, "/api/web/v1/themes/demo/settings", nil, "")
	if w2.Code != http.StatusConflict {
		t.Errorf("当前启用主题的配置不应可清理，实际 %d", w2.Code)
	}
	if code, _, _ := decodeEnvelope(t, w2); code != 40901 {
		t.Errorf("错误码应为 40901，实际 %d", code)
	}
}

func TestRouteThemeUninstall(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("victim", `"Pages":["/"]`, themeIndexMarker)
	env.writeTheme("keeper", `"Pages":["/"]`, themeIndexMarker)
	engine := newTestRouter(env)

	// 启用的主题不可卸载 → 40901
	body, _ := json.Marshal(map[string]string{"ThemeID": "victim"})
	if w := do(t, engine, http.MethodPut, "/api/web/v1/themes/active", body, "application/json"); w.Code != http.StatusOK {
		t.Fatal("切换失败")
	}
	w := do(t, engine, http.MethodDelete, "/api/web/v1/themes/victim", nil, "")
	if w.Code != http.StatusConflict {
		t.Errorf("启用中的主题不应可卸载，实际 %d", w.Code)
	}

	// default 不可卸载 → 40901
	w2 := do(t, engine, http.MethodDelete, "/api/web/v1/themes/default", nil, "")
	if w2.Code != http.StatusConflict {
		t.Errorf("default 不应可卸载，实际 %d", w2.Code)
	}

	// 不存在的主题 → 40401
	w3 := do(t, engine, http.MethodDelete, "/api/web/v1/themes/ghost", nil, "")
	if code, _, _ := decodeEnvelope(t, w3); code != 40401 {
		t.Errorf("不存在的主题应为 40401，实际 %d", code)
	}

	// 正常卸载
	w4 := do(t, engine, http.MethodDelete, "/api/web/v1/themes/keeper", nil, "")
	if w4.Code != http.StatusOK {
		t.Fatalf("卸载失败 HTTP %d: %s", w4.Code, w4.Body.String())
	}
	if dirExists(filepath.Join(env.ThemesDir, "keeper")) {
		t.Error("目录应被删除")
	}
}

func TestRouteThemeRescan(t *testing.T) {
	env := newTestEnv(t)
	env.seedDefault()
	engine := newTestRouter(env)

	// 手工放一个新主题，然后 rescan
	env.writeTheme("late", `"Pages":["/"]`, themeIndexMarker)

	w := do(t, engine, http.MethodPost, "/api/web/v1/themes/rescan", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("rescan 失败 HTTP %d: %s", w.Code, w.Body.String())
	}
	_, _, data := decodeEnvelope(t, w)
	items, _ := data["Items"].([]any)

	found := false
	for _, it := range items {
		if m, ok := it.(map[string]any); ok && m["ID"] == "late" {
			found = true
		}
	}
	if !found {
		t.Error("rescan 后应发现新放进去的主题")
	}
}

func TestRouteThemeScreenshot(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", `"Pages":["/"]`, themeIndexMarker)
	if err := os.WriteFile(filepath.Join(env.ThemesDir, "demo", ScreenshotFile), []byte("PNGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := newTestRouter(env)

	w := do(t, engine, http.MethodGet, "/api/web/v1/themes/demo/screenshot", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "PNGDATA" {
		t.Errorf("内容不符: %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q", ct)
	}

	// 没有预览图 → 40401
	w2 := do(t, engine, http.MethodGet, "/api/web/v1/themes/default/screenshot", nil, "")
	if code, _, _ := decodeEnvelope(t, w2); code != 40401 {
		t.Errorf("无预览图应为 40401，实际 %d", code)
	}
}

// ---- 安装（multipart）----

func TestRouteThemeInstallMultipart(t *testing.T) {
	env := newTestEnv(t)
	engine := newTestRouter(env)

	buildMultipart := func(entries []zipEntry, overwrite string) ([]byte, string) {
		ra, _ := buildZip(t, entries)
		// 把 zip 读成字节（测试数据很小）
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)

		fw, err := mw.CreateFormFile("File", "theme.zip")
		if err != nil {
			t.Fatal(err)
		}
		data := make([]byte, 0)
		// 通过 io.Copy 读全部内容
		tmp := make([]byte, 4096)
		for {
			n, err := ra.ReadAt(tmp, int64(len(data)))
			if n > 0 {
				data = append(data, tmp[:n]...)
			}
			if err != nil {
				break
			}
		}
		if _, err := fw.Write(data); err != nil {
			t.Fatal(err)
		}
		if overwrite != "" {
			_ = mw.WriteField("Overwrite", overwrite)
		}
		_ = mw.Close()
		return buf.Bytes(), mw.FormDataContentType()
	}

	body, ct := buildMultipart(validZipEntries("uploaded"), "")
	w := do(t, engine, http.MethodPost, "/api/web/v1/themes/install", body, ct)
	if w.Code != http.StatusOK {
		t.Fatalf("安装失败 HTTP %d: %s", w.Code, w.Body.String())
	}
	_, _, data := decodeEnvelope(t, w)
	if data["ID"] != "uploaded" || data["Installed"] != true {
		t.Errorf("安装响应不符: %#v", data)
	}
	if !dirExists(filepath.Join(env.ThemesDir, "uploaded")) {
		t.Error("主题目录未创建")
	}

	// 重复安装（未勾选覆盖）→ 40901
	body2, ct2 := buildMultipart(validZipEntries("uploaded"), "")
	w2 := do(t, engine, http.MethodPost, "/api/web/v1/themes/install", body2, ct2)
	if code, _, _ := decodeEnvelope(t, w2); code != 40901 {
		t.Errorf("重复安装应为 40901，实际 %d", code)
	}

	// 勾选覆盖 → 成功
	body3, ct3 := buildMultipart(validZipEntries("uploaded"), "true")
	w3 := do(t, engine, http.MethodPost, "/api/web/v1/themes/install", body3, ct3)
	if w3.Code != http.StatusOK {
		t.Errorf("覆盖安装应成功，实际 %d: %s", w3.Code, w3.Body.String())
	}

	// 缺 File 字段 → 40001
	w4 := do(t, engine, http.MethodPost, "/api/web/v1/themes/install", []byte("x"), "application/json")
	if code, _, _ := decodeEnvelope(t, w4); code != 40001 {
		t.Errorf("缺 File 应为 40001，实际 %d", code)
	}

	// 含 Zip Slip 的包 → 40001，且无残留
	slip := validZipEntries("slipper")
	slip = append(slip, zipEntry{name: "../escape.txt", body: "PWNED"})
	body5, ct5 := buildMultipart(slip, "")
	w5 := do(t, engine, http.MethodPost, "/api/web/v1/themes/install", body5, ct5)
	if code, _, _ := decodeEnvelope(t, w5); code != 40001 {
		t.Errorf("Zip Slip 应返回 40001，实际 %d: %s", code, w5.Body.String())
	}
	if _, err := os.Stat(filepath.Join(env.DataDir, "escape.txt")); err == nil {
		t.Error("Zip Slip 写出了主题目录之外的文件！")
	}
	assertNoResidue(t, env)
}

// ---- 中间件注入 ----

func TestRouteAdminMiddlewaresAreApplied(t *testing.T) {
	env := newTestEnv(t)
	env.seedDefault()

	invocations := 0
	blocker := func(c *gin.Context) {
		invocations++
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"Code": 40301, "Message": "权限不足"})
	}

	engine := newTestRouter(env, blocker)

	w := do(t, engine, http.MethodGet, "/api/web/v1/themes", nil, "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("注入的中间件应能拦截请求，实际 %d", w.Code)
	}
	if invocations != 1 {
		t.Errorf("中间件应被调用一次，实际 %d", invocations)
	}

	// 公开端点不受 admin 中间件影响
	w2 := do(t, engine, http.MethodGet, "/api/web/v1/site/config", nil, "")
	if w2.Code != http.StatusOK {
		t.Errorf("/site/config 不应被 admin 中间件拦下，实际 %d", w2.Code)
	}
}

// ---- 页面分发（D99.1）----

func TestRoutePageDispatch(t *testing.T) {
	env := newTestEnv(t)
	env.seedDefault()
	env.writeTheme("demo", `"Pages":["/","/gallery"]`, themeIndexMarker)
	if err := env.Svc.SetActive(t.Context(), "demo", "t"); err != nil {
		t.Fatal(err)
	}
	engine := newTestRouter(env)

	cases := []struct {
		path      string
		wantTheme bool
	}{
		{"/", true},             // 主题注册
		{"/gallery", true},      // 主题注册
		{"/gallery/up_1", true}, // 前缀匹配覆盖子路径
		{"/upload", false},      // 未注册 → 内置 SPA
		{"/albums", false},
		{"/jobs", false},
		{"/logs", false},
		{"/settings", false},
		{"/login", false}, // 认证页**永远**内置 SPA
		{"/first-login", false},
		{"/forgot-password", false},
		{"/reset-password", false},
		{"/admin", false}, // 后台**永远**内置 SPA
		{"/admin/themes", false},
		{"/admin/users", false},
		{"/galleryx", false}, // 边界：不被前缀误伤
	}

	for _, tc := range cases {
		w := do(t, engine, http.MethodGet, tc.path, nil, "")
		if w.Code != http.StatusOK {
			t.Errorf("%s: HTTP %d", tc.path, w.Code)
			continue
		}
		body := w.Body.String()
		fromTheme := strings.Contains(body, themeIndexMarker)
		fromSPA := isSPAResponse(body)

		if tc.wantTheme {
			if !fromTheme {
				t.Errorf("%s 应由主题渲染", tc.path)
			}
		} else {
			if fromTheme {
				t.Errorf("%s **不应**由主题渲染（安全/默认回退）", tc.path)
			}
			if !fromSPA {
				t.Errorf("%s 应由内置 SPA 渲染，实际响应既不含 SPA 标记也不含未构建提示:\n%s",
					tc.path, truncate(body))
			}
		}
	}
}

func TestRouteLoginStaysSPAWhenThemeTriesToHijack(t *testing.T) {
	env := newTestEnv(t)

	// 手工写一个「想接管 /login」的主题目录（绕过校验，模拟文件被直接编辑）
	dir := filepath.Join(env.ThemesDir, "hijack")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile),
		[]byte(`{"ID":"hijack","Name":"evil","Pages":["/login","/admin"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, IndexFile),
		[]byte("<html>EVIL-LOGIN-PAGE</html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 即便试图启用它也必须失败（清单不合法）
	if err := env.Svc.SetActive(t.Context(), "hijack", "t"); err == nil {
		t.Fatal("含认证页/后台的 Pages 的主题不应可启用")
	}

	engine := newTestRouter(env)
	for _, path := range []string{"/login", "/admin", "/admin/users"} {
		w := do(t, engine, http.MethodGet, path, nil, "")
		body := w.Body.String()
		if strings.Contains(body, "EVIL-LOGIN-PAGE") {
			t.Errorf("%s 被恶意主题接管了！", path)
		}
		if !isSPAResponse(body) {
			t.Errorf("%s 应由内置 SPA 渲染", path)
		}
	}
}

func TestRouteAPINotFoundIsJSON(t *testing.T) {
	env := newTestEnv(t)
	engine := newTestRouter(env)

	w := do(t, engine, http.MethodGet, "/api/web/v1/definitely-not-a-route", nil, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("HTTP %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("/api/** 未匹配必须返回 JSON，实际 Content-Type=%q", ct)
	}
	if code, _, _ := decodeEnvelope(t, w); code != 40401 {
		t.Errorf("错误码应为 40401，实际 %d", code)
	}
}

// ---- 静态资源 ----

func TestRouteThemeAssets(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", `"Pages":["/"]`, themeIndexMarker)
	env.writeAsset("demo", "app.js", "console.log('theme')")
	env.writeAsset("demo", "style.css", "body{}")
	if err := env.Svc.SetActive(t.Context(), "demo", "t"); err != nil {
		t.Fatal(err)
	}
	engine := newTestRouter(env)

	// 正常读取 + immutable 长缓存
	w := do(t, engine, http.MethodGet, "/theme-assets/app.js", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "console.log('theme')" {
		t.Errorf("内容不符: %q", w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control 应含 immutable，实际 %q", cc)
	}

	if w := do(t, engine, http.MethodGet, "/theme-assets/style.css", nil, ""); w.Code != http.StatusOK {
		t.Errorf("css 应可读，实际 %d", w.Code)
	}

	// 不存在 → 40401（不回退内嵌主题的同名文件）
	w2 := do(t, engine, http.MethodGet, "/theme-assets/missing.js", nil, "")
	if code, _, _ := decodeEnvelope(t, w2); code != 40401 {
		t.Errorf("缺失资源应为 40401，实际 %d", code)
	}

	// 路径穿越 → 40401，且绝不返回主题目录外的文件
	secret := filepath.Join(env.ThemesDir, "..", "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP-SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"/theme-assets/../manifest.json",
		"/theme-assets/../../secret.txt",
		"/theme-assets/..%2fmanifest.json",
		"/theme-assets/a/../../secret.txt",
	} {
		w := do(t, engine, http.MethodGet, p, nil, "")
		if strings.Contains(w.Body.String(), "TOP-SECRET") {
			t.Errorf("%s 泄露了主题目录外的文件！", p)
		}
		if w.Code == http.StatusOK {
			t.Errorf("%s 不应返回 200，实际 %d（body=%s）", p, w.Code, truncate(w.Body.String()))
		}
	}
}

func TestRouteSpaAssetsAndThemesPath(t *testing.T) {
	env := newTestEnv(t)
	env.seedDefault()
	engine := newTestRouter(env)

	// /assets/** 属于内置 SPA：真实构建产物应当可取到
	spaAssets := spaAssetPaths(t)
	if len(spaAssets) == 0 {
		t.Skip("内置 SPA 未构建（internal/webfs/dist 只有占位文件），跳过 /assets 断言")
	}
	w := do(t, engine, http.MethodGet, "/assets/"+spaAssets[0], nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("/assets/%s 应可读，实际 %d", spaAssets[0], w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control 应含 immutable，实际 %q", cc)
	}

	// /assets 下的路径穿越必须被拒（且不能返回 index.html）
	for _, p := range []string{"/assets/../index.html", "/assets/../../secret.txt"} {
		w := do(t, engine, http.MethodGet, p, nil, "")
		if w.Code == http.StatusOK {
			t.Errorf("%s 不应返回 200（实际 body=%s）", p, truncate(w.Body.String()))
		}
	}

	// /themes/** 永远 404：不暴露主题目录与 manifest
	for _, p := range []string{
		"/themes/default/manifest.json",
		"/themes/../../etc/passwd",
		"/themes/default/index.html",
	} {
		w := do(t, engine, http.MethodGet, p, nil, "")
		if code, _, _ := decodeEnvelope(t, w); code != 40401 {
			t.Errorf("%s 应为 40401，实际 %d（body=%s）", p, code, truncate(w.Body.String()))
		}
	}
}

func TestRouteFavicon(t *testing.T) {
	env := newTestEnv(t)
	env.writeTheme("demo", `"Pages":["/"]`, themeIndexMarker)
	env.writeAsset("demo", "favicon.ico", "THEME-ICO")
	if err := env.Svc.SetActive(t.Context(), "demo", "t"); err != nil {
		t.Fatal(err)
	}
	engine := newTestRouter(env)

	// 主题有 favicon → 用它
	w := do(t, engine, http.MethodGet, "/favicon.ico", nil, "")
	if w.Code != http.StatusOK || w.Body.String() != "THEME-ICO" {
		t.Errorf("应优先主题 favicon，实际 %d %q", w.Code, w.Body.String())
	}

	// 主题没有 → 回退内置 SPA 的 favicon（没有则 40401）
	env.writeTheme("plain", `"Pages":["/"]`, themeIndexMarker)
	if err := env.Svc.SetActive(t.Context(), "plain", "t"); err != nil {
		t.Fatal(err)
	}
	w2 := do(t, engine, http.MethodGet, "/favicon.ico", nil, "")
	if w2.Code != http.StatusOK && w2.Code != http.StatusNotFound {
		t.Errorf("应回退内置 favicon 或 404，实际 %d", w2.Code)
	}
	if strings.Contains(w2.Body.String(), "THEME-ICO") {
		t.Error("切换主题后不应再返回上一个主题的 favicon（缓存由 no-cache 策略保证）")
	}
}

// ---- 永不白屏 ----

func TestRouteNeverBlankWhenThemesDirDeleted(t *testing.T) {
	env := newTestEnv(t)
	env.seedDefault()
	engine := newTestRouter(env)

	// 首页先能开
	if w := do(t, engine, http.MethodGet, "/", nil, ""); w.Code != http.StatusOK {
		t.Fatalf("初始首页不可用: %d", w.Code)
	}

	// 删掉整个主题目录
	if err := os.RemoveAll(env.ThemesDir); err != nil {
		t.Fatal(err)
	}

	// 首页仍应由**内嵌默认主题**渲染（永不白屏）
	w := do(t, engine, http.MethodGet, "/", nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("删掉主题目录后首页不应失败，实际 %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), embeddedThemeMarker) {
		t.Errorf("应回退内嵌默认主题渲染首页:\n%s", truncate(w.Body.String()))
	}

	// 登录页与后台仍可用（内置 SPA）
	for _, p := range []string{"/login", "/admin/themes"} {
		w := do(t, engine, http.MethodGet, p, nil, "")
		if w.Code != http.StatusOK || !isSPAResponse(w.Body.String()) {
			t.Errorf("%s 应始终可用（内置 SPA），实际 %d", p, w.Code)
		}
	}

	// site/config 仍返回可用信息
	w2 := do(t, engine, http.MethodGet, "/api/web/v1/site/config", nil, "")
	if code, _, data := decodeEnvelope(t, w2); code != 0 || data["Theme"] == nil {
		t.Errorf("删掉主题目录后 site/config 仍应可用: %#v", data)
	}
}

func TestRouteHeadAndNonGetOnUnknownPath(t *testing.T) {
	env := newTestEnv(t)
	env.seedDefault()
	engine := newTestRouter(env)

	// 非 GET/HEAD 的未知路径不应拿到 SPA HTML
	w := do(t, engine, http.MethodPost, "/some/unknown", []byte("{}"), "application/json")
	if w.Code == http.StatusOK && isSPAResponse(w.Body.String()) {
		t.Error("非 GET 请求不应返回 SPA HTML")
	}

	// HEAD 应当与 GET 一致（gin 会自动处理 body）
	if w := do(t, engine, http.MethodHead, "/login", nil, ""); w.Code != http.StatusOK {
		t.Errorf("HEAD /login 应 200，实际 %d", w.Code)
	}
}

// ---- 辅助 ----

// spaAssetPaths 返回内置 SPA 的 assets/ 下的文件名（未构建时返回空）。
func spaAssetPaths(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join("internal", "webfs", "dist", "assets")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

func truncate(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}
