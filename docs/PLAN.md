# 开发计划

> **上位约束**：[`DECISIONS.md`](./DECISIONS.md)（最高约束）与 [`DATA-MODEL.md`](./DATA-MODEL.md)（表结构唯一真源）。
> 本文档只负责**分工、里程碑、验收与实施约束**；表结构与接口细节一律引用上述两份文档。
> 若本文档与决策冲突，以决策为准，并在文末「与决策的偏差」中登记。
>
> 本轮已落地的三条全局约定（实现时随处可见，先在此复述）：
> - **API 路径分层（D80）**：内部 API `= /api/web/v1/**`；Lsky 兼容层**独占** `/api/v1/**`。
> - **命名规范（D81）**：API JSON 字段与数据库表名/列名一律 **PascalCase（大驼峰）**，缩写词全大写
>   （`UID` / `URL` / `ID` / `API`）；URL 路径段保持小写复数；TS 变量与函数保持 camelCase。
> - **前端归属（D94/D99）**：**前端整体内置**（`go:embed web/dist`）；
>   **主题**是**可选的页面覆盖层**，在 `manifest.Pages` 里**自行注册**要接管的业务页面（默认主题只注册首页 `/`）；
>   **认证页与 `/admin/**` 永久保留、主题无法接管**；资源前缀**必须分开**
>   （主题 `/theme-assets/**`，内置 SPA `/assets/**`）。详见 **W10**。

## 0. 总览

三个可独立开发的子项目，共用一个仓库、一处契约；外加一个**上游依赖 fork**：

| 子项目 | 技术栈 | 职责 | 决策依据 |
|---|---|---|---|
| `server/` | Go 1.25 + Gin 1.12 + GORM 1.31 | 鉴权、队列、配额、日志、REST/SSE、托管前端、Lsky 兼容 | D9–D12、D16–D17 |
| `picgo-agent/` | Node 24 + TS + hono + `picgo@3.0.2` | 持有 **单个** `PicGo` 实例、上传、插件、驱动能力探测 | D6–D8、D15 |
| `web/` | React 19 + TS + Vite 7 + Tailwind v4 + Radix + RR7 + Zustand + Axios | **内置 SPA**（图库 / 上传 / 任务 / 日志 / 设置 / 后台 / 认证页）+ **默认首页主题**（独立构建单元） | D13–D14、**D94** |
| **`../PicGo-Core`（fork）** | TS + rollup | 上游 `picgo-core` 的**自有分支 `PicGo-Web`**，承载按次指定图床等补丁 | D48–D51 |

**核心取舍（D2/D59）**：后端**不做存储、不做图片分发**。图片落在图床上，URL 由 PicGo 驱动决定，本项目只负责「元数据 + 管理 + 调度」。

**前端取舍（D94/D99）**：**前端整体内置**（`go:embed web/dist`，单二进制）；
**主题是可替换的「页面前端」**，接管 `manifest.Pages` 声明的路径。
**默认主题只注册首页 `/`**（未注册的页面走内置 SPA）；主题可**自行注册更多业务页面**，
后续加页面 **只需改主题 manifest，Go 零改动**。**认证页与 `/admin/**` 永久保留、主题无法接管**（安全默认值）。

**交付物**

```
docker-compose.yml            面向用户的唯一部署入口（D76）
docker-compose.pgsql.yml      可选覆盖：切 PostgreSQL
根目录 `Dockerfile`      多阶段构建（前端 → 默认主题 → PicGo-Core → agent → Go）
Makefile                      **只封装 docker compose**：dev / dev-* / up / up-pgsql / down / logs
                              / check-* / e2e-*（**不再有** build / server / agent / web / pack /
                              vendor / theme 这类宿主直跑目标，D76）
.env.example                  启动引导类变量（业务配置一律进数据库，D18）+ **部署只走 compose** 的说明
README.md                     面向用户只写 compose；开发也用 compose（docker-compose-dev.yml）
docs/                         九份开发文档（D79：README 索引 + 8 份专题，含 DESIGN.md）
picgo-core 依赖               **npm 包 `@yeqingky/picgo-core`**（fork 的 PicGo-Web 分支，
                              版本线独立 1.x）；**不再需要本地源码或 vendor tarball**
docker-compose.yml            生产（SQLite）；带 build 段，首次 up 自动构建镜像
docker-compose-dev.yml        开发（三容器 + 热重载：web/server/agent + e2e profile；
                              **直接用基础镜像跑源码，无 dev Dockerfile**）
Dockerfile                    生产镜像（根目录；四阶段：前端 → 侧车 → Go → alpine 运行时）
```

> ⚠️ **备份不在交付范围**（D82）：项目**不提供**数据库与配置的导出/导入功能（无界面、无端点、无定时备份任务）。
> compose 里给出「备份 `./data` 目录」的提示即可。

---

## 1. 里程碑

| 里程碑 | 目标 | 可验证的验收标准 |
|---|---|---|
| **M0 骨架** | 三个子项目各自可启动，工具链与门禁就绪 | ① `make check` 通过（允许空实现）；② `make dev-up` 后三容器起来（web:5173 / server:8080 / agent:36678）；③ `curl :8080/healthz` 返回 `status:ok` 且 `agent:"up"`；④ `/login` 能渲染登录页；⑤ `docker compose up -d --build` 成功；⑥ `curl /healthz` 通过 |
| **M1 核心** | 端到端跑通 **登录 → 配驱动 → 上传 → 看到图 → 复制外链** | ① 首启生成 `data/initial-admin-password.txt`（0600），用该邮箱 + 密码登录成功并被强制改密（D32）；② 管理员新建一个存储配置（如 GitHub 或 WebDAV），凭据加密入库（`StorageSecrets`），响应中凭据为掩码；③ 点击「测试连通」返回 `Ok:true`；④ 拖拽上传 1 张图，`Jobs` 产生 1 条 `succeeded`，`JobItems` 1 条 `succeeded`；⑤ `Uploads` 出现 1 行且 `URL` 可在浏览器打开；⑥ 刷新图库列表能看到该图；⑦ 复制得到 Markdown / 直链 / HTML 三种格式（D68）；⑧ 管理员未配限流时上传不受限（D73 默认关闭）；⑨ **同一驱动类型可再建第二条配置**（如第二个 WebDAV），两条互不影响，各自可单独设为默认、可单独上传成功（D64）；⑩ 启动时 `StorageConfigs` → agent 的 reconcile 幂等：重启后 `config.json` 中被我们管的键与 DB 一致，且插件私有键（如 `uploaded`）未被破坏（D22）；⑪ **默认 `upload.concurrency=1` 时上传成功（不依赖内核补丁）**；把 `upload.concurrency` 改为 2 后，指定不同存储的两个批次**并发**提交，两次上传分别落到各自图床（验证 W0 补丁生效），再调回 1 仍成功（D35/D48–D51） |
| **M2 完善** | 多用户 + 插件 + 日志 + 删除 | ① 管理员建第二个用户，配额取 `user.defaultCapacityBytes`（D21），可单独调整；② 该用户登录后**只能看到自己的图**；③ 管理员在 `/gallery` 用顶部 Tab 切「我的图片 / 全部图片」（D71）；④ 上传触发限流时管理员**跳过**、普通用户被拒（开启限流后验证，D73）；⑤ 配额用尽时上传返 **`40302`**；⑥ 删除图片 → 记录消失 + `Users.UsedBytes` 退还（D72）+ 写 `image.delete` 日志；**开启远端删除时**（插件实现了 `remove`，如 `picgo-plugin-github-plus`）图床文件被真删，未实现的驱动只删本地并在 UI 标注（D46/D47）；⑦ 插件搜索 / 安装 / 卸载 / 更新 / 启停可用，安装过程 SSE 有 `job.log`；⑧ 操作日志页可按类型过滤 + 关键词搜索（D45）；⑨ 魔法路径与魔法文件名在存储配置里各自生效（D43/D70），不支持的驱动自动降级并给出提示（D44）；⑩ **首页主题可用**：首启 `data/themes/default/` 自动生成、`/` 可打开；`/gallery` 等仍由内置 SPA 渲染；主题资源走 `/theme-assets/**`、`/themes/**` 返 404；**改主题 manifest 的 `Pages` 加 `/gallery` → 重扫后 `/gallery` 改由主题渲染（Go 未改动）**；`Pages` 写 `["/login"]` → 校验失败且 `/login` 仍为内置 SPA；删掉 `data/themes/` 后 `/` 仍能打开（内嵌兜底）（D94/D96/D98/D99，完整 8 条见 W10 验收） |
| **M3 进阶** | Lsky 兼容 + 邮件 + API Token + OAuth + 一键部署 | ① 用 `picgo-plugin-lankong`（或等价客户端）把本服务当图床：`POST /api/v1/tokens` 换 token → `POST /api/v1/upload` 上传成功 → `GET /api/v1/images` 能列出 → `DELETE /api/v1/images/{key}` 能删（D52）；② 返回体为 Lsky 风格 `{status,message,data}`（**该层保持 snake_case，不受 D81 影响**）；③ SMTP 配置后发出邀请邮件，`EmailLogs` 有一行（**不含正文**）且 `OperationLogs` 有 `mail.send`（D29）；④ 找回密码全流程可用；⑤ 创建 API Token 后 `Authorization: Bearer pcw_xxx` 可调 `/api/web/v1/uploads`；⑥ GitHub OAuth **绑定**后可登录（未绑定则拒绝，D27）；⑦ `docker compose up -d` 全新环境 5 分钟内可用；⑧ `docker compose -f docker-compose.yml -f docker-compose.pgsql.yml up -d` 用 PgSQL 同样可用（D76）；⑨ **容器首启后首页主题开箱可用**：`./data/themes/default/` 自动种入、`/` 渲染默认首页主题；即使挂载卷里**没有** `themes/` 也能从二进制内嵌兜底打开（D94） |
| **M4 打磨** | 体验与可维护性 | ① 暗色/亮色主题切换并持久化；② 图库批量选择 + 批量删除/移动；③ 上传队列面板支持逐文件进度与失败重试入口；④ SSE 断线自动重连且重连后状态自愈；⑤ 全部界面文案走 i18n key，`zh-CN` 完整（D75）；⑥ 操作日志保留清理任务按 `log.retentionDays=180` 生效，清理自身留痕（D74）；⑦ 服务端 SIGTERM 能优雅关闭并在下次启动恢复未完成任务（D41）；⑧ 文档与实现一致，`make check` 全绿 |

---

## 2. 工作流拆分

| # | 工作流 | 目录 | 主要产出 | 依赖 | 可并行 |
|---|---|---|---|---|---|
| **W0** | **PicGo-Core fork 与补丁** | `../PicGo-Core`（分支 `PicGo-Web`） | 按次指定图床（`UploadOptions.uploader`）、事件归属（`contextData`）、per-context 配置覆盖、`Lifecycle.step` 并发修复；`FORK-NOTES.md`；单测 | 无 | —（已完成） |
| **W1** | 契约与骨架 | `docs/`、仓库根 | **九份**文档定稿、目录骨架（含 `web/theme-default/`、`themes/`）、`Makefile`（含 `theme`/`check-theme`）、`.env.example`、`docker-compose*.yml`、`.gitignore` | W0（文档需反映补丁现状） | 否（最先） |
| **W2** | Go 基础设施 | `server/internal/{config,logger,database,model,repository,crypto,id,settings,response,middleware}` | 双方言 DB + 只增迁移 + **20 张表模型**（含 `ThemeConfigs`，D95；相册表已于 D101 移除） + 仓库层 + AES + ULID + KV 配置服务 + 统一响应 | W1 | ✅ 与 W4/W7 并行 |
| **W3** | Go 鉴权与用户 | `server/internal/{auth,service,handler}` | 邮箱密码登录、JWT + refresh、API Token、登录限流、GitHub OAuth 绑定、用户 CRUD、首启引导 | W2 | ✅ |
| **W4** | Agent 内核 | `picgo-agent/` | picgo-core 宿主：单文件同步上传、驱动能力探测、插件全生命周期、jobs/SSE、远端删除、优雅退出 | W0、W1 | ✅ 与 W2/W7 并行 |
| **W5** | Go 业务核心 | `server/internal/{agent,events,service,handler}` | 上传队列（job/item）、配额、限流、存储同步与 reconcile、图库、插件代理、SSE Hub | W2、W4 | ⚠️ 依赖 W2 |
| **W6** | Go 日志·邮件·删除 | `server/internal/{service,scheduler,mail,handler}` | 操作日志与查询、邮件发送与邮件日志、保留清理任务、删除流程（含远端） | W2、W3 | ✅ |
| **W7** | 前端基座 | `web/src/{lib,types,components,store,i18n,mocks}`、`web/theme-default/` | **设计 token（`docs/DESIGN.md` §2）**、Axios/SSE 客户端、`types/api.ts`、Radix UI 组件库、路由与守卫、Zustand store、按契约的 mock；**默认首页主题的独立构建入口（`base: '/theme-assets/'`）** | W1 | ✅ 与 W2/W4 并行 |
| **W8** | 前端页面 | `web/src/features/**` | 内置 SPA 的全部页面：登录/忘记密码/首登改密、上传、图库（无缩略图，D84；管理员 Tab 切换 D71）、任务、日志、设置、存储、插件、用户管理、**主题管理（`/admin/themes`）**；**默认首页主题的内容**（Hero + 能力 + 场景 + FAQ + CTA，见 `DESIGN.md` §4.2） | W7 | ⚠️ 依赖 W7 |
| **W9** | Lsky 兼容 + 部署与文档 | `server/internal/{lsky,web}`、`deploy/`、`README.md` | Lsky v1 兼容层（挂 `/api/v1`）、**内置 SPA 的 embed 接线**（`server/internal/web/embed.go`）、Dockerfile/compose、用户与开发文档 | W3、W5、W6、W8 | 否（收尾） |
| **W10** | **主题系统（首页）** | `server/internal/theme/`、`themes/default/` | 主题扫描 / manifest 解析校验（**含 `Pages`**）/ **`Pages` 最长前缀匹配分发** / seed / 静态托管（`/theme-assets`）/ zip 安装（9 条校验 + 原子性）/ **内嵌默认主题兜底**（永不白屏）/ `ThemeConfigs` 读写 / 构建标签 | W2、W7 | ✅ 与 W3/W4/W5/W6/W8 并行 |

---

## 3. 并行批次

```
批次 A ── 契约先行（已完成）
  W0 PicGo-Core fork 与补丁 ═══════ ✅ 已完成（6419c2f）
  W1 契约（docs/ 八份文档）  ═══════ ✅ 已完成
  W1 骨架（Makefile / compose / .env.example / 目录）⬜ 待补 ← 批次 B 的第 0 步
                                                              ║
批次 B ── 三路并行（彼此无依赖）                              ║
  W2 Go 基础设施 ─────────┐                                   ║
  W4 Agent 内核 ──────────┤                                   ║
  W7 前端基座 ────────────┤                                   ║
                          │                                   ║
批次 C ── 依赖批次 B                                           ║
  W2 → W3 Go 鉴权与用户                                        ║
  W2+W4 → W5 Go 业务核心                                       ║
  W2+W3 → W6 Go 日志·邮件·删除                                 ║
  W7 → W8 前端页面（接口按 docs/API.md 走 mock）               ║
  W2+W7 → W10 主题系统（首页）                                 ║
                          │                                   ║
批次 D ── 收尾（依赖全部）                                     ║
  W3+W5+W6+W8+W10 → W9 Lsky 兼容 + 部署与文档 ◄════════════════╝
```

**为什么这样切**

- **W0 先于一切**：`picgo-agent` 直接依赖本地 `PicGo-Core`（`file:../../PicGo-Core`），
  且 `UploadOptions.uploader` / `contextData` 的**类型契约**决定了 agent 的 `POST /api/upload` 形态。
  补丁未落地就写 agent，必然返工。**已完成**。
