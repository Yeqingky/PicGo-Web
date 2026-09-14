package theme

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// Theme 是一个**已装载**的主题：要么来自磁盘目录，要么是内嵌兜底副本。
type Theme struct {
	// ID 主题标识（= manifest.ID = 目录名）。
	ID string
	// Manifest 解析后的元数据。
	Manifest *Manifest
	// Dir 磁盘目录；内嵌兜底主题为空串。
	Dir string
	// Builtin 是否**内置主题**（ID == "default"）。
	//
	// 内置主题不可卸载（它是兜底锚点），因此 `CanUninstall` 恒为 false。
	Builtin bool
	// Embedded 是否来自**内嵌 FS**（Dir 为空）。
	//
	// 内嵌副本用于 seed 与「永不白屏」兜底；磁盘上的 default 可能是
	// `make theme` 产出的完整版。
	Embedded bool
	// Fallback 是否为「磁盘主题不可用 → 回退内嵌兜底」。
	Fallback bool
	// FallbackReason Fallback 为真时的原因（写入 theme.error 的 Detail）。
	FallbackReason string

	// fsys 是主题根目录的文件系统（磁盘用 os.DirFS，内嵌用 embed 子 FS）。
	fsys fs.FS
}

// Pages 返回该主题接管的路由前缀（已归一化，缺省 `["/"]`）。
func (t *Theme) Pages() []string { return t.Manifest.EffectivePages() }

// Name 按语言解析主题名。
func (t *Theme) Name(lang string) string { return t.Manifest.Name.Resolve(lang) }

// Description 按语言解析主题描述。
func (t *Theme) Description(lang string) string { return t.Manifest.Description.Resolve(lang) }

// Author 按语言解析作者。
func (t *Theme) Author(lang string) string { return t.Manifest.Author.Resolve(lang) }

// ConfigItems 返回该主题声明的配置项。
func (t *Theme) ConfigItems() []ConfigItem { return t.Manifest.Configuration.Items }

// ReadFile 读取主题根目录下的一个文件。
func (t *Theme) ReadFile(name string) ([]byte, error) {
	return fs.ReadFile(t.fsys, filepath.ToSlash(name))
}

// HasFile 判断主题根目录下是否存在某个普通文件。
func (t *Theme) HasFile(name string) bool {
	info, err := fs.Stat(t.fsys, filepath.ToSlash(name))
	return err == nil && !info.IsDir()
}

// AssetFS 返回以主题 `assets/` 为根的文件系统。
//
// 主题没有 assets/ 目录时返回 nil（调用方按 404 处理）。
func (t *Theme) AssetFS() fs.FS {
	sub, err := fs.Sub(t.fsys, AssetsDir)
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "."); err != nil {
		return nil
	}
	return sub
}

// InvalidTheme 描述一个扫描到但**不合法**的主题目录。
type InvalidTheme struct {
	Dir   string
	ID    string
	Error string
}

// ScanResult 是一次扫描的结果。
type ScanResult struct {
	Themes    []*Theme
	Invalid   []InvalidTheme
	ScannedAt int64
}

// Store 负责主题目录的 seed、扫描与装载。
//
// 它是**无状态**的（目录内容即真相源），因此每次请求都能拿到最新状态。
type Store struct {
	themesDir        string
	seedFrom         string
	maxManifestBytes int64
	log              *slog.Logger
	auditor          Auditor
}

// StoreOptions 构造 Store 的依赖。
type StoreOptions struct {
	// ThemesDir 主题根目录（<dataDir>/themes）。
	ThemesDir string
	// SeedFrom 可选的 seed 源目录（`PICGO_WEB_THEME_SEED`）。
	//
	// 为空时使用随二进制内嵌的默认主题；非空时从该目录复制
	// （让部署方把 `make theme` 产出的完整版默认主题种进去）。
	SeedFrom string
	// MaxManifestBytes manifest.json 大小上限（theme.maxManifestBytes）。
	MaxManifestBytes int64
	Log              *slog.Logger
	Auditor          Auditor
}

// NewStore 构造 Store。
func NewStore(opts StoreOptions) *Store {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	maxBytes := opts.MaxManifestBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxManifestBytes
	}
	return &Store{
		themesDir:        opts.ThemesDir,
		seedFrom:         strings.TrimSpace(opts.SeedFrom),
		maxManifestBytes: maxBytes,
		log:              log,
		auditor:          ensureNoop(opts.Auditor),
	}
}

// ThemesDir 返回主题根目录。
func (s *Store) ThemesDir() string { return s.themesDir }

// DirOf 返回某主题在磁盘上的目录。
func (s *Store) DirOf(id string) string { return filepath.Join(s.themesDir, id) }

// Seed 在主题目录为空时写出默认主题（D94.4 / OPERATIONS §8.3）。
//
// seed 源优先用 SeedFrom（`PICGO_WEB_THEME_SEED`），否则用内嵌副本。
func (s *Store) Seed() (bool, error) {
	seeded, err := seedIfEmpty(s.themesDir, s.seedFrom)
	if err != nil {
		return false, err
	}
	if seeded {
		source := "内嵌默认主题"
		if s.seedFrom != "" {
			source = s.seedFrom
		}
		s.log.Info("已完成默认主题的种子写入", "dir", s.DirOf(embeddedDefaultID), "source", source)
	}
	return seeded, nil
}

