package theme

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// ---- LocalizedText（多语言文本，D98）----

func TestLocalizedTextResolve(t *testing.T) {
	raw := []byte(`{"zh-CN":"默认主题","en":"Default Theme","ja":"デフォルト"}`)
	var lt LocalizedText
	if err := json.Unmarshal(raw, &lt); err != nil {
		t.Fatalf("解析多语言对象失败: %v", err)
	}

	cases := []struct {
		lang string
		want string
	}{
		{"zh-CN", "默认主题"},
		{"zh", "默认主题"},      // 主语言子标签回退
		{"zh-Hans", "默认主题"}, // 未知变体 → 先试精确、再试主标签
		{"en", "Default Theme"},
		{"en-US", "Default Theme"},
		{"ja", "デフォルト"},
		{"fr", "默认主题"}, // 未支持的语言 → 回退 zh-CN
		{"", "默认主题"},   // 空语言 → 回退 zh-CN
	}
	for _, tc := range cases {
		if got := lt.Resolve(tc.lang); got != tc.want {
			t.Errorf("Resolve(%q) = %q，期望 %q", tc.lang, got, tc.want)
		}
	}
}

func TestLocalizedTextFallbacks(t *testing.T) {
	// 只有 en：未支持的请求语言应回退到 en（没有 zh-CN 时）
	var lt LocalizedText
	if err := json.Unmarshal([]byte(`{"en":"Only English"}`), &lt); err != nil {
		t.Fatal(err)
	}
	if got := lt.Resolve("fr"); got != "Only English" {
		t.Errorf("应回退到 en，实际 %q", got)
	}

	// 既无 zh-CN 也无 en：取字典里第一个非空值（按键名排序，结果稳定）
	var lt2 LocalizedText
	if err := json.Unmarshal([]byte(`{"zz":"Z","aa":"A"}`), &lt2); err != nil {
		t.Fatal(err)
	}
	if got := lt2.Resolve("fr"); got != "A" {
		t.Errorf("应取排序后第一个（aa=A），实际 %q", got)
	}

	// 单字符串形态
	var lt3 LocalizedText
	if err := json.Unmarshal([]byte(`"纯字符串"`), &lt3); err != nil {
		t.Fatal(err)
	}
	if got := lt3.Resolve("en"); got != "纯字符串" {
		t.Errorf("单字符串形态应原样返回，实际 %q", got)
	}
}

func TestLocalizedTextEmptyAndOddInput(t *testing.T) {
	type holder struct {
		Name LocalizedText `json:"Name"`
	}

	// 非字符串类型不应让解析失败（可选字段写错不该拖垮整个主题）
	for _, raw := range []string{`null`, `123`, `true`, `["a"]`, `{}`, `{"zh-CN":""}`, `""`} {
		var h holder
		if err := json.Unmarshal([]byte(`{"Name":`+raw+`}`), &h); err != nil {
			t.Errorf("输入 %s 不应报错: %v", raw, err)
		}
		if !h.Name.Empty() {
			t.Errorf("输入 %s 应被判定为空", raw)
		}
	}

	// 多语言对象里混入非字符串值 → 跳过该语言，其余仍可用
	var h holder
	if err := json.Unmarshal([]byte(`{"Name":{"en":123,"zh-CN":"好"}}`), &h); err != nil {
		t.Fatal(err)
	}
	if h.Name.Empty() {
		t.Error("应保留可用的 zh-CN 值")
	}
	if got := h.Name.Resolve("zh-CN"); got != "好" {
		t.Errorf("期望 好，实际 %q", got)
	}
}

