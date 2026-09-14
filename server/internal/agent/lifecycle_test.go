package agent

import (
	"context"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newSupervisor(t *testing.T, token, tokenFile string) *Supervisor {
	t.Helper()
	s, err := NewSupervisor(SupervisorConfig{
		BaseURL:   "http://127.0.0.1:36678",
		Token:     token,
		TokenFile: tokenFile,
		Log:       testLogger(),
	})
	if err != nil {
		t.Fatalf("NewSupervisor 失败: %v", err)
	}
	return s
}

// ---- 令牌解析：env > 文件 > 新生成 ----

func TestResolveTokenFromEnvWins(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "agent-token.txt")

	// 文件内容与 env 不同，应取 env
	if err := os.WriteFile(file, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newSupervisor(t, "from-env", file)
	if s.Token() != "from-env" {
		t.Errorf("应优先使用 env 令牌，实际 %q", s.Token())
	}
}

func TestResolveTokenFromFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "agent-token.txt")
	if err := os.WriteFile(file, []byte("reused-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newSupervisor(t, "", file)
	if s.Token() != "reused-token" {
		t.Errorf("应从文件复用令牌，实际 %q", s.Token())
	}
}

func TestResolveTokenGeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "agent-token.txt")

	s := newSupervisor(t, "", file)
	token := s.Token()

	// 64 位十六进制（32 字节）
	if len(token) != 64 {
		t.Fatalf("生成的令牌应为 64 个十六进制字符，实际 %d（%s）", len(token), token)
	}
	if _, err := hex.DecodeString(token); err != nil {
		t.Errorf("令牌不是合法十六进制: %v", err)
	}

	// 必须落盘且权限 0600
	info, err := os.Stat(file)
	if err != nil {
		t.Fatalf("令牌文件未创建: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("令牌文件权限应为 0600，实际 %o", perm)
	}
	raw, _ := os.ReadFile(file)
	if strings.TrimSpace(string(raw)) != token {
		t.Error("落盘的令牌与内存中的不一致")
	}

	// 第二次构造应复用同一令牌（幂等）
	s2 := newSupervisor(t, "", file)
	if s2.Token() != token {
		t.Errorf("第二次应复用已有令牌，得到 %q", s2.Token())
	}
}

func TestResolveTokenNoFileAndNoEnvFails(t *testing.T) {
	_, err := NewSupervisor(SupervisorConfig{
		BaseURL: "http://127.0.0.1:36678",
		Log:     testLogger(),
	})
	if err == nil {
		t.Fatal("env 与 TokenFile 都为空时应当报错")
	}
}

func TestNewSupervisorValidatesRequiredFields(t *testing.T) {
	if _, err := NewSupervisor(SupervisorConfig{Log: testLogger(), TokenFile: "/tmp/x"}); err == nil {
		t.Error("BaseURL 为空应当报错")
	}
	if _, err := NewSupervisor(SupervisorConfig{BaseURL: "http://127.0.0.1:1", TokenFile: "/tmp/x"}); err == nil {
		t.Error("Log 为空应当报错")
	}
}

// ---- 环境变量组装 ----

func TestChildEnvInjectsTokenAndListen(t *testing.T) {
	s := newSupervisor(t, "tok123", "")
	s.cfg.ExtraEnv = map[string]string{
		"PICGO_AGENT_CONFIG_PATH":  "/data/picgo/config.json",
		"PICGO_AGENT_NPM_REGISTRY": "https://registry.npmmirror.com",
		"CUSTOM_EMPTY":             "", // 空值应被跳过
	}

	env := s.childEnv()
	got := map[string]string{}
	for _, kv := range env {
		if i := strings.Index(kv, "="); i > 0 {
			got[kv[:i]] = kv[i+1:]
		}
	}

	// 令牌必须注入（否则 agent 拒绝启动 —— 这就是"内核不可用"的根因）
	if got["PICGO_AGENT_TOKEN"] != "tok123" {
		t.Errorf("未注入 PICGO_AGENT_TOKEN，实际 %q", got["PICGO_AGENT_TOKEN"])
	}
	// 监听地址由 BaseURL 推导
	if got["PICGO_AGENT_HOST"] != "127.0.0.1" {
		t.Errorf("PICGO_AGENT_HOST 应为 127.0.0.1，实际 %q", got["PICGO_AGENT_HOST"])
	}
	if got["PICGO_AGENT_PORT"] != "36678" {
		t.Errorf("PICGO_AGENT_PORT 应为 36678，实际 %q", got["PICGO_AGENT_PORT"])
	}
	if got["PICGO_AGENT_CONFIG_PATH"] != "/data/picgo/config.json" {
		t.Errorf("ExtraEnv 未生效: %q", got["PICGO_AGENT_CONFIG_PATH"])
	}
	if _, exists := got["CUSTOM_EMPTY"]; exists {
		t.Error("空值的 ExtraEnv 应被跳过")
	}
}