// Scan 扫描主题目录。
//
// 不合法的主题**不进列表**，并写 `theme.error`（OPERATIONS §8.4）。
// 内嵌默认主题**总是**出现在结果里（Builtin=true），这样后台始终能看到兜底锚点。
func (s *Store) Scan() *ScanResult {
	res := &ScanResult{ScannedAt: ensureNow()}

	entries, err := os.ReadDir(s.themesDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.log.Warn("读取主题目录失败", "dir", s.themesDir, "err", err)
		}
	} else {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if !e.IsDir() || isTempName(e.Name()) {
				continue
			}
			names = append(names, e.Name())
		}
		sort.Strings(names)

		for _, name := range names {
			dir := s.DirOf(name)
			// 内嵌默认主题的磁盘副本仍会出现在列表里（IsBuiltin 由内嵌来源决定），
			// 但这里按普通磁盘主题装载：磁盘版本可能已被 make theme 覆盖为完整版。
			th, err := s.loadFromDir(dir, name)
			if err != nil {
				res.Invalid = append(res.Invalid, InvalidTheme{Dir: dir, ID: name, Error: err.Error()})
				s.recordThemeError(name, err, dir)
				s.log.Warn("主题不合法，已跳过", "dir", dir, "err", err)
				continue
			}
			res.Themes = append(res.Themes, th)
		}
	}

	// 确保内嵌默认主题在列表中（磁盘缺失时它就是兜底锚点）
	hasDefault := false
	for _, th := range res.Themes {
		if th.ID == embeddedDefaultID {
			hasDefault = true
			break
		}
	}
	if !hasDefault {
		if embedded, err := s.Embedded(); err == nil {
			res.Themes = append(res.Themes, embedded)
		} else {
			// 编译期就把内嵌主题打进去了，走到这里说明构建产物异常
			s.log.Error("内嵌默认主题不可用（构建异常）", "err", err)
		}
	}

	sort.Slice(res.Themes, func(i, j int) bool { return res.Themes[i].ID < res.Themes[j].ID })
	return res
}

// Embedded 返回内嵌兜底主题（永远可用，是「永不白屏」的锚点）。
func (s *Store) Embedded() (*Theme, error) {
	sub, err := EmbeddedFS()
	if err != nil {
		return nil, err
	}

	raw, err := embeddedFile(ManifestFile)
	if err != nil {
		return nil, fmt.Errorf("读取内嵌默认主题清单失败: %w", err)
	}
	manifest, err := ParseManifest(raw, s.maxManifestBytes)
	if err != nil {
		return nil, err
	}

	th := &Theme{
		ID:       manifest.ID,
		Manifest: manifest,
		Builtin:  manifest.ID == embeddedDefaultID,
		Embedded: true,
		fsys:     sub,
	}
	// 内嵌主题是编译期产物，校验失败属于构建事故（用 isEmbedded 标记避免再次走兜底）
	if err := manifest.Validate(th.ID, th.HasFile(IndexFile)); err != nil {
		return nil, fmt.Errorf("内嵌默认主题校验失败（构建异常）: %w", err)
	}
	return th, nil
}

// Load 按 ID 装载主题。
//
// 查找顺序：磁盘目录 → （ID 为 default 时）内嵌兜底。
func (s *Store) Load(id string) (*Theme, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		id = embeddedDefaultID
	}
	if !manifestIDPattern.MatchString(id) {
		return nil, fmt.Errorf("%w: 主题 ID %q 格式非法", ErrNotFound, id)
	}

	if dir := s.DirOf(id); dirExists(dir) {
		th, err := s.loadFromDir(dir, id)
		if err == nil {
			return th, nil
		}
		// 磁盘主题损坏：ID 为 default 时回退内嵌兜底，否则按不存在处理
		if id != embeddedDefaultID {
			return nil, err
		}
		s.log.Warn("磁盘上的 default 主题不可用，回退内嵌副本", "err", err)

		embedded, embErr := s.Embedded()
		if embErr != nil {
			return nil, err
		}
		embedded.Fallback = true
		embedded.FallbackReason = err.Error()
		return embedded, nil
	}

	if id == embeddedDefaultID {
		return s.Embedded()
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Resolve 解析「当前生效的主题」，**带兜底**（OPERATIONS §8.7）。
//
// 这是分发路径上唯一的入口：任何异常都不应该让首页白屏。
func (s *Store) Resolve(activeID string) *Theme {
	th, err := s.Load(activeID)
	if err == nil {
		return th
	}

	reason := err.Error()
	s.log.Warn("当前主题不可用，回退内嵌默认主题", "active", activeID, "err", err)

	embedded, embErr := s.Embedded()
	if embErr != nil {
		// 理论上不可达（内嵌主题来自编译期）；返回一个最小可用的内存主题以免 500
		s.log.Error("内嵌默认主题不可用，使用最小兜底主题", "err", embErr)
		return minimalFallbackTheme(reason)
	}
	embedded.Fallback = true
	embedded.FallbackReason = reason
	return embedded
}

// loadFromDir 从磁盘目录装载并校验一个主题。
func (s *Store) loadFromDir(dir, dirName string) (*Theme, error) {
	manifestPath := filepath.Join(dir, ManifestFile)
	info, err := os.Stat(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrManifestMissing, manifestPath)
		}
		return nil, fmt.Errorf("读取 %s 失败: %w", manifestPath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%w: %s 是目录", ErrManifestMissing, manifestPath)
	}
	if s.maxManifestBytes > 0 && info.Size() > s.maxManifestBytes {
		return nil, fmt.Errorf("%w: %d > %d 字节", ErrManifestTooLarge, info.Size(), s.maxManifestBytes)
	}

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", manifestPath, err)
	}

	manifest, err := ParseManifest(raw, s.maxManifestBytes)
	if err != nil {
		return nil, err
	}

	th := &Theme{
		ID:       manifest.ID,
		Manifest: manifest,
		Dir:      dir,
		Builtin:  manifest.ID == embeddedDefaultID,
		fsys:     os.DirFS(dir),
	}
	if err := manifest.Validate(dirName, th.HasFile(IndexFile)); err != nil {
		return nil, err
	}
	return th, nil
}

