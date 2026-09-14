# 架构设计

> **上位约束**：[`DECISIONS.md`](./DECISIONS.md)（98 条决策）。本文档与之冲突时以 `DECISIONS.md` 为准。
> **表结构真源**：[`DATA-MODEL.md`](./DATA-MODEL.md)。本文档不重复字段定义，只讲结构。
> **接口契约**：[`API.md`](./API.md)。**运行机制**：[`OPERATIONS.md`](./OPERATIONS.md)。
> **picgo 集成与补丁细节**：[`PICGO-INTEGRATION.md`](./PICGO-INTEGRATION.md)。

---

## 1. 项目定位

| 维度 | 定位 | 决策 |
|---|---|---|
| 目标用户 | **自用为主**，其次朋友 / 小团队之间分享 | D1 |
| 不是什么 | 不是商业化图床 SaaS（无商城、支付、会员、工单、兑换码） | D60 |
| 后端职责 | **元数据 + 管理 + 调度**：用户、配额、图片记录、存储配置、插件管理、任务队列、审计 | D2 |
| 后端**不做** | **不落盘存储、不做图片分发、不生成图片 URL** —— 全部交给 picgo-core | D2 / D59 |
| 存储驱动 | **仅管理员可配置**，一套全局驱动池，所有用户共用（不做驱动级租户） | D4 |
| 多租户维度 | 只有两个：**多用户** + **存储空间配额** | D5 |
| 功能骨架来源 | 参考 lsky-pro，但砍掉「存储 + 分发」整条链路 | D3 |

**核心取舍一句话**：我们把「图片存在哪、URL 长什么样」完全交给 PicGo 生态（驱动 + 插件），
自己只维护「谁在什么时候传了什么、占了多少配额、能不能删」这部分**业务元数据**。

**已砍掉的功能**（不要在设计或实现中引入）：用户组、标签、公开画廊、API 限流、
自助注册、内容去重、回收站 / 软删除、商城支付、兑换码、工单、Passkey、水印、图片审核、
PicGo Cloud、lsky-pro 历史数据导入、备份 / 恢复功能（D53–D63、D69、D82）。

---

## 2. 总体拓扑

### 2.1 三层结构

```
┌────────────────────────────────────────────────────────────────────┐
│  Browser                                                           │
│  ① 内置 SPA —— Vite 7 构建，产物由 Go 用 embed.FS 打进二进制           │
│     登录 / 图库 / 上传 / 存储配置 / 插件 / 日志 / 设置 / 用户           │
│  ② 首页主题 —— 页面级可替换前端，运行时从 <dataDir>/themes/ 读取        │
│     （首次只接管 /，见 §9；其余页面一律走内置 SPA）                     │
│  · Axios 调 REST + EventSource 订阅 SSE                             │
│  · Axios baseURL = /api/web/v1                                      │
└───────────────────────────┬────────────────────────────────────────┘
                            │
              ┌─────────────┴──────────────┐
              │                            │
   /api/web/v1/**  内部 API       /api/v1/**  Lsky v1 兼容层
   （本项目前端 + API Token）      （外部冻结契约，第三方客户端直连）
   信封 {Code,Message,Data}       信封 {status,message,data}
   PascalCase 字段                snake_case 字段
              │                            │
              └─────────────┬──────────────┘
                            ▼
┌────────────────────────────────────────────────────────────────────┐
│  Go server  :8080                        ★ 主进程                  │
│  Gin + GORM                                                        │
│  · 静态资源托管：内置 SPA（embed web/dist）+ 首页主题（data/themes/）  │
│    · 路由分发见 §9.3；/assets/** 属内置 SPA，/theme-assets/** 属主题   │
│  · 鉴权：邮箱密码 / GitHub OAuth（先绑定）/ JWT + refresh / API Token │
│  · REST API：用户 配额 图库 相册 存储 插件 任务 日志 设置              │
│  · SQLite（默认）/ PostgreSQL（可选）                                │
│  · 配置真相源：SystemSettings / UserSettings（KV）                   │
│  · 上传队列 + worker pool（默认并发 1）                              │
│  · 上传暂存落盘 <dataDir>/uploads（仅中转，非图床）                   │
│  · SSE 事件总线；操作日志 / 邮件日志                                  │
└───────────────────────────┬────────────────────────────────────────┘
                            │ HTTP 127.0.0.1:36678
                            │ 所有请求带 X-Agent-Token
                            ▼
┌────────────────────────────────────────────────────────────────────┐
│  picgo-agent  (Node 24 + TypeScript + hono)   ★ 侧车，Go 托管        │
│  · 单例 PicGo 实例（picgo-core 3.0.2 + 自维护补丁）                   │
│  · config.json 读写（**键级合并**，D22）                             │
│  · 存储驱动同步（uploaderConfig 多配置，D64/D65）                     │
│  · 插件：列表 / 安装 / 卸载 / 更新 / 启停 / README                    │
│  · 上传执行（单文件 + 同步，D39）                                     │
│  · 魔法路径 / 魔法文件名（beforeUploadPlugins，D44/D70）              │
│  · 远端删除（`remove` 事件 + guiApi shim，D47）                       │
│  · 任务日志与 SSE                                                    │
└───────────────────────────┬────────────────────────────────────────┘
                            ▼
┌────────────────────────────────────────────────────────────────────┐
│  picgo-core → 存储驱动（插件 / 内置）                                 │
│  GitHub · S3 · WebDAV · 七牛 · 阿里 OSS · 腾讯 COS · …（生态扩展）     │
│  ★ 图片实际存储、URL 生成、内容分发全部发生在这里，与本项目无关         │
└────────────────────────────────────────────────────────────────────┘
```

### 2.2 API 路径分层（D80）

| 挂载点 | 归属 | 信封 | 字段命名 | 说明 |
|---|---|---|---|---|
| **`/api/web/v1/**`** | PicGo-Web 内部 API | `{Code, Message, Data}` | PascalCase | 本项目前端与 API Token 使用 |
| **`/api/v1/**`** | Lsky v1 兼容层 | `{status, message, data}` | snake_case | **外部冻结契约**，内部 API 永不占用 |
| **`/assets/**`** | **内置 SPA** 的静态资源（embed） | — | — | 与主题资源**严格分离**（§9.4） |
| **`/theme-assets/**`** | **当前主题**的静态资源 | — | — | 防目录穿越；`/themes/**` 一律 `404` |
| `/healthz` | 健康检查 | — | **无信封** | 容器探针直接解析 |
| 其余路径 | 内置 SPA 的 SPA 回退，或**当前主题**（若被其 `Pages` 接管） | — | — | 分发算法见 §9.3 |

**两个前缀天然隔离，不存在路径冲突。** 前一版设计曾让内部 API 与 Lsky 层共享 `/api/v1`，
并把内部相册让位到 `/api/v1/gallery/albums`；**D80 之后已取消**，
内部相册回到普通路径 **`/api/web/v1/albums`**。

仍保留一道**启动时路由冲突检测**作为防护：注册完路由后校验内部路由前缀不与 Lsky 保留集
（`/api/v1/{tokens,profile,strategies,upload,images,albums}`）重叠，重叠则直接 `panic`。
这不是为了解决现有冲突，而是**防止将来有人新增接口时误占 Lsky 契约**。

### 2.3 为什么必须有 Node 侧车（D6）

`picgo-core` 的插件是 **npm 包 + `require()` 动态加载**，只能在 Node 运行时里跑。
Go 无法安全地加载它们。若走「Go shell 出 `picgo upload`」的方案，会失去：

- 上传进度与生命周期事件
- 结构化错误（`upload()` 的失败语义见 §8.2）
- 生命周期钩子（魔法路径正是靠 `beforeUploadPlugins` 实现）
- 多配置图床管理（`uploaderConfig`）
- 插件安装过程的实时日志

GUI（Electron）与 CLI 都是 Node 宿主，本项目**沿用同一模式**：一个 Node 进程持有唯一的
`PicGo` 实例，对外提供稳定的 JSON 契约。

### 2.4 进程边界与生命周期

| 组件 | 进程 | 由谁拉起 | 崩溃影响 | 恢复策略 |
|---|---|---|---|---|
| **Go server** | 主进程 | docker（官方唯一部署方式，D76） | 全站不可用 | 容器重启策略 |
| **picgo-agent** | Go 的子进程 | Go 启动时拉起（`PICGO_WEB_AGENT_AUTOSTART=true`）<br>或独立部署（`=false` + `PICGO_WEB_AGENT_URL`） | **功能降级**：上传、插件、存储同步不可用；<br>登录、图库浏览、日志、设置、用户管理仍正常 | Go 健康探测 + 退避重启 |
| **内置 SPA** | 无独立进程 | Go `embed.FS` 直接托管（除主题接管的页面外的全部前端） | 随 Go 主进程 | — |
| **首页主题** | 无独立进程 | Go 从 `<dataDir>/themes/` 读文件；缺失 / 损坏 / `Pages` 非法 → 回退**内嵌默认主题** | 随 Go 主进程（**永不白屏**） | 启动时 seed；切换主题无需重启 |
| **picgo 内置 HTTP server** | agent 进程内 | 默认**不启用** | — | 可选开启（供插件自带路由），见 `PICGO-INTEGRATION.md` |

### 2.5 端口分配（D6）

| 进程 | 默认端口 | 说明 |
|---|---|---|
| Go server | **`8080`** | 对外唯一入口（`PICGO_WEB_LISTEN`） |
| picgo-agent | `36678` | **仅 `127.0.0.1`**，所有请求带 `X-Agent-Token` |
| picgo-core 内置 server | `36677` | **默认不启用**。这是 picgo-core 的默认端口，我们刻意避开它 —— Go 服务因此不用 `36677` |

**agent 健康探测与重启（D7）**：

```
每 N 秒 GET http://127.0.0.1:36678/healthz
  超时 2s
  ├─ 成功 → 标记 agentStatus = up，重置退避计数
  └─ 失败 → 重试 3 次
        └─ 仍失败 → 结束子进程，按指数退避重启（最多 5 次）
              └─ 5 次后放弃 → agentStatus = down，不再自动重启
                  需要管理员在「设置 → 系统」手动触发重启
```

**降级表现**：

| 情况 | 表现 |
|---|---|
| agent 不可用 | 依赖它的接口返回 `503` + `50002`；`GET /system/info` 的 `AgentStatus = down` |
| 前端感知 | 顶部横幅提示「上传内核不可用」（SSE `system.notice`）；上传按钮置灰；`/healthz` 轮询等待恢复 |
| 队列中的任务 | 标记为 `queued` 保持不动，等 agent 恢复后继续（不丢任务） |
| 正在执行的任务 | 超时后按失败处理（`upload.itemTimeoutSeconds`），记录失败原因 |

**agent 重启的触发点**（除崩溃外）：

- 插件安装 / 卸载 / 更新成功后**主动重启**，换取干净的插件加载状态（低频操作，可接受）
- 重启期间上传接口短暂返回 `503`，前端通过 SSE `system.notice` 展示「正在重启内核」并轮询 `/healthz`

---

## 3. 目录结构

