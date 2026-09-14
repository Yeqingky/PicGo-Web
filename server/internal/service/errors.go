// Package service 承载业务规则与事务。
//
// 分层铁律（D77.2）：handler → service → repository。
// handler 只做「解析入参 / 调 service / 写响应」，**不得出现 *gorm.DB**。
package service

import (
	"errors"
	"fmt"

	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// Error 是 service 层向 handler 传递的**带错误码的业务错误**。
//
// handler 用 CodeOf 取出错误码并写响应；未识别的错误一律按 50001 处理，
// 从而不会把内部细节泄露给客户端。
type Error struct {
	Code    response.Code
	Message string
	Cause   error
}

// Error 实现 error。
func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code.Message()
}

// Unwrap 支持 errors.Is / errors.As 追溯底层原因。
func (e *Error) Unwrap() error { return e.Cause }

// NewError 构造业务错误（Message 为空时用错误码的默认文案）。
func NewError(code response.Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Errorf 构造带格式化消息的业务错误。
func Errorf(code response.Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap 构造带底层原因的业务错误。
func Wrap(code response.Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

// CodeOf 提取错误码；非业务错误一律视为 50001。
func CodeOf(err error) response.Code {
	if err == nil {
		return response.CodeOK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return response.CodeInternal
}

// MessageOf 提取可安全外传的错误消息。
//
// ⚠️ 只对**业务错误**返回其 Message；系统错误一律返回通用文案，
// 避免把数据库/网络细节暴露给客户端。
func MessageOf(err error) string {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		if e.Message != "" {
			return e.Message
		}
		return e.Code.Message()
	}
	return response.CodeInternal.Message()
}
