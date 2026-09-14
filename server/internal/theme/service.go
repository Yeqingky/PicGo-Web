package theme

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gorm.io/gorm"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// AssetBasePath 是**主题**静态资源前缀（D99.2）。
//
// 与内置 SPA 的 `/assets/**` 严格分离：主题资源走这里，
// 内置 SPA 资源走 `/assets/**`。两者混用会导致主题覆盖后台/登录页的资源。
const AssetBasePath = "/theme-assets"

// SettingsSourceDB / SettingsSourceDefault 是配置值的来源（D95）。
const (
	SettingsSourceDB      = "db"
	SettingsSourceDefault = "default"
)

// Options 构造 Service 的依赖。
type Options struct {
	// ThemesDir 主题根目录（<dataDir>/themes）。
	ThemesDir string
	// SeedFrom 可选的 seed 源目录（`PICGO_WEB_THEME_SEED`）；空则用内嵌默认主题。
	SeedFrom string
	// DB 用于读写 ThemeConfigs 表。
	DB *gorm.DB
	// Settings 读写 SiteSettings（theme.active 与五个阈值键）。
	Settings SettingsProvider
	// Log 进程日志。
	Log *slog.Logger
	// Auditor 操作日志（不注入时静默）。
	Auditor Auditor
}

// Service 是主题系统的门面：列表 / 装载 / 切换 / 配置 / 安装 / 卸载。
//
// 并发安全：内部无共享可变状态（Store 无状态，configStore 走 DB），
// 唯一的可变状态是 warnOnce（自带锁）。
type Service struct {
	store    *Store
	cfg      *configStore
	settings SettingsProvider
	log      *slog.Logger
	auditor  Auditor

	// fallbackOnce 让「回退内嵌主题」只写一次 theme.error，
	// 否则每个页面请求都会写一条（分发路径会频繁调用 Current）。
	fallbackOnce *warnOnce
}

// New 构造主题服务。
func New(opts Options) (*Service, error) {
	if strings.TrimSpace(opts.ThemesDir) == "" {
		return nil, errors.New("theme: ThemesDir 不能为空")
	}
	if opts.DB == nil {
		return nil, errors.New("theme: DB 不能为空")
	}

	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	auditor := ensureNoop(opts.Auditor)

	limits := readLimits(opts.Settings)
	store := NewStore(StoreOptions{
		ThemesDir:        opts.ThemesDir,
		SeedFrom:         opts.SeedFrom,
		MaxManifestBytes: limits.MaxManifestBytes,
		Log:              log,
		Auditor:          auditor,
	})

	return &Service{
		store:        store,
		cfg:          &configStore{db: opts.DB},
		settings:     opts.Settings,
		log:          log,
		auditor:      auditor,
		fallbackOnce: newWarnOnce(),
	}, nil
}

// ThemesDir 返回主题根目录（供只读用途）。
func (s *Service) ThemesDir() string { return s.store.ThemesDir() }

// Store 暴露底层 Store（供分发逻辑按路径查主题）。
func (s *Service) Store() *Store { return s.store }

// Seed 在主题目录为空时写出内嵌默认主题（D94.4）。
func (s *Service) Seed() (bool, error) { return s.store.Seed() }

// Active 返回当前启用的主题 ID（缺省 default）。
func (s *Service) Active() string {
	if s.settings == nil {
		return embeddedDefaultID
	}
	id := strings.TrimSpace(s.settings.GetString(KeyActiveTheme))
	if id == "" {
		return embeddedDefaultID
	}
	return id
}

// Current 返回**当前生效**的主题（带兜底，永不返回 nil）。
//
// 这是分发路径上的入口：主题缺失/损坏/Pages 非法时回退内嵌默认主题，
// 并（按原因去重后）写一条 `theme.error`。
func (s *Service) Current() *Theme {
	active := s.Active()
	th := s.store.Resolve(active)

	if th.Fallback {
		key := active + "|" + th.FallbackReason
		if s.fallbackOnce.shouldFire(key) {
			s.log.Warn("当前主题不可用，已回退内嵌默认主题", "active", active, "reason", th.FallbackReason)
			s.auditor.Log(contextBackground(), AuditEntry{
				Type:       model.LogTypeThemeError,
				Status:     model.LogStatusFailed,
				TargetType: "theme",
				TargetUID:  active,
				Detail:     map[string]any{"Reason": th.FallbackReason, "Fallback": true},
			})
		}
	}
	return th
}

