// Package events 是进程内的 SSE 事件总线。
//
// 位置：`picgo-agent` 的 SSE ──(Bridge)──▶ **Hub** ──▶ 各 HTTP SSE 订阅者（浏览器）
//
// 设计要点：
//
//   - **事件名是小写点分字符串**（`upload.progress` / `job.finished` / `ping` …），
//     原样保留：它是协议层标识符，不是 JSON 字段，与 D81（PascalCase）无关。
//   - **事件体内部字段用 PascalCase**（D81）。
//   - **慢消费者丢弃而不是阻塞**（D78 的取舍）：一个卡住的浏览器不能拖垮上传流程。
//   - Hub 只做**广播**；「谁能看到哪些事件」的权限过滤在 handler 侧完成
//     （按 job 的 UserUID 过滤，见 docs/API.md §8.1「投递范围」）。
package events

import "sync"

// 事件名（**小写点分，原样**，见 docs/API.md §8.1）。
const (
	// EventJobStarted job 从 queued 转 running。
	EventJobStarted = "job.started"
	// EventUploadProgress 单文件进度变化（Progress 0..100）。
	EventUploadProgress = "upload.progress"
	// EventUploadFinished 单文件成功。
	EventUploadFinished = "upload.finished"
	// EventUploadFailed 单文件最终失败（已耗尽重试）。
	EventUploadFailed = "upload.failed"
	// EventJobLog 任务逐行日志（npm 输出、上传过程）。
	EventJobLog = "job.log"
	// EventJobFinished job 结束（succeeded 或 failed）。
	EventJobFinished = "job.finished"
	// EventSystemNotice 服务端提示（优雅重启、agent 重启等）。
	EventSystemNotice = "system.notice"
	// EventPing 保活（每 25 秒）。
	EventPing = "ping"
)

// PingInterval 是 SSE 保活间隔（docs/API.md §8.1）。
const PingIntervalSeconds = 25

// Event 是一条待广播的事件。
type Event struct {
	// Name 事件名（原样的小写点分字符串）。
	Name string
	// Data 事件体（字段用 PascalCase）。
	Data any
	// UserUID 该事件的归属用户；空表示「所有人可见」。
	//
	// Hub **不做**过滤，只把它带下去：订阅端（handler）据此决定是否推给该客户端。
	// 之所以放在事件上而不是 Hub 里：同一条 Hub 服务所有连接，过滤依据属于业务语义。
	UserUID string
	// AdminOnly 为真时只有管理员可见（如全局的系统通知）。
	AdminOnly bool
}

// subscriber 是一个订阅者。
type subscriber struct {
	// ch 事件通道。缓冲满时丢弃（不阻塞发布方）。
	ch chan Event
	// userUID 订阅者的身份，供 handler 侧的过滤使用。
	userUID string
	isAdmin bool
}

// Hub 是进程内的事件广播中心。并发安全。
type Hub struct {
	mu     sync.RWMutex
	subs   map[*subscriber]struct{}
	closed bool
	log    Logger
}

// Logger 是本包对日志的最小依赖。
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// New 构造 Hub。log 可为 nil。
func New(log Logger) *Hub {
	return &Hub{subs: make(map[*subscriber]struct{}), log: log}
}

// subscriberBuffer 是每个订阅者的通道容量。
//
// 取 256：足够吸收一次批量上传的进度事件，又不会在客户端卡死时堆积内存。
const subscriberBuffer = 256

// Subscribe 注册一个订阅者。
//
// 返回的 cancel **必须**被调用（通常 defer），否则会泄漏通道与 map 条目。
// userUID/isAdmin 用于订阅端的可见性过滤。
func (h *Hub) Subscribe(userUID string, isAdmin bool) (<-chan Event, func()) {
	sub := &subscriber{
		ch:      make(chan Event, subscriberBuffer),
		userUID: userUID,
		isAdmin: isAdmin,
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		close(sub.ch)
		return sub.ch, func() {}
	}
	h.subs[sub] = struct{}{}
	h.mu.Unlock()

	// cancel 必须**幂等**，且必须能容忍「Hub 已经 Close 过」。
	//
	// 场景：进程退出时 Hub.Close() 会关闭所有订阅者通道并清空 subs；
	// 与此同时，仍在运行的 SSE handler 会执行 `defer cancel()`。
	// 若 cancel 无条件 close(ch)，就会 panic: close of closed channel ——
	// 也就是「优雅关闭时反而崩掉」。
	//
	// 做法：在锁内判断订阅是否仍在表里；不在（说明 Close 已处理）就只跳过，
	// 不再 close。
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			_, present := h.subs[sub]
			if present {
				delete(h.subs, sub)
			}
			h.mu.Unlock()

			if present {
				close(sub.ch)
			}
		})
	}
	return sub.ch, cancel
}

