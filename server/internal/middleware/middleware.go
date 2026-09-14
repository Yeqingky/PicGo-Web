// Package middleware 提供 Gin 中间件。
//
// 注意：**鉴权中间件**（RequireAuth / RequireAdmin）属于 W3，见 internal/auth。
// 本文件只放与业务无关的基础中间件。
package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/id"
	"github.com/YeqingKy/PicGo-Web/server/internal/logger"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// HeaderRequestID 请求 ID 的响应头名。
const HeaderRequestID = "X-Request-Id"

// ctxKeyRequestID 在 gin.Context 里存请求 ID 的键。
const ctxKeyRequestID = "request_id"

// RequestID 为每个请求生成（或透传）一个请求 ID，写入响应头与 context。
//
// 若上游（反向代理）已带 X-Request-Id 则复用它，便于链路追踪。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := strings.TrimSpace(c.GetHeader(HeaderRequestID))
		if rid == "" || len(rid) > 64 {
			rid = id.New()
		}
		c.Set(ctxKeyRequestID, rid)
		c.Header(HeaderRequestID, rid)

		ctx := logger.WithRequestID(c.Request.Context(), rid)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// GetRequestID 取当前请求 ID。
func GetRequestID(c *gin.Context) string {
	if v, ok := c.Get(ctxKeyRequestID); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Recovery 捕获 panic，返回 50001 并记录堆栈。
//
// ⚠️ 不把 panic 内容或堆栈返回给客户端（可能含内部信息），只写日志。
func Recovery(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				log.Error("请求处理发生 panic",
					"err", r,
					"path", c.Request.URL.Path,
					"method", c.Request.Method,
					"request_id", GetRequestID(c),
					"stack", string(debug.Stack()),
				)
				if !c.Writer.Written() {
					response.Abort(c, response.CodeInternal)
					return
				}
				c.Abort()
			}
		}()
		c.Next()
	}
}

// AccessLog 记录访问日志（JSON，走 slog）。
//
// 健康检查路径默认不记录，避免刷屏。
func AccessLog(log *slog.Logger, skipPaths ...string) gin.HandlerFunc {
	skip := make(map[string]bool, len(skipPaths))
	for _, p := range skipPaths {
		skip[p] = true
	}

	return func(c *gin.Context) {
		if skip[c.Request.URL.Path] {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()

		args := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", status,
			"latency_ms", latency.Milliseconds(),
			"ip", ClientIP(c),
			"request_id", GetRequestID(c),
		}
		if uid := logger.UserUID(c.Request.Context()); uid != "" {
			args = append(args, "user_uid", uid)
		}
		if len(c.Errors) > 0 {
			args = append(args, "errors", c.Errors.String())
		}

		switch {
		case status >= http.StatusInternalServerError:
			log.Error("请求失败", args...)
		case status >= http.StatusBadRequest:
			log.Warn("请求异常", args...)
		default:
			log.Info("请求完成", args...)
		}
	}
}

// ClientIP 返回客户端 IP。
//
// ⚠️ 只有显式配置了可信代理（PICGO_WEB_TRUST_PROXY=true）时才信任
// X-Forwarded-For / X-Real-IP，否则攻击者可伪造 IP 绕过登录限流。
func ClientIP(c *gin.Context) string {
	if ip := c.ClientIP(); ip != "" {
		return ip
	}
	return c.Request.RemoteAddr
}

// NoCache 给响应加禁止缓存头（用于 index.html 与敏感的 JSON 接口）。
func NoCache() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.Next()
	}
}

// SecurityHeaders 添加一组基础安全响应头。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}