// ---- 列表 ----

// ListItem 是后台主题列表的一项（API.md §10 的 `/themes` 响应元素）。
type ListItem struct {
	ID            string   `json:"ID"`
	Name          string   `json:"Name"`
	Version       string   `json:"Version"`
	Description   string   `json:"Description"`
	Author        string   `json:"Author"`
	Tags          []string `json:"Tags"`
	Repo          string   `json:"Repo"`
	URL           string   `json:"URL"`
	MinAppVersion string   `json:"MinAppVersion"`
	Pages         []string `json:"Pages"`

	IsActive     bool `json:"IsActive"`
	IsBuiltin    bool `json:"IsBuiltin"`
	CanUninstall bool `json:"CanUninstall"`

	SettingCount int `json:"SettingCount"`

	ScreenshotURL string `json:"ScreenshotURL"`

	// Valid 为 false 时该主题不可启用，Error 说明原因（缺 manifest / ID 不匹配 / 缺 index.html …）。
	Valid bool   `json:"Valid"`
	Error string `json:"Error"`
}

// ListResult 是 `/themes` 与 `/themes/rescan` 的响应数据。
type ListResult struct {
	Active    string     `json:"Active"`
	Items     []ListItem `json:"Items"`
	ScannedAt int64      `json:"ScannedAt"`
}

// List 扫描并返回全部主题（含**不合法**的，Valid=false，便于后台修复）。
//
// lang 用于解析多语言文本（来自请求的 Accept-Language）。
func (s *Service) List(lang string) *ListResult {
	active := s.Active()
	scan := s.store.Scan()

	res := &ListResult{
		Active:    active,
		ScannedAt: scan.ScannedAt,
		Items:     make([]ListItem, 0, len(scan.Themes)+len(scan.Invalid)),
	}

	// 合法主题
	for _, th := range scan.Themes {
		res.Items = append(res.Items, s.listItemFromTheme(th, active, lang))
	}

	// 不合法主题：仍然列出（Valid=false + Error），否则管理员无法在界面上发现与修复
	for _, inv := range scan.Invalid {
		res.Items = append(res.Items, ListItem{
			ID:            inv.ID,
			Name:          inv.ID,
			Pages:         []string{},
			Tags:          []string{},
			Valid:         false,
			Error:         inv.Error,
			IsActive:      false,
			IsBuiltin:     inv.ID == embeddedDefaultID,
			CanUninstall:  inv.ID != embeddedDefaultID && inv.ID != active,
			ScreenshotURL: screenshotURL(inv.ID),
		})
	}

	sort.Slice(res.Items, func(i, j int) bool {
		// 合法的排前面，其次按 ID
		if res.Items[i].Valid != res.Items[j].Valid {
			return res.Items[i].Valid
		}
		return res.Items[i].ID < res.Items[j].ID
	})
	return res
}

func (s *Service) listItemFromTheme(th *Theme, active, lang string) ListItem {
	tags := th.Manifest.Tags
	if tags == nil {
		tags = []string{}
	}
	pages := th.Pages()
	if pages == nil {
		pages = []string{}
	}

	return ListItem{
		ID:            th.ID,
		Name:          th.Name(lang),
		Version:       th.Manifest.Version,
		Description:   th.Description(lang),
		Author:        th.Author(lang),
		Tags:          tags,
		Repo:          th.Manifest.RepoOrURL(),
		URL:           th.Manifest.URL,
		MinAppVersion: th.Manifest.MinAppVersion,
		Pages:         pages,
		IsActive:      th.ID == active,
		IsBuiltin:     th.Builtin,
		CanUninstall:  !th.Builtin && th.ID != active,
		SettingCount:  len(th.ConfigItems()),
		ScreenshotURL: screenshotURL(th.ID),
		Valid:         true,
	}
}

// screenshotURL 生成后台预览图地址（走内部 API，不是静态资源）。
func screenshotURL(themeID string) string {
	return "/api/web/v1/themes/" + themeID + "/screenshot"
}

// ---- 切换 ----

