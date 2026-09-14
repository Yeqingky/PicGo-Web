// Package agent 是 picgo-agent（Node 侧车）的 Go 客户端。
//
// 职责边界：
//
//	Go 侧            本包                 picgo-agent（Node）
//	─────            ─────                ─────────────────
//	存储配置真相源 →  投影到 agent 的配置  → 持有唯一的 PicGo 实例
//	上传队列/重试  →  单文件同步调用       → 执行一次真实上传
//	任务与审计     →  只消费契约           → 只维护执行期状态
//
// 契约见 docs/API.md §13。要点：
//
//   - agent **仅监听 127.0.0.1**，所有请求带 `X-Agent-Token`
//   - 信封是 `{Code, Message, Data}`，**`Code` 为字符串**
//     （`OK` / `ERR_PARAM` / `ERR_PICGO` / `ERR_INTERNAL` / `ERR_NOT_FOUND`）
//   - 外层 JSON 字段用 PascalCase（D81）；但**从 picgo 读来的原生结构**
//     （`picBed` / `_configName` / 驱动字段 `repo`/`token`…）**原样透传**
//     —— 见 RawConfig / RawImgInfo 的注释
//
// 本包**不负责脱敏**：agent 返回的是 picgo 的真实（明文）存储，
// 哪些字段属敏感、如何掩码，是 Go 上层（storage service）的职责。
package agent

import (
	"time"
)

// 上游（agent）返回的字符串错误码。
const (
	CodeOK          = "OK"
	CodeErrParam    = "ERR_PARAM"
	CodeErrPicgo    = "ERR_PICGO"
	CodeErrInternal = "ERR_INTERNAL"
	CodeErrNotFound = "ERR_NOT_FOUND"
)

// 默认超时。
const (
	// DefaultRequestTimeout 普通请求（列表、配置、插件）的超时。
	DefaultRequestTimeout = 30 * time.Second
	// DefaultUploadTimeout 单文件上传的超时；对应设置 upload.itemTimeoutSeconds 的默认值。
	DefaultUploadTimeout = 300 * time.Second
)

// UploadTimeoutFunc 返回**当前**的单文件上传超时。
//
// 之所以用函数而不是固定值：`upload.itemTimeoutSeconds` 是运行时可改的设置，
// 每次上传前现取才能让改动即时生效。为 nil 时退回 Config.UploadTimeout。
type UploadTimeoutFunc func() time.Duration

// Config 是客户端构造参数。
type Config struct {
	// BaseURL agent 地址，如 http://127.0.0.1:36678（不带尾斜杠）。
	BaseURL string
	// Token 与 agent 共享的令牌（注入到 X-Agent-Token）。
	Token string
	// Log 进程日志。
	Log Logger

	// RequestTimeout 普通请求超时；<= 0 时用 DefaultRequestTimeout。
	RequestTimeout time.Duration
	// UploadTimeout 上传超时兜底值；<= 0 时用 DefaultUploadTimeout。
	UploadTimeout time.Duration
	// UploadTimeoutFn 动态上传超时；优先于 UploadTimeout。
	UploadTimeoutFn UploadTimeoutFunc
}

// Logger 是本包对日志的最小依赖（避免强绑 slog，便于测试）。
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}