- **W1 独占批次 A**：三方都要读同一份契约。契约没冻结就并行，必然产生字段对不上的返工（D77.2「契约唯一」）。
- **W2 / W4 / W7 并行**：三者在**不同进程、不同语言、不同目录**，唯一交集是 HTTP 契约，而契约已在批次 A 冻结。
- **W3 / W5 / W6 在批次 C**：都要落表与鉴权中间件，必须等 W2 的 repository 与 middleware 就绪。
- **W8 与 W5 并行**：前端按 `docs/API.md` 造 mock，不阻塞在 Go 侧实现上。
- **W10 在批次 C，与 W3/W5/W6/W8 平行**：它只依赖 W2（配置/迁移/`ThemeConfigs` 表）与 W7
  （默认主题的构建入口与打包约定），**不依赖 W5 的上传链路**，因此可与业务核心同时做。
- **W9 独占批次 D**：Lsky 兼容层要复用已实现的鉴权、上传、图库服务，属于「薄适配」，越晚做越省事；
  同时它是**内置 SPA 的 embed 接线**（`server/internal/web/embed.go`）的落脚点，
  必须等 W8 产出 `web/dist` 且 W10 定好「主题资源前缀 `/theme-assets`」后才收尾。

**解耦手段（四条，缺一不可）**

1. **前端 mock**：`web/src/mocks/` 按 `docs/API.md` 提供假数据与假延迟，`VITE_USE_MOCK=true` 时启用。
   W5 未完成时 W8 照样能把页面做完（含空状态、加载态、错误态）。
2. **Go 侧 AgentClient 接口 + 假实现**：`internal/agent/client.go` 定义接口，`internal/agent/mock.go` 提供假实现，
   `PICGO_WEB_AGENT_MOCK=true` 启用。W4 未就绪时 W5 能独立开发与测试队列逻辑。
3. **契约唯一**：三方都以 `docs/API.md` 为准，**任何字段改动先改文档**，再同批次内同步实现。
4. **主题与内置 SPA 构建解耦**：两者是 `web/` 下的两个独立构建单元（`base` 不同、产物不同），
   W10 只依赖「`themes/default/` 的产物 + `manifest.json` 格式」这个契约，
   而不需要内置 SPA 的代码就绪；反之，W8 改内置 SPA 也不影响主题产物。

---

## 4. 各工作流详细定义

### W0 PicGo-Core fork 与补丁 ✅ 已完成

**仓库与分支**

| 项 | 值 |
|---|---|
| 仓库 | `Github-me:YeqingKy/PicGo-Core`（本地 `PicGo/PicGo-Core`） |
| 分支 | **`PicGo-Web`**（D49） |
| 基线 | 上游 `dev` @ `v3.0.2`（`f710083`） |
| 提交 | **`6419c2f`** —— `feat(upload): per-upload uploader target + per-context config overrides` |
| 策略 | **自维护，不提交上游**；改动**只增不改**，便于 rebase（D48/D51） |

**产出（6 个文件，+687 / −16）**

| 文件 | 变更 | 说明 |
|---|---|---|
| `src/types/index.ts` | +36 | 新增 `IUploaderTarget`、`UploadOptions.uploader`、`UploadOptions.contextData`；`IPicGo` 增加可选 `contextData` |
| `src/utils/createContext.ts` | +114 | per-context 配置覆盖表（`WeakMap`）+ `getContextOverrides()`；`getConfig` 支持精确/后代/祖先三种命中；`applyOverrides` 只克隆覆盖路径沿途分支 |
| `src/core/Lifecycle.ts` | +79 | `applyUploaderOverride()`（在 `try` 之前调用，非法目标以 rejection 抛出）；`private step` → 局部变量（修并发串扰） |
| `src/__tests__/unit/uploadTargetOverride.spec.ts` | +327（新增） | **13 个用例** |
| `FORK-NOTES.md` | +145（新增） | 改动清单、实测证据、上游合并注意 |
| `pnpm-workspace.yaml` | +2（新增） | pnpm 12 构建审批（`allowBuilds: esbuild: true`） |

**四处改动与本项目的关系**

| # | 改动 | 何时被走到 |
|---|---|---|
| P1 | `UploadOptions.uploader?: { type, configName? }` —— 按次指定目标 | 仅 `concurrency > 1` |
| P2 | `createContext` per-ctx 配置覆盖（`getConfig` 先查覆盖表） | 仅 `concurrency > 1` |
| P3 | `Lifecycle.step` 实例字段 → 局部变量 | 始终（无副作用的行为修正） |
| P4 | `UploadOptions.contextData` —— 事件归属 | 需要归属事件时（`concurrency > 1` 必需） |

> **命名注意（易错点）**：P1 的字段名是 **camelCase `{ type, configName }`**，
> 因为它落在 **PicGo-Core（上游代码）内部**，跟随上游与 picgo 自身的命名（`uploaderConfig` 用 `_configName`），
> **不进我们的 API、不受 D81 约束**。
> 而 agent 对外暴露的 `POST /api/upload` 请求体是我们自己的契约，用 **PascalCase `Uploader: { Type, ConfigName }`**，
> **agent 负责两者之间的映射**（见 W4）。

> **关键收益（P2）**：`doUpload` 与所有内置/第三方 uploader 里的
> `ctx.getConfig('picBed.<type>')` **一个字都不用改**就能拿到正确的图床 —— **插件零适配**。
> `concurrency = 1` 时 agent 走 `picgo.setConfig()`（内存、不落盘）老路径，**完全不碰补丁**（D8/D35）。

**实测证据（均已跑通）**

| 验证项 | 结果 |
|---|---|
| 并发隔离（改前） | 两个并发批次**都落到** `/personal-bucket`（串了）❌ |
| 并发隔离（改后） | 分别落到 `/work-bucket` 与 `/personal-bucket` ✅ |
| 不带 `UploadOptions.uploader` 时行为不变 | ✅ 走全局 config 路径 |
| `setConfig` 不污染 `config.json` | ✅ 只改内存 |
| 覆盖表不修改根实例 config | ✅ 单测断言 |
| `configName` 大小写不敏感 | ✅ |
| 非法 `type` / `configName` 抛错且消息明确 | ✅ `Uploader type "nosuchtype" not found` |
| 并发下 `contextData` 事件归属 | ✅ 两批次事件各归各家 |
| 端到端上传 + 事件序列 + 魔法文件名 | ✅ `0→beforeTransform→30→60→beforeUpload→afterUpload→100→finished` |
| 全量回归 | ✅ **250 passed / 5 skipped** |
| `pnpm lint`（dpdm + tsc + eslint） | ✅ 通过，零循环依赖 |
| `pnpm build` | ✅ 产出 `dist/index.cjs.js` + `dist/index.esm.js` |

**验收（已满足）**：`pnpm build` / `pnpm lint` / `pnpm test` 全绿；并发两批次各走各的 bucket。

**对下游的约束**

- `picgo-agent` 必须依赖**本地源码**（`"picgo": "file:../../PicGo-Core"`）；
  `PicGo-Core` 的 `dist/` 被 gitignore 且**没有 `prepare: build`**，因此 **git 依赖不可用**（D48–D51）。
- 构建/部署走 `pnpm build && pnpm pack` 出的 tarball（`file:./vendor/picgo-3.0.2-custom.tgz`）。
- 上游 `dev` 更新后以 `dev` 为基线 rebase，冲突面仅上述 3 个源文件。

---

### W1 契约与骨架

**已完成部分：契约（docs/）**

| 文件 | 说明 |
|---|---|
| `docs/{README,DECISIONS,ARCHITECTURE,DATA-MODEL,API,OPERATIONS,PICGO-INTEGRATION,DESIGN,PLAN}.md` | **九份**：`README` 为索引 + 8 份专题文档（D79；`DESIGN.md` 为前端设计规范，D87） |
| `docs/research/{lsky-pro,skyimage}.md` | 两份调研报告，结论已抽进 `DECISIONS.md`，原文仅供追溯 |

**待补部分：骨架**

> ⚠️ 截至本次更新，仓库根目录**仅有** `docs/`、`README.md`、`LICENSE`。
> 下列产物**尚未创建**，作为批次 B 的**第 0 步**补齐（预计半天内）。

| 文件 / 目录 | 说明 |
|---|---|
| `Makefile` | `dev` / `server` / `agent` / `web` / **`theme`** / `build` / `pack` / **`check-theme`** / `check`（目标名与 README 一致） |
| `.env.example` | **仅启动引导类**变量，见下 |
| `docker-compose.yml`、`docker-compose.pgsql.yml` | 面向用户（D76） |
| `.gitignore` | 忽略 `data/`、`node_modules/`、`dist/`、`web/dist/`、`web/theme-default/dist/`、**`themes/`（主题打包产物）**、`server/bin/`、`vendor/` |
| 目录骨架 | `server/`、`picgo-agent/`、`web/`、**`web/theme-default/`**、`deploy/`、**`themes/`** 建好占位 |

**Makefile 关键：保证 PicGo-Core 的构建顺序**

`agent` / `dev` / `build` / `pack` / `check` 五个目标**内部先执行** PicGo-Core 的准备步骤：

```make
# 内部前置步骤（不新增用户可见目标，保持与 README 的目标集合一致）
#   cd ../PicGo-Core && pnpm install && pnpm build
```

- 理由：`PicGo-Core` 的 `dist/` 被 gitignore，**未构建则 `require` 失败**；
  agent 用 `file:../../PicGo-Core` 依赖它，构建顺序必须由 Makefile 兜住。
- （已废弃）~~`make pack` 产出 picgo-core tarball~~ —— 现在 picgo-core 从 npm 装，不需要 tarball。

**默认主题的维护方式（W10 实测现状）**

默认主题**不是**由前端工具链构建的，而是**手写的一份自包含单文件**：

```
server/internal/theme/embedded/
├── manifest.json     # 元数据 + 配置 schema + Pages（D98）
└── index.html        # 内联 CSS/JS，**不引用 /assets/**（资源前缀隔离，D99.2）
```

用 `//go:embed all:embedded` 打进二进制，运行时：

- **启动 seed**：`<dataDir>/themes/` 为空 → 写出 `default/`；非空则不动
- **运行时兜底**：主题缺失/损坏/`Pages` 非法 → **直接从内嵌副本服务**，永不白屏

> 为什么不用「构建产物 + 压缩归档」：默认主题只需一页落地页，自包含单文件更简单，
> 且避免「主题目录只被 seed 了一半也能渲染」。可替换性不受影响 ——
> 用户放进 `data/themes/` 的主题照常生效（`make e2e-theme` 覆盖了这条）。

**`.env.example` 内容边界（D18）**

只列**启动引导类**（只读、不可热更）：

```dotenv
PICGO_WEB_LISTEN=0.0.0.0:8080
PICGO_WEB_DATA_DIR=./data
PICGO_WEB_DB_DRIVER=sqlite
PICGO_WEB_SQLITE_PATH=./data/picgo-web.db
PICGO_WEB_DB_AUTO_MIGRATE=true
PICGO_WEB_SECRET_KEY=                 # 留空则首次启动生成 data/secret.key (0600)
PICGO_WEB_AGENT_URL=http://127.0.0.1:36678
PICGO_WEB_AGENT_TOKEN=                # 留空则首次启动随机生成
PICGO_WEB_AGENT_AUTOSTART=true
PICGO_WEB_LOG_LEVEL=info
# PostgreSQL 时改为 postgres 并填下面的连接信息（见 DATA-MODEL.md §0.1）
```

> `.env.example` 里**绝不能**出现站点名、上传上限、SMTP、OAuth 等业务配置——它们全在
> `SystemSettings` 表（D18）。
> 环境变量名保持 `UPPER_SNAKE_CASE`，**不受 D81 影响**（D81.3 第 2 条）。

**验收**

- 九份文档互相引用无死链；`make check` 在空实现下不报错；`docker compose config` 校验通过。
- （已废弃）~~`make agent` 在未手动构建 PicGo-Core 的干净环境下也能跑~~ —— 现在从 npm 装 `@yeqingky/picgo-core`，无此依赖。
- 默认主题的打包产物 `themes/default/` 由 `server/internal/theme/embedded/` 提供（二进制内嵌同一份作兜底，D94）。

---

### W2 Go 基础设施

**产出**

```
server/cmd/picgo-web/main.go            入口：装配 config→logger→db→migrate→exit
server/internal/config/
  ├── config.go                         启动引导配置结构体 + Load()
  ├── env.go                            env/.env 读取（含默认值）
  └── defaults.go                       全部配置键的代码默认值表（D18 第 3 层）
server/internal/logger/logger.go        log/slog JSON Handler
server/internal/database/
  ├── open.go                           GORM 打开 + 连接池 + 方言选择 + NamingStrategy（D11/D12/D81.4）
  ├── dsn.go                            DSN 拼装（sqlite / postgres）
  ├── schema_meta.go                    SchemaMeta 读写
  ├── migrations.go                     schemaMigrations 表 + 升序执行（D16/D77）
  └── migrate_v1.go                     v1 init：建 20 张表 + 全部索引
server/internal/model/                  20 张表的 GORM 模型，按域分文件，**每个都实现 TableName()**
                                         （含 ThemeConfigs，见 DATA-MODEL.md §3.3）
  ├── user.go                           Users / UserProfiles / OAuthIdentities /
  │                                     RefreshTokens / APITokens / LoginAttempts
  ├── storage.go                        StorageConfigs / StorageSecrets
  ├── media.go                          Uploads / UploadResults
  ├── job.go                            Jobs / JobItems / JobLogs
  ├── audit.go                          OperationLogs / EmailLogs
  ├── setting.go                        SystemSettings / UserSettings
  ├── plugin.go                         Plugins
  └── schema.go                         SchemaMeta
server/internal/repository/             纯数据访问，返回模型，不写业务逻辑
  ├── user_repo.go  token_repo.go  login_attempt_repo.go
  ├── storage_repo.go  upload_repo.go
  ├── job_repo.go  log_repo.go  email_repo.go
  ├── setting_repo.go  plugin_repo.go
server/internal/crypto/aes.go           AES-256-GCM 加解密 + 主密钥加载/生成（D19）
server/internal/id/ulid.go              ULID 生成 + 前缀（up_ / st_ / job_ / log_）
server/internal/settings/service.go     SettingsService：GetInt/GetString/GetJSON/Set + onChanged
server/internal/response/
  ├── response.go                       OK / Fail / Page 助手（信封 {Code, Message, Data}）
  └── errors.go                         错误码常量（0/40001/40101/…/**40302**/50004）
server/internal/middleware/
  ├── recovery.go   requestlog.go   requestid.go
  └── ratelimit.go                      通用令牌桶（登录用；上传限流在 W5，按张数不走这里）
```

**关键实现要点**

