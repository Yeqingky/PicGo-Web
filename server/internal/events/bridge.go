package events

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// AgentBridgeConfig 是 Bridge 的构造参数。
type AgentBridgeConfig struct {
	// EventsURL agent 的 SSE 端点（`http://127.0.0.1:36678/api/events`）。
	EventsURL string
	// Token 共享令牌（agent 的 SSE 也要求鉴权）。
	Token string
	// HTTPClient 可选，便于测试注入。
	HTTPClient *http.Client
}

// AgentBridge 把 picgo-agent 的 SSE 事件转发到本 Hub。
//
// 为什么需要它：agent 是唯一能观察到「上传进度」与「npm 安装日志」的地方，
// 但浏览器**不能**直连 agent（它只监听 127.0.0.1 且要求内部令牌）。
// 因此由 Go 侧单向订阅 agent 的 SSE，再经 Hub 广播给浏览器。
//
// 断线重连采用**指数退避**（1s → 2s → 4s … 上限 30s），与前端约定一致。
type AgentBridge struct {
	cfg AgentBridgeConfig
	hub *Hub
	log Logger
}

// NewAgentBridge 构造。
func NewAgentBridge(cfg AgentBridgeConfig, hub *Hub, log Logger) *AgentBridge {
	if cfg.HTTPClient == nil {
		// 无整体超时：SSE 是长连接，超时由 ctx 与空闲看护控制。
		// 连接阶段的超时放在 Transport 上（见 stream 的说明），不能放在
		// request context 上 —— 那会连响应 body 一起取消。
		cfg.HTTPClient = &http.Client{
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: bridgeConnectTimeout}).DialContext,
				TLSHandshakeTimeout:   bridgeConnectTimeout,
				ResponseHeaderTimeout: bridgeConnectTimeout,
			},
		}
	}
	return &AgentBridge{cfg: cfg, hub: hub, log: log}
}

// 重连退避参数（与前端 SSE 一致：1s→2s→4s→8s，上限 30s）。
const (
	bridgeBackoffMin = 1 * time.Second
	bridgeBackoffMax = 30 * time.Second
	// bridgeConnectTimeout 单次连接建立的超时。
	bridgeConnectTimeout = 10 * time.Second
	// bridgeReadIdleTimeout 多久没收到任何字节就认为连接已死。
	//
	// agent 每 25s 发一次 ping，所以 60s 无字节必定是死连接。
	bridgeReadIdleTimeout = 60 * time.Second
)

