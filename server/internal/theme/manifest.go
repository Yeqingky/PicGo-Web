package theme

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// 主题 manifest 的元数据文件名。
const ManifestFile = "manifest.json"

// IndexFile 是主题入口文件名。
const IndexFile = "index.html"

// AssetsDir 是主题静态资源目录名。
const AssetsDir = "assets"

// ScreenshotFile 是可选预览图文件名。
const ScreenshotFile = "screenshot.png"

// manifestIDPattern 是主题 ID 的合法格式（D98 校验清单第 1 条）。
//
// 限制字符集是为了防路径穿越与怪字符（ID 同时用作目录名）。
var manifestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// configKeyPattern 是主题配置项 Key 的合法格式（D98 校验清单第 4 条）。
var configKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

// 配置项类型（D98「配置项类型」表）。
//
// 注意与**插件 schema**（input/password/list/checkbox/confirm/editor）是两套命名，
// 前端用一层适配把两者规约到同一组内部类型（DESIGN.md §7）。
const (
	TypeString = "string"
	TypeText   = "text"
	TypeNumber = "number"
	TypeSwitch = "switch"
	TypeSelect = "select"
	TypeJSON   = "json"
)

// Configuration.Type 的取值（D98）。
const (
	// ConfigTypeManaged 由 Items 声明 schema，后台自动渲染表单 —— **唯一实现**。
	ConfigTypeManaged = "managed"
	// ConfigTypeRaw 不实现（等于任意 HTML/JS 注入，与 D92 冲突）。
	ConfigTypeRaw = "raw"
	// ConfigTypeRedirect 不实现（v1 不做外部托管）。
	ConfigTypeRedirect = "redirect"
)

// DefaultPages 是 manifest 不写 `Pages` 时的默认接管范围。
//
// `["/"]` 表示**精确匹配 `/` 一个路径**（不是「接管全部」）。
func DefaultPages() []string { return []string{"/"} }

// WildcardPage 是唯一的通配写法：接管**所有非保留业务页面**。
const WildcardPage = "/*"

// LocalizedText 是支持多语言对象形态的文本字段（D98「多语言文本」）。
//
// 允许两种 JSON 形态：
//
//	"Name": "默认主题"                                        // 单字符串
//	"Name": { "zh-CN": "默认主题", "en": "Default Theme" }     // 多语言对象
//
// 其它类型（数字、数组、null）会被当作空值而不是报错：
// manifest 的可选文本字段写错不该让整个主题装载失败，
// 而**必填**字段的空值由 Validate 统一拦下。
type LocalizedText struct {
	// flat 是单字符串形态的原值。
	flat string
	// dict 是多语言对象形态（保持键的原始大小写）。
	dict map[string]string
}

// UnmarshalJSON 实现多形态解析。
func (t *LocalizedText) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}

	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		t.flat = s
		t.dict = nil
		return nil

	case '{':
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &raw); err != nil {
			return err
		}
		dict := make(map[string]string, len(raw))
		for k, v := range raw {
			var s string
			// 值不是字符串就跳过该语言，而不是整体报错
			if err := json.Unmarshal(v, &s); err == nil {
				dict[k] = s
			}
		}
		t.dict = dict
		t.flat = ""
		return nil

	default:
		// 数字 / 布尔 / 数组：视为空值
		t.flat = ""
		t.dict = nil
		return nil
	}
}

// MarshalJSON 以「原样形态」序列化：单字符串原样返回，多语言对象原样返回。
func (t LocalizedText) MarshalJSON() ([]byte, error) {
	if len(t.dict) > 0 {
		return json.Marshal(t.dict)
	}
	return json.Marshal(t.flat)
}

