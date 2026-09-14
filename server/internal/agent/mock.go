package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// MockConfig 是 mock 客户端的行为配置。
type MockConfig struct {
	// BaseURL 用于 EventsURL()（mock 下不会被真正连接）。
	BaseURL string
	// Token 用于 Token()。
	Token string
	// Log 进程日志。
	Log Logger
	// TempDir 假装上传时「落盘」的目录；为空则用 os.TempDir()。
	TempDir string
	// CDNBase 生成假 URL 的前缀。
	CDNBase string
	// PicgoVersion 健康检查返回的版本。
	PicgoVersion string

	// FailPaths 含子串的路径一律上传失败（用于测「部分失败」）。
	//
	// 之所以按**路径**而不是按调用序号：按序号会让测试随并发时序抖动，
	// 按路径则完全确定。
	FailPaths []string
	// FailTargets 指定的 `Type` 一律上传失败（用于测「目标不可用」）。
	FailTargets []string
	// UploadDelay 每次上传的模拟耗时（0 表示立即返回）。
	UploadDelay time.Duration
	// RemoteDeleteSupported 远端删除是否「支持」（默认 false，符合大多数驱动）。
	RemoteDeleteSupported bool
	// Uploaders 自定义可用驱动列表；为空则用内置样例（github / smms）。
	Uploaders []UploaderInfo
}

// MockClient 是 Client 的内存实现。
//
// 用途：
//   - `PICGO_WEB_AGENT_MOCK=true`：无 Node 环境也能跑通全链路（前端联调、CI）
//   - 单元测试：注入确定的成功/失败行为
//
// **不依赖任何外部进程**，且状态是进程内的（重启即清空），
// 因此**不得**用于生产。
type MockClient struct {
	cfg MockConfig

	mu       sync.RWMutex
	configs  map[string]map[string]RawConfig // type -> configName -> raw config
	used     map[string]string               // type -> 当前激活的 configName
	jobs     map[string]*JobInfo
	patchLog []string
	// lastRemove 记录被交回删除的 IImgInfo，供测试断言字段名未被转换。
	lastRemove []RawImgInfo
}

// NewMock 构造 mock 客户端。
func NewMock(cfg MockConfig) *MockClient {
	if cfg.CDNBase == "" {
		cfg.CDNBase = "https://mock.picgo-web.local"
	}
	if cfg.PicgoVersion == "" {
		cfg.PicgoVersion = "3.0.2"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://127.0.0.1:36678"
	}
	if cfg.TempDir == "" {
		cfg.TempDir = filepath.Join(os.TempDir(), "picgo-web-mock-agent")
	}
	_ = os.MkdirAll(cfg.TempDir, 0o755)

	m := &MockClient{
		cfg:     cfg,
		configs: make(map[string]map[string]RawConfig),
		used:    make(map[string]string),
		jobs:    make(map[string]*JobInfo),
	}
	// 预置一条 github 配置，让「不显式建配置就能上传」的用例也能跑
	m.configs["github"] = map[string]RawConfig{
		"Default": {"_id": "st_mock_github_default", "_configName": "Default", "repo": "mock/repo", "path": "img/"},
	}
	m.used["github"] = "Default"
	return m
}

// EventsURL 实现 Client。
func (m *MockClient) EventsURL() string { return m.cfg.BaseURL + "/api/events" }

// Token 实现 Client。
func (m *MockClient) Token() string { return m.cfg.Token }

// Healthz 实现 Client。
func (m *MockClient) Healthz(context.Context) (*HealthzData, error) {
	return &HealthzData{
		Ok:           true,
		PicgoVersion: m.cfg.PicgoVersion,
		ConfigPath:   filepath.Join(m.cfg.TempDir, "config.json"),
		PID:          os.Getpid(),
	}, nil
}

// Shutdown 实现 Client。
func (m *MockClient) Shutdown(context.Context) error { return nil }

