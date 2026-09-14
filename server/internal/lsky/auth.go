package lsky

import (
	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/logger"
	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// requireAPIToken 是 **Lsky 专用的**令牌鉴权中间件。
//
// # 为什么不能用 middleware.RequireAPIToken
//
// 那个中间件失败时调 `response.Abort`，产出的是**内部信封**
// `{Code, Message, Data}` —— 而 Lsky 契约要求 `{status, message, data}`，
// 且状态码语义不同（内部 40102 = 未认证；Lsky 一律 401 + "Unauthenticated."）。
//
// 若复用，第三方客户端会在「令牌无效」时解析失败（拿不到 `status` 字段），
// 可能表现为「登录成功但上传报未知错误」这种极难排查的现象（**已实测踩到**）。
//
// # 语义
//
// 只认 API Token（`pcw_...`）：Lsky 客户端是**程序**，用 Cookie 反而会让
// 「浏览器已登录」意外影响 API 语义；也避免内部 JWT 的 15 分钟过期影响长跑客户端。
func (h *Handler) requireAPIToken() gin.HandlerFunc {
	tokens := h.deps.TokenRepo
	users := h.deps.Users

	return func(c *gin.Context) {
		bearer := auth.BearerToken(c)
		if bearer == "" || !auth.IsAPIToken(bearer) {
			abortLsky(c, response.CodeUnauthorized)
			return
		}

		t, err := tokens.FindAPITokenByHash(auth.HashToken(bearer))
		if err != nil {
			if repository.IsNotFound(err) {
				abortLsky(c, response.CodeUnauthorized)
				return
			}
			h.deps.Log.Error("Lsky 鉴权：查询令牌失败", "err", err)
			abortLsky(c, response.CodeInternal)
			return
		}
		if !t.Valid() {
			abortLsky(c, response.CodeTokenExpired)
			return
		}

		u, err := users.FindByUID(t.UserUID)
		if err != nil {
			if repository.IsNotFound(err) {
				abortLsky(c, response.CodeUnauthorized)
				return
			}
			h.deps.Log.Error("Lsky 鉴权：加载用户失败", "user_uid", t.UserUID, "err", err)
			abortLsky(c, response.CodeInternal)
			return
		}
		if !u.IsActive() {
			abortLsky(c, response.CodeAccountDisabled)
			return
		}

		// 与内部一致的上下文注入：让 service/logger 能关联到该用户。
		// ⚠️ 必须走 middleware.SetCurrentUser：所有 service 都依赖 CurrentUser(c)，
		// 自己 Set 一个自定义键会让鉴权「看起来成功但后续全 401」。
		middleware.SetCurrentUser(c, u)
		ctx := logger.WithUserUID(c.Request.Context(), u.UID)
		c.Request = c.Request.WithContext(ctx)

		// 顺带更新 LastUsedAt（失败不影响请求）
		if err := tokens.TouchAPIToken(t.ID, model.Now()); err != nil {
			h.deps.Log.Debug("更新令牌 LastUsedAt 失败", "token_uid", t.UID, "err", err)
		}

		c.Next()
	}
}

// abortLsky 用 **Lsky 信封**中断请求。
func abortLsky(c *gin.Context, code response.Code) {
	status, msg := mapCode(code)
	c.AbortWithStatusJSON(status, Envelope{Status: false, Message: msg, Data: nil})
}
