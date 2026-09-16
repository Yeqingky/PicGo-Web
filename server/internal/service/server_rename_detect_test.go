package service

import (
	"testing"

	"github.com/YeqingKy/PicGo-Web/server/internal/agent"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
)

// urlFileName: 从 URL 取文件名（去查询串/锚点；无文件名返回空）。
func TestUrlFileName(t *testing.T) {
	cases := map[string]string{
		"https://cdn.example.com/i/abc123.jpg":         "abc123.jpg",
		"https://cdn.example.com/i/abc123.jpg?token=x": "abc123.jpg",
		"https://cdn.example.com/i/abc123.jpg#frag":    "abc123.jpg",
		"https://cdn.example.com/a/b/c.png":            "c.png",
		"https://cdn.example.com":                      "", // 无 path 文件名 → 空（不判定）
		"://bad":                                       "", // 解析失败
	}
	for raw, want := range cases {
		if got := urlFileName(raw); got != want {
			t.Errorf("urlFileName(%q) = %q, want %q", raw, got, want)
		}
	}
}

// stripExt: 去扩展名。
func TestStripExt(t *testing.T) {
	cases := map[string]string{
		"yeqing.jpeg": "yeqing",
		"a.b.c.png":   "a.b.c",
		"noext":       "noext",
		".hidden":     ".hidden", // 点开头是隐藏文件, 不当作扩展名剥离
	}
	for in, want := range cases {
		if got := stripExt(in); got != want {
			t.Errorf("stripExt(%q) = %q, want %q", in, got, want)
		}
	}
}

// MarkServerRenameDetected: URL 文件名 ≠ 期望名 ⇒ 置位 Capabilities.ServerRenames;
// 一致 ⇒ 不置位; 已置位 ⇒ 幂等; 参数异常 / 存储不存在 ⇒ 静默忽略。
func TestMarkServerRenameDetected(t *testing.T) {
	e := newW5Env(t)
	storage := e.makeStorage("renames-probe", false)

	// —— 场景 1: URL 文件名与期望一致 → 不置位 ——
	if e.storage.MarkServerRenameDetected(storage.UID, "yeqing.jpg", "https://cdn.example.com/i/yeqing.jpg") {
		t.Fatal("URL 与期望一致时不应置位")
	}
	caps := readStorageCaps(t, e.storage, storage.UID)
	if caps.ServerRenames {
		t.Fatal("不应置位")
	}

	// —— 场景 2: URL 文件名不同（NodeImage 风格短链）→ 置位 ——
	if !e.storage.MarkServerRenameDetected(storage.UID, "yeqing.jpg", "https://cdn.nodeimage.com/i/m6Oi6qgVtuS3.jpg") {
		t.Fatal("URL 文件名不同应置位")
	}
	caps = readStorageCaps(t, e.storage, storage.UID)
	if !caps.ServerRenames {
		t.Fatal("ServerRenames 应为 true")
	}
	if caps.DetectedAt == 0 {
		t.Fatal("置位时应刷新 DetectedAt")
	}
	// 其它能力字段不能被洗掉（Capabilities JSON 是整存整取）
	if !caps.SupportsPathTemplate {
		t.Fatal("已有能力字段应保留（github 带 path 字段）")
	}
	firstDetectedAt := caps.DetectedAt

	// —— 场景 3: 已置位再调用 → 幂等 ——
	if e.storage.MarkServerRenameDetected(storage.UID, "other.png", "https://cdn.nodeimage.com/i/xxxx.png") {
		t.Log("重复探测返回 false（幂等）")
	}
	caps = readStorageCaps(t, e.storage, storage.UID)
	if !caps.ServerRenames || caps.DetectedAt != firstDetectedAt {
		t.Fatalf("重复探测应幂等: caps=%+v", caps)
	}

	// —— 场景 4: 参数异常 → 不判定不 panic ——
	if e.storage.MarkServerRenameDetected(storage.UID, "x.png", "") {
		t.Fatal("空 URL 不应置位")
	}
	if e.storage.MarkServerRenameDetected(storage.UID, "", "https://x/a.png") {
		t.Fatal("空文件名不应置位")
	}
	if e.storage.MarkServerRenameDetected("", "a.png", "https://x/b.png") {
		t.Fatal("空 UID 不应置位")
	}

	// —— 场景 5: 存储不存在 → 静默忽略 ——
	if e.storage.MarkServerRenameDetected("st_notexist00000000000000000", "a.png", "https://x/b.png") {
		t.Fatal("不存在的存储不应置位")
	}
}

// readStorageCaps 从存储配置读回 Capabilities。
func readStorageCaps(t *testing.T, svc *StorageService, uid string) agent.Capabilities {
	t.Helper()
	row, err := svc.CapabilitiesOf(uid)
	if err != nil {
		t.Fatalf("读取存储配置失败: %v", err)
	}
	return row
}

// 确认 model.Job 的 Kind 常量与投影器前缀判断一致（防手滑改坏 plugin.% 匹配）。
func TestPluginJobKindPrefixConst(t *testing.T) {
	if !isPluginJobKind(model.JobKindPluginInstall) ||
		!isPluginJobKind(model.JobKindPluginUninstall) ||
		!isPluginJobKind(model.JobKindPluginUpdate) {
		t.Fatal("plugin.* 前缀判断失效")
	}
	if isPluginJobKind(model.JobKindUpload) || isPluginJobKind(model.JobKindThemeInstall) {
		t.Fatal("非插件类 Kind 被误判")
	}
}
