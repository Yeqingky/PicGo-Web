package events

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestHubPublishSubscribe 断言基本的广播语义。
func TestHubPublishSubscribe(t *testing.T) {
	h := New(quietLogger())
	defer h.Close()

	ch, cancel := h.Subscribe("usr_a", false)
	defer cancel()

	h.Publish(Event{Name: EventUploadFinished, UserUID: "usr_a", Data: map[string]any{"URL": "x"}})

	select {
	case ev := <-ch:
		if ev.Name != EventUploadFinished {
			t.Fatalf("事件名不符：%s", ev.Name)
		}
		if ev.UserUID != "usr_a" {
			t.Fatalf("UserUID 应被带下来（订阅端据此过滤）：%q", ev.UserUID)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到事件")
	}
}

// TestHubMultipleSubscribers 断言一条事件广播给全部订阅者。
func TestHubMultipleSubscribers(t *testing.T) {
	h := New(quietLogger())
	defer h.Close()

	ch1, c1 := h.Subscribe("usr_a", false)
	ch2, c2 := h.Subscribe("usr_b", false)
	defer c1()
	defer c2()

	h.Publish(Event{Name: EventSystemNotice, Data: map[string]any{"Message": "hi"}})

	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("订阅者 %d 未收到事件", i)
		}
	}
}

// TestHubSlowConsumerDropped 断言**慢消费者被丢弃而不是阻塞发布方**。
//
// 这是本 Hub 最重要的可靠性设计：一个卡住的浏览器不能拖垮上传 worker。
func TestHubSlowConsumerDropped(t *testing.T) {
	h := New(quietLogger())
	defer h.Close()

	// 订阅但**不消费**
	ch, cancel := h.Subscribe("usr_slow", false)
	defer cancel()

	done := make(chan struct{})
	go func() {
		// 发布远超缓冲容量的事件；若 Hub 阻塞，这里会卡住
		for i := 0; i < subscriberBuffer*3; i++ {
			h.Publish(Event{Name: EventUploadProgress, Data: map[string]any{"i": i}})
		}
		close(done)
	}()

	select {
	case <-done:
		// 发布方没有被阻塞 ✓
	case <-time.After(2 * time.Second):
		t.Fatal("Publish 被慢消费者阻塞了（应丢弃而不是阻塞）")
	}

	// 缓冲里的数量不应超过容量
	if got := len(ch); got > subscriberBuffer {
		t.Fatalf("缓冲长度 %d 超过容量 %d", got, subscriberBuffer)
	}
}

// TestHubAdminOnlyEvent 断言 AdminOnly 事件只投给管理员。
func TestHubAdminOnlyEvent(t *testing.T) {
	h := New(quietLogger())
	defer h.Close()

	userCh, uc := h.Subscribe("usr_u", false)
	adminCh, ac := h.Subscribe("usr_admin", true)
	defer uc()
	defer ac()

	h.Publish(Event{Name: "job.secret", AdminOnly: true, Data: map[string]any{}})

	select {
	case <-userCh:
		t.Fatal("普通用户不应收到 AdminOnly 事件")
	case <-time.After(100 * time.Millisecond):
		// 期望：没收到
	}

	select {
	case <-adminCh:
		// 期望：管理员收到
	case <-time.After(time.Second):
		t.Fatal("管理员应收到 AdminOnly 事件")
	}
}

// TestHubCancelRemovesSubscriber 断言 cancel 会移除订阅并关闭通道。
func TestHubCancelRemovesSubscriber(t *testing.T) {
	h := New(quietLogger())
	defer h.Close()

	ch, cancel := h.Subscribe("usr_a", false)
	if h.SubscriberCount() != 1 {
		t.Fatalf("订阅数应为 1，实际 %d", h.SubscriberCount())
	}

	cancel()
	// 幂等：重复 cancel 不应 panic（close of closed channel）
	cancel()

	if h.SubscriberCount() != 0 {
		t.Fatalf("取消后订阅数应为 0，实际 %d", h.SubscriberCount())
	}
	// 通道应已关闭
	if _, ok := <-ch; ok {
		t.Fatal("取消后通道应已关闭")
	}
}