func TestPreferenceLanguage(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"zh-CN,zh;q=0.9,en;q=0.8", "zh-CN"},
		{"en-US", "en-US"},
		{"en", "en"},
		{"", ""},
		{"   ", ""},
		{"a", ""},                     // 太短
		{"<script>", ""},              // 非法字符
		{"en;q=0.9", "en"},            // 去掉 q 参数
		{strings.Repeat("a", 40), ""}, // 过长
	}
	for _, tc := range cases {
		if got := PreferenceLanguage(tc.in); got != tc.want {
			t.Errorf("PreferenceLanguage(%q) = %q，期望 %q", tc.in, got, tc.want)
		}
	}
}

// ---- manifest 解析 ----

// validManifestJSON 是一份**最小合法**的 manifest。
const validManifestJSON = `{
  "ID": "demo",
  "Name": "示例主题",
  "Version": "1.2.3",
  "Pages": ["/"],
  "Configuration": {
    "Type": "managed",
    "Items": [
      { "Key": "BackgroundURL", "Name": "背景图", "Type": "string", "Default": "https://x/y.png" }
    ]
  }
}`

func mustParse(t *testing.T, raw string) *Manifest {
	t.Helper()
	m, err := ParseManifest([]byte(raw), defaultMaxManifestBytes)
	if err != nil {
		t.Fatalf("解析 manifest 失败: %v", err)
	}
	return m
}

func TestParseManifestValid(t *testing.T) {
	m := mustParse(t, validManifestJSON)
	if m.ID != "demo" {
		t.Errorf("ID = %q", m.ID)
	}
	if m.Name.Resolve("zh-CN") != "示例主题" {
		t.Errorf("Name 解析错误: %q", m.Name.Resolve("zh-CN"))
	}
	if err := m.Validate("demo", true); err != nil {
		t.Errorf("合法 manifest 校验失败: %v", err)
	}
	if got := m.EffectivePages(); len(got) != 1 || got[0] != "/" {
		t.Errorf("Pages = %v", got)
	}
	if m.ConfigType() != ConfigTypeManaged {
		t.Errorf("ConfigType = %q", m.ConfigType())
	}
}

func TestParseManifestTooLarge(t *testing.T) {
	raw := []byte(validManifestJSON)
	if _, err := ParseManifest(raw, int64(len(raw)-1)); !errors.Is(err, ErrManifestTooLarge) {
		t.Errorf("超过上限应返回 ErrManifestTooLarge，实际 %v", err)
	}
	// 不限制时应当通过
	if _, err := ParseManifest(raw, 0); err != nil {
		t.Errorf("maxBytes=0 表示不限制，实际 %v", err)
	}
}

func TestParseManifestInvalidJSON(t *testing.T) {
	if _, err := ParseManifest([]byte("{"), defaultMaxManifestBytes); !errors.Is(err, ErrManifestInvalid) {
		t.Errorf("非法 JSON 应返回 ErrManifestInvalid，实际 %v", err)
	}
}

func TestParseManifestIgnoresUnknownFields(t *testing.T) {
	// 多写的键不该让主题装载失败（D77 只追加原则）
	raw := `{"ID":"demo","Name":"n","UnknownField":{"a":1},"Pages":["/"]}`
	m := mustParse(t, raw)
	if err := m.Validate("demo", true); err != nil {
		t.Errorf("未知字段不应导致校验失败: %v", err)
	}
}

