package theme

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/YeqingKy/PicGo-Web/server/internal/webfs"
)

// htmlEscape 是极简的 HTML 转义（仅用于最小兜底页，避免引入 html/template 的重量级路径）。
func htmlEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// contentType 按扩展名推断 Content-Type。
//
// 委托给 webfs（本仓库 MIME 判断的唯一真相源）：主题资源与内置 SPA 资源
// 在同一个源下提供服务，MIME 判定必须完全一致，否则同一份字体文件
// 在两条路径上可能拿到不同的 Content-Type。
func contentType(name string) string { return webfs.ContentType(name) }

// isHTML 判断是否是 HTML 响应（据此决定缓存策略）。
func isHTML(name string) bool { return webfs.IsHTML(name) }

// randomSuffix 返回一个短的随机十六进制串，用于临时目录 / 备份目录命名。
//
// 用 crypto/rand 而不是 math/rand：目录名出现在文件系统上，可预测的名字
// 会带来符号链接抢占之类的风险（概率极低，但代价只是一次 rand.Read）。
func randomSuffix() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// 极端情况下退化为固定串；临时目录仍带 MkdirTemp 的随机后缀，不会真的撞名
		return "fallback"
	}
	return hex.EncodeToString(buf)
}