func TestSplitHostPort(t *testing.T) {
	cases := []struct{ in, host, port string }{
		{"http://127.0.0.1:36678", "127.0.0.1", "36678"},
		{"http://127.0.0.1:36679/", "127.0.0.1", "36679"},
		{"https://agent.internal:8443/api", "agent.internal", "8443"},
		{"http://localhost", "localhost", "36678"},
	}
	for _, tc := range cases {
		host, port := splitHostPort(tc.in)
		if host != tc.host || port != tc.port {
			t.Errorf("splitHostPort(%q) = (%q,%q)，期望 (%q,%q)", tc.in, host, port, tc.host, tc.port)
		}
	}
}

func TestMaskTokenPrefix(t *testing.T) {
	if got := maskTokenPrefix("short"); got != "******" {
		t.Errorf("短令牌应整体掩码，实际 %q", got)
	}
	if got := maskTokenPrefix("0123456789abcdef"); got != "01234567…" {
		t.Errorf("长令牌应保留前 8 位，实际 %q", got)
	}
}

// ---- 命令解析 ----

func TestResolveCommandExplicit(t *testing.T) {
	s := newSupervisor(t, "t", "")
	s.cfg.Command = []string{"node", "/custom/agent.js"}
	cmd, dir, err := s.resolveCommand()
	if err != nil {
		t.Fatalf("显式命令不应报错: %v", err)
	}
	if len(cmd) != 2 || cmd[0] != "node" || cmd[1] != "/custom/agent.js" {
		t.Errorf("命令未原样使用: %v", cmd)
	}
	if dir == "" {
		t.Error("未提供 WorkDir 时应按命令路径推断")
	}
}

