// Package settings 实现「运行时配置」的读写（D18 第 2 层）。
//
// 三级兜底（读取顺序）：
//
//	数据库值（SystemSettings）→ 代码默认值（config.Keys）→ 类型零值
//
// 写入后触发 onChanged 回调，供其它模块刷新内存状态
// （例如写入 picgo.* 时推送到 agent、写入 upload.rateLimit.* 时刷新限流器）。
//
// 敏感值（config.TypeSecret）以 AES-256-GCM 加密后入库，
// 对外读接口一律返回掩码（crypto.Mask）。
package settings

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/crypto"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
)

// Source 表示一个配置值的来源，供后台显示「来自数据库 / 使用默认值」徽章。
type Source string

const (
	SourceDB      Source = "db"
	SourceDefault Source = "default"
)

// Item 是一个配置项的完整视图（元数据 + 值 + 来源）。
type Item struct {
	Key         string      `json:"Key"`
	Category    string      `json:"Category"`
	Type        string      `json:"Type"`
	Value       any         `json:"Value"`
	Default     any         `json:"Default"`
	Source      Source      `json:"Source"`
	Secret      bool        `json:"Secret"`
	HasValue    bool        `json:"HasValue"` // secret 类型：是否已设置
	Description string      `json:"Description"`
}

// ChangedEvent 描述一次配置变更。
type ChangedEvent struct {
	Key      string
	OldValue any
	NewValue any
	Source   Source
	By       string // 操作者 UserUID
}

// OnChangedFunc 是变更回调。实现必须是**非阻塞**的（同步调用，慢回调会拖慢写请求）。
type OnChangedFunc func(ev ChangedEvent)

// Service 是配置服务。并发安全。
type Service struct {
	repo   *repository.SettingRepo
	cipher *crypto.Cipher
	log    *slog.Logger

	mu        sync.RWMutex
	cache     map[string]any // key -> 已解码的值（不含 secret 明文，secret 单独存）
	secrets   map[string]string
	callbacks []OnChangedFunc
}

// New 构造配置服务并加载一次全量缓存。
func New(repo *repository.SettingRepo, cipher *crypto.Cipher, log *slog.Logger) (*Service, error) {
	s := &Service{
		repo:    repo,
		cipher:  cipher,
		log:     log,
		cache:   make(map[string]any),
		secrets: make(map[string]string),
	}
	if err := s.reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// OnChanged 注册变更回调（可在启动装配阶段多次调用）。
func (s *Service) OnChanged(fn OnChangedFunc) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callbacks = append(s.callbacks, fn)
}

// reload 重新从数据库加载全部配置到缓存。
func (s *Service) reload() error {
	rows, err := s.repo.ListSystem("")
	if err != nil {
		return fmt.Errorf("加载站点配置失败: %w", err)
	}

	cache := make(map[string]any, len(rows))
	secrets := make(map[string]string)

	for _, row := range rows {
		_, known := config.Lookup(row.Key)

		// secret 类型：解密后放 secrets 缓存，cache 里只放掩码
		if row.Encrypted {
			if s.cipher == nil {
				s.log.Warn("配置项已加密但未配置主密钥，跳过", "key", row.Key)
				continue
			}
			plain, err := s.cipher.DecryptString(row.Value)
			if err != nil {
				// 解密失败不阻断启动：记日志并保留「已设置但不可读」状态
				s.log.Error("配置项解密失败，将使用默认值", "key", row.Key, "err", err)
				continue
			}
			secrets[row.Key] = plain
			cache[row.Key] = crypto.Mask
			continue
		}

		v, err := decodeValue(row.Value, row.ValueType)
		if err != nil {
			s.log.Warn("配置值解析失败，将使用默认值", "key", row.Key, "raw", row.Value, "err", err)
			continue
		}
		if !known {
			// 未注册的键（例如手工写入或旧版本遗留）：仍然生效，但记为 unknown
			s.log.Warn("发现未注册的配置键（仍会生效）", "key", row.Key)
		}
		cache[row.Key] = v
	}

	s.mu.Lock()
	s.cache = cache
	s.secrets = secrets
	s.mu.Unlock()
	return nil
}

// Reload 供外部在批量改动后手动刷新缓存。
func (s *Service) Reload() error { return s.reload() }

// ---- 读取：三级兜底 ----

// Get 返回配置项的值（不含 secret 明文；secret 返回掩码）。
//
// 顺序：DB → 代码默认值 → 零值。
func (s *Service) Get(key string) any {
	if v, ok := s.lookupCache(key); ok {
		return v
	}
	if d, ok := config.DefaultOf(key); ok {
		return d
	}
	return nil
}

