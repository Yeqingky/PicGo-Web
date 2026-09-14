package webfs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHasSPAAndIndex 断言嵌入情况与实际构建产物一致。
//
// 若 `internal/webfs/dist` 只有占位文件（未跑过 make build-web），
// HasSPA 为 false，ServeSPA 会返回「前端尚未构建」提示页 —— 这也是被断言的路径。
func TestHasSPAAndIndex(t *testing.T) {
	hasSPA := HasSPA()
	index, ok := IndexHTML()

	if hasSPA {
		if !ok {
			t.Fatal("HasSPA 为真但读不到 index.html")
		}
		if len(index) == 0 {
			t.Fatal("index.html 为空")
		}
		// 真实 SPA 的入口应含挂载点
		if !strings.Contains(string(index), `id="root"`) {
			t.Errorf("index.html 未包含 SPA 挂载点 id=\"root\"：\n%s", truncate(string(index)))
		}
		return
	}

	// 未构建前端：必须仍能编译运行，并给出**可操作**的提示页
	if ok {
		t.Error("HasSPA 为 false 时不应读到 index.html")
	}
	ph := PlaceholderHTML()
	if len(ph) == 0 {
		t.Fatal("占位页不应为空")
	}
	for _, want := range []string{"前端尚未构建", "make build-web"} {
		if !strings.Contains(string(ph), want) {
			t.Errorf("占位页应包含 %q", want)
		}
	}
}

func TestEmbedIsAlwaysCompilable(t *testing.T) {
	// 占位文件必须被提交，否则全新 clone 上 go:embed 会编译失败
	if _, err := os.Stat(filepath.Join("dist", ".gitkeep")); err != nil {
		t.Errorf("dist/.gitkeep 必须存在（保证未构建前端时也能 go build）: %v", err)
	}
}

func TestContentType(t *testing.T) {
	cases := map[string]string{
		"a.js":         "text/javascript; charset=utf-8",
		"a.mjs":        "text/javascript; charset=utf-8",
		"a.css":        "text/css; charset=utf-8",
		"a.svg":        "image/svg+xml",
		"a.json":       "application/json; charset=utf-8",
		"a.woff2":      "font/woff2",
		"a.webp":       "image/webp",
		"a.ico":        "image/x-icon",
		"./b/c.js":     "text/javascript; charset=utf-8",
		"a.unknownext": "application/octet-stream",
		"noext":        "application/octet-stream",
	}
	for name, want := range cases {
		if got := ContentType(name); got != want {
			t.Errorf("ContentType(%q) = %q，期望 %q", name, got, want)
		}
	}
}

func TestIsHTML(t *testing.T) {
	if !IsHTML("index.html") || !IsHTML("a.HTM") {
		t.Error("html/htm 应被识别")
	}
	if IsHTML("a.js") || IsHTML("a.json") {
		t.Error("非 HTML 不应被识别")
	}
}

