package theme

import "context"

// 主题相关配置键（`SystemSettings`，`category=theme`）。
//
// 只有 `theme.active` 属于「站点级选择」，其余是**上传保护阈值**，
// 与任何主题的 schema 无关（D95）。键名保持 dot.lowerCamel（D81.3 第 3 条）。
const (
	// KeyActiveTheme 当前启用的主题 ID。
	KeyActiveTheme = "theme.active"

	// KeyMaxPackageBytes zip 上传包大小上限。
	KeyMaxPackageBytes = "theme.maxPackageBytes"
	// KeyMaxExtractBytes 解压后总体积上限（防 zip bomb）。
	KeyMaxExtractBytes = "theme.maxExtractBytes"
	// KeyMaxFileBytes 单个解压文件上限。
	KeyMaxFileBytes = "theme.maxFileBytes"
	// KeyMaxFiles 解压后文件数上限。
	KeyMaxFiles = "theme.maxFiles"
	// KeyMaxManifestBytes manifest.json 大小上限。
	KeyMaxManifestBytes = "theme.maxManifestBytes"
)

// 默认阈值（与 config/defaults.go 中登记的默认值保持一致）。
const (
	defaultMaxPackageBytes  int64 = 64 << 20  // 64 MiB
	defaultMaxExtractBytes  int64 = 512 << 20 // 512 MiB
	defaultMaxFileBytes     int64 = 128 << 20 // 128 MiB
	defaultMaxFiles               = 10000
	defaultMaxManifestBytes int64 = 1 << 20 // 1 MiB
)

// SettingsProvider 是主题系统需要的配置读写能力。
//
// 用接口而不是直接依赖 *settings.Service：便于单测注入内存实现，
// 也让 theme 包不依赖 settings 包的内部结构。
// `*settings.Service` 天然满足本接口。
type SettingsProvider interface {
	GetString(key string) string
	GetInt(key string, def int64) int64
	GetBool(key string, def bool) bool
	Set(key string, value any, by string) error
}

// Limits 是 zip 安装与清单解析用到的阈值。
type Limits struct {
	MaxPackageBytes  int64
	MaxExtractBytes  int64
	MaxFileBytes     int64
	MaxFiles         int
	MaxManifestBytes int64
}

// readLimits 从配置读取阈值（不硬编码，可在后台调整）。
func readLimits(sp SettingsProvider) Limits {
	l := Limits{
		MaxPackageBytes:  defaultMaxPackageBytes,
		MaxExtractBytes:  defaultMaxExtractBytes,
		MaxFileBytes:     defaultMaxFileBytes,
		MaxFiles:         defaultMaxFiles,
		MaxManifestBytes: defaultMaxManifestBytes,
	}
	if sp == nil {
		return l
	}
	l.MaxPackageBytes = sp.GetInt(KeyMaxPackageBytes, l.MaxPackageBytes)
	l.MaxExtractBytes = sp.GetInt(KeyMaxExtractBytes, l.MaxExtractBytes)
	l.MaxFileBytes = sp.GetInt(KeyMaxFileBytes, l.MaxFileBytes)
	l.MaxFiles = int(sp.GetInt(KeyMaxFiles, int64(l.MaxFiles)))
	l.MaxManifestBytes = sp.GetInt(KeyMaxManifestBytes, l.MaxManifestBytes)

	// 防御：配置被写成 0 或负数时回落到默认值，避免「0 = 拒绝一切」
	if l.MaxPackageBytes <= 0 {
		l.MaxPackageBytes = defaultMaxPackageBytes
	}
	if l.MaxExtractBytes <= 0 {
		l.MaxExtractBytes = defaultMaxExtractBytes
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = defaultMaxFileBytes
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = defaultMaxFiles
	}
	if l.MaxManifestBytes <= 0 {
		l.MaxManifestBytes = defaultMaxManifestBytes
	}
	return l
}

// contextBackground 是一个小包装，便于将来替换为带超时的 context。
func contextBackground() context.Context { return context.Background() }