// recordThemeError 写一条 theme.error 操作日志。
//
// 用于「主题目录不合法」这类扫描期发现的问题（OPERATIONS §5.3）。
func (s *Store) recordThemeError(id string, cause error, dir string) {
	s.auditor.Log(contextBackground(), AuditEntry{
		Type:       model.LogTypeThemeError,
		Status:     model.LogStatusFailed,
		TargetType: "theme",
		TargetUID:  id,
		Detail:     map[string]any{"Reason": cause.Error(), "Path": dir, "Fallback": false},
		Cause:      cause,
	})
}

// dirExists 判断目录是否存在且为目录。
func dirExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

// minimalFallbackTheme 是「连内嵌主题都不可用」时的最后兜底。
//
// 编译期就把内嵌主题打进去了，正常永远走不到这里；留着是为了「任何情况下都不 500」。
func minimalFallbackTheme(reason string) *Theme {
	return &Theme{
		ID: "fallback",
		Manifest: &Manifest{
			ID:          "fallback",
			Name:        LocalizedText{flat: "PicGo-Web"},
			Version:     Version,
			Pages:       []string{"/"},
			Description: LocalizedText{flat: "主题不可用"},
		},
		Builtin:        true,
		Embedded:       true,
		Fallback:       true,
		FallbackReason: reason,
		fsys:           fs.FS(fallbackFS{reason: reason}),
	}
}

// fallbackFS 为最小兜底主题提供一个「内存中的」单文件 FS。
type fallbackFS struct{ reason string }

func (f fallbackFS) Open(name string) (fs.File, error) {
	if filepath.ToSlash(name) != IndexFile {
		return nil, fs.ErrNotExist
	}
	html := "<!DOCTYPE html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">" +
		"<title>PicGo-Web</title></head><body style=\"font-family:sans-serif;padding:40px\">" +
		"<h1>主题不可用</h1><p>" + htmlEscape(f.reason) + "</p>" +
		"<p>请进入 <a href=\"/admin/themes\">后台 · 主题</a> 切换或重新扫描。</p>" +
		"</body></html>"
	return &memFile{name: IndexFile, data: []byte(html)}, nil
}

// memFile 是 fallbackFS 用的内存文件。
type memFile struct {
	name   string
	data   []byte
	offset int
}

func (m *memFile) Stat() (fs.FileInfo, error) {
	return memFileInfo{name: m.name, size: int64(len(m.data))}, nil
}

func (m *memFile) Read(p []byte) (int, error) {
	if m.offset >= len(m.data) {
		return 0, fs.ErrClosed
	}
	n := copy(p, m.data[m.offset:])
	m.offset += n
	return n, nil
}

func (m *memFile) Close() error { return nil }

// memFileInfo 是 memFile 的 FileInfo。
type memFileInfo struct {
	name string
	size int64
}

func (i memFileInfo) Name() string       { return i.name }
func (i memFileInfo) Size() int64        { return i.size }
func (i memFileInfo) Mode() fs.FileMode  { return 0o644 }
func (i memFileInfo) ModTime() time.Time { return time.Unix(0, 0) }
func (i memFileInfo) IsDir() bool        { return false }
func (i memFileInfo) Sys() any           { return nil }

// warnOnce 是「同一原因只记一次日志」的守卫。
//
// 分发路径在每个页面请求上都会调用 Resolve，如果每次都写 theme.error，
// 一旦主题坏掉就会把操作日志刷爆。因此按 (key) 去重。
type warnOnce struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func newWarnOnce() *warnOnce { return &warnOnce{seen: map[string]struct{}{}} }

// shouldFire 第一次遇到 key 时返回 true。
func (w *warnOnce) shouldFire(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.seen[key]; ok {
		return false
	}
	w.seen[key] = struct{}{}
	return true
}