// GetConfig 实现 Client。
func (m *MockClient) GetConfig(context.Context) (RawConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	picBed := map[string]any{"uploader": m.currentTypeLocked(), "current": m.currentTypeLocked()}
	for typ, byName := range m.configs {
		if cfg, ok := byName[m.used[typ]]; ok {
			picBed[typ] = map[string]any(cfg)
		}
	}
	uploader := map[string]any{}
	for typ, byName := range m.configs {
		list := make([]any, 0, len(byName))
		for name, cfg := range byName {
			item := map[string]any(cfg)
			item["_configName"] = name
			list = append(list, item)
		}
		uploader[typ] = map[string]any{"configList": list, "defaultId": m.defaultIDLocked(typ)}
	}
	return RawConfig{
		"picBed":       picBed,
		"uploader":     uploader,
		"picgoPlugins": map[string]any{},
		"settings":     map[string]any{},
		// 模拟插件私有键：**必须被保留、不能被我们清掉**（D22 的回归点）
		"uploaded": []any{},
	}, nil
}

// PutConfig 实现 Client。
func (m *MockClient) PutConfig(_ context.Context, cfg RawConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 只保留我们管辖的键（与 agent 的真实行为一致）
	for _, key := range []string{"picBed", "uploader", "picgoPlugins", "settings"} {
		if _, ok := cfg[key]; !ok {
			return NewError(response.CodeInvalidParam,
				fmt.Sprintf("PUT /api/config 缺少受管键 %q，已拒绝（防毁插件状态）", key), nil)
		}
	}
	m.clearManagedLocked()
	if picBed, ok := cfg["picBed"].(map[string]any); ok {
		for k, v := range picBed {
			if sub, ok := v.(map[string]any); ok && k != "uploader" && k != "current" && k != "transformer" {
				raw := RawConfig{}
				for kk, vv := range sub {
					raw[kk] = vv
				}
				name, _ := raw["_configName"].(string)
				if name == "" {
					name = "Default"
				}
				if m.configs[k] == nil {
					m.configs[k] = map[string]RawConfig{}
				}
				m.configs[k][name] = raw
				m.used[k] = name
			}
		}
	}
	return nil
}

// PatchConfig 实现 Client（点路径合并，只覆盖受管键）。
func (m *MockClient) PatchConfig(_ context.Context, patch map[string]any) (*PatchConfigResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	applied := make([]string, 0, len(patch))
	for path := range patch {
		applied = append(applied, path)
	}
	m.patchLog = append(m.patchLog, applied...)
	return &PatchConfigResult{
		Applied: applied,
		// 插件私有键被原样保留 —— 这是 D22 的核心保证
		PreservedPluginKeys: []string{"uploaded"},
	}, nil
}

// PatchLog 返回曾被 PATCH 的点路径（测试用）。
func (m *MockClient) PatchLog() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, len(m.patchLog))
	copy(out, m.patchLog)
	return out
}

// ListUploaders 实现 Client。
func (m *MockClient) ListUploaders(context.Context) (*UploadersData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	defs := m.cfg.Uploaders
	if len(defs) == 0 {
		defs = defaultMockUploaders()
	}

	out := make([]UploaderInfo, 0, len(defs))
	for _, d := range defs {
		names := make([]string, 0, len(m.configs[d.Type]))
		for name := range m.configs[d.Type] {
			names = append(names, name)
		}
		d.ConfigNames = names
		out = append(out, d)
	}

	cur := CurrentUploader{Type: m.currentTypeLocked(), ConfigName: m.used[m.currentTypeLocked()]}
	return &UploadersData{Uploaders: out, Current: cur, Transformer: "path"}, nil
}

// UploaderSchema 实现 Client。
func (m *MockClient) UploaderSchema(_ context.Context, typ string, _ map[string]any) (*UploaderSchemaData, error) {
	for _, d := range m.uploaderDefs() {
		if d.Type == typ {
			return &UploaderSchemaData{Type: d.Type, Name: d.Name, Config: d.Config}, nil
		}
	}
	return nil, NewError(response.CodeNotFound, fmt.Sprintf("上传器类型 %q 不存在", typ), nil)
}