// GetRaw 返回**真实值**：secret 类型返回解密后的明文。
//
// ⚠️ 仅供内部使用（发送邮件、调 OAuth、同步给 agent），
// **绝不可**直接放进 API 响应。
func (s *Service) GetRaw(key string) string {
	s.mu.RLock()
	if v, ok := s.secrets[key]; ok {
		s.mu.RUnlock()
		return v
	}
	s.mu.RUnlock()

	// 非 secret 或未设置：按普通值取并转字符串
	if v, ok := s.lookupCache(key); ok {
		return toString(v)
	}
	if d, ok := config.DefaultOf(key); ok {
		return toString(d)
	}
	return ""
}

// HasValue 判断 secret 类型是否已设置（非 secret 恒为 true）。
func (s *Service) HasValue(key string) bool {
	meta, known := config.Lookup(key)
	if !known || meta.Type != config.TypeSecret {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.secrets[key] != ""
}

// GetString 取字符串（三级兜底）。
func (s *Service) GetString(key string) string {
	return toString(s.Get(key))
}

// GetStringDefault 取字符串，缺省用 def。
func (s *Service) GetStringDefault(key, def string) string {
	if v := s.GetString(key); v != "" {
		return v
	}
	return def
}

// GetInt 取整数（三级兜底，解析失败用 def）。
func (s *Service) GetInt(key string, def int64) int64 {
	switch v := s.Get(key).(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n
		}
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n
		}
	}
	return def
}

// GetBool 取布尔（三级兜底，解析失败用 def）。
func (s *Service) GetBool(key string, def bool) bool {
	switch v := s.Get(key).(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on", "y":
			return true
		case "0", "false", "no", "off", "n":
			return false
		}
	case float64:
		return v != 0
	case int64:
		return v != 0
	}
	return def
}

// GetStringSlice 取字符串数组（三级兜底）。
func (s *Service) GetStringSlice(key string, def []string) []string {
	v := s.Get(key)
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			out = append(out, toString(item))
		}
		return out
	case string:
		if strings.TrimSpace(t) == "" {
			return def
		}
		// 逗号分隔的兜底解析
		parts := strings.Split(t, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return def
}

// GetJSON 把配置项反序列化到 out（三级兜底）。
func (s *Service) GetJSON(key string, out any) error {
	v := s.Get(key)
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("配置项 %s 无法序列化: %w", key, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("配置项 %s 无法反序列化: %w", key, err)
	}
	return nil
}

// ---- 写入 ----

// Set 写入一个配置项。by 为操作者 UserUID（系统写入传空）。
//
// 未注册的键会被拒绝（防脏写）：必须在 config.Keys 里先声明。
func (s *Service) Set(key string, value any, by string) error {
	meta, known := config.Lookup(key)
	if !known {
		return fmt.Errorf("未注册的配置键 %q：请先在 config.Keys 中声明", key)
	}

	raw, err := encodeValue(value, meta.Type)
	if err != nil {
		return fmt.Errorf("配置项 %s 编码失败: %w", key, err)
	}

	row := &model.SystemSetting{
		Key:       key,
		ValueType: string(meta.Type),
		Category:  string(meta.Category),
		UpdatedBy: by,
		CreatedAt: model.Now(),
		UpdatedAt: model.Now(),
	}

	oldValue := s.Get(key)

	if meta.Type == config.TypeSecret {
		if s.cipher == nil {
			return fmt.Errorf("写入 %s 失败：未配置加密主密钥", key)
		}
		// 掩码表示「不修改」
		if crypto.IsMasked(toString(value)) {
			return nil
		}
		enc, err := s.cipher.EncryptString(raw)
		if err != nil {
			return fmt.Errorf("配置项 %s 加密失败: %w", key, err)
		}
		row.Encrypted = true
		row.Value = enc
	} else {
		row.Value = raw
	}

	if err := s.repo.UpsertSystem(row); err != nil {
		return fmt.Errorf("保存配置项 %s 失败: %w", key, err)
	}

	// 更新缓存
	s.mu.Lock()
	if meta.Type == config.TypeSecret {
		if !crypto.IsMasked(toString(value)) {
			s.secrets[key] = toString(value)
		}
		s.cache[key] = crypto.Mask
	} else {
		s.cache[key] = value
	}
	s.mu.Unlock()

	s.emit(ChangedEvent{
		Key:      key,
		OldValue: oldValue,
		NewValue: value,
		Source:   SourceDB,
		By:       by,
	})
	return nil
}