| 点 | 要求 |
|---|---|
| 双方言 | `PICGO_WEB_DB_DRIVER` 选 dialector；SQLite 用 `github.com/glebarez/sqlite`，PgSQL 用 `gorm.io/driver/postgres`；**必须 `CGO_ENABLED=0` 可编译** |
| **命名（D81.4，必做）** | GORM 必须配 **`NamingStrategy{NoLowerCase: true}`**，否则 `User` 会被折成 `users` 表、`UserUID` 折成 `user_uid` 列。**每个模型显式实现 `TableName()`**，不依赖词形变化库 |
| **PgSQL 引号坑（D81.4）** | PostgreSQL 会把**未加引号**的标识符折成小写。GORM 生成的 SQL 总带引号，应用层无影响；但**手写原生 SQL / 迁移脚本必须自己加双引号**，否则报 `relation "uploads" does not exist`。约定：**两方言统一加双引号** |
| **表名与列名** | 严格照抄 `DATA-MODEL.md`（`Users` / `UserUID` / `CapacityBytes` / `UsedBytes` / `PicgoConfigName` / `RawOutput` …），**不得自行改名或臆造** |
| **API 信封** | `{Code, Message, Data}`（**PascalCase**）；分页 `Data` 为 `{Items, Total, Page, PageSize}` |
| **错误码** | 配额不足 = **`40302`**；权限不足 = `40301`（两者不可混用，D20） |
| SQLite 参数 | `journal_mode(WAL)` + `busy_timeout(5000)` + `foreign_keys(1)` |
| 时间 | 全部 `int64` Unix 秒，禁止 `time.Time` 列（DATA-MODEL §0.2） |
| JSON 列 | `TEXT` + `gorm:"serializer:json"`，**不用** `gorm.io/datatypes` |
| 枚举 | 一律 `VARCHAR` 存字符串，不建 DB enum（D77） |
| 外键 | **只建索引，不建 `FOREIGN KEY` 约束**（D77） |
| 迁移 | 只追加，已发布条目永不修改；每条迁移单事务；失败回滚并终止启动（`PICGO_WEB_DB_AUTO_MIGRATE=false` 时仅告警） |
| 主密钥 | `PICGO_WEB_SECRET_KEY` 优先；为空则生成 `data/secret.key`（0600）；**绝不进数据库**（D19） |
| 分层 | `handler → service → repository`；**handler 不得出现 `*gorm.DB`**（D77.2） |
| 默认值 | `defaults.go` 是 D18 第 3 层；新增配置键只改这里 + `SystemSettings`，**不动 schema**（D77） |
| 配置键命名 | 保持 `dot.lowerCamel`（`site.name` / `upload.rateLimit.perHour`），**不受 D81 影响**（D81.3 第 3 条） |

**验收**

- `go vet ./...` 与 `go test ./...` 通过（含：DSN 拼装单测、AES 加解密往返、ULID 唯一性与前缀、迁移幂等性——连跑两次 `Version` 不变）。
- `CGO_ENABLED=0 go build ./cmd/picgo-web` 成功。
- 起服务后两种方言各建一次库：`SchemaMeta.Version = 1`，**20 张表**与索引全部存在（对照 DATA-MODEL §10 索引清单逐项核对，含 `ThemeConfigs(ThemeID, Key)` 唯一索引）。
- **命名回归测试**：查库确认表名是大写驼峰（`SELECT name FROM sqlite_master WHERE type='table'` 里是 `Users` / `StorageConfigs` …，不是 `users` / `storage_configs`）；PgSQL 下同样确认（`information_schema.tables`）。
- **引号回归测试**：在 PgSQL 下执行一条带双引号的原生 SQL 建索引成功；把引号去掉后确认会失败（把这个差异写进迁移注释，避免后人踩坑）。
- `SettingsService` 读一个未落库的键返回代码默认值，`Set` 后返回 DB 值，`onChanged` 被触发。

---

### W3 Go 鉴权与用户

**产出**

```
server/internal/auth/
  ├── password.go        bcrypt cost 12（D24）
  ├── jwt.go             HS256 签发/校验，claim {sub(uid), role, typ:"access"}，15min
  ├── token.go           refresh token 生成 + sha256 哈希；API Token pcw_ 生成 + 哈希（D30/D31）
  ├── oauth_github.go    GitHub OAuth 授权码流 + /user 拉取（D26/D28）
  └── state.go           OAuth state 的 HMAC 签名与校验（携带 redirect，防 CSRF）
server/internal/middleware/
  ├── auth.go            RequireAuth：Bearer JWT → Bearer pcw_ → Cookie pcw_at（顺序固定）
  └── admin.go           RequireAdmin
server/internal/service/
  ├── user_service.go    用户 CRUD、配额调整、状态、注销（D34）
  ├── identity_service.go OAuth 绑定/解绑/列表（D27）
  └── auth_service.go    登录、刷新、登出、改密、首启引导
server/internal/handler/
  ├── auth_handler.go    对应 API.md §1（路径 /api/web/v1/auth/**）
  └── user_handler.go    对应 API.md §2（admin；/api/web/v1/users）
server/internal/bootstrap/admin_seed.go  首启建管理员 + 写 initial-admin-password.txt（D32）
```

**关键实现要点**

| 点 | 要求 |
|---|---|
| **路径前缀** | 全部内部端点在 **`/api/web/v1/**`**（D80）：`/api/web/v1/auth/login`、`/api/web/v1/auth/refresh`、`/api/web/v1/users` … |
| **字段命名** | 请求/响应字段用 PascalCase（`Email` / `Password` / `AccessToken` / `ExpiresIn` / `UserUID`）（D81） |
| 登录标识 | **只认 `Users.Email`**，无用户名（D23/D57） |
| 自助注册 | **默认关闭**（`user.allowSelfRegistration=false`）（D25/D58） |
| OAuth 语义 | **先绑定才能登录**（D27）：回调时若 `(Provider, ProviderUserID)` 未命中 `OAuthIdentities` → 拒绝登录并提示先绑定；已登录用户可 `POST .../bind`（D26 只做 GitHub） |
| 登录限流 | 基于 `LoginAttempts` 表按 `(Email, ClientIP)` 统计 `security.loginWindowMinutes` 窗口内的失败次数，超 `security.loginMaxAttempts` 返 `42901` |
| Cookie | `pcw_at` / `pcw_rt`，`httpOnly` + `SameSite=Lax`，生产 HTTPS 时 `Secure` |
| 刷新轮换 | `POST /auth/refresh` 用 `pcw_rt` 轮换，旧 token 立即置 `RefreshTokens.RevokedAt`（防重放） |
| 首启引导 | 无任何用户时建管理员，随机密码写日志 + `data/initial-admin-password.txt`（0600），`MustChangePassword=true`；改密后该文件删除（D32） |
| 保护规则 | 不可删除自己；不可删除/降级最后一个 admin；不可停用自己 |
| 配额默认 | 新建用户的 `Users.CapacityBytes` 取 `user.defaultCapacityBytes`（若 `user.unlimitedCapacity=true` 则取 0）——**只影响新建用户**（D21） |
| 注销 | 走 W6 的删除流程（逐条退还配额 + 逐条写 `user.delete` 日志），不自行实现 |

**验收**

- 正确密码登录成功并下发 cookie + `AccessToken`；错误密码返 `40101`；连续失败超阈值返 `42901`。
- 首启无用户时创建管理员；用文件中的密码登录后 `MustChangePassword=true`，改密后为 `false`。
- 未登录访问受保护接口返 `40102`；普通用户访问 `/api/web/v1/users` 返 `40301`。
- refresh 轮换后旧 token 再刷新返 `40103`。
- 创建 API Token 后，明文只返回一次；用 `pcw_` 调受保护接口成功，DB 中只有 sha256。
- GitHub 未绑定时 OAuth 回调被拒；绑定后可登录；解绑后不能再登录；无密码且无其他身份时**拒绝解绑**（防锁死）。

---

### W4 Agent 内核（核心）

**产出**

```
picgo-agent/
├── package.json                  scripts: dev / build / start / lint / typecheck / test
├── tsconfig.json                 strict
├── vitest.config.ts
└── src/
    ├── index.ts                  启动：校验 token → 建 PicGo → 起 hono → 注册优雅退出
    ├── env.ts                    PICGO_AGENT_PORT / PICGO_AGENT_TOKEN / PICGO_AGENT_CONFIG_PATH /
    │                             PICGO_AGENT_NPM_REGISTRY / PICGO_AGENT_NPM_PROXY
    ├── logger.ts                 JSON 行日志
    ├── http/
    │   ├── server.ts             hono 应用装配
    │   ├── envelope.ts           {Code, Message, Data} + code 字符串常量
    │   └── auth.ts               X-Agent-Token 中间件（恒定时间比较）
    ├── picgo/
    │   ├── instance.ts           单例 PicGo（D8）：configPath 注入、写前备份、PICGO_ENV=WEBUI
    │   ├── uploaders.ts          uploaderConfig 封装 + evaluatePluginConfig 求值 + schema-only context
    │   ├── capability.ts         驱动能力探测（D44/D77）：SupportsPathTemplate / SupportsRemoteDelete / ConfigFields
    │   ├── rename.ts             beforeUploadPlugins 注册：魔法文件名与魔法路径（D42–D44/D70）
    │   ├── template.ts           模板变量替换引擎（共用一套变量）
    │   ├── upload.ts             单文件同步上传（D39）：按需传 uploader/contextData，或 setConfig 切驱动
    │   ├── remove.ts             远端删除（D47）：guiApi shim + emit('remove') + 文案反推
    │   └── plugins.ts            插件列表（读 node_modules/<name>/package.json）/ 安装 / 卸载 / 更新 / README / 启停
    ├── jobs/
    │   ├── store.ts              内存 job 表 + 逐行日志环形缓冲
    │   ├── runner.ts             npm 类长任务的异步执行与状态流转
    │   └── sse.ts                SSE 广播（25s ping）
    ├── routes/
    │   ├── healthz.ts  config.ts  uploaders.ts  plugins.ts
    │   ├── upload.ts  remove.ts  jobs.ts  events.ts  logs.ts  shutdown.ts
    └── types.ts                  与 docs/API.md §13 一一对应的请求/响应类型
```

**关键实现要点**

| 主题 | 要求 |
|---|---|
| **依赖来源** | `"picgo": "file:../../PicGo-Core"`（开发期）；部署期换 `file:./vendor/picgo-3.0.2-custom.tgz`（D48–D51）。**dist 必须先构建**，Makefile 兜住顺序（见 W1） |
| **单例** | 全进程**一个** `PicGo` 实例（D8）；`configPath` 由 `PICGO_AGENT_CONFIG_PATH` 注入，默认 `<dataDir>/picgo/config.json`，**不污染用户主目录** |
| **备份** | 每次写 `config.json` 前复制为 `config.json.bak.N`，保留最近 5 份（**纯内部轮转，不对用户暴露任何界面**，D82） |
| **config.json 边界** | **键级合并，绝不整体重建**（D22）。只写 `picBed.*` / `uploader.*` / `picgoPlugins.*` / `settings.*`；插件私有键（如 `uploaded`）原样保留 |
| **单文件同步上传** | `POST /api/upload` 阻塞到完成才返回（D39）。单文件让 `uploadProgress` 的四档变成**该文件自己的进度** |
| **调用形态（两种路径）** | `concurrency = 1`：`picgo.setConfig({...})`（**只改内存、不落盘**）后 `picgo.upload([path])`，**不依赖补丁**；`concurrency > 1`：`picgo.upload([path], { uploader: { type, configName }, contextData: { JobUID, ItemSeq } })`，**走 W0 补丁** |
| **命名映射（易错点）** | agent 的 HTTP 请求体用 **PascalCase**（`Uploader: { Type, ConfigName }`，D81）；调用 picgo 时**必须转成 picgo 侧的 camelCase**（`uploader: { type, configName }`）。**不要**把 `Type` / `ConfigName` 直接传给 picgo，否则驱动匹配不到配置 |
| **失败判定（关键）** | picgo `upload()` 传路径**失败时既不 reject 也不返回 `Error`**，而是吞掉异常、返回**可能为空的数组**。因此 agent **必须同时监听 `failed` 事件**，两路都要归一化成 job/item 状态 |
| **非法上传目标的例外** | picgo 侧 `options.uploader.type` / `.configName` 指向不存在的值时，picgo **会抛错**（不是返回空数组）。agent 应捕获并映射为 **HTTP 400**（对应 agent 请求体里的 `Uploader.Type` / `Uploader.ConfigName` 非法，属调用方错误，而不是上传失败） |
| **远端删除** | `POST /api/delete`：造 `guiApi` shim（提供插件会解构的 `showNotification` 等）→ `emit('remove', [imgInfo], shim)` → **从 shim 捕获的文案反推成功/失败**（`emit` 无返回值、插件 async 无人 await）（D47） |
| **能力探测** | 从驱动 `config` schema 里找 `path`/`root`/`basePath` 类字段推断 `SupportsPathTemplate`；探测插件是否 `on('remove')` 得 `SupportsRemoteDelete`。**结果缓存后回传，不在 Go 侧硬编码驱动名**（D44/D77.3） |
| **魔法命名** | `beforeUploadPlugins.register('renameFn', ...)` 改 `ctx.output[i].fileName`（D44）；驱动不支持路径时降级为文件名前缀；**必须在 `upload()` 之前注册** |
| **schema 求值** | `evaluatePluginConfig(config, {}, {onError})` 把函数形态的 `default`/`choices` 求值成静态结构（前端永不执行插件代码）；求值时用 **schema-only context 隐藏该 uploader 已有配置**，否则插件读历史值会把 `default` 覆盖；`dependsOn` 联动由 `POST /api/uploaders/schema` 带 answers 重求值 |
| **插件安装** | `pluginHandler.install(names, {npmRegistry, npmProxy})`；装/卸成功后**重启自身进程**换取状态干净（低频操作，可接受）；返回 `JobUID` 异步跟踪 |
| **JSON 命名（D81）** | **agent 是我们自己的代码，所有 JSON 字段用 PascalCase**（`PicgoVersion` / `ConfigPath` / `JobUID` / `SupportsRemoteDelete` …）。**唯一例外**：从 picgo 读来、原样透传的配置内部键（`picBed` / `picgoPlugins` / `_configName`）、驱动的配置字段名（`repo` / `token` / `path`）、以及 `UploadResults.RawOutput` 里的 `IImgInfo` 字段（`fileName` / `imgUrl` / `sha` …）**必须原样保留、不得转换** |
| **jobs/SSE** | 事件的 `JobUID` 与 Go 侧 `Jobs.UID` 对齐；`GET /api/events` 推 `job.started` / `upload.progress` / `upload.finished` / `upload.failed` / `job.log` / `job.finished` / `system.notice` / `ping`（事件名本身是字符串标识符，**保持小写**，与 `docs/API.md` §8 一致） |
| **日志** | 把 `picgo.log` 尾随成 `job.log` 推给前端，插件安装过程用户可见；`GET /api/logs?tail=` 供 Go 读取 |
| **优雅退出** | `POST /api/shutdown` → 等待在跑任务结束 → 退出；同时监听 SIGTERM/SIGINT |
| **安全** | 只监听 `127.0.0.1`；所有请求校验 `X-Agent-Token`；错误信息不泄露凭据 |

**验收**

- `curl -H "X-Agent-Token: $T" :36678/healthz` 返回 `PicgoVersion: 3.0.2` 与 `ConfigPath`。
- `GET /api/uploaders` 列出 7 个内置 uploader（`smms/tcyun/upyun/aliyun/qiniu/imgur/github`）及各自求值后的 `Config` 字段。
- `POST /api/upload`（单文件 + 指定 `Uploader`）成功返回 `URL` 与完整 `Raw`（**含插件回写字段**）。
- **补丁路径验证**：并发提交两个不同 `Uploader` 目标的单文件上传，两者分别落到各自图床（依赖 W0 的 P1/P2）。
- **失败语义验证**：图床故意失败时，`POST /api/upload` 返回失败（HTTP 500 语义）而**不是**假的成功（证明 `failed` 事件被正确监听）。
- **非法目标验证**：`Uploader.Type = "nosuchtype"` 返回 **400** 且消息明确。
- 连传两个文件到不同驱动，`config.json` 的 `uploaded`（插件私有键）**未被破坏**（D22 回归测试）。
- 安装 `picgo-plugin-github-plus` → 列表出现该插件 → `Raw` 中存在 `sha` 字段 → `POST /api/delete` 成功删除远端。
- 驱动不支持 `remove` 时，`SupportsRemoteDelete=false`。
- `vitest` 覆盖：模板引擎、能力探测、config 键级合并、上传结果归一化、remove 文案反推、非法目标映射 400。

---

### W5 Go 业务核心（核心）

**产出**