// TestUploader 实现 Client。
//
// 两种模式与真实 agent 一致：
//   - 传 `Config`：**不落盘**直接测（新建表单的场景）
//   - 只传 `ConfigName`：读**已保存的配置**再测（后台点「测试」的场景）
//
// 校验内容：必填字段非空。任一必填为空 → `Ok=false`（**不是错误**，
// 与真实 agent 的契约一致：测试本身执行成功，结果由 Ok 表达）。
func (m *MockClient) TestUploader(_ context.Context, in TestUploaderInput) (*TestUploaderData, error) {
	if _, err := m.UploaderSchema(context.Background(), in.Type, in.Answers); err != nil {
		return nil, err
	}

	cfg := in.Config
	if cfg == nil {
		// 只给了配置名 → 从已保存的配置里取（与真实 agent 行为对齐）
		name := strings.TrimSpace(in.ConfigName)
		if name == "" {
			m.mu.RLock()
			name = m.used[in.Type]
			m.mu.RUnlock()
		}
		m.mu.RLock()
		if stored, ok := m.configs[in.Type][name]; ok {
			cfg = RawConfig{}
			for k, v := range stored {
				cfg[k] = v
			}
		}
		m.mu.RUnlock()
	}
	if cfg == nil {
		return &TestUploaderData{Ok: false, Message: fmt.Sprintf("未找到类型 %s 的配置", in.Type)}, nil
	}

	for _, d := range m.uploaderDefs() {
		if d.Type != in.Type {
			continue
		}
		for _, f := range d.Config {
			if !f.Required {
				continue
			}
			if v, ok := cfg[f.Name]; !ok || strings.TrimSpace(fmt.Sprintf("%v", v)) == "" {
				return &TestUploaderData{Ok: false, Message: fmt.Sprintf("必填字段 %s 为空", f.Name)}, nil
			}
		}
	}
	return &TestUploaderData{Ok: true, Message: "连接成功", LatencyMs: 12}, nil
}

// ListUploaderConfigs 实现 Client。
func (m *MockClient) ListUploaderConfigs(_ context.Context, typ string) (*UploaderConfigsData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	byName, ok := m.configs[typ]
	if !ok {
		return nil, NewError(response.CodeNotFound, fmt.Sprintf("上传器类型 %q 不存在", typ), nil)
	}
	list := make([]RawConfig, 0, len(byName))
	for name, cfg := range byName {
		item := RawConfig{}
		for k, v := range cfg {
			item[k] = v
		}
		item["_configName"] = name
		list = append(list, item)
	}
	return &UploaderConfigsData{Type: typ, DefaultConfigName: m.used[typ], Configs: list}, nil
}

