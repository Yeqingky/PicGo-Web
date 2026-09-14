package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// 默认重启策略（D7）。
const (
	// defaultRestartMaxAttempts 连续重启失败上限，超过后放弃（不再自动拉起）。
	defaultRestartMaxAttempts = 5
	// defaultRestartBackoffBase 退避基数：1s, 2s, 4s, 8s, 16s。
	defaultRestartBackoffBase = time.Second
	// defaultHealthTimeout 单次健康探测超时。
	defaultHealthTimeout = 2 * time.Second
	// defaultStartupTimeout 等待 agent 首次就绪的总时长。
	defaultStartupTimeout = 30 * time.Second
	// defaultShutdownTimeout 优雅关停 agent 的等待时长。
	defaultShutdownTimeout = 5 * time.Second
)

// SupervisorConfig 是侧车进程管理器的配置。
//
// 只有 `Autostart` 为真时才会真正拉起子进程；否则它只负责
// **令牌的准备与一致性**（独立部署场景下 Go 与 agent 仍需共享同一令牌）。
type SupervisorConfig struct {
	// Autostart 是否由 Go 拉起 agent 子进程（PICGO_WEB_AGENT_AUTOSTART）。
	Autostart bool

	// BaseURL agent 地址，如 http://127.0.0.1:36678。
	BaseURL string
	// Token 共享令牌。为空时按 env → 文件 → 新生成的顺序解析。
	Token string
	// TokenFile 令牌落盘位置（默认 <dataDir>/agent-token.txt）。
	TokenFile string

	// AgentDir picgo-agent 目录（含 dist/index.js）。
	// 为空时按 cwd → 可执行文件目录 → /app 的顺序自动探测。
	AgentDir string
	// Command 启动命令（显式指定时优先于 AgentDir）。
	Command []string
	// WorkDir 子进程工作目录（默认 agentDir）。
	WorkDir string
	// ExtraEnv 追加到子进程的环境变量（如 npm 源、代理）。
	ExtraEnv map[string]string

	// Log 进程日志。
	Log *slog.Logger

	// 可调参数（测试用；为 0 时取默认值）
	RestartMaxAttempts int
	RestartBackoffBase time.Duration
	HealthTimeout      time.Duration
	StartupTimeout     time.Duration
	ShutdownTimeout    time.Duration
}

// procWait 是可**重复读取**的子进程等待句柄。
//
// 为什么不用裸 `chan error`：`waitReady` 与错误清理路径都会读它，
// 一旦其中一方先把值取走，另一方就会永久阻塞（死锁）。
// 用「关闭的 channel + 字段存错误」即可任意次读取。
type procWait struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
}

// startProcWait 启动 cmd 并开启等待协程。
func startProcWait(cmd *exec.Cmd) *procWait {
	w := &procWait{cmd: cmd, done: make(chan struct{})}
	go func() {
		w.err = cmd.Wait()
		close(w.done)
	}()
	return w
}

// wait 阻塞直到进程退出，返回其退出错误。
func (w *procWait) wait() error {
	<-w.done
	return w.err
}

// hasExited 非阻塞地判断进程是否已退出。
func (w *procWait) hasExited() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

// killAndWait 终止进程并等待其真正退出（幂等）。
//
// 若进程已退出则直接返回；`cmd.Process.Kill()` 失败（进程已不存在）也不影响结果。
func (w *procWait) killAndWait() {
	if w.hasExited() {
		return
	}
	if w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
	<-w.done
}

// Supervisor 管理 picgo-agent 子进程的生命周期。
//
// 职责：
//  1. 令牌解析：env > 文件 > 新生成（写盘 0600），并保证与子进程一致
//  2. 拉起子进程并把令牌通过环境变量注入
//  3. 健康探测（`GET /healthz`）：失败则按退避重启，超过上限后放弃
//  4. 优雅关停：先 `POST /api/shutdown`，再 SIGTERM，最后 SIGKILL
//
// 并发安全。`Start` 与 `Stop` 各自只应被调用一次。
type Supervisor struct {
	cfg     SupervisorConfig
	log     *slog.Logger
	baseURL string
	token   string

	mu       sync.Mutex
	cmd      *exec.Cmd
	done     chan struct{}
	stopping bool
	attempts int
	// onStateChange 在 agent 状态变化时回调（供事件总线发系统通知）。
	onStateChange func(up bool, err error)
}

