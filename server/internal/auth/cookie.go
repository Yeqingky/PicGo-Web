package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Cookie 名称（D30）。
const (
	CookieAccess  = "pcw_at"
	CookieRefresh = "pcw_rt"
)

// cookiePath 统一为 "/"，让 /api 与前端路由都能带上。
const cookiePath = "/"

// safeCookieValue 去掉 cookie 值里 Gin 会用 url.QueryEscape 处理的字符带来的隐患。
//
// Gin 的 SetCookie 会 QueryEscape、Cookie() 会 QueryUnescape，往返是干净的；
// 这里只是把空值挡掉，避免写入一个空 cookie。
func safeCookieValue(v string) string { return strings.TrimSpace(v) }

// SetAuthCookies 下发 access / refresh 两个 httpOnly Cookie。
//
// D30 要求：HttpOnly + SameSite=Lax + Path=/；生产环境（HTTPS）下额外加 Secure。
func SetAuthCookies(c *gin.Context, access string, accessTTL time.Duration, refresh string, refreshTTL time.Duration, secure bool) {
	if v := safeCookieValue(access); v != "" {
		c.SetSameSite(cookieSameSite())
		c.SetCookie(CookieAccess, v, int(accessTTL.Seconds()), cookiePath, "", secure, true)
	}
	if v := safeCookieValue(refresh); v != "" {
		c.SetSameSite(cookieSameSite())
		c.SetCookie(CookieRefresh, v, int(refreshTTL.Seconds()), cookiePath, "", secure, true)
	}
}

// ClearAuthCookies 清除两个 Cookie（maxAge = -1）。
func ClearAuthCookies(c *gin.Context, secure bool) {
	c.SetSameSite(cookieSameSite())
	c.SetCookie(CookieAccess, "", -1, cookiePath, "", secure, true)
	c.SetSameSite(cookieSameSite())
	c.SetCookie(CookieRefresh, "", -1, cookiePath, "", secure, true)
}

// cookieSameSite 用 Lax：既防 CSRF，又不影响从外部链接跳回来时保持登录。
func cookieSameSite() http.SameSite { return http.SameSiteLaxMode }

// AccessCookie 读 pcw_at，不存在返回空串。
func AccessCookie(c *gin.Context) string {
	v, err := c.Cookie(CookieAccess)
	if err != nil {
		return ""
	}
	return v
}

// RefreshCookie 读 pcw_rt，不存在返回空串。
func RefreshCookie(c *gin.Context) string {
	v, err := c.Cookie(CookieRefresh)
	if err != nil {
		return ""
	}
	return v
}

// BearerToken 从 Authorization 头取 Bearer 令牌；不存在返回空串。
//
// 头部形如 `Authorization: Bearer <token>`（scheme 大小写不敏感）。
func BearerToken(c *gin.Context) string {
	h := strings.TrimSpace(c.GetHeader("Authorization"))
	if h == "" {
		return ""
	}
	const scheme = "bearer "
	if len(h) <= len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return ""
	}
	return strings.TrimSpace(h[len(scheme):])
}
