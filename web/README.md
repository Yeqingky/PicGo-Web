# web/ — PicGo-Web 内置 SPA

React 19 + TypeScript 5.9 + Vite 7 + Tailwind CSS v4 + Radix UI + React Router 7 + Zustand 5 + Axios 1。

---

## 这个包是什么（以及不是什么）

`web/` 是 **内置 SPA** —— 它承载**除首页之外**的全部界面：

| 由**本包**（内置 SPA）渲染 | 由**主题**渲染（不在本包） |
|---|---|
| `/login`、`/first-login`、`/forgot-password`、`/reset-password` | `/`（首页） |
| `/upload`、`/gallery`、`/albums`、`/jobs`、`/logs`、`/settings` | （默认主题只注册首页；将来可注册更多页面） |
| `/admin/**`（users / storage / plugins / themes / site / logs） | |

两条边界（`docs/DECISIONS.md` D94 / D99.2、`docs/DESIGN.md` §9.5）：

1. **本包产物走 `/assets/**`**，由 Go 用 `go:embed web/dist` 打进二进制；
   **主题**走 `/theme-assets/**`，两者**不得混用**。
2. **认证页与 `/admin/**` 永久内置** —— 主题（第三方前端代码）无法接管，
   否则可伪造登录框窃取凭据（D94.2）。

> ⚠️ 因此本仓库里 `/` 只是一个**开发期占位页**（`src/features/home/home-page.tsx`）：
> 生产环境 Go 在第 3 步就把 `/` 交给主题，本包这个路由不可达；
> 但 `vite dev server` 不经过 Go 的分发，所以本地直接访问 `/` 会落到占位页。
> **落地页属于默认主题（`themes/default/`，W10），不要在本包实现。**

---

## 快速开始

```bash
pnpm install          # 依赖（注意：pnpm 12 需要 pnpm-workspace.yaml 放行 esbuild 构建）
pnpm dev              # Vite dev server，默认 http://localhost:5173
```

`pnpm dev` 会把 `/api` 与 `/healthz` 代理到 **`http://127.0.0.1:8080`**（Go 后端）。
因此联调时请同时启动后端：

```bash
# 另一个终端
cd ../server && go run ./cmd/picgo-web
```

首启会在日志里打印管理员初始密码（同时写入 `<dataDir>/initial-admin-password.txt`，权限 0600）。

### 没有后端时：mock 模式

```bash
VITE_USE_MOCK=true pnpm dev
```

mock 通过**替换 axios 的 adapter** 实现（不是起 mock server），因此**拦截器路径与真实后端一致**。
mock 账号：

| 邮箱 | 密码 | 角色 |
|---|---|---|
| 任意合法邮箱 | `adminpassword` | admin |
| 任意合法邮箱 | `userpassword` | user |

未覆盖的请求会**回落到真实 adapter**，所以可以「后端就绪一部分、mock 补一部分」。

---

## 命令

| 命令 | 作用 |
|---|---|
| `pnpm dev` | Vite dev server（`/api` 代理到 :8080） |
| `pnpm build` | `tsc --noEmit` + `vite build` → `dist/` |
| `pnpm preview` | 本地预览 `dist/`（**不含** `/api` 代理，仅看静态资源） |
| `pnpm typecheck` | `tsc --noEmit`（`tsconfig.json` + `tsconfig.node.json`） |
| `pnpm lint` | ESLint（flat config） |
| `pnpm lint:fix` | 自动修复 |

仓库根还有统一入口：`make web` / `make build-web` / `make check-web`。

### 构建产物与缓存头

`vite.config.ts` 把「很少变动」的第三方库拆成独立 chunk（`vendor-react` / `vendor-radix` / `vendor-misc`），
配合 Go 侧给 `/assets/**` 设的 `immutable, max-age=31536000`，
升级业务代码时这些 chunk 的哈希不变，用户不必重新下载（`docs/API.md` §10.1）。

`index.html` **不缓存**（`no-cache`），保证换主题/换版本立即生效。

---

## 目录结构

```
src/
├── main.tsx / app.tsx / router.tsx   入口 / Provider 装配 / 路由表
├── styles/
│   ├── tokens.css                    ★ 设计 token（逐字对应 docs/DESIGN.md §2.1）
│   └── globals.css                   Tailwind v4 入口 + @theme 映射 + 基础层
├── lib/
│   ├── http.ts                       Axios 实例：拆信封 + ApiError + 静默刷新
│   ├── sse.ts                        EventSource 封装：指数退避重连 + 事件订阅
│   ├── api/index.ts                  按域封装的请求函数（请求不要散落在组件里）
│   ├── format.ts                     字节/时间/配额/尺寸格式化
│   ├── clipboard.ts                  外链格式化（Markdown/直链/HTML，D68）+ 复制
│   ├── navigation.ts                 ★ 导航配置数组（新增页面只改这里）
│   └── utils.ts                      cn() 等
├── types/
│   ├── api.ts                        ★ 与 docs/API.md 手写同步（字段 PascalCase，D81）
│   └── schema.ts                     两套 schema 的原始类型（插件 / 主题）
├── store/                            Zustand：auth / ui / task
├── hooks/api/                        服务端状态 hooks（useAsync / useUsers / useSiteConfig）
├── components/
│   ├── ui/                           29 个 shadcn 风格基础件（Radix + Tailwind）
│   ├── layout/                       AppShell / Sidebar / Topbar / UserMenu / QuotaBar /
│   │                                  ThemeToggle / 路由守卫 / 403 / 404 / 占位页
│   ├── icons/                        本地图标（lucide 1.x 已移除品牌图标）
│   └── schema-form/                  ★ 动态表单渲染器（插件 + 主题共用）
├── i18n/                             中文为源，英文预留（D75）
├── mocks/                            VITE_USE_MOCK 的 adapter
└── features/                         按业务域切分
    ├── auth/                         login（完整）/ first-login（完整）/ forgot / reset
    ├── home/                         开发期占位（生产由主题渲染）
    ├── upload/ gallery/ albums/ jobs/ logs/ settings/
    └── admin/{users,storage,plugins,themes,site,logs}/
```

