package handler

import (
	"encoding/json"
	"log/slog"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/config"
	"github.com/YeqingKy/PicGo-Web/server/internal/middleware"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/service"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// SettingsDeps 是设置路由的依赖。
type SettingsDeps struct {
	Settings *settings.Service
	SetRepo  *repository.SettingRepo
	Audit    *service.AuditService
	AuthMW   *middleware.Auth
	Log      *slog.Logger
}

// SettingsHandler 处理 `docs/API.md` §11 的设置端点。
//
// 两层语义（**不可混**）：
//   - `GET/PUT /settings`        用户级（落 `UserSettings`），所有登录用户
//   - `GET/PUT /settings/system` 站点级（落 `SystemSettings`），**仅 admin**
//
// 注意：`POST /settings/mail/test` 由 W6 的 EmailHandler 注册，本 handler **不重复注册**。
type SettingsHandler struct {
	deps SettingsDeps
}

// NewSettingsHandler 构造。
func NewSettingsHandler(deps SettingsDeps) *SettingsHandler {
	return &SettingsHandler{deps: deps}
}

// Register 挂载设置路由（`api` 已带 `/api/web/v1` 前缀且已 apply 鉴权中间件）。
func (h *SettingsHandler) Register(api *gin.RouterGroup) {
	g := api.Group("/settings")
	{
		g.GET("", h.GetUserSettings)
		g.PUT("", h.UpdateUserSettings)
		g.GET("/system", h.deps.AuthMW.RequireAdmin(), h.GetSystemSettings)
		g.PUT("/system", h.deps.AuthMW.RequireAdmin(), h.UpdateSystemSettings)
	}
}

// ------------------------------------------------------------------ 用户级

// userSettingAllowedPrefixes 是允许用户自定义的键前缀。
//
// ⚠️ 严格白名单：用户级设置**不得**触及站点级配置，
// 否则普通用户就能改 SMTP、OAuth Secret、上传限流等（提权）。
const userSettingAllowedPrefixes = "user. ui."

// GetUserSettings 处理 `GET /settings`。
//
// 一次性返回「站点公开信息 + 上传限制 + 功能开关 + 当前用户的用户级设置」，
// 让前端首屏只发一个请求（这些数据在多个页面都要用）。
func (h *SettingsHandler) GetUserSettings(c *gin.Context) {
	s := h.deps.Settings

	userValues, err := h.listUserSettings(middleware.CurrentUserUID(c))
	if err != nil {
		h.deps.Log.Error("读取用户设置失败", "err", err)
		response.Fail(c, response.CodeInternal)
		return
	}

	response.OK(c, gin.H{
		"Site": gin.H{
			"name":                  s.GetString("site.name"),
			"subtitle":              s.GetString("site.subtitle"),
			"description":           s.GetString("site.description"),
			"notice":                s.GetString("site.notice"),
			"icp":                   s.GetString("site.icp"),
			"baseUrl":               s.GetString("site.baseUrl"),
			"iconUrl":               s.GetString("site.iconUrl"),
			"allowSelfRegistration": s.GetBool("user.allowSelfRegistration", false),
		},
		"Upload": gin.H{
			"maxSizeBytes": s.GetInt("upload.maxSizeBytes", 20<<20),
			"allowedExts":  s.GetStringSlice("upload.allowedExts", nil),
			"blockSvg":     s.GetBool("upload.blockSvg", true),
		},
		"Features": gin.H{
			"oauthGithubEnabled": s.GetBool("oauth.github.enabled", false),
			"mailEnabled":        s.GetBool("mail.enabled", false),
		},
		"User": userValues,
	})
}

// UpdateUserSettings 处理 `PUT /settings`。
//
// body 形如 `{"ui.theme":"dark","sidebar.collapsed":true}`。
// 白名单外的键**不报错**，而是收集到 `Ignored` 返回 —— 前端可据此提示，
// 且避免了「一个脏键让整次提交失败」的糟糕体验。
func (h *SettingsHandler) UpdateUserSettings(c *gin.Context) {
	var payload map[string]any
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.InvalidParam(c, "请求体必须是 JSON 对象")
		return
	}

	userUID := middleware.CurrentUserUID(c)
	applied := make([]string, 0, len(payload))
	ignored := make([]string, 0)

	// 排序保证响应稳定（便于前端 diff 与测试断言）
	for _, k := range sortedKeys(payload) {
		if !isUserSettingAllowed(k) {
			ignored = append(ignored, k)
			continue
		}
		raw, err := json.Marshal(payload[k])
		if err != nil {
			ignored = append(ignored, k)
			continue
		}
		now := model.Now()
		rec := &model.UserSetting{
			UserUID:   userUID,
			Key:       k,
			Value:     string(raw),
			ValueType: "json",
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := h.deps.SetRepo.UpsertUser(rec); err != nil {
			h.deps.Log.Error("写入用户设置失败", "key", k, "err", err)
			ignored = append(ignored, k)
			continue
		}
		applied = append(applied, k)
	}

	response.OK(c, gin.H{"Applied": applied, "Ignored": ignored})
}

func isUserSettingAllowed(key string) bool {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return false
	}
	for _, prefix := range strings.Fields(userSettingAllowedPrefixes) {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func (h *SettingsHandler) listUserSettings(userUID string) (map[string]any, error) {
	rows, err := h.deps.SetRepo.ListUser(userUID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(rows))
	for _, r := range rows {
		var v any
		if err := json.Unmarshal([]byte(r.Value), &v); err != nil {
			out[r.Key] = r.Value // 非 JSON（历史数据）原样返回
			continue
		}
		out[r.Key] = v
	}
	return out, nil
}

// ------------------------------------------------------------------ 站点级

// systemSettingGroupLabel 是分类的中文标签（后台设置页的 Tab 名）。
var systemSettingGroupLabel = map[config.Category]string{
	config.CategorySite:        "站点",
	config.CategoryTheme:       "主题",
	config.CategoryUser:        "用户",
	config.CategoryUpload:      "上传",
	config.CategoryMail:        "邮件",
	config.CategoryOAuth:       "登录方式",
	config.CategorySecurity:    "安全",
	config.CategoryLog:         "日志",
	config.CategoryPicgo:       "PicGo 内核",
	config.CategoryIntegration: "对外集成",
}

// GetSystemSettings 处理 `GET /settings/system`（admin）。
//
// 返回**全部**站点配置键（含未落库、使用默认值的），按分类分组。
// `secret` 类型的 `Value` 恒为掩码，另有 `HasValue` 表示是否已设置（**不泄露值**）。
func (h *SettingsHandler) GetSystemSettings(c *gin.Context) {
	s := h.deps.Settings

	groups := make([]gin.H, 0, len(systemSettingGroupLabel))
	for _, cat := range s.Categories() {
		items := s.ByCategory(cat)
		keys := make([]gin.H, 0, len(items))
		for _, it := range items {
			entry := gin.H{
				"Key":         it.Key,
				"Type":        it.Type,
				"Value":       it.Value,
				"Default":     it.Default,
				"Source":      string(it.Source),
				"Secret":      it.Secret,
				"Description": it.Description,
			}
			if it.Secret {
				entry["HasValue"] = it.HasValue
			}
			keys = append(keys, entry)
		}
		groups = append(groups, gin.H{
			"Category": string(cat),
			"Label":    systemSettingGroupLabel[cat],
			"Keys":     keys,
		})
	}

	rows, err := h.deps.SetRepo.ListSystem("")
	if err != nil {
		h.deps.Log.Error("统计已落库的配置键失败", "err", err)
		response.Fail(c, response.CodeInternal)
		return
	}
	var updatedAt int64
	for _, r := range rows {
		if r.UpdatedAt > updatedAt {
			updatedAt = r.UpdatedAt
		}
	}

	response.OK(c, gin.H{
		"Groups": groups,
		"Meta": gin.H{
			"SchemaKeyCount": len(config.Keys),
			"DBKeyCount":     len(rows),
			"UpdatedAt":      updatedAt,
		},
	})
}

// UpdateSystemSettings 处理 `PUT /settings/system`（admin）。
//
// 未注册的键会被 `SettingsService.Set` 拒绝（防脏写）；这里把它们收集到
// `Rejected` 一并返回，而不是让整次提交失败 —— 管理员经常一次改多个分组。
func (h *SettingsHandler) UpdateSystemSettings(c *gin.Context) {
	var payload map[string]any
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.InvalidParam(c, "请求体必须是 JSON 对象")
		return
	}

	by := middleware.CurrentUserUID(c)
	applied := make([]string, 0, len(payload))
	effects := make([]string, 0, len(payload))
	rejected := make(map[string]string)

	for _, k := range sortedKeys(payload) {
		if err := h.deps.Settings.Set(k, payload[k], by); err != nil {
			rejected[k] = err.Error()
			continue
		}
		applied = append(applied, k)
		if effect := systemSettingEffect(k); effect != "" {
			effects = append(effects, effect)
		}
	}

	if len(applied) > 0 && h.deps.Audit != nil {
		h.deps.Audit.Log(c.Request.Context(), service.AuditEntry{
			Type:       model.LogTypeSettingUpdate,
			Status:     model.LogStatusSuccess,
			TargetType: "setting",
			Detail:     map[string]any{"Applied": applied},
		})
	}

	body := gin.H{"Applied": applied, "Effects": effects}
	if len(rejected) > 0 {
		body["Rejected"] = rejected
	}
	response.OK(c, body)
}

// systemSettingEffect 把「改了哪些键」翻译成「会有什么效果」。
//
// 用途：部分配置**改了不会立即生效**（如并发度需重启、npm 源下次装插件才用），
// 前端据此提示用户，避免「明明保存了却没变化」的困惑。
func systemSettingEffect(key string) string {
	switch {
	case strings.HasPrefix(key, "oauth."):
		return "GitHub 登录设置已更新，立即生效"
	case strings.HasPrefix(key, "mail."):
		return "邮件设置已更新，建议发送测试邮件验证"
	case strings.HasPrefix(key, "picgo.npm"):
		return "npm 源已更新，重新安装插件时生效"
	case strings.HasPrefix(key, "picgo.uploadProxy"):
		return "上传代理已更新，下一次上传生效"
	case strings.HasPrefix(key, "upload.rateLimit"):
		return "上传限流设置已更新，立即生效"
	case strings.HasPrefix(key, "upload.concurrency"):
		// 并发度会立即调整 worker 池（UploadService.RefreshConcurrency），
		// 不需要重启 —— 但**补丁缺失时会强制降级为 1**（见 UploadService.concurrency）
		return "上传并发度已更新，立即生效（内核补丁缺失时会自动降为单并发）"
	case strings.HasPrefix(key, "log."):
		return "日志保留策略已更新，下次清理任务生效"
	case key == "theme.active":
		return "已切换主题，刷新页面即可看到"
	case strings.HasPrefix(key, "security."):
		return "安全设置已更新，对新的登录与令牌生效"
	default:
		return ""
	}
}

// ------------------------------------------------------------------ 助手

// sortedKeys 返回 map 的有序键，保证响应稳定（便于前端 diff 与测试）。
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
