// Package theme 实现「主题」：**可选的页面覆盖层**（D94–D99）。
//
// # 核心模型（D94）
//
// 内置 SPA（由 internal/webfs 提供，`go:embed web/dist`）为**全部页面**提供默认实现；
// 主题是一组可替换的前端代码（`index.html` + `assets/`），通过 manifest 的 `Pages`
// **自行注册**要接管的页面。未注册的页面一律走内置 SPA。
//
// 默认主题只注册 `["/"]`（首页），因此默认体验下只有首页走主题。
//
// # 永久保留路径（安全底线，**代码硬编码，manifest 声明无效**）
//
//   - 认证页：/login、/first-login、/forgot-password、/reset-password、/logout
//   - 后台：/admin/**
//   - 系统路径：/api/**、/healthz、/theme-assets/**、/assets/**、/themes/**、/favicon.ico
//
// 为什么：主题是第三方代码。若允许它接管登录页，它能伪造登录框把密码 POST 到自己的
// 服务器——受害的是**没选过主题的普通用户**。因此保留列表写死在代码里，不是配置项。
//
// # 磁盘布局（D94.3）
//
//	<dataDir>/themes/<ThemeID>/
//	├── manifest.json      元数据 + 配置 schema + Pages 注册（D98）
//	├── index.html         入口（主题自己是 SPA，自行按 location.pathname 分流）
//	├── assets/            主题自己的 js / css / 图片
//	└── screenshot.png     可选，后台预览图
//
// 主题列表**扫描文件系统**得到（不建表，D94）；当前主题存
// `SystemSettings.theme.active`；主题配置值存独立表 `ThemeConfigs`（D95）。
//
// # 兜底（永不白屏，D94.4 / OPERATIONS §8.7）
//
// 二进制内嵌一份默认主题（见 embedded/）。主题缺失 / 损坏 / `Pages` 非法 →
// 直接用内嵌那份服务。
package theme

// Version 是 manifest.json 的 `MinAppVersion` 与之比较的当前程序版本。
//
// 为避免与 internal/server 形成循环依赖，这里单独声明；server.Version 与之保持一致。
const Version = "0.1.0"