```
PicGo-Web/
├── README.md                     # 用户看部署（只写 docker compose）；开发看命令
├── LICENSE
├── Makefile                      # dev / web / agent / server / build / pack / check
├── .env.example                  # 启动引导类环境变量示例
├── .gitignore
├── docker-compose.yml            # ★ 官方部署入口（Go + agent + SQLite，单容器）
├── docker-compose.pgsql.yml      # 覆盖文件：切 PostgreSQL
│
├── docs/
│   ├── README.md                 # 文档索引与阅读顺序
│   ├── DECISIONS.md              # 决策记录（最高约束）
│   ├── ARCHITECTURE.md           # ← 本文档
│   ├── DATA-MODEL.md             # 表设计（唯一真源）
│   ├── API.md                    # 内部 REST + SSE + Lsky 兼容 + agent 契约
│   ├── OPERATIONS.md             # 队列/配额/限流/日志/删除机制
│   ├── PICGO-INTEGRATION.md      # picgo-core 集成、补丁、魔法路径
│   ├── PLAN.md                   # 分工、里程碑、验收
│   └── research/                 # lsky-pro / skyImage 调研报告（参考用）
│
├── deploy/
│   └── docker/
│       ├── Dockerfile            # 多阶段：web 构建 → 默认主题打包 → agent 构建 → Go 编译 → 运行时
│       └── entrypoint.sh
│
├── themes/
│   └── default/                  # ★ 默认首页主题的打包产物（由 `make theme` 生成）
│       ├── manifest.json         #   元数据 + 配置 schema + Pages（D94.2 / D98）
│       ├── index.html
│       ├── assets/               # 构建时 base 必须为 /theme-assets/（§9.4）
│       └── screenshot.png
│
├── server/                       # ★ Go 后端（主要开发语言）
│   ├── go.mod
│   ├── cmd/
│   │   └── picgo-web/main.go     # 入口：加载配置 → 开会话 → 迁移 → 起 HTTP → 停 SIGTERM
│   └── internal/
│       ├── config/               # 引导类配置（env/.env）+ defaults.go 配置键默认值表
│       ├── database/             # GORM 初始化（NoLowerCase）+ dsn.go + migrations.go + SchemaMeta
│       ├── model/                # 21 张表的 GORM 模型（每个显式 TableName()，见 §5）
│       ├── repository/           # 数据访问层：**唯一** 允许直接使用 *gorm.DB 的层
│       ├── service/              # 业务逻辑层（配额/限流/队列/存储同步/删除/日志）
│       ├── handler/              # HTTP 处理层（Gin）：参数绑定与响应封装，不写业务
│       │   │                     #   内部 API（/api/web/v1/**），PascalCase 信封
│       │   └── lsky/             # Lsky v1 兼容处理器（独立信封 {status,message,data}）
│       ├── middleware/           # Recovery / Logger / CORS / RequireAuth / RequireAdmin / RequireNotDisabled
│       ├── agent/                # agent HTTP 客户端（超时/重试/X-Agent-Token）+ 子进程生命周期 + mock
│       ├── auth/                 # JWT 签发校验、bcrypt、refresh/API Token、GitHub OAuth
│       ├── crypto/               # AES-256-GCM；主密钥加载与生成（data/secret.key）
│       ├── settings/             # SettingsService：KV 读写 + 默认值合并 + onChanged 回调
│       ├── events/               # SSE Hub（对外推送）+ 内部事件总线（进程内钩子）
│       ├── taskqueue/            # 上传队列与 worker pool（并发=upload.concurrency）
│       ├── mail/                 # SMTP 发送 + 邮件模板 + 发信日志落库
│       ├── response/             # 统一响应体、错误码常量、分页结构
│       ├── logger/               # log/slog JSON Handler 初始化与级别控制
│       ├── theme/                # ★ 主题系统（D94–D99）：扫 manifest 解析校验 + Pages 最长前缀匹配分发
│       │                         #   + /theme-assets 防目录穿越 + 内嵌默认主题兜底 + zip 安装校验
│       │                         #   + 启动 seed + ThemeConfigs 读写 + 缓存头
│       │   └── embedded/         #   go:embed 的默认主题压缩归档（兜底与 seed 的唯一来源）
│       └── web/                  # 内置 SPA：embed.FS 托管 web/dist + SPA fallback + assets 缓存头
│
├── picgo-agent/                  # ★ Node 侧车（picgo-core 宿主）
│   ├── package.json              # "picgo": "file:../../PicGo-Core"（见 §8.6）
│   ├── tsconfig.json
│   └── src/
│       ├── index.ts              # 入口：读 env → 建 PicGo 实例 → 起 hono → 优雅退出
│       ├── server.ts             # hono app + 路由挂载 + X-Agent-Token 中间件
│       ├── picgo/
│       │   ├── instance.ts       # 单例 PicGo（configPath 由 env 注入）
│       │   ├── config.ts         # 读取 / 键级合并写入 / 写前备份
│       │   ├── uploaders.ts      # uploaderConfig 封装 + evaluatePluginConfig 求值 + 能力探测
│       │   ├── plugins.ts        # 列表 / 安装 / 卸载 / 更新 / README
│       │   ├── magic-path.ts     # 魔法路径与文件名（beforeUploadPlugins 钩子）
│       │   ├── remove.ts         # 远端删除（emit('remove') + guiApi shim）
│       │   └── capabilities.ts   # 驱动能力探测与缓存
│       ├── jobs/                 # 内存 job 表 + 日志环形缓冲
│       ├── routes/               # 按域拆分的路由：config / uploaders / plugins / upload / jobs / events
│       └── events/               # SSE 广播（含 25s ping）
│
└── web/                          # ★ React 前端
    ├── package.json
    ├── vite.config.ts            # dev 时 proxy '/api' → :8080；内置 SPA 的 base 为 '/'
    ├── index.html                # 内置 SPA 入口
    ├── theme-default/            # ★ 默认首页主题的源码（独立构建入口）
    │   ├── index.html            #   base 必须设为 /theme-assets/
    │   ├── manifest.json         #   与主题一起打包（Pages / Configuration.Items）
    │   └── src/                  #   落地页：Hero + 能力 + 场景 + FAQ + CTA（DESIGN.md §4.2）
    └── src/
        ├── main.tsx
        ├── App.tsx               # createBrowserRouter 挂载
        ├── routes/               # 路由表 + RequireAuth / RequireAdmin 守卫
        ├── layouts/              # AppShell（侧栏+顶栏）、AuthLayout
        ├── features/             # 按业务域切分（见 §10.1）
        ├── components/
        │   ├── ui/               # Radix 原语 + Tailwind 封装（shadcn 风格）
        │   └── schema-form/      # 通用配置表单渲染器
        │                         #   消费插件 IPluginConfig[] 与主题 Configuration.Items
        │                         #   两者命名不同，由内部适配层规约到同一组字段类型
        ├── lib/                  # http.ts（Axios 实例+拦截器）、sse.ts、utils.ts（cn）
        ├── hooks/                # 服务端数据 hooks（列表/详情/分页/筛选）
        ├── store/                # Zustand（只放客户端状态）
        ├── types/api.ts          # 与 docs/API.md 手工同步的类型定义（字段 PascalCase）
        └── i18n/                 # zh-CN（默认）+ 预留 en
```

### 3.1 分层纪律（D77.2）

```
handler  →  service  →  repository  →  *gorm.DB
   │           │            │
   │           │            └─ 只有这一层碰 GORM；SQL 与索引都写在这里
   │           └─ 业务规则：配额、限流、队列、状态机、审计日志
   └─ 只做：参数绑定与校验、调用 service、包装 response；不写业务判断
```

| 规则 | 说明 |
|---|---|
| `handler` 不得 import `gorm.io/gorm` | 强制于 CI（`go vet` + 自定义检查） |
| `model` 是纯结构体，不带业务方法 | 避免模型方法里偷偷读写 DB |
| 跨领域协作走 `service` | 例如「删除图片」由 `UploadService` 编排，调用 `StorageService`、`OperationLogService` |
| 对外只暴露 `UID` | service/handler 层不得把自增 `ID` 放进响应结构 |

---

## 4. 技术栈

### 4.1 后端（D9–D12、D16、D17）

| 关注点 | 选型 | 版本 | 理由 |
|---|---|---|---|
| Web 框架 | `gin-gonic/gin` | v1.12 | 生态成熟、中间件齐全 |
| ORM | `gorm.io/gorm` | v1.31 | 方言无关的模型层 |
| 数据库（默认） | `github.com/glebarez/sqlite` | v1.11 | **纯 Go SQLite，免 CGO** |
| 数据库（可选） | `gorm.io/driver/postgres` | v1.6（pgx） | PostgreSQL，同样纯 Go |
| JWT | `golang-jwt/jwt/v5` | v5 | access token 签发与校验 |
| OAuth2 | `golang.org/x/oauth2` | 最新 | GitHub 授权码流（D26） |
| 密码 | `golang.org/x/crypto/bcrypt` | 最新 | cost = 12（D24） |
| 日志 | `log/slog` | 标准库 | JSON Handler，无额外依赖（D17） |
| 迁移 | 自研 | — | `SchemaMeta` + `schemaMigrations` 升序表（D16） |
| 配置 | env + `.env` | — | 三层分层的第 1 层（D18） |

> **硬性要求**：`CGO_ENABLED=0 go build` 必须成功。这排除了 `mattn/go-sqlite3`
> 等 CGO 依赖，也决定了必须用纯 Go 的 SQLite 实现。

### 4.2 前端（D13、D14）

| 关注点 | 选型 | 版本 |
|---|---|---|
| 框架 | React | 19 |
| 语言 | TypeScript | 5.9 |
| 构建 | Vite | 7 |
| 样式 | Tailwind CSS | v4（`@tailwindcss/vite` 插件） |
| UI 原语 | Radix UI | 按需引入（dialog / dropdown-menu / select / tabs / tooltip / avatar / switch …） |
| 路由 | React Router | 7（`createBrowserRouter`） |
| 状态 | Zustand | 5 |
| HTTP | Axios | 1 |
| 其他 | lucide-react（图标）、sonner（toast）、class-variance-authority + tailwind-merge（组件变体） | — |

### 4.3 侧车（D15）

| 关注点 | 选型 | 版本 |
|---|---|---|
| 运行时 | Node | 24 |
| 语言 | TypeScript | 5.9 |
| 路由 | `hono` | 4（轻量、原生 SSE 友好） |
| picgo | `picgo` | 3.0.2（`file:../../PicGo-Core`，走自维护 `PicGo-Web` 分支，D49） |

> **注意**：`PicGo-Core` 的 `dist/` 被 gitignore 且没有 `prepare: build`，
> 所以 **不能** 用 git 依赖，必须 `file:` 或 tarball（详见 §8.6 与 `PICGO-INTEGRATION.md`）。
> 本地必须先 `cd PicGo-Core && pnpm install && pnpm build`。

### 4.4 镜像与运行时（D76）

- 单镜像多阶段构建：web 产物 → agent 产物 → Go 二进制 → 运行时层
- 运行时层**必须包含 Node**（agent 需要），但不含 Electron
- `CGO_ENABLED=0` 编译 Go，运行时基础镜像可用 `alpine` 或 `debian-slim`
- 数据卷 `./data`：数据库 + `picgo/` 配置 + 上传暂存 + `secret.key`。
  **备份由运维直接备份该目录**（项目不提供导出/导入功能，D82）

---

## 5. 数据库与命名规范

> 字段级定义以 [`DATA-MODEL.md`](./DATA-MODEL.md) 为唯一真源，本节只讲**架构层面的规则**。

### 5.1 双数据库方言

支持 **SQLite（默认）** 与 **PostgreSQL（可选）**，通过环境变量切换（D11、D12）：

```dotenv
PICGO_WEB_DB_DRIVER=sqlite        # sqlite | postgres
PICGO_WEB_DB_DSN=                 # 非空时完全覆盖下面的拼装结果
PICGO_WEB_SQLITE_PATH=./data/picgo-web.db
PICGO_WEB_PG_HOST / PORT / USER / PASSWORD / DBNAME / SSLMODE / TIMEZONE
```

```go
switch cfg.DBDriver {
case "postgres":
    dialector = postgres.Open(dsn)   // gorm.io/driver/postgres（pgx，纯 Go）
default:
    dialector = sqlite.Open(dsn)     // github.com/glebarez/sqlite（纯 Go，无 CGO）
}
```

### 5.2 命名规范：PascalCase（D81）

> 主人指定：「项目中 api 变量 数据库等尽量使用大驼峰命名法」

| 层 | 命名 | 示例 |
|---|---|---|
| **API JSON 字段** | **PascalCase** | `AccessToken` / `JobUID` / `StorageUID` / `CreatedAt` |
| **API Query 参数名** | **PascalCase**（与响应字段保持一致） | `?Page=1&PageSize=20&Keyword=xxx&StorageUID=st_01` |
| **数据库表名** | **PascalCase 复数** | `Users` / `StorageConfigs` / `OperationLogs` / `JobItems` |
| **数据库列名** | **PascalCase** | `UID` / `UserUID` / `FileName` / `CreatedAt` |
| Go 结构体字段 | PascalCase（本来就是） | `UserUID` / `ImageURL` |
| 前端 TS **类型字段** | **PascalCase**（与 API JSON 一致，不手写转换） | `interface Upload { UID: string; JobUID: string }` |
| 前端 TS **变量 / 函数 / store** | camelCase（**不变**，符合 JS 生态） | `const accessToken = res.AccessToken` / `fetchUploads()` |
| Go 局部变量 / 函数 | camelCase（**不变**） | `func handleUpload()` |
| **URL 路径段** | **小写复数** | `/api/web/v1/storage/configs`、`/uploads`、`/albums`、`/logs` |

**缩写词一律全大写**：`UID` / `URL` / `ID` / `API` / `HTTP` / `TLS` / `SMTP` / `CSRF` / `JSON`
（符合 Go 社区惯例，golint / staticcheck 推荐）。

> **Query 参数也用 PascalCase**：与响应字段同名同形，避免 `Page` / `page` 语义割裂，
> 也让前端能直接用同一套类型定义构造请求。详见 [`API.md`](./API.md)。

```
✅ UserUID   JobUID   StorageUID   ImageURL   APIKey   ID   ThumbURL
❌ UserUid   JobUid   StorageUid   ImageUrl   ApiKey   Id   ThumbUrl
```

#### 硬性例外（**绝不能改**）

| 对象 | 保持原样 | 原因 |
|---|---|---|
| **Lsky 兼容层一切** | 路径 `/api/v1/**`；字段 `strategy_id` / `capacity` / `useCapacity`；包络 `{status, message, data}` | **外部冻结契约**，第三方客户端按此实现 |
| 环境变量 | `UPPER_SNAKE_CASE`：`PICGO_WEB_LISTEN` / `PICGO_WEB_DB_DRIVER` … | 运维习惯；容器编排依赖 |
| `settings` 配置键 | `dot.lowerCamel`：`site.name` / `upload.rateLimit.perHour` / `mail.host` … | 只是 KV 表的字符串 key，不是列名；改它得不偿失 |
| picgo 侧字段名 | `_id` / `_configName` / `picBed` / `picgoPlugins` / `uploader.<type>.configList` | 跟随 picgo-core；我们 DB 用 `UID`/`Name`，**转 picgo 时映射**（D65） |
| picgo 的 `IImgInfo` 字段 | `fileName` / `imgUrl` / `extname` / `sha` … | 存在 `UploadResults.RawOutput` 里，删除远端时要**原封不动交回插件**（D47） |
| 驱动配置字段名 | `repo` / `token` / `path` / `bucket` … | 插件自定义，非本项目所有 |
| 操作日志 `Type` 取值 | `upload` / `user.create` / `mail.send` … | 字符串枚举值，用于过滤与搜索，改了会打断已有日志的可读性 |

### 5.3 GORM 实现要点（D81.4）

```go
// internal/database/gorm.go
gorm.Open(dialector, &gorm.Config{
    NamingStrategy: schema.NamingStrategy{
        NoLowerCase: true,   // ← 关键：不做 snake_case 转换
    },
})
```

- **`NoLowerCase: true`** 之后 `User` → 表 `Users`，`UserUID` → 列 `UserUID`
- **每个模型显式实现 `TableName()`**，不依赖词形变化（inflection）库 ——
  避免 `OAuthIdentity` 之类拼出意料之外的复数：

  ```go
  func (User) TableName() string          { return "Users" }
  func (OAuthIdentity) TableName() string { return "OAuthIdentities" }
  func (APIToken) TableName() string      { return "APITokens" }
  func (SchemaMeta) TableName() string    { return "SchemaMeta" }  // 单数，刻意
  ```

- ⚠️ **PostgreSQL 会把未加引号的标识符折成小写**。GORM 生成的 SQL 总是带双引号
  （`SELECT * FROM "Users" WHERE "UID" = $1`），因此**应用层不受影响**；
  但**手写原生 SQL、迁移脚本、DBA 临时查询必须自己带双引号** ——
  写 `SELECT * FROM Users` 在 PgSQL 下会报 `relation "users" does not exist`。
  迁移里需要原生 SQL 时必须写成：

  ```go
  tx.Exec(`CREATE INDEX IF NOT EXISTS idx_uploads_user_created
           ON "Uploads" ("UserUID", "CreatedAt" DESC)`)
  ```

- SQLite 标识符对 ASCII 大小写不敏感，两种写法都能跑，不影响本地开发；
  为一致性**建议两方言统一加双引号**。