// Publish 广播一条事件。
//
// 语义：**非阻塞**。订阅者缓冲满时该事件对它丢弃（并记 debug 日志）。
//
//	理由：SSE 客户端可能因为网络慢、页面卡住而停止消费；
//	如果这里阻塞，会把「推送事件」的上传 worker 一起卡住 —— 不可接受。
//	前端有「重连后用 GET /jobs 做状态对齐」的约定（docs/API.md §8.1），
//	因此丢事件不会导致状态永久不一致。
func (h *Hub) Publish(ev Event) {
	if ev.Name == "" {
		return
	}

	h.mu.RLock()
	subs := make([]*subscriber, 0, len(h.subs))
	for s := range h.subs {
		subs = append(subs, s)
	}
	closed := h.closed
	h.mu.RUnlock()

	if closed {
		return
	}

	for _, s := range subs {
		// AdminOnly 的事件只投给管理员
		if ev.AdminOnly && !s.isAdmin {
			continue
		}
		select {
		case s.ch <- ev:
		default:
			if h.log != nil {
				h.log.Debug("事件被丢弃（订阅者缓冲已满）",
					"event", ev.Name, "user_uid", s.userUID)
			}
		}
	}
}

// PublishUploadProgress 是上传进度的便捷发布。
func (h *Hub) PublishUploadProgress(jobUID, uploadUID string, seq int, fileName string, progress int, userUID string) {
	h.Publish(Event{
		Name:    EventUploadProgress,
		UserUID: userUID,
		Data: map[string]any{
			"JobUID": jobUID, "UploadUID": uploadUID, "Seq": seq,
			"FileName": fileName, "Progress": progress,
		},
	})
}

// PublishUploadFinished 单文件成功。
func (h *Hub) PublishUploadFinished(jobUID, uploadUID string, seq int, fileName, url, thumbURL, userUID string) {
	h.Publish(Event{
		Name:    EventUploadFinished,
		UserUID: userUID,
		Data: map[string]any{
			"JobUID": jobUID, "UploadUID": uploadUID, "Seq": seq,
			"FileName": fileName, "URL": url, "ThumbURL": thumbURL,
		},
	})
}

// PublishUploadFailed 单文件最终失败。
func (h *Hub) PublishUploadFailed(jobUID, uploadUID string, seq int, fileName, errMsg string, attempts int, userUID string) {
	h.Publish(Event{
		Name:    EventUploadFailed,
		UserUID: userUID,
		Data: map[string]any{
			"JobUID": jobUID, "UploadUID": uploadUID, "Seq": seq,
			"FileName": fileName, "Error": errMsg, "Attempts": attempts,
		},
	})
}

// PublishJobLog 任务逐行日志。
func (h *Hub) PublishJobLog(jobUID string, seq int, line string, createdAt int64, userUID string) {
	h.Publish(Event{
		Name:    EventJobLog,
		UserUID: userUID,
		Data: map[string]any{
			"JobUID": jobUID, "Seq": seq, "Line": line, "CreatedAt": createdAt,
		},
	})
}

// JobFinishedPayload 是 job.finished 的事件体。
type JobFinishedPayload struct {
	JobUID         string `json:"JobUID"`
	Kind           string `json:"Kind"`
	Status         string `json:"Status"`
	Progress       int    `json:"Progress"`
	TotalItems     int    `json:"TotalItems"`
	SucceededItems int    `json:"SucceededItems"`
	FailedItems    int    `json:"FailedItems"`
	SkippedItems   int    `json:"SkippedItems"`
}

// PublishJobFinished job 结束。
func (h *Hub) PublishJobFinished(p JobFinishedPayload, userUID string) {
	h.Publish(Event{Name: EventJobFinished, UserUID: userUID, Data: p})
}

// PublishJobStarted job 开始。
func (h *Hub) PublishJobStarted(jobUID, kind, userUID string) {
	h.Publish(Event{
		Name:    EventJobStarted,
		UserUID: userUID,
		Data:    map[string]any{"JobUID": jobUID, "Kind": kind, "Status": "running"},
	})
}

// PublishNotice 系统提示（如「内核正在重启」）。
//
// AdminOnly = false：提示要发给所有在线用户（他们都会受影响）。
func (h *Hub) PublishNotice(level, message string) {
	h.Publish(Event{
		Name: EventSystemNotice,
		Data: map[string]any{"Level": level, "Message": message},
	})
}

// SubscriberCount 返回当前订阅者数（健康检查/测试用）。
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

// Close 关闭 Hub 并释放全部订阅者通道。
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	subs := make([]*subscriber, 0, len(h.subs))
	for s := range h.subs {
		subs = append(subs, s)
	}
	h.subs = make(map[*subscriber]struct{})
	h.mu.Unlock()

	for _, s := range subs {
		close(s.ch)
	}
}