// CreateOrUpdateUploaderConfig 实现 Client。
func (m *MockClient) CreateOrUpdateUploaderConfig(_ context.Context, in CreateUploaderConfigInput) (*UploaderConfigResult, error) {
	if !m.hasUploaderType(in.Type) {
		return nil, NewError(response.CodeNotFound, fmt.Sprintf("上传器类型 %q 不存在", in.Type), nil)
	}
	name := strings.TrimSpace(in.ConfigName)
	if name == "" {
		name = "Default"
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.configs[in.Type] == nil {
		m.configs[in.Type] = map[string]RawConfig{}
	}
	cfg := RawConfig{}
	for k, v := range in.Config {
		cfg[k] = v
	}
	if _, ok := cfg["_id"]; !ok {
		cfg["_id"] = "st_mock_" + in.Type + "_" + strings.ToLower(name)
	}
	cfg["_configName"] = name
	m.configs[in.Type][name] = cfg
	if in.Activate || m.used[in.Type] == "" {
		m.used[in.Type] = name
	}
	return &UploaderConfigResult{Type: in.Type, Config: cfg}, nil
}

// DeleteUploaderConfig 实现 Client。
func (m *MockClient) DeleteUploaderConfig(_ context.Context, typ, configName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.configs[typ] == nil {
		return NewError(response.CodeNotFound, fmt.Sprintf("上传器类型 %q 不存在", typ), nil)
	}
	if _, ok := m.configs[typ][configName]; !ok {
		return NewError(response.CodeNotFound, fmt.Sprintf("配置 %q 不存在", configName), nil)
	}
	delete(m.configs[typ], configName)
	if m.used[typ] == configName {
		m.used[typ] = ""
		for name := range m.configs[typ] {
			m.used[typ] = name
			break
		}
	}
	return nil
}

// UseUploader 实现 Client。
func (m *MockClient) UseUploader(_ context.Context, typ, configName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.configs[typ] == nil {
		return NewError(response.CodeNotFound, fmt.Sprintf("上传器类型 %q 不存在", typ), nil)
	}
	if _, ok := m.configs[typ][configName]; !ok {
		return NewError(response.CodeNotFound,
			fmt.Sprintf("配置 %q 不存在（type=%s）", configName, typ), nil)
	}
	m.used[typ] = configName
	return nil
}

// ListTransformers 实现 Client。
func (m *MockClient) ListTransformers(context.Context) (*TransformersData, error) {
	return &TransformersData{
		Current:      "path",
		Transformers: []TransformerInfo{{Type: "path", Name: "path"}, {Type: "base64", Name: "base64"}},
	}, nil
}

// ListPlugins 实现 Client。
func (m *MockClient) ListPlugins(context.Context) (*PluginsData, error) {
	return &PluginsData{Plugins: []PluginInfo{}, Disabled: []string{}}, nil
}

// PluginReadme 实现 Client。
func (m *MockClient) PluginReadme(_ context.Context, name string) (*PluginReadmeData, error) {
	return &PluginReadmeData{Content: "# " + name, Path: "README.md"}, nil
}

// InstallPlugins 实现 Client。
func (m *MockClient) InstallPlugins(_ context.Context, names []string) (string, error) {
	return m.newJob("plugin.install", names), nil
}

// UninstallPlugins 实现 Client。
func (m *MockClient) UninstallPlugins(_ context.Context, names []string) (string, error) {
	return m.newJob("plugin.uninstall", names), nil
}

// UpdatePlugins 实现 Client。
func (m *MockClient) UpdatePlugins(_ context.Context, names []string) (string, error) {
	return m.newJob("plugin.update", names), nil
}

// SetPluginEnabled 实现 Client。
func (m *MockClient) SetPluginEnabled(context.Context, string, bool) error { return nil }

// Upload 实现 Client（单文件 + 同步）。
//
// 行为矩阵：
//   - 目标类型/配置不存在 → CodeInvalidParam（与真实 agent 一致）
//   - 路径命中 FailPaths    → CodeUploadFailed（模拟图床报错）
//   - 类型命中 FailTargets  → CodeUploadFailed
//   - 其余                  → 成功，返回确定的假 URL
func (m *MockClient) Upload(ctx context.Context, req UploadRequest) (*UploadData, error) {
	if m.cfg.UploadDelay > 0 {
		select {
		case <-time.After(m.cfg.UploadDelay):
		case <-ctx.Done():
			return nil, NewError(response.CodeAgentUnavailable, "上传被取消", ctx.Err())
		}
	}

	typ := ""
	configName := ""
	if req.Uploader != nil {
		typ = req.Uploader.Type
		configName = req.Uploader.ConfigName
	}
	if typ == "" {
		m.mu.RLock()
		typ = m.currentTypeLocked()
		configName = m.used[typ]
		m.mu.RUnlock()
	}

	// 目标校验（与真实 agent 的前置校验等价）
	m.mu.RLock()
	byName, ok := m.configs[typ]
	if !ok {
		m.mu.RUnlock()
		return nil, NewError(response.CodeInvalidParam,
			fmt.Sprintf("Uploader type %q not found", typ), nil)
	}
	if configName == "" {
		configName = m.used[typ]
	}
	if _, ok := byName[configName]; !ok {
		m.mu.RUnlock()
		return nil, NewError(response.CodeInvalidParam,
			fmt.Sprintf("Uploader config %q not found for type %q", configName, typ), nil)
	}
	m.mu.RUnlock()

	for _, t := range m.cfg.FailTargets {
		if t == typ {
			return nil, NewError(response.CodeUploadFailed,
				fmt.Sprintf("mock：目标 %s 被配置为必然失败", typ), nil)
		}
	}
	for _, p := range m.cfg.FailPaths {
		if p != "" && strings.Contains(req.Path, p) {
			return nil, NewError(response.CodeUploadFailed,
				fmt.Sprintf("mock：图床返回 422（路径命中 %q）", p), nil)
		}
	}

	// 校验源文件真的存在 —— 这样「本地暂存文件丢失」的场景也能被测出来
	info, err := os.Stat(req.Path)
	if err != nil {
		return nil, NewError(response.CodeUploadFailed,
			fmt.Sprintf("mock：源文件不可读（%v）", err), nil)
	}

	base := filepath.Base(req.Path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	fileName := base
	if req.FileName != "" {
		fileName = req.FileName
	}

	// 模拟魔法路径：把 PathTemplate 里的变量做最小替换（仅用于断言「模板被透传了」）
	remotePath := ""
	if req.PathTemplate != "" && (req.SupportsPathTemplate == nil || *req.SupportsPathTemplate) {
		remotePath = expandMockTemplate(req.PathTemplate, stem, ext, req.UserUID)
	}
	if req.FileTemplate != "" {
		fileName = expandMockTemplate(req.FileTemplate, stem, ext, req.UserUID)
		fileName = strings.ReplaceAll(fileName, "{filename}", stem)
	}

	url := m.cfg.CDNBase + "/" + typ + "/" + configName
	if remotePath != "" {
		url += "/" + strings.Trim(remotePath, "/")
	}
	url += "/" + fileName

	// 模拟插件回写的附加字段（sha）—— 删除远端时必须原样交回（D47）
	raw := RawImgInfo{
		"fileName":    fileName,
		"imgUrl":      url,
		"extname":     ext,
		"width":       800,
		"height":      600,
		"size":        info.Size(),
		"contentType": guessContentType(ext),
		"type":        typ,
		"origin":      req.Path,
		"sha":         "mock-sha-" + stem,
	}

	return &UploadData{
		Seq:          req.Seq,
		URL:          url,
		FileName:     fileName,
		Extname:      ext,
		Width:        800,
		Height:       600,
		Size:         info.Size(),
		ContentType:  guessContentType(ext),
		UploaderType: typ,
		Raw:          raw,
	}, nil
}

// DeleteRemote 实现 Client。
func (m *MockClient) DeleteRemote(_ context.Context, req RemoveRequest) (*RemoveData, error) {
	if !m.cfg.RemoteDeleteSupported {
		return &RemoveData{
			RemoteDeleted: false,
			Supported:     false,
			Message:       "该驱动不支持远端删除（没有插件实现 remove 事件）",
		}, nil
	}
	// 记录被交回的 IImgInfo：测试会断言字段名是 picgo 原生的（含 sha）
	m.mu.Lock()
	m.lastRemove = append(m.lastRemove, req.Items...)
	m.mu.Unlock()

	return &RemoveData{
		RemoteDeleted: true,
		Supported:     true,
		Message:       fmt.Sprintf("成功同步删除 %d 个文件", len(req.Items)),
	}, nil
}

// LastRemovedItems 返回最近一次被交回删除的 IImgInfo（测试用）。
func (m *MockClient) LastRemovedItems() []RawImgInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]RawImgInfo, len(m.lastRemove))
	copy(out, m.lastRemove)
	return out
}

