package handler

import (
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/YeqingKy/PicGo-Web/server/internal/service"
)

// 本文件放 handler 层共用的纯函数助手。
//
// 原则：handler 只做「解析入参 / 调 service / 写响应」，
// 因此这里的函数都不碰数据库、不碰业务规则。

// clampPage 把页码纠正到合法范围（越界时纠正而不是报错，对调用方更友好）。
func clampPage(v int) int {
	if v <= 0 {
		return service.DefaultPage
	}
	return v
}

// clampPageSize 把每页条数纠正到合法范围。
func clampPageSize(v int) int {
	if v <= 0 {
		return service.DefaultPageSize
	}
	if v > service.MaxPageSize {
		return service.MaxPageSize
	}
	return v
}

// parseBoolQuery 解析 query 里的布尔值（宽松：接受 true/1/yes/on）。
func parseBoolQuery(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on", "y":
		return true
	default:
		return false
	}
}

// parseBoolPtr 解析可选布尔值；空串返回 nil（表示「未指定」）。
//
// 用指针区分「未传」与「传了 false」—— 前者应回落到全局设置，后者是显式关闭。
func parseBoolPtr(raw string) *bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	v := parseBoolQuery(trimmed)
	return &v
}

// firstFormValue 取 multipart 表单里某个字段的第一个值。
func firstFormValue(values map[string][]string, key string) string {
	if values == nil {
		return ""
	}
	list, ok := values[key]
	if !ok || len(list) == 0 {
		return ""
	}
	return strings.TrimSpace(list[0])
}

// queryInt64 读 int64 query 参数；缺失或非法时返回 def。
func queryInt64(c interface{ Query(string) string }, name string, def int64) int64 {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return def
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// sanitizeFileName 清洗客户端提交的文件名，防止路径穿越与怪字符。
//
// 规则：
//   - 只取 base name（去掉 `../` 与目录部分）
//   - 去掉路径分隔符与控制字符
//   - 长度限制 200（留出扩展名与后缀空间，且不超过 Uploads.FileName 的 255）
func sanitizeFileName(name string) string {
	// 统一分隔符后再取 base（Windows 客户端可能传 `a\b\c.png`）
	normalized := strings.ReplaceAll(name, "\\", "/")
	base := filepath.Base(normalized)
	if base == "." || base == "/" || base == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range base {
		switch {
		case r < 0x20 || r == 0x7f:
			// 控制字符直接丢弃
			continue
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' ||
			r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}

	out := strings.TrimSpace(b.String())
	if out == "" {
		return ""
	}
	// 纯点名（如 ".."）要拒绝
	if out == ".." || strings.Trim(out, ".") == "" {
		return ""
	}
	if len(out) > 200 {
		// 保留扩展名，截断主干
		ext := filepath.Ext(out)
		if len(ext) > 20 {
			ext = ""
		}
		stemLen := 200 - len(ext)
		if stemLen < 1 {
			stemLen = 1
		}
		out = out[:stemLen] + ext
	}
	return out
}

// urlHost 取主机名（用于日志，不含凭据）。
func urlHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