// NewSupervisor 构造管理器并完成令牌解析。
//
// 令牌解析顺序（与 docs/DECISIONS.md D7 一致）：
//
//  1. cfg.Token（来自环境变量 PICGO_WEB_AGENT_TOKEN）
//  2. cfg.TokenFile 已存在 → 读取
//  3. 都没有 → 生成 32 字节随机令牌并写入 cfg.TokenFile（权限 0600）
//
// 返回的 Supervisor 已带最终令牌；调用方应把 `Token()` 传给 Client，
// 保证与服务端一致。
func NewSupervisor(cfg SupervisorConfig) (*Supervisor, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("agent: SupervisorConfig.BaseURL 不能为空")
	}
	if cfg.Log == nil {
		return nil, errors.New("agent: SupervisorConfig.Log 不能为空")
	}

	token, generated, err := resolveToken(cfg.Token, cfg.TokenFile)
	if err != nil {
		return nil, err
	}
	if generated {
		cfg.Log.Warn("已生成新的 agent 共享令牌，请妥善保管",
			"path", cfg.TokenFile,
			// 只打印前缀，便于人工核对而不泄露完整令牌
			"token_prefix", maskTokenPrefix(token),
		)
	}

	s := &Supervisor{
		cfg:     cfg,
		log:     cfg.Log,
		baseURL: strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		token:   token,
	}

	if cfg.RestartMaxAttempts <= 0 {
		s.cfg.RestartMaxAttempts = defaultRestartMaxAttempts
	}
	if cfg.RestartBackoffBase <= 0 {
		s.cfg.RestartBackoffBase = defaultRestartBackoffBase
	}
	if cfg.HealthTimeout <= 0 {
		s.cfg.HealthTimeout = defaultHealthTimeout
	}
	if cfg.StartupTimeout <= 0 {
		s.cfg.StartupTimeout = defaultStartupTimeout
	}
	if cfg.ShutdownTimeout <= 0 {
		s.cfg.ShutdownTimeout = defaultShutdownTimeout
	}
	return s, nil
}

// Token 返回最终解析出的共享令牌。
//
// 调用方必须用它构造 Client —— 这是 Go 与 agent 能对上话的前提。
func (s *Supervisor) Token() string { return s.token }

// OnStateChange 注册状态变化回调（可为 nil）。须在 Start 之前调用。
func (s *Supervisor) OnStateChange(fn func(up bool, err error)) { s.onStateChange = fn }

// Start 启动管理。
//
// Autostart=false 时**不拉起子进程**，仅记录一条明确的提示
// （独立部署场景下由外部负责启动 agent）。
func (s *Supervisor) Start(ctx context.Context) error {
	if !s.cfg.Autostart {
		s.log.Info("未启用 agent 自动拉起（PICGO_WEB_AGENT_AUTOSTART=false）",
			"url", s.baseURL,
			"hint", "请自行确保 agent 已启动，并使用同一令牌（见 "+s.cfg.TokenFile+"）",
		)
		// 独立部署时也做一次探测，让用户立刻知道是否连得上
		go s.watchOnly(ctx)
		return nil
	}

	cmdline, workDir, err := s.resolveCommand()
	if err != nil {
		return err
	}
	s.cfg.Command = cmdline
	s.cfg.WorkDir = workDir

	s.log.Info("将由 Go 拉起 picgo-agent",
		"cmd", strings.Join(cmdline, " "),
		"workdir", workDir,
		"url", s.baseURL,
	)

	s.mu.Lock()
	s.done = make(chan struct{})
	s.mu.Unlock()

	go s.runLoop(ctx)
	return nil
}

// Stop 优雅关停 agent（幂等）。
func (s *Supervisor) Stop(ctx context.Context) {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return
	}
	s.stopping = true
	done := s.done
	cmd := s.cmd
	s.mu.Unlock()

	if done == nil {
		return // 从未 Start，或已 Stop
	}

	// 1) 先请求 agent 自我退出（它会关掉 HTTP server 与 SSE）
	shutdownCtx, cancel := context.WithTimeout(ctx, s.cfg.ShutdownTimeout)
	defer cancel()
	if client := New(Config{BaseURL: s.baseURL, Token: s.token, Log: s.log}); client != nil {
		if err := client.Shutdown(shutdownCtx); err != nil {
			s.log.Debug("请求 agent 优雅退出失败（可能已退出）", "err", err)
		}
	}

	// 2) 等子进程自己结束
	if cmd != nil && cmd.Process != nil {
		waitCh := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(waitCh)
		}()
		select {
		case <-waitCh:
			s.log.Info("picgo-agent 已退出")
		case <-shutdownCtx.Done():
			s.log.Warn("picgo-agent 未在期限内退出，发送 SIGKILL")
			_ = cmd.Process.Kill()
			<-waitCh
		}
	}

	close(done)
}