- 索引名保持 snake_case（`idx_uploads_user_created`），仅为运维可读性，不参与 ORM 映射。

### 5.4 跨方言一致性规则

| 关注点 | 统一做法 | 原因 |
|---|---|---|
| 内部主键 | `uint64` + `primaryKey;autoIncrement` | SQLite `INTEGER PRIMARY KEY` / PgSQL `BIGSERIAL`，GORM 自动适配 |
| **对外标识** | **`UID` `VARCHAR(32)` + `uniqueIndex`**（ULID 前缀如 `u_` / `up_` / `st_` / `job_` / `log_`） | 与自增 ID 解耦；便于将来分库/归档 |
| 时间 | **一律 `int64` Unix 秒**（`CreatedAt` / `UpdatedAt` / …） | 彻底避开时区与方言时间类型差异 |
| JSON 列 | `TEXT` + `gorm:"serializer:json"` | SQLite 无 jsonb，避免方言分支 |
| 枚举 | **`VARCHAR` 存字符串** | 新增取值不涉及 DDL；可读（D77） |
| 外键 | **只建索引，不建 `FOREIGN KEY` 约束** | 两方言行为差异大；阻碍分表/归档；一致性由应用层保证 |
| 删除 | **硬删除**（D46） | 无软删除、无回收站 |
| 扩展位 | 主要业务表带 **`Metadata` JSON 列** | 新字段可先落此，**无需迁移**（D77） |
| NOT NULL | 除语义必需外**允许 NULL** | 避免将来加列要回填历史数据 |

> 明确**不使用** `gorm.io/datatypes`（依赖 MySQL 方言）。

### 5.5 分表原则（D78）

**同一种语义只放一张表；不同生命周期 / 敏感级 / 体量的内容一律拆表。**

| 拆表依据 | 本项目实例 | 收益 |
|---|---|---|
| **敏感级不同** | `StorageSecrets` 独立于 `StorageConfigs` | 日常列表查询**不可能**误带出密钥 |
| **体量不同** | `UploadResults`（大 JSON）独立于 `Uploads`（热点列） | 图库列表查询更轻 |
| **写入频率不同** | `LoginAttempts` / `JobLogs` / `OperationLogs` 独立 | 高频写不锁主表 |
| **生命周期不同** | `JobLogs`（短期清理）vs `OperationLogs`（保留 180 天） | 清理策略互不干扰 |
| **可选性不同** | `UserProfiles` 独立于 `Users` | 认证数据查询天然不带展示列 |
| **一对多关系** | `EmailLogs` 独立成表，不塞 JSON | 可索引、可分页、可统计 |
| **生命周期不同**（主题） | `ThemeConfigs` 独立于 `SystemSettings`（D95） | 主题配置随主题装卸而生灭；每行带 `UpdatedBy`，可逐键审计；两个管理员改不同键**不互相覆盖** |

**域分组（21 张表，详见 [`DATA-MODEL.md`](./DATA-MODEL.md)）**：

```
身份鉴权  Users / UserProfiles / OAuthIdentities / RefreshTokens / APITokens / LoginAttempts
存储配置  StorageConfigs / StorageSecrets
主题配置  ThemeConfigs
媒体资源  Uploads / UploadResults / Albums
任务执行  Jobs / JobItems / JobLogs
审计记录  OperationLogs / EmailLogs
系统配置  SystemSettings / UserSettings
插件缓存  Plugins
迁移版本  SchemaMeta
```

### 5.6 迁移

`SchemaMeta` + `schemaMigrations` 升序表（D16、D77.1）：

- **只追加条目**；已发布条目**永不修改**
- 每条迁移在**单个事务**内完成；成功后写回 `SchemaMeta.Version`
- 失败则回滚并终止启动（`PICGO_WEB_DB_AUTO_MIGRATE=false` 时仅告警）
- `SchemaMeta.Version` 是整数序列，与程序版本**解耦**（只增不减）

---

## 6. 配置分层

### 6.1 三级合并（D18）

```
┌─────────────────────────────────────────────────────────────────────┐
│ 第 1 层  环境变量 / .env / docker compose environment                │
│          启动引导类 · 只读 · 不可热更                                  │
│          ── 监听地址、数据库连接、数据目录、上传暂存目录                 │
│          ── 加密主密钥、agent 地址与令牌、agent 是否自启                 │
│          ── 日志级别、可信代理                                         │
└───────────────────────────┬─────────────────────────────────────────┘
                            │ 覆盖
                            ▼
┌─────────────────────────────────────────────────────────────────────┐
│ 第 2 层  数据库 SystemSettings / UserSettings（KV）                   │
│          业务配置 · 运行时可改 · ★ DB 为真相源                         │
│          ── 站点信息、新建用户默认配额                                 │
│          ── 上传限制（大小/扩展名/并发/重试/超时/队列上限）              │
│          ── 上传限流（默认禁用）、邮件 SMTP、GitHub OAuth 凭据           │
│          ── 安全策略（会话时长/登录限流）、日志保留、picgo 源与代理        │
│          ── 对外集成开关（Lsky 兼容层）                                │
│          ── 当前启用的主题：theme.active（**只有这一个键**；主题自身的    │
│             配置值在独立表 ThemeConfigs，见 §9.8）                      │
│          敏感值 AES-256-GCM 加密入库（Encrypted = true）               │
└───────────────────────────┬─────────────────────────────────────────┘
                            │ 覆盖
                            ▼
┌─────────────────────────────────────────────────────────────────────┐
│ 第 3 层  代码默认值  server/internal/config/defaults.go               │
│          兜底 · 与 DATA-MODEL.md §7.4 的键位表一一对应                  │
└─────────────────────────────────────────────────────────────────────┘

第 2 层的一个并列分支：主题配置（D95）
  ThemeConfigs（独立表，每行一键）—— 键集合由主题 manifest 声明，Go 不硬编码
  合并顺序：ThemeConfigs 的 DB 值 → manifest 的 Default → 类型零值（三级兜底，§9.8）
  为什么不放 SystemSettings：生命周期随主题装卸而生灭 + 一对多 + 可逐键审计（D78）
```

### 6.2 读取 API

```go
// internal/settings —— 唯一的配置读取入口
type Service interface {
    GetString(key string, def string) string
    GetInt(key string, def int) int
    GetBool(key string, def bool) bool
    GetJSON(key string, out any) error
    Set(key string, value any, updatedBy string) error   // 写入后触发 onChanged
    GetWithMeta(key string) SettingMeta                  // {Value, Default, Source, Secret, Category}
}
```

| 设计点 | 说明 |
|---|---|
| **两级合并** | `DB 覆盖 → 代码默认值`；`Source` 字段区分取值来自 `db` 还是 `default` |
| **加密透明** | `ValueType = secret` 的键在 `Set` 时自动加密、`Get` 时自动解密；上层无感 |
| **变更回调** | `onChanged(key, old, new)` 注册回调，用于：写 `picgo.*` → 推送到 agent；<br>写 `upload.rateLimit.*` → 刷新内存限流器；写 `site.*` → 广播配置变更给前端 |
| **进程内缓存** | 全量加载为 `map[string]SettingMeta` 常驻内存，`Set` 时同步更新；DB 只读一次 |
| **热更边界** | 只有第 2 层能热更。第 1 层改动必须重启容器 |

### 6.3 主密钥不进数据库（D19）

| 顺序 | 来源 | 场景 |
|---|---|---|
| 1 | `PICGO_WEB_SECRET_KEY` 环境变量 | **容器 / 生产推荐**（可接入 docker secret、K8s Secret） |
| 2 | `<dataDir>/secret.key`（首次启动自动生成，权限 0600） | 裸机 / 快速试用 |

**理由**：主密钥用于解密库中的密文（`StorageSecrets.EncryptedPayload`、
`SystemSettings` 中 `Encrypted = true` 的行）。若与密文同库存储，等于没加密 ——
拿到数据库文件的人可以同时拿到密文与密钥。

`StorageSecrets.KeyVersion` 预留主密钥轮换：轮换时新数据用新版本加密，
旧数据按 `KeyVersion` 选择对应密钥解密，支持渐进式重加密。

### 6.4 配置键分类

**键名保持 dot.lowerCamel 不变**（D81.3 例外 3）。
键名与默认值以 [`DATA-MODEL.md` §7.4](./DATA-MODEL.md) 为唯一真源，此处只列分类：

| 分类 `Category` | 覆盖范围 | 典型键前缀 | 敏感值 |
|---|---|---|---|
| `site` | 站点名称/描述/公告/ICP/对外访问地址 | `site.*` | — |
| `user` | 新建用户默认配额/默认状态/自助注册预留开关 | `user.*` | — |
| `upload` | 大小上限、扩展名白名单、并发度、重试、超时、队列上限、限流、暂存保留 | `upload.*` | — |
| `mail` | SMTP 主机/端口/加密方式/账号/发件人 | `mail.*` | `mail.password` |
| `oauth` | GitHub 开关、clientId、自动绑定策略 | `oauth.*` | `oauth.github.clientSecret` |
| `security` | 会话时长、access token 时长、登录失败阈值与时间窗 | `security.*` | — |
| `log` | 操作日志保留天数、任务日志保留天数 | `log.*` | — |
| `picgo` | npm 源与代理、上传代理、transformer、config.json 路径 | `picgo.*` | — |
| `integration` | Lsky 兼容层开关、token 有效期、删除是否同步远端 | `integration.*` | — |
| `theme` | 当前启用的主题 ID（**仅此一键**） | `theme.active` | — |

> **新增配置项不需要数据库迁移**（D77）：往 `SystemSettings` 插一行即可。
> 默认值写在 `internal/config/defaults.go` 的键位表里。

#### 主题自身的配置不在 `SystemSettings`（D95）

主题的配置值（如默认主题的 `BackgroundURL` / `ShowHomeFeatures` / `HomepageFeatures`）
存在**独立表 `ThemeConfigs`**，**不是** `SystemSettings`（§9.8）：

| 项 | `SystemSettings` | `ThemeConfigs` |
|---|---|---|
| 存什么 | 站点级设置（`site.*` / `upload.*` / …）+ **`theme.active`** | 每个主题自己的配置值（`ThemeID` + `Key` + `Value`） |
| 键集合 | 由 `internal/config/defaults.go` 固定 | 由**主题 manifest** 声明，**Go 不硬编码** |
| 生命周期 | 随站点 | 随主题的装卸 |
| 合并 | DB → 代码默认值 | DB → manifest `Default` → 类型零值 |
| 可否放新字段无需迁移 | ✅（KV） | ✅（KV，但**键必须先写进 manifest**） |

> **manifest 的元数据不落库**：`ID` / `Name` / `Pages` / `Configuration.Items` 等一律以**文件**为真相源（§9.6）。

---

## 7. 请求生命周期

以一次**带图上传**（`POST /api/web/v1/uploads`，multipart，一个批次一个存储驱动）为例。

```
┌─ Browser ───────────────────────────────────────────────────────────────┐
│ 图库页拖入 3 张图，选中存储「我的坚果云 WebDAV」（uid=st_01H…）              │
│ POST /api/web/v1/uploads  multipart: Files[]=3, StorageUID=st_01H…,       │
│                                   AlbumUID=<可空>                        │
│ Cookie: pcw_at（或 Authorization: Bearer …）                             │
└──────────────────────────────┬──────────────────────────────────────────┘
                               ▼
┌─ middleware 链 ────────────────────────────────────────────────────────┐
│ 1. Recovery       panic → 500 + 栈入日志（不泄露给前端）                  │
│ 2. Logger         method/path/status/耗时/requestId                     │
│ 3. CORS           默认同源；dev 由 Vite proxy 规避                       │
│ 4. RequireAuth    ← ★ 鉴权点 1（见 §7.1）                                 │
│ 5. RequireNotDisabled  Status=active，否则 40104                        │
│    （管理员接口另有 RequireAdmin）                                        │
└──────────────────────────────┬──────────────────────────────────────────┘
                               ▼
┌─ handler/upload.go（内部 API 处理器）───────────────────────────────────┐
│ · 解析 multipart，读取 StorageUID / AlbumUID                             │
│ · 校验：文件数 > 0、单文件 ≤ upload.maxSizeBytes、扩展名在白名单内          │
│ · 拒绝 SVG（若 upload.blockSvg）                                         │
│ · 按内容嗅探 MIME（**不信任 Content-Type**）                              │
│ · 生成 Job.UID，落盘暂存 <dataDir>/uploads/<yyyy>/<mm>/<ulid>.<ext>       │
│ · 计算 sha256（仅记录，不做去重 —— D66）                                  │
└──────────────────────────────┬──────────────────────────────────────────┘
                               ▼
┌─ service/upload.go ────────────────────────────────────────────────────┐
│ · 解析 StorageUID → StorageConfigs（不存在/未启用 → 40401 / 40901）       │
│ · 资源归属校验：AlbumUID 必须属于当前用户（或管理员）                       │
│ · ★ 配额校验：UsedBytes + 本批总大小 > CapacityBytes → 40302              │
│        └─ CapacityBytes = 0 视为不限额；**管理员跳过**（D20 例外 1）       │
│ · ★ 限流校验：upload.rateLimit.enabled（默认 false）                      │
│        └─ 按 Uploads 表近一小时/近一天 count；超限按 action 决定          │
│           reject → 42901；log → 只记日志不拦；**管理员跳过**（D73 例外 2）  │
│ · 队列上限：排队中的 JobItems ≥ upload.queueMaxLength → 42901            │
│ · 事务内创建：Jobs 行（Kind=upload, Status=queued, StorageUID=…）        │
│              + N 个 JobItems（Seq=0..N-1, Status=queued）               │
│              + N 个 Uploads 行（Status=pending, JobUID=…, UserUID=…）    │
│ · 投递到 taskqueue，立即返回 200 {JobUID, Items[]}                       │
└──────────────────────────────┬──────────────────────────────────────────┘
                               ▼
┌─ taskqueue worker（并发 = upload.concurrency，默认 1）────────────────────┐
│ 取一个 item：                                                           │
│   JobItems.Status = running，Attempts++                                 │
│   调 agent  POST /api/upload（**单文件 + 同步**，D39）                    │
│     超时 upload.itemTimeoutSeconds                                       │
│     失败 → 按 upload.retryTimes 与 retryBackoffMs 指数退避重试            │
│   更新 Job.Progress = 已完成 item 数 / 总数 × 100                         │
│   推 SSE upload.progress                                                │
└──────────────────────────────┬──────────────────────────────────────────┘
                               ▼
┌─ picgo-agent  POST /api/upload ────────────────────────────────────────┐
│ 1. 校验 X-Agent-Token（★ 内网凭据，只有 Go 知道）                         │
│ 2. 切驱动：                                                              │
│    · concurrency = 1（默认）→ picgo.setConfig({ picBed.uploader,         │
│        picBed.current, picBed[type] })  **只改内存、不落盘**，零补丁      │
│    · concurrency > 1       → picgo.upload([path], { uploader: {type,    │
│        configName}, contextData: {JobUID, ItemUID} }) 走补丁（§8.6）     │
│ 3. 注册 beforeUploadPlugins 钩子：按 StorageConfigs.FileTemplate /       │
│    PathTemplate 改写 ctx.output[i].fileName（魔法路径，D44/D70）          │
│ 4. await picgo.upload([path])                                           │
│    ⚠️ 失败时**既不 reject 也不返回 Error**，而是返回空数组 + emit failed；   │
│       agent 必须同时监听 failed 事件（§8.2）                              │
│    ⚠️ 唯一例外：uploader 指向不存在的 type/configName 时**会抛错**（补丁行为）│
│ 5. 返回 { URL, FileName, Width, Height, Size, Raw: <完整 IImgInfo> }     │
└──────────────────────────────┬──────────────────────────────────────────┘
                               ▼
┌─ 回写（Go 侧，在 worker 内）─────────────────────────────────────────────┐
│ 事务内：                                                                 │
│  · Uploads：Status=success, URL, ThumbURL, FileName, Width, Height,     │
│             Size, MimeType                                              │
│  · UploadResults：RawOutput = 完整 IImgInfo JSON（**删除远端时必需**，     │
│                    插件回写的 sha 等字段只能靠它留住 —— D47）              │
│  · JobItems：Status=succeeded, FinishedAt                                │
│  · Users.UsedBytes += Size（D20）                                        │
│  · Albums.ImageCount += 1（若归属相册）                                   │
│  · OperationLogs：Type=upload, Status=success                            │
│ 失败分支：Uploads.Status=failed + Error；JobItems.Status=failed；          │
│          OperationLogs Type=upload Status=failed                        │
│ 全部 item 结束后：Jobs.Status = succeeded | failed，                     │
│                  汇总 Total/Succeeded/Failed/Skipped 写入 Result         │
└──────────────────────────────┬──────────────────────────────────────────┘
                               ▼
┌─ events → SSE → Browser ───────────────────────────────────────────────┐
│ event: upload.progress / upload.finished / upload.failed                │
│ 前端更新上传队列与图库列表                                                │
└─────────────────────────────────────────────────────────────────────────┘
```

