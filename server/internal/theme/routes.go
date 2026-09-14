package theme

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/webfs"
)

// ---- 注册入口 ----
//
// 路由注册刻意放在 theme 包内（B8 明确允许「或直接放在 theme 包里」）：
// 本模块（清单解析 → 主题目录 → 配置 → 安装 → 分发 → HTTP 表面）是一个自洽的
// 垂直切片，放在一起可以独立编译与测试，不需要 handler 包先行就绪。
// server.go 只负责注入鉴权中间件与依赖（保持最小改动）。

// SiteRoutesOptions 是公开站点路由的依赖。
type SiteRoutesOptions struct {
	// Version 程序版本，回填到 Site.Version。
	Version string
}

// RegisterSiteRoutes 注册**无需鉴权**的公开端点。
//
// `GET /api/web/v1/site/config` —— 前端首屏调用一次（D83/D94/D95）。
func (s *Service) RegisterSiteRoutes(api *gin.RouterGroup, opts SiteRoutesOptions) {
	h := &siteHandler{svc: s, version: opts.Version}
	api.GET("/site/config", h.getSiteConfig)
}

// AdminRoutesOptions 是后台主题路由的依赖。
type AdminRoutesOptions struct {
	// Middlewares 由 server 注入（RequireAuth → RequirePasswordChanged → RequireAdmin）。
	//
	// 用注入而不是在包内 import middleware：避免本包与鉴权实现耦合，
	// 也让单测可以用空中间件直接测 handler。
	Middlewares []gin.HandlerFunc
}

// RegisterAdminRoutes 注册后台主题管理端点（全部需要 admin）。
//
// 端点清单见 docs/API.md §10。
func (s *Service) RegisterAdminRoutes(api *gin.RouterGroup, opts AdminRoutesOptions) {
	h := &themeHandler{svc: s}

	group := api.Group("/themes", opts.Middlewares...)
	group.GET("", h.list)
	group.POST("/rescan", h.rescan)
	group.POST("/install", h.install)
	group.PUT("/active", h.setActive)
	group.DELETE("/:themeID", h.uninstall)
	group.GET("/:themeID/settings", h.getSettings)
	group.PUT("/:themeID/settings", h.updateSettings)
	group.DELETE("/:themeID/settings", h.clearSettings)
	group.GET("/:themeID/screenshot", h.screenshot)
}

// RegisterAssetRoutes 注册主题的静态资源与 favicon（D99.2）。
//
// 挂在 engine 上（不是 /api 分组），因为它们是页面级资源。
//
//   - `GET /theme-assets/**` → **当前主题**的 assets/
//   - `GET /favicon.ico`     → 优先当前主题，缺失回退内置 SPA
//
// ⚠️ `/assets/**` **不在这里**：它永远属于内置 SPA（webfs），
// 主题不得占用，否则会覆盖登录页与后台的资源。
func (s *Service) RegisterAssetRoutes(engine *gin.Engine) {
	engine.GET("/theme-assets/*filepath", s.serveThemeAsset)
	engine.GET("/favicon.ico", s.serveFavicon)
}

// serveThemeAsset 托管当前主题的静态资源。
func (s *Service) serveThemeAsset(c *gin.Context) {
	rel := strings.TrimPrefix(c.Param("filepath"), "/")

	data, ctype, err := s.CurrentAsset(rel)
	if err != nil {
		// 越界与不存在都按 40401 处理：不暴露主题目录结构，
		// 也不参与 SPA 回退（把 HTML 当 JS 返回会引发难查的模块加载错误）
		response.Fail(c, response.CodeNotFound)
		return
	}

	c.Header("Cache-Control", webfs.CacheImmutable)
	c.Data(http.StatusOK, ctype, data)
}

// serveFavicon 优先当前主题的 favicon，缺失回退内置 SPA。
func (s *Service) serveFavicon(c *gin.Context) {
	if data, ctype, err := s.CurrentFavicon(); err == nil {
		c.Header("Cache-Control", webfs.CacheFavicon)
		c.Data(http.StatusOK, ctype, data)
		return
	}
	if data, ctype, ok := webfs.Favicon(); ok {
		c.Header("Cache-Control", webfs.CacheFavicon)
		c.Data(http.StatusOK, ctype, data)
		return
	}
	response.Fail(c, response.CodeNotFound)
}