// TestHubCloseIdempotent 断言 Close 可重复调用且不 panic。
func TestHubCloseIdempotent(t *testing.T) {
	h := New(quietLogger())
	_, cancel := h.Subscribe("usr_a", false)
	defer cancel()

	h.Close()
	h.Close() // 重复关闭不应 panic

	// 关闭后发布不应 panic
	h.Publish(Event{Name: EventPing})

	// 关闭后订阅应返回已关闭的通道
	ch2, c2 := h.Subscribe("usr_b", false)
	defer c2()
	if _, ok := <-ch2; ok {
		t.Fatal("已关闭的 Hub 不应再接受订阅")
	}
}

// TestHubPublishUploadProgressPayload 断言事件体字段是 PascalCase（D81）。
func TestHubPublishUploadProgressPayload(t *testing.T) {
	h := New(quietLogger())
	defer h.Close()

	ch, cancel := h.Subscribe("usr_a", false)
	defer cancel()

	h.PublishUploadProgress("job_1", "up_1", 2, "a.png", 60, "usr_a")

	ev := <-ch
	m, ok := ev.Data.(map[string]any)
	if !ok {
		t.Fatalf("事件体应为 map：%T", ev.Data)
	}
	// PascalCase（不是 jobUid / uploadUid）
	if m["JobUID"] != "job_1" || m["UploadUID"] != "up_1" {
		t.Fatalf("事件体字段应为 PascalCase：%v", m)
	}
	if _, bad := m["jobUid"]; bad {
		t.Fatal("不得使用 camelCase 字段名")
	}
	if m["Progress"] != 60 {
		t.Fatalf("Progress 不符：%v", m["Progress"])
	}
	// 事件名保持小写点分（协议层标识符，与 D81 无关）
	if ev.Name != "upload.progress" {
		t.Fatalf("事件名应为小写点分，实际 %s", ev.Name)
	}
}

// TestHubPublishJobFinishedPayload 断言 job 结束事件的字段完整（D37：无 partial）。
func TestHubPublishJobFinishedPayload(t *testing.T) {
	h := New(quietLogger())
	defer h.Close()

	ch, cancel := h.Subscribe("usr_a", false)
	defer cancel()

	h.PublishJobFinished(JobFinishedPayload{
		JobUID: "job_1", Kind: "upload", Status: "failed", Progress: 100,
		TotalItems: 3, SucceededItems: 2, FailedItems: 1, SkippedItems: 0,
	}, "usr_a")

	ev := <-ch
	if ev.Name != EventJobFinished {
		t.Fatalf("事件名不符：%s", ev.Name)
	}
	payload, ok := ev.Data.(JobFinishedPayload)
	if !ok {
		t.Fatalf("事件体类型不符：%T", ev.Data)
	}
	if payload.Status != "failed" || payload.SucceededItems != 2 || payload.FailedItems != 1 {
		t.Fatalf("载荷不符：%+v", payload)
	}
}

