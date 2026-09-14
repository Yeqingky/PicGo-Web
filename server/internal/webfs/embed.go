// Package webfs 托管**内置 SPA**：编译期把 `web/` 的构建产物嵌入二进制。
//
// # 为什么是内置而不是运行时读取
//
// D94 的模型是「内置 SPA 提供**全部页面**的默认实现；主题是可选的页面覆盖层」。
// 内置 SPA 随二进制发布，因此：
//
//   - 部署只有一个可执行文件（不需要额外的静态文件目录）
//   - 即使主题目录被删光，`/login` 与 `/admin/**` 也**始终可用**
//
// # go:embed 的目录约束
//
// `go:embed` **不能跨模块目录**（不能 embed `../../web/dist`），
// 因此构建流程把 `web/dist` 同步到 `server/internal/webfs/dist/`：
//
//	cd web && pnpm build
//	rm -rf server/internal/webfs/dist && cp -r web/dist server/internal/webfs/dist
//
// `make build-web` 已包含这一步（见仓库根 Makefile）。
//
// 本目录**始终提交一个 `.distkeep` 占位**（`dist/.gitkeep`），这样：
//   - 未构建前端时 `//go:embed all:dist` 仍能匹配、`go build` 不会失败
//   - 运行时通过 HasSPA() 判断是否真的有产物，缺失时返回「前端尚未构建」提示页
//
// 资源前缀约定（D99.2）：内置 SPA 用 **`/assets/**`**；
// 主题用 **`/theme-assets/**`**，两者严格分离。
package webfs

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
)

// distRoot 是 embed 中的产物根目录名。
const distRoot = "dist"

// IndexFile 是 SPA 入口文件名。
const IndexFile = "index.html"

// AssetsPrefix 是内置 SPA 的资源 URL 前缀（D99.2）。
const AssetsPrefix = "/assets"

//go:embed all:dist
var embedded embed.FS

// distFS 返回以 `dist/` 为根的文件系统。
//
// 编译期保证存在（占位文件），因此这里只需处理 fs.Sub 的理论错误。
func distFS() (fs.FS, error) {
	sub, err := fs.Sub(embedded, distRoot)
	if err != nil {
		return nil, fmt.Errorf("打开内置前端资源失败: %w", err)
	}
	return sub, nil
}

// HasSPA 判断是否嵌入了真实的构建产物。
//
// 判据：`index.html` 存在且 `assets/` 目录存在。
// 只有占位文件时返回 false，调用方据此渲染「前端尚未构建」提示页。
func HasSPA() bool {
	sub, err := distFS()
	if err != nil {
		return false
	}
	if !fileExists(sub, IndexFile) {
		return false
	}
	return dirExists(sub, "assets")
}

// IndexHTML 返回内置 SPA 的 index.html。
//
// 第二个返回值为 false 表示**尚未构建前端**（只有占位文件），
// 调用方应改用 PlaceholderHTML()。
func IndexHTML() ([]byte, bool) {
	sub, err := distFS()
	if err != nil {
		return nil, false
	}
	data, err := fs.ReadFile(sub, IndexFile)
	if err != nil {
		return nil, false
	}
	return data, true
}

// Asset 读取内置 SPA 的一个静态资源（相对 `dist/` 的路径）。
//
// 路径规范化后必须落在 `dist/` 内，否则返回 ErrNotFound（**防 `..` 穿越**）。
func Asset(rel string) ([]byte, string, error) {
	clean, err := CleanRelativePath(rel)
	if err != nil {
		return nil, "", err
	}

	sub, err := distFS()
	if err != nil {
		return nil, "", err
	}

	data, err := fs.ReadFile(sub, clean)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, "", fmt.Errorf("%w: /%s", ErrNotFound, clean)
		}
		return nil, "", fmt.Errorf("读取内置前端资源失败: %w", err)
	}
	return data, ContentType(clean), nil
}

// Favicon 返回内置 SPA 的 favicon（不存在返回 false）。
func Favicon() ([]byte, string, bool) {
	sub, err := distFS()
	if err != nil {
		return nil, "", false
	}
	for _, name := range []string{"favicon.ico", "favicon.svg", "favicon.png"} {
		if data, err := fs.ReadFile(sub, name); err == nil {
			return data, ContentType(name), true
		}
	}
	return nil, "", false
}

// ErrNotFound 表示请求的文件不存在（或路径越界）。
var ErrNotFound = errors.New("资源不存在")

// CleanRelativePath 归一化 URL 相对路径并拒绝越界。
//
// 与 theme 包的同名逻辑一致：**只接受相对路径**，`..` 一律报错
// （而不是「清理掉」，这样攻击尝试会留下痕迹且绝不会读到别的文件）。
//
// ⚠️ 关键：`..` 的检查在 **path.Clean 之前**做。
// 若先 Clean 再检查，`assets/../index.html` 会被归一为 `index.html` 而通过检查——
// 这正好是契约里警告的反模式：「把 HTML 当 JS 返回会导致
// Failed to fetch dynamically imported module」。
// （主题的 `assets/` 前缀没有这个问题，因为它不额外拼前缀；
// 但 `/assets/**` 会拼出 `assets/<rel>`，因此必须在源头拒绝。）
func CleanRelativePath(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("%w: 空路径", ErrNotFound)
	}
	if strings.Contains(rel, "\x00") {
		return "", fmt.Errorf("%w: 路径含非法字符", ErrNotFound)
	}

	slash := filepath.ToSlash(rel)
	if strings.HasPrefix(slash, "/") {
		return "", fmt.Errorf("%w: 不接受绝对路径", ErrNotFound)
	}

	// 先按原始分段拒绝 `..`（不做归一化）
	for _, seg := range strings.Split(slash, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: 路径越界", ErrNotFound)
		}
	}

	clean := path.Clean(slash)
	if clean == "." || clean == "" {
		return "", fmt.Errorf("%w: 空路径", ErrNotFound)
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: 路径越界", ErrNotFound)
	}
	// 纵深防御：归一化后再查一遍（防将来改动上面的逻辑）
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: 路径越界", ErrNotFound)
		}
	}
	return clean, nil
}

// fileExists 判断文件系统里是否存在某个普通文件。
func fileExists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

// dirExists 判断文件系统里是否存在某个目录。
func dirExists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && info.IsDir()
}