// TestCleanRelativePathRejectsTraversal 是本包最关键的安全断言。
func TestCleanRelativePathRejectsTraversal(t *testing.T) {
	bad := []string{
		"",
		"  ",
		"..",
		"../",
		"../secret",
		"../../etc/passwd",
		"a/../../b",
		"a/b/../../../c",
		"/etc/passwd",
		"/",
		"./../x",
		"a\x00b",
	}
	for _, rel := range bad {
		if _, err := CleanRelativePath(rel); err == nil {
			t.Errorf("路径 %q 应当被拒绝", rel)
		} else if !errors.Is(err, ErrNotFound) {
			t.Errorf("路径 %q 的错误应为 ErrNotFound，实际 %v", rel, err)
		}
	}

	good := map[string]string{
		"a.js":            "a.js",
		"assets/a.js":     "assets/a.js",
		"./a.js":          "a.js",
		"assets//a.js":    "assets/a.js",
		"assets/./a.js":   "assets/a.js",
		"deep/nested/x.c": "deep/nested/x.c",
	}
	for in, want := range good {
		got, err := CleanRelativePath(in)
		if err != nil {
			t.Errorf("路径 %q 应当合法: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("CleanRelativePath(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestAssetRejectsTraversal 断言即使真实文件存在，越界路径也读不到。
func TestAssetRejectsTraversal(t *testing.T) {
	// 在 dist 之外放一个「机密」文件
	secret := filepath.Join("..", "zz-secret-probe.txt")
	if err := os.WriteFile(secret, []byte("TOP-SECRET"), 0o644); err != nil {
		t.Fatalf("准备探测文件失败: %v", err)
	}
	defer func() { _ = os.Remove(secret) }()

	for _, rel := range []string{
		"../zz-secret-probe.txt",
		"../../zz-secret-probe.txt",
		"assets/../../zz-secret-probe.txt",
		"/etc/passwd",
	} {
		data, _, err := Asset(rel)
		if err == nil {
			t.Errorf("路径 %q 应当被拒绝，但读到了 %q", rel, string(data))
			continue
		}
		if strings.Contains(string(data), "TOP-SECRET") {
			t.Errorf("路径 %q 泄露了 dist 之外的文件！", rel)
		}
	}
}

// TestAssetRejectsDotDotBeforeCleaning 是回归测试：
//
// `assets/../index.html` 若「先 Clean 再检查」，会被归一为 `index.html`
// （确实在 dist 内）而通过检查，于是把一个 HTML 当成资源返回 ——
// 这正是契约（API.md §10.1）警告的反模式：
// 「把 HTML 当 JS 返回会导致 Failed to fetch dynamically imported module」。
//
// 因此 `..` 必须在归一化**之前**就拒掉。
func TestAssetRejectsDotDotBeforeCleaning(t *testing.T) {
	for _, rel := range []string{
		"assets/../index.html",
		"assets/../favicon.ico",
		"assets/sub/../../index.html",
		"./assets/../index.html",
	} {
		data, _, err := Asset(rel)
		if err == nil {
			t.Errorf("路径 %q 应当被拒绝（含 .. 段），但返回了 %d 字节", rel, len(data))
			continue
		}
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("路径 %q 的错误应为 ErrNotFound，实际 %v", rel, err)
		}
	}
}

func TestAssetMissingReturnsNotFound(t *testing.T) {
	_, _, err := Asset("definitely/not/here.js")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("缺失文件应返回 ErrNotFound，实际 %v", err)
	}
}

func TestAssetExistingWhenBuilt(t *testing.T) {
	if !HasSPA() {
		t.Skip("未构建前端，跳过 /assets 断言")
	}

	entries, err := os.ReadDir(filepath.Join("dist", "assets"))
	if err != nil || len(entries) == 0 {
		t.Skip("dist/assets 为空，跳过")
	}

	name := entries[0].Name()
	data, ctype, err := Asset("assets/" + name)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", name, err)
	}
	if len(data) == 0 {
		t.Errorf("%s 内容为空", name)
	}
	if ctype == "" {
		t.Errorf("%s 的 Content-Type 为空", name)
	}
}

func TestFaviconOptional(t *testing.T) {
	// favicon 是可选的：有则返回，无则 false（由调用方回退）
	data, ctype, ok := Favicon()
	if !ok {
		return
	}
	if len(data) == 0 || ctype == "" {
		t.Errorf("favicon 存在但内容/类型异常: len=%d ctype=%q", len(data), ctype)
	}
}

func TestServeHandlersUseCorrectCacheHeaders(t *testing.T) {
	// 缓存策略是契约的一部分（D99.2），常量值直接断言
	if !strings.Contains(CacheImmutable, "immutable") || !strings.Contains(CacheImmutable, "max-age=31536000") {
		t.Errorf("CacheImmutable 不符: %q", CacheImmutable)
	}
	if !strings.Contains(CacheNoCache, "no-cache") {
		t.Errorf("CacheNoCache 不符: %q", CacheNoCache)
	}
	if CacheFavicon == "" {
		t.Error("CacheFavicon 不应为空")
	}
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