```
server/internal/agent/
  ├── client.go          AgentClient 接口（Go 侧只依赖接口，D77.2）
  ├── http_client.go     真实实现：超时、重试、X-Agent-Token
  ├── mock.go            PICGO_WEB_AGENT_MOCK=true 的假实现
  ├── process.go         子进程拉起/关闭（PICGO_WEB_AGENT_AUTOSTART）
  └── health.go          健康探测 + 退避重启（最多 5 次）（D7）
server/internal/events/
  ├── bus.go             内部事件总线（D77.2「事件留钩子」）
  └── sse_hub.go         浏览器侧 SSE 广播（25s ping，带连接计数）
server/internal/service/
  ├── upload_service.go  批次入口：校验 → 建 Job/JobItems → 入队
  ├── upload_file.go     落盘、MIME 嗅探、sha256、扩展名白名单
  ├── fetcher.go         from-url 下载 + SSRF 防护
  ├── job_service.go     job/item 状态机、进度、结果汇总
  ├── worker.go          worker pool（并发 = upload.concurrency，默认 1）
  ├── quota_service.go   配额校验 / 累加 / 退还（D20/D21/D72）
  ├── rate_limit.go      上传限流（按张数，默认禁用，管理员跳过）（D73）
  ├── storage_service.go 存储配置 CRUD + 密钥加解密 + 启动 reconcile + Capabilities 缓存
  ├── gallery_service.go 图库列表/详情/改名/统计（含 Scope=mine|all）
  ├── plugin_service.go  插件代理（转发 agent）+ Plugins 表缓存刷新
  └── picgo_service.go   picgo config/uploader/transformer/logs/resync 代理
server/internal/handler/
  ├── upload_handler.go  storage_handler.go
  ├── plugin_handler.go  job_handler.go    event_handler.go
  └── picgo_handler.go
```

**关键实现要点**

| 主题 | 要求 |
|---|---|
| **路径前缀** | 全部内部端点在 **`/api/web/v1/**`**（D80）：`/api/web/v1/uploads`、`/api/web/v1/storage/configs` … |
| **字段命名** | 请求/响应字段 PascalCase（`StorageUID` / `TotalItems` / `SucceededItems` / `ThumbURL` / `JobUID` …）（D81） |
| **表名引用** | 一律用 `DATA-MODEL.md` 的 PascalCase 名（`Uploads` / `Jobs` / `JobItems` …） |
| **队列结构** | 一次 `POST /api/web/v1/uploads` = 1 个 `Jobs` 行（D36）+ N 个 `JobItems`；driver 信息（`StorageUID`）只在 `Jobs` 层（D38 一批一驱动） |
| **并发控制** | worker 数 = `upload.concurrency`（**默认 1**，D35），作用在 **item 层**；`>1` 需 picgo-core 补丁（W0 已落地），但**只改配置值不改代码** |
| **进度** | `已完成 item 数 / 总 item 数`，单文件内用 agent 回传的四档插值（D39） |
| **状态机** | `queued → running → succeeded \| failed`；**只要有 item 失败 → job=failed**，不引入 `partial`（D37）；成功项 URL 照常写入 `Jobs.Result`，不丢数据 |
| **重试与超时** | `upload.retryTimes` / `retryBackoffMs`（指数退避）/ `itemTimeoutSeconds`；重试写 `JobItems.Attempts` |
| **队列上限** | 待处理 item 超 `upload.queueMaxLength` → 返 `42901` |
| **启动恢复** | 所有 `JobItems.Status=running` 重置为 `queued`，对应 job 回 `running` 并重新入队；源文件已不存在 → 直接标 `failed`，错误「源文件已清理」（D41） |
| **优雅关闭** | SIGTERM：停收新任务（`/uploads` 返 `503`）→ 等在跑 item 完成（最多 `shutdownGraceSeconds`）→ 超时项回 `queued` 留待恢复 → 给前端推「服务正在重启」再关 SSE（D41） |
| **落盘** | 暂存目录 `<dataDir>/uploads/<yyyy>/<mm>/<sha256>.<ext>`（仅暂存，非图片分发）；`upload.keepLocalCopy` 决定是否保留，`keepLocalDays` 决定保留期 |
| **校验顺序** | 扩展名白名单（`upload.allowedExts`，`blockSvg` 默认禁 SVG）→ 大小（`upload.maxSizeBytes`）→ 限流（**默认关**，管理员跳过）→ 配额（**管理员跳过**）→ 入队 |
| **MIME 嗅探** | 按**文件内容**嗅探，不信任 `Content-Type` |
| **SSRF 防护** | `from-url` 解析真实 IP，拒绝私有网段与回环；`PICGO_WEB_ALLOW_PRIVATE_FETCH=true` 可关闭该限制 |
| **配额** | 上传前校验 `Users.UsedBytes + 本批总量 > Users.CapacityBytes` → **`40302`**（`CapacityBytes=0` 不限额）；上传成功后按实际 `Size` 累加；删除时退还（D72） |
| **限流** | 按登录用户统计**张数**（`Uploads` 表时间窗 count），阈值 `upload.rateLimit.perHour` / `perDay`；`action=reject\|log`；**`enabled` 默认 `false`**；管理员跳过（D73）。设置写入后经 `SettingsService.onChanged` 立即刷新内存限流器 |
| **存储 reconcile** | 启动时把 `Enabled` 的 `StorageConfigs` 按 `UpdatedAt` 升序推给 agent，最后激活 `IsDefault` 那条；**幂等**，agent 重启后可重跑（D22） |
| **密钥分离** | 凭据存 `StorageSecrets` 加密，元数据存 `StorageConfigs`；**所有响应绝不返回密钥**（D78） |
| **能力缓存** | 从 agent 探测结果写 `StorageConfigs.Capabilities`；**判断驱动差异只查能力表，不 `switch` 类型名**（D77.3） |
| **Scope** | 图库列表支持 `Scope=mine\|all`：普通用户**强制 mine**；管理员可 `all`（对应前端 Tab 切换，D71） |
| **外链格式** | Markdown / 直链 / HTML 三种，服务端提供 `Format` 参数（D68） |
| **失败降级** | agent 不可用时依赖它的接口返 `503 / 50002`；`/system/info` 的 `AgentStatus=down` |
| **前端 embed** | `server/internal/web/embed.go` 用 `embed.FS` 托管 `web/dist`，SPA 回退到 `index.html`（W9 完成接线） |

**验收**

- 用 `PICGO_WEB_AGENT_MOCK=true` 跑通全链路：建存储 → 上传 3 张 → job `failed`（1 个假失败）且 `Result` 中仍有 2 个成功 URL（D37）。
- 队列上限、重试次数、超时均可用极端值触发并有单测。
- 配额：配额 1 KiB 的用户传 2 KiB 图 → **`40302`**；删除后 `UsedBytes` 回到 0（D72）。
- 限流：默认关闭时不受限；开启 `perHour=2` 后第 3 张被拒（`42901`）；**管理员同样操作不被拒**（D73）。
- 启动恢复：杀掉进程后重启，`running` 的 item 被重新入队并有日志。
- SIGTERM 后在 `shutdownGraceSeconds` 内完成在跑 item，未完成的回 `queued`。
- 存储列表响应中不含任何密钥；`StorageSecrets` 中是密文。
- `Scope=all` 时普通用户仍只拿到自己的图（权限被强制降级）。
- `from-url` 指向 `127.0.0.1` 或 `192.168.x.x` 被拒（SSRF 单测）。
- 表名验证：单测断言 `Upload{}.TableName() == "Uploads"` 等 20 个映射与 DATA-MODEL §1 一致。

---

### W6 Go 日志·邮件·删除

**产出**

```
server/internal/service/
  ├── operation_log_service.go   写入 + 查询（类型过滤 + 关键词搜索）（D45）
  ├── email_service.go           发送 + 写 EmailLogs（不含正文）+ 写 OperationLogs
  ├── email_template.go          邀请 / 找回密码 模板（变量渲染，不落正文）
  ├── delete_service.go          统一删除流程（D46/D47/D72）
  └── retention.go               日志保留清理（D74）
server/internal/mail/smtp.go     SMTP 客户端（ssl / starttls / none）
server/internal/scheduler/scheduler.go  每日清理任务（操作日志 + 任务日志 + 暂存文件）
server/internal/handler/
  ├── log_handler.go             对应 API.md §9（/api/web/v1/logs）
  └── email_log_handler.go       邮件日志列表（/api/web/v1/logs/emails）
```

**关键实现要点**

| 主题 | 要求 |
|---|---|
| **路径与命名** | `/api/web/v1/logs`；字段 PascalCase（`Type` / `Status` / `TargetType` / `TargetUID` / `Detail` / `Error` / `ClientIP` / `UserAgent` / `CreatedAt`）（D80/D81） |
| **日志写入** | 统一入口 `Write(ctx, entry)`；`UID` 为 `log_` 前缀 ULID；`Status ∈ {success, failed}`；**失败时 `Error` 必填**（D45） |
| **Type 取值不改名** | 取值来自 DATA-MODEL §6.1 的类型清单（`upload` / `mail.send` / `user.create` / `user.delete` / `user.update` / `storage.create\|update\|delete` / `plugin.*` / `auth.login\|failed\|logout` / `image.delete` / `image.update` / `setting.update` / `system.log.cleanup`）。**这些是字符串枚举值，原样保留，不适用 D81**（D81.3 第 5 条精神：外部/既有约定不改）；**字符串存储，新增取值无需迁移**（D77） |
| **查询** | `Type` 精确过滤 + 关键词匹配 `Username` / `TargetUID` / `Detail` / `Error`；`Status` 过滤；时间范围 `From`/`To`；分页 |
| **与 `Jobs` 的区分** | 文档与实现都要明确：`Jobs`/`JobLogs` = **任务执行过程**（实时进度、逐行输出）；`OperationLogs` = **审计级结果记录**（一次操作一条）（D45 注） |
| **邮件日志** | 每次发信写 `EmailLogs`（`ToAddress` / `Subject` / `Template` / `Status` / `Error` / `RelatedUserUID`），**不存正文**；同时写一条 `OperationLogs` 的 `mail.send`（D29） |
| **保留清理** | 每日任务按 `log.retentionDays`（默认 180，`0`=永久）删 `OperationLogs`；按 `log.jobRetentionDays` 删 `JobLogs` 与已完成 `Jobs`；按 `upload.keepLocalDays` 清暂存文件；**清理自身写一条 `system.log.cleanup`（记录删除条数）**（D74） |
| **统一删除流程** | `delete_service` 是**唯一**的删除入口，被图片删除、批量删除、账号注销共同复用。步骤：权限判定 → 可选远端删除（走 agent `POST /api/delete`）→ 删 `Uploads` + `UploadResults` → 退还配额（D72）→ 写 `OperationLogs`（含 `RemoteDeleted` 与失败原因） |
| **远端删除失败降级** | **仍然删除本地记录**（硬删除，D46），日志记 `RemoteDeleted=false` + 原因；驱动 `SupportsRemoteDelete=false` 时直接跳过远端删除并标注 |
| **账号注销** | 必须走同一条 `delete_service` 路径逐条删除：逐条退还配额、逐条写日志、最后写 `user.delete`。**不得**用裸 SQL 批量删（会漏退配额与日志） |
| **邮件用途** | 仅「邮件邀请（D25 无自助注册时建号）」与「找回密码」（D29）；模板 key 化（D75） |
| **不做导出导入** | 不实现任何数据导出/导入端点或界面（D82）。清理任务也不涉及备份 |

**验收**

- 上传 / 删除 / 建号 / 改配额 / 建删存储 / 插件操作 / 登录成功与失败，各自产生**恰好一条** `OperationLogs`。
- 按 `Type=upload`、`Status=failed` 过滤，以及对 `Error` 中的关键词搜索，均能命中。
- SMTP 未配置时发信失败 → `EmailLogs` 一行 `failed` + `OperationLogs` 一行 `mail.send` failed；配置后为 success。
- `EmailLogs` 表中**无正文列**（对照 DATA-MODEL 字段核对）。
- 保留清理：把 `log.retentionDays` 设为极小值后，超期记录被删且产生 `system.log.cleanup`；设 `0` 时不删。
- 删除远端失败时，本地记录仍被删除，`Uploads` 无残留，`UploadResults` 同步删除，配额已退还。
- 账号注销后：该用户 `Uploads` 全部消失，`UsedBytes` 归零，日志条数 = 图片数 + 1。
- **无备份端点**：`grep` 全仓确认不存在 `export` / `import` / `backup` 相关路由与 handler（D82）。

---

### W7 前端基座

**产出**

`web/` 下有**两个独立构建单元**（同一仓库、分开配置、分开产物）：

| 构建单元 | 产物 | `base` | 托管方 |
|---|---|---|---|
| **内置 SPA** | `web/dist/` → **`go:embed`** 进 Go 二进制 | **`/`** | Go（embed） |
| **默认首页主题** | `themes/default/`（`manifest.json` + `index.html` + `assets/`） | **`/theme-assets/`** | Go（从 `data/themes/` 读） |

```
web/
├── vite.config.ts / tsconfig.json / eslint.config.js / components.json
│       # 内置 SPA 的构建配置：base = '/'
├── src/                        # 内置 SPA（图库/上传/任务/日志/设置/后台/认证页）
│   ├── main.tsx  App.tsx
│   ├── lib/
│   │   ├── http.ts      Axios 实例（baseURL /api/web/v1）+ 拦截器：Code!==0 抛 ApiError；401 静默刷新
│   │   ├── api.ts       按域封装的请求函数（不散落在组件里）
│   │   ├── sse.ts       EventSource 封装：心跳看护 + 自动重连 + 订阅分发
│   │   ├── i18n.ts      i18n 初始化（zh-CN，预留 en）（D75）
│   │   ├── consts.ts    共享常量（分页大小、轮询间隔、格式化模板等）
│   │   └── utils.ts     cn()、字节/时间格式化、剪贴板
│   ├── types/api.ts     与 docs/API.md 一一对应的类型（**必须与文档同步**）
│   ├── components/
│   │   ├── ui/*         Radix + Tailwind 封装（shadcn 风格）（D14）
│   │   ├── layout/*     AppShell（侧栏 + 顶栏）、ThemeToggle、UserMenu
│   │   ├── form/*       通用表单 + **schema 适配层**（同时服务插件 schema 与主题 schema，见 §7）
│   │   └── theme/*      ThemeCard / ThemeUploadDialog / ThemeSettingsDrawer
│   ├── routes/          createBrowserRouter + RequireAuth / RequireAdmin 守卫
│   ├── store/           Zustand：只放客户端状态（auth / upload 队列 / task / ui）
│   ├── features/        （W8 填充）
│   └── mocks/           VITE_USE_MOCK=true 时按契约返回假数据
└── theme-default/              # ★ 默认首页主题（独立构建单元，与内置 SPA 无关）
    ├── vite.config.ts          # base = '/theme-assets/'
    ├── index.html              # 主题入口（构建后拷到 themes/default/index.html）
    ├── manifest.json           # 主题元数据 + Pages + Configuration.Items（D98）
    └── src/                    # Hero / 核心能力 / 应用场景 / FAQ / CTA（内容来自主题配置）
```

> **为何拆成 `web/theme-default/` 而不是同一入口**：两者的 `base` 必须不同
> （`/` vs `/theme-assets/`），且主题需产出 `manifest.json` 与 `assets/` 目录布局。
> 同一 Vite 工程做两套 `base` 需要两套构建配置与两套 `index.html`，反而更绕；
> 内置 SPA 与默认主题是两个独立构建单元，各自 `base` 不同（`/` vs `/theme-assets/`）。

**关键实现要点**