// Empty 判断是否完全没有可用文本。
func (t LocalizedText) Empty() bool {
	if strings.TrimSpace(t.flat) != "" {
		return false
	}
	for _, v := range t.dict {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

// Resolve 按语言解析成单字符串（D98）。
//
// 顺序：请求语言的精确匹配 → 主语言子标签（zh-CN → zh）→ zh-CN → en →
// 字典里第一个非空值（按键名排序，保证结果稳定）→ 单字符串形态的原值。
func (t LocalizedText) Resolve(lang string) string {
	if len(t.dict) == 0 {
		return t.flat
	}

	if v := lookupLang(t.dict, lang); v != "" {
		return v
	}
	if primary := primarySubtag(lang); primary != "" {
		if v := lookupLang(t.dict, primary); v != "" {
			return v
		}
	}
	if v := lookupLang(t.dict, "zh-CN"); v != "" {
		return v
	}
	if v := lookupLang(t.dict, "en"); v != "" {
		return v
	}

	keys := make([]string, 0, len(t.dict))
	for k := range t.dict {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v := strings.TrimSpace(t.dict[k]); v != "" {
			return t.dict[k]
		}
	}
	return ""
}

// lookupLang 做大小写不敏感的精确语言匹配。
func lookupLang(dict map[string]string, lang string) string {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return ""
	}
	if v, ok := dict[lang]; ok && strings.TrimSpace(v) != "" {
		return v
	}
	for k, v := range dict {
		if strings.EqualFold(k, lang) && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// primarySubtag 取语言标签的主子标签（zh-CN → zh）。
func primarySubtag(lang string) string {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return ""
	}
	if i := strings.IndexAny(lang, "-_"); i > 0 {
		return lang[:i]
	}
	return ""
}

// PreferenceLanguage 从 Accept-Language 头解析出首选语言（D98）。
//
// 只取第一个可用的语言标签：形如 `zh-CN,zh;q=0.9,en;q=0.8` → `zh-CN`。
// 解析失败返回空串，由 LocalizedText.Resolve 走回退链。
func PreferenceLanguage(acceptLanguage string) string {
	acceptLanguage = strings.TrimSpace(acceptLanguage)
	if acceptLanguage == "" {
		return ""
	}
	first := acceptLanguage
	if i := strings.IndexByte(first, ','); i >= 0 {
		first = first[:i]
	}
	if i := strings.IndexByte(first, ';'); i >= 0 {
		first = first[:i]
	}
	first = strings.TrimSpace(first)
	if len(first) < 2 || len(first) > 32 {
		return ""
	}
	for _, r := range first {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return ""
		}
	}
	return first
}

// ConfigItem 是主题声明的一个配置项（D98「配置项类型」）。
type ConfigItem struct {
	// Key 是配置项的**存储键**（写入 ThemeConfigs.Key）。
	Key string `json:"Key"`
	// Name 是展示名（支持多语言）。
	Name LocalizedText `json:"Name"`
	// Type 见 TypeString / TypeText / TypeNumber / TypeSwitch / TypeSelect / TypeJSON。
	Type string `json:"Type"`
	// Required 为真时前端应校验非空。
	Required bool `json:"Required"`
	// Default 是代码默认值（三级兜底的第 2 层）。
	Default json.RawMessage `json:"Default"`
	// Help 是辅助说明（支持多语言）。
	Help LocalizedText `json:"Help"`
	// Options 是 select 的选项（逗号分隔）。
	Options string `json:"Options"`
	// ItemSchema 是 json 类型在「repeater 模式」下的子字段定义。
	ItemSchema []ConfigItem `json:"ItemSchema"`
}

// Configuration 是 manifest 的配置声明段。
type Configuration struct {
	// Type 只实现 managed（D98）。
	Type string `json:"Type"`
	// Items 是配置 schema。
	Items []ConfigItem `json:"Items"`
}

// Manifest 是主题的元数据（D98）。
//
// ⚠️ 这里**只承载 manifest 文件的内容**；主题目录、合法性等运行时信息在 Theme 里。
type Manifest struct {
	ID            string        `json:"ID"`
	Name          LocalizedText `json:"Name"`
	Description   LocalizedText `json:"Description"`
	Author        LocalizedText `json:"Author"`
	Version       string        `json:"Version"`
	URL           string        `json:"URL"`
	Repo          string        `json:"Repo"`
	Preview       string        `json:"Preview"`
	MinAppVersion string        `json:"MinAppVersion"`
	Tags          []string      `json:"Tags"`
	Pages         []string      `json:"Pages"`

	Configuration Configuration `json:"Configuration"`
}

// RepoOrURL 返回展示用的仓库地址（兼容 manifest 的两种写法）。
func (m *Manifest) RepoOrURL() string {
	if strings.TrimSpace(m.Repo) != "" {
		return m.Repo
	}
	return m.URL
}

// EffectivePages 返回归一化后的接管范围。
//
// 未声明或声明为空数组 → 默认 `["/"]`（D94.2）。
func (m *Manifest) EffectivePages() []string {
	if len(m.Pages) == 0 {
		return DefaultPages()
	}
	out := make([]string, 0, len(m.Pages))
	for _, p := range m.Pages {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return DefaultPages()
	}
	return out
}

// ConfigType 返回归一化后的 Configuration.Type（缺省即 managed）。
func (m *Manifest) ConfigType() string {
	t := strings.ToLower(strings.TrimSpace(m.Configuration.Type))
	if t == "" {
		return ConfigTypeManaged
	}
	return t
}

// 主题不合法时的哨兵错误（handler 据此映射错误码）。
var (
	// ErrManifestMissing manifest.json 不存在或不可读。
	ErrManifestMissing = errors.New("主题缺少 manifest.json")
	// ErrManifestTooLarge manifest.json 超过 theme.maxManifestBytes。
	ErrManifestTooLarge = errors.New("manifest.json 超过大小限制")
	// ErrManifestInvalid manifest.json 不是合法 JSON。
	ErrManifestInvalid = errors.New("manifest.json 格式错误")
	// ErrThemeInvalid 主题不合法（校验清单任一项失败）。
	ErrThemeInvalid = errors.New("主题不合法")
	// ErrIndexMissing 缺少 index.html。
	ErrIndexMissing = errors.New("主题缺少 index.html")
)

// ParseManifest 解析 manifest 字节。
//
// maxBytes ≤ 0 表示不限制。
func ParseManifest(raw []byte, maxBytes int64) (*Manifest, error) {
	if maxBytes > 0 && int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("%w: %d > %d 字节", ErrManifestTooLarge, len(raw), maxBytes)
	}

	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	// 忽略未知字段：manifest 里多写的键不该让主题装载失败（D77 只追加原则）
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrManifestInvalid, err)
	}
	return &m, nil
}

