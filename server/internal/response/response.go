// Package response 提供统一响应信封与错误码。
//
// 信封（D81：API JSON 字段用 PascalCase）：
//
//	{ "Code": 0, "Message": "ok", "Data": { ... } }
//
// 分页 Data：{ "Items": [...], "Total": 128, "Page": 1, "PageSize": 20 }
//
// ⚠️ 该信封为**内部 API**（/api/web/v1/**）专用。
// Lsky 兼容层（/api/v1/**）使用 {status, message, data} 小写信封，见 internal/lsky。
package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Code 是业务错误码。
type Code int

// 错误码表（见 docs/API.md §0）。
//
// ⚠️ 配额不足是 40302，权限不足是 40301 —— 两者不可混用（D20）。
const (
	CodeOK Code = 0

	CodeInvalidParam Code = 40001

	CodeBadCredentials Code = 40101 // 邮箱或密码错误
	CodeUnauthorized   Code = 40102 // 未登录或令牌无效
	CodeTokenExpired   Code = 40103
	CodeAccountDisabled Code = 40104

	CodeForbidden Code = 40301 // 权限不足
	CodeQuotaExceeded Code = 40302 // 配额不足（D20）

	CodeNotFound Code = 40401
	CodeConflict Code = 40901
	CodeTooMany  Code = 42901

	CodeInternal        Code = 50001
	CodeAgentUnavailable Code = 50002
	CodeUploadFailed     Code = 50003
	CodePluginFailed     Code = 50004
	CodeThemeFailed      Code = 50005
)

// HTTPStatus 返回错误码对应的 HTTP 状态码。
func (c Code) HTTPStatus() int {
	switch c {
	case CodeOK:
		return http.StatusOK
	case CodeInvalidParam:
		return http.StatusBadRequest
	case CodeBadCredentials, CodeUnauthorized, CodeTokenExpired, CodeAccountDisabled:
		return http.StatusUnauthorized
	case CodeForbidden, CodeQuotaExceeded:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict:
		return http.StatusConflict
	case CodeTooMany:
		return http.StatusTooManyRequests
	case CodeAgentUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// Message 返回错误码的默认中文提示。
func (c Code) Message() string {
	switch c {
	case CodeOK:
		return "ok"
	case CodeInvalidParam:
		return "参数校验失败"
	case CodeBadCredentials:
		return "邮箱或密码错误"
	case CodeUnauthorized:
		return "未登录或令牌无效"
	case CodeTokenExpired:
		return "令牌已过期"
	case CodeAccountDisabled:
		return "账号已被禁用"
	case CodeForbidden:
		return "权限不足"
	case CodeQuotaExceeded:
		return "存储配额不足"
	case CodeNotFound:
		return "资源不存在"
	case CodeConflict:
		return "资源冲突"
	case CodeTooMany:
		return "请求过于频繁"
	case CodeAgentUnavailable:
		return "内核（picgo-agent）不可用"
	case CodeUploadFailed:
		return "上传失败"
	case CodePluginFailed:
		return "插件操作失败"
	case CodeThemeFailed:
		return "主题操作失败"
	default:
		return "服务器内部错误"
	}
}

// Envelope 是统一响应体。
type Envelope struct {
	Code    Code `json:"Code"`
	Message string `json:"Message"`
	Data    any    `json:"Data"`
}

// PageData 是分页响应体。
type PageData struct {
	Items    any   `json:"Items"`
	Total    int64 `json:"Total"`
	Page     int   `json:"Page"`
	PageSize int   `json:"PageSize"`
}

// OK 返回成功响应（HTTP 200）。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Envelope{Code: CodeOK, Message: "ok", Data: data})
}

// OKMsg 返回带自定义消息的成功响应。
func OKMsg(c *gin.Context, message string, data any) {
	c.JSON(http.StatusOK, Envelope{Code: CodeOK, Message: message, Data: data})
}

// Page 返回分页成功响应；items 为 nil 时返回空数组而非 null。
func Page(c *gin.Context, items any, total int64, page, pageSize int) {
	if items == nil {
		items = []any{}
	}
	OK(c, PageData{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// Fail 以错误码的默认消息返回失败响应。
func Fail(c *gin.Context, code Code) {
	FailMsg(c, code, code.Message())
}

// FailMsg 以自定义消息返回失败响应。
//
// 注意：**自定义消息可能暴露内部细节**，仅用于可安全外传的提示
// （如参数校验的具体原因）。系统内部错误请用 Fail + 通用消息。
func FailMsg(c *gin.Context, code Code, message string) {
	if message == "" {
		message = code.Message()
	}
	c.JSON(code.HTTPStatus(), Envelope{Code: code, Message: message, Data: nil})
}

// Abort 返回失败响应并中断后续 handler。
func Abort(c *gin.Context, code Code) {
	Fail(c, code)
	c.Abort()
}

// AbortMsg 返回带自定义消息的失败响应并中断。
func AbortMsg(c *gin.Context, code Code, message string) {
	FailMsg(c, code, message)
	c.Abort()
}

// InvalidParam 是参数校验失败的快捷方式（带具体原因）。
func InvalidParam(c *gin.Context, reason string) {
	FailMsg(c, CodeInvalidParam, reason)
}
