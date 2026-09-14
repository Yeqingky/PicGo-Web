# 前端设计规范

> **上位约束**：[`DECISIONS.md`](./DECISIONS.md)（尤其 D33 可见性、D68 外链格式、D71 管理员视图、D73 限流、D75 i18n、D80 API 前缀、D81 命名、D82 不做备份、**D84 不做缩略图**、**D94~D99 主题系统**）。
> 本文档是**前端设计与交互的唯一真源**；表/字段以 [`DATA-MODEL.md`](./DATA-MODEL.md) 为准，接口以 [`API.md`](./API.md) 为准。
>
> **前端由两部分组成**（D94）：
> - **内置 SPA**（`go:embed web/dist`）—— 承载图库、上传、相册、任务、日志、设置、后台、认证页
> - **主题**（`<dataDir>/themes/<ThemeID>/`）—— **可选的页面覆盖层**，在 `manifest.Pages` 里**自行注册**要接管的页面（默认主题只注册首页 `/`），
>   接管范围由主题 `manifest.json` 的 `Pages` 声明（架构已预留，将来加页面**不改 Go 代码**）
>
> **参考来源**：
> - 布局与交互参考 `XTheme`（lsky-pro 主题，Vue3 + NaiveUI，见文末 §16）
> - 首页/上传页视觉参考主人提供的三张截图
> - 主题系统参考 [`Komari`](https://github.com/komari-monitor/komari)（内嵌默认主题兜底、zip 校验思路、
>   配置 schema 思路；见文末 §16）
> - 背景图是一个**主题配置项**（默认 `BackgroundURL`），值指向 ACG API（D97，见 §6）

---

## 1. 定位与设计基调

| 项 | 内容 |
|---|---|
| 定位 | **自用为主 → 朋友/团队分享**的内部图床控制台。不是商业图床 |
| 基调 | 简洁、克制、信息密度高。**不做**营销式夸大（无「海量存储」「弹性扩容」「CDN 加速」等不成立的说法） |
| 技术实现 | 内置 SPA：React 19 + TS + Vite 7 + Tailwind v4 + Radix UI + React Router 7 + Zustand + Axios（D13/D14） |
| 组件模式 | shadcn 风格：`components/ui/*` = Radix 原语 + Tailwind 封装；**不引入第三方组件库**（不用 NaiveUI/Element） |
| **首页** | **由「主题」提供**（独立构建的 HTML+JS+CSS，D94）；**本文档 §4.2 给出的是「默认主题」的设计基准** |
| **其余页面** | 全部是**内置 SPA**（embed）；图库/上传/相册/任务/日志/设置/后台/认证页**不受主题影响** |
| 其他页面主题化 | ⚠️ **架构允许**（主题在 `Pages` 里声明），但**首次实现不开放**（D94.1） |
| 主题切换 | 亮色 / 暗色 / 跟随系统，`class` 策略 + `localStorage` |
| 语言 | 中文界面，文案全部 key 化（D75） |

> **本文档的 token（§2）同样是「主题」的推荐基准**：默认主题照此实现以保证视觉一致；
> 但主题是可替换的前端代码，**可自由发挥**（不强制遵循本文档的 token）。
>
> **两条边界必须遵守**（D99.2）：
> 1. 主题必须用 **`/theme-assets/...`** 引用自己的资源（构建时 `base: '/theme-assets/'`）；
>    `/assets/**` 永远属于内置 SPA，主题**不得**占用。
> 2. 主题**不得**接管认证页（`/login` 等）与 `/admin/**`（D94.2）。

**与参考图的四处主动偏离**（因为定位不同）：

| 参考图 | 本项目 | 原因 |
|---|---|---|
| 「免费注册」「注册即可获得免费存储空间」 | **无自助注册**（D25），CTA 改为「登录」 | 管理员建号 / 邮件邀请 |
| 「立即订阅」「立即购买」、套餐页、订单页 | **全部删除** | 无付费模块（D60） |
| 「探索广场」「用户主页 `/explore/@username`」「分享页 `/shares/:slug`」 | **全部删除** | 图片各自私有（D33），分享靠直接发图床链接 |
| 「多端同步」「全球 CDN 加速」「SSL + AES-256 加密存储」 | 改为真实能力描述 | 存储与分发在图床侧，不是我们的能力 |

---

## 2. 设计系统（Design Tokens）

### 2.1 颜色

采用 **shadcn/ui neutral（zinc）调色板**，HSL 三元组形式存入 CSS 变量（Tailwind v4 `@theme` 消费）。

```css
/* src/styles/tokens.css —— 亮色 */
:root {
  --background: 0 0% 100%;
  --foreground: 240 10% 3.9%;

  --card: 0 0% 100%;
  --card-foreground: 240 10% 3.9%;

  --popover: 0 0% 100%;
  --popover-foreground: 240 10% 3.9%;

  --primary: 240 5.9% 10%;
  --primary-foreground: 0 0% 98%;

  --secondary: 240 4.8% 95.9%;
  --secondary-foreground: 240 5.9% 10%;

  --muted: 240 4.8% 95.9%;
  --muted-foreground: 240 5.9% 64.9%;

  --accent: 240 4.8% 95.9%;
  --accent-foreground: 240 5.9% 10%;

  --destructive: 0 72.2% 50.6%;
  --destructive-foreground: 0 0% 98%;

  --border: 240 5.9% 90%;
  --input: 240 5.9% 90%;
  --ring: 240 5.9% 10%;

  /* 强调色（来自参考主题，Apple Blue） */
  --brand: 211 100% 50%;            /* #007AFF */
  --brand-foreground: 0 0% 100%;

  /* 语义色（用于状态点、日志状态、任务状态） */
  --success: 142 71% 45%;
  --warning: 38 92% 50%;
  --info: 199 89% 48%;

  --radius: 0.5rem;                 /* 8px —— 卡片/按钮基准圆角 */
  --radius-sm: 0.375rem;            /* 6px */
  --radius-lg: 0.75rem;             /* 12px —— 输入框/大卡片 */
  --radius-full: 9999px;
}

.dark {
  --background: 240 10% 3.9%;
  --foreground: 0 0% 98%;
  --card: 240 10% 3.9%;
  --card-foreground: 0 0% 98%;
  --popover: 240 10% 3.9%;
  --popover-foreground: 0 0% 98%;
  --primary: 0 0% 98%;
  --primary-foreground: 240 5.9% 10%;
  --secondary: 240 3.7% 15.9%;
  --secondary-foreground: 0 0% 98%;
  --muted: 240 3.7% 15.9%;
  --muted-foreground: 240 5% 64.9%;
  --accent: 240 3.7% 15.9%;
  --accent-foreground: 0 0% 98%;
  --destructive: 0 62.8% 30.6%;
  --destructive-foreground: 0 0% 98%;
  --border: 240 3.7% 15.9%;
  --input: 240 3.7% 15.9%;
  --ring: 240 4.9% 83.9%;
  --brand: 211 100% 52%;
  --brand-foreground: 0 0% 100%;
  --success: 142 71% 45%;
  --warning: 38 92% 50%;
  --info: 199 89% 48%;
}
```

**使用规则**

- 一律用语义变量（`bg-background` / `text-muted-foreground` / `border-border`），**禁止硬编码颜色值**。
- 强调色 `--brand` 只用于：主 CTA 按钮、当前导航项、聚焦环、进度条。
- 状态色用法：`success` 上传成功/任务成功；`warning` 部分失败/配额接近上限；`destructive` 失败/删除；`info` 进行中。

### 2.2 字号与行高

| Token | 大小 / 行高 | 用途 |
|---|---|---|
| `text-xs` | 12 / 16 | 徽章、辅助说明、表格次要列 |
| `text-sm` | 14 / 20 | **正文默认**、表单标签、表格 |
| `text-base` | 16 / 24 | 表单输入（避免移动端聚焦缩放）、对话框正文 |
| `text-lg` | 18 / 28 | 卡片标题 |
| `text-xl` | 20 / 28 | 页面区块标题 |
| `text-2xl` | 24 / 32 | 页面主标题 |
| `text-4xl` | 36 / 40 | 首页 Hero 主标题 |
| `text-5xl` | 48 / 1 | 首页 Hero 大标题（桌面端） |

- 中文字体栈：`Inter var, -apple-system, "PingFang SC", "Microsoft YaHei", "Noto Sans SC", sans-serif`
- 等宽（日志/任务/插件输出）：`"Fira Code", ui-monospace, SFMono-Regular, Menlo, monospace`

### 2.3 间距 / 圆角 / 阴影 / 层级

| 项 | 规范 |
|---|---|
| 间距 | 4px 基准：`1/2/3/4/6/8/12/16/20/24`（Tailwind 默认刻度）。卡片内边距 16~24；页面水平边距 16（移动）/ 24（桌面） |
| 圆角 | 按钮/输入 `--radius-lg`(12) 与 `--radius`(8) 混用按组件定；卡片 `--radius-lg`；缩略图 `--radius`；徽章 `--radius-full` |
| 阴影 | 亮色：`shadow-sm`（卡片）/ `shadow-lg`（浮层）。暗色：改用 `border` 区分，阴影仅用于浮层 |
| 层级 | `z-0` 内容 → `z-10` 吸顶栏 → `z-20` 侧栏 → `z-40` 抽屉 → `z-50` 对话框/Toast |
| 容器 | 首页内容区 `max-w-6xl`(1152px)；后台内容区 `max-w-[1400px]`；表单页 `max-w-2xl` |
| 动效 | 过渡 150ms（hover/焦点）、200ms（面板开合）；尊重 `prefers-reduced-motion` |

### 2.4 断点

| 断点 | 宽度 | 布局变化 |
|---|---|---|
| 默认（移动） | < 640 | 单列；侧栏变抽屉；图库 2 列；表格转卡片列表 |
| `sm` | ≥ 640 | 图库 3 列 |
| `md` | ≥ 768 | 侧栏收起为图标条 |
| `lg` | ≥ 1024 | 侧栏展开；图库 4 列；表格正常 |
| `xl` | ≥ 1280 | 图库 5 列 |
| `2xl` | ≥ 1536 | 图库 6 列；后台内容区留白增大 |

---

## 3. 路由与页面清单

> **由谁渲染**（D99.1 分发顺序）：认证页与 `/admin/**` **永久内置**（主题无法接管）；
> 其余路径先查**当前主题的 `Pages`**（最长前缀匹配），命中则走主题，否则回退内置 SPA。
> 默认主题 `Pages = ["/"]`，所以**首次只有 `/` 走主题**。

### 3.1 公开（无需登录）

| 路由 | 页面 | 由谁渲染 | 说明 |
|---|---|---|---|
| `/` | 首页（落地页） | **当前主题**（默认主题注册的就是它） | Hero + 上传区 + 核心能力 + 应用场景 + FAQ + CTA + Footer |
| `/login` | 登录 | **内置 SPA**（永久保留） | 邮箱 + 密码；GitHub OAuth 按钮（已绑定才可用，D27） |
| `/forgot-password` | 忘记密码 | **内置 SPA**（永久保留） | 邮箱输 → 发信（对不存在邮箱也返回成功，防枚举） |
| `/reset-password?token=` | 重置密码 | **内置 SPA**（永久保留） | 从邮件链接进入 |
| `/first-login` | 首次登录强制改密 | **内置 SPA**（永久保留） | `MustChangePassword = true` 时强制跳转（D32） |

> **无自助注册**（D25）——参考图的 `/register` 不实现。
>
> ⚠️ **认证页为什么是永久内置**：主题 = 第三方前端代码。若允许它接管 `/login`，
> 它能**伪造登录框并把密码 POST 到自己服务器**；受害的是**普通用户**（他们并未选过主题）。
> 因此这条保留列表**写死在代码里，`manifest.Pages` 声明了也会被校验拒绝**（D94.2）。

### 3.2 需登录

| 路由 | 页面 | 由谁渲染 | 内容 |
|---|---|---|---|
| `/upload` | 上传 | 内置 SPA | 拖拽/点击/粘贴上传；目标存储选择；相册选填；上传队列 |
| `/gallery` | 图库 | 内置 SPA | 瀑布流/网格；筛选；批量操作；**管理员在此页用顶部 Tab 切「我的/全部」**（D71） |
| `/gallery/:uid` | 图片详情 | 内置 SPA | 大图预览 + 元数据 + 外链复制 + 重命名/移动/删除 |
| `/albums` | 相册 | 内置 SPA | 相册列表 + 新建/重命名/删除；进入查看相册内图片 |
| `/jobs` | 任务 | 内置 SPA | 任务列表 + 逐行日志抽屉（SSE 实时） |
| `/settings` | 个人设置 | 内置 SPA | 资料（昵称/头像/主页）、改密码、已绑定身份、API Token 管理、偏好 |
| `/logs` | 操作日志 | 内置 SPA | 本人可见自己的日志；**管理员看全部**；按类型筛选 + 关键词搜索 |

### 3.3 需管理员

| 路由 | 页面 | 由谁渲染 | 内容 |
|---|---|---|---|
| `/admin/users` | 用户管理 | 内置 SPA（永久保留） | 用户 CRUD、配额调整、状态、重置密码、注销 |
| `/admin/storage` | 存储驱动 | 内置 SPA（永久保留） | 配置多实例、激活、连通性测试、魔法路径模板 |
| `/admin/plugins` | 插件 | 内置 SPA（永久保留） | 已安装/搜索/安装/卸载/更新/启停 + 任务日志 |
| **`/admin/themes`** | **主题管理** | 内置 SPA（永久保留） | 主题列表 / 上传 zip / 重新扫描 / 启用 / 卸载 / **主题设置** / 清理配置（见 §5.8） |
| `/admin/site` | 站点设置 | 内置 SPA（永久保留） | 站点信息 + 邮件 + 登录方式 + 安全 + 日志 + 关于（**不含首页内容与背景图**，它们在主题设置里，见 §5.7） |
| `/admin/logs` | 全部日志 | 内置 SPA（永久保留） | 同 `/logs` 但范围全站；含邮件日志 Tab |

> 路由守卫：`RequireAuth`（未登录 → `/login?redirect=`）、`RequireAdmin`（非 admin → 403 页）。
> 导航项由 `src/lib/navigation.ts` 的配置数组驱动，**新增页面只需往数组加一项**（D77.2 扩展点）。

**三条需要记住的边界**：

1. `/admin/**` 与认证页**永久内置**，`manifest.Pages` 声明也无法接管（D94.2）。
2. 若将来某主题声明 `Pages: ["/", "/gallery"]`，`/gallery` 会自动改由**主题**渲染，
   **Go 无改动**（D99.1）——但那时 `/gallery` 的服务端 API 与数据契约**不变**。
3. 被主题接管的路径，其**前端路由守卫不再生效**（主题自己控制页面）；
   真正的鉴权始终在**后端 API**（隐藏菜单/页面不是权限）。

---

## 4. 布局

### 4.1 后台 AppShell

```
┌────────────────────────────────────────────────────────────┐
│ ☰  站点名                                  🔍  🌓  👤       │  ← 顶栏 h-14，吸顶
├───────────┬────────────────────────────────────────────────┤
│           │                                                │
│  导航栏   │              内容区                             │
│  w-60     │        max-w-[1400px] px-6 py-6                │
│           │                                                │
│  · 上传   │                                                │
│  · 图库   │                                                │
│  · 相册   │                                                │
│  · 任务   │                                                │
│  · 日志   │                                                │
│  ─────    │                                                │
│  管理     │  ← 仅 admin 可见，分组标题                     │
│  · 用户   │                                                │
│  · 存储   │                                                │
│  · 插件   │                                                │
│  · 站点   │                                                │
└───────────┴────────────────────────────────────────────────┘
```

- 侧栏：桌面固定展开（`lg`+），`md` 收为图标条，移动端变 `Sheet` 抽屉。
- 顶栏：左侧汉堡（移动端）+ 站点名；右侧命令面板（`⌘K`）、主题切换、用户菜单。
- 用户菜单：昵称 + 头像；含「个人设置 / 操作日志 / 退出登录」。
- 底部显示配额条：`已用 1.2 GB / 5 GB`（`CapacityBytes = 0` 时显示「不限额」）。

### 4.2 首页（公开落地页）—— **由主题实现；本节是「默认主题」的设计基准**

> **首页由当前主题提供**（D94），**不是**内置 SPA 的页面。
> 下面的结构描述是**默认主题**（`themes/default/`）应达到的设计基准；
> 第三方主题可自由发挥，但建议保留 Hero、能力、FAQ、CTA 这几个区块（否则落地页会显得空）。
>
> **数据来源**：首页通过 `GET /api/web/v1/site/config` 拿到站点信息（`Site`）与
> **本主题的配置值**（`Theme.Settings`，键集合由主题 `manifest.Configuration.Items` 声明，见 §7、§5.8）。
> 本节提到的区块内容**全部来自主题配置**，**不是** `site.*` 下的键。

页面结构（自上而下）：

```
① Hero 区（全屏高 100svh，背景为背景图 + 白色/黑色蒙层）
   ┌──────────────────────────────────────────────────┐
   │  ● 服务状态（徽章，绿点=正常）                    │
   │                                                  │
   │  (=•ω•=)♥           ┌───────────────────┐        │
   │  <站点名>            │                   │        │
   │                     │   ☁  ↑            │        │
   │  [立刻上传]          │   点击上传         │        │
   │   探索功能  登录      │ 支持 JPG, PNG,    │        │
   │                     │ GIF, WebP         │        │
   │  ──────────          └───────────────────┘        │
   │  （细横线分隔）        （虚线圆，四周漂浮格式标签） │
   └──────────────────────────────────────────────────┘
   左列文案 + CTA；右列上传放置区（点击/拖拽/粘贴 → 未登录跳登录）

② 核心能力（Bento 网格，来自主题配置 ShowHomeFeatures / HomepageFeatures）
   ┌──────────────┬──────────────┬──────────────┐
   │              │  小卡 A      │  小卡 B      │
   │   大卡       ├──────────────┼──────────────┤
   │  （跨 2 行）  │  小卡 C      │  小卡 D      │
   └──────────────┴──────────────┴──────────────┘
   每卡：图标（圆角方块）+ 标题 + 描述；大卡可带一条「● 事实性说明」

③ 应用场景（来自主题配置 HomepageScenarios，3~4 列卡片）

④ 常见问题（手风琴，来自主题配置 HomepageFaq，`Accordion` 单开）

⑤ CTA 区（深色横幅：标题 + 副标题 + 主按钮「立刻上传」→ /login）

⑥ Footer（站点名 + 备案号 + 友链 + 版本号）
```

**交互细节**

- **背景图（D97）**：直接 `` <img src={Settings.BackgroundURL} alt="" aria-hidden /> ``
  绝对定位铺满 + `object-cover`；上方蒙层 `bg-background/60 dark:bg-background/70`；文字用 `text-foreground`。
  **不做任何判断**（不选横竖、不 fetch、不缓存、无降级链）——详见 §6。
- Hero 内的上传放置区与 `/upload` 页复用同一个 `UploadDropzone` 组件；未登录时点击/拖入 → 跳 `/login?redirect=/upload`。
- 滚动到「核心能力」时 Header 淡入（`IntersectionObserver`），后续区域用 `fade-up` 入场（`prefers-reduced-motion` 时禁用）。
- **资源引用必须走 `/theme-assets/...`**（主题构建时 `base: '/theme-assets/'`）；
  走 `/assets/...` 会命中内置 SPA 的资源（D99.2）。
- 主题**不应**假设自己能与内置 SPA 共享运行时状态（无共享 store、无全局变量）；
  需要的一切都通过 `GET /api/web/v1/site/config` 与后端 API 获取。

### 4.3 登录页

> 登录页是**内置 SPA**，**永久保留**（D94.2）—— 主题**不可**接管。
> 因此它**不消费主题配置**（不用主题的 `BackgroundURL`）：主题是第三方代码，
> 其配置不应该影响承载凭据输入的页面。

- 左右分栏（桌面）：左侧 `AuthFormCard` 表单；右侧**中性装饰背景**（纯色渐变 + 细微图案，见 §2 token）。
- 移动端：仅表单，中性背景为整页底图 + 蒙层。
- 表单字段：邮箱、密码、记住我、忘记密码链接。
- 底部：GitHub 登录按钮（**仅当站点已配置 OAuth 且该邮箱已绑定**时才可用；未绑定时按钮可见但提示「请先用邮箱登录后在「个人设置」里绑定 GitHub」，D27）。
- 登录失败：表单顶部 `Alert`（`destructive`），不区分「邮箱不存在 / 密码错误」（防枚举）。

---

## 5. 关键页面设计

### 5.1 上传页 `/upload`

```
┌─────────────────────────────────────────────────────────────┐
│  上传                                                       │
│  ┌───────────────────────────────────────────────────────┐  │
│  │           ☁                                          │  │
│  │       拖拽图片到此处，或点击选择                        │  │
│  │       也支持 Ctrl/⌘+V 粘贴                             │  │
│  │       支持 JPG / PNG / GIF / WebP / BMP / SVG / ICO    │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                             │
│  目标存储  [ GitHub · work  ▾ ]      ⚙ 管理存储             │
│  相册      [ 未选择        ▾ ]                              │
│                                                             │
│  ── 队列（3） ────────────────────────  全部重试 · 清理      │
│  ┌───────────────────────────────────────────────────────┐  │
│  │ 🖼 a.png         ████████░░ 80%   上传中               │  │
│  │ 🖼 b.png         ██████████ 100%  ✓ 已复制链接         │  │
│  │ 🖼 c.png         ░░░░░░░░░░ --    ✗ 失败  重试         │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

**要点**

- **目标存储必须显式选择**（一批一个驱动，D38）；默认取 `IsDefault` 的那条。切换存储只影响**新加入队列**的文件，已在队列中的不变（避免串驱动）。
- 队列项状态：`排队中 / 上传中 / 成功 / 失败`，逐项可重试/删除。
- **进度**：整批进度 = 已完成项 / 总项数；单项进度来自 SSE `upload.progress`（picgo 只有 0/30/60/100 四档，前端在档位间做平滑插值 —— 见 `OPERATIONS.md`）。
- 上传成功后：该项显示「已复制链接」并可选择格式（D68：Markdown / 直链 / HTML）。
- 上传中离开页面会提示（`beforeunload` + 内部路由守卫）；任务在后端继续（可回 `/jobs` 看）。
- 失败原因来自 SSE `upload.failed` 的 `Error` 字段，可展开看详情。

### 5.2 图库 `/gallery`

```
┌─────────────────────────────────────────────────────────────┐
│  图库                                                       │
│  [ 我的图片 | 全部图片 ]   ← 仅管理员可见（D71）            │
│                                                             │
│  🔍 搜索文件名      存储 [全部 ▾]  相册 [全部 ▾]  状态 [▾]   │
│                                            [网格|列表] 📋    │
│                                                             │
│  ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐   ← 瀑布流（列数随断点）│
│  │      │ │      │ │      │ │      │                        │
│  │ img  │ │ img  │ │ img  │ │ img  │                        │
│  ├──────┤ │      │ └──────┘ ├──────┤                        │
│  │ img  │ │ img  │ ┌──────┐ │ img  │                        │
│  └──────┘ └──────┘ └──────┘ └──────┘                        │
│                                                             │
│  ↓ 滚动到底自动加载更多（无限滚动，每页 50）                 │
└─────────────────────────────────────────────────────────────┘
```

**要点**

- **缩略图策略（D84）**：**不做缩略图，直接引图床 URL**。
  - 卡片内 `<img src={URL} loading="lazy" decoding="async">`，用 CSS `aspect-ratio` + 数据里的 `Width/Height` **预留占位**，避免懒加载导致的布局跳动。
  - 显示尺寸由 CSS 控制，**不下载缩略图**（后端不碰图片）。
  - 图床慢/不可达时的降级：`onError` 显示占位图 + `URL` 角标，点击可在新标签打开原链接。
  - `ThumbURL` 列**保留但不使用**（D77「宁可先留着不用」），前端不得依赖它。
- 卡片 hover：显示操作浮层（复制链接 / 详情 / 移动 / 删除）+ 勾选框。
- 多选：`Shift` 连选、`Ctrl/⌘+A` 全选当前已加载项、`Esc` 清除选择。
- 批量操作条：`已选 12 项` + 「复制链接（格式 ▾）」「移动到相册 ▾」「删除」。
- 点击卡片 → 灯箱预览（`Dialog` 大图 + 左右方向键切换 + `Esc` 关闭），而非跳页；详情入口在灯箱内。
- **管理员**：顶部 Tab 切换「我的图片 / 全部图片」；切到「全部」后列表多一列「上传者」。两个 Tab 独立保存筛选状态。
- 列表视图：表格列 = 文件名 / 大小 / 尺寸 / 存储 / 相册 / 状态 / 时间 / 操作。

### 5.3 存储驱动 `/admin/storage`

```
┌─────────────────────────────────────────────────────────────┐
│  存储驱动                                    [+ 新建配置]    │
│                                                             │
│  ┌───────────────────────────────────────────────────────┐  │
│  │ ⭐ GitHub · work            [已启用]     激活中        │  │
│  │    github  ·  魔法路径: {Y}/{m}/{d}                    │  │
│  │    ✓ 支持自定义路径   ✓ 支持远端删除                   │  │
│  │                    [测试] [编辑] [设为默认] [删除]      │  │
│  ├───────────────────────────────────────────────────────┤  │
│  │    WebDAV · 坚果云          [已启用]                   │  │
│  │    webdav  ·  魔法路径: (未设置)                       │  │
│  │    ✓ 支持自定义路径   ✗ 不支持远端删除                 │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

**要点**

- 同一驱动类型可有**多条实例**（D64）。列表展示 `Name`（展示名），内部用 `UID`。
- 卡片上显示**能力徽章**（来自 `Capabilities`）：支持自定义路径 / 支持远端删除。**不支持时给出原因提示**（如「该驱动的插件未实现 remove 事件」）。
- 新建/编辑抽屉：
  1. 驱动类型选择（来自 `GET /api/web/v1/storage/drivers`）
  2. **动态表单**：由 agent 返回的 `Config` schema 渲染（详见 §7）
  3. 魔法路径 / 魔法文件名两个输入框 + 变量提示（点击变量名插入）
  4. 保存 → 可选「立即测试连通性」
- **密钥字段**：`type: password` 一律遮蔽显示；编辑时若未改动则**不提交该字段**（后端保留原值）。
- 连通性测试：显示 `Ok / Message / LatencyMs`；测试中按钮转圈禁用。
- `PicgoConfigName` 创建后只读（API 层拒绝 PATCH），界面只显示不可编辑。

### 5.4 插件 `/admin/plugins`

- 两个 Tab：「已安装」/「浏览」（npm 搜索，默认关键词 `picgo-plugin-`）。
- 已安装卡片：名称、版本、作者、描述、`GuiOnly` 徽章（「仅桌面端可用」置灰开关）、启用开关、更新/卸载按钮。
- 安装/卸载/更新 → 返回 `JobUID` → 打开**任务日志抽屉**，SSE 实时输出 npm 日志。
- agent 重启期间（装/卸后）顶部显示持久 `Alert`：「内核正在重启…」，按钮禁用，轮询 `/healthz` 恢复后自动消失。

### 5.5 任务 `/jobs`

- 表格：`ID / 类型 / 状态 / 进度 / 创建者 / 创建时间 / 操作`。
- 点击行 → 详情抽屉（`Sheet`）：基本信息 + 逐行日志（自动滚底、可下载）+ 结果 JSON。
- 进行中的任务进度条实时更新（SSE）；完成后状态徽章变化 + Toast 提示。
- 「清理已完成」批量删除（`DELETE /jobs/{uid}`）。

### 5.6 操作日志 `/logs`（与 `/admin/logs`）

```
┌─────────────────────────────────────────────────────────────┐
│  操作日志                                                   │
│  类型 [全部 ▾]  状态 [全部 ▾]  🔍 关键词      [时间范围 ▾]   │
│                                                             │
│  12:03:41  上传        成功  admin    up_01HX…  a.png       │
│  12:01:07  邮件发送    失败  system   me@x.com  连接超时     │
│  11:58:22  存储配置创建 成功  admin    st_01HY…  坚果云      │
└─────────────────────────────────────────────────────────────┘
```

- 类型下拉来自 `GET /logs/types`（D45 的类型清单）。
- 关键词搜索匹配 `Username / TargetUID / Detail / Error`。
- 行首用状态色点（成功绿 / 失败红）；失败行默认展开错误摘要。
- 点行 → 详情抽屉：完整 JSON（`Detail` / `Error` 高亮格式化）。
- 管理员页多一个 Tab：「邮件日志」（`EmailLogs`，**不显示正文**，D29）。
- 保留期提示：「日志保留 180 天」（`log.retentionDays`，`0` 显示「永久保留」）。

### 5.7 站点设置 `/admin/site`

分 Tab：**站点信息 / 邮件 / 登录方式 / 安全 / 日志 / 关于**。

- **站点信息**：站点名、副标题、描述、关键词、公告（Markdown）、备案号、`site.baseUrl`（含 OAuth 回调地址展示与复制按钮）、站点图标 URL。
- **邮件**：SMTP 配置 + 「发送测试邮件」按钮（结果写 `EmailLogs`）。
- **登录方式**：GitHub OAuth 的 `ClientID` / `ClientSecret`（遮蔽）+ 回调地址 + 启用开关；
  显示「用户需先绑定 GitHub 才能用它登录」（D27）。
- **安全**：`sessionTtlHours` / `accessTokenTtlMinutes` / `loginMaxAttempts` / `loginWindowMinutes`。
- **日志**：`log.retentionDays` / `log.jobRetentionDays`。
- 所有设置项右侧显示**来源徽章**：`来自数据库` / `使用默认值`（来自 API 的 `Source` 字段）。

> ⚠️ **本站点设置页没有「首页内容」与「背景图」两个 Tab**（D95 归类原则：
> 「主题的画法 → 主题配置；站点属性 → `site.*`」）。
> 它们已迁到 **主题管理 → 主题设置**（`/admin/themes`，见 §5.8）。
> 页面内放一条引导链接：「首页与背景相关设置在 **主题管理 → 主题设置**」。

### 5.8 主题管理 `/admin/themes`（admin）

> 管理**页面级主题**：安装、启用、卸载、配置（D94~D98）。

```
┌─────────────────────────────────────────────────────────────┐
│  主题管理                  [上传主题 (zip)]  [重新扫描]       │
│  ┌───────────────────────────────────────────────────────┐  │
│  │ ! 主题等同于在服务器上运行任意前端代码，只安装信任的主题 │  │
│  └───────────────────────────────────────────────────────┘  │
│  ┌───────────────────────────┐  ┌─────────────────────────┐  │
│  │ [预览图]                   │  │ [预览图]                 │  │
│  │ 默认首页主题  v1.0.0       │  │ 我的主题   v2.1.0        │  │
│  │ YeqingKy                  │  │ someone                 │  │
│  │ [已启用] [内置]            │  │ [ ]                     │  │
│  │ 接管：/                    │  │ 接管：/ /gallery         │  │
│  │ [设置] [卸载]              │  │ [启用] [设置] [卸载]     │  │
│  └───────────────────────────┘  └─────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

**主题卡片列表**（数据来自 `GET /api/web/v1/themes`）

| 元素 | 说明 |
|---|---|
| 预览图 | 主题的 `screenshot.png`（`ScreenshotURL`）；缺失时用占位图 |
| 名称 / 版本 / 作者 | 来自 manifest（多语言文本已由服务端按 `Accept-Language` 解析为单串） |
| **`Pages`（接管范围）** | **必须展示**，如「接管：/」或「接管：/ /gallery」；让管理员一眼看出该主题会影响哪些页面 |
| 徽章 `IsActive` | 当前启用中（只可能有一个） |
| 徽章 `IsBuiltin` | 随镜像发布（`default`）；不可卸载 |
| 按钮启用 | 不在启用中且 `Valid=true` 时显示 |
| 按钮设置 | 打开「主题设置」抽屉（§5.8.2） |
| 按钮卸载 | `CanUninstall=false`（启用中或 `IsBuiltin`）时**禁用**并给出原因 tooltip |
| `Valid=false` | **红色错误卡片** + `Error` 原因文本（缺 manifest / ID 与目录名不匹配 / 缺 index.html / `Pages` 非法）；**不允许启用** |

**顶部操作**

| 操作 | 行为 |
|---|---|
| **上传主题（zip）** | 打开上传对话框（见下） |
| **重新扫描** | `POST /api/web/v1/themes/rescan`；用于「手动把目录放进 `data/themes/`」的情形；完成后 Toast + 列表刷新 |

#### 5.8.1 上传对话框

- 文件选择器（仅 `.zip`）+ 体积提示「≤ `theme.maxPackageBytes`（默认 64 MiB）」。
- `Overwrite` 开关（默认关）：目标 ID 已存在时需显式开启，否则返 `40901`。
- 上传进度；完成后显示解析出的 `ID` / `Name` / `Version` / **`Pages`**，并高亮新增项。
- **失败时展示具体校验原因**（逐条对应 D96 的校验清单），例如：
  * `压缩包内含非法路径（Zip Slip）：../evil.js`
  * `解压后总大小超过限制（512 MiB）` / `文件数超过限制（10000）`
  * `压缩包内含符号链接，已拒绝`
  * `manifest.json 缺失` / `manifest.json 超过 1 MiB`
  * `主题 ID 与目录名不一致` / `主题 ID 格式无效`
  * `缺少 index.html`
  * `Pages 非法：/login 是系统保留路径（认证页不可被主题接管）`
  * `主题已存在（default），需开启「覆盖」`
- 上传区旁固定一段说明：「主题等同于在服务器上运行任意前端代码」。

#### 5.8.2 主题设置抽屉（`ThemeSettingsDrawer`）

- 数据来自 `GET /api/web/v1/themes/{ThemeID}/settings`（返回 `Schema` + `Values`）。
- 表单由**§7 的渲染器**渲染（主题 schema：`string`/`text`/`number`/`switch`/`select`/`json`）。
- 每个字段右侧显示 **`Source` 徽章**：`来自数据库` / `使用默认值`。
- `json` 类型且带 `ItemSchema` 时用 **`RepeaterField`**（增删改 + 上下移 + 折叠）。
- 保存 → `PUT /api/web/v1/themes/{ThemeID}/settings`（只提交该主题声明过的键）；成功 Toast + 刷新 `Source`。
- 抽屉顶部显示该主题的 **`Pages`**，并提示「该主题会影响以下页面：/」。

#### 5.8.3 危险区

| 操作 | 约束 |
|---|---|
| **清理该主题配置** | `DELETE /api/web/v1/themes/{ThemeID}/settings`；**仅非当前启用主题**可用；`AlertDialog` 二次确认（写明「将删除 N 项配置，不可恢复」）；写 `theme.settings.clear` 日志 |

> 卸载主题时**不自动删配置值**（D95「换主题不丢值」）；需清理请显式操作。

#### 5.8.4 安全与边界说明区

页面底部固定一块只读说明（不是可配置项），内容：

1. **主题 = 服务器上的任意前端代码**（影响它接管的页面）→ 仅安装可信主题。
2. **认证页（`/login` / `/first-login` / `/forgot-password` / `/reset-password` / `/logout`）
   与 `/admin/**` 由系统内置、永久保留**，主题**无法接管**（即使 manifest 声明也会被校验拒绝）。
   这是安全默认值（防伪造登录框钓鱼凭据），**不是 bug，也不可配置**。
3. 主题资源走 `/theme-assets/**`，内置 SPA 资源走 `/assets/**`，二者互不干扰。
4. 主题缺失/损坏时，首页会自动回退到**内置默认主题**（永不白屏），并在操作日志里留一条 `theme.error`。

---

## 6. 背景图（D97：单一 URL，不做判断）

### 6.1 配置项

背景图不是站点设置，而是**主题的配置项**（D95 归类原则）：

| 项 | 值 |
|---|---|
| 配置键 | `BackgroundURL` |
| 类型 | `string`（主题 manifest 的 `Configuration.Items` 里声明） |
| 默认值 | `https://api.yppp.net/api.php` |
| 归属 | **当前主题**（`ThemeConfigs` 表，`ThemeID = 当前主题`） |
| 修改入口 | 后台 **主题管理 → 主题设置**（`/admin/themes`，§5.8.2） |

### 6.2 用法（**不做任何判断**）

默认主题只需要一行：

```html
<img src={Settings.BackgroundURL} alt="" aria-hidden className="..." />
```

- 绝对定位铺满 + `object-cover`；上方蒙层 `bg-background/60 dark:bg-background/70`。
- 留空或加载失败 → **不显示背景图**（浏览器 `onError` 的自然行为，**不是业务逻辑**）。

**为什么一个 URL 就够**：`api.php` 是上游的**自适应端点**——它自己按 `User-Agent`
判断并 302 到横图（`pc.php`）或竖图（`pe.php`），再 302 到图片。
所以前端**不需要**选横竖、不需要 `fetch`、不需要 CORS、不需要解析 JSON。
`<img>` 直连即可，302 由浏览器自动跟随。

### 6.3 明确不做（D97）

| 不做 | 原因 |
|---|---|
| ❌ 模式切换（`none` / `fixed` / `acg`） | 一个 URL 就够 |
| ❌ 横竖图判断（`pc.php` / `pe.php` 分支） | 由上游 `api.php` 按 UA 自行处理 |
| ❌ `fetch` + JSON 解析（`?return=json`） | `<img>` 直连即可，无需 JS 解析 |
| ❌ 会话缓存 / 降级链 | 用户明确要求不做判断 |
| ❌ 后端缓存 / 定时任务 / 代理端点 | **无** `site/background` 端点，后端不参与背景图请求 |
| ❌ `site.background.*` 配置键 | 已全部废除（属于主题配置） |

### 6.4 已知行为（写进文档，**不写代码**）

同一 URL 会被浏览器缓存，刷新可能看到同一张图（上游文档也提到此现象）。
需要换图时，管理员自行把 `BackgroundURL` 改成带参形式（如 `/api.php?t=...`）——
**代码不做任何处理**。

---

## 7. 动态表单渲染器（插件 schema + 主题 schema）

**这是本项目「不硬编码表单」的核心 UI 能力**。前端有**两处**需要按 schema 渲染表单：

| 来源 | schema 形态 | 字段类型命名 | 是否需要回源 | 用在哪 |
|---|---|---|---|---|
| **插件 / 驱动** | `IPluginConfig[]`（agent 求值后返回） | `input` / `password` / `list` / `checkbox` / `confirm` / `editor` | ✅ 需要（`DependsOn` 联动要回源 agent 重求值） | `/admin/storage` 的驱动配置表单 |
| **主题** | `Configuration.Items[]`（来自主题 `manifest.json`） | `string` / `text` / `number` / `switch` / `select` / `json` | ❌ 不需要（静态 schema，本地求值即可） | `/admin/themes` 的主题设置抽屉（§5.8.2） |

### 7.1 适配层（两套命名 → 一组内部字段类型）

**两套命名不能直接共用**，但**共用同一套渲染组件**。中间放一个**薄适配层**：

```
插件 schema（IPluginConfig[]）  ─┐
                                 ├─→  适配层  ─→  内部 SchemaField[]  ─→  渲染组件
主题 schema（Items[]）          ─┘
     （把各自的 Type + 属性名规约到内部类型）
```

**内部字段类型**（渲染器只认这一组）：

| 内部类型 | 渲染为 |
|---|---|
| `text` | `Input`（单行） |
| `password` | `Input type=password` + 显示/隐藏切换 |
| `textarea` | `Textarea`（可拖拽调高） |
| `number` | `Input type=number`（带 min/max 校验） |
| `switch` | `Switch`（布尔） |
| `select` | `Select`（单选） |
| `multiselect` | 多选（多选 `Select` / `Switch` 组） |
| `repeater` | 可增删排序的列表（`RepeaterField`） |
| `unknown` | `Input` + 警告提示（降级，**不崩溃**） |

### 7.2 映射表

**插件 schema → 内部类型**（`IPluginConfig`，来自 `PICGO-INTEGRATION.md` §3）

| 插件 `Type` | 内部类型 | 备注 |
|---|---|---|
| `input` | `text` | |
| `password` | `password` | 值遮蔽；**未改动时不提交**该字段 |
| `editor` | `textarea` | 多行文本，可含 `\n` |
| `confirm` | `switch` | |
| `list` | `select` | 选项来自 `Choices` |
| `checkbox` | `multiselect` | 值为数组 |
| 其它 | `unknown` | 降级渲染 + 警告 |

**主题 schema → 内部类型**（`Configuration.Items[]`，来自 D98）

| 主题 `Type` | 内部类型 | 备注 |
|---|---|---|
| `string` | `text` | |
| `text` | `textarea` | |
| `number` | `number` | |
| `switch` | `switch` | |
| `select` | `select` | 选项来自 `Options`（**逗号分隔的字符串**） |
| `json` | `repeater`（**有** `ItemSchema` 时）<br>`textarea`（无 `ItemSchema` 时，按 JSON 文本编辑） | 用于首页内容（核心能力 / 场景 / FAQ）等结构化列表 |
| 其它 | `unknown` | 降级渲染 + 警告 |

**属性名映射**

| 语义 | 插件 schema | 主题 schema | 内部 |
|---|---|---|---|
| 键名 | `Name` | `Key` | `name` |
| 展示标签 | `Alias`（优先）/ `Name` | `Name`（多语言已在服务端解析） | `label` |
| 辅助说明 | `Message` | `Help` | `help` |
| 是否必填 | `Required` | `Required` | `required` |
| 默认值 | `Default` | `Default` | `defaultValue` |
| 选项 | `Choices`（数组或 `{Name, Value}`） | `Options`（逗号分隔字符串） | `options[]` |
| 依赖字段 | `DependsOn` | —（无） | `dependsOn` |

> 适配层只做**纯数据变换**（无副作用、可单测），放 `src/components/schema-form/adapt.ts`。

### 7.3 联动（`DependsOn`）

- **只对插件 schema 有效**（`DependsOn` 是 picgo 的特性）。
- 带 `DependsOn` 的字段：依赖字段值变化时，
  **回调 `POST /api/web/v1/storage/uploaders/schema`**（body 带当前 `Answers`）重新求值。
- 前端**永不执行插件代码**（`PICGO-INTEGRATION.md` §3）。
- 求值期间该字段显示 `Skeleton`；请求失败保留旧选项并提示。
- **主题 schema 是静态的**：没有联动、不回源，纯本地渲染。

### 7.4 表单结构（内部形态）

```
{
  name, label, help?, type,          // 内部类型
  required, defaultValue,
  options?, dependsOn?,
  itemSchema?                        // 仅 repeater
}
```

- 标签优先用适配后的 `label`；`help` 作为 `helperText`。
- `required` 为真时校验非空（前端校验 + 后端兜底）。
- 初始值：**已存值 > `defaultValue`**（主题 schema 的已存值来自 `ThemeConfigs`，见 §5.8.2；
  插件 schema 的已存值来自驱动配置）。若 `required` 且无默认且无已存值，提交按钮禁用并提示。
- **`password` / 密钥类字段**：已有值时显示掩码；**用户未改动则不提交该字段**（后端保留原值）。

---

## 8. 组件清单

### 8.1 基座（`src/components/ui/*`，Radix + Tailwind，shadcn 风格）

`Button` `IconButton` `Input` `Textarea` `Label` `Switch` `Checkbox` `RadioGroup`
`Select` `Combobox` `Dialog` `AlertDialog` `Sheet`（抽屉）`Drawer` `Popover` `Tooltip`
`DropdownMenu` `ContextMenu` `Tabs` `Accordion` `Table` `Card` `Badge` `Avatar`
`Separator` `Skeleton` `Progress` `ScrollArea` `Toast`（sonner）`Pagination`
`Breadcrumb` `Alert` `Form`（react-hook-form + zod 封装）`Calendar` `EmptyState`

### 8.2 业务组件（`src/components/**`）

| 组件 | 职责 | 用在 |
|---|---|---|
| `AppShell` | 顶栏 + 侧栏 + 内容区 + 配额条 | 全部需登录页 |
| `UploadDropzone` | 拖拽/点击/粘贴接收文件 | 首页 Hero、`/upload` |
| `UploadQueue` / `UploadQueueItem` | 队列与逐项进度 | `/upload` |
| `ImageCard` / `ImageGrid` / `ImageList` | 图片卡片与两种视图 | `/gallery` |
| `ImageLightbox` | 大图预览（方向键切换） | `/gallery` |
| `LinkCopyMenu` | 外链复制（Markdown / 直链 / HTML） | 图库、详情、上传完成 |
| `StorageConfigForm` | 动态表单渲染器（§7） | `/admin/storage` |
| `CapabilityBadges` | 驱动能力徽章 | `/admin/storage` |
| `PluginCard` | 插件卡片（含 `GuiOnly` 徽章） | `/admin/plugins` |
| `JobLogDrawer` | 任务日志实时抽屉 | 插件/上传/任务 |
| `OperationLogTable` | 日志表 + 筛选 + 详情抽屉 | `/logs` |
| `RepeaterField` | 可增删排序的列表编辑器（**也用于主题的 `json` 类型配置项**，由 `ItemSchema` 驱动） | `/admin/themes`（主题设置） |
| `ThemeCard` | 主题卡片：预览图 + 名称/版本/作者 + `IsActive`/`IsBuiltin` 徽章 + **`Pages`（接管范围）** + 启用/设置/卸载按钮；`Valid=false` 时渲染红色错误卡 + 原因 | `/admin/themes` |
| `ThemeUploadDialog` | zip 上传对话框：文件选择 + 体积提示 + `Overwrite` 开关 + 进度；**失败时逐条列出校验原因**（Zip Slip / 超限 / ID 冲突 / 缺 index.html / `Pages` 非法） | `/admin/themes` |
| `ThemeSettingsDrawer` | 主题设置抽屉：按主题 schema 渲染表单（§7）+ `Source` 徽章 + `Pages` 影响面提示 + 危险区 | `/admin/themes` |
| `QuotaBar` | 配额进度 | 侧栏、用户管理 |
| `SchemaFormField` | 单字段渲染（按**内部类型**分派，§7.1；插件与主题 schema 共用） | 动态表单内部 |
| `ThemeToggle` / `CommandMenu` | 亮暗色切换 / `⌘K` 命令面板（**注意：此「Theme」指亮暗色，与「页面主题」无关**） | 顶栏 |
| `ClientOnly` | 避免首屏闪烁（亮暗色、图表等） | 亮暗色、图表 |

---

## 9. 交互规范

### 9.1 键盘

| 快捷键 | 行为 | 作用域 |
|---|---|---|
| `⌘/Ctrl + K` | 打开命令面板（跳页、搜图、执行操作） | 全局 |
| `⌘/Ctrl + V` | 粘贴上传 | 上传页（**首页由主题自行决定是否支持**） |
| `⌘/Ctrl + A` | 全选当前已加载图片 | 图库（焦点在列表内） |
| `Shift + 点击` | 连选区间 | 图库 |
| `←` `→` | 灯箱上一张 / 下一张 | 灯箱开启时 |
| `Esc` | 关闭浮层 / 清除选择 | 全局 |
| `g` 然后 `u` / `g` / `a` | 跳转 上传 / 图库 / 相册 | 全局（不带输入焦点时） |

### 9.2 拖拽与粘贴

- 拖入窗口任意位置：显示整页虚线遮罩「松开即可上传」。
- 粘贴：读取 `ClipboardEvent.items` 里的图片；也支持从文件管理器复制**文件**后粘贴。
- 拖拽排序：相册排序、Repeater 项排序用 `@dnd-kit`。

### 9.3 状态与反馈

| 状态 | 规范 |
|---|---|
| 加载 | 首屏用 `Skeleton`；局部刷新用 `Spinner`；表格用行内骨架 |
| 空 | `EmptyState`（图标 + 一句话 + 主操作按钮）。图库空态：「还没有图片」+「去上传」 |
| 错误 | 可重试错误：错误块 + 「重试」；不可重试：`Alert`。请求错误统一 Toast |
| 成功 | Toast 轻提示（2s）；重要结果（如上传拿到链接）用可交互 Toast |
| 破坏性操作 | 一律 `AlertDialog` 二次确认，明确写出后果（如「同时删除图床上的文件」开关） |
| 乐观更新 | 重命名、移动相册等低风险操作先更新 UI 再请求，失败回滚 + Toast |

**特殊：`AlertDialog` 中的异步确认按钮**——必须用普通 `Button` + 手动控制 `open`，
**不得用 `AlertDialogAction`**（它会在点击后立即关闭，无论是否 `preventDefault`）。

### 9.4 SSE 连接

- `/gallery` 之外的页面按需连接；`/upload`、`/jobs`、插件安装期间必须连。
- 断线：指数退避重连（1s → 2s → 4s → 8s，上限 30s），期间顶部显示「连接已断开，正在重连…」。
- 收到 `system.notice` → 顶部持久 `Alert`（`level` 决定样式）。

### 9.5 首页（主题）与内置 SPA 的边界

> 首页由主题提供（D94），内置 SPA 不参与它的渲染。两者之间的边界必须清楚：

| 维度 | 首页（主题） | 内置 SPA |
|---|---|---|
| 代码来源 | `<dataDir>/themes/<当前主题>/` | `go:embed web/dist` |
| 资源前缀 | **`/theme-assets/**`** | **`/assets/**`** |
| 多语言 | 主题自决（可采用站点信息里的文案） | i18n key（D75） |
| 状态 | 主题自己的 | Zustand store（§11） |
| 数据获取 | `GET /api/web/v1/site/config` + 后端 API | 自研 hooks + Axios |
| 实时推送 | 主题自行调 `GET /api/web/v1/events`（SSE） | SSE → Zustand（§9.4） |

**三条硬约束**

1. 主题通过 `GET /api/web/v1/site/config` 拿数据，**不依赖内置 SPA 的任何 store**。
2. 首页「立刻上传」按钮：**未登录 → `/login?redirect=/upload`；已登录 → `/upload`**（D86）。
   登录态可从 `GET /api/web/v1/auth/me` 判断（未登录返 `40102`），或直接交给目标页自己处理。
3. 主题**不应**假设自己能与内置 SPA 共享运行时状态（无共享 store、无全局变量、无共享 chunk）。
   它拿到的是一个干净的窗口：自己的 DOM、自己的 JS、后端 API。

> 将来某主题接管了 `/gallery` 等页面时，同样的边界适用：
> 主题负责渲染，**数据契约（`API.md`）与鉴权（后端）不变**。

---

## 10. 可见性与权限的 UI 表达（D33 / D71）

| 角色 | 图库 | 日志 | 用户管理 | 存储/插件/站点/主题 |
|---|---|---|---|---|
| 普通用户 | 仅「我的图片」（无 Tab） | 仅自己的（无 Tab） | 不可见 | 不可见 |
| 管理员 | 顶部 Tab：我的 / 全部 | Tab：我的 / 全部 / 邮件 | 可见 | 可见 |

- 管理员在「全部图片」Tab 下，列表**明确标注上传者**，且删除他人图片时二次确认里写明对方用户名。
- 侧栏「管理」分组仅管理员渲染 —— 但**后端仍必须独立鉴权**（隐藏菜单不是权限）。
- **主题管理（`/admin/themes`）仅管理员可见**：安装主题 = 在服务器上引入任意前端代码，
  权限级别与插件安装相同（D96）。
- 首页（`/`）是**公开**的，但**主题不因此获得任何权限**：它只能调公开 API（`/site/config`）
  与普通用户 API；接管的页面也不能绕过后端鉴权（D99.1 边界 3）。

---

## 11. 状态管理

| 类型 | 方案 | 内容 |
|---|---|---|
| 服务端状态 | 自研 hooks + Axios（`src/hooks/api/*`） | 列表、详情、设置等远程数据；含加载/错误/重试 |
| 客户端状态 | **Zustand**（`src/store/*`） | `auth`（登录态与当前用户快照）、`upload`（队列）、`task`（任务面板）、`gallery`（筛选/选择/视图模式）、`ui`（侧栏、**亮暗色**） |
| 服务端推送 | SSE → 写入 Zustand（upload/task） | 进度与任务状态 |

**规则**

- 服务端数据**不镜像进 Zustand**（避免双份真相）；Zustand 只存 UI 意图与推送态。
- `auth` store 持久化到 `localStorage`（仅快照，不含令牌 —— 令牌在 httpOnly Cookie 里）。
- 亮暗色存 `localStorage`，首屏用内联脚本设置 `class` 防闪烁（**注意：需与 CSP 兼容**，用 nonce 或改用 `data-theme` + 早期 CSS）。
- **以上 Zustand store 全部属于内置 SPA**；首页主题**没有**共享 store（§9.5）。

---

## 12. 可访问性（a11y）

- 全部交互元素可键盘到达；焦点环用 `--ring`（`focus-visible` 显示，鼠标点击不显示）。
- 图标按钮必须有 `aria-label`。
- 对话框/抽屉：`focus trap` + `Esc` 关闭 + `aria-modal`。
- 图片 `alt`：用 `AliasName || OriginalName`；纯装饰（背景）用 `alt=""`。
- 颜色对比：正文与背景对比度 ≥ 4.5:1（shadcn neutral 调色板已满足）。
- 尊重 `prefers-reduced-motion`：关闭入场动画与背景视差。

---

## 13. 国际化

- 中文为默认与唯一完整语言；`src/i18n/locales/zh-CN.json` 为源，`en.json` 预留（D75）。
- **文案禁止硬编码**，用 `t('KEY')`；key 用 `SCREAMING_SNAKE_CASE`。
- 后端返回的 `Message` 已是中文，前端直接展示（不二次翻译）。
- **例外**：插件 schema 的 `Alias` / `Message` 由 picgo 的 i18n 提供，已翻译的字符串直接透传，前端不处理（`PICGO-INTEGRATION.md` §11）。
- **例外**：主题 schema（`Configuration.Items`）的 `Name` / `Help` **支持多语言对象**，
  由服务端按请求的 `Accept-Language` 解析成单串后返回（D98）；前端**直接展示**，不做二次翻译。

---

## 14. 前端目录结构与产物

> **前端有两个独立的构建单元**（D94）：**内置 SPA**（编译进二进制）与
> **默认首页主题**（打包成 `themes/default/`，供用户替换）。两者**目录分离、构建分离**。

### 14.1 内置 SPA（`web/`）

```
web/
├── index.html
├── vite.config.ts                 # base: '/'；dev proxy: '/api' → http://127.0.0.1:8080
├── tailwind.config.ts / tokens.css
└── src/
    ├── main.tsx  App.tsx  router.tsx
    ├── lib/
    │   ├── http.ts                # Axios 实例 + 拦截器（baseURL '/api/web/v1'）
    │   ├── sse.ts                 # EventSource 封装 + 重连
    │   ├── clipboard.ts           # 外链格式化（Markdown/URL/HTML）
    │   ├── format.ts              # 字节/时间/尺寸格式化
    │   ├── navigation.ts          # 导航配置数组（新增页面改这里）
    │   └── utils.ts               # cn() 等
    ├── types/
    │   └── api.ts                 # 与 docs/API.md 手写同步（字段 PascalCase）
    ├── store/                     # Zustand：auth/upload/task/gallery/ui
    ├── hooks/api/                 # 服务端数据 hooks（按域分文件）
    ├── components/
    │   ├── ui/                    # shadcn 基座
    │   ├── layout/                # AppShell 等
    │   ├── upload/ gallery/ storage/ plugins/ logs/ site/ themes/
    │   └── schema-form/           # 动态表单渲染器（含 adapt.ts 适配层，§7.1）
    ├── i18n/
    └── features/                  # 按业务域切分（与 PLAN.md W8 一致）
        ├── auth/                  # login / reset-password / first-login（永久内置）
        ├── upload/ gallery/ albums/ jobs/ settings/ logs/
        └── admin/                 # users storage plugins themes site logs
```

**关键点**

- **`features/` 里没有 `home/`** —— 首页由主题提供，不属于内置 SPA。
- SPA 构建产物全部走 **`/assets/**`**，由 `go:embed web/dist` 打进二进制。
- `/admin/themes` 的页面放在 `features/admin/themes/`；相关组件放 `components/themes/`。

### 14.2 默认首页主题（`themes/default/` 的来源）

> **`themes/default/` 是构建产物，不是手写目录。**

**来源与生成方式**（源在 `server/internal/theme/embedded/`，二进制内嵌同一份作兜底；详见 `PLAN.md` W10）：

```
① 主题外壳（手写，位于仓库内，由实现时确定具体路径）
   ├── manifest.json          # ID/Name/Pages/Configuration.Items（D98）
   ├── src/                   # 首页源码（可用 React/Vue/纯 HTML 任意技术栈）
   ├── screenshot.png         # 后台预览图
   └── vite.config.ts         # base: '/theme-assets/'
          │
          │  构建（独立于内置 SPA 的构建）
          ▼
② themes/default/            # ★ 打包产物
   ├── manifest.json
   ├── index.html            # 引用 /theme-assets/...
   ├── assets/               # 带内容哈希的 js/css
   └── screenshot.png
          │
          ├─→ ③ 压缩归档 → `//go:embed` 进二进制（作兜底，D94.4）
          │      启动时若 data/themes/ 为空则 seed 出一份
          │
          └─→ ④ 也作为「主题格式的参考实现」随发行包提供
```

**约定**

| 项 | 值 |
|---|---|
| 资源 base | **`/theme-assets/`**（**不可**用 `/assets/`，那是内置 SPA 的目录） |
| `manifest.Pages` | `["/"]`（默认主题只注册首页；机制上可注册更多业务页面） |
| `index.html` 位置 | 主题根（**不是** `dist/index.html`；主题目录**没有** `dist/` 子目录） |
| 是否参与 `go:embed web/dist` | ❌ 不参与（独立产物流，见上方 ③） |

> **为什么把默认主题的源码与内置 SPA 分开**：两者的**资源前缀必须不同**（§1 边界 1），
> 且主题要能被用户整体替换而不影响 SPA；混在一次构建里会导致 `base` 冲突。

---

## 15. 前端开发顺序（对应 PLAN.md 的 W7/W8/W10）

| 阶段 | 内容 | 验收 |
|---|---|---|
| F0 | tokens.css + `components/ui/*` 基座 + AppShell + 路由骨架 + 亮暗色切换 | 能渲染空壳页面，亮暗色正常 |
| F1 | `lib/http.ts` + `types/api.ts` + 登录/忘记密码/首登改密（**永久内置**） | 登录流程通（后端可用前用 mock） |
| F2 | **默认首页主题**（源在 `server/internal/theme/embedded/`）：Hero + 能力 + 场景 + FAQ + CTA + Footer | 与截图布局一致；**背景图直接引 `BackgroundURL`，无任何判断**（§6.2）；资源走 `/theme-assets/**`；同一份被 `go:embed` 作为兜底（D94） |
| F3 | 上传页 + 队列 + SSE 进度 | 能真实上传并看到进度 |
| F4 | 图库（瀑布流/列表/筛选/批量/灯箱/外链复制，**不做缩略图** D84） | 图库可用 |
| F5 | 相册 + 任务页 | 整理功能可用 |
| F6 | 存储（**插件 schema** 动态表单）+ 插件（含任务日志抽屉） | 能配驱动、装插件 |
| F7 | 站点设置（站点信息/邮件/登录方式/安全/日志/关于）+ **主题管理页（列表/上传 zip/重新扫描/启用/卸载/主题设置/清理配置）** + 日志 + 用户管理 | 管理员可用；能装一个第三方主题并切换 |
| F8 | 移动端适配 + a11y + 空/错/载状态 + 性能（虚拟滚动/图片懒加载） | 全站走查通过 |

> **F2 与 F3 及以后是两条产物流**：F2 产出主题（`themes/default/`），
> F3 及以后都属于**内置 SPA**（`web/dist`）。
> 两者的构建配置独立（`base` 不同，见 §14.2）。

---

## 16. 参考来源与取舍

| 来源 | 采纳 | 不采纳 |
|---|---|---|
| **XTheme**（lsky-pro 主题，Vue3 + Vite + NaiveUI） | 设计 token（shadcn neutral + `#007AFF`）、首页结构（Hero/能力/场景/FAQ/CTA）、**首页内容数据驱动（Repeater）**、瀑布流图库、主题 `manifest` 的元素（ID/Name/Description/Author/Version/Preview） | NaiveUI 组件库（我们用 Radix）、Vue 实现、`/register`、`/explore`、`/shares`、`/user/plans`、`/user/orders`、`/user/tickets`、广告设置、自定义 CSS/JS 注入（安全与可控性考虑，暂不做）、**背景图三种模式与 ACG 定向/缓存方案**（已被 D97 取代为单一 URL） |
| **lsky-pro** 后台 | 侧栏分组、表格 + 抽屉的 CRUD 模式、存储策略管理形态 | 用户组/角色组、公共画廊 |
| **Komari**（Go 自托管监控，`komari-monitor/komari`） | **内嵌默认主题作兜底**（永不白屏）、zip 安装的校验思路（体积/文件数/ID 校验）、主题配置 schema 的思路（`Configuration.Items`）、配置与主题元数据分离 | **主题市场**（多 source + catalog，自用不需要）、`raw` / `redirect` 主题类型（D98 只做 `managed`）、**HTML 字符串替换注入**（title/description/custom head+body；与 D92「不做后台注入 CSS/JS」冲突）、**整站主题化**（本项目主题可选注册页面，默认只注册首页，D94） |
| **主人提供的三张截图** | Hero 布局、Bento 网格比例、FAQ 手风琴、深色 CTA 横幅、漂浮格式标签 | 「免费注册」「立即订阅」「海量存储」「全球 CDN」等不成立文案 |

> XTheme 是编译后的产物（`assets/*.js` 为 minified），本文档的结论来自其
> `manifest.json`、`XTheme.php`（687 行配置定义，含 `features`/`scenarios`/`faq`/`background_images` 等
> Repeater 与 FileUpload 字段）、构建产物中的路由表与 CSS 变量，以及首页/上传组件的可见文案。
> **未复制其任何代码**，仅参考设计意图。
>
> Komari 的结论来自其源码阅读：`web/public/public.go`（内嵌 `defaultTheme/dist.tar.zst`、
> `forceDefaultTheme` 强制内置主题渲染认证与后台路由、路径穿越校验）、`web/api/admin/theme.go`
> （zip 校验与解压、`SetTheme`、theme settings 写入）、`database/models/theme.go`
> （`Configuration` 的 `managed`/`raw`/`redirect` 三型、多语言文本、redirect 归一化）、
> `internal/managedconfig/selectors.go`（配置项默认值与引用解析）。
> **本项目对其两处安全实现做了硬化**（D96）：用 `filepath.Rel` 替代 `strings.HasPrefix` 判路径；
> 强制权限位并显式拒绝 symlink entry。同样**未复制其代码**。

---

## 与决策的偏差

本文档已按最新决策（**D83–D99**）撰写，**无未决偏差**。以下把收敛结果与「已被推翻的旧设计」记录清楚，
避免实现者照着旧理解动手。

### 已同步说明（最终态）

| # | 事项 | 最终结果 |
|---|---|---|
| 1 | **前端内置** | ✅ 内置 SPA 用 `go:embed web/dist`；图库/上传/相册/任务/日志/设置/后台/认证页**全部内置**（D94） |
| 2 | **主题只为「自己注册的页面」提供渲染** | ✅ 默认主题注册 `Pages = ["/"]`；主题可自行注册 `/upload`、`/gallery` 等业务页面，**Go 零改动**（D94.2 / D99.1） |
| 3 | **认证页与 `/admin/**` 永久保留** | ✅ 硬编码保留列表；`manifest.Pages` 声明了也会**被校验拒绝**（D94.2） |
| 4 | **资源前缀分离** | ✅ 主题 → `/theme-assets/**`；内置 SPA → `/assets/**`；`/themes/**` → **404**（D99.2） |
| 5 | **内嵌默认主题兜底 + seed** | ✅ 主题缺失/损坏/`Pages` 非法 → 回退内嵌默认主题，**永不白屏**；`data/themes/` 为空才 seed，升级不覆盖（D94.4） |
| 6 | **主题配置独立表** | ✅ `ThemeConfigs`（**每行一键**，带 `UpdatedBy` 可审计）+ `theme.active` 在 `SystemSettings`；三级兜底（D95） |
| 7 | **zip 安装双通道 + 9 条安全校验 + 原子性** | ✅ 且比 Komari 硬化两处：`filepath.Rel` 判路径、强制权限位 + 显式拒绝 symlink（D96） |
| 8 | **manifest 规范** | ✅ 含 `Pages`（接管范围）+ 多语言文本 + 只实现 `managed`（D98） |
| 9 | **背景图单一 URL，不做判断** | ✅ 主题配置项 `BackgroundURL`（默认 `https://api.yppp.net/api.php`）；无模式、无横竖判断、无 fetch、无缓存、**后端零参与**（D97，取代原 D85） |
| 10 | **首页内容与背景图已迁移** | ✅ 从 `site.homepage.*` / `site.background.*` 迁到**主题配置**（`ThemeConfigs`）；站点设置页**不再包含**这两项（D95 归类原则） |
| 11 | **不做缩略图** | ✅ `Uploads.ThumbURL` 保留但前端不使用；图库直接引图床 URL（D84） |
| 12 | **首页公开、其余需登录** | ✅ D86；**无游客上传** |
| 13 | **动态表单渲染器覆盖两套 schema** | ✅ 插件 schema（`IPluginConfig`）与主题 schema（`Configuration.Items`）经**适配层**规约到同一组内部类型，共用渲染组件（§7） |
| 14 | **参考图中不成立的功能** | ✅ 注册 / 套餐 / 订单 / 分享 / 探索 / 广告 / 后台注入 CSS-JS **全部不实现**（D88–D93） |

### 已被推翻的旧设计（**历史记录，不要实现**）

| 旧设计 | 现状 |
|---|---|
| 「前端以主题形式交付；Go **不** embed 前端」 | ❌ **已推翻**：前端**内置**（`go:embed web/dist`）；只有主题声明的页面走主题（D94） |
| 「主题接管**整个前端**」「认证页由内置主题渲染 / `forceDefaultTheme`」 | ❌ **已推翻**：主题**无法**接管认证页与 `/admin/**`（D94.2） |
| 主题目录带 `dist/` 子目录（`<theme>/{manifest.json, dist/, src/}`） | ❌ **已推翻**：正确布局是 **`manifest.json` + `index.html` + `assets/` 直接放主题根**（D94.3） |
| 「`/assets/**` 指向当前主题」 | ❌ **已推翻**：`/assets/**` **永远属于内置 SPA**；主题必须用 `/theme-assets/**`（D99.2） |
| 背景图三种模式（`none` / `fixed` / `acg`）+ 横竖定向 + `sessionStorage` 会话缓存 + 降级链 | ❌ **已推翻**：**单一 URL，不做任何判断**（D97） |
| `site.homepage.*` / `site.background.*` 配置键 | ❌ **已废除**：首页相关配置归**主题配置**（`ThemeConfigs`，D95） |
| ACG **后端缓存 / 定时任务** / `GET /api/web/v1/site/background` 端点 | ❌ **已废除**：背景图由前端 `<img>` 直连上游，**后端零参与**（D97） |
| `BackgroundPicker`（背景图模式选择器）组件 | ❌ **已移除**：无模式可选，背景图只是一个 URL 配置项（§6.1） |

### 本文档与其他文档的关系

- 本文档**只写前端设计**。表/字段以 [`DATA-MODEL.md`](./DATA-MODEL.md) 为准（`ThemeConfigs` 见其 §3.3），
  接口以 [`API.md`](./API.md) 为准（路由分发与静态托管见其 §10.1），
  运行机制（seed / 扫描 / 校验 / 兜底 / 日志）见 [`OPERATIONS.md`](./OPERATIONS.md)。
- 本文档提到的 `theme.*` 操作日志类型（`theme.install` / `theme.uninstall` / `theme.activate` /
  `theme.rescan` / `theme.settings.update` / `theme.settings.clear` / `theme.error`）以
  `DECISIONS.md` D96 与 `OPERATIONS.md` 的类型表为准。
- 默认主题的源目录（`server/internal/theme/embedded/`）与 `PLAN.md` W10 保持一致。
