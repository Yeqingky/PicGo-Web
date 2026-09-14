package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// Client 是 Go 侧对 picgo-agent 的全部依赖。
//
// 定义成接口（而不是具体类型）是为了让上层能注入 **MockClient**：
// 前端联调、无 Node 环境的 CI、以及单测都不需要真的跑一个 Node 进程
// （`PICGO_WEB_AGENT_MOCK=true`，见 mock.go）。
type Client interface {
	// ---- 健康与生命周期 ----
	Healthz(ctx context.Context) (*HealthzData, error)
	Shutdown(ctx context.Context) error

	// ---- 配置（picgo 原生结构）----
	GetConfig(ctx context.Context) (RawConfig, error)
	PutConfig(ctx context.Context, cfg RawConfig) error
	PatchConfig(ctx context.Context, patch map[string]any) (*PatchConfigResult, error)

	// ---- 图床与配置表单 ----
	ListUploaders(ctx context.Context) (*UploadersData, error)
	UploaderSchema(ctx context.Context, typ string, answers map[string]any) (*UploaderSchemaData, error)
	TestUploader(ctx context.Context, in TestUploaderInput) (*TestUploaderData, error)
	ListUploaderConfigs(ctx context.Context, typ string) (*UploaderConfigsData, error)
	CreateOrUpdateUploaderConfig(ctx context.Context, in CreateUploaderConfigInput) (*UploaderConfigResult, error)
	DeleteUploaderConfig(ctx context.Context, typ, configName string) error
	UseUploader(ctx context.Context, typ, configName string) error
	ListTransformers(ctx context.Context) (*TransformersData, error)

	// ---- 插件 ----
	ListPlugins(ctx context.Context) (*PluginsData, error)
	PluginReadme(ctx context.Context, name string) (*PluginReadmeData, error)
	InstallPlugins(ctx context.Context, names []string) (string, error)
	UninstallPlugins(ctx context.Context, names []string) (string, error)
	UpdatePlugins(ctx context.Context, names []string) (string, error)
	SetPluginEnabled(ctx context.Context, name string, enabled bool) error

	// ---- 上传与删除 ----
	Upload(ctx context.Context, req UploadRequest) (*UploadData, error)
	DeleteRemote(ctx context.Context, req RemoveRequest) (*RemoveData, error)

	// ---- 任务与日志 ----
	ListJobs(ctx context.Context) (*JobsData, error)
	GetJob(ctx context.Context, uid string) (*JobInfo, error)
	DeleteJob(ctx context.Context, uid string) error
	TailLogs(ctx context.Context, n int) (*LogsData, error)

	// ---- SSE ----
	// EventsURL 返回 SSE 端点地址（供 events.Bridge 连接）。
	EventsURL() string
	// Token 返回共享令牌（Bridge 连接 SSE 时需要）。
	Token() string
}

// Error 是调用 agent 失败的错误，携带**已映射的业务错误码**。
//
// 上层（service）用 ToServiceError 把它转成 `service.Error`，
// 这样 handler 的 respondServiceError 能给出正确的 HTTP 状态码。
type Error struct {
	Code    response.Code
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code.Message()
}

// Unwrap 支持 errors.Is / errors.As 追溯。
func (e *Error) Unwrap() error { return e.Cause }