### 7.1 鉴权点汇总

| # | 位置 | 检查内容 | 失败码 |
|---|---|---|---|
| 1 | `middleware.RequireAuth` | 依次尝试：`Authorization: Bearer <JWT>` → `Authorization: Bearer pcw_<token>`（查 `APITokens.TokenHash`）→ Cookie `pcw_at` | `40102` / `40103` |
| 2 | `middleware.RequireNotDisabled` | 用户 `Status = active` | `40104` |
| 3 | `middleware.RequireAdmin` | `Role = admin`（仅用户管理、系统设置、存储写、插件写） | `40301` |
| 4 | `service` 资源归属 | 目标资源 `UserUID = 当前用户` **或** `Role = admin` | `40301` |
| 5 | `service` 业务例外 | 管理员**跳过**配额校验与上传限流 | — |
| 6 | `agent` 内网凭据 | 所有 agent 请求带 `X-Agent-Token` | `401`（agent 侧） |
| 7 | `handler/lsky` | Lsky 契约用 `Bearer` token（复用 `APITokens`） | Lsky 信封 |

> **原则**：middleware 只回答「你是谁」；「你能碰这个对象吗」一律在 service 层判定。
> 这样新增资源时不会漏掉权限检查（漏掉会体现在 service 单测里，而不是散落的 handler）。

### 7.2 SSE 订阅

```
GET /api/web/v1/events
  ├─ 鉴权：Cookie / Authorization；非浏览器场景允许 ?token=<jwt>
  ├─ 建立连接后立即发一条 event: ping 确认
  ├─ 每 25s 发 ping 保活（穿透反向代理的空闲超时）
  └─ 事件源合并：
       · taskqueue 的进度与结果（job.started / upload.progress / upload.finished / upload.failed）
       · agent 的任务日志（job.log / job.finished）
       · 系统级（system.notice —— 服务重启、agent 状态变化、配置 syncPending）
```

前端断线由 `EventSource` 自动重连；重连后前端主动拉一次当前任务列表做对账
（SSE 只保证「新事件」，不保证「补发历史」）。

### 7.3 首页请求（主题路径）

以浏览器访问 `/` 为例，串起 §9 的分发与兜底：

```
GET /  （未登录也是这个路径 —— 首页永远公开）
  │
  ├─ middleware → 保留路径？否
  ├─ 认证页保留列表？否
  ├─ 读 SystemSettings.theme.active → 例如 "default"
  │
  ├─ 解析该主题（内部/theme，结果带缓存）
  │    ├─ 主题目录存在 + manifest 合法 + index.html 存在 + Pages 合法 → 用它
  │    └─ 任一不满足 → ★ 回退【内嵌默认主题】+ 写 OperationLogs(theme.error)
  │
  ├─ 用 Pages 做最长前缀匹配
  │    ├─ "default" 主题 Pages = ["/"] → 命中 → 返回该主题的 index.html（no-cache）
  │    └─ 未命中 → 继续走 §9.3 的第 4/5 步（内置 SPA）
  │
  └─ 浏览器解析 index.html → 请求 /theme-assets/** → 从该主题 assets/ 读
                                   （路径规范化校验，防 .. 穿越）
  │
  └─ 主题前端自己调 GET /api/web/v1/site/config
        → 拿到 Site（站点信息）+ Theme.Settings（含 BackgroundURL 等合并后的值）
        → 渲染背景图（<img src={Settings.BackgroundURL}>，不做判断，D97）与各内容区块
```

| 观测点 | 说明 |
|---|---|
| **不存在「未登录看不到首页」** | `/` 在认证页保留列表**之外**，恒由 §9.3 第 3/5 步处理 |
| **`/` 走主题还是内置 SPA，取决于 `theme.active` 主题的 `Pages`** | 默认主题含 `"/"` → 走主题；主题若不含（或失效回退内嵌默认主题）→ 内嵌默认主题也含 `"/"`，仍走主题 |
| **改 `theme.active` 后无需重启** | 下一次请求即生效；已打开的页面刷新即可（`index.html` 为 `no-cache`） |

---

## 8. PicGo 集成边界

### 8.1 职责归属

| 能力 | 归属 | 说明 |
|---|---|---|
| **存储驱动配置** | **DB（真相源）**<br>`StorageConfigs` + `StorageSecrets` | 元数据与凭据分表（D78）；同类型可多条实例（D64）；响应永不返回密钥 |
| `config.json` | **agent**（唯一写入者） | **键级合并**，不可整体重建（D22） |
| 插件管理 | **agent** | `pluginLoader`（读取）/ `pluginHandler`（install·update·uninstall） |
| 插件配置表单 schema | **agent 求值 → 前端渲染** | `evaluatePluginConfig` 在服务端求值；**前端永不执行插件代码** |
| 上传执行 | **agent** | `picgo.upload()`；单文件 + 同步（D39） |
| 图片 URL 生成 | **picgo 驱动**（非本项目） | 我们只存驱动返回的 URL 字符串，**不猜不拼**（D66 的核心理由） |
| 魔法路径 / 魔法文件名 | **本项目决定，agent 执行** | 模板存在 `StorageConfigs`，由 agent 在 `beforeUploadPlugins` 里落地（D42–D44、D70） |
| 远端删除 | **agent**（靠 `remove` 约定） | 插件实现 `remove` 才生效（D47） |
| 驱动能力探测 | **agent 探测 → DB 缓存** | 结果存 `StorageConfigs.Capabilities`（D77：不硬编码驱动名） |
| 图片元数据 / 相册 / 配额 / 限流 | **Go** | agent 只返回 URL 与尺寸 |
| 用户、鉴权、权限、审计 | **Go** | picgo-core 全局单实例，权限完全由 Go 控制 |

### 8.2 picgo `upload()` 的失败语义（实测）

这是集成时最容易写错的一点：

| 情形 | `upload()` 的行为 |
|---|---|
| 上传失败（网络、鉴权、驱动报错） | **既不 reject，也不返回 `Error`** —— 吞掉异常，返回**可能为空的数组**，同时 `emit('failed', error)` |
| `options.uploader` 指向不存在的 `type` 或 `configName` | **抛出**（这是我们补丁新增的前置校验，见 §8.6） |

因此 agent 的判定逻辑必须是：

```
监听 picgo.on('failed') → 记录错误  ┐
监听 picgo.on('finished')            ├─ 两者交叉确认，不能只看返回值
const out = await picgo.upload([...]) ┘
out 为空数组 → 判定为失败，用 failed 事件里的 error 作为原因
```

`UploadOptions.contextData`（补丁提供）用于把全局事件归属到具体 job ——
因为 `createContext` 把事件 bind 到根实例，并发上传时事件是**混在一起**的。

### 8.3 `config.json` 的边界（D22）

> ⚠️ **早期设计错误已纠正**：曾认为 `config.json` 是派生文件、可整体重建。**这是错的。**

插件会往 picgo config 里写自己的私有状态，例如 `picgo-plugin-github-plus`：

```js
ctx.saveConfig({ uploaded: [...] })                 // 插件自己的图片账本
ctx.saveConfig({ [PluginName]: { lastSync } })      // 插件的同步时间戳
```

因此 agent 的写入策略是**键级合并**：

| 键空间 | 处理 |
|---|---|
| `picBed.*` | ✅ 由我们覆盖 |
| `uploader.*` | ✅ 由我们覆盖（多配置映射，D65） |
| `picgoPlugins.*` | ✅ 由我们覆盖（启停状态） |
| `settings.*` | ✅ 只覆盖我们管理的子键（npm 源、代理、transformer） |
| **其他所有键** | ❌ **原样保留**，插件私有键一律不动 |

**启动 reconcile**（幂等，agent 重启后可重复执行）：

```
1. 读 DB：所有 Enabled=true 的 StorageConfigs，按 UpdatedAt 升序
2. 逐条推给 agent（POST /api/uploaders/configs，createOrUpdate）
3. 最后把 IsDefault=true 的那条设为当前上传器（POST /api/uploader/use）
4. 探测并回写 Capabilities
```

### 8.4 魔法路径（D42–D44、D70）

| 维度 | 通用性 | 机制 |
|---|---|---|
| **魔法文件名** | ✅ **完全通用**，所有驱动生效 | `beforeUploadPlugins` 钩子内直接改写 `ctx.output[i].fileName` |
| **魔法路径** | ⚠️ **看驱动** | 多数驱动是 `config.path + fileName` 拼接，把路径塞进 `fileName` 也能生效（可造子目录） |
| **不支持的驱动** | 降级 | 路径转成文件名前缀，如 `2026-02-14_abc.png` |

- 能力由 `Capabilities.SupportsPathTemplate` 表达，**存储配置界面据此提示管理员**
- 模板变量（常用集）与降级规则见 [`PICGO-INTEGRATION.md`](./PICGO-INTEGRATION.md)
- 模板**每个存储配置独立设置**（D43），不使用全局默认

### 8.5 远端删除（D47）

`picgo-core` **没有**删除 API，但存在**事实约定**：

```ts
picgo.emit('remove', files, guiApi)   // 触发方（PicGo GUI：picgoCoreIPC.ts:158）
ctx.on('remove', onRemove)            // 消费方（插件：github-plus/dist/index.js:253）
```

agent 侧实现三件事：

1. 造 **`guiApi` shim**（提供插件会解构的 `showNotification` 等字段）
2. `emit('remove', [imgInfo], shim)`
3. 因 `emit` 无返回值、插件是 async 且无人 await，**从 shim 捕获到的通知文案反推成功/失败**

**前提条件**：`UploadResults.RawOutput` 必须保存完整 `IImgInfo`
（插件在上传时回写的 `sha` 等字段只有这里能留住，丢了就再也删不掉）。

| 驱动情况 | 行为 |
|---|---|
| 插件实现了 `remove` | 真删远端文件；操作日志记 `RemoteDeleted: true` |
| 插件没实现 `remove` | 只删本地记录；UI 标记不支持；日志记 `RemoteDeleted: false` + 原因 |

### 8.6 PicGo-Core 补丁（D48–D51）—— **已落地**

这是本项目的**唯一**一处修改上游代码，且是**纯增量**的。

| 项 | 值 |
|---|---|
| fork 仓库 | `Github-me:YeqingKy/PicGo-Core` |
| 分支 | **`PicGo-Web`** |
| 基线 | 上游 `dev` @ `v3.0.2`（`f710083`） |
| 提交 | **`6419c2f`** |
| 策略 | **自维护，不提交上游**；改动只增不改，便于 rebase |

**改动清单**（3 个源文件 + 1 个单测文件 + 2 个辅助文件）：

| 文件 | 改动 |
|---|---|
| `src/types/index.ts` | 新增 `IUploaderTarget`；`UploadOptions` 加 `uploader?` 与 `contextData?`；`IPicGo` 加可选 `contextData` |
| `src/utils/createContext.ts` | 新增 per-context 配置覆盖表（`WeakMap`）+ `getContextOverrides`；`getConfig` 支持精确/后代/祖先三种命中；写入 `contextData` |
| `src/core/Lifecycle.ts` | 新增 `applyUploaderOverride`（在**进入生命周期之前**解析并应用覆盖，非法目标直接抛出）；`private step` 实例字段改为**局部变量**（修并发串扰的真 bug） |
| `src/__tests__/unit/uploadTargetOverride.spec.ts` | **13 个单测**（覆盖隔离、不污染根 config、大小写不敏感、错误处理、事件归属） |
| `FORK-NOTES.md` | 补丁说明与上游合并指引 |
| `pnpm-workspace.yaml` | pnpm 12 构建审批（允许 `esbuild` 构建脚本） |

