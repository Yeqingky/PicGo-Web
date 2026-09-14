package webfs

import (
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// 缓存策略（D99.2）。
//
// 内置 SPA 的资源文件名带内容哈希，因此可以长缓存；
// `index.html` 必须 no-cache，否则换主题 / 换前端版本后刷新看不到变化。
const (
	// CacheImmutable 用于带内容哈希的静态资源。
	CacheImmutable = "public, max-age=31536000, immutable"
	// CacheNoCache 用于 index.html 与提示页。
	CacheNoCache = "no-cache, no-store, must-revalidate"
	// CacheFavicon favicon 的折中策略。
	CacheFavicon = "public, max-age=86400"
)

// mimeOverrides 收敛少数容易出错的扩展名。
//
// 不同系统 / Go 版本的 mime 数据库对 .js / .mjs / .woff2 判定不一致
// （有的给 text/plain、有的给 application/javascript），而这几类正是前端
// 资源最关键的，因此显式覆盖。
//
// 这是本仓库 MIME 判断的**唯一真相源**：theme 包的 contentType 委托到这里。
var mimeOverrides = map[string]string{
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".svg":         "image/svg+xml",
	".json":        "application/json; charset=utf-8",
	".map":         "application/json; charset=utf-8",
	".woff2":       "font/woff2",
	".woff":        "font/woff",
	".ttf":         "font/ttf",
	".otf":         "font/otf",
	".html":        "text/html; charset=utf-8",
	".htm":         "text/html; charset=utf-8",
	".ico":         "image/x-icon",
	".txt":         "text/plain; charset=utf-8",
	".webmanifest": "application/manifest+json; charset=utf-8",
	".webp":        "image/webp",
	".avif":        "image/avif",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".bmp":         "image/bmp",
	".wasm":        "application/wasm",
}

// ContentType 按扩展名推断 Content-Type（未知 → application/octet-stream）。
func ContentType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ct, ok := mimeOverrides[ext]; ok {
		return ct
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// IsHTML 判断是否是 HTML 响应（据此决定缓存策略）。
func IsHTML(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".html" || ext == ".htm"
}

// ServeAssets 托管 `GET /assets/**`（内置 SPA 的静态资源）。
//
//   - 长缓存（文件名带哈希）
//   - 路径越界 → 404（不参与 SPA 回退）
//   - 未构建前端时返回 404（而非 index.html：把 HTML 当 JS 返回会导致
//     "Failed to fetch dynamically imported module" 这类难查的错误）
func ServeAssets() gin.HandlerFunc {
	return func(c *gin.Context) {
		rel := c.Param("filepath")
		rel = strings.TrimPrefix(rel, "/")

		// `assets/` 前缀由本 handler 承载，读文件时补回去
		full := "assets"
		if rel != "" {
			full = "assets/" + rel
		}

		data, ctype, err := Asset(full)
		if err != nil {
			notFound(c)
			return
		}

		// 不变式：解析结果必须仍在 `assets/` 下。
		//
		// CleanRelativePath 已经会拒绝 `..`；这里再确认一次 ——
		// 一旦失败说明上游逻辑被改坏了，宁可不服务，也不要把 dist/ 根下的
		// index.html 当成某个 .js 资源发出去（那会导致
		// "Failed to fetch dynamically imported module" 这类极难排查的错误）。
		if full != "assets" && !strings.HasPrefix(full, "assets/") {
			notFound(c)
			return
		}

		c.Header("Cache-Control", CacheImmutable)
		c.Data(http.StatusOK, ctype, data)
	}
}

// ServeSPA 返回内置 SPA 的 index.html（SPA 回退 / 登录页 / 后台）。
//
//   - 始终 no-cache（换主题或换前端版本后刷新即生效）
//   - 未构建前端时返回「前端尚未构建」提示页（HTTP 200 + no-cache），
//     而不是 404 —— 这样至少能看出「服务在跑，只是没构建前端」。
func ServeSPA() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", CacheNoCache)

		if data, ok := IndexHTML(); ok {
			c.Data(http.StatusOK, "text/html; charset=utf-8", data)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", PlaceholderHTML())
	}
}

// ServeFavicon 返回内置 SPA 的 favicon（缺失时 404，由调用方决定是否兜底）。
func ServeFavicon() gin.HandlerFunc {
	return func(c *gin.Context) {
		data, ctype, ok := Favicon()
		if !ok {
			notFound(c)
			return
		}
		c.Header("Cache-Control", CacheFavicon)
		c.Data(http.StatusOK, ctype, data)
	}
}

// PlaceholderHTML 是「前端尚未构建」的提示页。
//
// 用途：`go build` 在未同步 web/dist 时也能跑起来，并且给出**可操作**的提示，
// 而不是让运维对着一个空白页猜原因。
func PlaceholderHTML() []byte {
	return []byte(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>PicGo-Web · 前端尚未构建</title>
<style>
  body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0a0a0a;color:#fafafa;
       font-family:Inter,-apple-system,"PingFang SC","Microsoft YaHei",sans-serif;padding:24px}
  .card{max-width:640px;background:#141414;border:1px solid #262626;border-radius:12px;padding:32px}
  h1{margin:0 0 12px;font-size:20px}
  p{color:#a3a3a3;line-height:1.7;margin:0 0 12px}
  code{background:#262626;padding:2px 6px;border-radius:6px;font-size:13px}
  pre{background:#0a0a0a;border:1px solid #262626;border-radius:8px;padding:12px;overflow:auto;font-size:13px}
</style>
</head>
<body>
<div class="card">
  <h1>前端尚未构建</h1>
  <p>内置 SPA 的静态资源没有被打进二进制（<code>server/internal/webfs/dist/</code> 里只有占位文件）。</p>
  <p>在仓库根目录执行：</p>
  <pre>make build-web      # 构建前端并同步到 server/internal/webfs/dist/
make build-server   # 重新编译二进制（go:embed 需要构建时文件存在）</pre>
  <p>主题系统与全部 API 不受影响，可正常使用。</p>
</div>
</body>
</html>`)
}

// notFound 以统一信封返回 40401（与 internal/response 保持一致；
// 这里不 import response 以避免 webfs 依赖上层包）。
func notFound(c *gin.Context) {
	c.JSON(http.StatusNotFound, gin.H{"Code": 40401, "Message": "资源不存在", "Data": nil})
}