func TestResolveCommandAutoDetect(t *testing.T) {
	// 在临时 cwd 下造出 picgo-agent/dist/index.js，验证被自动探测
	dir := t.TempDir()
	entry := filepath.Join(dir, "picgo-agent", "dist")
	if err := os.MkdirAll(entry, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entry, "index.js"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	s := newSupervisor(t, "t", "")
	cmd, workDir, err := s.resolveCommand()
	if err != nil {
		t.Fatalf("应自动探测到入口: %v", err)
	}
	if cmd[0] != "node" {
		t.Errorf("第一个参数应为 node，实际 %q", cmd[0])
	}
	if filepath.Base(cmd[1]) != "index.js" {
		t.Errorf("入口文件不对: %s", cmd[1])
	}
	// 工作目录应为 agent 根目录（<agentDir>），而不是 dist/
	if filepath.Base(workDir) != "picgo-agent" {
		t.Errorf("工作目录应为 agent 根目录，实际 %s", workDir)
	}
}

func TestResolveCommandExplicitAgentDir(t *testing.T) {
	dir := t.TempDir()
	dist := filepath.Join(dir, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "index.js"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 即使 cwd 是别处，显式 AgentDir 也应命中
	oldWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	s := newSupervisor(t, "t", "")
	s.cfg.AgentDir = dir

	cmd, workDir, err := s.resolveCommand()
	if err != nil {
		t.Fatalf("显式 AgentDir 应命中: %v", err)
	}
	if cmd[0] != "node" || filepath.Base(cmd[1]) != "index.js" {
		t.Errorf("命令不正确: %v", cmd)
	}
	if workDir != dir {
		t.Errorf("工作目录应为 agent 根 %s，实际 %s", dir, workDir)
	}
}

func TestResolveCommandMissing(t *testing.T) {
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	s := newSupervisor(t, "t", "")
	_, _, err := s.resolveCommand()
	if err == nil {
		t.Fatal("找不到入口时应当报错")
	}
	if !strings.Contains(err.Error(), "PICGO_WEB_AGENT_AUTOSTART") {
		t.Errorf("错误信息应给出可操作的提示，实际: %v", err)
	}
}

// ---- 完整生命周期（用一个假的 agent 可执行文件）----

// fakeAgent 是一个能响应 /healthz 与 /api/shutdown 的最小 HTTP 服务，
// 通过 `go run` 编译成临时二进制后被子进程方式拉起。
//
// 为了不引入编译开销，这里改成：直接启动 httptest.Server 冒充 agent，
// 并让 Supervisor 用 `/bin/sh -c 'exit 0'` 作为「子进程」——
// 用于验证「未就绪即退出 → 计入重启次数」的行为。
func TestSpawnFailureIsCountedAndCapped(t *testing.T) {
	dir := t.TempDir()
	// 一个立刻退出、且不监听端口的"agent"
	stub := filepath.Join(dir, "stub.sh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	s, err := NewSupervisor(SupervisorConfig{
		Autostart: true,
		BaseURL:   "http://127.0.0.1:1", // 永远连不上
		Token:     "t",
		Log:       testLogger(),
		Command:   []string{"/bin/sh", stub},
		WorkDir:   dir,
		// 加快测试：极短的探测与退避
		StartupTimeout:     300 * time.Millisecond,
		HealthTimeout:      50 * time.Millisecond,
		RestartBackoffBase: time.Millisecond,
		RestartMaxAttempts: 3,
		ShutdownTimeout:    time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	var failed atomic.Int32
	s.OnStateChange(func(up bool, _ error) {
		if !up {
			failed.Add(1)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	// 等它跑完重试循环（放弃后 runLoop 返回，attempts 停在 max）
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		attempts := s.attempts
		s.mu.Unlock()
		if attempts >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	s.mu.Lock()
	attempts := s.attempts
	s.mu.Unlock()

	if attempts < 3 {
		t.Fatalf("连续失败应被计数到上限 3，实际 %d", attempts)
	}
	if attempts > 3 {
		t.Fatalf("超过上限后不应继续尝试，实际 %d", attempts)
	}
	if failed.Load() == 0 {
		t.Error("放弃时应回调一次状态变化（供事件总线发通知）")
	}

	s.Stop(context.Background()) // 幂等，不 panic
	s.Stop(context.Background())
}

// TestStartWithoutAutostartDoesNotSpawn 验证
// `PICGO_WEB_AGENT_AUTOSTART=false` 时不会拉起任何子进程。
func TestStartWithoutAutostartDoesNotSpawn(t *testing.T) {
	s, err := NewSupervisor(SupervisorConfig{
		Autostart:     false,
		BaseURL:       "http://127.0.0.1:1",
		Token:         "t",
		Log:           testLogger(),
		HealthTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Autostart=false 时 Start 不应报错: %v", err)
	}

	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd != nil {
		t.Error("Autostart=false 时不应有子进程")
	}

	cancel()
	s.Stop(context.Background())
}

// TestWaitReadyDetectsHealthyAgent 用一个真实的 HTTP server 冒充 agent，
// 验证 waitReady 能正确判定"就绪"。
func TestWaitReadyDetectsHealthyAgent(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// 校验令牌透传
		if r.Header.Get("X-Agent-Token") != "secret-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Ok":true,"PicgoVersion":"3.0.2"}`))
	}))
	defer srv.Close()

	s, err := NewSupervisor(SupervisorConfig{
		BaseURL:        srv.URL,
		Token:          "secret-token",
		Log:            testLogger(),
		HealthTimeout:  time.Second,
		StartupTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 直接调 waitReady（不经过子进程），验证探测逻辑。
	// 传一个永不退出的 procWait 桩。
	proc := &procWait{done: make(chan struct{})}
	if err := s.waitReady(context.Background(), proc); err != nil {
		t.Fatalf("waitReady 应成功: %v", err)
	}
	if hits.Load() == 0 {
		t.Error("未发出探测请求")
	}
}

func TestWaitReadyFailsOnEarlyExit(t *testing.T) {
	s, err := NewSupervisor(SupervisorConfig{
		BaseURL:        "http://127.0.0.1:1",
		Token:          "t",
		Log:            testLogger(),
		HealthTimeout:  50 * time.Millisecond,
		StartupTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 模拟「子进程已退出」：done 已关闭且 err 有值
	done := make(chan struct{})
	close(done)
	proc := &procWait{done: done, err: context.DeadlineExceeded}

	err = s.waitReady(context.Background(), proc)
	if err == nil {
		t.Fatal("子进程提前退出时应报错")
	}
	if !strings.Contains(err.Error(), "在就绪前退出") {
		t.Errorf("错误信息应说明原因，实际: %v", err)
	}
}

// TestLogLevelFromEnv 验证 Go 日志级别到 agent 的映射。
func TestLogLevelFromEnv(t *testing.T) {
	cases := map[string]string{
		"debug": "debug", "DEBUG": "debug",
		"warn": "warn", "warning": "warn",
		"error": "error",
		"info":  "info",
		"":      "info",
		"weird": "info",
	}
	for in, want := range cases {
		t.Setenv("PICGO_WEB_LOG_LEVEL", in)
		if got := logLevelFromEnv(); got != want {
			t.Errorf("logLevelFromEnv(%q) = %q，期望 %q", in, got, want)
		}
	}
}