// ------------------------------------------------------------------ 内部实现

// runLoop 负责「拉起 → 探测 → 崩溃则退避重启」。
func (s *Supervisor) runLoop(ctx context.Context) {
	for {
		s.mu.Lock()
		stopping := s.stopping
		done := s.done
		s.mu.Unlock()
		if stopping {
			return
		}

		startErr := s.spawnAndWait(ctx)

		s.mu.Lock()
		stopping = s.stopping
		s.mu.Unlock()
		if stopping || ctx.Err() != nil {
			return
		}

		// 子进程退出（非我们主动停止）
		select {
		case <-done:
			return
		default:
		}

		s.mu.Lock()
		s.attempts++
		attempt := s.attempts
		s.mu.Unlock()

		if attempt >= s.cfg.RestartMaxAttempts {
			s.log.Error("picgo-agent 连续启动失败，已放弃自动拉起",
				"attempts", attempt,
				"last_err", startErr,
				"hint", "修好后可用后台「重启内核」或重启服务；也可设 PICGO_WEB_AGENT_AUTOSTART=false 自行管理",
			)
			s.notify(false, fmt.Errorf("agent 连续 %d 次启动失败: %w", attempt-1, startErr))
			return
		}

		backoff := s.cfg.RestartBackoffBase * time.Duration(1<<(attempt-1))
		s.log.Warn("picgo-agent 已退出，准备重启",
			"attempt", attempt,
			"max_attempts", s.cfg.RestartMaxAttempts,
			"backoff", backoff.String(),
			"err", startErr,
		)

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
	}
}

// spawnAndWait 启动一次子进程，等它就绪；若就绪前就退出则返回错误。
func (s *Supervisor) spawnAndWait(ctx context.Context) error {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return errors.New("已停止")
	}
	//nolint:gosec // 命令来自运维配置，非用户输入
	cmd := exec.CommandContext(ctx, s.cfg.Command[0], s.cfg.Command[1:]...)
	cmd.Dir = s.cfg.WorkDir
	cmd.Env = s.childEnv()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// 让子进程独立于父进程的进程组，便于精确控制其生命周期
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	if err := cmd.Start(); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("启动 agent 进程失败: %w", err)
	}
	s.cmd = cmd
	s.mu.Unlock()

	s.log.Info("picgo-agent 进程已启动", "pid", cmd.Process.Pid)

	proc := startProcWait(cmd)

	// 等待就绪；未就绪即退出或超时则清理句柄并返回错误（交给 runLoop 重启）
	if err := s.waitReady(ctx, proc); err != nil {
		proc.killAndWait()
		s.mu.Lock()
		s.cmd = nil
		s.mu.Unlock()
		return err
	}

	// 就绪：重置连续失败计数
	s.mu.Lock()
	s.attempts = 0
	s.mu.Unlock()
	s.log.Info("picgo-agent 已就绪", "url", s.baseURL)
	s.notify(true, nil)

	// 阻塞到它退出
	err := proc.wait()
	s.mu.Lock()
	s.cmd = nil
	s.mu.Unlock()
	return err
}