| 主题 | 要求 |
|---|---|
| **baseURL（D80）** | `baseURL = '/api/web/v1'`。**不要**用 `/api/v1`（那是 Lsky 层） |
| **内置 SPA 的 base** | **`base = '/'`**；产物由 **`go:embed web/dist`** 打进二进制（**W9 接线**）。资源前缀 `/assets/**`（D99.2） |
| **默认首页主题的 base** | **`base = '/theme-assets/'`**（D99.2）——与内置 SPA 的 `/assets/` **必须分开**，否则会相互覆盖 |
| **两个构建单元不混用资源** | 内置 SPA 不得引 `/theme-assets/**`；主题不得引 `/assets/**` |
| **类型命名（D81）** | `types/api.ts` 里的**类型字段用 PascalCase**，与 API JSON 完全一致（`interface Upload { UID: string; JobUID: string; StorageUID: string }`），**不写字段名转换层**；**变量与函数保持 camelCase**（`const accessToken = res.AccessToken`、`fetchUploads()`） |
| 状态职责 | **Zustand 只管客户端状态**（登录态、UI 主题、上传队列、筛选、侧栏折叠）；服务端数据用自研 hooks + Axios，**不把响应镜像进 store**；SSE 推送写入上传队列与任务面板 |
| 类型来源 | `types/api.ts` **以 `docs/API.md` 为准**；文档改了必须同步改这里 |
| **schema 适配层** | 两套命名必须规约到一组内部字段类型后在**同一套渲染组件**上渲染：<br>插件 `IPluginConfig`（`input`/`password`/`list`/`checkbox`/`confirm`/`editor`）<br>主题 `Configuration.Items`（`string`/`text`/`number`/`switch`/`select`/`json`）（D98）<br>**插件 schema 的 `dependsOn` 变化时回调后端重求值**（`POST /api/web/v1/storage/drivers/schema`），前端绝不执行插件代码；**主题 schema 是静态的，本地求值即可**（`DESIGN.md` §7） |
| 组件层 | `components/ui/*` 为 Radix 原语 + Tailwind；业务组件不直接引 Radix 原语 |
| 常量 | 分页大小、超时、轮询间隔等集中 `lib/consts.ts`，不在组件里硬编码 |
| i18n | 所有面向用户的文案走 i18n key，默认 `zh-CN`，目录预留 `en`（D75） |
| mock | 覆盖登录、存储列表、上传、图库、插件、日志、任务、**主题列表与主题设置**等主要端点，便于 W8 独立开发；**mock 字段名同样是 PascalCase** |
| 导航 | 导航项由配置数组驱动（D77.2「前端路由可插拔」），新增页面只加一项；按权限过滤 |

**验收**

- `pnpm lint`、`pnpm typecheck`、`pnpm build` 全绿（**两个构建单元分别跑**：内置 SPA 与 `web/theme-default/`）。
- 内置 SPA（`web/` 构建 → `go:embed`）与默认主题（`server/internal/theme/embedded/`）是两个独立单元，各自 `base` 不同。
- `VITE_USE_MOCK=true` 起前端（`docker compose -f docker-compose-dev.yml up web`）时：登录成功 → 进入 AppShell → 侧栏导航按权限正确显隐；
  `/login`、`/gallery`、`/upload`、`/jobs`、`/logs`、`/settings`、
  `/admin/storage`、`/admin/plugins`、`/admin/users`、`/admin/themes`、`/admin/site` 均可打开（可以是空状态）。
- **`/` 与 `/overview` 的说明**：生产环境中 `/` 由**当前主题**接管（主题不可用时由**内嵌默认主题**接管），
  **不会**落到内置 SPA；因此控制台概览页固定使用 **`/overview`**（D102），SPA 内部只在访问 `/` 时重定向过去。
  仅 `VITE_USE_MOCK=true pnpm dev`（无 Go 分发层）时 `/` 会走 SPA 的重定向，属**开发便利**，不是交付行为。
- **资源前缀隔离可验证**：内置 SPA 产物内**不存在** `/theme-assets/` 引用；
  默认首页主题产物内**不存在** `/assets/` 引用（两者不得混用，D99.2）。
- 401 拦截器行为可验证：使 mock 返回 401 后自动尝试刷新，失败则清登录态并跳转 `/login`。
- `lib/sse.ts` 在 mock 断线后能自动重连（手动关闭 mock 再恢复验证）。
- `types/api.ts` 无 `any`，`tsc --noEmit` 通过。
- **命名一致性检查**：`types/api.ts` 中不存在 snake_case 字段（对照 `docs/API.md` 抽查 3 个接口）。
- **schema 适配层单测**：插件 schema（`input`/`password`/…）与主题 schema（`string`/`switch`/…）
  分别能正确规约到内部字段类型，并在同一套渲染组件上渲染出正确控件。

---

### W8 前端页面

**产出**

```
web/src/features/                 # 全部属于【内置 SPA】
├── overview/   **概览（`/overview`）**：控制台落地页（**不用 `/`**，`/` 是主题首页，D102）
├── auth/       login（邮箱密码）、reset-password、force-change-password
├── upload/     拖拽上传、粘贴上传、目标驱动选择、实时进度、失败重试
├── gallery/    图库（瀑布流/列表，**不做缩略图**，直接引图床 URL）（D84）
│               + 顶部 Tab（我的/全部，仅管理员可见）+ 筛选 + 批量 + 灯箱 + 复制外链
├── storage/    存储配置列表 + 新建/编辑（插件 schema 表单）+ 测试连通 + 设默认 + 魔法路径配置
├── plugins/    已安装列表 + npm 搜索 + 安装/卸载/更新/启停 + README 抽屉 + 安装日志
├── themes/     **主题管理（`/admin/themes`）**：列表 / 上传 zip / 重新扫描 / 启用 / 卸载 /
│               主题设置表单 / 清理主题配置
├── logs/       操作日志（类型过滤 + 关键词搜索）+ 邮件日志
├── tasks/      任务面板（job 列表 + 逐文件明细 + 日志流）
├── settings/   个人设置 + 站点设置（**不含首页内容 / 背景图**）
├── users/      用户管理（admin）：列表、创建、配额、状态、重置密码、注销
└── profile/    个人资料、改密、OAuth 绑定、API Token
```

> ❌ **内置 SPA 没有 `home/`**：首页 `/` 由**主题**提供（D94）。
> ⚠️ 但控制台概览页是内置 SPA 的页面：固定使用 **`/overview`**（`features/overview/`，D102），
> **不得**占用 `/`，否则侧边栏「概览」会跳到主题首页，与首页冲突。
> 默认首页主题的代码在 **`PicGo-Web-Theme` 仓库**（D100），不在本仓库 `features/` 下。

**关键实现要点**

| 页面 | 要求 |
|---|---|
| **默认首页主题**（`web/theme-default/`） | 设计基准见 [`DESIGN.md`](./DESIGN.md) **§4.2**：Hero（背景图 + 站点名 + 上传放置区）+ 核心能力 Bento + 应用场景 + FAQ 手风琴 + CTA + Footer。内容来自**主题配置**（`HomepageFeatures` / `HomepageScenarios` / `HomepageFaq`，D95），站点信息来自 `GET /api/web/v1/site/config`；**背景图直引 `` <img src={BackgroundURL}> ``，不做任何判断**（D97）；CTA「立刻上传」→ 未登录跳 `/login?redirect=/upload`、已登录跳 `/upload` |
| **主题管理（`/admin/themes`）** | 主题卡片列表（预览图、名称/版本/作者、`IsActive`/`IsBuiltin` 徽章、**`Pages` 接管范围展示**、启用/设置/卸载）；顶部「上传主题（zip）」「重新扫描」；上传对话框（体积提示 + `Overwrite` 开关 + 失败时展示具体校验原因）；「主题设置」抽屉（用 schema 渲染器渲染该主题的 `Configuration.Items`，带 `Source` 徽章）；危险区「清理该主题配置」（仅非当前启用主题可用）；显著提示「主题等同于在服务器上运行任意前端代码」与「**认证页与后台由系统内置，主题无法接管**」 |
| 登录 | **只填邮箱 + 密码**（D23）；无注册入口（D25）；`MustChangePassword` 时强制跳改密页；有 GitHub 按钮但仅在 `OAuth.GitHubEnabled` 且账号已绑定时可用（D27） |
| 图库 | **单一路由 `/gallery`**，顶部 Tab「我的图片 / 全部图片」；普通用户不渲染切换器；两个 Tab **共用同一套**列表/筛选/批量组件（只换 `Scope` 与权限判定）（D71） |
| 上传 | 目标驱动为**单选**（一批一驱动，D38）；多驱动需求由前端**自行拆成多个批次**并行提交；进度来自 SSE，逐文件显示；失败项可重试 |
| 存储 | 列表显示 `Name`（前端只用 Name 展示，内部用 `UID`）；表单字段由后端 schema 驱动；**响应中凭据为掩码**，未修改时不提交该字段；显示能力提示（`SupportsPathTemplate` / `SupportsRemoteDelete`）（D44/D64） |
| 魔法路径 | 每个存储配置独立配置 `PathTemplate` 与 `FileTemplate`；提供常用变量插入按钮（D70）与实时预览 |
| 复制外链 | 三种格式可选（Markdown / 直链 / HTML），支持多选批量复制（D68） |
| 插件 | `GuiOnly=true` 的插件置灰并提示「该能力仅桌面端可用」；安装/卸载中显示进度并轮询 `/healthz` 等待 agent 重启回来 |
| 日志 | 类型下拉 + 关键词搜索 + 状态筛选 + 分页；详情展示 `Detail` / `Error` 的格式化 JSON（D45）；**类型下拉包含主题相关的 7 个类型** |
| 任务 | 任务面板与操作日志**视觉上明确区分**（前者是执行过程，后者是审计） |
| 设置 | 上传并发默认 1 并注明「>1 需要内核补丁（已提供）」（D35）；限流**默认关闭**（D73）；配额默认值区分「只影响新用户」（D21）；**站点设置里不出现「首页内容」与「背景图」**（它们在主题设置里，D95） |
| 用户 | 配额以人类可读单位展示（MiB/GiB）并可单独调整；危险操作二次确认（注销、改角色） |
| **无备份界面** | 不提供任何导出/导入/备份恢复的入口（D82） |
| 通用 | 空状态、加载骨架屏、错误提示（toast）齐全；暗色/亮色主题持久化 |

**验收**

- 走查 M1–M3 全部人工流程无阻断。
- 管理员与非管理员分别登录，导航项与图库 Tab 正确显隐；**`/admin/themes` 仅管理员可见**。
- **默认首页主题与 `DESIGN.md` §4.2 一致**：Hero / 核心能力 / 应用场景 / FAQ / CTA / Footer 齐备；
  背景图**直接引用配置项**（不做任何模式判断、不发 `fetch`、不处理 CORS）；
  改动主题设置后刷新页面**立即生效**（首页 `no-cache`）。
- **主题管理页可用**：上传 zip 成功/失败均有结果反馈；「重新扫描」能发现手动放入的主题；
  「启用」后刷新首页即变为新主题；"卸载"在「当前启用中」与「`default`」两种情形下被阻止并给出原因。
- 存储表单：改动 `dependsOn` 关联字段时字段选项实时重求值；凭据字段保持掩码直到用户主动修改。
- 上传面板：一次提交 5 个文件显示逐文件进度与总进度；其中 1 个失败时其余成功项仍可复制链接。
- 批量复制 3 张图为 Markdown，格式正确。
- **内置 SPA 刷新任一子路由不 404**（由 W9 的 embed + SPA 回退保证；主题接管的路径不参与此验收）。

---

### W9 Lsky 兼容 + 部署与文档

**产出**

```
server/internal/lsky/
  ├── routes.go        在 /api/v1 下注册 Lsky 路径（与内部路由做冲突检测）
  ├── envelope.go      {status, message, data} 信封（与内部信封不同）
  ├── auth.go          Lsky Bearer token → 复用 APITokens 校验
  ├── token.go         POST/DELETE /tokens
  ├── profile.go       GET /profile（容量 / 用量）
  ├── strategies.go    GET /strategies（可用存储列表）
  ├── upload.go        POST /upload（multipart，复用 W5 上传队列）
  ├── images.go        GET /images、DELETE /images/{key}
  └── albums.go        GET /albums、DELETE /albums/{id}（伪造响应，D101）
server/internal/web/embed.go          **内置 SPA 的 embed 接线**：`//go:embed dist`（`web/dist`）+ SPA 回退处理器
                                      仅负责「返回内置 SPA 的 index.html / assets」；
                                      **路由分发（含主题接管判断）在 W10 的 `internal/theme/`**（D99.1）