// TestHubEmptyEventNameIgnored 断言空事件名被忽略（防脏数据）。
func TestHubEmptyEventNameIgnored(t *testing.T) {
	h := New(quietLogger())
	defer h.Close()

	ch, cancel := h.Subscribe("usr_a", false)
	defer cancel()

	h.Publish(Event{Name: "", Data: "x"})

	select {
	case ev := <-ch:
		t.Fatalf("空事件名不应被广播，却收到了 %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

// ---------------------------------------------------------------------------
// Bridge：解析 agent 的 SSE 并转发到 Hub
// ---------------------------------------------------------------------------

// TestBridgeForwardsSSEFrames 断言 Bridge 能解析 SSE 帧并转发到 Hub。
func TestBridgeForwardsSSEFrames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 校验令牌被带上（agent 的 SSE 也要求鉴权）
		if r.Header.Get("X-Agent-Token") != "tok" {
			t.Errorf("SSE 请求应带 X-Agent-Token，实际 %q", r.Header.Get("X-Agent-Token"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		// 把事件一次性写完并结束响应（Bridge 会重连，但我们只验一轮）
		_, _ = w.Write([]byte(": keep-alive\n\n"))
		_, _ = w.Write([]byte("event: job.log\ndata: {\"JobUID\":\"j1\",\"Line\":\"hello\"}\n\n"))
		_, _ = w.Write([]byte("event: system.notice\ndata: {\"Level\":\"warn\",\"Message\":\"注意\"}\n\n"))
		flusher.Flush()
	}))
	defer srv.Close()

	hub := New(quietLogger())
	defer hub.Close()

	ch, cancel := hub.Subscribe("usr_a", true)
	defer cancel()

	bridge := NewAgentBridge(AgentBridgeConfig{
		EventsURL: srv.URL,
		Token:     "tok",
	}, hub, quietLogger())

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()
	go bridge.Run(ctx)

	got := map[string]bool{}
	deadline := time.After(3 * time.Second)
	for len(got) < 2 {
		select {
		case ev := <-ch:
			got[ev.Name] = true
			if ev.Name == "job.log" {
				m, ok := ev.Data.(map[string]any)
				if !ok || m["JobUID"] != "j1" {
					t.Fatalf("job.log 载荷不符：%#v", ev.Data)
				}
			}
		case <-deadline:
			t.Fatalf("超时未收到全部事件，已收到：%v", got)
		}
	}
}

// TestBridgeReconnectsOnFailure 断言 agent 不可用时 Bridge 会重试。
func TestBridgeReconnectsOnFailure(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		// 第一次直接失败，之后正常
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: system.notice\ndata: {\"Message\":\"recovered\"}\n\n"))
		w.(http.Flusher).Flush()
	}))
	defer srv.Close()

	hub := New(quietLogger())
	defer hub.Close()
	ch, cancel := hub.Subscribe("usr_a", true)
	defer cancel()

	bridge := NewAgentBridge(AgentBridgeConfig{EventsURL: srv.URL, Token: "t"}, hub, quietLogger())
	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()
	go bridge.Run(ctx)

	select {
	case ev := <-ch:
		if ev.Name != "system.notice" {
			t.Fatalf("重连后应收到事件，实际 %s", ev.Name)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Bridge 未能在失败后重连")
	}
}

// TestBridgeEmptyURLDoesNotPanic 断言空 URL 时只是报错重试，不会 panic。
func TestBridgeEmptyURLDoesNotPanic(t *testing.T) {
	hub := New(quietLogger())
	defer hub.Close()

	bridge := NewAgentBridge(AgentBridgeConfig{EventsURL: "", Token: "t"}, hub, quietLogger())
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		bridge.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run 未在 ctx 取消后退出")
	}
}

// TestBridgeHandlesMultilineData 断言多行 data 被正确拼接（SSE 规范）。
func TestBridgeHandlesMultilineData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// 多行 data：按规范应拼接为 "line1\nline2"
		_, _ = w.Write([]byte("event: job.log\ndata: line1\ndata: line2\n\n"))
		w.(http.Flusher).Flush()
	}))
	defer srv.Close()

	hub := New(quietLogger())
	defer hub.Close()
	ch, cancel := hub.Subscribe("usr_a", true)
	defer cancel()

	bridge := NewAgentBridge(AgentBridgeConfig{EventsURL: srv.URL, Token: "t"}, hub, quietLogger())
	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()
	go bridge.Run(ctx)

	select {
	case ev := <-ch:
		if ev.Name != "job.log" {
			t.Fatalf("事件名不符：%s", ev.Name)
		}
		// 非 JSON 的 data 会被包成 {Raw: ...}，保证不丢内容
		m, ok := ev.Data.(map[string]any)
		if !ok {
			t.Fatalf("载荷类型不符：%T", ev.Data)
		}
		raw, _ := m["Raw"].(string)
		if !strings.Contains(raw, "line1") || !strings.Contains(raw, "line2") {
			t.Fatalf("多行 data 应被拼接保留，实际 %q", raw)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未收到事件")
	}
}