// ServePage 是 NoRoute 的实现：按 D99.1 决定由主题还是内置 SPA 渲染。
//
// 约定（server.go 在调用它之前已保证）：
//   - `/api/**`、`/healthz`、`/assets/**`、`/theme-assets/**`、`/themes/**`、`/favicon.ico`
//     都已注册为独立路由，**不会**走到 NoRoute
//   - 因此这里只需处理「页面路径」
func ServePage(svc *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		// `/api/**` 未匹配 → 40401 JSON，**不参与** SPA 回退。
		//
		// 必须在 SPA 回退之前拦下：否则一个拼错的接口路径会返回
		// 200 + HTML，客户端很难看出自己调错了地址。
		if strings.HasPrefix(c.Request.URL.Path, "/api") {
			response.Fail(c, response.CodeNotFound)
			return
		}

		// 只有 GET/HEAD 参与 SPA 回退；其它方法返回 404
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			response.Fail(c, response.CodeNotFound)
			return
		}

		decision := svc.Decide(c.Request.URL.Path)

		if decision.Target == TargetTheme && decision.Theme != nil {
			index, err := svc.CurrentIndex()
			if err == nil {
				c.Header("Cache-Control", webfs.CacheNoCache)
				c.Data(http.StatusOK, "text/html; charset=utf-8", index)
				return
			}
			// 主题有效但 index.html 读不到（被手工删了）→ 记录并回退内置 SPA，
			// 这比返回 500 或空白页对用户更友好
			svc.log.Warn("主题 index.html 不可读，回退内置 SPA",
				"theme", decision.Theme.ID, "err", err)
		}

		webfs.ServeSPA()(c)
	}
}

// ===========================================================================
// 公开站点信息
// ===========================================================================

// SiteInfo 是 `/site/config` 的站点段。
type SiteInfo struct {
	Name        string `json:"Name"`
	Subtitle    string `json:"Subtitle"`
	Description string `json:"Description"`
	Keywords    string `json:"Keywords"`
	IconURL     string `json:"IconURL"`
	Notice      string `json:"Notice"`
	Icp         string `json:"Icp"`
	BaseURL     string `json:"BaseURL"`
	Version     string `json:"Version"`
}

// SiteFeatures 是前端用来隐藏入口的开关。
type SiteFeatures struct {
	// GitHubOAuthEnabled 控制登录页是否显示 GitHub 按钮（D26/D27）。
	GitHubOAuthEnabled bool `json:"GitHubOAuthEnabled"`
	// AllowSelfRegistration 恒为 false（D25：不做自助注册），
	// 前端据此隐藏注册入口。保留字段是为了让前端不必特殊处理缺字段。
	AllowSelfRegistration bool `json:"AllowSelfRegistration"`
}

// SiteConfig 是 `GET /site/config` 的完整响应数据。
type SiteConfig struct {
	Site     SiteInfo         `json:"Site"`
	Theme    *PublicThemeInfo `json:"Theme"`
	Features SiteFeatures     `json:"Features"`

	// ThemeError 在「当前主题不可用、已回退内嵌默认主题」时说明原因。
	//
	// 说明：因为二进制内嵌了默认主题（D94「永不白屏」），`Theme` 实际
	// 几乎不会为 null —— 回退时我们仍然返回**内嵌主题**的信息（让页面能正常渲染），
	// 同时用本字段告知前端「你配置的主题没生效」。只有连内嵌副本都不可用时
	// （构建事故）`Theme` 才会是 null。
	ThemeError string `json:"ThemeError,omitempty"`
}

type siteHandler struct {
	svc     *Service
	version string
}

