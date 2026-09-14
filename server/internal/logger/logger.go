// Package logger 提供基于 log/slog 的 JSON 日志（D17）。
//
// 与「操作日志」（OperationLogs 表）区分：
//   - 本包是**进程运行日志**，写 stdout，供容器/运维查看
//   - 操作日志是**业务审计**，写数据库，供后台界面查询
package logger

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Level 解析日志级别字符串（debug/info/warn/error），非法值回退 info。
func Level(name string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New 创建 JSON 格式的 slog.Logger 并设为全局默认。
//
// dev 为真时使用带颜色的文本格式，便于本地开发阅读。
func New(levelName string, dev bool) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level:     Level(levelName),
		AddSource: false,
	}

	var h slog.Handler
	if dev {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}

	l := slog.New(h)
	slog.SetDefault(l)
	return l
}

// With 返回带附加字段的 logger。
func With(l *slog.Logger, args ...any) *slog.Logger {
	return l.With(args...)
}

// Context keys 供 middleware 注入请求级字段。
type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyUserUID
)

// WithRequestID 把 request id 放进 context。
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// RequestID 取出 request id（不存在返回空串）。
func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRequestID).(string)
	return v
}

// WithUserUID 把当前用户 UID 放进 context。
func WithUserUID(ctx context.Context, uid string) context.Context {
	return context.WithValue(ctx, ctxKeyUserUID, uid)
}

// UserUID 取出当前用户 UID（不存在返回空串）。
func UserUID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyUserUID).(string)
	return v
}
