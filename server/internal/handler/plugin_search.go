package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/YeqingKy/PicGo-Web/server/internal/response"
)

// npmSearchTimeout 单次 npm registry 搜索的超时。
//
// 比配置里的上传超时短得多：这是**交互式**操作，用户在前端等结果，
// 超过 8 秒就该报错而不是继续转圈。
const npmSearchTimeout = 8 * time.Second

// npmSearchMaxResults npm registry 单次返回上限（前端只展示前 20 条）。
const npmSearchMaxResults = 20

// npmSearchDefaultKeyword 前端打开「浏览」Tab 时的默认关键词。
const npmSearchDefaultKeyword = "picgo-plugin"

// PluginSearchItem 是 `GET /plugins/search` 的单项。
//
// 字段对齐前端 `PluginSearchItem`（web/src/types/api.ts）。
type PluginSearchItem struct {
	Name        string `json:"Name"`
	Version     string `json:"Version"`
	Description string `json:"Description"`
	Author      string `json:"Author"`
	Homepage    string `json:"Homepage"`
	Repo        string `json:"Repo"`
	// Installed 是否已安装（便于前端把「安装」按钮变成「已安装」）。
	Installed bool `json:"Installed"`
}

// Search 处理 `GET /plugins/search?Q=xxx`（admin）。
//
// ## 为什么**不**经过 picgo-agent
//
// picgo-core 没有提供 npm 搜索 API，agent 也只是个 picgo 宿主。
// 而 npm registry 的搜索是**公开 HTTP 接口**，Go 直接查更简单、少一跳、
// 也避免为搜索这种只读操作去占用 agent（它是单线程串行的上传通道）。
//
// 搜索源取自 `picgo.npmRegistry`（与安装插件时一致），因此用户配了镜像源
// 也能搜到；镜像源不支持的极端情况下回退到官方 registry。
func (h *PluginHandler) Search(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("Q"))
	if keyword == "" {
		keyword = npmSearchDefaultKeyword
	}

	registry := strings.TrimSpace(h.npmRegistry())
	if registry == "" {
		registry = "https://registry.npmjs.org"
	}

	items, err := h.searchNpm(c.Request.Context(), registry, keyword)
	if err != nil {
		// 镜像源可能不支持 /-/v1/search（部分私有源如此）；回退官方源再试一次。
		if registry != "https://registry.npmjs.org" {
			h.log.Warn("通过配置的 npm 源搜索失败，回退官方源重试",
				"registry", registry, "err", err)
			if items2, err2 := h.searchNpm(c.Request.Context(), "https://registry.npmjs.org", keyword); err2 == nil {
				items, err = items2, nil
			}
		}
		if err != nil {
			h.log.Warn("npm 搜索失败", "registry", registry, "keyword", keyword, "err", err)
			// 搜索失败对用户是「无结果」而非「服务故障」；如实给出原因便于排查
			response.FailMsg(c, response.CodeInternal, "搜索插件失败："+err.Error())
			return
		}
	}

	// 标记已安装（避免前端再发一次 /plugins 请求做比对）
	installed := h.installedSet(c.Request.Context())

	out := make([]PluginSearchItem, 0, len(items))
	for _, it := range items {
		it.Installed = installed[it.Name]
		out = append(out, it)
	}

	response.Page(c, out, int64(len(out)), 1, len(out))
}

// installedSet 返回当前已安装的插件名集合；失败时返回空集（不影响搜索主流程）。
func (h *PluginHandler) installedSet(ctx context.Context) map[string]bool {
	result, err := h.plugins.List(ctx)
	if err != nil {
		h.log.Debug("读取已安装插件失败（搜索结果的 Installed 标记将为空）", "err", err)
		return map[string]bool{}
	}
	set := make(map[string]bool, len(result.Plugins))
	for _, p := range result.Plugins {
		set[p.Name] = true
	}
	return set
}

// npmRegistry 读取当前配置的 npm 源。
func (h *PluginHandler) npmRegistry() string {
	if h.settings == nil {
		return ""
	}
	return h.settings.GetString("picgo.npmRegistry")
}

// npmSearchResponse 是 registry 搜索接口的响应（只声明用到的字段）。
type npmSearchResponse struct {
	Objects []struct {
		Package struct {
			Name        string            `json:"name"`
			Version     string            `json:"version"`
			Description string            `json:"description"`
			Links       map[string]string `json:"links"`
			Publisher   struct {
				Username string `json:"username"`
			} `json:"publisher"`
			Author struct {
				Name string `json:"name"`
			} `json:"author"`
		} `json:"package"`
		Score struct {
			Final float64 `json:"final"`
		} `json:"score"`
	} `json:"objects"`
}

// searchNpm 查询 npm registry 的搜索接口。
//
// 接口：`GET {registry}/-/v1/search?text=<kw>&size=<n>`
// npmmirror 与官方 registry 都实现了它。
func (h *PluginHandler) searchNpm(ctx context.Context, registry, keyword string) ([]PluginSearchItem, error) {
	base := strings.TrimRight(registry, "/")
	endpoint := fmt.Sprintf("%s/-/v1/search?text=%s&size=%d",
		base, url.QueryEscape(keyword), npmSearchMaxResults)

	ctx, cancel := context.WithTimeout(ctx, npmSearchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "PicGo-Web")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 npm 源失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("npm 源返回 HTTP %d", resp.StatusCode)
	}

	// 限制读取量，防止异常响应打爆内存
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var parsed npmSearchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	items := make([]PluginSearchItem, 0, len(parsed.Objects))
	for _, o := range parsed.Objects {
		pkg := o.Package
		if pkg.Name == "" {
			continue
		}
		author := pkg.Author.Name
		if author == "" {
			author = pkg.Publisher.Username
		}
		items = append(items, PluginSearchItem{
			Name:        pkg.Name,
			Version:     pkg.Version,
			Description: pkg.Description,
			Author:      author,
			Homepage:    pkg.Links["homepage"],
			Repo:        pkg.Links["repository"],
		})
	}
	return items, nil
}
