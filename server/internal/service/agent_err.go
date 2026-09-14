package service

import (
	"errors"

	"github.com/YeqingKy/PicGo-Web/server/internal/agent"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// fromAgent 把 agent 调用错误转换成 service 层业务错误。
//
// 为什么要这一层转换：`service.CodeOf` 只认 `*service.Error`，
// 其余一律归为 50001。agent 的错误自带明确的语义（参数错 / 找不到 / 内核不可用），
// 直接落成 50001 会让「图床名写错了」这种调用方错误显示成「服务器内部错误」。
func fromAgent(err error) error {
	if err == nil {
		return nil
	}
	var ae *agent.Error
	if errors.As(err, &ae) {
		code := ae.Code
		if code == response.CodeOK {
			code = response.CodeInternal
		}
		return &Error{Code: code, Message: ae.Message, Cause: err}
	}
	return Wrap(response.CodeInternal, "", err)
}

// isAgentUnavailable 判断错误是否为「内核不可用」。
func isAgentUnavailable(err error) bool {
	return agent.IsUnavailable(err)
}
