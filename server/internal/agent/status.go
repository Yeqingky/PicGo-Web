package agent

import (
	"context"
	"sync"
	"time"
)

// Status 是 agent 的**缓存健康状态**快照。
type Status struct {
	// Up 是否可用。
	Up bool `json:"Up"`
	// Version picgo-core 版本（Up 为真时有效）。
	Version string `json:"Version"`
	// ConfigPath picgo 的 config.json 路径。
	ConfigPath string `json:"ConfigPath"`
	// PluginCount 已加载的插件数。
	PluginCount int `json:"PluginCount"`
	// PID agent 进程号。
	PID int `json:"PID"`
	// Error 最近一次失败原因（Up 为假时有效）。
	Error string `json:"Error"`
	// CheckedAt 最近一次探测的 Unix 秒。
	CheckedAt int64 `json:"CheckedAt"`
}

// StatusHolder 缓存 agent 健康状态。
//
// 为什么需要缓存：`/healthz` 必须**轻量**（容器 healthcheck 与前端都会高频轮询），
// 不能每次都去调 agent。因此由后台探测协程周期性刷新，handler 只读内存快照。
//
// 零值可用（初始为「不可用」）；并发安全。
type StatusHolder struct {
	mu     sync.RWMutex
	status Status
}

// NewStatusHolder 构造一个初始为「不可用」的持有者。
func NewStatusHolder() *StatusHolder {
	return &StatusHolder{}
}

// Get 读取当前快照（无 I/O，可在请求路径上安全调用）。
func (h *StatusHolder) Get() Status {
	if h == nil {
		return Status{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.status
}

// SetUp 标记为可用，并记录探测到的元信息。
func (h *StatusHolder) SetUp(info *HealthzData) {
	if h == nil {
		return
	}
	s := Status{Up: true, CheckedAt: time.Now().Unix()}
	if info != nil {
		s.Version = info.PicgoVersion
		s.ConfigPath = info.ConfigPath
		s.PluginCount = info.PluginsLoaded
		s.PID = info.PID
	}
	h.mu.Lock()
	h.status = s
	h.mu.Unlock()
}

// SetDown 标记为不可用，并记录原因。
func (h *StatusHolder) SetDown(err error) {
	if h == nil {
		return
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	h.mu.Lock()
	h.status = Status{Up: false, Error: msg, CheckedAt: time.Now().Unix()}
	h.mu.Unlock()
}

// Refresh 主动探测一次并更新缓存。
//
// 由后台协程（而非请求路径）调用，避免把网络 I/O 带进热路径。
func (h *StatusHolder) Refresh(ctx context.Context, client Client) Status {
	if h == nil {
		return Status{}
	}
	if client == nil {
		h.SetDown(nil)
		return h.Get()
	}
	info, err := client.Healthz(ctx)
	if err != nil {
		h.SetDown(err)
		return h.Get()
	}
	h.SetUp(info)
	return h.Get()
}

// Label 返回状态标签，供 `/healthz` 使用：
// `up`（可用）/ `down`（不可用）。
func (s Status) Label() string {
	if s.Up {
		return "up"
	}
	return "down"
}