// ListJobs 实现 Client。
func (m *MockClient) ListJobs(context.Context) (*JobsData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]JobInfo, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, *j)
	}
	return &JobsData{Jobs: out}, nil
}

// GetJob 实现 Client。
func (m *MockClient) GetJob(_ context.Context, uid string) (*JobInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	j, ok := m.jobs[uid]
	if !ok {
		return nil, NewError(response.CodeNotFound, "任务不存在", nil)
	}
	cp := *j
	return &cp, nil
}

// DeleteJob 实现 Client。
func (m *MockClient) DeleteJob(_ context.Context, uid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.jobs, uid)
	return nil
}

// TailLogs 实现 Client。
func (m *MockClient) TailLogs(_ context.Context, n int) (*LogsData, error) {
	return &LogsData{
		Path:  filepath.Join(m.cfg.TempDir, "picgo.log"),
		Lines: []string{"[PicGo INFO] mock agent started"},
		Total: 1,
	}, nil
}

// ---- 内部 ----

func (m *MockClient) newJob(kind string, names []string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	uid := fmt.Sprintf("job_mock_%s_%d", kind, len(m.jobs)+1)
	payload := map[string]any{}
	if len(names) > 0 {
		payload["Names"] = names
	}
	m.jobs[uid] = &JobInfo{
		UID:       uid,
		Kind:      kind,
		Status:    "succeeded",
		Progress:  100,
		Payload:   payload,
		CreatedAt: time.Now().Unix(),
	}
	return uid
}

func (m *MockClient) currentTypeLocked() string {
	// 优先返回显式 use 过的类型，否则取第一个
	for typ, name := range m.used {
		if name != "" {
			if _, ok := m.configs[typ]; ok {
				return typ
			}
		}
	}
	for typ := range m.configs {
		return typ
	}
	return ""
}

func (m *MockClient) defaultIDLocked(typ string) string {
	if name := m.used[typ]; name != "" {
		if cfg, ok := m.configs[typ][name]; ok {
			if id, ok := cfg["_id"].(string); ok {
				return id
			}
		}
	}
	return ""
}