// SetActive 切换当前主题（D94.4：立即生效，无需重启）。
func (s *Service) SetActive(ctx context.Context, themeID, by string) error {
	id := strings.TrimSpace(themeID)
	if id == "" {
		return fmt.Errorf("%w: ThemeID 不能为空", ErrNotFound)
	}

	th, err := s.store.Load(id)
	if err != nil {
		return err
	}
	if th.Fallback {
		// 目标主题自身就是兜底副本，说明它的磁盘副本不可用 → 不允许启用
		return fmt.Errorf("%w: %s（磁盘副本不可用）", ErrThemeInvalid, id)
	}

	previous := s.Active()
	if s.settings == nil {
		return errors.New("theme: 未配置 Settings，无法切换主题")
	}
	if err := s.settings.Set(KeyActiveTheme, id, by); err != nil {
		return fmt.Errorf("保存 theme.active 失败: %w", err)
	}

	s.log.Info("主题已切换", "from", previous, "to", id, "by", by)
	s.auditor.Log(ctx, AuditEntry{
		Type:       model.LogTypeThemeActivate,
		Status:     model.LogStatusSuccess,
		TargetType: "theme",
		TargetUID:  id,
		Detail:     map[string]any{"Previous": previous, "Active": id, "Pages": th.Pages()},
	})
	return nil
}

// ---- 主题配置（D95，三级兜底）----

// SettingValueView 是单个配置项的视图（API.md §10 的 `Values` 元素）。
type SettingValueView struct {
	Value    any    `json:"Value"`
	Default  any    `json:"Default"`
	Source   string `json:"Source"`
	HasValue bool   `json:"HasValue"`
}

// SettingSchemaItem 是配置项的表单描述。
//
// 同时提供 manifest 原生命名（Key / Name / Help）与插件 schema 风格别名
// （Alias / Message），让前端可以直接复用插件表单渲染器（DESIGN.md §7）。
// **身份用 `Key`**：Values 的键与写入时的键都是它。
type SettingSchemaItem struct {
	Key     string `json:"Key"`
	Name    string `json:"Name"`
	Alias   string `json:"Alias"`
	Message string `json:"Message"`
	Help    string `json:"Help"`

	Type     string `json:"Type"`
	Required bool   `json:"Required"`
	Default  any    `json:"Default"`
	Options  string `json:"Options"`

	ItemSchema []SettingSchemaItem `json:"ItemSchema,omitempty"`
}

// SettingsView 是 `GET /themes/{ThemeID}/settings` 的数据。
type SettingsView struct {
	ThemeID string                      `json:"ThemeID"`
	Name    string                      `json:"Name"`
	Schema  []SettingSchemaItem         `json:"Schema"`
	Values  map[string]SettingValueView `json:"Values"`
}

// SettingsView 返回某主题的配置 schema + 当前值。
func (s *Service) SettingsView(themeID, lang string) (*SettingsView, error) {
	th, err := s.store.Load(themeID)
	if err != nil {
		return nil, err
	}

	rows, err := s.cfg.list(th.ID)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]model.ThemeConfig, len(rows))
	for _, r := range rows {
		byKey[r.Key] = r
	}

	view := &SettingsView{
		ThemeID: th.ID,
		Name:    th.Name(lang),
		Schema:  make([]SettingSchemaItem, 0, len(th.ConfigItems())),
		Values:  make(map[string]SettingValueView, len(th.ConfigItems())),
	}

	for _, item := range th.ConfigItems() {
		def := defaultOf(item)
		view.Schema = append(view.Schema, schemaItemOf(item, lang))
		view.Values[item.Key] = s.valueViewOf(item, def, byKey[item.Key])
	}
	return view, nil
}

// valueViewOf 按三级兜底组装一个配置项的值（DB → manifest Default → 类型零值）。
func (s *Service) valueViewOf(item ConfigItem, def any, row model.ThemeConfig) SettingValueView {
	out := SettingValueView{Value: def, Default: def, Source: SettingsSourceDefault, HasValue: false}

	if row.Key == "" {
		return out
	}
	// DB 有行 → 用它；解码失败则退回默认值（避免一个坏值让整个设置页打不开）
	var v any
	if err := json.Unmarshal([]byte(row.Value), &v); err != nil {
		s.log.Warn("主题配置值解码失败，回退默认值", "theme", row.ThemeID, "key", row.Key, "err", err)
		return out
	}
	out.Value = v
	out.Source = SettingsSourceDB
	out.HasValue = true
	return out
}