// Reset 删除数据库中的值，回落到代码默认值。
func (s *Service) Reset(key, by string) error {
	meta, known := config.Lookup(key)
	if !known {
		return fmt.Errorf("未注册的配置键 %q", key)
	}
	oldValue := s.Get(key)
	if err := s.repo.DeleteSystem(key); err != nil {
		return fmt.Errorf("重置配置项 %s 失败: %w", key, err)
	}

	s.mu.Lock()
	delete(s.cache, key)
	delete(s.secrets, key)
	s.mu.Unlock()

	s.emit(ChangedEvent{
		Key:      key,
		OldValue: oldValue,
		NewValue: meta.Default,
		Source:   SourceDefault,
		By:       by,
	})
	return nil
}

// SetMany 批量写入（任一失败即返回错误，但**不回滚已成功的项**——
// 配置项之间无事务依赖，逐项幂等）。
func (s *Service) SetMany(values map[string]any, by string) error {
	var firstErr error
	for k, v := range values {
		if err := s.Set(k, v, by); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ---- 后台展示 ----

// All 返回全部站点配置项的视图（含元数据、值、来源），供 GET /settings/system。
//
// secret 类型的 Value 一律是掩码，另有 HasValue 表示是否已设置。
func (s *Service) All() []Item {
	keys := config.Keys
	out := make([]Item, 0, len(keys))
	for _, k := range keys {
		out = append(out, s.itemOf(k))
	}
	return out
}

// ByCategory 返回某分类下的配置项视图。
func (s *Service) ByCategory(c config.Category) []Item {
	keys := config.KeysOf(c)
	out := make([]Item, 0, len(keys))
	for _, k := range keys {
		out = append(out, s.itemOf(k))
	}
	return out
}

// Categories 返回全部分类。
func (s *Service) Categories() []config.Category { return config.AllCategories() }

// Meta 返回某个键的元数据（供 handler 校验）。
func (s *Service) Meta(key string) (config.Key, bool) { return config.Lookup(key) }

func (s *Service) itemOf(k config.Key) Item {
	item := Item{
		Key:         k.Name,
		Category:    string(k.Category),
		Type:        string(k.Type),
		Default:     k.Default,
		Description: k.Description,
		Source:      SourceDefault,
		Secret:      k.Type == config.TypeSecret,
	}

	if _, ok := s.lookupCache(k.Name); ok {
		item.Source = SourceDB
	}

	if k.Type == config.TypeSecret {
		item.Value = crypto.Mask
		item.HasValue = s.HasValue(k.Name)
		if item.Source == SourceDB && !item.HasValue {
			// 有行但解不出来（主密钥变更等）：仍然算「已设置」以免前端误清
			item.HasValue = true
		}
		return item
	}

	item.Value = s.Get(k.Name)
	if k.Type == config.TypeString || k.Type == config.TypeInt || k.Type == config.TypeBool || k.Type == config.TypeJSON {
		// 值来自 DB 时 Source 已置为 db
	}
	return item
}

// ---- 内部 ----

func (s *Service) lookupCache(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.cache[key]
	return v, ok
}

func (s *Service) emit(ev ChangedEvent) {
	s.mu.RLock()
	cbs := make([]OnChangedFunc, len(s.callbacks))
	copy(cbs, s.callbacks)
	s.mu.RUnlock()

	for _, fn := range cbs {
		fn(ev)
	}
}

// encodeValue 按类型把值编码成入库字符串（JSON 编码，便于统一还原类型）。
func encodeValue(v any, t config.ValueType) (string, error) {
	switch t {
	case config.TypeString, config.TypeSecret:
		return toString(v), nil
	case config.TypeInt:
		return strconv.FormatInt(toInt64(v), 10), nil
	case config.TypeBool:
		if toBool(v) {
			return "true", nil
		}
		return "false", nil
	case config.TypeJSON:
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
}

// decodeValue 把入库字符串还原成对应 Go 类型。
func decodeValue(raw, valueType string) (any, error) {
	switch config.ValueType(valueType) {
	case config.TypeString, config.TypeSecret:
		return raw, nil
	case config.TypeInt:
		return strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	case config.TypeBool:
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "1", "true", "yes", "on", "y":
			return true, nil
		default:
			return false, nil
		}
	case config.TypeJSON:
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		return v, nil
	default:
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err == nil {
			return v, nil
		}
		return raw, nil
	}
}

func toString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n
	case bool:
		if t {
			return 1
		}
	}
	return 0
}

func toBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "1", "true", "yes", "on", "y":
			return true
		}
	case int64:
		return t != 0
	case float64:
		return t != 0
	}
	return false
}