// getSiteConfig 返回站点公开信息 + 当前主题的元数据与设置。
//
// **绝不返回敏感信息**：无 SMTP、无 OAuth Secret、无密钥。
// 主题配置项类型里也没有 password/secret（D98），因此整份 Settings 可安全外传。
func (h *siteHandler) getSiteConfig(c *gin.Context) {
	lang := PreferenceLanguage(c.GetHeader("Accept-Language"))
	sp := h.svc.settings

	get := func(key, def string) string {
		if sp == nil {
			return def
		}
		if v := sp.GetString(key); v != "" {
			return v
		}
		return def
	}
	getBool := func(key string, def bool) bool {
		if sp == nil {
			return def
		}
		return sp.GetBool(key, def)
	}

	out := SiteConfig{
		Site: SiteInfo{
			Name:        get("site.name", "PicGo Web"),
			Subtitle:    get("site.subtitle", ""),
			Description: get("site.description", ""),
			Keywords:    get("site.keywords", ""),
			IconURL:     get("site.iconUrl", ""),
			Notice:      get("site.notice", ""),
			Icp:         get("site.icp", ""),
			BaseURL:     get("site.baseUrl", ""),
			Version:     h.version,
		},
		Features: SiteFeatures{
			GitHubOAuthEnabled: getBool("oauth.github.enabled", false),
			// D25：不做自助注册，恒为 false
			AllowSelfRegistration: false,
		},
	}

	themeInfo, err := h.svc.PublicTheme(lang)
	if err != nil {
		out.ThemeError = err.Error()
	} else {
		out.Theme = themeInfo
		if th := h.svc.Current(); th != nil && th.Fallback {
			out.ThemeError = "当前主题不可用，已回退内置默认主题：" + th.FallbackReason
		}
	}

	response.OK(c, out)
}

// ===========================================================================
// 后台主题管理
// ===========================================================================

type themeHandler struct {
	svc *Service
}

// langOf 解析请求语言（用于多语言 manifest 文本）。
func langOf(c *gin.Context) string {
	return PreferenceLanguage(c.GetHeader("Accept-Language"))
}

// list 列出全部主题。
func (h *themeHandler) list(c *gin.Context) {
	response.OK(c, h.svc.List(langOf(c)))
}

// rescan 重新扫描主题目录。
func (h *themeHandler) rescan(c *gin.Context) {
	response.OK(c, h.svc.Rescan(c.Request.Context(), langOf(c), currentUserUID(c)))
}

// install 从 zip 安装主题（multipart）。
func (h *themeHandler) install(c *gin.Context) {
	fileHeader, err := c.FormFile("File")
	if err != nil {
		response.InvalidParam(c, "缺少 File 字段（multipart 的 zip 包）")
		return
	}

	overwrite := parseBool(c.PostForm("Overwrite"))

	fh, err := fileHeader.Open()
	if err != nil {
		response.FailMsg(c, response.CodeInvalidParam, "无法读取上传文件")
		return
	}
	defer func() { _ = fh.Close() }()

	readerAt, size, err := asReaderAt(fh, fileHeader.Size)
	if err != nil {
		failTheme(c, err)
		return
	}

	// 审计在 theme.Service.Install 内统一写入（成功与失败都写，D96 第 9 条）
	res, err := h.svc.Install(c.Request.Context(), InstallRequest{
		File: readerAt, Size: size, Overwrite: overwrite,
	})
	if err != nil {
		failTheme(c, err)
		return
	}

	response.OK(c, gin.H{
		"ID":        res.ID,
		"Name":      res.Name,
		"Version":   res.Version,
		"Installed": true,
	})
}

// setActive 切换当前主题（立即生效）。
func (h *themeHandler) setActive(c *gin.Context) {
	var req struct {
		ThemeID string `json:"ThemeID"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体需为 {\"ThemeID\": \"...\"}")
		return
	}

	previous := h.svc.Active()
	if err := h.svc.SetActive(c.Request.Context(), req.ThemeID, currentUserUID(c)); err != nil {
		failTheme(c, err)
		return
	}
	response.OK(c, gin.H{"Active": req.ThemeID, "Previous": previous})
}

// uninstall 卸载主题。
func (h *themeHandler) uninstall(c *gin.Context) {
	id := c.Param("themeID")
	if err := h.svc.Uninstall(c.Request.Context(), id, currentUserUID(c)); err != nil {
		failTheme(c, err)
		return
	}
	response.OK(c, nil)
}

// getSettings 返回某主题的配置 schema + 当前值。
func (h *themeHandler) getSettings(c *gin.Context) {
	view, err := h.svc.SettingsView(c.Param("themeID"), langOf(c))
	if err != nil {
		failTheme(c, err)
		return
	}
	response.OK(c, view)
}

// updateSettings 写入主题配置。
func (h *themeHandler) updateSettings(c *gin.Context) {
	var req struct {
		Values map[string]any `json:"Values"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.InvalidParam(c, "请求体需为 {\"Values\": { ... }}")
		return
	}
	if req.Values == nil {
		response.InvalidParam(c, "Values 不能为空")
		return
	}

	n, err := h.svc.UpdateSettings(c.Request.Context(), c.Param("themeID"), req.Values, currentUserUID(c))
	if err != nil {
		failTheme(c, err)
		return
	}
	response.OK(c, gin.H{"Updated": n})
}