// schemaItemOf 把 manifest 的 ConfigItem 转成表单描述。
func schemaItemOf(item ConfigItem, lang string) SettingSchemaItem {
	label := item.Name.Resolve(lang)
	help := item.Help.Resolve(lang)

	out := SettingSchemaItem{
		Key:      item.Key,
		Name:     label,
		Alias:    label,
		Message:  help,
		Help:     help,
		Type:     normalizeConfigType(item.Type),
		Required: item.Required,
		Default:  defaultOf(item),
		Options:  item.Options,
	}
	if len(item.ItemSchema) > 0 {
		out.ItemSchema = make([]SettingSchemaItem, 0, len(item.ItemSchema))
		for _, sub := range item.ItemSchema {
			out.ItemSchema = append(out.ItemSchema, schemaItemOf(sub, lang))
		}
	}
	return out
}

// normalizeConfigType 把 manifest 的类型名归一到小写（缺省 string）。
func normalizeConfigType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "" {
		return TypeString
	}
	return t
}

// defaultOf 返回配置项的默认值（manifest.Default 缺失时按类型给零值）。
func defaultOf(item ConfigItem) any {
	if len(item.Default) > 0 {
		var v any
		if err := json.Unmarshal(item.Default, &v); err == nil && v != nil {
			return v
		}
		// `null` 或解析失败 → 走零值
	}
	return zeroOf(item)
}

// zeroOf 返回配置项类型的零值（D98「配置项类型」表的默认值列）。
func zeroOf(item ConfigItem) any {
	switch normalizeConfigType(item.Type) {
	case TypeNumber:
		return float64(0)
	case TypeSwitch:
		return false
	case TypeSelect:
		if opts := optionsOf(item.Options); len(opts) > 0 {
			return opts[0]
		}
		return ""
	case TypeJSON:
		return []any{}
	default:
		return ""
	}
}

// optionsOf 把 select 的 Options 拆成选项列表。
func optionsOf(options string) []string {
	if strings.TrimSpace(options) == "" {
		return nil
	}
	parts := strings.Split(options, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// UpdateSettings 写入主题配置。
//
// 只接受**该主题声明过的键**（未声明 → ErrSettingUnknown，防脏写）；
// 类型按 manifest 的 Type 校验（不匹配 → ErrSettingType）。返回更新条数。
func (s *Service) UpdateSettings(ctx context.Context, themeID string, values map[string]any, by string) (int, error) {
	th, err := s.store.Load(themeID)
	if err != nil {
		return 0, err
	}

	declared := make(map[string]ConfigItem, len(th.ConfigItems()))
	for _, item := range th.ConfigItems() {
		declared[item.Key] = item
	}

	// 先全量校验，再统一落库：避免「一半写进去一半报错」
	type pending struct {
		item ConfigItem
		val  any
		json string
	}
	todo := make([]pending, 0, len(values))

	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		item, ok := declared[key]
		if !ok {
			return 0, fmt.Errorf("%w: %s", ErrSettingUnknown, key)
		}
		coerced, err := coerceValue(item, values[key])
		if err != nil {
			return 0, err
		}
		raw, err := json.Marshal(coerced)
		if err != nil {
			return 0, fmt.Errorf("%w: 配置项 %s 无法序列化: %v", ErrSettingType, key, err)
		}
		todo = append(todo, pending{item: item, val: coerced, json: string(raw)})
	}

	now := ensureNow()
	for _, p := range todo {
		row := newThemeConfigRow(th.ID, p.item.Key, p.json, normalizeConfigType(p.item.Type), by, now)
		if err := s.cfg.upsert(row); err != nil {
			return 0, err
		}
	}

	s.log.Info("主题配置已更新", "theme", th.ID, "count", len(todo), "by", by)
	s.auditor.Log(ctx, AuditEntry{
		Type:       model.LogTypeThemeSettingsUpdate,
		Status:     model.LogStatusSuccess,
		TargetType: "theme",
		TargetUID:  th.ID,
		// 不记录值内容（可能含敏感串）
		Detail: map[string]any{"Keys": keys, "Count": len(todo)},
	})
	return len(todo), nil
}

// coerceValue 按声明类型校验并归一化一个写入值。
func coerceValue(item ConfigItem, raw any) (any, error) {
	switch normalizeConfigType(item.Type) {
	case TypeString, TypeText:
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("%w: 配置项 %s 需要字符串", ErrSettingType, item.Key)
		}
		return s, nil

	case TypeNumber:
		f, ok := toFloat(raw)
		if !ok {
			return nil, fmt.Errorf("%w: 配置项 %s 需要数字", ErrSettingType, item.Key)
		}
		return f, nil

	case TypeSwitch:
		b, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: 配置项 %s 需要布尔值", ErrSettingType, item.Key)
		}
		return b, nil

	case TypeSelect:
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("%w: 配置项 %s 需要字符串选项", ErrSettingType, item.Key)
		}
		opts := optionsOf(item.Options)
		for _, o := range opts {
			if o == s {
				return s, nil
			}
		}
		return nil, fmt.Errorf("%w: 配置项 %s 的值 %q 不在选项 %v 中", ErrSettingType, item.Key, s, opts)

	case TypeJSON:
		if raw == nil {
			return []any{}, nil
		}
		return raw, nil

	default:
		return nil, fmt.Errorf("%w: 配置项 %s 的类型 %q 不受支持", ErrSettingType, item.Key, item.Type)
	}
}