// waitReady 轮询 /healthz 直到就绪、子进程退出、或超时。
func (s *Supervisor) waitReady(ctx context.Context, proc *procWait) error {
	deadline := time.Now().Add(s.cfg.StartupTimeout)
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	client := New(Config{
		BaseURL:        s.baseURL,
		Token:          s.token,
		Log:            s.log,
		RequestTimeout: s.cfg.HealthTimeout,
	})

	probe := func() bool {
		probeCtx, cancel := context.WithTimeout(ctx, s.cfg.HealthTimeout)
		defer cancel()
		_, err := client.Healthz(probeCtx)
		return err == nil
	}

	for {
		if probe() {
			return nil
		}
		// 子进程可能在探测间隙就退出了
		if proc.hasExited() {
			return fmt.Errorf("agent 进程在就绪前退出: %w", proc.err)
		}
		select {
		case <-proc.done:
			return fmt.Errorf("agent 进程在就绪前退出: %w", proc.err)
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("agent 未在 %s 内就绪（地址 %s）", s.cfg.StartupTimeout, s.baseURL)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// watchOnly 是 Autostart=false 时的轻量看护：只探测并上报状态，不重启。
func (s *Supervisor) watchOnly(ctx context.Context) {
	client := New(Config{BaseURL: s.baseURL, Token: s.token, Log: s.log, RequestTimeout: s.cfg.HealthTimeout})

	wasUp := false
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		probeCtx, cancel := context.WithTimeout(ctx, s.cfg.HealthTimeout)
		_, err := client.Healthz(probeCtx)
		cancel()
		up := err == nil
		if up != wasUp {
			s.notify(up, err)
			if up {
				s.log.Info("picgo-agent 已就绪（外部启动）", "url", s.baseURL)
			} else {
				s.log.Warn("picgo-agent 不可用（外部启动）", "err", err, "url", s.baseURL)
			}
			wasUp = up
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

// childEnv 组装子进程环境变量。
//
// 关键：把**同一个令牌**通过 PICGO_AGENT_TOKEN 注入，否则 agent 会拒绝启动。
func (s *Supervisor) childEnv() []string {
	env := append([]string{}, os.Environ()...)

	// 解析出 host/port 供 agent 监听（与 Go 侧的 BaseURL 保持一致）
	host, port := splitHostPort(s.baseURL)

	env = append(env,
		"PICGO_AGENT_TOKEN="+s.token,
		"PICGO_AGENT_HOST="+host,
		"PICGO_AGENT_PORT="+port,
		"PICGO_AGENT_LOG_LEVEL="+logLevelFromEnv(),
	)

	for k, v := range s.cfg.ExtraEnv {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		env = append(env, k+"="+v)
	}
	return env
}

// resolveCommand 解析启动命令与工作目录。
//
// 优先级：
//  1. cfg.Command 或 PICGO_WEB_AGENT_COMMAND（运维显式指定）
//  2. cfg.AgentDir 或 PICGO_WEB_AGENT_DIR → <dir>/dist/index.js
//  3. 相对 **cwd**：`picgo-agent/dist/index.js`、`../picgo-agent/dist/index.js`
//  4. 相对 **可执行文件**：`<exeDir>/../picgo-agent/dist/index.js`、`<exeDir>/../../picgo-agent/dist/index.js`
//     （二进制在 `server/picgo-web` 时即命中仓库根）
//  5. `/app/picgo-agent/dist/index.js`（容器镜像）
//  6. PATH 上的 `picgo-agent`
//
// 为什么同时看 cwd 与 exe 目录：`go run` 时 cwd 在 server/ 能命中；
// 而直接运行编译好的二进制时 cwd 可能在任意位置，此时只能靠 exe 位置推断。
func (s *Supervisor) resolveCommand() ([]string, string, error) {
	if len(s.cfg.Command) > 0 {
		dir := s.cfg.WorkDir
		if dir == "" {
			dir = filepath.Dir(s.cfg.Command[0])
		}
		return s.cfg.Command, dir, nil
	}

	var tried []string

	try := func(entry string) ([]string, string, bool) {
		if strings.TrimSpace(entry) == "" {
			return nil, "", false
		}
		abs, err := filepath.Abs(entry)
		if err != nil {
			return nil, "", false
		}
		tried = append(tried, abs)
		if st, err := os.Stat(abs); err == nil && !st.IsDir() {
			// 工作目录取 **agent 根目录**（entry 在 <agentDir>/dist/index.js），
			// 而不是 entry 所在目录：agent 以此为 cwd 解析相对路径与包路径。
			return []string{"node", abs}, agentRootDir(abs), true
		}
		return nil, "", false
	}

	// 2) 显式指定的 agent 目录
	if s.cfg.AgentDir != "" {
		if cmd, dir, ok := try(filepath.Join(s.cfg.AgentDir, "dist", "index.js")); ok {
			return cmd, dir, nil
		}
	}

	// 3) 相对 cwd（`go run ./cmd/picgo-web` 时 cwd 在 server/）
	if cmd, dir, ok := try(filepath.Join("picgo-agent", "dist", "index.js")); ok {
		return cmd, dir, nil
	}
	if cmd, dir, ok := try(filepath.Join("..", "picgo-agent", "dist", "index.js")); ok {
		return cmd, dir, nil
	}

	// 4) 相对可执行文件位置
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		if cmd, dir, ok := try(filepath.Join(exeDir, "..", "picgo-agent", "dist", "index.js")); ok {
			return cmd, dir, nil
		}
		if cmd, dir, ok := try(filepath.Join(exeDir, "..", "..", "picgo-agent", "dist", "index.js")); ok {
			return cmd, dir, nil
		}
	}

	// 5) 容器镜像固定路径
	if cmd, dir, ok := try(filepath.Join(string(filepath.Separator), "app", "picgo-agent", "dist", "index.js")); ok {
		return cmd, dir, nil
	}

	// 6) PATH
	if p, err := exec.LookPath("picgo-agent"); err == nil {
		return []string{p}, "", nil
	}

	return nil, "", fmt.Errorf(
		"未找到 picgo-agent 入口文件（已尝试：%s）。\n"+
			"解决办法任选其一：\n"+
			"  · 构建侧车并在仓库内启动：make build-agent && make dev\n"+
			"  · 显式指定路径：PICGO_WEB_AGENT_DIR=/path/to/picgo-agent\n"+
			"  · 自行启动 agent：PICGO_WEB_AGENT_AUTOSTART=false + node picgo-agent/dist/index.js\n"+
			"    （注意让两侧使用同一令牌：PICGO_AGENT_TOKEN=$(cat <dataDir>/agent-token.txt)）",
		strings.Join(tried, "\n    "),
	)
}

func (s *Supervisor) notify(up bool, err error) {
	if s.onStateChange != nil {
		s.onStateChange(up, err)
	}
}

// ------------------------------------------------------------------ 令牌

// resolveToken 按 env → 文件 → 新生成的顺序解析共享令牌。
func resolveToken(fromEnv, tokenFile string) (token string, generated bool, err error) {
	if t := strings.TrimSpace(fromEnv); t != "" {
		return t, false, nil
	}
	if strings.TrimSpace(tokenFile) == "" {
		return "", false, errors.New("agent: AgentToken 为空且未提供 TokenFile，无法生成共享令牌")
	}

	if raw, readErr := os.ReadFile(tokenFile); readErr == nil {
		if t := strings.TrimSpace(string(raw)); t != "" {
			return t, false, nil
		}
	} else if !os.IsNotExist(readErr) {
		return "", false, fmt.Errorf("读取 agent 令牌文件 %s 失败: %w", tokenFile, readErr)
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", false, fmt.Errorf("生成 agent 令牌失败: %w", err)
	}
	t := hex.EncodeToString(buf)

	if dir := filepath.Dir(tokenFile); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", false, fmt.Errorf("创建 agent 令牌目录失败: %w", err)
		}
	}
	if err := os.WriteFile(tokenFile, []byte(t+"\n"), 0o600); err != nil {
		return "", false, fmt.Errorf("写入 agent 令牌文件 %s 失败: %w", tokenFile, err)
	}
	return t, true, nil
}

// agentRootDir 从 `<agentDir>/dist/index.js` 推出 `<agentDir>`。
//
// 若入口不在 `dist/` 下（自定义构建），退化为其所在目录。
func agentRootDir(entryAbs string) string {
	dir := filepath.Dir(entryAbs)
	if filepath.Base(dir) == "dist" {
		return filepath.Dir(dir)
	}
	return dir
}

// maskTokenPrefix 只保留令牌前 8 位用于人工核对。
func maskTokenPrefix(token string) string {
	if len(token) <= 8 {
		return "******"
	}
	return token[:8] + "…"
}

// splitHostPort 从 BaseURL 解析出 host 与 port，供 agent 监听使用。
func splitHostPort(baseURL string) (host, port string) {
	s := strings.TrimSpace(baseURL)
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "https://")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, "36678"
}

// logLevelFromEnv 把 Go 的日志级别映射到 agent 的级别。
func logLevelFromEnv() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PICGO_WEB_LOG_LEVEL"))) {
	case "debug":
		return "debug"
	case "warn", "warning":
		return "warn"
	case "error":
		return "error"
	default:
		return "info"
	}
}