**新增能力**：

| 能力 | 作用 |
|---|---|
| `UploadOptions.uploader?: { type, configName? }` | **按次指定图床**，不改全局 config |
| per-context 配置覆盖 | 让插件里的 `getConfig('picBed.xxx')` 按上下文隔离，**插件零适配** |
| `UploadOptions.contextData?` | 让全局 `finished` / `failed` 事件能归属到具体 job |
| `Lifecycle.step` 改局部变量 | 修并发下「要不要补跑 afterUpload」的判定串扰 |

**实测结论**：

```
改前：两个并发批次实际落到的桶 = /personal-bucket , /personal-bucket   ❌ 串了
改后：                          = /work-bucket     , /personal-bucket   ✅ 各走各的

其它已验证：不带 uploader 选项时行为与上游完全一致；setConfig 不污染 config.json；
           非法目标抛错并给出明确消息；250 个单测全通过；pnpm lint 通过。
```

**使用边界**：

| 并发度 | agent 走的路径 | 是否用补丁 |
|---|---|---|
| `upload.concurrency = 1`（**默认**） | `picgo.setConfig()` 切驱动 + 串行 `upload()` | ❌ **完全不碰补丁** |
| `upload.concurrency > 1` | `upload(paths, { uploader, contextData })` | ✅ 用补丁 |

> 因此补丁的稳定性**不阻塞主线开发** —— 默认配置下根本不走这条路。
> 若要提速，只需把 `upload.concurrency` 调大（同一分支，无需改业务代码）。
> 补丁细节与上游合并指引见 [`PICGO-INTEGRATION.md`](./PICGO-INTEGRATION.md)。

**依赖方式**：

| 阶段 | 方式 |
|---|---|
| 开发期 | `picgo-agent` 用 `"picgo": "file:../../PicGo-Core"`；Makefile 保证先 `pnpm build` |
| 构建 / 部署 | `pnpm build && pnpm pack` 出 tarball，Docker 里用 `file:./vendor/picgo-3.0.2-custom.tgz` |

### 8.7 Lsky v1 兼容层（D52）

在 Go 侧实现一层**独立信封**的兼容 API，让生态里的兰空插件
（`picgo-plugin-lankong`、`picgo-plugin-lskypro`、`picgo-plugin-lsky-uploader`、
`picgo-plugin-lskypro-own`）以及 PicList / uPic / ShareX 能直接把本站当图床。

| 特征 | 内部 API | Lsky 兼容层 |
|---|---|---|
| **路径前缀** | **`/api/web/v1/**`** | **`/api/v1/**`**（独占） |
| 响应信封 | `{Code, Message, Data}` | `{status, message, data}` |
| 字段命名 | PascalCase（D81） | snake_case（**外部冻结契约，不改**） |
| 鉴权 | JWT / API Token / Cookie | `Bearer <token>`，复用 `APITokens` |
| 上传入口 | `POST /uploads`（异步，返回 JobUID） | `POST /upload`（**同步返回 URL**，符合 lsky 客户端预期） |
| 处理器位置 | `internal/handler/**` | `internal/handler/lsky/**` |

**为什么这一层必须占 `/api/v1`**：实测生态里全部兰空插件都是**字符串拼接**
（`${serverUrl}/api/v1/upload`、`Url + '/api/v1/upload'`），**没有一个用 `new URL()`**。
让 Lsky 用最「正统」的前缀，用户在第三方客户端只需填**裸域名**，零后缀，兼容性最好。

端点清单见 [`API.md`](./API.md)。兼容层**复用**同一套 service，只在 handler 层做信封与字段映射。

---

## 9. 主题系统（页面级）

> 决策：[`DECISIONS.md`](./DECISIONS.md) **D94**（内置 + 首页主题 + 预留接管范围）、
> **D99**（路由分发与资源前缀）、**D95**（`ThemeConfigs` 独立表）、**D96**（zip 安装）、
> **D98**（manifest 规范）、**D97**（背景图单一 URL）。
> 接口契约：[`API.md`](./API.md) §10 与 §10.1。前端设计：[`DESIGN.md`](./DESIGN.md) §5.x。

### 9.1 定位：主题是**可选的页面覆盖层**，自行注册接管页面

「主题」在本项目里是**一组可替换的前端代码**（独立构建的 HTML + JS + CSS），
它接管的不是「整个前端」，而是**由它自己声明的若干路由**。

| 项 | 内容 |
|---|---|
| **注册机制** | 主题在 `manifest.json` 用 **`Pages`** **自行注册**要接管的页面；**未注册的页面一律走内置 SPA** |
| **可注册页面** | `/`、`/upload`、`/gallery`、`/albums`、`/jobs`、`/logs`、`/settings`（**业务页面**均可注册） |
| **不可注册** | 全部认证页 + `/admin/**` —— 代码硬编码保留（安全底线，manifest 声明无效） |
| **默认主题** | 只注册 `["/"]`（首页），因此默认体验下只有首页走主题 |
| **内置 SPA** | `go:embed web/dist` —— 回到单二进制方案，除主题接管的页面外的资源全部 embed |
| **主题能力** | 作者可用任意技术栈（Vue / React / 纯 HTML），构建产物按 D94.3 的目录结构放入即可 |
| **主题不做什么** | ❌ 不实现登录 / 找回密码 / 后台；❌ 不提供后端接口；❌ 不接触数据库 |

**与「前端整体主题化」的区别**（早期设计曾如此，已收回）：主题是**页面级**可替换，
不是**整站级**。这样风险面与工作量都收窄，同时保留了将来扩展到更多页面的能力。

### 9.2 职责划分

| 路由 | 由谁提供 | 可替换 | 说明 |
|---|---|---|---|
| **`/`（首页落地页）** | **当前主题** | ✅ 可换主题 | 默认主题的 `Pages = ["/"]` |
| `/login`、`/first-login`、`/forgot-password`、`/reset-password`、`/logout` | **内置 SPA**（embed） | ❌ **永久保留** | 认证页保留列表（§9.5） |
| `/upload`、`/gallery`、`/gallery/:UID`、`/albums`、`/jobs`、`/logs`、`/settings` | **内置 SPA**（embed），**主题可注册接管** | ✅ 可注册 | 主题在 `Pages` 声明即接管，**Go 零改动** |
| `/admin/**` | **内置 SPA**（embed） | ❌ **永久保留** | 后台是敏感界面 |
| `/api/web/v1/**`、`/api/v1/**`、`/healthz` | Go 后端 | ❌ | 保留路径 |
| `/theme-assets/**` | **当前主题**的 `assets/` | 随主题 | 主题**唯一**的资源前缀 |
| `/assets/**` | **内置 SPA** 的 `assets/` | ❌ | 主题**不得**占用 |

> 关键点：**主题的接管范围是数据（`Pages`），不是代码分支**。
> 首次只有 `/` 命中，未来某个主题写 `["/", "/gallery"]`，`/gallery` 会自动改由该主题渲染，
> 而 Go 侧**一行都不用改**。

### 9.3 请求分发算法（D99.1）

**一次写通用，后续加页面不改代码**：

```
收到请求 path
  │
  ├─ 1. 保留路径？（/api/**, /healthz, /theme-assets/**, /assets/**, /themes/**, /favicon.ico）
  │      → 交给对应处理器（API / 健康检查 / 主题资源 / SPA 资源 / favicon）
  │      （/themes/** 一律 404，不暴露主题目录与 manifest）
  │
  ├─ 2. 认证页保留列表？（/login, /first-login, /forgot-password, /reset-password, /logout）
  │      → 内置 SPA 的 index.html   ★ 主题**无法**接管（§9.5）
  │
  ├─ 3. 查当前主题的 Pages 接管范围（**最长前缀匹配**）
  │      ├─ 命中 → 当前主题的 index.html
  │      └─ 未命中 ↓
  │
  ├─ 4. /admin/** ？
  │      → 内置 SPA 的 index.html   ★ 主题**无法**接管
  │
  └─ 5. 其他 → 内置 SPA 的 index.html（SPA 回退）
```

| 匹配细节 | 规则 |
|---|---|
| 匹配方式 | **路径前缀匹配**，**最长匹配优先**（`/gallery` 比 `/` 更具体，先命中 `/gallery`） |
| `"/"` 的含义 | **精确匹配 `/` 一个路径** —— 它**不是**「接管全部」 |
| `"/*"` | 通配：接管**所有非保留业务页面**（等于整站主题化；语法允许，默认主题不这么做） |
| 缺省值 | 不写 `Pages` → 等价 `["/"]` |

> 第 3 步是**唯一**与主题相关的判断，且只依赖 `manifest.Pages`。
> 首次实现的默认主题 `Pages = ["/"]`，所以只有 `/` 走主题。

### 9.4 资源前缀划分（D99.2）

主题与内置 SPA 同源，**必须有不重叠的路径空间**，否则会互相覆盖资源：

| 路径 | 归属 | 缓存头 |
|---|---|---|
| **`/theme-assets/**`** | **当前主题**的 `assets/` | `public, max-age=31536000, immutable` |
| **`/assets/**`** | **内置 SPA** 的 `assets/`（embed） | `public, max-age=31536000, immutable` |
| `/favicon.ico` | 优先当前主题的 `assets/favicon.ico`，缺失回退内置 | `max-age=86400` |
| `/themes/**` | **404**（不暴露主题目录、`manifest.json`、主题源码） | — |

**为什么必须分开**：

| 若混用 | 后果 |
|---|---|
| 主题占用 `/assets/**` | 主题的 `index-abc.js` 可能**覆盖内置 SPA 的同名资源**，导致后台 / 登录页加载到第三方代码 |
| 内置 SPA 占用 `/theme-assets/**` | 换主题时两边资源互相污染，缓存无法按内容哈希失效 |

**约束**：

1. 主题构建时**必须**设 `base: '/theme-assets/'`，其 `index.html` 里的资源引用因此形如
   `/theme-assets/index-abc.js`。
2. `/assets/**` **永远**指向内置 SPA。
3. `/theme-assets/**` 做**路径规范化校验**，解析后必须落在当前主题的 `assets/` 目录内，
   否则 `40401`（防 `..` 穿越）。
4. 不提供目录列表；不返回 `manifest.json`（后台通过 `GET /api/web/v1/themes` 读取其内容）。
5. 缓存策略：主题的 `index.html` **`no-cache`**（保证换主题后立即生效），
   `/theme-assets/**` 因文件名带内容哈希而长缓存。

### 9.5 `Pages` 声明的校验，与「认证页永久保留」的安全理由

#### 校验规则（`validateThemeManifest`，D98 §校验清单）