// Validate 执行 D98 的七项校验。
//
// dirName 是主题目录名，用于校验第 1 条「ID 与目录名一致」。
// hasIndex 表示 index.html 是否存在（由调用方探测，便于单测）。
func (m *Manifest) Validate(dirName string, hasIndex bool) error {
	// 1) ID 非空、格式合法、与目录名一致
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("%w: ID 不能为空", ErrThemeInvalid)
	}
	if !manifestIDPattern.MatchString(m.ID) {
		return fmt.Errorf("%w: ID %q 格式非法（只允许字母数字与 . _ -，长度 1~64）", ErrThemeInvalid, m.ID)
	}
	if dirName != "" && m.ID != dirName {
		return fmt.Errorf("%w: ID %q 与目录名 %q 不一致", ErrThemeInvalid, m.ID, dirName)
	}

	// 2) Name 至少一种语言非空
	if m.Name.Empty() {
		return fmt.Errorf("%w: Name 不能为空", ErrThemeInvalid)
	}

	// 3) Configuration.Type ∈ {空, managed}
	switch m.ConfigType() {
	case ConfigTypeManaged:
	case ConfigTypeRaw, ConfigTypeRedirect:
		return fmt.Errorf("%w: 不支持的主题类型 %q（v1 只实现 managed）", ErrThemeInvalid, m.Configuration.Type)
	default:
		return fmt.Errorf("%w: 不支持的主题类型 %q", ErrThemeInvalid, m.Configuration.Type)
	}

	// 4) Items[].Key 非空、唯一、格式合法
	seen := make(map[string]struct{}, len(m.Configuration.Items))
	for i, item := range m.Configuration.Items {
		key := strings.TrimSpace(item.Key)
		if key == "" {
			return fmt.Errorf("%w: Configuration.Items[%d].Key 不能为空", ErrThemeInvalid, i)
		}
		if !configKeyPattern.MatchString(key) {
			return fmt.Errorf("%w: 配置项 Key %q 格式非法", ErrThemeInvalid, key)
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("%w: 配置项 Key %q 重复", ErrThemeInvalid, key)
		}
		seen[key] = struct{}{}
		if err := validateConfigItemType(&m.Configuration.Items[i]); err != nil {
			return err
		}
	}

	// 5) index.html 存在
	if !hasIndex {
		return fmt.Errorf("%w: %s", ErrIndexMissing, IndexFile)
	}

	// 6) manifest 大小在 ParseManifest 里校验（这里无法再判断，由调用方传 maxBytes）

	// 7) Pages 合法
	if err := ValidatePages(m.EffectivePages()); err != nil {
		return err
	}

	// 附带：MinAppVersion 只做形态校验（不阻断装载，避免版本格式差异导致主题不可用）
	if mv := strings.TrimSpace(m.MinAppVersion); mv != "" && !isVersionLike(mv) {
		return fmt.Errorf("%w: MinAppVersion %q 格式非法", ErrThemeInvalid, mv)
	}

	return nil
}

