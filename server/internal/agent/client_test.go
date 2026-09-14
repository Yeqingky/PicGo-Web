package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// newTestAgent 起一个假的 agent HTTP 服务，并把请求记录下来供断言。
func newTestAgent(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, Client) {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)

	client := New(Config{
		BaseURL: srv.URL,
		Token:   "test-token",
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return srv, client
}

func writeOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Envelope{Code: CodeOK, Message: "ok", Data: data})
}

// TestClientSendsAgentToken 断言每个请求都带 `X-Agent-Token`。
func TestClientSendsAgentToken(t *testing.T) {
	var got string
	_, client := newTestAgent(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Agent-Token")
		writeOK(w, map[string]any{"Uploaders": []any{}})
	})

	_, err := client.ListUploaders(context.Background())
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if got != "test-token" {
		t.Fatalf("X-Agent-Token 期望 test-token，实际 %q", got)
	}
}

// TestClientMapsErrorCodes 断言 agent 的字符串码被映射成**正确的业务码**。
//
// 注意 ERR_PICGO 是「业务失败」的通用码，具体映射成哪个错误码由**调用点**决定
// （上传 → CodeUploadFailed，配置类 → CodeInternal），因为只有调用方知道
// 「这个失败意味着什么」。这里用 ListUploaders 验证默认路径。
func TestClientMapsErrorCodes(t *testing.T) {
	cases := []struct {
		agentCode string
		httpCode  int
		want      response.Code
	}{
		{CodeErrParam, http.StatusOK, response.CodeInvalidParam},
		{CodeErrNotFound, http.StatusOK, response.CodeNotFound},
		{CodeErrInternal, http.StatusOK, response.CodeInternal},
		// ERR_PICGO 的业务码由**调用点**决定（上传 → 50003；列表 → 50001），
		// 因此这里用 ListUploaders 时映射到 CodeInternal。
		{CodeErrPicgo, http.StatusOK, response.CodeInternal},
	}
	for _, tc := range cases {
		t.Run(tc.agentCode, func(t *testing.T) {
			_, client := newTestAgent(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.httpCode)
				_ = json.NewEncoder(w).Encode(Envelope{Code: tc.agentCode, Message: "boom"})
			})

			_, err := client.ListUploaders(context.Background())
			if err == nil {
				t.Fatal("应返回错误")
			}
			if got := CodeOf(err); got != tc.want {
				t.Fatalf("错误码映射不符：期望 %d，实际 %d（%v）", tc.want, got, err)
			}
			if MessageOf(err) != "boom" {
				t.Fatalf("消息应透传 agent 的 Message，实际 %q", MessageOf(err))
			}
		})
	}
}