// NewError 构造 agent 调用错误。
func NewError(code response.Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

// CodeOf 提取 agent 错误的业务码；非 agent 错误返回 CodeOK。
func CodeOf(err error) response.Code {
	if err == nil {
		return response.CodeOK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return response.CodeOK
}

// MessageOf 提取可安全外传的消息。
func MessageOf(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		if e.Message != "" {
			return e.Message
		}
		return e.Code.Message()
	}
	return response.CodeInternal.Message()
}

// IsUnavailable 判断错误是否为「agent 不可用」（连接失败/超时）。
func IsUnavailable(err error) bool {
	return CodeOf(err) == response.CodeAgentUnavailable
}

// httpClient 是基于 net/http 的 Client 实现。
type httpClient struct {
	baseURL         string
	token           string
	log             Logger
	http            *http.Client
	requestTimeout  time.Duration
	uploadTimeout   time.Duration
	uploadTimeoutFn UploadTimeoutFunc
}

// New 构造真实的 HTTP 客户端。
func New(cfg Config) Client {
	reqTimeout := cfg.RequestTimeout
	if reqTimeout <= 0 {
		reqTimeout = DefaultRequestTimeout
	}
	upTimeout := cfg.UploadTimeout
	if upTimeout <= 0 {
		upTimeout = DefaultUploadTimeout
	}
	return &httpClient{
		baseURL:         strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/"),
		token:           cfg.Token,
		log:             cfg.Log,
		http:            &http.Client{},
		requestTimeout:  reqTimeout,
		uploadTimeout:   upTimeout,
		uploadTimeoutFn: cfg.UploadTimeoutFn,
	}
}

// NewWithHTTPClient 允许注入自定义 http.Client（测试用）。
func NewWithHTTPClient(cfg Config, hc *http.Client) Client {
	c := New(cfg)
	if hc != nil {
		c.(*httpClient).http = hc
	}
	return c
}

// EventsURL 实现 Client。
func (c *httpClient) EventsURL() string { return c.baseURL + "/api/events" }

// Token 实现 Client。
func (c *httpClient) Token() string { return c.token }

// ---------------------------------------------------------------- 请求内核

// call 执行一次请求并把 `Data` 反序列化到 out（out 为 nil 时忽略）。
//
// 错误映射（见 docs/API.md §13）：
//
//	网络/连接失败        → CodeAgentUnavailable（503）
//	HTTP 401             → CodeAgentUnavailable（令牌不匹配，等同于没法用）
//	Code = ERR_PARAM     → CodeInvalidParam（调用方错误）
//	Code = ERR_NOT_FOUND → CodeNotFound
//	Code = ERR_PICGO     → 由 withCode 指定的业务码（默认 CodeUploadFailed）
//	Code = ERR_INTERNAL  → CodeInternal
func (c *httpClient) call(ctx context.Context, method, path string, body any, timeout time.Duration, errCode response.Code, out any) error {
	if timeout <= 0 {
		timeout = c.requestTimeout
	}
	if errCode == 0 {
		errCode = response.CodeUploadFailed
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return NewError(response.CodeInternal, "序列化请求体失败", err)
		}
		reader = bytes.NewReader(buf)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(reqCtx, method, url, reader)
	if err != nil {
		return NewError(response.CodeInternal, "构造请求失败", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("X-Agent-Token", c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// 区分超时与其他网络错误，但对调用方都是「agent 不可用」。
		//
		// 消息里**带上底层原因**：否则用户只看到「内核不可用」而无法判断
		// 是「没启动」「端口不对」还是「令牌不匹配」（这三种最常见）。
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return NewError(response.CodeAgentUnavailable,
				fmt.Sprintf("内核响应超时（%s，超过 %s）", c.baseURL, timeout), err)
		case errors.Is(err, syscall.ECONNREFUSED):
			return NewError(response.CodeAgentUnavailable,
				fmt.Sprintf("内核未启动或端口未监听（%s）：请检查 PICGO_WEB_AGENT_AUTOSTART / PICGO_WEB_AGENT_URL", c.baseURL), err)
		default:
			return NewError(response.CodeAgentUnavailable,
				fmt.Sprintf("无法连接内核（%s）：%v", c.baseURL, err), err)
		}
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20)) // 32 MiB 上限，防异常大响应
	if err != nil {
		return NewError(response.CodeAgentUnavailable, "读取内核响应失败", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return NewError(response.CodeAgentUnavailable,
			"内核拒绝了请求（X-Agent-Token 不匹配）", nil)
	}

	// agent 的 /healthz 不带信封
	if path == "/healthz" {
		if resp.StatusCode >= 400 {
			return NewError(response.CodeAgentUnavailable,
				fmt.Sprintf("内核健康检查失败（HTTP %d）", resp.StatusCode), nil)
		}
		if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				return NewError(response.CodeAgentUnavailable, "内核健康检查响应无法解析", err)
			}
		}
		return nil
	}

	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return NewError(response.CodeAgentUnavailable,
			fmt.Sprintf("内核响应不是合法 JSON（HTTP %d）", resp.StatusCode), err)
	}

	if env.Code != "" && env.Code != CodeOK {
		code := errCode
		switch env.Code {
		case CodeErrParam:
			code = response.CodeInvalidParam
		case CodeErrNotFound:
			code = response.CodeNotFound
		case CodeErrInternal:
			code = response.CodeInternal
		case CodeErrPicgo:
			// 用调用方语义（上传失败 / 插件失败 / …）
		}
		return NewError(code, env.Message, nil)
	}

	if resp.StatusCode >= 400 {
		return NewError(response.CodeAgentUnavailable,
			fmt.Sprintf("内核返回 HTTP %d：%s", resp.StatusCode, strings.TrimSpace(env.Message)), nil)
	}

	if out == nil {
		return nil
	}
	// 把 Data 重新编码再解码到目标类型（Data 是 any，直接断言不可靠）
	buf, err := json.Marshal(env.Data)
	if err != nil {
		return NewError(response.CodeInternal, "重新编码内核响应失败", err)
	}
	if err := json.Unmarshal(buf, out); err != nil {
		return NewError(response.CodeInternal, "内核响应结构与预期不符", err)
	}
	return nil
}

