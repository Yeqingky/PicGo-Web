package theme

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
)

// Target 表示一个页面路径应由谁渲染。
type Target int

const (
	// TargetSPA 由**内置 SPA** 渲染（默认，也是认证页与后台的唯一归属）。
	TargetSPA Target = iota
	// TargetTheme 由**当前主题**渲染。
	TargetTheme
)

// Decision 是一次路由判定的结果。
type Decision struct {
	Target Target
	// Theme 仅在 Target == TargetTheme 时非 nil。
	Theme *Theme
	// MatchedPage 命中的 Pages 项（便于日志排查）。
	MatchedPage string
}

// Decide 按 D99.1 判定一个页面路径由谁渲染。
//
// 判定顺序（**保留路径优先**，这是安全底线）：
//
//  1. 认证页保留列表 → 内置 SPA
//  2. `/admin/**`     → 内置 SPA
//  3. 命中当前主题的 Pages（**最长前缀匹配**）→ 主题
//  4. 其他            → 内置 SPA（SPA 回退）
//
// 文档（D99.1 / OPERATIONS §8.5）把 `/admin/**` 放在主题判定之后，
// 但**清单校验（ValidatePages）已保证主题不可能注册到这两类路径**；
// 这里把它们前置只是**纵深防御**——即使校验被绕过，登录页与后台也不会落到第三方代码上。
//
// 只接受 GET 类页面请求；调用方需在此之前拦掉 `/api/**` 与静态资源前缀。
func (s *Service) Decide(reqPath string) Decision {
	if reqPath == "" {
		reqPath = "/"
	}
	// 规范化：去掉 query 与尾部斜杠（`/gallery/` 等价于 `/gallery`）
	clean := normalizePagePath(reqPath)

	// 1) 认证页：永远内置
	if IsAuthPage(clean) {
		return Decision{Target: TargetSPA}
	}
	// 2) 后台：永远内置
	if IsAdminPath(clean) {
		return Decision{Target: TargetSPA}
	}

	// 3) 主题接管范围（最长前缀匹配）
	th := s.Current()
	if th != nil {
		if page, ok := MatchPage(th.Pages(), clean); ok {
			return Decision{Target: TargetTheme, Theme: th, MatchedPage: page}
		}
	}

	// 4) 回退到内置 SPA
	return Decision{Target: TargetSPA}
}

// normalizePagePath 归一化页面路径：去 query/fragment、去重复斜杠、去尾部斜杠。
//
// 根路径统一返回 "/"。
func normalizePagePath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	// path.Clean 会去掉重复斜杠与 `.` / `..`，并保证以 / 开头（输入已是 / 开头）
	p = path.Clean(p)
	if p == "." || p == "" {
		return "/"
	}
	return p
}

// ---- 主题静态资源 ----

// CurrentAsset 读取**当前主题** `assets/` 下的一个文件。
//
// 用于 `GET /theme-assets/**`（D99.2）。
//
// 安全与降级规则：
//   - 路径规范化后必须落在当前主题的 `assets/` 内，否则返回 ErrNotFound（**防 `..` 穿越**）
//   - 主题**有效**但文件不存在 → ErrNotFound（**不**回退内嵌主题的同名文件：
//     否则「破主题」会被喂上别的主题的代码，问题更难查 —— OPERATIONS §8.6）
//   - 主题**无效**时 Current() 已回退内嵌主题，资源自然来自内嵌副本
func (s *Service) CurrentAsset(rel string) ([]byte, string, error) {
	th := s.Current()
	if th == nil {
		return nil, "", fmt.Errorf("%w: 当前主题不可用", ErrNotFound)
	}

	clean, err := cleanRelativePath(rel)
	if err != nil {
		return nil, "", err
	}

	assetsFS := th.AssetFS()
	if assetsFS == nil {
		return nil, "", fmt.Errorf("%w: 主题 %s 没有 assets 目录", ErrNotFound, th.ID)
	}

	data, err := fs.ReadFile(assetsFS, clean)
	if err != nil {
		if isNotExist(err) {
			return nil, "", fmt.Errorf("%w: /%s/%s", ErrNotFound, AssetsDir, clean)
		}
		return nil, "", fmt.Errorf("读取主题资源失败: %w", err)
	}
	return data, contentType(clean), nil
}

// CurrentIndex 读取当前主题的 `index.html`。
//
// 主题有效但缺 index.html 时返回 ErrNotFound（清单校验已要求它存在，
// 走到这里说明文件被手工删了）。
func (s *Service) CurrentIndex() ([]byte, error) {
	th := s.Current()
	if th == nil {
		return nil, fmt.Errorf("%w: 当前主题不可用", ErrNotFound)
	}
	data, err := th.ReadFile(IndexFile)
	if err != nil {
		return nil, fmt.Errorf("%w: 主题 %s 缺少 %s", ErrNotFound, th.ID, IndexFile)
	}
	return data, nil
}

// CurrentFavicon 读取当前主题的 favicon（优先 `assets/favicon.ico`，其次主题根 `favicon.ico`）。
//
// 都找不到时返回 ErrNotFound，由调用方回退到内置 SPA 的 favicon。
func (s *Service) CurrentFavicon() ([]byte, string, error) {
	th := s.Current()
	if th == nil {
		return nil, "", fmt.Errorf("%w: 当前主题不可用", ErrNotFound)
	}

	for _, name := range []string{path.Join(AssetsDir, "favicon.ico"), "favicon.ico"} {
		data, err := th.ReadFile(name)
		if err == nil {
			return data, "image/x-icon", nil
		}
	}
	return nil, "", fmt.Errorf("%w: 主题 %s 没有 favicon", ErrNotFound, th.ID)
}

// cleanRelativePath 归一化一个 URL 相对路径，并拒绝越界。
//
// 只接受相对路径；`..` 一律拒绝（不是「清理掉」，而是**明确报错**，
// 这样能让攻击尝试在日志里留下痕迹，也不会意外读到别的文件）。
func cleanRelativePath(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("%w: 空路径", ErrNotFound)
	}
	if strings.Contains(rel, "\x00") {
		return "", fmt.Errorf("%w: 路径含非法字符", ErrNotFound)
	}

	slash := filepath.ToSlash(rel)
	if strings.HasPrefix(slash, "/") {
		return "", fmt.Errorf("%w: 不接受绝对路径", ErrNotFound)
	}

	clean := path.Clean(slash)
	if clean == "." || clean == "" {
		return "", fmt.Errorf("%w: 空路径", ErrNotFound)
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: 路径越界", ErrNotFound)
	}

	// 纵深防御：显式检查每个路径段（`a/../../b` 已被 path.Clean 归一为 `../b`，
	// 但保留这层以防将来有人改动上面的逻辑）
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: 路径越界", ErrNotFound)
		}
	}
	return clean, nil
}

// isNotExist 判断错误是否为「文件不存在」。
func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