// clearSettings 清理某主题的全部配置值。
func (h *themeHandler) clearSettings(c *gin.Context) {
	n, err := h.svc.ClearSettings(c.Request.Context(), c.Param("themeID"), currentUserUID(c))
	if err != nil {
		failTheme(c, err)
		return
	}
	response.OK(c, gin.H{"Deleted": n})
}

// screenshot 返回主题预览图。
func (h *themeHandler) screenshot(c *gin.Context) {
	data, ctype, err := h.svc.Screenshot(c.Param("themeID"))
	if err != nil {
		failTheme(c, err)
		return
	}
	c.Header("Cache-Control", webfs.CacheFavicon)
	c.Data(http.StatusOK, ctype, data)
}

// ===========================================================================
// 辅助
// ===========================================================================

// currentUserUID 取当前登录用户 UID（未登录返回空串）。
//
// 用 middleware 的规范访问器，而不是硬编码 gin.Context 的键名：
// 键名是 middleware 的内部实现细节，复制一份字符串会在重构时静默失效。
func currentUserUID(c *gin.Context) string {
	return middleware.CurrentUserUID(c)
}

// parseBool 解析表单布尔值（"1" / "true" / "on" / "yes" 为真）。
func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes", "y":
		return true
	default:
		return false
	}
}

// asReaderAt 把上传文件转成 io.ReaderAt + 精确长度（zip.NewReader 需要两者）。
//
// 正常路径：multipart 的文件实现已经支持随机读，且 FormFile 给出了 Size，直接复用。
// 退化路径：拿不到 ReaderAt 或 Size 时读进内存（带**硬上限**，防超大文件打爆内存）。
func asReaderAt(r io.Reader, size int64) (io.ReaderAt, int64, error) {
	if ra, ok := r.(io.ReaderAt); ok && size > 0 {
		return ra, size, nil
	}

	data, err := io.ReadAll(io.LimitReader(r, defaultMaxPackageBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(data)) > defaultMaxPackageBytes {
		return nil, 0, ErrPackageTooLarge
	}
	return bytes.NewReader(data), int64(len(data)), nil
}

// failTheme 把主题域错误映射到统一错误码。
func failTheme(c *gin.Context, err error) {
	if err == nil {
		return
	}

	switch {
	case errors.Is(err, ErrNotFound):
		response.FailMsg(c, response.CodeNotFound, err.Error())

	case errors.Is(err, ErrThemeExists),
		errors.Is(err, ErrThemeActive),
		errors.Is(err, ErrBuiltinTheme),
		errors.Is(err, ErrSettingsInUse):
		response.FailMsg(c, response.CodeConflict, err.Error())

	case errors.Is(err, ErrThemeInvalid),
		errors.Is(err, ErrManifestMissing),
		errors.Is(err, ErrManifestInvalid),
		errors.Is(err, ErrManifestTooLarge),
		errors.Is(err, ErrIndexMissing),
		errors.Is(err, ErrArchiveInvalid),
		errors.Is(err, ErrZipSlip),
		errors.Is(err, ErrZipSymlink),
		errors.Is(err, ErrPackageTooLarge),
		errors.Is(err, ErrExtractTooLarge),
		errors.Is(err, ErrSingleFileTooLarge),
		errors.Is(err, ErrTooManyFiles),
		errors.Is(err, ErrSettingUnknown),
		errors.Is(err, ErrSettingType):
		response.FailMsg(c, response.CodeInvalidParam, err.Error())

	default:
		response.FailMsg(c, response.CodeThemeFailed, err.Error())
	}
}