// callNoBody 简化「只要成功/失败」的调用。
func (c *httpClient) callNoBody(ctx context.Context, method, path string, errCode response.Code) error {
	return c.call(ctx, method, path, nil, c.requestTimeout, errCode, nil)
}

// ---------------------------------------------------------------- 健康

// Healthz 实现 Client。
func (c *httpClient) Healthz(ctx context.Context) (*HealthzData, error) {
	var out HealthzData
	// 健康检查要快：固定 2s，与 Go 侧探测约定一致
	if err := c.call(ctx, http.MethodGet, "/healthz", nil, 2*time.Second, response.CodeAgentUnavailable, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Shutdown 实现 Client。
func (c *httpClient) Shutdown(ctx context.Context) error {
	return c.call(ctx, http.MethodPost, "/api/shutdown", nil, 5*time.Second, response.CodeAgentUnavailable, nil)
}

// ---------------------------------------------------------------- 配置

// GetConfig 实现 Client。
func (c *httpClient) GetConfig(ctx context.Context) (RawConfig, error) {
	var out RawConfig
	if err := c.call(ctx, http.MethodGet, "/api/config", nil, 0, response.CodeInternal, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PutConfig 实现 Client。
func (c *httpClient) PutConfig(ctx context.Context, cfg RawConfig) error {
	body := map[string]any{"Config": cfg}
	return c.call(ctx, http.MethodPut, "/api/config", body, 0, response.CodeInternal, nil)
}

// PatchConfig 实现 Client（点路径合并，D22：只覆盖我们管辖的键）。
func (c *httpClient) PatchConfig(ctx context.Context, patch map[string]any) (*PatchConfigResult, error) {
	var out PatchConfigResult
	body := PatchConfigInput{Patch: patch}
	if err := c.call(ctx, http.MethodPatch, "/api/config", body, 0, response.CodeInternal, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------- 图床

// ListUploaders 实现 Client。
func (c *httpClient) ListUploaders(ctx context.Context) (*UploadersData, error) {
	var out UploadersData
	if err := c.call(ctx, http.MethodGet, "/api/uploaders", nil, 0, response.CodeInternal, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UploaderSchema 实现 Client（DependsOn 联动重求值）。
func (c *httpClient) UploaderSchema(ctx context.Context, typ string, answers map[string]any) (*UploaderSchemaData, error) {
	var out UploaderSchemaData
	body := map[string]any{"Type": typ, "Answers": answers}
	if err := c.call(ctx, http.MethodPost, "/api/uploaders/schema", body, 0, response.CodeInvalidParam, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TestUploader 实现 Client（连通性测试）。
func (c *httpClient) TestUploader(ctx context.Context, in TestUploaderInput) (*TestUploaderData, error) {
	var out TestUploaderData
	body := map[string]any{"Type": in.Type}
	if in.ConfigName != "" {
		body["ConfigName"] = in.ConfigName
	}
	if in.Config != nil {
		body["Config"] = in.Config
	}
	if in.Answers != nil {
		body["Answers"] = in.Answers
	}
	// 测试要真上传一个小文件，给足时间
	if err := c.call(ctx, http.MethodPost, "/api/uploaders/test", body, c.uploadTimeout, response.CodeAgentUnavailable, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListUploaderConfigs 实现 Client。
func (c *httpClient) ListUploaderConfigs(ctx context.Context, typ string) (*UploaderConfigsData, error) {
	var out UploaderConfigsData
	path := "/api/uploaders/configs?type=" + urlQueryEscape(typ)
	if err := c.call(ctx, http.MethodGet, path, nil, 0, response.CodeInternal, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateOrUpdateUploaderConfig 实现 Client。
func (c *httpClient) CreateOrUpdateUploaderConfig(ctx context.Context, in CreateUploaderConfigInput) (*UploaderConfigResult, error) {
	var out UploaderConfigResult
	body := map[string]any{
		"Type":       in.Type,
		"ConfigName": in.ConfigName,
		"Config":     in.Config,
		"Activate":   in.Activate,
	}
	if err := c.call(ctx, http.MethodPost, "/api/uploaders/configs", body, 0, response.CodeInvalidParam, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteUploaderConfig 实现 Client。
func (c *httpClient) DeleteUploaderConfig(ctx context.Context, typ, configName string) error {
	path := "/api/uploaders/configs?type=" + urlQueryEscape(typ) + "&configName=" + urlQueryEscape(configName)
	return c.call(ctx, http.MethodDelete, path, nil, 0, response.CodeInternal, nil)
}

// UseUploader 实现 Client。
func (c *httpClient) UseUploader(ctx context.Context, typ, configName string) error {
	body := map[string]any{"Type": typ, "ConfigName": configName}
	return c.call(ctx, http.MethodPost, "/api/uploader/use", body, 0, response.CodeInvalidParam, nil)
}

// ListTransformers 实现 Client。
func (c *httpClient) ListTransformers(ctx context.Context) (*TransformersData, error) {
	var out TransformersData
	if err := c.call(ctx, http.MethodGet, "/api/transformers", nil, 0, response.CodeInternal, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------- 插件

// ListPlugins 实现 Client。
func (c *httpClient) ListPlugins(ctx context.Context) (*PluginsData, error) {
	var out PluginsData
	if err := c.call(ctx, http.MethodGet, "/api/plugins", nil, 0, response.CodeInternal, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PluginReadme 实现 Client。
func (c *httpClient) PluginReadme(ctx context.Context, name string) (*PluginReadmeData, error) {
	var out PluginReadmeData
	path := "/api/plugins/" + urlPathEscape(name) + "/readme"
	if err := c.call(ctx, http.MethodGet, path, nil, 0, response.CodeNotFound, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// InstallPlugins 实现 Client，返回 JobUID（异步任务）。
func (c *httpClient) InstallPlugins(ctx context.Context, names []string) (string, error) {
	return c.pluginJob(ctx, "/api/plugins/install", names)
}

// UninstallPlugins 实现 Client。
func (c *httpClient) UninstallPlugins(ctx context.Context, names []string) (string, error) {
	return c.pluginJob(ctx, "/api/plugins/uninstall", names)
}

// UpdatePlugins 实现 Client。
func (c *httpClient) UpdatePlugins(ctx context.Context, names []string) (string, error) {
	return c.pluginJob(ctx, "/api/plugins/update", names)
}

func (c *httpClient) pluginJob(ctx context.Context, path string, names []string) (string, error) {
	var out JobCreated
	body := map[string]any{"Names": names}
	// npm 安装可能很慢，给足时间（真正耗时在 agent 侧是异步的，这里只是启动任务）
	if err := c.call(ctx, http.MethodPost, path, body, c.uploadTimeout, response.CodePluginFailed, &out); err != nil {
		return "", err
	}
	return out.JobUID, nil
}

// SetPluginEnabled 实现 Client。
func (c *httpClient) SetPluginEnabled(ctx context.Context, name string, enabled bool) error {
	body := map[string]any{"Enabled": enabled}
	path := "/api/plugins/" + urlPathEscape(name)
	return c.call(ctx, http.MethodPatch, path, body, 0, response.CodePluginFailed, nil)
}

// ---------------------------------------------------------------- 上传/删除

// Upload 实现 Client（**单文件 + 同步**，D39）。
//
// 返回的错误语义：
//   - 目标图床不存在 → CodeInvalidParam（HTTP 400），调用方错误
//   - 上传本身失败   → CodeUploadFailed（agent 用 ERR_PICGO + HTTP 200 表达）
//   - agent 不可用   → CodeAgentUnavailable
func (c *httpClient) Upload(ctx context.Context, req UploadRequest) (*UploadData, error) {
	timeout := c.uploadTimeout
	if c.uploadTimeoutFn != nil {
		if d := c.uploadTimeoutFn(); d > 0 {
			timeout = d
		}
	}
	var out UploadData
	if err := c.call(ctx, http.MethodPost, "/api/upload", req, timeout, response.CodeUploadFailed, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteRemote 实现 Client（走 picgo 的 remove 事件约定，D47）。
//
// ⚠️ 注意：`Supported = false` **不是错误** —— 它表示该驱动没实现 remove，
// 上层应「只删本地记录」并如实告知用户。
func (c *httpClient) DeleteRemote(ctx context.Context, req RemoveRequest) (*RemoveData, error) {
	var out RemoveData
	// 插件实现是 async 且 emit 无返回值，agent 需要等一小段超时再判定
	if err := c.call(ctx, http.MethodPost, "/api/delete", req, 60*time.Second, response.CodeInternal, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------- 任务与日志

// ListJobs 实现 Client。
func (c *httpClient) ListJobs(ctx context.Context) (*JobsData, error) {
	var out JobsData
	if err := c.call(ctx, http.MethodGet, "/api/jobs", nil, 0, response.CodeInternal, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetJob 实现 Client。
func (c *httpClient) GetJob(ctx context.Context, uid string) (*JobInfo, error) {
	var out JobInfo
	path := "/api/jobs/" + urlPathEscape(uid)
	if err := c.call(ctx, http.MethodGet, path, nil, 0, response.CodeNotFound, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteJob 实现 Client。
func (c *httpClient) DeleteJob(ctx context.Context, uid string) error {
	path := "/api/jobs/" + urlPathEscape(uid)
	return c.call(ctx, http.MethodDelete, path, nil, 0, response.CodeInternal, nil)
}

// TailLogs 实现 Client（读 picgo.log 尾部，只读不解析）。
func (c *httpClient) TailLogs(ctx context.Context, n int) (*LogsData, error) {
	if n <= 0 {
		n = 200
	}
	var out LogsData
	path := fmt.Sprintf("/api/logs?tail=%d", n)
	if err := c.call(ctx, http.MethodGet, path, nil, 0, response.CodeInternal, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------- 助手

// urlQueryEscape 做最小编码（配置名可能含空格/中文）。
func urlQueryEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9',
			ch == '-', ch == '_', ch == '.', ch == '~', ch == '@', ch == '/':
			b.WriteByte(ch)
		default:
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}

// urlPathEscape 编码路径段（插件名可能含 @scope/name）。
func urlPathEscape(s string) string {
	// 插件名里的 "/"（scoped package）必须编码，否则会被当成路径分隔符
	return strings.ReplaceAll(urlQueryEscape(s), "/", "%2F")
}

// IsConnRefused 判断错误是否为「连接被拒绝」（agent 未启动）。
//
// 供上层给出更准确的提示（「内核未启动」而不是「内核不可用」）。
func IsConnRefused(err error) bool {
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return strings.Contains(strings.ToLower(netErr.Error()), "connection refused")
	}
	return false
}