★ = **改动入口**：设计规范在 `docs/DESIGN.md`，接口契约在 `docs/API.md`。

---

## 关键约定

### 命名（D81）

| 层 | 规则 | 示例 |
|---|---|---|
| API JSON 字段 / TS **类型**字段 | **PascalCase**，缩写全大写 | `AccessToken` / `JobUID` / `UID` |
| TS **变量 / 函数 / store** | camelCase | `const accessToken = res.AccessToken` |
| i18n key | `SCREAMING_SNAKE_CASE` | `AUTH_LOGIN_SUBMIT` |
| 组件文件 / 导出 | kebab-case 文件 + PascalCase 导出 | `login-page.tsx` → `LoginPage` |

**绝不能改的例外**：Lsky 兼容层字段（snake_case）、`settings` 配置键（`site.name`）、
picgo 侧字段与驱动配置字段名（`picgoPlugins` / `repo` / `token` / `path`）。

### 状态职责（DESIGN §11）

- **服务端数据不进 Zustand**（避免双份真相）。用 `hooks/api/*` + Axios。
- **Zustand 只放用户意图与本地 UI 状态**：登录态快照、主题、侧栏、任务跟踪。
- **令牌不在 store 里** —— 它在 httpOnly Cookie 中（D30），JS 读不到。
- 两者用**标识符**连接（`StorageUID` / `JobUID`），不要把整个响应对象塞进 store。

### 样式（DESIGN §2）

- **一律用语义类**：`bg-background` / `text-muted-foreground` / `border-border`；
  **禁止硬编码颜色**（`#fff`、`hsl(...)` 字面量）。
- 强调色 `--brand`（#007AFF）**只用于**：主 CTA、当前导航项、聚焦环、进度条。
- 状态色：`success` 成功 / `warning` 部分失败与配额接近上限 / `destructive` 失败与删除 / `info` 进行中。

### HTTP 与鉴权

- `baseURL = /api/web/v1`；响应体是统一信封 `{Code, Message, Data}`。
- 拦截器**自动把 `Code === 0` 的 `Data` 拆出来**，`Code !== 0` 抛 `ApiError`。
- `40102` / `40103` → **单飞（single-flight）静默刷新**一次并重放原请求；
  失败则清登录态，由 `RequireAuth` 声明式跳登录页（不做命令式 `window.location`）。

### i18n（D75）

- 文案**禁止硬编码**，用 `t('KEY')`；后端返回的 `Message` 已是中文，直接展示不二次翻译。
- 例外：插件 schema 的 `Alias` / `Message` 由 picgo 的 i18n 提供；主题 schema 的 `Name` / `Help`
  由服务端按 `Accept-Language` 解析成单串（D98）——两者都**直接展示**。

### a11y（DESIGN §12）

- 图标按钮**必须**传 `label`（`IconButton` 强制要求），它同时作为 `aria-label` 与悬浮提示。
- 焦点环用 `--ring`，只在键盘导航时显示（`:focus-visible`）。
- 对话框/抽屉用 Radix，自带 focus trap + `Esc` + `aria-modal`。
- 尊重 `prefers-reduced-motion`（`globals.css` 已全局处理）。

### 动态表单（DESIGN §7）

两套 schema 经 `components/schema-form/adapt.ts` 规约到同一组内部字段类型，共用同一套渲染组件：

| 来源 | 类型词汇 | 是否需要回源 |
|---|---|---|
| 插件 / 驱动（`GET /api/uploaders` 的 `Config`） | `input` / `password` / `list` / `checkbox` / `confirm` / `editor` | ✅ `DependsOn` 联动要回源 agent 重求值 |
| 主题（`manifest.Configuration.Items`） | `string` / `text` / `number` / `switch` / `select` / `json` | ❌ 静态 schema，本地渲染 |

⚠️ 前端**永不执行插件代码**；`dependsOn` 是把当前值回传后端求值。

---

## 与文档的边界

| 想知道 | 看 |
|---|---|
| 设计 token、路由、页面、组件、交互 | `docs/DESIGN.md`（**唯一真源**） |
| 接口契约（含错误码、字段名） | `docs/API.md` |
| 决策与取舍（含「为什么不用某个方案」） | `docs/DECISIONS.md` |
| 项目级规范（命名 / DB / 检查清单） | `../AGENTS.md` |

**改接口时**：先改 `docs/API.md`，再同步 `src/types/api.ts`（AGENTS.md §9）。

---

## 工程注意

- **pnpm 12 需要 `pnpm-workspace.yaml` 放行 `esbuild` 的构建脚本**，否则
  `pnpm install` 会以 `ERR_PNPM_IGNORED_BUILDS` 失败（esbuild 由 vite 传递依赖引入）。
- **`lucide-react` 1.x 移除了品牌图标**（`Github` 已不存在），
  因此 GitHub 标记是本地 SVG：`src/components/icons/github-mark.tsx`。
- **`/` 是开发期占位**，生产由主题渲染（见上文说明）。
- `AlertDialog` 中的**异步确认按钮不要用 `AlertDialogAction`**（它会立即关闭对话框）；
  用普通 `Button` + 手动控制 `open`（DESIGN §9.3）。