func (m *MockClient) clearManagedLocked() {
	m.configs = make(map[string]map[string]RawConfig)
	m.used = make(map[string]string)
}

func (m *MockClient) hasUploaderType(typ string) bool {
	for _, d := range m.uploaderDefs() {
		if d.Type == typ {
			return true
		}
	}
	return false
}

func (m *MockClient) uploaderDefs() []UploaderInfo {
	if len(m.cfg.Uploaders) > 0 {
		return m.cfg.Uploaders
	}
	return defaultMockUploaders()
}

// defaultMockUploaders 返回与内置驱动结构一致的样例（字段名照抄真实驱动）。
func defaultMockUploaders() []UploaderInfo {
	return []UploaderInfo{
		{
			Type: "github", Name: "GitHub", Builtin: true,
			Config: []DriverConfigField{
				{Name: "repo", Type: "input", Required: true, Alias: "仓库名", Message: "格式 username/reponame"},
				{Name: "branch", Type: "input", Required: true, Alias: "分支名", Default: "master"},
				{Name: "token", Type: "password", Required: true, Alias: "Token"},
				{Name: "path", Type: "input", Required: false, Alias: "存储路径"},
				{Name: "customUrl", Type: "input", Required: false, Alias: "自定义域名"},
			},
			Capabilities: Capabilities{
				SupportsPathTemplate: true,
				SupportsRemoteDelete: true,
				ConfigFields:         []string{"repo", "branch", "token", "path", "customUrl"},
				PathFieldNames:       []string{"path"},
				PicgoVersion:         "3.0.2",
			},
		},
		{
			Type: "smms", Name: "SM.MS", Builtin: true,
			Config: []DriverConfigField{
				{Name: "token", Type: "password", Required: true, Alias: "API Token"},
				{Name: "backupDomain", Type: "input", Required: false, Alias: "备用域名"},
			},
			Capabilities: Capabilities{
				SupportsPathTemplate: false,
				SupportsRemoteDelete: false,
				ConfigFields:         []string{"token", "backupDomain"},
				PathFieldNames:       []string{},
				PicgoVersion:         "3.0.2",
			},
		},
		{
			Type: "webdav", Name: "WebDAV", Builtin: true,
			Config: []DriverConfigField{
				{Name: "url", Type: "input", Required: true, Alias: "服务器地址"},
				{Name: "username", Type: "input", Required: true, Alias: "用户名"},
				{Name: "password", Type: "password", Required: true, Alias: "密码"},
				{Name: "path", Type: "input", Required: false, Alias: "存储路径", Default: "/"},
			},
			Capabilities: Capabilities{
				SupportsPathTemplate: true,
				SupportsRemoteDelete: true,
				ConfigFields:         []string{"url", "username", "password", "path"},
				PathFieldNames:       []string{"path"},
				PicgoVersion:         "3.0.2",
			},
		},
	}
}

// expandMockTemplate 做最小模板替换，覆盖 D70 的常用变量。
//
// ⚠️ 真实实现（agent 侧）在 picgo 的 beforeUploadPlugins 钩子里做，变量集与语义
// 以其为准；这里的 mock 只需要「模板被透传且产出了可预期的字符串」。
func expandMockTemplate(tpl, stem, ext, userUID string) string {
	now := time.Now()
	repl := strings.NewReplacer(
		"{Y}", now.Format("2006"),
		"{m}", now.Format("01"),
		"{d}", now.Format("02"),
		"{H}", now.Format("15"),
		"{i}", now.Format("04"),
		"{s}", now.Format("05"),
		"{timestamp}", fmt.Sprintf("%d", now.Unix()),
		"{filename}", stem,
		"{md5}", mockHash(stem),
		"{md5-8}", mockHash(stem)[:8],
		"{sha256-8}", mockHash(stem)[:8],
		"{uid}", userUID,
		"{uniqid}", "mockuniq",
		"{extname}", ext,
	)
	return repl.Replace(tpl)
}

func mockHash(s string) string {
	// 固定的伪哈希，保证测试可断言
	const hex = "0123456789abcdef"
	out := make([]byte, 32)
	for i := range out {
		out[i] = hex[(int(s[i%max(len(s), 1)])+i)%16]
	}
	return string(out)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func guessContentType(ext string) string {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/x-icon"
	case ".avif":
		return "image/avif"
	default:
		return "application/octet-stream"
	}
}