根目录 `Dockerfile`              多阶段：前端 → **默认主题打包** → PicGo-Core → agent → Go → 运行时
docker-compose.yml / docker-compose.pgsql.yml   最终版
README.md                             面向用户只写 compose；开发/测试命令单独一节（D76）
```

**关键实现要点**

| 主题 | 要求 |
|---|---|
| **路径分层（D80）** | Lsky 层**独占** `/api/v1/**`（`/tokens`、`/profile`、`/strategies`、`/upload`、`/images/**`、`/albums/**`）；内部 API 全部在 `/api/web/v1/**`。**两个前缀天然隔离，不存在路径冲突** |
| **启动防护** | 路由注册后做**冲突检测**：若内部路由与 Lsky 保留集重叠 → **直接 panic**，防将来新增接口时静默覆盖。这是**防护**，不是补救 |
| **信封与命名隔离（D52/D81.3）** | Lsky 层返回 `{status, message, data}` 且**字段保持 snake_case**（`strategy_id` / `capacity` / `useCapacity`），**不受 D81 影响**——它是外部冻结契约，第三方客户端按此实现 |
| **鉴权复用** | Lsky 的 Bearer token 直接复用 `APITokens`（`pcw_` 前缀）；`POST /tokens` 用邮箱密码换取 API Token；有效期 `integration.lsky.tokenTtlDays` |
| **上传复用** | `POST /upload` 走 W5 的同一个 `upload_service`（同一套队列、配额、限流），**不得另起一套上传路径** |
| **key 映射** | Lsky 的 `images/{key}` 中的 `key` = `Uploads.UID` |
| **开关与删除语义** | `integration.lsky.enabled`（默认 `true`）控制是否挂载；`integration.lsky.deleteRemoteOnDelete`（默认 `false`）控制 `DELETE /images/{key}` 是否同步删远端（该契约本身无此参数，故用开关表达） |
| **Dockerfile** | 多阶段（根目录 `Dockerfile`，**实测现状**）：① `node:24-alpine` 构建 `web/`（内置 SPA）；② `node:24-alpine` 构建 `picgo-agent`（**从 npm 装 `@yeqingky/picgo-core`**，并断言补丁存在）；③ `golang:1.25-alpine` `CGO_ENABLED=0` 编译，前端产物经 `COPY --from=web-builder` 进 `internal/webfs/dist`（**跨阶段不能用 `cp`**）；④ `alpine:3.21` 运行时（含 `nodejs`，agent 需要）<br>⚠️ 默认主题由 `go:embed` 内嵌，**不需要**构建归档；agent 由 Go 拉起，**不需要** entrypoint 脚本 |
| **数据卷** | `./data` 挂载数据库 + `picgo/` 配置 + 上传暂存 + `secret.key` + **`themes/`（首页主题）**；compose 中显式声明并给出**「请自行备份 `./data`」**的提示（D82：项目不提供备份功能） |
| **PgSQL 覆盖** | 用 compose 覆盖文件模式（`-f docker-compose.yml -f docker-compose.pgsql.yml`），不要用 profile（D76） |
| **README 边界** | **面向用户的部署只写 docker compose**；systemd / 裸机二进制**不写成官方路径**；开发/构建/测试/lint 命令单独一节（D76/D79）；给出「**用 PicGo 桌面端 / PicList 连过来时填裸域名**，走 `/api/v1`」的说明 |

**验收**

- 用 `picgo-plugin-lankong` 实测：`POST /api/v1/tokens` → `POST /api/v1/upload` → `GET /api/v1/images` → `DELETE /api/v1/images/{key}` 全通，返回体为 Lsky 风格、字段为 snake_case。
- **与内部 API 无重叠**：列出全部注册路由，断言 `/api/v1/**` 下只有 Lsky 的 9 条；`/api/web/v1/**` 下无一条落到 Lsky 保留集。
- 路由冲突检测生效：人为注册重叠路径时启动即失败并给出明确错误。
- `docker compose up -d` 全新环境：自动建库、打印初始管理员密码路径、`/healthz` 通过、前端可打开。
- `docker compose -f docker-compose.yml -f docker-compose.pgsql.yml up -d` 同样可用。
- 容器内 `node -v` 可用（agent 依赖）；`node -e "require('picgo')"` 在部署镜像内可加载（证明 tarball 依赖正确）。
- **内置 SPA 与主题分离可验证**：删掉容器内 `data/themes/` 后 `/` 仍能打开（走内嵌默认主题）；
  `/login`、`/gallery` 等仍由内置 SPA 渲染；`/assets/**` 返回**内置 SPA** 的资源。
- README 中「面向用户」一节除 `docker compose` 外无其他部署方式；开发命令一节包含 `make check` 与三端单独命令且实际可跑。

---

### W10 主题系统（首页）

> **上位依据**：D94（职责划分 + `Pages` 接管范围）、D99（路由分发 + 资源前缀）、
> D95（`ThemeConfigs` 独立表）、D96（zip 安装 + 9 条校验）、D98（manifest 规范）、D97（背景图单一 URL）。
> 接口细节以 [`API.md`](./API.md) §10 与 §10.1 为准；表结构以 [`DATA-MODEL.md`](./DATA-MODEL.md) §3.3 为准。

**产出**

```
server/internal/theme/
  ├── theme.go            Theme 结构体与 manifest 解析（含多语言文本解析、Pages 归一化）
  ├── validate.go         **九项校验**（含 Pages 合法性）；非法 → 判不合法 + 写 theme.error
  ├── store.go            扫描 <dataDir>/themes/；缓存已解析的主题；提供 List / Get / Rescan
  ├── dispatch.go         **路由分发**：保留路径 → 认证页保留 → Pages 最长前缀匹配 → /admin/** → SPA 回退（D99.1）
  ├── assets.go           /theme-assets/** 静态托管（**filepath.Rel** 防穿越 + MIME 映射 + 缓存头）
  ├── config.go           ThemeConfigs 读写：三级兜底 / 类型校验 / 敏感键掩码与跳过 / 未声明键拒绝
  ├── install.go          zip 安装：**9 条安全校验** + 原子性（临时目录 → rename）+ 阈值从设置读
  ├── seed.go             启动 seed：data/themes/ 为空 → 解压内嵌默认主题；非空不动
  ├── embedded/
  │   └── embedded/             **内嵌默认主题源**（`//go:embed all:embedded`，手写自包含单文件）
  │   └── embed.go             解压归档到内存 FS；提供**运行时兜底**服务
  └── handlers.go         /api/web/v1/themes[/**] 的 handler（薄层，转发 service）

themes/default/            默认首页主题的打包产物（manifest.json + index.html + assets/ + screenshot.png）
```

> **`server/internal/web/embed.go`（W9）与 `internal/theme/`（W10）的界限**：
> 前者只负责「**内置 SPA** 的 `go:embed` 与 SPA 回退处理器」；
> 后者负责「主题扫描 / 校验 / **分发决策** / 主题资源托管」。
> 路由注册由 `internal/theme/dispatch.go` 统一编排，它调用前者的 SPA 处理器作为兜底。

**关键实现要点**

#### ① 内嵌默认主题与 seed

| 项 | 要求 |
|---|---|
| 内嵌内容 | `server/internal/theme/embedded/{manifest.json,index.html}`（**自包含单文件**），用 `//go:embed all:embedded` 打进二进制 |
| 生成方式 | 直接维护 `server/internal/theme/embedded/{manifest.json,index.html}`（自包含，内联 CSS/JS，无 assets 依赖） |
| **启动 seed** | 若 `<dataDir>/themes/` **为空**（不存在或无子目录）→ 把内嵌副本写到 `data/themes/default/`；**非空则完全不动**（升级不覆盖用户主题） |
| **运行时兜底** | 主题**缺失 / 损坏（manifest 非法或 index.html 缺失）/ `Pages` 非法** → **直接从内嵌副本服务**（`Pages` 按内嵌默认的 `["/"]`），**永不白屏**；同时写 `OperationLogs`（`theme.error`） |
| 为何不依赖磁盘 | 磁盘主题坏了仍能进后台修复；也避免「解压失败 → 首页 503」的连带故障 |

#### ② manifest 解析与**九项校验**

> 下表是 [`DECISIONS.md`](./DECISIONS.md) D98「校验清单」的**实现级展开**（七项 + 两个已声明字段的落地校验）。

| # | 校验项 | 失败后果 |
|---|---|---|
| 1 | `ID` 非空、匹配 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`、**与目录名一致** | 主题不合法 |
| 2 | `Name` 至少一种语言非空（多语言对象按 `Accept-Language` → `zh-CN` → 首个非空值解析） | 主题不合法 |
| 3 | `Version` 若提供则须为合法版本形态（便于升级判断，不强制） | 主题不合法 |
| 4 | `MinAppVersion` 若提供且**高于当前程序版本** → 视为**不兼容**（列出但不允许启用） | 不可启用 |
| 5 | `Configuration.Type` ∈ {空, `managed`}；其它值（`raw` / `redirect` / 未知）→ 报「不支持的主题类型」 | 主题不合法 |
| 6 | `Items[].Key` 非空、唯一、匹配 `^[A-Za-z][A-Za-z0-9_]{0,63}$` | 主题不合法 |
| 7 | `Items[].Type` ∈ `{string,text,number,switch,select,json}`；`select` 必须有 `Options` | 主题不合法 |
| 8 | **`index.html` 存在** | 主题不合法 |
| 9 | **`Pages` 合法**（D94.2）：每项以 `/` 开头；不含 `..`；只允许 `/*` 一种通配；**不得命中保留路径或认证页保留列表** | 主题不合法 |

补充：`manifest.json` 本身 ≤ `theme.maxManifestBytes`（1 MiB），**超限则拒绝安装**（不属「不合法」，属「不接受」）。

**`Pages` 归一化**：缺省 → `["/"]`；去重；按长度降序排序（便于最长前缀匹配，无需每次排序）。

#### ③ **`Pages` 最长前缀匹配**的分发实现

```
分发决策（dispatch.go）—— 按 D99.1 的 5 步顺序，**一次写通用**：

  path := c.Request.URL.Path

  1. 保留路径？（/api/**, /healthz, /theme-assets/**, /assets/**, /themes/**, /favicon.ico）
     → 各处理器（/themes/** 一律 404）

  2. 认证页保留列表？（/login, /first-login, /forgot-password, /reset-password, /logout）
     → internal/web 的 SPA index.html        【硬编码，主题无法接管】

  3. 命中当前主题的 Pages？（最长前缀匹配；"/" 为精确匹配，"/*" 为通配）
     → 主题的 index.html（active = 磁盘主题；不合法则内嵌默认主题）

  4. /admin/** → internal/web 的 SPA index.html   【硬编码，主题无法接管】

  5. 其他 → internal/web 的 SPA index.html（SPA 回退）
```

| 要点 | 说明 |
|---|---|
| `/` 是**精确匹配** | 仅命中 `path == "/"`；**不等于**「接管全部」（要全部写 `"/*"`） |
| `"/*"` | 通配：命中**所有非保留、非认证、非 `/admin`** 的路径 |
| 匹配算法 | `Pages` 已在归一化时按长度降序，逐项比对 `path == p \|\| strings.HasPrefix(path, p + "/")` |
| **认证页与 `/admin/**` 硬编码保留** | **不读 manifest、不可配置**（D94.2 的安全默认值）；`Pages` 声明了也会被第 9 项校验拒绝 |
| 首次行为 | 默认主题 `Pages = ["/"]` → **只有 `/` 走主题**，其他全部走内置 SPA |
| 扩展 | 某主题写 `["/", "/gallery"]` → `/gallery` 也走主题，**Go 零改动** |
| 决策可观测 | 记一条 debug 级结构化日志（`path` / `decided: theme\|spa\|auth` / `themeID` / `matchedRule`），便于验证验收④ |

#### ④ 静态托管与资源前缀

| 项 | 要求 |
|---|---|
| **两个前缀不混用** | `/theme-assets/**` → 主题的 `assets/`；`/assets/**` → **内置 SPA** 的 `assets/`（embed）（D99.2） |
| **防目录穿越** | 用 **`filepath.Rel`** 判定（**不用 `strings.HasPrefix`**）；`rel == ".." \|\| strings.HasPrefix(rel, ".."+string(os.PathSeparator))` → `40401` |
| MIME | `js`/`mjs`→`text/javascript`，`css`→`text/css`，`svg`→`image/svg+xml`，`json`→`application/json`，`woff2`→`font/woff2`；其余 `mime.TypeByExtension`，未知→`application/octet-stream` |
| 缓存头 | `index.html`（主题与 SPA 两者）→ `Cache-Control: no-cache`；`/theme-assets/**` 与 `/assets/**` → `public, max-age=31536000, immutable`；`/favicon.ico` → `max-age=86400` |
| `/favicon.ico` | 优先当前主题的 `assets/favicon.ico`，缺失回退内置 |
| `/themes/**` | **404**（不暴露主题目录 / `manifest.json` / 源码）；不提供目录列表 |
| **主题有效时的缺失文件** | 返回 **404**（**不回退**到其他主题的同名文件，避免给破主题喂错代码）；只有**主题整体无效**时才整体回退内嵌默认主题 |

#### ⑤ `ThemeConfigs` 读写

| 项 | 要求 |
|---|---|
| 表 | `ThemeConfigs`（**每行一键**，唯一索引 `(ThemeID, Key)`）—— 见 [`DATA-MODEL.md`](./DATA-MODEL.md) §3.3 |
| 键集合 | **只从当前主题 manifest 的 `Configuration.Items` 取**；`Go` 侧**不硬编码任何键名** |
| **读取（三级兜底）** | **DB 值 → manifest 的 `Default` → 类型零值**（`string/text`→`""`；`number`→`0`；`switch`→`false`；`select`→`Options` 首项；`json`→`[]`） |
| 写入 | 只接受**该主题声明过的键**；未声明的键 → `40001`（防脏写）；类型按 `Items[].Type` 校验 |
| 敏感键 | 若 `Items[].Type` 为敏感类（如 `password`，属预留类型）→ API 响应恒为掩码 + `HasValue`；**值等于掩码时跳过不更新**（保留原值） |
| `Source` | 有 DB 行 → `db`；否则 `default`；前端显示来源徽章 |
| 写入者 | 写 `UpdatedBy = 当前 UserUID`（可审计） |
| 换主题 | 旧主题的值**保留**（`ThemeID` 不同），切回来仍生效 |
| 卸载主题 | **不自动删值**；显式清理接口 `DELETE /api/web/v1/themes/{ThemeID}/settings`（`WHERE ThemeID = ?`） |
| 并发 | 每键独立行；两个管理员改**不同键**不互相覆盖（这是选「每行一键」而非「一主题一行 JSON」的核心理由） |

> `theme.active` **不在本表**：它是站点级选择，存 `SystemSettings`（键 `dot.lowerCamel`，符合 D81.3 例外）。

#### ⑥ zip 安装：**9 条校验** + 原子性

| # | 校验 | 失败返回 |
|---|---|---|
| 1 | 仅 `.zip`；压缩包 ≤ `theme.maxPackageBytes`（64 MiB） | `40001` |
| 2 | **防 Zip Slip**：逐 entry 用 **`filepath.Rel`** 判定规范化后的目标必须落在目标目录内，拒绝 `..` 与绝对路径 | `40001` |
| 3 | **拒绝符号链接 entry**（`Mode()&os.ModeSymlink != 0`） | `40001` |
| 4 | 单文件 ≤ `theme.maxFileBytes`（128 MiB）、总计 ≤ `theme.maxExtractBytes`（512 MiB）、文件数 ≤ `theme.maxFiles`（10000） | `40001` |
| 5 | **强制权限位**：目录 `0755`、文件 `0644`，**忽略 zip 里声明的 mode** | 静默强制 |
| 6 | `manifest.json` ≤ `theme.maxManifestBytes`；**九项校验**全过 | `40001` |
| 7 | 目标目录已存在且 `Overwrite=false` | `40901` |
| 8 | **原子性**：先解压到 `<dataDir>/themes/.tmp-<随机>/`，全部校验通过后再 `rename`；失败则删临时目录 | — |
| 9 | 全部主题操作写 `OperationLogs` | — |

**阈值从设置读**（不硬编码）：`theme.maxPackageBytes` / `theme.maxExtractBytes` / `theme.maxFileBytes` / `theme.maxFiles` / `theme.maxManifestBytes`。

**其他规则**：不能卸载**当前启用**的主题（先切走）；不能卸载 `default`（内嵌兜底的锚点）；
目录名与 `manifest.ID` 不一致 → 视为损坏，不进列表，写 `theme.error`。

#### ⑦ 操作日志

全部主题操作写 `OperationLogs`（D45 清单追加）：

| Type | 触发 |
|---|---|
| `theme.install` | zip 上传安装（`Detail` 含 ID / 版本 / 包大小 / `Overwrite`） |
| `theme.uninstall` | 卸载主题 |
| `theme.activate` | 切换 `theme.active`（`Detail` 含 `Previous` / `Next`） |
| `theme.rescan` | 重新扫描（`Detail` 含扫描到的主题数与不合法项） |
| `theme.settings.update` | 改主题设置（`Detail` 只记**键名列表**，**不记值**） |
| `theme.settings.clear` | 清理某主题配置 |
| `theme.error` | 主题损坏 / `Pages` 非法 / 回退内嵌默认主题 / 装包被拒 |

**验收**

> 下表前 8 条对应任务书要求的 ①–⑧；后续为补充回归项。

| 标准 | 验收项 | 验证方式 |
|---|---|---|
| ① | 首启后 `data/themes/default/` 出现（含 `manifest.json` / `index.html` / `assets/`），访问 `/` 可打开 | 全新数据目录启动一次，检查目录与首页 |
| ② | `/gallery`、`/upload`、`/jobs`、`/logs`、`/settings`、`/admin/**` 仍由**内置 SPA** 渲染；`/assets/**` 返回**内置 SPA** 的资源 | 打开各页；对比 `/assets/**` 与 `/theme-assets/**` 的响应内容不同源 |
| ③ | 主题资源走 `/theme-assets/**` 可访问（带 `immutable`）；`/themes/**` 返回 **404**；穿越请求返回 **40401** | 直请求三个路径；试 `/theme-assets/../manifest.json` |
| ④ | 手动把 `manifest.json` 的 `Pages` 改为 `["/", "/gallery"]` → 「重新扫描」（或重启）→ `/gallery` **立刻改由主题渲染**，**Go 源码未改动** | 改文件 + 重扫 + 重访；检查分发 debug 日志的 `decided=theme` |
| ⑤ | `Pages` 写成 `["/login"]` → **校验失败**：主题 `Valid=false` 且带原因、`/login` **仍为内置 SPA**、回退内嵌默认主题、`OperationLogs` 有 `theme.error` | 改文件 + 重扫；看列表、`/login`、日志 |
| ⑥ | 含 `../` 路径的 zip **被拒**（`40001`）且 `data/themes/` 下**无任何残留** | 构造恶意 zip 上传；列目录确认临时目录已清理 |
| ⑦ | 删掉整个 `data/themes/` → `/` **仍能打开**（走**内嵌默认主题**） | 删目录 + 重启（或热重扫）；访问 `/` |
| ⑧ | 主题设置的 `Source` 徽章能区分 `db`（改过的键）与 `default`（未改的键） | 改一个键、留一个不改；对比两个键的 `Source` |

**补充回归项**

- 上传含 symlink entry 的 zip → 被拒；单文件 > `theme.maxFileBytes` 的 → 被拒；
  解压后总体积/文件数超限的 → 被拒（四类拒绝均为 `40001`）。
- 把 `theme.active` 指向不存在的 ID → 回退内嵌默认主题（不白屏），并写 `theme.error`。
- 尝试卸载 `default` 或**当前启用中**的主题 → `40901` 并给出原因。
- 装一个 `Pages = ["/*"]` 的主题 → `/`、`/gallery`、`/jobs` 均走主题，
  但 `/login` 与 `/admin/users` **仍为内置 SPA**（保留列表生效）。
- 改主题设置 → `ThemeConfigs` 行数正确变化（**每行一键**）；两个管理员同时改**不同键** → 两键都生效。
- 提交 manifest **未声明**的键 → `40001`；提交类型不符的值（如 `number` 传字符串）→ `40001`。
- 切换到主题 B 再切回 A → A 的旧值**仍在**；卸载 A 后 `ThemeConfigs` 中 A 的行**仍保留**，
  直到显式「清理该主题配置」。
- `manifest.json` > 1 MiB → **拒绝安装**（与「主体不合法」区分开）。

---

## 5. 接口冻结与变更流程

**冻结时机**：`docs/API.md` 在**批次 A 结束时冻结**，作为批次 B/C 并行开发的唯一输入。（W0 已定型的
`UploadOptions.uploader` / `contextData` 形态是其上游依赖，已反映在 `docs/API.md` §13。）

**变更流程**

1. 先改 `docs/API.md`（含请求/响应示例与错误码）。
2. 在对应工作流内**同批次**完成三端同步：
   - Go 侧 `handler` / `service` 与 `response` 错误码
   - agent 侧 `src/types.ts` 与路由实现（如涉及）
   - 前端 `web/src/types/api.ts` 与 `lib/api.ts`
3. 若变更涉及表结构 → 先改 `docs/DATA-MODEL.md`，且**只通过新增迁移实现**（D77「迁移只前进」）。
4. 若变更涉及方向性决策 → 先改 `docs/DECISIONS.md`，**追加新编号**而不修改历史条目。
5. 若变更涉及 PicGo-Core 补丁（如新增 `UploadOptions` 字段）→ 先改 `../PicGo-Core` 的类型与
   `FORK-NOTES.md`，再同步 agent 调用点与 `docs/PICGO-INTEGRATION.md`（D48–D51）。
6. 若变更涉及**主题接口或其 manifest 格式** → 先改 `docs/API.md` §10/§10.1 与
   `docs/DECISIONS.md`（D94/D95/D98）；`manifest.json` 的字段与校验规则属于**外部契约**，
   **只可新增字段、不可改语义**（与 Lsky 契约同等级）。

**向后兼容要求**

- 新增字段一律可选，响应中 `omitempty`（D77.2）。
- 不重命名既有字段、不改变既有字段语义；确需弃用时**保留旧字段**并标记 Deprecated（D77.1）。
- 不改变既有错误码含义；新增错误码只能追加。
- **内部 API** 路径带版本（`/api/web/v1`）；破坏性变更走 `/api/web/v2`。
- **Lsky 兼容层**路径与字段**永不变更**（外部冻结契约，D52）。
- **主题 `manifest.json`** 的已有字段与校验语义**永不变更**（已发布的第三方主题不被破坏）；
  新增能力一律用新字段（D98/D94）。
- **主题接管的保留列表（认证页与 `/admin/**`）不得收缩为可配置**（安全默认值，D94.2）。

**命名同步要求（D81）**

- API JSON 字段与数据库列名一律 PascalCase、缩写词全大写；**任何新字段都必须遵守**。
- `web/src/types/api.ts` 是 `docs/API.md` 的**命名镜像**：文档用 `JobUID`，前端类型就必须是 `JobUID`
  （不是 `jobUid`、也不是 `job_id`），**不做字段名转换层**。
- 三类「不改名」的例外必须遵守：Lsky 契约（snake_case）、环境变量（`UPPER_SNAKE_CASE`）、
  settings 键（`dot.lowerCamel`）；picgo 侧字段名（`picBed` / `_configName`）与驱动配置字段名
  （`repo` / `token` / `path`）一律原样保留。

**前端类型同步要求**

- `web/src/types/api.ts` 是 `docs/API.md` 的镜像，**不接受**「文档改了前端后补」。
- 校验手段：`pnpm typecheck` 必须通过；`get` 一个文档中不存在的字段时用 mock 快速暴露。

---

## 6. 质量门禁

| 子项目 | 命令 | 要求 |
|---|---|---|
| **PicGo-Core（fork）** | `cd ../PicGo-Core && pnpm lint && pnpm test` | 全绿；**当前基线：`250 passed / 5 skipped`**（22 个测试文件通过、3 个跳过）。`pnpm lint` 含 dpdm 循环依赖检查 + `tsc --noEmit` + eslint |
| **PicGo-Core（编译）** | `cd ../PicGo-Core && pnpm build` | 必须成功，产出 `dist/index.cjs.js` + `dist/index.esm.js`（**agent 依赖该产物**） |
| server | `cd server && go vet ./... && go test ./...` | 全绿；无 vet 告警 |
| server（编译） | `cd server && CGO_ENABLED=0 go build ./cmd/picgo-web` | 必须成功（D11 硬性要求） |
| picgo-agent | `cd picgo-agent && pnpm lint && pnpm typecheck && pnpm test` | 全绿 |
| web（内置 SPA） | `cd web && pnpm lint && pnpm typecheck && pnpm build` | 全绿；产出 `web/dist`（`base = '/'`） |
| web（默认首页主题） | `cd web/theme-default && pnpm lint && pnpm typecheck && pnpm build` | 全绿；`base = '/theme-assets/'` |
| **主题（W10）** | `make e2e-theme` | 73 项：分发算法 / 认证页保留 / 资源前缀隔离 / 防穿越 / 内嵌兜底 / zip 安装 9 条校验 / 卸载规则 |
| **主题端到端（W10）** | `make e2e-theme` | 73 项，含：① 内嵌主题的 `index.html` **无 `/assets/` 引用**（资源前缀隔离，D99.2）；② 认证页与 `/admin/**` 无法被主题接管；③ zip 安装 9 条校验；④ 删空 `data/themes/` 仍不白屏 |
| **全仓** | `make check` | **串行跑上面全部端（含 PicGo-Core 与主题打包校验）**，任一失败即整体失败 |

**门禁补充约定**

- `make check` 是**提交前必跑**命令，也是本次计划的统一验收入口；
  它必须**先构建 `PicGo-Core`**，否则 agent 端会因缺 `dist/` 而假失败（见 W1）。
- 涉及迁移的改动，必须补一条「连跑两次迁移 `Version` 不变」的测试。
- 涉及凭据的改动，必须补一条「响应中不含明文密钥」的测试。
- 涉及删除的改动，必须补一条「配额已退还 + 日志已写入」的测试。
- 涉及表/列命名的新增，必须同步更新 DATA-MODEL §1 的映射表并补一条 `TableName()` 断言测试。
- 涉及 PicGo-Core 的改动，必须补对应单测并更新 `FORK-NOTES.md`。
- **涉及主题接管的改动**，必须补一条「`Pages` 越界（写到 `/login` 或 `/admin/**`）被拒绝」的测试。
- **涉及分发逻辑的改动**，必须补一组「保留路径 / 认证页 / 命中主题 / `/admin` / SPA 回退」五分枝的测试。
- **涉及主题资源的改动**，必须补一条「`/theme-assets` 防穿越（`..` 与绝对路径均被拒）」的测试。
- **涉及 zip 安装的改动**，必须覆盖 **9 条校验**中至少：Zip Slip、symlink 拒绝、体积/文件数上限、**原子性（失败后无残留）**。
- **涉及主题兜底的改动**，必须补一条「主题目录缺失/损坏 → `/` 仍可打开（走内嵌默认主题）」的测试。

---

## 7. 风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| **PicGo-Core 补丁与上游 rebase 冲突** | 上游 `dev` 更新后难以合并，补丁漂移失效 | 冲突面**仅 3 个源文件**（`src/types/index.ts`、`src/utils/createContext.ts`、`src/core/Lifecycle.ts`），`FORK-NOTES.md` 已逐条记录改动位置、动机与合并提示；策略为「只增不改」；**上游若原生支持「按次指定图床」则直接删除本地补丁**、改用上游 API（D48–D51） |
| **PostgreSQL 标识符大小写** | 手写 SQL / 迁移脚本在 PgSQL 下直接报 `relation "users" does not exist` | GORM 层配 `NoLowerCase: true` 且每个模型显式 `TableName()`，应用层不受影响；**约定手写原生 SQL 一律加双引号**（`"Uploads" ("UserUID")`）；在迁移文件与 `ARCHITECTURE.md` 中标注该坑；W2 验收含一条「去引号会失败」的回归测试（D81.4） |
| **命名规范在跨层传递时被忘记** | 后端返回 `JobUID`、前端写成 `jobUid`，出现字段永远取不到的隐性 bug | `web/src/types/api.ts` 作为 `docs/API.md` 的**命名镜像**，禁止写转换层；`pnpm typecheck` + mock 提前暴露；§5 专列「命名同步要求」；W2/W5 验收含命名回归测试；新增字段时三端同批次同步（D81） |
| **picgo-core 事件粒度粗**（`uploadProgress` 仅 `0/30/60/100/-1` 四档） | 进度条体验差 | 单文件同步上传（D39）让四档变成该文件自己的进度；总进度以「已完成 item / 总 item」为主，单文件内四档插值 |
| **并发下事件无法归属**（`createContext` 把 `emit`/`on` 绑到根实例） | 并发时 A 的 `finished` 会喂给 B | 默认 `concurrency=1` 天然无歧义（D35）；`>1` 时用 W0 补丁 P4 的 **`contextData`** 做归属（**已落地并实测**） |
| **`upload()` 失败语义反直觉** | 失败被当成成功，用户看到空 URL 却记为成功 | 实测：传路径失败时**既不 reject 也不返回 `Error`**，而是返回可能为空的数组；agent **必须同时监听 `failed` 事件**；W4 验收含「图床故意失败时返回失败」的用例（D39 注） |
| **插件热加载不可靠** | 装完插件不生效 | 装/卸后**重启 agent 进程**换取状态干净（低频操作可接受）；Go 侧做好重连与「内核重启中」提示 |
| **插件 = 服务器上执行任意代码** | 安全风险 | 插件管理**仅 admin**；全部操作写 `OperationLogs`；README 与插件页明确风险提示；agent 只监听 `127.0.0.1` + `X-Agent-Token` |
| **SQLite 并发写** | `database is locked` | 开启 WAL + `busy_timeout`；写操作集中在 service 层；文档推荐生产切 PgSQL（D11/D12） |
| **picgo-core 单实例** | 无法按用户隔离驱动 | 本项目定位「驱动只由管理员配置」（D4），单实例正好匹配；`concurrency>1` 靠补丁而非多实例 |
| **远端删除依赖插件约定**（`emit('remove')` 非公开 API） | 部分驱动删不掉远端文件 | 以 `Capabilities.SupportsRemoteDelete` 显式告知；不支持时只删本地记录并在 UI 标注；删除结果写入 `OperationLogs` 便于追溯（D47） |
| **魔法路径因驱动而异** | 用户配了模板却不生效 | 能力探测自动推断 `SupportsPathTemplate`；不支持时**降级为文件名前缀**并给出界面提示（D44） |
| **Tailwind v4 / Radix 版本较新** | 组件踩坑 | 组件基座（W7）一次做对并集中封装，业务页面只组合；不引入第二套 UI 方案 |
| **前端 SSE 断线** | 进度卡死、状态不同步 | `lib/sse.ts` 带心跳看护与自动重连；重连后主动拉一次任务/图库状态做自愈；服务端 25s `ping` |
| **配额冗余计数漂移**（`Users.UsedBytes`） | 配额不准 | 所有增减走单一入口（`quota_service` / `delete_service`）；随每日维护任务**静默对账**（发现偏差才写日志）；删除流程单测覆盖。**不新增对账端点**（属运维动作，且 D82 不做导出导入类入口） |
| **`/api/v1` 与 `/api/web/v1` 被混用** | 前端调错前缀、或新接口误占 Lsky 保留集 | 前缀已天然隔离（D80），**不再需要任何「路径让位」**；仍保留**启动时冲突检测并 panic** 作为防护；W9 验收含一条「列出全部注册路由并断言无重叠」的检查 |
| **agent 崩溃/重启期间上传失败** | 用户体验差 | 健康探测 + 退避重启（最多 5 次）；期间返回 `503 / 50002`，前端提示并轮询 `/healthz`；job 已有重试机制兜底 |
| **主题目录缺失/损坏**（被误删、解压不完整、manifest 写坏） | 首页白屏或 503，用户以为服务挂了 | **二进制内嵌一份默认主题**（`go:embed` 压缩归档）作为**运行时兜底**：主题无效时直接从内存归档服务（**永不白屏**）；启动时若 `data/themes/` 为空则自动 seed；写 `OperationLogs`（`theme.error`） + 后台主题页显著提示（D94.4） |
| **zip 安装被用于路径穿越 / zip bomb** | 可覆盖服务器任意文件（远高于「首页被篡改」的危害） | **9 条校验**：**`filepath.Rel`** 判路径（不用 `strings.HasPrefix`）+ **显式拒绝 symlink entry** + **强制权限位 `0644/0755`**（忽略 zip mode，防 setuid）+ 包/单文件/总解压/文件数上限 + manifest 九项校验；**原子性**（临时目录 → `rename`，失败清理），且**仅 admin 可操作**（D96） |
| **主题误接管敏感页面**（尤其登录页） | 第三方主题可伪造登录框**窃取普通用户密码** | 双重防护：① **`Pages` 校验**拒绝保留路径与认证页列表（主题直接不合法）；② **认证页与 `/admin/**` 硬编码保留**在分发代码里，**不读 manifest、不可配置**（D94.2/D99.1）；UI 上明示「认证页与后台由系统内置」 |
| **主题设置变更后首页仍缓存旧版** | 用户以为改了不生效 | 主题 `index.html` 一律 `no-cache`（D99.2）；`/theme-assets/**` 因文件名带哈希可长缓存；切换主题**无需重启**，刷新页面即可 |

---

## 8. 与可扩展性相关的实施约束

以下来自 **D77 / D78 / D80 / D81 / D82 / D94 / D95 / D99**，**是实现者不得因「图省事」而破坏的硬约束**：

| # | 约束 | 依据 | 反例（禁止） |
|---|---|---|---|
| 1 | **新内容域一律新建独立表** | D78 | 把日志、附件、配置塞进某个表的 JSON 列 |
| 2 | **对外标识一律用 `UID`**（ULID 字符串） | D77 | API 暴露自增 `ID` 并让前端当稳定标识 |
| 3 | **枚举一律字符串存储** | D77 | `Status = 3` 之类的数字魔法值；建 DB enum |
| 4 | **迁移只追加**，已发布条目永不修改 | D77 | 回头改 `migrateV1` 的字段定义 |
| 5 | **不建跨表外键约束**，只建索引 | D77 | 加 `FOREIGN KEY ... ON DELETE CASCADE` 依赖 DB 级联 |
| 6 | **业务配置进 KV 表**（`SystemSettings` / `UserSettings`） | D77 | 为新配置项加一个数据库列 |
| 7 | **驱动差异靠能力探测** | D77.3 | 在 Go/TS 里 `switch (driverType) { case 'github': ... }` |
| 8 | **主要业务表带 `Metadata` JSON 扩展位** | D77 | 为不确定的新字段先加列 |
| 9 | **字段只增不改**；废弃字段标记 Deprecated 保留 | D77 | 删除字段或改其语义 |
| 10 | **分层不穿透**：`handler → service → repository` | D77.2 | 在 handler 里直接写 `db.Where(...)` |
| 11 | **关键动作发内部事件** | D77.2 | 把后续功能硬编码进主流程（如「上传成功后顺便发通知」写死在 `upload_service`） |
| 12 | **前端导航项配置数组驱动** | D77.2 | 在 AppShell 里写死菜单 JSX |
| 13 | **非语义必需字段允许 NULL** | D77 | 对新功能列加 `NOT NULL` 导致要回填历史数据 |
| 14 | **内部 API 前缀只能是 `/api/web/v1`**；`/api/v1` 的 Lsky 保留集（`tokens`/`profile`/`strategies`/`upload`/`images`/`albums`）**永不得占用** | **D80** | 图省事把新内部接口挂在 `/api/v1/xxx`，撞掉第三方客户端 |
| 15 | **命名：API JSON 字段 + 表名 + 列名一律 PascalCase，缩写全大写** | **D81** | 新接口返回 `jobUid` / 新表叫 `job_items` / 新列叫 `created_at` |
| 16 | **三类命名例外不得顺手「统一」**：Lsky 契约（snake_case）、环境变量（`UPPER_SNAKE_CASE`）、settings 键（`dot.lowerCamel`）；picgo 侧与驱动字段名原样 | D81.3 | 把 `strategy_id` 改成 `StrategyId`、把 `PICGO_WEB_LISTEN` 改成 `PicgoWebListen` |
| 17 | **URL 路径段保持小写复数** | D81.3 | `/api/web/v1/Storage/Configs`（大小写敏感的代理/中间件会出问题） |
| 18 | **GORM 必须 `NoLowerCase: true` + 每模型显式 `TableName()`** | D81.4 | 依赖词形变化库自动推断表名（会得到 `users`） |
| 19 | **手写原生 SQL 必须给标识符加双引号** | D81.4 | `CREATE INDEX ... ON Uploads (UserUID)`（PgSQL 下失败） |
| 20 | **不做备份/恢复的任何端点或界面** | **D82** | 加一个「导出数据库」按钮或定时备份任务 |
| 21 | **新页面主题化 = 只改主题 manifest 的 `Pages`**（Go **零改动**） | **D94.2 / D99.1** | 在 Go 里写 `if path == "/gallery" { ...主题... }` 之类的硬编码判断 |
| 22 | **主题新配置项 = 只改主题 manifest 的 `Configuration.Items`**（Go **零改动**） | **D95 / D98** | 为某个主题的配置项在 Go 里加一个配置键常量或结构体字段 |
| 23 | **主题配置存独立表 `ThemeConfigs`**（每行一键）；`SystemSettings` 只留 `theme.active` | **D95 / D78** | 把主题配置塞进 `SystemSettings`（`theme.<ID>.<Key>` 前缀）或塞进 `site.*` |
| 24 | **不得把主题特有配置塞进 `site.*`**（归类原则：站点属性 → `site.*`；主题的画法 → 主题配置） | **D95** | 恢复 `site.homepage.*` / `site.background.*` 这类键 |
| 25 | **不得让主题接管认证页与 `/admin/**`**（硬编码保留、**不读 manifest、不可配置**） | **D94.2 / D99.1** | 把认证页交给主题；或加一个「允许主题接管登录页」的开关 |
| 26 | **资源前缀不得混用**：主题用 `/theme-assets/**`，内置 SPA 用 `/assets/**` | **D99.2** | 让主题构建用默认 `base: '/'`（它会去自己的 `/assets/**` 找文件，实际拿到 SPA 的资源） |
| 27 | **主题目录布局固定为 `manifest.json` + `index.html` + `assets/`**（**无 `dist/` 子目录**） | **D94.3** | 跟随 Komari 的 `dist/` 布局或在该层再加一层 |
| 28 | **主题的元数据与 `Pages` 不落库**（manifest 文件即真相源，**不建 Themes 表**） | **D94.4** | 为图方便把主题元信息搬进数据库并按库分发 |

**新增功能前的自检清单**（对照 DATA-MODEL §11）：能否放 `Metadata`？是新配置项吗（→ KV 表）？是新枚举值吗（→ 字符串列）？是新内容域吗（→ 新表）？对外暴露用 `UID` 了吗？引用用 `UID` 字符串了吗？**命名是 PascalCase 吗（且避开了三类例外）？路径挂在 `/api/web/v1` 下吗？**

**涉及前端/页面时的额外自检**：

- 新页面是**内置 SPA** 的还是**交给主题**的？若是主题：**只改主题 manifest 的 `Pages`**，不要动 Go。
- 该页面是否属于**认证页保留列表**或 `/admin/**`？若是 → **必须是内置 SPA，主题不可接管**。
- 新配置项是**站点属性**（→ `site.*`）还是**主题的画法**（→ 主题 manifest 的 `Configuration.Items` + `ThemeConfigs`）？
- 新增静态资源是否落在正确的资源前缀下（内置 SPA `/assets/**`；主题 `/theme-assets/**`）？
- 主题构建配置的 `base` 是否与它被托管的前缀一致？

---

## 9. 当前进度

| 工作流 | 状态 | 说明 |
|---|---|---|
| **W0 PicGo-Core fork 与补丁** | ✅ **已完成** | 分支 `PicGo-Web`（基线 v3.0.2 / `f710083`），补丁 4 处（`UploadOptions.uploader` / `contextData` / per-context 配置覆盖 / `Lifecycle.step` 并发修复），**250 单测全绿**；实测并发两批次各走各的图床 |
| **W1 契约与骨架** | ✅ **已完成** | `docs/` 九份 + `Makefile` + `.env.example` + `docker-compose{,.pgsql}.yml` + `.gitignore` + `AGENTS.md` |
| **W2 Go 基础设施** | ✅ **已完成** | 12 个包；20 张表全 PascalCase + 12 个组合索引（相册表已于 D101 移除）；**492 个测试用例**；`CGO_ENABLED=0` 静态编译 45MB |
| **W3 Go 鉴权与用户** | ✅ **已完成** | bcrypt/JWT/refresh 轮换/API Token/GitHub OAuth/登录限流/防枚举/首启引导；`/auth/*`、`/users/*` |
| **W4 Agent 内核** | ✅ **已完成** | hono 侧车：单文件同步上传（同时判返回值与 `failed` 事件）、图床多配置、能力探测、魔法路径、远端删除、SSE、插件管理；**168 单测** |
| **W5 Go 业务核心** | ✅ **已完成** | 存储（DB→config.json 键级合并投影 + reconcile）、上传队列（Job/JobItem、并发度可配、配额、限流、重试、超时、重启恢复、优雅关闭）、图库 |
| **W6 Go 日志·邮件·删除** | ✅ **已完成** | 操作日志查询、SMTP 发信 + EmailLogs（不存正文）、远端删除（D47）、每日清理、插件管理 |
| **W7 前端基座** | ✅ **已完成** | 设计 token（DESIGN §2 全量）、30 个 UI 组件、http/sse 客户端、Zustand store、路由三层守卫、登录页 |
| **W8 前端页面** | ✅ **已完成** | 上传（队列 + 进度插值）、图库（无缩略图 + 管理员 Tab）、任务、日志、存储（动态表单）、插件、主题、站点、用户、设置 |
| **W9 Lsky 兼容 + 静态托管** | ✅ **已完成** | 静态托管（`/assets` vs `/theme-assets`、SPA 回退、防穿越）；**Lsky v1 兼容层**（9 端点 + Lsky 信封 + Laravel 分页 + 令牌可重复签发）；**启动路由冲突检测**；Dockerfile（四阶段） |
| **W10 主题系统** | ✅ **已完成** | `manifest.Pages` 自行注册 + 最长前缀匹配 + 认证页/后台永久保留 + 内嵌兜底 + seed + `ThemeConfigs` 三级兜底 + zip 安装 9 条校验 |

### 端到端验证结论（真实跑通，非推断）

```
主题分发      / → 主题首页(12KB)   /gallery 与 /login → 内置 SPA(2.8KB，二者一致)
安全         /theme-assets 缺失 404 · /themes/** 404 · 路径穿越 404 · 认证页无法被接管
site/config  Site + Theme(Pages=["/"]) + Settings(BackgroundURL 默认 ACG API)
主题配置     读写 Source: default → db
存储配置     密钥 AES-GCM 加密入库（DB 非明文）· 响应掩码 · 能力探测正确
上传链路     2 文件 → job succeeded → 魔法路径 /2026/09/14/a-12345678 生效
配额         累加 140B → 删除后退还(140→70) + FreedBytes 报告
外链         markdown / url / html 三种格式正确
日志         OperationLogs 写入 14 条 · 类型清单 25 项
前端         经 Vite 代理用真实后端账号登录成功 · 图库列表可取
```

### 端到端验证（可复现脚本）

| 命令 | 覆盖 | 实测 |
|---|---|---|
| `make e2e-theme` | 主题分发算法、认证页保留、资源前缀隔离、防穿越、内嵌兜底、zip 安装 9 条校验、卸载规则 | ✅ **73 项通过** |
| `make e2e-lsky` | Lsky 9 端点、**信封一致性（11 个错误场景）**、令牌可重复签发、冲突检测 | ✅ **20 项通过** |
| `make e2e-all` | 两者串行 | — |

### 剩余工作

1. **前端 e2e**：目前只有 typecheck/lint/build；端到端靠上面两个脚本（服务端视角）。
2. **Docker 构建实测**：根目录 `Dockerfile` 已通过指令自检，
   但**本机无 docker，未实际 `docker build` 过**。首次构建请留意：
   - `make vendor` 必须先生成 `deploy/vendor/picgo-*.tgz`（Dockerfile 依赖它）
   - Go 阶段用 `COPY --from=web-builder` 取前端产物（跨阶段不能用 `cp`）

## 与决策的偏差

### X1 ✅ 已由 D80 解决：内部 API 迁至 `/api/web/v1`，与 Lsky 彻底隔离

曾规划「共享 `/api/v1` 前缀 + 内部相册让位至 `/api/v1/gallery/albums`」。

**D80 后**：内部 API 全部迁至 **`/api/web/v1/**`**，Lsky 兼容层**独占 `/api/v1/**`**，
**冲突彻底消失**；内部相册回到普通路径 **`/api/web/v1/albums`**；
仅保留**启动时路由冲突检测并 panic** 作为防护（防将来新增接口时静默覆盖 Lsky 保留集）。

已同步至 `DECISIONS.md` D52/D80、`API.md` §15、`ARCHITECTURE.md` 偏差表 C1。
**W9 实现时必须加上启动时的路由冲突检测。**

### X2 ✅ 已处理：文档交叉引用笔误

`docs/API.md`（旧版）§10 引用「`DATA-MODEL.md` §4.6」，
而现版 `DATA-MODEL.md` 的配置键位表位于 **§7.4**。
这属于文档交叉引用问题，**不构成对决策的偏差**；已随本轮重写一并修正。

### X3 ✅ 已记录：命名规范变更（D81）

本轮新增 D81（PascalCase 大驼峰），属于**全局命名规范的转向**：

- **变更前**：API JSON 字段用 camelCase（`accessToken` / `jobUid`），数据库表名/列名用 snake_case
  （`users` / `upload_results` / `created_at`）。
- **变更后**：API JSON 字段与数据库表名/列名一律 **PascalCase**（`AccessToken` / `JobUID` /
  `Users` / `UploadResults` / `CreatedAt`），缩写词全大写。

本文档及相关文档已按 D81 改写。实现时注意 **D81.3 的三类例外不得被「统一」**：
Lsky 契约（snake_case）、环境变量（`UPPER_SNAKE_CASE`）、settings 键（`dot.lowerCamel`），
以及 picgo 侧字段名与驱动配置字段名保持原样。

### X4 ✅ 已同步：`DECISIONS.md` / `DATA-MODEL.md` 的旧写法已修正

上一轮登记的「上位文档自身仍有旧写法」问题，**已全部同步**：

| 位置 | 原状 | 现在 |
|---|---|---|
| `DATA-MODEL.md` §4.3 `Albums` 注 | 「对外路径为 `/api/v1/gallery/albums`」 | ✅ `/api/web/v1/albums` |
| `DATA-MODEL.md` §7.4 OAuth 回调示例 | `/api/v1/auth/oauth/github/callback` | ✅ `/api/web/v1/auth/oauth/github/callback` |
| `DECISIONS.md` D36 / D38 示意 | `POST /api/v1/uploads` | ✅ `POST /api/web/v1/uploads` |
| `DECISIONS.md` D77.2 | 「路径带 `/api/v1`」 | ✅ `/api/web/v1` |
| `DECISIONS.md` §十 补丁状态 | 「**待落**补丁」 | ✅ 「**已落补丁（已实现并验证，提交 `6419c2f`）**」，含实测证据 |
| `DECISIONS.md` D16 / D45 / D47 / D65 / D78 | 表名/列名 snake_case | ✅ PascalCase（`SchemaMeta` / `OperationLogs` / `UploadResults.RawOutput` / `StorageConfigs.UID` / `Users` …） |

> 因此实现时**可以放心直接照 `DECISIONS.md` 与 `DATA-MODEL.md` 抄写**，两处已无冲突。

### X5 ✅ 已同步：主题范围与前端归属变更（D94–D99）

本轮把「主题」从「整个前端的主题」定为「**可选的页面覆盖层，自行注册接管页面，默认主题只注册首页**」，
并把它从原先的「前端不内置」改回「**前端内置 + 主题管声明的页面**」。本文档已按新定义重写：

| 位置 | 旧（已作废） | 现（已同步） |
|---|---|---|
| W8 产出 | 内置 SPA 里有 `home/`（公开落地页） | ❌ 已删；**首页由主题提供**（代码在 `web/theme-default/`，W7） |
| W8 落地页行 | 「内容来自 `site/config` 的 `Homepage`；背景图按 `Background.Mode` 处理（D85）」 | ✅ 「内容来自**主题配置**（`HomepageFeatures` 等，D95）；**背景图直引，不做任何判断**（D97）」 |
| W8 / 站点设置 | 站点设置含「首页内容 / 背景图」 | ✅ 已移出；改为**主题管理页 `/admin/themes`** |
| W9 产出 | 「前端 embed」（语义含糊，易误读为「主题也 embed」） | ✅ 明确为「**内置 SPA 的 embed 接线**」 |
| 工作流表 | 无 W10 | ✅ 新增 **W10 主题系统（首页）** |
| 表数量 | 20 张 | ✅ **20 张**（新增 `ThemeConfigs`（D95）；相册表后于 D101 移除） |
| D83 / D85（旧首页与背景图决策） | 曾被本文引用 | ✅ 已分别被 **D94**（首页改由主题提供）与 **D97**（背景图单一 URL）取代；本文不再引用 |
| **D84**（不做缩略图） / **D86**（页面访问范围） | 曾被本文引用 | ✅ **仍然有效**，本文继续引用（未受本轮主题变更影响） |

**关于「九项校验」的说明**：W10 的九项校验是 `DECISIONS.md` D98「校验清单」的**实现级展开**
（七项 + `Version` 形态与 `MinAppVersion` 兼容性两个**已声明字段**的落地校验），
**不构成对决策的偏离**；若后续要正式化这两项，应先追加到 D98。

**关于默认主题构建目录的说明**：任务书给了两个选项（`web/theme-default/` 或同一工程独立入口），
本文选择 **`web/theme-default/`（独立构建单元）**并在 W7 写明了理由（两者 `base` 必须不同），
属**实现选择**而**非对决策的偏离**（D94 只规定「主题是可替换的前端代码」）。

以上均已同步至 `DECISIONS.md`（D94–D99）、`DATA-MODEL.md`（§3.3 / §7.4）、`API.md`（§10 / §10.1）。
**无待裁决项**。

### X6 ℹ️ 建议同步（跨文档，非冲突）：`DECISIONS.md` D86 的页面清单缺 `/admin/themes`

D86「页面访问范围」的「需管理员」一行列了 `/admin/users`、`/admin/storage`、`/admin/plugins`、
`/admin/site`、`/admin/logs`，**未列 `/admin/themes`**（本轮 D94–D99 新增的主题管理页）。

- 这**不是与 D86 的冲突**（它只是枚举未穷尽），**不影响本文档的结论**；
  本文已在 W8 / §8 / 里程碑多处按**含 `/admin/themes`** 撰写。
- **建议**：由 `DECISIONS.md` 的负责人把 `/admin/themes` 补进 D86 的管理员清单，避免实现者照抄时漏掉该页。