// validateConfigItemType 校验单个配置项的 Type 与其附属字段是否自洽。
func validateConfigItemType(item *ConfigItem) error {
	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case TypeString, TypeText, TypeNumber, TypeSwitch, TypeJSON:
		return nil
	case TypeSelect:
		if strings.TrimSpace(item.Options) == "" {
			return fmt.Errorf("%w: 配置项 %q 类型为 select 但缺少 Options", ErrThemeInvalid, item.Key)
		}
		return nil
	case "":
		return fmt.Errorf("%w: 配置项 %q 缺少 Type", ErrThemeInvalid, item.Key)
	default:
		return fmt.Errorf("%w: 配置项 %q 的类型 %q 不受支持", ErrThemeInvalid, item.Key, item.Type)
	}
}

// isVersionLike 判断字符串是否形如 x.y.z（允许 1~4 段，段内为数字）。
func isVersionLike(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// 永久保留路径（D94.2 / OPERATIONS §8.1）——**代码硬编码，不是配置项**。
var (
	// reservedSystemPaths 是系统路径的生命周期前缀。
	reservedSystemPaths = []string{
		"/api",
		"/healthz",
		"/theme-assets",
		"/assets",
		"/themes",
		"/favicon.ico",
	}

	// AuthPagePaths 是认证页保留列表：主题**永远不能**接管。
	//
	// 导出是为了让路由分发与测试共用同一份真相源。
	AuthPagePaths = []string{
		"/login",
		"/first-login",
		"/forgot-password",
		"/reset-password",
		"/logout",
	}

	// AdminPathPrefix 是后台前缀：主题**永远不能**接管。
	AdminPathPrefix = "/admin"
)

// errPageReserved 表示某个 Pages 项命中了保留路径。
type errPageReserved struct {
	Page     string
	Reserved string
}

func (e *errPageReserved) Error() string {
	return fmt.Sprintf("%s: Pages 项 %q 与保留路径 %q 冲突（认证页与 /admin/** 永久保留，不可被主题接管）",
		ErrThemeInvalid.Error(), e.Page, e.Reserved)
}

// Unwrap 让 errors.Is(err, ErrThemeInvalid) 成立。
func (e *errPageReserved) Unwrap() error { return ErrThemeInvalid }

// ValidatePages 校验接管范围（D98 校验清单第 7 条 / D94.2）。
//
// 规则：
//   - 每项以 `/` 开头
//   - 不含 `..`（防穿越）
//   - 只允许 `/*` 这一种通配
//   - **不得命中保留路径、认证页或 /admin/**
//
// `"/"` 是**精确匹配** `/` 一个路径，因此它合法（不会与 `/login` 冲突）。
func ValidatePages(pages []string) error {
	for _, raw := range pages {
		page := strings.TrimSpace(raw)
		if page == "" {
			return fmt.Errorf("%w: Pages 含空项", ErrThemeInvalid)
		}
		if !strings.HasPrefix(page, "/") {
			return fmt.Errorf("%w: Pages 项 %q 必须以 / 开头", ErrThemeInvalid, page)
		}
		if strings.Contains(page, "..") {
			return fmt.Errorf("%w: Pages 项 %q 含 ..", ErrThemeInvalid, page)
		}

		// 通配只允许 /* 一种写法
		if strings.Contains(page, "*") {
			if page != WildcardPage {
				return fmt.Errorf("%w: Pages 项 %q 的通配写法非法（只允许 %q）", ErrThemeInvalid, page, WildcardPage)
			}
			// `/*` 明确表示「接管所有非保留业务页面」——保留路径仍由分发逻辑先拦下
			continue
		}

		if conflict := pageConflict(page); conflict != "" {
			return &errPageReserved{Page: page, Reserved: conflict}
		}
	}
	return nil
}

// pageConflict 判断一个（非通配）Pages 项是否与保留路径冲突。
//
// 冲突的两种方向：
//  1. 该项**覆盖**了保留路径（如 `/admin`、`/login`）
//  2. 该项**被**保留路径覆盖（如 `/admin/users`、`/api/x`）
//
// `"/"` 是精确匹配，只覆盖 `/` 自身，因此永不冲突。
func pageConflict(page string) string {
	if page == "/" {
		return ""
	}

	all := make([]string, 0, len(reservedSystemPaths)+len(AuthPagePaths)+1)
	all = append(all, reservedSystemPaths...)
	all = append(all, AuthPagePaths...)
	all = append(all, AdminPathPrefix)

	for _, r := range all {
		if pathCovers(page, r) || pathCovers(r, page) {
			return r
		}
	}
	return ""
}

// pathCovers 判断 prefix 是否覆盖 target。
//
// 语义（与分发时的最长前缀匹配一致）：
//   - `"/"` 只覆盖 `"/"` 自身（精确匹配，D94.2）
//   - 其它前缀按**路径段边界**匹配：`/gallery` 覆盖 `/gallery` 与 `/gallery/abc`，
//     但**不**覆盖 `/galleryx`
func pathCovers(prefix, target string) bool {
	if prefix == target {
		return true
	}
	if prefix == "/" {
		return false // 精确匹配，不覆盖其他路径
	}
	return strings.HasPrefix(target, prefix+"/")
}

// MatchPage 在 pages 中找出**覆盖** path 的最长前缀。
//
// 返回 (命中的 page, true)；无命中返回 ("", false)。
// 最长匹配优先：`/gallery` 比 `/` 更具体，先命中 `/gallery`。
func MatchPage(pages []string, path string) (string, bool) {
	best := ""
	for _, page := range pages {
		if page == WildcardPage {
			// 通配的优先级最低：只有当没有更具体的前缀时才用它
			if best == "" {
				best = page
			}
			continue
		}
		if !pathCovers(page, path) {
			continue
		}
		if len(page) > len(best) || best == WildcardPage {
			best = page
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}

// IsAuthPage 判断 path 是否属于认证页保留列表（含其子路径）。
func IsAuthPage(path string) bool {
	for _, p := range AuthPagePaths {
		if pathCovers(p, path) {
			return true
		}
	}
	return false
}

// IsAdminPath 判断 path 是否属于后台（含子路径）。
func IsAdminPath(path string) bool {
	return pathCovers(AdminPathPrefix, path)
}

// IsReservedSystemPath 判断 path 是否属于系统路径（非页面路由）。
func IsReservedSystemPath(path string) bool {
	for _, p := range reservedSystemPaths {
		if p == "/favicon.ico" {
			if path == p {
				return true
			}
			continue
		}
		if pathCovers(p, path) {
			return true
		}
	}
	return false
}