// toFloat 把 JSON 解出来的数字归一到 float64。
func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

// ClearSettings 清理某主题的全部配置值（D95）。
//
// **不允许**对当前启用主题操作（避免把正在使用的主题配置清空）。
func (s *Service) ClearSettings(ctx context.Context, themeID, by string) (int64, error) {
	id := strings.TrimSpace(themeID)
	if id == "" {
		return 0, fmt.Errorf("%w: ThemeID 不能为空", ErrNotFound)
	}
	if id == s.Active() {
		return 0, fmt.Errorf("%w: %s", ErrSettingsInUse, id)
	}

	rows, err := s.cfg.list(id)
	if err != nil {
		return 0, err
	}
	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, r.Key)
	}

	deleted, err := s.cfg.deleteByTheme(id)
	if err != nil {
		return 0, err
	}

	s.log.Info("主题配置已清理", "theme", id, "deleted", deleted, "by", by)
	s.auditor.Log(ctx, AuditEntry{
		Type:       model.LogTypeThemeSettingsClear,
		Status:     model.LogStatusSuccess,
		TargetType: "theme",
		TargetUID:  id,
		Detail:     map[string]any{"Keys": keys, "DeletedCount": deleted},
	})
	return deleted, nil
}

// ---- 卸载 ----

// Uninstall 卸载主题目录。
//
// 两条不可卸载规则（D96）：
//   - 当前启用的主题（先切到别的主题）
//   - `default`（兜底锚点）
func (s *Service) Uninstall(ctx context.Context, themeID, by string) error {
	id := strings.TrimSpace(themeID)
	if id == "" {
		return fmt.Errorf("%w: ThemeID 不能为空", ErrNotFound)
	}

	// 安全检查：ID 必须落在 themesDir 之内（用 filepath.Rel，见 safeJoin）
	dir, err := safeJoin(s.store.ThemesDir(), id)
	if err != nil {
		return err
	}

	// `default` 是兜底锚点：它是**基于 ID** 的规则，与磁盘上是否真的存在该目录无关，
	// 因此必须在「目录是否存在」之前判定 —— 否则磁盘副本被删时会错报 404，
	// 让人以为「删掉就好」，而实际上它永远不该被卸载。
	if id == embeddedDefaultID {
		return fmt.Errorf("%w: default 是兜底锚点", ErrBuiltinTheme)
	}
	if !dirExists(dir) {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if id == s.Active() {
		return fmt.Errorf("%w: %s", ErrThemeActive, id)
	}

	// 读一下名字（可能是不合法的主题目录，读不到就只记 ID）
	name := id
	if mnf, err := readManifestAt(dir, s.limits().MaxManifestBytes); err == nil {
		name = mnf.Name.Resolve("zh-CN")
	}

	if err := os.RemoveAll(dir); err != nil {
		s.auditor.Log(ctx, AuditEntry{
			Type: model.LogTypeThemeUninstall, Status: model.LogStatusFailed,
			TargetType: "theme", TargetUID: id, Cause: err,
		})
		return fmt.Errorf("删除主题目录失败: %w", err)
	}

	s.log.Info("主题已卸载", "id", id, "by", by)
	s.auditor.Log(ctx, AuditEntry{
		Type:       model.LogTypeThemeUninstall,
		Status:     model.LogStatusSuccess,
		TargetType: "theme",
		TargetUID:  id,
		Detail:     map[string]any{"Name": name},
	})
	return nil
}

// ---- 重新扫描 ----

// Rescan 重新扫描主题目录并写一条审计（D96）。
func (s *Service) Rescan(ctx context.Context, lang, by string) *ListResult {
	res := s.List(lang)

	valid, invalid := 0, 0
	for _, item := range res.Items {
		if item.Valid {
			valid++
		} else {
			invalid++
		}
	}

	s.log.Info("主题目录已重新扫描", "found", len(res.Items), "valid", valid, "invalid", invalid, "by", by)
	s.auditor.Log(ctx, AuditEntry{
		Type:       model.LogTypeThemeRescan,
		Status:     model.LogStatusSuccess,
		TargetType: "theme",
		Detail:     map[string]any{"Found": len(res.Items), "Valid": valid, "Invalid": invalid},
	})
	return res
}

// ---- 预览图 ----

// Screenshot 读取主题预览图。
//
// 优先 manifest.Preview，其次约定的 screenshot.png。返回 (数据, Content-Type)。
func (s *Service) Screenshot(themeID string) ([]byte, string, error) {
	th, err := s.store.Load(themeID)
	if err != nil {
		return nil, "", err
	}

	candidates := make([]string, 0, 2)
	if p := strings.TrimSpace(th.Manifest.Preview); p != "" {
		candidates = append(candidates, p)
	}
	candidates = append(candidates, ScreenshotFile)

	for _, name := range candidates {
		// 预览图必须落在主题目录内（防 manifest 里写 ../../etc/passwd）
		rel, err := safeJoin(".", name)
		if err != nil {
			continue
		}
		data, err := th.ReadFile(rel)
		if err != nil {
			continue
		}
		return data, contentType(rel), nil
	}
	return nil, "", fmt.Errorf("%w: 主题 %s 没有预览图", ErrNotFound, th.ID)
}

// ---- 公开信息（供 /site/config）----

// PublicThemeInfo 是 `/site/config` 里的主题段（API.md §10）。
type PublicThemeInfo struct {
	ID        string         `json:"ID"`
	Name      string         `json:"Name"`
	Version   string         `json:"Version"`
	Pages     []string       `json:"Pages"`
	AssetBase string         `json:"AssetBase"`
	Settings  map[string]any `json:"Settings"`
}

// PublicTheme 返回**当前主题**的公开信息（含三级兜底后的最终设置值）。
//
// 绝不返回敏感信息：主题配置项类型里没有 password/secret
// （D98 只定义 string/text/number/switch/select/json），因此整份 Settings 可安全外传。
func (s *Service) PublicTheme(lang string) (*PublicThemeInfo, error) {
	th := s.Current()
	if th == nil {
		return nil, errors.New("theme: 当前主题不可用")
	}

	values, err := s.effectiveValues(th)
	if err != nil {
		return nil, err
	}

	pages := th.Pages()
	if pages == nil {
		pages = []string{}
	}
	return &PublicThemeInfo{
		ID:        th.ID,
		Name:      th.Name(lang),
		Version:   th.Manifest.Version,
		Pages:     pages,
		AssetBase: AssetBasePath,
		Settings:  values,
	}, nil
}

// effectiveValues 返回某主题全部配置项的**最终生效值**（三级兜底）。
func (s *Service) effectiveValues(th *Theme) (map[string]any, error) {
	rows, err := s.cfg.list(th.ID)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]model.ThemeConfig, len(rows))
	for _, r := range rows {
		byKey[r.Key] = r
	}

	out := make(map[string]any, len(th.ConfigItems()))
	for _, item := range th.ConfigItems() {
		view := s.valueViewOf(item, defaultOf(item), byKey[item.Key])
		out[item.Key] = view.Value
	}
	return out, nil
}

// limits 读取当前阈值（每次读，保证后台改了立即生效）。
func (s *Service) limits() Limits { return readLimits(s.settings) }

// readManifestAt 从磁盘目录解析 manifest（供卸载时取展示名，失败可忽略）。
func readManifestAt(dir string, maxBytes int64) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return nil, err
	}
	return ParseManifest(raw, maxBytes)
}
