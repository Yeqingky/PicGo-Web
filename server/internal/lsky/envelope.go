// Package lsky 实现 **Lsky Pro v1 API 兼容层**（docs/API.md §12，D52）。
//
// # 目的
//
// 让 PicGo 桌面端 / PicList / uPic / ShareX 等第三方图床客户端
// **直接把本项目当图床用**。生态里主流兰空插件（已核实存在）：
//
//	picgo-plugin-lankong / picgo-plugin-lskypro /
//	picgo-plugin-lsky-uploader / picgo-plugin-lskypro-own
//
// 它们把路径**硬编码**为 `${server}/api/v1/upload`、`${server}/api/v1/images/{key}`，
// 且要求 `server` 配置**不能以 `/` 结尾**。**实测**它们全部用**字符串拼接**，
// **没有一个用 `new URL()`** —— 所以挂在根级 `/api/v1` 兼容性最好
// （用户只需填**裸域名**，零后缀）。
//
// # 为什么独占 /api/v1（D80）
//
// 内部 API 全部在 `/api/web/v1/**`，两者前缀不重叠。
// 启动时仍做**路由冲突检测**（见 DetectRouteConflicts），
// 防止将来有人把内部接口误挂到 `/api/v1` 上静默覆盖 Lsky 契约。
//
// # 这是「外部冻结契约」
//
// ⚠️ **本包内的一切 JSON 字段名保持 snake_case，不受 D81（PascalCase）约束**。
// 原因：第三方客户端按 lsky 的字段名实现，改名即破坏兼容。
// 同理，响应信封是 `{status, message, data}`（**不是**内部的 `{Code, Message, Data}`）。
//
// # 鉴权复用
//
// Lsky 令牌**就是**本项目的 API Token（`APITokens` 表，D31），不另建体系。
// 客户端把 `POST /api/v1/tokens` 拿到的 `pcw_<random>` 放进 `Authorization: Bearer`。
// lsky 原版用 Sanctum 的 `1|xxxx` 形式，但插件只是**原样透传**该字符串，
// 因此任意不透明串都能工作。
package lsky

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// MessageSuccess 是成功响应的固定文案（对齐 lsky 的 "success"）。
const MessageSuccess = "success"

// Envelope 是 Lsky 响应信封。
//
// ⚠️ 字段名 snake_case 且 `status` 为 **bool** —— 与内部信封
// `{Code:int, Message, Data}` 完全不同。这是刻意的（外部冻结契约）。
type Envelope struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// PageEnvelope 是 Laravel 分页器形状（`GET /api/v1/images` 依赖它）。
//
// lsky 客户端会读 `data.data`（**双重 data**）与 `current_page` / `last_page` 等字段，
// 因此不能简化。
type PageEnvelope struct {
	Data        any   `json:"data"`
	CurrentPage int   `json:"current_page"`
	LastPage    int   `json:"last_page"`
	PerPage     int   `json:"per_page"`
	Total       int64 `json:"total"`
	From        int   `json:"from"`
	To          int   `json:"to"`
}

// ---------------------------------------------------------------------------
// 响应助手
// ---------------------------------------------------------------------------

// OK 返回成功响应。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Envelope{Status: true, Message: MessageSuccess, Data: data})
}

// OKPage 返回 Laravel 形状的分页成功响应。
func OKPage(c *gin.Context, items any, total int64, page, perPage int) {
	if items == nil {
		// Laravel 分页器在空结果时返回 `[]` 而不是 `null`
		items = []any{}
	}

	lastPage := 0
	if perPage > 0 {
		lastPage = int((total + int64(perPage) - 1) / int64(perPage))
	}

	// from/to 表示「本页实际包含的第几条到第几条」。
	//
	// ⚠️ 页码超出范围时两者都是 0（本页没有数据）。
	// 若只 clamp `to` 而不 clamp `from`，会得到 `from=41, to=5` 这种自相矛盾的值 ——
	// 客户端据此计算「显示第 41-5 条」会出错（测试覆盖了这条边界）。
	from, to := 0, 0
	if total > 0 && perPage > 0 {
		f := (page-1)*perPage + 1
		if int64(f) <= total {
			from = f
			to = page * perPage
			if int64(to) > total {
				to = int(total)
			}
		}
	}

	OK(c, PageEnvelope{
		Data:        items,
		CurrentPage: page,
		LastPage:    lastPage,
		PerPage:     perPage,
		Total:       total,
		From:        from,
		To:          to,
	})
}

// FailMsg 返回失败响应（带自定义文案）。
func FailMsg(c *gin.Context, code response.Code, message string) {
	status, defaultMsg := mapCode(code)
	if message == "" {
		message = defaultMsg
	}
	c.JSON(status, Envelope{Status: false, Message: message, Data: nil})
}

// Fail 返回失败响应（用映射后的默认文案）。
func Fail(c *gin.Context, code response.Code) {
	FailMsg(c, code, "")
}

// mapCode 把内部错误码映射到 Lsky 语义的 HTTP 状态码与文案。
//
// 映射表见 docs/API.md §12.2。**注意 40001 映射到 422**（Laravel 校验失败语义），
// 而不是 400 —— 这是 lsky 客户端的预期。
func mapCode(code response.Code) (int, string) {
	switch code {
	case response.CodeInvalidParam:
		return http.StatusUnprocessableEntity, "The given data was invalid."
	case response.CodeBadCredentials:
		// lsky 的原文案（Laravel 的 auth.failed）
		return http.StatusUnauthorized, "These credentials do not match our records."
	case response.CodeUnauthorized, response.CodeTokenExpired:
		return http.StatusUnauthorized, "Unauthenticated."
	case response.CodeAccountDisabled:
		return http.StatusForbidden, "Account disabled."
	case response.CodeForbidden:
		return http.StatusForbidden, "Forbidden."
	case response.CodeQuotaExceeded:
		return http.StatusForbidden, "Insufficient storage capacity."
	case response.CodeNotFound:
		return http.StatusNotFound, "Not Found."
	case response.CodeConflict:
		return http.StatusConflict, "Conflict."
	case response.CodeTooMany:
		return http.StatusTooManyRequests, "Too Many Requests."
	case response.CodeAgentUnavailable:
		return http.StatusServiceUnavailable, "Service Unavailable."
	default:
		return http.StatusInternalServerError, "Server Error."
	}
}

// FailInternal 是未预期错误的统一出口（不泄露内部细节）。
func FailInternal(c *gin.Context) {
	Fail(c, response.CodeInternal)
}