// Run 持续订阅 agent 事件，直到 ctx 取消。
//
// 本方法**阻塞**，应在独立 goroutine 中调用：
//
//	go bridge.Run(ctx)
func (b *AgentBridge) Run(ctx context.Context) {
	backoff := bridgeBackoffMin
	for {
		if ctx.Err() != nil {
			return
		}

		startedAt := time.Now()
		err := b.stream(ctx)
		if ctx.Err() != nil {
			return
		}

		// 连上并稳定运行过一段时间 → 重置退避（避免长期运行后一次抖动就等 30s）
		if time.Since(startedAt) > 2*bridgeBackoffMax {
			backoff = bridgeBackoffMin
		}

		if err != nil && b.log != nil {
			b.log.Warn("agent 事件流断开，将重连",
				"err", err, "retry_in", backoff.String())
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > bridgeBackoffMax {
			backoff = bridgeBackoffMax
		}
	}
}

// stream 建立一次连接并读取直到出错。
//
// ⚠️ 连接超时**不能**用 `context.WithTimeout` + 建立后 cancel 的方式：
// Go 的 http client 会把「request context 结束」直接等同于「丢弃响应」，
// cancel 后 body 立刻报 context canceled —— 表现就是刚连上就断、
// 无限重连循环（踩过）。超时下沉到 Transport 的 Dialer/TLS/响应头（见构造）。
func (b *AgentBridge) stream(ctx context.Context) error {
	if strings.TrimSpace(b.cfg.EventsURL) == "" {
		return fmt.Errorf("agent EventsURL 为空")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.cfg.EventsURL, nil)
	if err != nil {
		return fmt.Errorf("构造 SSE 请求失败: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	if b.cfg.Token != "" {
		req.Header.Set("X-Agent-Token", b.cfg.Token)
	}

	resp, err := b.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("连接 agent 事件流失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent 事件流返回 HTTP %d", resp.StatusCode)
	}


	if b.log != nil {
		b.log.Info("已连接 agent 事件流", "url", b.cfg.EventsURL)
	}

	return b.readLoop(ctx, resp.Body)
}

// readLoop 解析 SSE 帧并转发。
//
// 空闲看护：起一个定时器，超过 bridgeReadIdleTimeout 没有任何字节就主动断开重连
// （防止 TCP 半开导致「看起来连着但收不到事件」）。
func (b *AgentBridge) readLoop(ctx context.Context, body io.Reader) error {
	type frame struct {
		event string
		data  string
	}

	frames := make(chan frame, 16)
	errCh := make(chan error, 1)

	// 读取 goroutine：按 SSE 规范逐帧解析
	go func() {
		scanner := bufio.NewScanner(body)
		// 单帧可能很大（job.log 的一行），给 4 MiB
		scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)

		var cur frame
		for scanner.Scan() {
			line := scanner.Text()

			switch {
			case line == "":
				// 空行 = 一帧结束
				if cur.event != "" || cur.data != "" {
					select {
					case frames <- cur:
					default:
						// 下游处理不过来，丢弃该帧（与 Hub 的「慢消费者丢弃」一致）
					}
				}
				cur = frame{}

			case strings.HasPrefix(line, ":"):
				// 注释（心跳），忽略内容但**算作有活动**
				continue

			case strings.HasPrefix(line, "event:"):
				cur.event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))

			case strings.HasPrefix(line, "data:"):
				chunk := strings.TrimPrefix(line, "data:")
				chunk = strings.TrimPrefix(chunk, " ")
				if cur.data != "" {
					cur.data += "\n"
				}
				cur.data += chunk
			}
		}

		// 流结束前可能还有未闭合的帧
		if cur.event != "" || cur.data != "" {
			select {
			case frames <- cur:
			default:
			}
		}

		if err := scanner.Err(); err != nil {
			errCh <- err
			return
		}
		errCh <- io.EOF
	}()

	idle := time.NewTimer(bridgeReadIdleTimeout)
	defer idle.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case err := <-errCh:
			return err

		case f := <-frames:
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(bridgeReadIdleTimeout)
			b.forward(f.event, f.data)
		}
	}
}

// forward 把一帧转发到 Hub。
func (b *AgentBridge) forward(name, data string) {
	if name == "" {
		// 只有 data 没有 event 时，SSE 规范默认事件名是 "message"；
		// agent 不会这么发，忽略以免污染前端事件集。
		return
	}

	var payload any
	if strings.TrimSpace(data) != "" {
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			// 解析失败也要转发原始字符串，便于排查（前端会看到非对象 data）
			payload = map[string]any{"Raw": data}
		}
	}

	// agent 侧的事件没有 UserUID 信息 → 广播给所有人。
	//
	// 这是**有意为之**：agent 的事件只用于「进度与日志」这类非敏感内容，
	// 而真正需要按用户隔离的 job 状态在 Go 侧（自己的 Jobs 表）。
	// 若将来要严格隔离，可让 agent 在事件里带上 UserUID，再在这里填。
	b.hub.Publish(Event{Name: name, Data: payload})
}

// PublishUploadFinishedFromAgent 是给 agent 事件补 UserUID 的扩展点。
//
// 说明：Go 侧在上传流程里**也会**发一套带 UserUID 的事件（见 upload_service），
// 因此前端可能同时收到 agent 的（无归属）与我们自己的（有归属）事件。
// 前端据此以「带 UserUID 的事件为准」，并忽略来自 agent 的重复进度 —— 见
// docs/API.md §8.1「投递范围」。
func (b *AgentBridge) PublishUploadFinishedFromAgent(payload map[string]any) {
	ev := Event{Name: EventUploadFinished, Data: payload}
	if uid, _ := payload["UserUID"].(string); uid != "" {
		ev.UserUID = uid
	}
	b.hub.Publish(ev)
}