| # | 校验 | 失败后果 |
|---|---|---|
| 1 | 每项必须以 `/` 开头 | 主题不合法（写 `theme.error`） |
| 2 | 不含 `..`、不含 `\` | 同上 |
| 3 | 只允许 `/*` 这一种通配形式 | 同上 |
| 4 | **不得命中保留路径**（`/api/**`、`/healthz`、`/theme-assets/**`、`/assets/**`、`/themes/**`、`/favicon.ico`） | 同上 |
| 5 | **不得命中认证页保留列表**（`/login`、`/first-login`、`/forgot-password`、`/reset-password`、`/logout`） | 同上 |
| 6 | 不得命中 `/admin/**` | 同上 |

> 校验在**装载时**做（安装 zip / 重扫描 / 启动），非法主题**不进列表**且**不被激活**。

#### 为什么认证页与 `/admin/**` 永久保留，且**不可配置**

> 主题 = **第三方前端代码**。若允许它接管 `/login`，它就能渲染一个**一模一样的登录框**，
> 把用户输入的邮箱与密码 `POST` 到自己的服务器。
>
> 关键在于**受害者不是装主题的人**：管理员为了「换个好看的首页」装了第三方主题，
> 而密码被偷的是**普通用户** —— 他们从未选择过这个主题，也无从察觉。
>
> 因此认证路径写死在**代码里的保留列表**，`manifest` 声明无效。
> **这是安全默认值，不是可配置项** —— 不做开关，避免「一键变不安全」变得太容易。

`/admin/**` 同理保留：后台是管理操作界面，被篡改的后果（改配额、删图、装插件）远超首页。

> 附带收益：**早期设计里专门为「认证页防钓鱼」做的 `forceDefaultTheme` 机制不再需要** ——
> 认证页本来就属于内置 SPA，主题在架构上够不到它。

### 9.6 目录结构、解析与生效

#### 主题目录（D94.3）

```
<dataDir>/themes/<ThemeID>/
├── manifest.json       # 元数据 + 配置 schema（Configuration.Items）+ 接管范围 Pages
├── index.html          # 入口（Go 在命中 Pages 时直接返回它）
├── assets/             # 主题自己的 js / css / 图片（带内容哈希）
│   ├── index-<hash>.js
│   └── index-<hash>.css
└── screenshot.png      # 可选，后台主题列表预览图
```

> `manifest.json` / `index.html` / `assets/` **直接放在主题根**（**没有** `dist/` 或 `src/` 中间层）。
>
> **多页面主题（未来）**：仍然只有一个 `index.html` —— 主题自己是 SPA，
> 内部用自己的路由区分 `/`、`/gallery` 等；Go 只负责「这个前缀交给主题的 `index.html`」。
> 这样主题**不必**为每个页面提供独立 HTML，也不用把自己做成 MPA。

#### 解析与生效

| 项 | 规则 |
|---|---|
| 主题根目录 | **`<dataDir>/themes/`**（数据真相源） |
| 当前主题 | `SystemSettings` 的 `theme.active`（默认 `default`） |
| 主题列表 | **扫描文件系统**读 `manifest.json`；**不建 Themes 表**（通常只有 1~3 个主题，扫描成本可忽略） |
| 启动 seed | 启动时若 `<dataDir>/themes/` **为空**，把**内嵌默认主题**解压一份进去；非空则**不动**（升级只重跑容器，**不覆盖用户主题**） |
| 切换主题 | 改 `theme.active` → **立即生效**（无需重启）；已打开的页面刷新即可 |
| 损坏处理 | 主题损坏 / 回退内嵌 / `Pages` 非法 → 写 `OperationLogs`（`theme.error`） |
| 禁止访问 | `/themes/**` 一律 `404`（不暴露 `manifest.json` 与主题源码） |

### 9.7 兜底：内嵌默认主题，永不白屏

这是参考 [Komari](https://github.com/komari-monitor/komari) 的 `defaultTheme/dist.tar.zst` 思路。

| 项 | 规则 |
|---|---|
| 内嵌内容 | 默认主题的**压缩归档**（`server/internal/theme/embedded/`，`go:embed`） |
| 生成方式 | `make theme` 构建默认主题 → 打包 `themes/default/` → 同时产出压缩归档拷入 Go 包目录 |
| **兜底条件** | 以下任一情况**直接服务内嵌默认主题**（其 `Pages = ["/"]`）：<br>① `theme.active` 指向的主题目录不存在；② `manifest.json` 缺失或非法；③ `index.html` 缺失；④ `Pages` 校验失败 |
| 资产兜底 | 主题**有效**时，主题的某个资源缺失 → **404**（便于排查，**不**给它喂内嵌默认主题的同名文件，否则会静默错配代码）；**只有整个主题无效时**才整体回退 |
| 为什么必须内嵌 | 磁盘上的主题目录可能被误删、被半途替换、或用户装了个坏主题。没有内嵌兜底，一次误操作就会让**首页白屏**，而首页正是用户最先看到的东西 |
| 与「不内置」的关系 | 内嵌的只是**兜底副本**。运行时的主题一律来自 `data/themes/`；替换 / 新增 / 升级主题都**不需要重新编译 Go** |

### 9.8 主题配置：独立表 `ThemeConfigs`（D95）

主题的配置值**不存 `SystemSettings`**，而是独立表 `ThemeConfigs`（字段定义见
[`DATA-MODEL.md`](./DATA-MODEL.md) §3.3）。

| 依据（D78 拆表原则） | 说明 |
|---|---|
| **生命周期不同** | 主题配置随主题装卸而生灭；站点设置随站点而生死 |
| **一对多关系** | 一个主题 → 多个配置键，天然适合独立表 |
| **可审计** | 每行带 `UpdatedBy` / `UpdatedAt`，能回答「谁在何时改了哪个键」 |

| 读写规则 | 内容 |
|---|---|
| **键集合** | 由该主题 `manifest.json` 的 `Configuration.Items` 声明，**Go 侧不硬编码** |
| **读取顺序** | **DB 值（`ThemeConfigs`） → manifest 的 `Default` → 类型零值**（三级兜底，与 §6.1 一致） |
| **写入** | 只接受该主题**声明过**的键；类型按 `Type` 校验；未声明的键 → `40001` |
| **`Source`** | API 返回 `db` 或 `default`，后台显示来源徽章 |
| **换主题** | 旧主题的值**保留**（`ThemeID` 不同），切回来仍生效 |
| **卸载主题** | **不自动删值**；另有显式「清理该主题配置」（`WHERE ThemeID = ?`） |
| **并发** | 每键独立行，两个管理员改**不同键**不会互相覆盖 |

> **`SystemSettings` 里只保留 `theme.active`** —— 它是**站点级选择**（当前用哪个主题），
> 不属于任何主题的 schema，因此随站点而生死。
>
> **manifest 里的元数据不落库**：`ID` / `Name` / `Description` / `Author` / `Version` / `URL` /
> `Preview` / **`Pages`（接管范围）** / `MinAppVersion` / `Configuration.Type` /
> `Configuration.Items`（字段定义）全部以**文件为真相源**，**不落库**（D94：「不建 Themes 表」）。
> 只有**用户填写的配置值**进 `ThemeConfigs`；只有**当前选了哪个主题**进 `SystemSettings.theme.active`。

### 9.9 安装：zip 上传 + 文件系统双通道（D96）

| 通道 | 说明 |
|---|---|
| **后台 zip 上传** | `POST /api/web/v1/themes/install`（admin，multipart） |
| **文件系统放置** | 直接把主题目录放进 `<dataDir>/themes/`，后台点「重新扫描」 |
| **重新扫描** | `POST /api/web/v1/themes/rescan`（admin） |

**zip 安装的九条校验**（详见 [`API.md`](./API.md) §10）：仅 `.zip` + 包大小上限 →
**防 Zip Slip（用 `filepath.Rel`）** → **拒绝符号链接 entry** → 体积 / 文件数 / 单文件上限 →
**强制权限位（忽略 zip 里的 mode）** → manifest 校验（含 `Pages`）→ 冲突检测 →
**原子性（先解压到临时目录再 `rename`）** → 写 `OperationLogs`。

**其他规则**：

- **不能卸载当前启用的主题**（先切到别的主题）
- **不能卸载 `default`**（它是内嵌兜底的锚点）
- 目录名与 `manifest.ID` 不一致 → 视为损坏，不进列表，写 `theme.error`
- **主题 = 服务器上的任意前端代码**（影响它接管的页面）→ **仅 admin 可操作**，
  UI 与文档都要明示

**操作日志类型**（D45 清单追加）：
`theme.install` / `theme.uninstall` / `theme.activate` / `theme.rescan` /
`theme.settings.update` / `theme.settings.clear` / `theme.error`

### 9.10 与参考项目的关系

#### lsky-pro 主题（`lsky-pro-themes`）

| 维度 | 说明 |
|---|---|
| **参考了什么** | 主题目录 + `manifest.json` 描述元数据（`id` / `name` / `description` / `author` / `tags`）的约定；主题自带可配置项的思路 |
| **为什么不兼容** | lsky 主题是 **Vue3 + Vite + NaiveUI** 产物，且依赖 lsky 的 **Blade 模板 + Filament 表单 API**（主题类继承 `ThemeAbstract`、用 `Filament\Forms\Components\*` 声明配置、跑在 PHP 进程里）。本项目是 Go + React，**运行模型完全不同**，其主题无法直接使用 |
| **因此** | 我们**自定义** manifest 规范（D98），只在「目录形态 + manifest 描述元数据」这一层与之神似；不使用、不加载任何 lsky 主题代码 |

#### Komari（Go + 主题的同类项目）

| 维度 | 说明 |
|---|---|
| **采纳** | ① **二进制内嵌默认主题作兜底**（本项目的 §9.7）；② zip 安装的校验清单与阈值思路；③ 主题用 schema 声明配置项、后端自动渲染表单；④ manifest 文本支持多语言对象 |
| **不采纳** | ① **主题市场**（多 source + catalog，自用定位不需要）；② `raw` / `redirect` 两种主题类型（本项目**只实现 `managed`**，D98）；③ **HTML 字符串替换注入**（Komari 用 `strings.NewReplacer` 往 `index.html` 注入 title / description / custom head+body —— 这等价于后台注入任意 HTML/JS，与 **D92** 冲突）；④ 整站主题化（Komari 主题接管整个前端；本项目是**页面级**） |
| **比 Komari 更严的两处** | ① 解压判路径：Komari 用 `strings.HasPrefix(path, clean(dir)+separator)`，本项目改用 **`filepath.Rel`**（前缀比较在 base 本身、盘符 / UNC 路径、Windows 大小写等边界易误判）；② 落权限：Komari 用 `f.FileInfo().Mode()`，本项目**强制 `0644` / `0755`** 并**显式拒绝 symlink entry**（防「先建软链再写到链外」） |

---

## 10. 前端架构

### 10.1 路由与页面

**权威定义在 [`DESIGN.md`](./DESIGN.md) §3**，此处仅列骨架。

```
公开（无需登录，D86）
├── /                        首页落地页 ★ **由当前主题提供**（§9）
│                            Hero + 上传区 + 核心能力 + 应用场景 + FAQ + CTA
│                            数据来自 GET /api/web/v1/site/config；背景图是主题配置项 BackgroundURL
│                            （单一 URL，直接 <img src>，**不做任何判断**，D97）
│                            资源前缀为 /theme-assets/**（§9.4）
│                            ⚠️ 该路径由主题渲染时，内置 SPA **不参与渲染**
├── /login                   登录 ★ **内置 SPA**（主题无法接管，§9.5）
│                            （邮箱 + 密码；GitHub 按钮仅当已绑定时可用，D27）
├── /forgot-password         忘记密码（发信）
├── /reset-password          重置密码（邮件链接进入）
└── /first-login             首启强制改密（Users.MustChangePassword，D32）

需登录（AppShell 布局）
├── /upload                  上传（拖拽/选择/粘贴、目标存储单选、进度队列）
├── /gallery                 图库（**不做缩略图**，直接引图床 URL，D84；筛选/批量/灯箱/外链复制）
│                            管理员：顶部 Tab「我的图片 | 全部图片」（D71）
├── /gallery/:UID            图片详情（大图、元数据、重命名/移动/删除）
├── /albums                  相册管理
├── /jobs                    任务面板（Jobs 列表 + JobLogs 实时日志）
├── /logs                    操作日志（类型过滤 + 关键词搜索）
└── /settings                个人设置（资料、改密、GitHub 绑定、API Token、偏好）

需管理员（全部为内置 SPA，**主题永久无法接管**，§9.5）
├── /admin/users             用户管理（创建、配额、状态、重置密码、注销）
├── /admin/storage           存储配置（多实例、激活、连通性测试、魔法路径模板、能力提示）
├── /admin/plugins           插件（已安装/搜索安装/启停/README/任务日志）
├── /admin/themes            ★ 主题管理（列表 / zip 上传 / 重新扫描 / 启用 / 卸载 /
│                            主题设置表单 / 清理主题配置）
├── /admin/site              站点设置（站点信息 / 邮件 / 登录方式 / 安全 / 日志 / 关于）
│                            ⚠️ 首页内容与背景图**不在这里** —— 它们是主题配置项（§9.8），
│                               挂在 /admin/themes 的「主题设置」里
└── /admin/logs              全部日志 + 邮件日志
```

> **`/` 的两条渲染路径**：默认主题 `Pages = ["/"]`，所以 `/` 由主题渲染，内置 SPA 不参与；
> 主题若把 `Pages` 改成不含 `/`（或主题失效回退内嵌默认主题），行为仍由 §9.3 的分发算法决定。
> 无论哪条路径，`/login` 等认证页与 `/admin/**` **永远**由内置 SPA 渲染。

| 守卫 | 行为 |
|---|---|
| `RequireAuth` | 未登录 → 跳 `/login?redirect=`；`MustChangePassword` → 跳 `/first-login` |
| `RequireAdmin` | `Role != admin` → 渲染 403 页（不跳转，避免用户困惑） |
| 导航可插拔 | 侧栏项由 `lib/navigation.ts` 的配置数组驱动（含权限谓词），**新页面只需往数组加一项**（D77.2） |

### 10.2 状态职责划分

| 状态类型 | 归属 | 例子 |
|---|---|---|
| **服务端数据** | 自研 hooks + Axios | 图库列表、用户列表、存储配置、插件列表、日志、任务列表 |
| **客户端状态** | **Zustand** | 当前登录用户快照、主题、侧栏折叠、图库筛选条件（选中存储/相册/关键词/视图模式）、上传队列的本地视图、任务面板展开状态 |
| **实时推送** | SSE → Zustand（仅「写入客户端状态」） | 上传进度、任务日志流、agent 状态、系统通知 |

**铁律**：

- ❌ **不把服务端返回的数据镜像进 Zustand**（避免双份真相与陈旧数据）
- ✅ Zustand 只存「用户的意图与本地 UI 状态」；服务端数据的刷新由 hooks 负责
- ✅ 两者用**标识符**连接（如 `StorageUID`、`AlbumUID`、`JobUID`），而不是把整个对象拷进 store
- ✅ SSE 到达时：进度类直接更新 Zustand 的上传队列；**结果类（完成/失败）触发对应 hook 重新拉取**

Zustand store 划分（均在 `web/src/store/`，命名以 `PLAN.md` 的实施目录树为准）：

```
auth-store     当前用户、角色、MustChangePassword
upload-store   上传队列（本地视图）、目标存储选择、粘贴/拖拽暂存
gallery-store  图库筛选与视图模式、多选集合
task-store     任务面板展开态、正在跟踪的 JobUID 集合
ui-store       主题、侧栏折叠、全局 loading
```

### 10.3 类型与命名（D81）

```ts
// web/src/types/api.ts —— 与 docs/API.md 手工同步
// 类型字段 = PascalCase（与 API JSON 一致）；变量/函数 = camelCase
interface Upload {
  UID: string
  UserUID: string
  StorageUID: string
  FileName: string
  URL: string
  ThumbURL: string
  CreatedAt: number
}

const { Data } = await http.get<ApiResponse<Upload[]>>('/uploads')
const accessToken = Data.AccessToken   // 变量仍 camelCase
```

**为什么类型字段用 PascalCase**：避免在每一处响应处理里手写 `snake_case → camelCase` 映射；
类型与线上 JSON 保持**字面一致**，调试时肉眼可比。

### 10.4 组件组织

```
components/ui/*         Radix 原语 + Tailwind 封装（Button/Input/Dialog/Select/Table/
                        Tabs/DropdownMenu/Switch/Tooltip/Sheet/Badge/Card/Skeleton/…）
                        统一变体管理（cva + tailwind-merge），`cn()` 合并类名
components/layout/*     AppShell（侧栏 + 顶栏 + 内容区）、UserMenu、ThemeToggle、AgentStatusBanner
components/schema-form/ ★ 通用配置表单渲染器（**同时服务插件与主题**）
                          插件：agent 求值后的 IPluginConfig[]
                                input / password / list / checkbox / confirm / editor
                                联动：字段变化时带 answers 回调 agent 重新求值（dependsOn）
                          主题：manifest 的 Configuration.Items（§9.8）
                                string / text / number / switch / select / json
                                静态 schema，本地求值即可，无需回源
features/<域>/*         页面级组件，按业务域切分；跨域复用的抽到 components/
```

**`schema-form` 的关键约束**：前端只渲染，**永不执行插件代码**。
所有函数形态的 `default` / `choices` 都在 agent 侧用 `evaluatePluginConfig` 求值成静态结构后下发。

> 注意：插件 schema 里的 `alias` / `message` 是**插件侧 i18n 已翻译后的字符串**，
> 直接展示，**不要在前端做二次 i18n**（与 §10.5 的前端 i18n 相互独立）。

### 10.5 i18n（D75）

- 默认只提供 `zh-CN`；所有面向用户的文案**走 key**，不硬编码
- 目录结构预留 `en`，后续加语言不改代码
- 与 PicGo 生态（PicGo GUI / PicGo-Core 均全面 i18n）保持一致

---

## 11. 可扩展性设计原则（重点）

> 目标（D77）：**避免后续想加新功能时，因为数据库或程序设定导致不兼容要大改。**

### 11.1 数据库层（D77.1）

| 原则 | 具体做法 | 为什么 |
|---|---|---|
| **只追加，不复用** | 字段/表只增不改。废弃字段保留（标 `// Deprecated`）而不删除或改语义 | 改语义会让新旧数据在同一列里含义不同，无法回退 |
| **迁移只前进** | `schemaMigrations` 只追加条目；已发布条目**永不修改** | 已发布迁移可能已在生产跑过，改动会造成环境不一致 |
| **公开标识与主键解耦** | 对外一律 `UID`（ULID 字符串）；内部自增 `ID` 只用于索引与外键 | 将来归档/分表时自增 ID 会变，UID 不变；也避免被爬虫枚举 |
| **预留元数据位** | 主要业务表带 `Metadata` JSON 列（`gorm:"serializer:json"`） | 新字段可先落此处，**无需迁移** |
| **配置入 KV 表** | `SystemSettings` / `UserSettings` 都是 KV | 加新配置项 = 插一行，**不需要迁移**（D18） |
| **枚举存字符串** | 状态/类型一律 `VARCHAR` 存字符串，不用 DB enum、不用数字魔法值 | 新增取值不涉及 DDL；可读；日志里能直接看懂 |
| **不设过多 NOT NULL** | 除语义必需外允许 NULL | 避免将来加列要回填历史数据 |
| **不设跨表强外键约束** | 只建索引，不建 `FOREIGN KEY` | SQLite 与 PgSQL 行为差异大；外键阻碍分表与归档；一致性由应用层保证 |
| **命名统一 PascalCase** | 表名 PascalCase 复数、列名 PascalCase、缩写全大写（D81） | 跨层命名一致，减少映射代码；见 §5.2 |
| **分表而非塞 JSON** | 不同生命周期 / 敏感级 / 体量的一律拆表（D78） | 见 §11.4 |

### 11.2 应用层（D77.2）

| 原则 | 具体做法 |
|---|---|
| **接口版本化** | 内部路径带 `/api/web/v1`（D80）；响应信封保留扩展位；新增字段一律 `omitempty`（老客户端不受影响） |
| **冻结契约隔离** | Lsky 层独占 `/api/v1`，内部 API 永不占用；启动时校验，重叠即 `panic` |
| **分层不穿透** | `handler → service → repository`；handler 不碰 `*gorm.DB`（见 §3.1） |
| **侧车契约稳定** | agent 的 HTTP 契约单独成文（`API.md` §13）；Go 只消费契约，不依赖 agent 内部实现 |
| **驱动能力可探测** | 驱动是否支持魔法路径 / 远端删除，由**运行时探测 + `Capabilities` 标志**表达；<br>**不硬编码驱动类型名列表** |
| **事件留钩子** | 关键动作（上传后 / 删除后 / 建号后）发内部事件；后续功能订阅即可，不改主流程 |
| **前端路由可插拔** | 导航项由配置数组驱动；新页面只需加一项 |
| **并发度可配置** | 上传并发是配置值而非代码分支：`concurrency=1` 走 setConfig，`>1` 走补丁。**切换不动业务代码** |
| **新增内容域 → 新增表** | 不要把新语义塞进现有表的 JSON 列 |

### 11.3 明确禁止的反模式（D77.3）

| ❌ 反模式 | 后果 |
|---|---|
| 把多种内容塞进一个表的 JSON 列 | 无法索引、无法约束、拆不出来（D78 的反面） |
| 用数字型 enum 落库（如 `Status = 3`） | 新增取值要动 DDL；日志/DB 里看不懂；改语义会导致历史数据歧义 |
| 把自增 `ID` 直接暴露给前端并当作稳定标识 | 将来归档/分表即破坏兼容；可被枚举 |
| 在业务代码里 `switch 驱动类型名` 实现差异 | 新增驱动要改代码，与「驱动靠生态扩展」直接冲突；应查 `Capabilities` |
| 为了当前功能删减未来可能的字段位 | 后面想加就得迁移 + 回填，成本远高于「先留着不用」 |
| 在 `handler` 里直接写 GORM 查询 | 权限/配额/审计规则会散落各处，必然漏 |
| 把 Lsky 的 snake_case 改成 PascalCase | 会直接打断全部第三方客户端（外部冻结契约） |
| 给 `Uploads` 加「可见性 / visibility」列 | 与 D33 语义重复且无法真正生效（后端不分发图片） |

### 11.4 分表原则（D78）

见 §5.5（不重复展开）。

### 11.5 扩展点清单

> 「未来想加 X，往哪里挂」——按此表操作可避免破坏性改动。

| 想加的东西 | 挂载点 | 要迁移吗 | 要改主流程吗 |
|---|---|---|---|
| **新的存储驱动**（如 MinIO、又拍云） | 装对应的 picgo 插件 → 能力探测自动生效 | ❌ | ❌ 不改代码 |
| **新的配置项** | `SystemSettings` 插一行 + `defaults.go` 加默认值 | ❌ | ❌ |
| **新的操作日志类型** | 字符串常量加一个取值 | ❌ | ❌ |
| **新的任务类型**（如「批量迁移」） | `Jobs.Kind` 加字符串值 + `taskqueue` 注册 handler | ❌ | ❌ |
| **新的前端页面** | `lib/navigation.ts` 加一项 + 路由表加一项（并先读 `DESIGN.md` 的 token 与组件清单） | ❌ | ❌ |
| **新的内部 API 接口** | `/api/web/v1/**` 下加路由（**不要碰 `/api/v1/`**） | ❌ | ❌ |
| **把某个已有页面交给主题接管**（如 `/gallery`） | **在主题 manifest 的 `Pages` 加一项**（如 `["/", "/gallery"]`）。分发算法（§9.3）已按最长前缀匹配通用实现 —— **Go 侧零改动**；该主题需自行实现该页面的 SPA 视图 | ❌ | ❌ |
| **给主题加新的配置项** | **写进该主题 manifest 的 `Configuration.Items`** + 在主题前端读取。键集合由 manifest 声明，Go **不硬编码** —— **Go 零改动**（`ThemeConfigs` 是 KV） | ❌ | ❌ |
| **新增 / 替换首页主题** | 把主题目录放进 `<dataDir>/themes/`（或后台 zip 上传）→ 重新扫描 → 启用。**不需要重新编译 Go**（§9.6） | ❌ | ❌ |
| **新增一种主题类型**（如 `redirect` 外部托管） | `Configuration.Type` **字段已预留**；在 `internal/theme` 加一个分支 + 校验。manifest 格式**不用改**（D98） | ❌ | ❌ |
| **开放更多页面给主题 / 放开通配** | ⚠️ `"/*"` 通配语法**已支持**；但**认证页与 `/admin/**` 的保留列表不可放开**（§9.5，安全默认值）。要放开须改代码并重新评估风险 | ❌ | ⚠️ 需评审 |
| **新的内容域**（如「分享链接」「评论」） | **新建独立表** + `schemaMigrations` 追加一条 | ✅ 只追加 | ❌ |
| **某个表加少量字段** | 先试 `Metadata` JSON；确实要索引再加列（可空） | 视情况 | ❌ |
| **图片可见性升级**（如按相册共享） | `Metadata` 或新表；当前靠 `UserUID` 归属过滤 | 视方案 | 需改查询条件 |
| **提高上传并发** | 改 `upload.concurrency`（补丁已落地，见 §8.6） | ❌ | ❌ 只改配置值 |
| **新的 OAuth 平台** | 需满足 D28（平台提供稳定唯一标识）；在 `internal/auth` 加 provider + settings 键 | ❌ | ❌ |
| **多语言** | `web/src/i18n/` 加语言目录 | ❌ | ❌ |
| **多实例部署** | 见 §13 | ✅ | ✅（需外部状态） |
| **备份 / 恢复** | ❌ **不做**（D82）。运维直接备份 `./data` 目录 | — | — |

### 11.6 新增功能前的检查清单

- [ ] 新字段能否放 `Metadata`，避免迁移？
- [ ] 是新配置项吗？→ 写 KV 表，**不需要迁移**
- [ ] 是新枚举取值吗？→ 字符串列直接加值，**不需要 DDL**
- [ ] 是新内容域吗？→ **新建独立表**，不要塞进现有表的 JSON
- [ ] 对外暴露了吗？→ 用 `UID` 而非自增 `ID`
- [ ] 命名是否符合 D81？→ 表/列/API 字段 PascalCase，缩写全大写，路径段小写复数
- [ ] 需要跨表引用吗？→ 存 `UID` 字符串 + 建索引，**不建外键约束**
- [ ] 要删除或改语义了吗？→ **不允许**；新字段替代，旧字段标 Deprecated 保留
- [ ] 与驱动有关吗？→ 查 `Capabilities`，**不要 switch 驱动类型名**
- [ ] handler 里是否写了业务判断？→ 移到 service
- [ ] 是否把服务端数据镜像进了 Zustand？→ 不要
- [ ] 新增路由是否落在 `/api/web/v1/**`？→ 不要占用 `/api/v1/**`（Lsky 契约）
- [ ] 是否想让主题接管某个页面？→ 改**主题 manifest 的 `Pages`**，不要在 Go 里加分支（§9.3）
- [ ] 主题的配置项是否写进了 manifest 的 `Configuration.Items`？→ 不要写进 `site.*` 或在 Go 里硬编码（§9.8）
- [ ] 是否试图让主题接管认证页或 `/admin/**`？→ **不允许**，保留列表不可放开（§9.5）

---

## 12. 安全清单

### 12.1 鉴权与会话

| 项 | 做法 |
|---|---|
| 登录标识 | **只认邮箱**（D23）；`Users.Email` 唯一 |
| 密码 | bcrypt，**cost = 12**（D24） |
| 自助注册 | ❌ 关闭（D25）；用户由管理员创建或邮件邀请 |
| OAuth2 | 仅 GitHub（D26）；**必须先绑定才能登录**（D27）；依赖平台稳定唯一标识（D28） |
| Access token | JWT HS256，15 分钟（`security.accessTokenTtlMinutes`） |
| Refresh token | 32 字节随机串，DB 存 SHA-256，7 天（`security.sessionTtlHours`）；**轮换即失效旧 token**（防重放） |
| API Token | `pcw_<random>`，DB 存 SHA-256，明文仅创建时返回一次（D31） |
| 登录限流 | `LoginAttempts` 按 `(Email, ClientIP)` 统计近窗；超 `security.loginMaxAttempts` 锁定 |
| 首启引导 | 无用户时创建 admin，随机密码写日志 + `<dataDir>/initial-admin-password.txt`（0600），**强制首登改密**（D32） |

### 12.2 密钥与加密

| 项 | 做法 |
|---|---|
| 存储驱动凭据 | `StorageSecrets.EncryptedPayload`，AES-256-GCM（D78 分表） |
| 配置中的敏感值 | `SystemSettings.Encrypted = true`（`mail.password`、`oauth.github.clientSecret`） |
| 主密钥位置 | 环境变量 或 `<dataDir>/secret.key`（0600）；**绝不入库**（D19） |
| 密钥轮换 | `StorageSecrets.KeyVersion` 支持渐进式重加密 |
| 响应脱敏 | 所有读取存储配置的接口**永不返回密钥**；列表接口连密文列都不 select |
| 日志脱敏 | 密钥、token、密码在写日志前统一过一遍脱敏函数 |

### 12.3 网络与传输

| 项 | 做法 |
|---|---|
| agent 监听 | **仅 `127.0.0.1`**，不对外暴露 |
| agent 鉴权 | 所有请求带 `X-Agent-Token`（启动时随机生成，Go 注入 env，不落外围日志） |
| Cookie | `httpOnly`、`SameSite=Lax`、生产 + HTTPS 时 `Secure` |
| CORS | 默认**关闭**（前后端同源，Go 托管前端产物）；开发环境由 Vite proxy 规避 |
| 信任代理 | `PICGO_WEB_TRUST_PROXY` 显式开关，仅在其为真时采信 `X-Forwarded-For` |
| SSE 鉴权 | 浏览器走 Cookie；非浏览器允许 `?token=<jwt>`（仅此场景） |

### 12.4 上传安全

| 项 | 做法 |
|---|---|
| 大小限制 | `upload.maxSizeBytes`（默认 20 MiB） |
| 扩展名白名单 | `upload.allowedExts` |
| MIME 校验 | **按内容嗅探**，不信任 `Content-Type` 头 |
| SVG | 可选禁用（`upload.blockSvg`）—— SVG 可携带脚本 |
| 文件名 | 落盘用生成的 ULID，**不使用用户提供的文件名做路径**（防路径穿越） |
| 暂存清理 | 上传后按 `upload.keepLocalCopy` / `keepLocalDays` 清理 |
| 远端抓取 | `POST /uploads/from-url` 需做 SSRF 防护：解析真实 IP，拒绝私网/回环（可用开关关闭） |
| 插件风险 | **插件等于在服务器上执行任意代码**；仅管理员可安装；每次安装写审计日志 |

### 12.5 主题安全（D94–D99）

主题是**运行在服务器上的第三方前端代码**，信任级别与插件同级，但**影响面更窄、更容易验证**。

| 风险 | 缓解措施 |
|---|---|
| **凭据钓鱼**（主题伪造登录框偷密码） | **认证页（`/login` 等 5 个）永久由内置 SPA 提供，主题无法接管**；`/admin/**` 同样保留。保留列表写死在代码里，**manifest 声明无效，且不提供开关**（§9.5） |
| **资源覆盖**（主题的 JS/CSS 顶掉后台资源） | 资源前缀**严格分离**：主题只能占 `/theme-assets/**`，`/assets/**` 永远属内置 SPA（§9.4） |
| **目录穿越**（借主题读取任意文件） | ① `/theme-assets/**` 路径规范化后必须落在该主题 `assets/` 内；② `/themes/**` 一律 `404`；③ 不提供目录列表 |
| **zip 逃逸**（恶意主题包写到主题目录之外） | 逐 entry 用 **`filepath.Rel`** 判定目标位置；**拒绝符号链接 entry**；**强制权限位 `0644`/`0755`**（忽略 zip 里的 mode）；**原子性**（先解压到临时目录再 `rename`）（§9.9） |
| **zip bomb / 磁盘打满** | 压缩包 ≤ `theme.maxPackageBytes`；解压总体积 ≤ `theme.maxExtractBytes`；单文件 ≤ `theme.maxFileBytes`；文件数 ≤ `theme.maxFiles`；`manifest.json` ≤ `theme.maxManifestBytes` |
| **任意 HTML/JS 注入** | ❌ **不做**后台注入自定义 CSS/JS（D92）；❌ **不实现** `raw` 类型主题（等价于注入）；❌ **不做** HTML 字符串替换注入（Komari 的做法，§9.10） |
| **未知主题类型** | `Configuration.Type` 只接受 `managed`；其它取值**装载失败**并写 `theme.error`（D98） |
| **权限** | 安装 / 卸载 / 启用 / 改配置**一律 `RequireAdmin`**；每次操作写 `OperationLogs`（`theme.*`） |
| **兜底失败导致白屏** | 主题缺失 / 损坏 / `Pages` 非法 → **回退内嵌默认主题**（`go:embed`），永不白屏（§9.7） |
| **不可卸载的锚点** | 不能卸载当前启用的主题；不能卸载 `default`（它是内嵌兜底的锚点） |
| **给管理员的明示** | 后台主题管理页有显著提示：**主题等同于在服务器上运行任意前端代码，只安装可信主题** |

> **为什么主题比插件风险低**：插件能读写文件、发网络请求、执行任意 Node 代码；
> 主题只能影响它接管的那几个页面，且**够不到认证与后台**。这也是「页面级主题」相对
> 「整站主题化」的核心安全收益（§9.1）。

### 12.6 授权与审计

| 项 | 做法 |
|---|---|
| 资源归属 | service 层统一判定 `UserUID == 当前用户` 或 `admin` |
| 管理员接口 | `RequireAdmin`：用户管理、系统设置、存储写、插件写 |
| **管理员例外 1** | **跳过存储配额校验**（D20） |
| **管理员例外 2** | **跳过上传限流**（D73） |
| 图片可见性 | **各自私有**（D33）：不靠可见性字段，**靠 `Uploads.UserUID` 归属过滤**；<br>管理员可查全部，但 UI 必须用 Tab 隔离（D71） |
| 审计覆盖 | 上传、删图、改图、账号增删改、存储增删改、插件增删改、**主题增删启停改配置**（`theme.*`）、登录成功/失败/登出、改系统设置、日志清理自身 |
| 审计保留 | 180 天自动清理（`log.retentionDays`，D74）；清理动作本身留痕 |
| 邮件日志 | 每次发信写 `EmailLogs`（收件人/主题/模板/状态/失败原因），**不存正文**（D29） |

> **两个管理员例外的理由**：管理员是运维角色，不应被业务配额与限流卡住。
> 实现时必须成对出现（配额、限流）——这是最容易漏的一致性点。

---

## 13. 演进路线

### 13.1 为什么现在不做「可扩容」

| 事实 | 说明 |
|---|---|
| 定位是自用 / 小团队（D1） | 单机 SQLite 完全够用；引入分布式中间件是纯粹的复杂度浪费 |
| 只有一个 Node 侧车进程 | `picgo-core` 是**单实例**（D8），天然不适合多副本横向扩展 |
| 图片不经过本项目 | 存储与分发的带宽压力在图床侧，本项目的负载是「元数据 CRUD + 队列调度」，量级很小 |

### 13.2 运维职责边界：备份由运维负责（D82）

本项目**不提供**数据库与配置的导出 / 导入功能（无界面、无端点、无定时备份任务）。

- 备份方式：Docker 部署时把 `./data` 目录整体备份 ——
  它包含数据库文件、`picgo/` 配置（含插件私有状态）、上传暂存、`secret.key`
- **`secret.key` 必须与数据库一起备份**：少了它，`StorageSecrets` 与
  `SystemSettings` 中加密过的值都无法解密
- 理由：属于运维职责；跨数据库方言的导出导入会引入大量兼容成本，与「自用为主」的定位不匹配

### 13.3 已经为将来留好的余地

| 现在的选择 | 为将来保留了什么 |
|---|---|
| 对外只用 `UID`（ULID） | 分库分表 / 归档 / 数据迁移时对外契约不变 |
| **不建外键约束**，只建索引 | 可以随时把表拆到不同库；跨库关联不会因外键失败 |
| **无软删除**，硬删除 + 审计日志 | 主表始终是「当前状态」的精确快照，不需要在每个查询里带 `deleted_at` 条件 |
| 配置存 KV 表 | 多实例部署时配置天然一致（都读同一个 DB），无需配置文件分发 |
| 状态/类型存字符串 | 跨版本数据兼容：新版本读到旧值不会解析失败 |
| `Metadata` JSON 扩展位 | 新功能可先落地，不必等一次统一迁移 |
| 迁移表只追加 | 任何版本都能从任意历史版本升上来，不需要「跳跃式升级」特殊处理 |
| 内部事件总线 | 新功能订阅现有事件即可扩展，不必插入主流程 |
| 驱动能力靠运行时探测 | 新增存储驱动永远不改本项目代码 |
| 上传并发是配置值 + 补丁已落地 | 从 1 提到 N 只改一个配置，不动业务代码 |
| 两套 API 前缀隔离（D80） | 将来加新版本的内部 API（`/api/web/v2`）或新兼容层时不会互相挤压 |
| **主题接管范围是数据（`Pages`）而非代码分支**（D94.2 / D99.1） | 将来把更多页面交给主题、甚至整站主题化（`"/*"`）都**不需要改 Go**；保留列表（认证页 / `/admin/**`）是唯一硬编码处 |
| **主题配置是 KV + manifest 声明键集合**（D95 / D98） | 加主题配置项**不需要迁移**；换主题不丢值；卸载不误删 |

### 13.4 真要做多实例时需要补的东西

若将来需要横向扩展，以下是**已知需要新增**的部分（现有设计不会成为阻碍，但需要补组件）：

| 缺口 | 需要补 |
|---|---|
| 任务队列是**进程内**的 | 引入基于数据库的抢占式任务领取（`Jobs` 加租约字段）或外部队列；`Jobs` / `JobItems` 的表结构已经支持（有 `Status`、`Attempts`） |
| SSE 广播是**进程内**的 | 引入 Redis Pub/Sub 之类的跨进程广播；`events` 包的 Hub 接口已经抽象好 |
| 进程内配置缓存 | 多个实例需要变更通知（DB 轮询或 Pub/Sub） |
| 上传暂存是**本地目录** | 多实例需共享存储（或把暂存放到对象存储） |
| **agent 仍是单点** | `picgo-core` 单实例约束（D8）：需要多副本时得做 agent 池 + 按 `StorageUID` 路由；<br>这正是 P1/P2 补丁的目标 —— 补丁已落地，届时可直接复用 |

> 这些**都不需要改动已有的表结构与 API 契约**——这正是 §11 各项原则的验收标准。

---

## 与决策的偏差（已逐条裁决）

以下是撰写本文档时发现的**冲突或需澄清项**。**均已裁决并同步回 [`DECISIONS.md`](./DECISIONS.md)**，
实现时照此执行即可。

| # | 类型 | 内容 | 裁决 |
|---|---|---|---|
| **C1** | ✅ 已解决（本轮） | **Lsky 兼容层与内部 API 的路径前缀重叠**：`GET/DELETE /api/v1/albums` 与内部相册接口撞车（内部用 `{uid}`，Lsky 用数字 `id`），语义也不同 | **已由 [D80](./DECISIONS.md) 彻底解决**：内部 API 整体迁至 **`/api/web/v1/**`**，Lsky 独占 `/api/v1/**`，**前缀不同，冲突消失**。内部相册回到普通路径 **`/api/web/v1/albums`**（不再需要 `gallery/` 中间段）。仅保留**启动时路由冲突检测**作为防护（内部路由与 Lsky 保留集重叠则 `panic`） |
| **C2** | ✅ 已修正 | D8 误引用「串行队列（见 D24）」，但 D24 实为 bcrypt 密码登录 | 已改为 **D35**（并把「串行队列」改为「默认串行的队列」） |
| **C3** | ✅ 已修正 | D33 误引用「见 D45」，但 D45 是操作日志 | 已改为 **D71**（管理员图片页 Tab 隔离） |
| **C4** | ✅ 已修正 | 编号断档：D67 已不存在（内容并入 D66），但 `DATA-MODEL.md` 顶部仍引用 `D64–D67` | 已修正为 **`D64–D66`** |
| **C5** | ✅ 已修正 | 命名不一致：D47 用 `canDeleteRemote`，`DATA-MODEL.md` 用 `capabilities.supportsRemoteDelete` | 统一为 **`Capabilities.SupportsRemoteDelete`** |
| **C6** | ✅ 已修正 | D45 示意结构用 `UserID` / `Target`，与 `OperationLogs` 实际列（`UserUID` / `TargetType` / `TargetUID`）不一致 | 以 **[`DATA-MODEL.md`](./DATA-MODEL.md) 为准**；D45 已改为引用而非重复定义 |
| **C7** | ✅ 已修正 | D47 误写「`uploads` 表存 `raw_output`」，但按 D78 已拆表 | 已改为 **`UploadResults.RawOutput`** |
| **C8** | ✅ 已标注 | D25「不做自助注册」 vs 配置键 `user.allowSelfRegistration`（默认 `false`） | 保留键并标注为**预留开关**（默认关闭），实现时不要误做注册流程 |
| **C9** | ✅ 已标注 | D33「各自私有」，但 `Uploads` 表**没有可见性列** | 刻意如此（靠 `UserUID` 归属过滤）；已在 `DATA-MODEL.md` 加注释**防止后续误加列** |
| **C10** | ✅ 已裁决 | 端口冲突：D6 原设定 Go 服务监听 `:36677`，而 `36677` 正是 **picgo-core 内置 HTTP server 的默认端口** | Go 服务默认端口改为 **`:8080`**；agent 保持 `:36678`；picgo 内置 server 保留 `36677` 且**默认不启用**。已同步 D6 与全部文档 |
| **C11** | ✅ 已迁移（本轮） | **命名规范变更（D81）**：本文档与全部文档此前使用 snake_case（表名 `users`、列名 `created_at`、API 字段 `accessToken`） | 已全面改为 **PascalCase**：表 `Users`、列 `CreatedAt`、API 字段 `CreatedAt` / `JobUID` / `StorageUID`，缩写词全大写。配套要求：GORM `NoLowerCase: true` + 每个模型显式 `TableName()`；PostgreSQL 手写 SQL 必须加双引号。**例外**：Lsky 兼容层（snake_case，外部冻结契约）、环境变量、`settings` 配置键、picgo 侧字段名、驱动配置字段名、操作日志 `Type` 取值。已同步 `DECISIONS.md` D81 与 `DATA-MODEL.md` |
| **C12** | ✅ 已解决（本轮） | **同一页面的路径说明冲突**：旧版本文档在 §7.7 与偏差表 C1 中保留了「内部相册让位至 `/api/v1/gallery/albums`」的表述，与 D80 后的事实不符 | 已按 D80 全面清理：本文档所有路径说明统一为 `/api/web/v1/**`（内部）与 `/api/v1/**`（Lsky），不再出现「让位 / 共享前缀 / 保留集挤占」的措辞 |
| **C13** | ℹ️ 已对齐（本轮） | **目录命名在本文档与 `PLAN.md` 间不一致**（非决策冲突）：旧版本文档写前端状态目录为 `web/src/stores/`，而 `PLAN.md` 写 `web/src/store/`；内部 API 处理器本文档一度写成 `internal/handler/web/` | **以 `PLAN.md` 的实施目录树为准**：前端状态目录为 **`web/src/store/`**（单数），内部 API 处理器为 **`internal/handler/`**，Lsky 兼容处理器为 **`internal/handler/lsky/`**。本文档已全部对齐 —— `DECISIONS.md` 未规定这两处目录名，属于实施细节 |
| **C14** | ✅ 已裁决（主题最终定为「页面级可选覆盖层」） | **前端主题化范围前后两版定义不同**：早期设计一度把「主题」理解为**接管整个前端**（并因此专门设计了「认证页强制内置默认主题」的 `forceDefaultTheme` 机制）；随后收窄为**只做首页** | **以 [`DECISIONS.md`](./DECISIONS.md) D94–D99 为准**：① **前端内置**（`go:embed web/dist`，回到 embed 方案）；② 主题是**页面级可选覆盖层**，**自行注册**要接管的业务页面（`/`、`/upload`、`/gallery`…），**未注册的走内置 SPA**；默认主题只注册 `["/"]`；③ 接管范围由 manifest 的 **`Pages`** 声明，分发算法（§9.3）**一次性写通用**，将来加页面 **Go 零改动**；④ **认证页（5 个）与 `/admin/**` 永久保留、不可配置**（§9.5）；⑤ 资源前缀分离：主题 `/theme-assets/**`、内置 SPA `/assets/**`（§9.4）。**已废弃**：`forceDefaultTheme` 机制（因认证页本就属内置 SPA，主题够不到）、「主题接管整个前端」、主题目录带 `dist/` 或 `src/` 中间层、「`/assets/**` 指向当前主题」 |
| **C15** | ✅ 已裁决（本轮，背景图简化） | **背景图方案被推翻**：早期设计（D85）有「三种模式 + 横竖定向 + 会话缓存 + 后端定时任务缓存直链 + `GET /site/background` 端点」整套机制 | **以 D97 为准**：`BackgroundURL` 是**主题的一个配置项**（默认 `https://api.yppp.net/api.php`），首页直接 `` <img src={BackgroundURL}> ``，**不做任何判断**。上游 `api.php` 会按 UA 自行 302 到横/竖图。**已废弃**：`site.background.mode` / `site.background.acgCache` / `acgCacheMinutes` / `GET /site/background` / ACG 后端缓存与定时任务 / `site.homepage.*`（首页内容改为主题配置项，见 §9.8） |