func TestEffectivePagesDefault(t *testing.T) {
	cases := []struct {
		name  string
		pages string
		want  []string
	}{
		{"未声明", ``, []string{"/"}},
		{"null", `"Pages": null,`, []string{"/"}},
		{"空数组", `"Pages": [],`, []string{"/"}},
		{"只含空白项", `"Pages": ["  "],`, []string{"/"}},
		{"显式多项", `"Pages": ["/","/gallery"],`, []string{"/", "/gallery"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"ID":"d","Name":"n",` + tc.pages + `"Configuration":{"Type":"managed"}}`
			m := mustParse(t, raw)
			got := m.EffectivePages()
			if len(got) != len(tc.want) {
				t.Fatalf("Pages = %v，期望 %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Pages = %v，期望 %v", got, tc.want)
				}
			}
		})
	}
}

// ---- 校验清单（D98 七项）----

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name      string
		manifest  string
		dirName   string
		hasIndex  bool
		wantErr   error
		substring string
	}{
		{
			name:     "ID 为空",
			manifest: `{"ID":"","Name":"n","Configuration":{"Type":"managed"}}`,
			dirName:  "", hasIndex: true,
			wantErr: ErrThemeInvalid,
		},
		{
			name:     "ID 格式非法",
			manifest: `{"ID":"../evil","Name":"n","Configuration":{"Type":"managed"}}`,
			dirName:  "../evil", hasIndex: true,
			wantErr: ErrThemeInvalid,
		},
		{
			name:     "ID 与目录名不一致",
			manifest: `{"ID":"demo","Name":"n","Configuration":{"Type":"managed"}}`,
			dirName:  "other", hasIndex: true,
			wantErr: ErrThemeInvalid,
		},
		{
			name:     "Name 为空",
			manifest: `{"ID":"demo","Name":"","Configuration":{"Type":"managed"}}`,
			dirName:  "demo", hasIndex: true,
			wantErr: ErrThemeInvalid,
		},
		{
			name:     "Name 多语言全为空",
			manifest: `{"ID":"demo","Name":{"zh-CN":"","en":""},"Configuration":{"Type":"managed"}}`,
			dirName:  "demo", hasIndex: true,
			wantErr: ErrThemeInvalid,
		},
		{
			name:     "Type 为 raw",
			manifest: `{"ID":"demo","Name":"n","Configuration":{"Type":"raw"}}`,
			dirName:  "demo", hasIndex: true,
			wantErr:   ErrThemeInvalid,
			substring: "不支持的主题类型",
		},
		{
			name:     "Type 为 redirect",
			manifest: `{"ID":"demo","Name":"n","Configuration":{"Type":"redirect"}}`,
			dirName:  "demo", hasIndex: true,
			wantErr:   ErrThemeInvalid,
			substring: "不支持的主题类型",
		},
		{
			name:     "Type 未知",
			manifest: `{"ID":"demo","Name":"n","Configuration":{"Type":"weird"}}`,
			dirName:  "demo", hasIndex: true,
			wantErr:   ErrThemeInvalid,
			substring: "不支持的主题类型",
		},
		{
			name: "配置项 Key 为空",
			manifest: `{"ID":"demo","Name":"n","Configuration":{"Type":"managed",
			  "Items":[{"Key":"","Type":"string"}]}}`,
			dirName: "demo", hasIndex: true,
			wantErr:   ErrThemeInvalid,
			substring: "Key 不能为空",
		},
		{
			name: "配置项 Key 重复",
			manifest: `{"ID":"demo","Name":"n","Configuration":{"Type":"managed",
			  "Items":[{"Key":"A","Type":"string"},{"Key":"A","Type":"string"}]}}`,
			dirName: "demo", hasIndex: true,
			wantErr:   ErrThemeInvalid,
			substring: "重复",
		},
		{
			name: "配置项 Key 格式非法",
			manifest: `{"ID":"demo","Name":"n","Configuration":{"Type":"managed",
			  "Items":[{"Key":"1bad","Type":"string"}]}}`,
			dirName: "demo", hasIndex: true,
			wantErr: ErrThemeInvalid,
		},
		{
			name: "配置项类型未知",
			manifest: `{"ID":"demo","Name":"n","Configuration":{"Type":"managed",
			  "Items":[{"Key":"A","Type":"nope"}]}}`,
			dirName: "demo", hasIndex: true,
			wantErr:   ErrThemeInvalid,
			substring: "不受支持",
		},
		{
			name: "select 缺 Options",
			manifest: `{"ID":"demo","Name":"n","Configuration":{"Type":"managed",
			  "Items":[{"Key":"A","Type":"select"}]}}`,
			dirName: "demo", hasIndex: true,
			wantErr:   ErrThemeInvalid,
			substring: "缺少 Options",
		},
		{
			name:     "缺 index.html",
			manifest: validManifestJSON,
			dirName:  "demo", hasIndex: false,
			wantErr: ErrIndexMissing,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseManifest([]byte(tc.manifest), defaultMaxManifestBytes)
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			err = m.Validate(tc.dirName, tc.hasIndex)
			if err == nil {
				t.Fatal("应当校验失败，但通过了")
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("错误应为 %v，实际 %v", tc.wantErr, err)
			}
			if tc.substring != "" && !strings.Contains(err.Error(), tc.substring) {
				t.Errorf("错误信息应包含 %q，实际 %q", tc.substring, err.Error())
			}
		})
	}
}

func TestValidateAcceptsManagedImplicit(t *testing.T) {
	// 省略 Configuration.Type 等价于 managed
	m := mustParse(t, `{"ID":"demo","Name":"n"}`)
	if m.ConfigType() != ConfigTypeManaged {
		t.Errorf("缺省应为 managed，实际 %q", m.ConfigType())
	}
	if err := m.Validate("demo", true); err != nil {
		t.Errorf("应当通过校验: %v", err)
	}
}

// ---- Pages 校验（D94.2 / D98 第 7 条）----

func TestValidatePages(t *testing.T) {
	valid := [][]string{
		{"/"},
		{"/", "/gallery"},
		{"/", "/upload", "/gallery", "/jobs", "/logs", "/settings"},
		{"/"},                  // 精确匹配，不与 /login 冲突
		{"/*"},                 // 通配：允许（接管所有非保留业务页面）
		{"/apixyz"},            // 不是 /api 的路径段边界，合法
		{"/theme-assets-page"}, // 同样不是保留前缀
		{"/adminx"},            // 不是 /admin 的路径段边界
	}
	for _, pages := range valid {
		if err := ValidatePages(pages); err != nil {
			t.Errorf("Pages %v 应当合法，实际 %v", pages, err)
		}
	}

	invalid := [][]string{
		{""},                 // 空项
		{"gallery"},          // 不以 / 开头
		{"/../etc"},          // 含 ..
		{"/login"},           // 认证页
		{"/login/x"},         // 认证页子路径
		{"/first-login"},     // 认证页
		{"/forgot-password"}, // 认证页
		{"/reset-password"},  // 认证页
		{"/logout"},          // 认证页
		{"/admin"},           // 后台
		{"/admin/users"},     // 后台子路径
		{"/admin/settings"},  // 后台子路径
		{"/api"},             // 系统路径
		{"/api/web/v1/x"},    // 系统路径子路径
		{"/healthz"},         // 系统路径
		{"/theme-assets"},    // 系统路径
		{"/assets"},          // 系统路径
		{"/themes"},          // 系统路径
		{"/favicon.ico"},     // 系统路径
		{"/foo*"},            // 只允许 /* 一种通配
		{"/**"},              // 同上
		{"/gallery*"},        // 同上
	}
	for _, pages := range invalid {
		if err := ValidatePages(pages); err == nil {
			t.Errorf("Pages %v 应当非法，但通过了", pages)
		} else if !errors.Is(err, ErrThemeInvalid) {
			t.Errorf("Pages %v 的错误应为 ErrThemeInvalid，实际 %v", pages, err)
		}
	}
}

// ---- 路径覆盖与最长前缀匹配 ----

func TestPathCovers(t *testing.T) {
	cases := []struct {
		prefix, target string
		want           bool
	}{
		{"/", "/", true},
		{"/", "/login", false}, // "/" 是精确匹配，不覆盖其他路径
		{"/", "/gallery", false},
		{"/gallery", "/gallery", true},
		{"/gallery", "/gallery/abc", true},
		{"/gallery", "/galleryx", false}, // 路径段边界
		{"/admin", "/admin", true},
		{"/admin", "/admin/users", true},
		{"/admin", "/administrator", false},
	}
	for _, tc := range cases {
		if got := pathCovers(tc.prefix, tc.target); got != tc.want {
			t.Errorf("pathCovers(%q, %q) = %v，期望 %v", tc.prefix, tc.target, got, tc.want)
		}
	}
}

func TestMatchPageLongestPrefix(t *testing.T) {
	pages := []string{"/", "/gallery"}

	// /gallery/abc 必须命中更具体的 /gallery，而不是 /
	if got, ok := MatchPage(pages, "/gallery/abc"); !ok || got != "/gallery" {
		t.Errorf("/gallery/abc 应命中 /gallery，实际 %q（ok=%v）", got, ok)
	}
	if got, ok := MatchPage(pages, "/gallery"); !ok || got != "/gallery" {
		t.Errorf("/gallery 应命中 /gallery，实际 %q", got)
	}
	// 只有 / 精确匹配
	if got, ok := MatchPage(pages, "/"); !ok || got != "/" {
		t.Errorf("/ 应命中 /，实际 %q", got)
	}
	// 未注册的路径不命中
	if _, ok := MatchPage(pages, "/upload"); ok {
		t.Error("/upload 不应命中")
	}
}

func TestMatchPageWildcard(t *testing.T) {
	// /* 接管所有非保留业务页面；但更具体的前缀优先
	pages := []string{"/*", "/gallery"}
	if got, ok := MatchPage(pages, "/gallery"); !ok || got != "/gallery" {
		t.Errorf("更具体的前缀应优先，实际 %q", got)
	}
	if got, ok := MatchPage(pages, "/upload"); !ok || got != "/*" {
		t.Errorf("/upload 应命中通配 /*，实际 %q", got)
	}
	if got, ok := MatchPage([]string{"/*"}, "/anything"); !ok || got != "/*" {
		t.Errorf("通配应命中任意路径，实际 %q（ok=%v）", got, ok)
	}
}

func TestIsAuthPageAndAdmin(t *testing.T) {
	for _, p := range AuthPagePaths {
		if !IsAuthPage(p) {
			t.Errorf("IsAuthPage(%q) 应为真", p)
		}
		if !IsAuthPage(p + "/sub") {
			t.Errorf("IsAuthPage(%q) 应为真", p+"/sub")
		}
	}
	if IsAuthPage("/loginx") {
		t.Error("/loginx 不是认证页")
	}
	if !IsAdminPath("/admin") || !IsAdminPath("/admin/users") {
		t.Error("IsAdminPath 应覆盖 /admin 与其子路径")
	}
	if IsAdminPath("/administrator") {
		t.Error("/administrator 不是后台路径")
	}
}

func TestIsReservedSystemPath(t *testing.T) {
	for _, p := range []string{"/api", "/api/web/v1/x", "/healthz", "/theme-assets/a.js", "/assets/a.js", "/themes/x/manifest.json", "/favicon.ico"} {
		if !IsReservedSystemPath(p) {
			t.Errorf("IsReservedSystemPath(%q) 应为真", p)
		}
	}
	for _, p := range []string{"/api-x", "/healthz2", "/assets2", "/gallery", "/", "/login"} {
		if IsReservedSystemPath(p) {
			t.Errorf("IsReservedSystemPath(%q) 应为假", p)
		}
	}
}

func TestRepoOrURL(t *testing.T) {
	m := mustParse(t, `{"ID":"d","Name":"n","URL":"https://a","Repo":"https://b"}`)
	if m.RepoOrURL() != "https://b" {
		t.Errorf("Repo 优先，实际 %q", m.RepoOrURL())
	}
	m2 := mustParse(t, `{"ID":"d","Name":"n","URL":"https://a"}`)
	if m2.RepoOrURL() != "https://a" {
		t.Errorf("无 Repo 时应回退 URL，实际 %q", m2.RepoOrURL())
	}
}