// TestClientUnauthorizedMeansUnavailable 断言 401 被映射为「内核不可用」。
//
// 理由：agent 拒绝请求（令牌不匹配）对调用方而言等价于「这个内核用不了」，
// 比报「参数错」更贴近事实。
func TestClientUnauthorizedMeansUnavailable(t *testing.T) {
	_, client := newTestAgent(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	_, err := client.ListUploaders(context.Background())
	if got := CodeOf(err); got != response.CodeAgentUnavailable {
		t.Fatalf("401 应映射为 CodeAgentUnavailable，实际 %d", got)
	}
	if !IsUnavailable(err) {
		t.Fatal("IsUnavailable 应返回 true")
	}
}

// TestClientNetworkFailureMeansUnavailable 断言连不上 = 内核不可用。
func TestClientNetworkFailureMeansUnavailable(t *testing.T) {
	client := New(Config{BaseURL: "http://127.0.0.1:1", Token: "x"})
	_, err := client.ListUploaders(context.Background())
	if got := CodeOf(err); got != response.CodeAgentUnavailable {
		t.Fatalf("连接失败应映射为 CodeAgentUnavailable，实际 %d（%v）", got, err)
	}
}

// TestClientHealthzNoEnvelope 断言 `/healthz` **不使用信封**（契约特例）。
func TestClientHealthzNoEnvelope(t *testing.T) {
	_, client := newTestAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("路径不符: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Ok":true,"PicgoVersion":"3.0.2","ConfigPath":"/data/picgo/config.json","Uptime":10,"PID":42,"PluginsLoaded":3}`))
	})

	data, err := client.Healthz(context.Background())
	if err != nil {
		t.Fatalf("健康检查失败: %v", err)
	}
	if !data.Ok || data.PicgoVersion != "3.0.2" || data.PID != 42 {
		t.Fatalf("健康检查结果不符：%+v", data)
	}
}

// TestClientUploadSendsPascalCaseFields 断言请求体字段是 PascalCase，
// 而 **IImgInfo 保持 picgo 原生字段名**。
func TestClientUploadSendsPascalCaseFields(t *testing.T) {
	var body map[string]any
	_, client := newTestAgent(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		writeOK(w, UploadData{
			Seq: 1, URL: "https://cdn/x.png", FileName: "x.png",
			Raw: RawImgInfo{"fileName": "x.png", "imgUrl": "https://cdn/x.png", "sha": "abc"},
		})
	})

	tmpl := true
	data, err := client.Upload(context.Background(), UploadRequest{
		Path:                 "/tmp/x.png",
		Uploader:             &UploadTarget{Type: "github", ConfigName: "work"},
		JobUID:               "job_1",
		Seq:                  2,
		PathTemplate:         "img/{Y}",
		FileTemplate:         "{uniqid}{extname}",
		UserUID:              "usr_1",
		SupportsPathTemplate: &tmpl,
	})
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}

	// 外层字段 PascalCase
	if body["Path"] != "/tmp/x.png" {
		t.Fatalf("Path 字段名或值不对：%v", body)
	}
	if _, ok := body["PathTemplate"]; !ok {
		t.Fatalf("应有 PascalCase 的 PathTemplate，实际 %v", body)
	}
	// Uploader 嵌套里也是 PascalCase
	up, ok := body["Uploader"].(map[string]any)
	if !ok {
		t.Fatalf("Uploader 应为对象：%v", body["Uploader"])
	}
	if up["Type"] != "github" || up["ConfigName"] != "work" {
		t.Fatalf("Uploader 字段不符：%v", up)
	}
	// 不得出现 camelCase 混入
	if _, ok := body["pathTemplate"]; ok {
		t.Fatal("不得使用 camelCase 字段名（D81）")
	}

	// 响应里的 Raw 保持 picgo 原生字段名
	if _, ok := data.Raw["fileName"]; !ok {
		t.Fatalf("Raw 应保留 picgo 原生字段名：%v", data.Raw)
	}
	if data.Raw["sha"] != "abc" {
		t.Fatal("Raw 里的插件回写字段应完整保留")
	}
}

// TestClientDeleteRemotePreservesImgInfo 断言删除请求把 IImgInfo 原样传出。
func TestClientDeleteRemotePreservesImgInfo(t *testing.T) {
	var body map[string]any
	_, client := newTestAgent(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		writeOK(w, RemoveData{RemoteDeleted: true, Supported: true, Message: "ok"})
	})

	items := []RawImgInfo{
		{"fileName": "a.png", "imgUrl": "https://cdn/a.png", "sha": "sha-a", "extname": ".png"},
	}
	data, err := client.DeleteRemote(context.Background(), RemoveRequest{
		UploaderType: "github",
		Items:        items,
	})
	if err != nil {
		t.Fatalf("删除调用失败: %v", err)
	}
	if !data.Supported || !data.RemoteDeleted {
		t.Fatalf("结果不符：%+v", data)
	}

	got, ok := body["Items"].([]any)
	if !ok || len(got) != 1 {
		t.Fatalf("Items 应为长度 1 的数组：%v", body["Items"])
	}
	first, _ := got[0].(map[string]any)
	if first["sha"] != "sha-a" {
		t.Fatalf("IImgInfo 的 sha 字段必须原样传出：%v", first)
	}
	if _, ok := first["FileName"]; ok {
		t.Fatal("IImgInfo 不得被转成 PascalCase")
	}
	// 外层是 PascalCase
	if body["UploaderType"] != "github" {
		t.Fatalf("UploaderType 应为 PascalCase：%v", body)
	}
}

// TestClientConfigRoundTrip 断言 RawConfig 的键**原样保留**（D22：插件私有键不能丢）。
func TestClientConfigRoundTrip(t *testing.T) {
	rawConfig := map[string]any{
		"picBed":       map[string]any{"uploader": "github"},
		"picgoPlugins": map[string]any{"picgo-plugin-github-plus": true},
		// 插件私有键：必须被保留
		"uploaded":                 []any{map[string]any{"type": "githubPlus"}},
		"picgo-plugin-github-plus": map[string]any{"lastSync": "2026-01-01"},
	}

	_, client := newTestAgent(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeOK(w, rawConfig)
		case http.MethodPatch:
			writeOK(w, PatchConfigResult{
				Applied:             []string{"picBed.uploader"},
				PreservedPluginKeys: []string{"uploaded", "picgo-plugin-github-plus"},
			})
		default:
			writeOK(w, nil)
		}
	})

	got, err := client.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig 失败: %v", err)
	}
	// 插件私有键必须还在 —— 这是「不可整体重建 config.json」的直接体现
	if _, ok := got["uploaded"]; !ok {
		t.Fatalf("插件私有键 uploaded 不得被丢弃：%v", keysOfMap(got))
	}
	if _, ok := got["picgo-plugin-github-plus"]; !ok {
		t.Fatalf("插件私有键 picgo-plugin-github-plus 不得被丢弃：%v", keysOfMap(got))
	}

	patch, err := client.PatchConfig(context.Background(), map[string]any{"picBed.uploader": "github"})
	if err != nil {
		t.Fatalf("PatchConfig 失败: %v", err)
	}
	if len(patch.PreservedPluginKeys) == 0 {
		t.Fatal("应回报被保留的插件键（便于排查）")
	}
}

// TestClientPluginsPathEscapesScopedName 断言 scoped 包名的 `/` 被编码，
// 否则 agent 会把它当成路径分隔符而路由不匹配。
func TestClientPluginsPathEscapesScopedName(t *testing.T) {
	var gotPath string
	_, client := newTestAgent(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		writeOK(w, PluginReadmeData{Content: "# x", Path: "README.md"})
	})

	if _, err := client.PluginReadme(context.Background(), "@scope/picgo-plugin-x"); err != nil {
		t.Fatalf("调用失败: %v", err)
	}
	if strings.Contains(gotPath, "@scope/picgo") {
		t.Fatalf("scoped 名的 / 必须被编码，实际路径 %s", gotPath)
	}
	if !strings.Contains(gotPath, "%2F") {
		t.Fatalf("应含 %%2F，实际路径 %s", gotPath)
	}
}

// TestClientTimeoutMeansUnavailable 断言超时映射为内核不可用。
func TestClientTimeoutMeansUnavailable(t *testing.T) {
	_, client := newTestAgent(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		writeOK(w, nil)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.ListUploaders(ctx)
	if got := CodeOf(err); got != response.CodeAgentUnavailable {
		t.Fatalf("超时应映射为 CodeAgentUnavailable，实际 %d（%v）", got, err)
	}
}

// TestMockClientFailsByPath 断言 mock 的失败注入是**确定性的**（按路径）。
//
// 之所以按路径而不是按调用序号：按序号会让测试随并发时序抖动。
func TestMockClientFailsByPath(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.png")
	bad := filepath.Join(dir, "bad.png")
	for _, p := range []string{good, bad} {
		if err := os.WriteFile(p, []byte("fake-png-bytes"), 0o644); err != nil {
			t.Fatalf("写测试文件失败: %v", err)
		}
	}

	m := NewMock(MockConfig{FailPaths: []string{"bad"}})

	if _, err := m.Upload(context.Background(), UploadRequest{Path: good}); err != nil {
		t.Fatalf("good.png 应成功: %v", err)
	}
	_, err := m.Upload(context.Background(), UploadRequest{Path: bad})
	if err == nil {
		t.Fatal("bad.png 应失败")
	}
	if CodeOf(err) != response.CodeUploadFailed {
		t.Fatalf("失败码应为 CodeUploadFailed，实际 %d", CodeOf(err))
	}
}

// TestMockClientRequiresSourceFile 断言 mock 也校验源文件存在 ——
// 这样「本地暂存文件丢失」的场景在无 Node 环境下也能被测出来。
func TestMockClientRequiresSourceFile(t *testing.T) {
	m := NewMock(MockConfig{})
	_, err := m.Upload(context.Background(), UploadRequest{Path: "/definitely/not/here.png"})
	if err == nil {
		t.Fatal("源文件不存在时应失败")
	}
	if CodeOf(err) != response.CodeUploadFailed {
		t.Fatalf("应为 CodeUploadFailed，实际 %d", CodeOf(err))
	}
}

// TestMockClientRejectsUnknownTarget 断言 mock 与真实 agent 一样：
// 目标非法时返回 **CodeInvalidParam**（而不是上传失败）。
func TestMockClientRejectsUnknownTarget(t *testing.T) {
	m := NewMock(MockConfig{})

	_, err := m.Upload(context.Background(), UploadRequest{
		Path:     "any.png",
		Uploader: &UploadTarget{Type: "no-such-type"},
	})
	if CodeOf(err) != response.CodeInvalidParam {
		t.Fatalf("未知类型应映射为 CodeInvalidParam，实际 %d（%v）", CodeOf(err), err)
	}

	_, err = m.Upload(context.Background(), UploadRequest{
		Path:     "any.png",
		Uploader: &UploadTarget{Type: "github", ConfigName: "no-such-config"},
	})
	if CodeOf(err) != response.CodeInvalidParam {
		t.Fatalf("未知配置名应映射为 CodeInvalidParam，实际 %d（%v）", CodeOf(err), err)
	}
}

// TestMockUploaderConfigLifecycle 断言 mock 的多配置管理语义与真实 agent 一致（D64）。
func TestMockUploaderConfigLifecycle(t *testing.T) {
	m := NewMock(MockConfig{})
	ctx := context.Background()

	if _, err := m.CreateOrUpdateUploaderConfig(ctx, CreateUploaderConfigInput{
		Type: "github", ConfigName: "work", Config: RawConfig{"repo": "a/b"},
	}); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if _, err := m.CreateOrUpdateUploaderConfig(ctx, CreateUploaderConfigInput{
		Type: "github", ConfigName: "personal", Config: RawConfig{"repo": "c/d"},
	}); err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	list, err := m.ListUploaderConfigs(ctx, "github")
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	// mock 预置了一条 Default（便于「不显式建配置就能上传」的用例），
	// 因此这里断言「我们建的两条都在」。
	names := map[string]bool{}
	for _, c := range list.Configs {
		if n, ok := c["_configName"].(string); ok {
			names[n] = true
		}
	}
	if !names["work"] || !names["personal"] {
		t.Fatalf("应包含 work 与 personal，实际 %v", names)
	}

	// 切到 personal
	if err := m.UseUploader(ctx, "github", "personal"); err != nil {
		t.Fatalf("切换失败: %v", err)
	}
	up, err := m.ListUploaders(ctx)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if up.Current.ConfigName != "personal" {
		t.Fatalf("当前配置应为 personal，实际 %s", up.Current.ConfigName)
	}

	// 切到不存在的
	if err := m.UseUploader(ctx, "github", "nope"); CodeOf(err) != response.CodeNotFound {
		t.Fatalf("不存在的配置应返回 NotFound，实际 %v", err)
	}

	// 删掉当前默认 → 应自动回落到另一条
	if err := m.DeleteUploaderConfig(ctx, "github", "personal"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	list, _ = m.ListUploaderConfigs(ctx, "github")
	for _, c := range list.Configs {
		if n, _ := c["_configName"].(string); n == "personal" {
			t.Fatal("personal 应已被删除")
		}
	}
	if len(list.Configs) == 0 {
		t.Fatal("不应把默认那条也删掉")
	}
}

// TestMockPatchConfigPreservesPluginKeys 断言 mock 也遵守 D22（键级合并）。
//
// mock 是「无 Node 环境的联调替身」，它必须与真实 agent 有**相同的安全语义**，
// 否则本机联调会掩盖真实环境的问题。
func TestMockPatchConfigPreservesPluginKeys(t *testing.T) {
	m := NewMock(MockConfig{})

	res, err := m.PatchConfig(context.Background(), map[string]any{"picBed.uploader": "github"})
	if err != nil {
		t.Fatalf("PATCH 失败: %v", err)
	}
	if len(res.Applied) != 1 {
		t.Fatalf("Applied 应有 1 项，实际 %v", res.Applied)
	}
	found := false
	for _, k := range res.PreservedPluginKeys {
		if k == "uploaded" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应回报保留的插件私有键 uploaded，实际 %v", res.PreservedPluginKeys)
	}

	// PUT 缺少受管键 → 必须拒绝（防误毁插件状态）
	err = m.PutConfig(context.Background(), RawConfig{"picBed": map[string]any{}})
	if CodeOf(err) != response.CodeInvalidParam {
		t.Fatalf("PUT 缺少受管键时应拒绝，实际 %v", err)
	}
}

// TestMockHealthz 断言 mock 的健康检查结构与真实 agent 对齐。
func TestMockHealthz(t *testing.T) {
	m := NewMock(MockConfig{PicgoVersion: "9.9.9"})
	data, err := m.Healthz(context.Background())
	if err != nil {
		t.Fatalf("健康检查失败: %v", err)
	}
	if !data.Ok || data.PicgoVersion != "9.9.9" || data.PID == 0 {
		t.Fatalf("健康检查结果不符：%+v", data)
	}
}

func keysOfMap(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
