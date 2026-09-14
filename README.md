# PicGo-Web

**在服务器上运行 PicGo，通过浏览器管理存储驱动、插件与图片。**

PicGo-Web 把桌面端 [PicGo](https://github.com/Molunerfinn/PicGo) 的能力搬到服务器：
后端负责账号、配额、队列、元数据与审计，而**图片的存储与分发完全由 PicGo 内核（`picgo-core`）及其插件承担**。
因此你可以在浏览器里配置图床、装插件、批量上传、整理相册、拷贝外链，
而图片本身照旧落在你自己的 GitHub / S3 / WebDAV / 七牛 / 阿里云 OSS 等图床上，由你完全掌控。

> 定位：**自用为主，其次朋友 / 团队之间分享使用。** 不是商业化图床服务。

## 特性

| 能力 | 说明 |
|---|---|
| **多用户 + 存储配额** | 按字节计量的每用户配额（`0` = 不限额），管理员可逐个调整；新建用户默认配额由系统设置决定 |
| **邮箱登录** | 只认邮箱；用户由管理员创建或邮件邀请，**不开放自助注册** |
| **GitHub OAuth 登录** | 需先在后台绑定才能用于登录（不是注册入口） |
| **存储驱动 · 管理员配置** | 由 `picgo-core` 提供驱动能力；**同一驱动类型可添加多条实例**（多个 WebDAV / 多个 GitHub 仓库），后端以 `UID` 唯一标识、界面显示 `Name` |
| **插件管理** | 搜索 / 安装 / 卸载 / 更新 / 启停 PicGo 插件，安装过程实时输出日志 |
| **后端上传队列** | 队列 + 并发限制 + 失败重试 + 单文件超时 + 优雅关闭 + 重启恢复；进度经 SSE 实时推送 |
| **魔法路径 / 魔法文件名** | 每个存储配置**独立**配置命名模板（`{Y}{m}{d}{md5-8}{uniqid}...`），驱动是否支持自定义路径由能力探测决定 |
| **图库与整理** | 相册、重命名、批量操作、按驱动/相册/状态筛选；一键复制外链（Markdown / 直链 / HTML） |
| **管理员视图隔离** | 「我的图片」与「全部用户的图片」在同一页面用顶部 Tab 切换，避免误操作他人图片 |
| **操作日志与邮件日志** | 统一审计日志（按类型 / 状态 / 关键词过滤与搜索，默认保留 180 天并自动清理）+ 独立的邮件发送记录（不存邮件正文） |
| **远端删除** | 删除图片时可选同步删除图床上的文件 —— **仅当该驱动的插件实现了 `remove` 事件时才生效**，否则只删本地记录并标记 |
| **Lsky API v1 兼容** | 提供 Lsky Pro v1 契约，PicGo 桌面端 / PicList / uPic / ShareX 等客户端可直接把本站当图床 |
| **双数据库** | 默认纯 Go SQLite（免 CGO），可切 PostgreSQL |
| **中文界面** | 文案全部 key 化（i18n），预留英文；支持明暗主题 |
| **可替换主题（页面级）** | 主题在 `manifest.json` 里**自行注册**要接管的页面（`Pages`：首页、上传页、图库…）；**未注册的页面一律走内置界面**。支持后台 zip 上传 / 启用 / 卸载 / 重新扫描，主题自带配置项。**认证页与后台永久内置，不可被主题接管**。主题缺失或损坏时自动回退内置默认主题，**不会白屏** |
| **可配置落地页** | 首页内容（背景图、核心能力、应用场景、FAQ）**存在主题配置里**，在后台「主题 → 设置」编辑，切换后立即生效 |

## 快速开始（面向用户）

唯一官方部署方式是 **Docker Compose**（`DECISIONS.md` D76）。

### 前置要求

- Docker 20.10+
- Docker Compose v2（`docker compose` 子命令形式）

不需要预装 Go / Node / PicGo —— 镜像内已包含全部运行时。

### 1. 建目录并下载部署文件

```bash
mkdir picgo-web && cd picgo-web

curl -O https://raw.githubusercontent.com/YeqingKy/PicGo-Web/main/docker-compose.yml
curl -o .env https://raw.githubusercontent.com/YeqingKy/PicGo-Web/main/.env.example
```

### 2. 编辑 `.env`

最小可用配置（其余保持默认即可）：

```dotenv
# 监听地址（容器内），默认 8080
PICGO_WEB_LISTEN=0.0.0.0:8080

# 数据目录（容器内路径，对应 compose 中的数据卷）
PICGO_WEB_DATA_DIR=/data

# 数据库：sqlite（默认）
PICGO_WEB_DB_DRIVER=sqlite

# 加密主密钥。留空 = 首次启动自动生成 ./data/secret.key（权限 0600）
# 迁移/多实例部署时请显式填写，否则密钥不一致会导致凭据无法解密
PICGO_WEB_SECRET_KEY=
```

> ⚠️ `.env` 必须存在。若缺失，Docker 会把 `./.env` 挂载成**目录**，导致配置写入失败。

### 3. 启动

```bash
docker compose up -d
```

查看状态与日志：

```bash
docker compose ps
docker compose logs -f picgo-web
```

### 4. 拿到管理员密码并登录

首次启动（数据库中还没有任何用户）时会自动创建管理员账号并生成随机密码，
密码同时写入**启动日志**和 **`./data/initial-admin-password.txt`**（权限 `0600`）：

```bash
# 二选一
docker compose logs picgo-web | grep -i "admin password"
cat data/initial-admin-password.txt
```

然后打开浏览器访问 **<http://localhost:8080>**，用管理员账号登录。
该账号带 `MustChangePassword` 标记，**首次登录会被强制要求修改密码**。

### 5. 接下来做什么

1. **配一个存储驱动** —— 进入「存储」页 → 新建配置 → 选择驱动类型（GitHub / S3 / WebDAV …）
   → 填写凭据 → 可点击「测试」验证连通性 → 按需设置魔法路径与文件名模板
2. **传第一张图** —— 进入「上传」页拖拽或粘贴图片，选好目标驱动，观察实时进度
3. **在图库复制外链** —— 进入「图库」页，找到刚上传的图，点复制并选择 Markdown / 直链 / HTML

### 更换首页主题

**首页（`/`）**由「主题」提供，主题就是 `./data/themes/` 下的一个目录：

```
data/themes/<主题ID>/
├── manifest.json     # 元数据 + 配置项声明（含要接管的页面 Pages）
├── index.html        # 首页入口
└── assets/           # 主题自己的 js / css / 图片
```

两种安装方式：

1. **后台 zip 上传** —— 进入「主题」页（`/admin/themes`）→ 上传主题 zip → 安装后点「启用」
2. **直接放目录** —— 把主题目录拷进 `./data/themes/` → 后台点「重新扫描」→ 点「启用」

**切换后立即生效**，刷新页面即可（不需要重启容器）。

> 主题的静态资源必须用 **`/theme-assets/...`** 引用（构建时把 `base` 设成 `/theme-assets/`）。
> `/assets/**` 永远属于内置界面，**不要占用**。
>
> 主题坏掉、被删掉、或 `manifest.json` 不合法时，会自动回退到**内嵌的默认首页主题**，
> 你不会看到白屏；回退会写一条 `theme.error` 操作日志便于排查。

### 切换到 PostgreSQL

用覆盖文件叠加（无需改动 `docker-compose.yml`）：

```bash
# 在 .env 中补上 PG 连接（写法见 .env.example）
docker compose -f docker-compose.yml -f docker-compose.pgsql.yml up -d
```

对应的环境变量：

```dotenv
PICGO_WEB_DB_DRIVER=postgres
PICGO_WEB_PG_HOST=postgres
PICGO_WEB_PG_PORT=5432
PICGO_WEB_PG_USER=picgo
PICGO_WEB_PG_PASSWORD=change-me
PICGO_WEB_PG_DBNAME=picgo_web
PICGO_WEB_PG_SSLMODE=disable
PICGO_WEB_PG_TIMEZONE=Asia/Shanghai
# 也可以直接用完整 DSN（非空时覆盖以上拼装结果）
PICGO_WEB_DB_DSN=
```

> SQLite 与 PostgreSQL 的表结构、字段语义完全一致，切换只需改环境变量；
> 但**数据不会自动迁移**，需要自行导出导入。

### 数据卷与备份

所有持久化状态都在宿主机 `./data` 下：

| 路径 | 内容 |
|---|---|
| `./data/picgo-web.db` | SQLite 数据库（用户 / 配额 / 元数据 / 日志） |
| `./data/picgo/` | picgo `config.json`（存储驱动配置投影）与插件安装目录 `node_modules/` |
| `./data/themes/` | **首页主题**目录。首次启动若为空，会自动从镜像种入内置默认主题（`default/`）；之后新增/替换主题都在这里 |
| `./data/uploads/` | 上传暂存文件（上传成功后按策略清理或保留） |
| `./data/secret.key` | 加密主密钥（`PICGO_WEB_SECRET_KEY` 留空时自动生成） |
| `./data/initial-admin-password.txt` | 首启管理员随机密码（改密后建议删除） |

> ⚠️ **本项目不提供备份 / 恢复功能**（`DECISIONS.md` D82）：没有导出导入界面，
> 也没有定时备份任务。备份完全由运维负责。

**备份做法**：停止容器后整体打包 `./data` 即可 —— 数据库、配置、插件、**你放进来的主题**与密钥都在里面。

> 主题也存放在 `./data/themes/`，因此**不需要单独备份主题**；升级容器镜像也不会覆盖它。

```bash
docker compose stop
tar czf picgo-web-backup-$(date +%F).tar.gz data/
docker compose start
```

> 使用 PostgreSQL 时，数据库不在 `./data` 内，需另行备份（`pg_dump`）。

### 其他部署方式

自行从源码编译二进制、或用 systemd 托管均可行，但**不作为官方支持的部署路径**，
遇到问题请以 Docker Compose 为准。构建命令见下一节。

## 开发与测试（面向开发者）

### 依赖要求

| 依赖 | 版本 | 说明 |
|---|---|---|
| Go | 1.25+ | SQLite 使用纯 Go 实现，**无需 CGO** |
| Node.js | 24+ | 侧车与前端 |
| pnpm | 10+ | 前端与侧车包管理器 |
| Docker | 20.10+ | 仅构建镜像时需要 |

### 目录结构

```
PicGo-Web/
├── server/           # Go 后端（Gin + GORM + SQLite/PgSQL），主进程
├── picgo-agent/      # Node/TS 侧车，持有 picgo-core 实例、插件与上传
├── web/              # React 前端（Vite + Tailwind + Radix + Zustand）—— 内置 SPA
├── themes/           # 默认首页主题的**打包产物**（由 `make theme` 生成，作为可替换的起点）
├── docs/             # 开发文档集（索引见 docs/README.md）
├── deploy/           # Dockerfile 等构建产物
├── docker-compose.yml
├── docker-compose.pgsql.yml
├── Makefile
└── .env.example
```

### 准备 PicGo-Core 本地依赖

侧车需要一份**打过补丁的 picgo-core**，仓库位于与 `PicGo-Web` **同级**的 `PicGo-Core`：

```bash
cd ../PicGo-Core        # 与 PicGo-Web 同级
git checkout PicGo-Web  # 补丁分支（基于上游 dev / v3.0.2）
pnpm install
pnpm build              # 必须构建出 dist/，picgo-agent 通过 file: 依赖它
```

> - `picgo-agent` 以 `"picgo": "file:../../PicGo-Core"` 引用本地源码，因此**必须先 `pnpm build`**
>   （`dist/` 在 `.gitignore` 中，且该仓库没有 `prepare: build` 钩子，**无法使用 git 依赖**）。
> - 该分支上的改动清单与「合并上游时的注意事项」见 **`PicGo-Core/FORK-NOTES.md`**。
> - 该分支的主要改动是让 `upload()` 支持**按次指定图床**（`UploadOptions.uploader`）与
>   **事件归属**（`UploadOptions.contextData`）；不传这些参数时行为与上游一致。

### 开发命令

**最常用：一条命令起全部**

```bash
make dev
```

它并行启动三个进程（各自独立终端日志，Ctrl-C 一次全部退出）：

| 进程 | 地址 | 说明 |
|---|---|---|
| `server` | <http://127.0.0.1:8080> | Go 后端（Gin） |
| `agent` | <http://127.0.0.1:36678> | picgo-agent 侧车（仅 127.0.0.1，带 `X-Agent-Token`） |
| `web` | <http://127.0.0.1:5173> | Vite dev server（`/api` 代理到 8080） |

**分开跑（便于单独调试）**

| 命令 | 作用 |
|---|---|
| `make server` | 只启动 Go 后端（`:8080`） |
| `make agent` | 只启动 Node 侧车（`127.0.0.1:36678`） |
| `make web` | 只启动前端 Vite dev server（代理 `/api` → `:8080`） |

**首次启动要做的两件事**

1. **准备 PicGo-Core**（agent 依赖它，且 `dist/` 被 gitignore，必须先构建）：

   ```bash
   cd ../PicGo-Core        # 与 PicGo-Web 同级
   git checkout PicGo-Web  # 补丁分支（基于上游 dev / v3.0.2）
   pnpm install && pnpm build
   ```

   或直接 `make deps`（会一并安装三端依赖）。

2. **拿管理员初始密码**：首次启动时后端会生成随机密码，出现在两处：

   ```bash
   # 方式一：看后端启动日志（WARN 级）
   #   已创建初始管理员账号 ... password":"xxxxxxxx"

   # 方式二：读文件（权限 0600）
   cat data/initial-admin-password.txt
   ```

   登录邮箱固定为 **`admin@localhost`**；**首次登录会强制修改密码**，然后即可正常使用。

**不装 Node 也能跑通全链路**（仅用于前端联调 / CI）：

```bash
PICGO_WEB_AGENT_MOCK=true make server
# 或写进 .env：PICGO_WEB_AGENT_MOCK=true
```

**前端脱离后端开发**（走内置 mock）：

```bash
cd web && VITE_USE_MOCK=true pnpm dev
```

> 三端都有热重载；后端在 `PICGO_WEB_AGENT_AUTOSTART=false`（默认）时不会自己拉起侧车，
> 正好配合手动启动的 agent。

### 构建与打包

| 命令 | 作用 |
|---|---|
| `make theme` | 导出默认主题到 `themes/default/`（源：`server/internal/theme/embedded`，自包含单文件） |
| `make build` | 构建全部产物：内置 SPA 的 `dist`（同步到 `server/internal/webfs/dist` 供 `go:embed`）+ 侧车产物 + Go 静态二进制（`CGO_ENABLED=0`） |
| `make vendor` | 把打过补丁的 PicGo-Core 打成 `deploy/vendor/picgo-*.tgz`（Docker 构建需要） |
| `make pack` | 产出可发布包：二进制 + 主题 + PicGo-Core tarball |
| `make docker-build` | 构建 Docker 镜像（自动先 `vendor`） |

> **内置 SPA 用 `go:embed` 打进二进制**（图库 / 上传 / 后台 / 登录页等**全部内置**，单二进制、无外部依赖）。
> `make theme` 导出的 `themes/default/` 是给用户一个**可替换的起点**（放一份到 `./data/themes/` 就能改），
> 同时二进制里**也内嵌同一份**作为主题缺失/损坏时的兜底（D94）——因此即使 `data/themes/` 被删空也不会白屏。
>
> **本地验证主题**：`make theme` 后把 `themes/default/` 拷到 `./data/themes/default/`，
> 用 `make server` 起后端访问 <http://localhost:8080> 即可看到效果（`make web` 的 Vite dev server 走的是内置 SPA）。

### 测试与检查

**全仓一次跑完**（串行执行三端检查，失败即停）：

```bash
make check
```

**各端单独执行**：

```bash
# Go 后端
cd server && go vet ./... && go test ./... && CGO_ENABLED=0 go build ./cmd/picgo-web

# Node 侧车
cd picgo-agent && pnpm lint && pnpm typecheck && pnpm test

# 前端
cd web && pnpm lint && pnpm typecheck && pnpm build

# PicGo-Core（补丁仓库）
cd ../PicGo-Core && pnpm lint && pnpm test
```

### 环境变量

**只有「启动引导类」配置走环境变量**，其余全部在后台界面里改（存数据库）。

| 变量 | 默认 | 说明 |
|---|---|---|
| `PICGO_WEB_LISTEN` | `0.0.0.0:8080` | HTTP 监听地址 |
| `PICGO_WEB_DATA_DIR` | `./data` | 数据目录（数据库 / picgo 配置 / **首页主题** / 上传暂存 / 密钥） |
| `PICGO_WEB_DB_DRIVER` | `sqlite` | `sqlite` \| `postgres` |
| `PICGO_WEB_DB_DSN` | 空 | 非空时**完全覆盖**下面的连接参数拼装结果 |
| `PICGO_WEB_SQLITE_PATH` | `<data>/picgo-web.db` | SQLite 文件路径（`driver=sqlite` 时生效） |
| `PICGO_WEB_PG_HOST` | `127.0.0.1` | PostgreSQL 主机 |
| `PICGO_WEB_PG_PORT` | `5432` | PostgreSQL 端口 |
| `PICGO_WEB_PG_USER` | `picgo` | PostgreSQL 用户 |
| `PICGO_WEB_PG_PASSWORD` | 空 | PostgreSQL 密码 |
| `PICGO_WEB_PG_DBNAME` | `picgo_web` | PostgreSQL 库名 |
| `PICGO_WEB_PG_SSLMODE` | `disable` | 生产建议 `require` 以上 |
| `PICGO_WEB_PG_TIMEZONE` | `Asia/Shanghai` | PostgreSQL 时区 |
| `PICGO_WEB_DB_MAX_OPEN_CONNS` | `25` | 连接池上限 |
| `PICGO_WEB_DB_MAX_IDLE_CONNS` | `5` | 空闲连接数 |
| `PICGO_WEB_DB_CONN_MAX_LIFETIME` | `1h` | 连接最长存活 |
| `PICGO_WEB_DB_AUTO_MIGRATE` | `true` | 启动时自动执行 `schemaMigrations`；`false` 时仅告警 |
| `PICGO_WEB_SECRET_KEY` | 空 | 加密主密钥；留空则首启生成 `<data>/secret.key`（0600）。**不进数据库** |
| `PICGO_WEB_AGENT_URL` | `http://127.0.0.1:36678` | 侧车地址 |
| `PICGO_WEB_AGENT_TOKEN` | 空 | 侧车鉴权令牌；留空则首启随机生成 |
| `PICGO_WEB_AGENT_AUTOSTART` | `true` | 是否由 Go 拉起侧车子进程；独立部署时设 `false` |
| `PICGO_WEB_AGENT_MOCK` | `false` | **仅开发**：用假实现替代真实侧车 |
| `PICGO_WEB_LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error`（`log/slog` JSON 输出） |
| `PICGO_WEB_TRUST_PROXY` | `false` | 是否信任反向代理的 `X-Forwarded-*` 头 |
| `PICGO_WEB_ALLOW_PRIVATE_FETCH` | `false` | `POST /uploads/from-url` 是否允许抓取私有网段（防 SSRF，默认拒绝） |

> 完整配置项（上传限制、限流、SMTP、OAuth、日志保留、npm 源等）
> 见 [`docs/DATA-MODEL.md`](./docs/DATA-MODEL.md) 的配置键位表 —— 它们在**后台界面**里改，存数据库、即时生效、无需重启。
>
> 注意：**环境变量名保持 `UPPER_SNAKE_CASE`**，不受「命名规范」影响（`DECISIONS.md` D81.3）。

**数据库切换对照**：

| 场景 | 改法 |
|---|---|
| SQLite（默认） | `PICGO_WEB_DB_DRIVER=sqlite` + `PICGO_WEB_SQLITE_PATH=./data/picgo-web.db` |
| → PostgreSQL | `PICGO_WEB_DB_DRIVER=postgres` + `PICGO_WEB_PG_*` 七项（或直接给 `PICGO_WEB_DB_DSN`） |

### 两个必须知道的编码约定

1. **命名规范（`DECISIONS.md` D81）**：API JSON 字段与数据库表名/列名一律 **PascalCase（大驼峰）**，
   缩写词全大写（`UID` / `URL` / `ID` / `API`）；前端 TS 的**变量与函数仍是 camelCase**，
   只有**类型字段**跟随 API 用 PascalCase。例外：Lsky 兼容层（外部冻结契约，保持 snake_case）、
   环境变量、`settings` 配置键、picgo 侧字段名（`picBed` / `_configName` 等）。
2. **数据库标识符大小写**：Go 侧 GORM 需配置 `NoLowerCase: true`，且**每个模型显式实现 `TableName()`**；
   手写原生 SQL 时**必须给标识符加双引号**（PostgreSQL 会把未加引号的标识符折成小写）。
   细节见 [`docs/DATA-MODEL.md`](./docs/DATA-MODEL.md) §0.2。

## 架构一眼图

```
        ┌──────────────────────────────────────────┐
        │  Browser                                 │
        │  首页 = 当前主题（data/themes/）         │
        │  其余 = 内置 React SPA                   │
        └───────────────────┬──────────────────────┘
                            │  内部 API  /api/web/v1/**
                            │  SSE       /api/web/v1/events
                            ▼
        ┌──────────────────────────────────────────┐
        │  server — Go (Gin)              :8080    │
        │  鉴权 · 用户与配额 · 上传队列 · 元数据   │
        │  操作日志 · 邮件 · 存储配置 · SSE        │
        │  SQLite / PostgreSQL                     │
        │  内置 SPA（go:embed）+ 首页主题托管      │
        └───────────────────┬──────────────────────┘
                            │  HTTP + SSE  (127.0.0.1:36678, X-Agent-Token)
                            ▼
        ┌──────────────────────────────────────────┐
        │  picgo-agent — Node/TS                   │
        │  持有唯一 picgo-core 实例 · 插件安装/更新│
        │  单文件同步上传 · 魔法路径 · 远端删除    │
        └───────────────────┬──────────────────────┘
                            ▼
        ┌──────────────────────────────────────────┐
        │  picgo-core  ──►  图床                   │
        │  GitHub / S3 / WebDAV / 七牛 / OSS / ... │
        │  （存储与图片分发完全由此层负责）        │
        └──────────────────────────────────────────┘
```

**四条边界**：

1. **后端不做存储、不做图片分发** —— 图片字节从不落在本项目的存储里，外链始终指向图床。
2. **必须有一个 Node 进程** —— `picgo-core` 的插件是 npm 包，只能在 Node 运行时里 `require()` 动态加载。
3. **一个批次一个驱动** —— 要同时发往多个图床，由前端拆成多个批次并行请求。
4. **主题只接管它声明的页面** —— 默认只有首页；**认证页与后台永久内置**，主题无法接管（防钓鱼）。
   主题资源走 `/theme-assets/**`，内置界面的资源走 `/assets/**`，两者严格分开。

## 两套 API

服务端同时提供两组 HTTP 接口，**前缀天然隔离，互不干扰**（`DECISIONS.md` D80）：

| 前缀 | 归属 | 用途 | 响应信封 |
|---|---|---|---|
| **`/api/web/v1/**`** | PicGo-Web **内部 API** | 供本项目 Web 前端与 API Token 使用 | `{ Code, Message, Data }`（PascalCase 字段） |
| **`/api/v1/**`** | **Lsky 兼容层** | 供 PicGo 桌面端 / PicList / uPic / ShareX 等第三方客户端当作图床使用 | `{ status, message, data }`（snake_case，**外部冻结契约**） |
| `/healthz` · `/assets/**` | 系统 | 健康检查 · **内置界面**的静态资源 | — |
| `/theme-assets/**` · `/` | **当前主题** | 首页主题的静态资源与入口（`/themes/**` 返回 404） | — |

> 用第三方客户端连过来时，**只需填写裸域名**（如 `https://pic.example.com`），
> 客户端会自行拼出 `/api/v1/upload`。
>
> 完整端点清单见 [`docs/API.md`](./docs/API.md)。

## 文档导航

完整文档索引与阅读顺序见 **[`docs/README.md`](./docs/README.md)**。

| 文档 | 用途 |
|---|---|
| [`docs/DECISIONS.md`](./docs/DECISIONS.md) | **决策记录（最高约束）** —— 改动前必读 |
| [`docs/ARCHITECTURE.md`](./docs/ARCHITECTURE.md) | 架构总览、目录结构、扩展点、安全清单 |
| [`docs/DATA-MODEL.md`](./docs/DATA-MODEL.md) | 表结构唯一真源（21 张表）与配置键位表 |
| [`docs/API.md`](./docs/API.md) | 接口契约唯一真源（REST + SSE + Lsky + agent） |
| [`docs/OPERATIONS.md`](./docs/OPERATIONS.md) | 运行机制：队列、配额、限流、日志、邮件、删除 |
| [`docs/PICGO-INTEGRATION.md`](./docs/PICGO-INTEGRATION.md) | picgo-core 集成、魔法路径、补丁清单 |
| [`docs/DESIGN.md`](./docs/DESIGN.md) | **前端设计唯一真源**：设计 token、路由与页面、组件与交互规范 |
| [`docs/PLAN.md`](./docs/PLAN.md) | 分工、里程碑、验收、风险对策 |

## 常见问题

**为什么必须跑一个 Node 进程？**

`picgo-core` 的插件体系是 npm 包 + `require()` 动态加载，只能在 Node 运行时里工作。
Electron 桌面版和 `picgo` CLI 也都是 Node 宿主，本项目沿用同一模式：
用一个 `picgo-agent` 侧车进程持有唯一的 `PicGo` 实例，Go 后端通过本地 HTTP 消费它的能力。
这也意味着**插件能做的事 = 服务器上能做的事**（见下一条）。

**为什么图片链接是图床的，而不是本项目的？**

这是本项目的核心取舍：后端**不做存储、不做图片分发**。
好处是图片始终在你的图床/CDN 上直连，不经过本站中转，也不消耗本站带宽；
代价是**图片的公开性与可用性由该图床决定**，本项目无法为它加防盗链或私有访问控制。
URL 的形态也完全由图床（以及所选插件的路径配置）决定。

**插件能做什么？有什么风险？**

等同于**在服务器上执行任意代码**。PicGo 插件是普通 npm 包，
安装后会被 `picgo-agent` 直接加载并调用，能读写文件、发起网络请求、访问配置里的全部凭据。
因此：**插件安装仅限管理员**，每次安装/卸载/更新都会写入操作日志。
请只安装来源可信的插件；生产环境建议先在测试环境验证。

**上传很慢，是不是坏了？**

默认 `upload.concurrency = 1`，上传是**严格串行**的 —— 一个文件跑完才开始下一个
（`DECISIONS.md` D35）。队列进度是「已完成文件数 / 总文件数」，
所以你会看到数字一个个增长，而不是整体百分比平滑推进。
想要更高吞吐，管理员可在后台「系统设置 → 上传」调整并发度；
调到 `> 1` 时侧车会走 PicGo-Core 补丁的「按次指定图床」路径（见 `PicGo-Core/FORK-NOTES.md`）。

**删除了图片，为什么图床上的文件还在？**

`picgo-core` 本身**没有删除远端文件的 API**，只能依赖插件实现的事实约定事件 `remove`。
所以删除时：

- 该驱动的插件**实现了 `remove`** → 远端文件被真正删除；
- 该驱动的插件**没实现** → 只删除本地记录，界面会标记「该驱动不支持远端删除」。

无论哪种情况，该次删除都会写一条操作日志（含远端是否删除成功）。

**能不能用 PicGo 桌面端 / PicList 直接连本站？**

可以。本站实现了 Lsky Pro v1 契约（`/api/v1/tokens`、`/api/v1/upload`、`/api/v1/images`、
`/api/v1/albums` 等），可直接配合生态里的兰空插件使用
（如 `picgo-plugin-lankong`、`picgo-plugin-lskypro`、`picgo-plugin-lsky-uploader`、
`picgo-plugin-lskypro-own`）。

配置方式：在插件里把**服务地址填成裸域名**（例如 `https://pic.example.com`，
**不要**带 `/api/v1` 后缀，也不要以 `/` 结尾），再填邮箱与密码换取的 Token 即可。

> 注意：这一层是**外部冻结契约**，它的路径与字段名（snake_case）**不会**跟随本项目的命名规范变化。

**怎么换首页？**

首页（`/`）由「主题」提供，主题就是 `./data/themes/` 下的一个目录：

```
data/themes/<主题ID>/
├── manifest.json     # 元数据 + 配置项声明（含要接管的页面 Pages）
├── index.html        # 首页入口
└── assets/           # 主题自己的 js / css / 图片
```

两种安装方式：**后台「主题」页上传 zip**，或**把目录拷进 `./data/themes/` 后点「重新扫描」**；
然后点「启用」。**切换后立即生效**，刷新页面即可。

主题自己的 CSS/JS 必须用 `/theme-assets/...` 引用（构建时把 `base` 设成 `/theme-assets/`）；
`/assets/**` 是内置界面的资源目录，主题不要占用，否则会覆盖后台与登录页的资源。

**换了主题会影响图库 / 后台吗？**

不会。**默认只有首页会变**。图库、上传、相册、任务、日志、设置、后台、登录页全部是内置界面，
与主题无关。

主题可以在 `manifest.json` 里用 `Pages` 声明接管更多页面（例如 `["/", "/gallery"]`），
但**认证页与后台是永久保留的**，声明了也不会生效（见下一条）。

**主题能改登录页吗？**

**不能**，这是刻意的安全设计。

主题是一段第三方前端代码。如果允许它接管 `/login`，它就能伪造一个登录框，把你的密码发到它自己的服务器。
受害的是**普通用户**（他们从未选择过主题），而装主题的却是管理员。因此下列路径由系统
**内置且永久保留**，主题在 `manifest.json` 里声明接管也会被校验拒绝（并写一条 `theme.error` 日志）：

- `/login`、`/first-login`、`/forgot-password`、`/reset-password`、`/logout`
- `/admin/**`

**主题坏了 / 被删了会不会白屏？**

不会。二进制里内嵌了一份默认首页主题作为兜底：

- 首次启动时若 `./data/themes/` 为空，会自动把这份默认主题种进去；
- 运行中若当前主题目录缺失、`manifest.json` 损坏、或 `Pages` 非法，会自动回退到内嵌默认主题；
- 回退会写一条 `theme.error` 操作日志（在「日志」页按类型筛 `theme.error` 即可看到）。

**主题安全吗？**

主题等同于**在服务器上运行任意前端代码** —— 它可以读写当前登录用户能访问的一切数据，
并把这些数据发往任意外部地址。因此：

- **仅管理员**可以安装 / 启用 / 卸载主题，所有主题操作都会写入操作日志；
- 请只安装**来源可信**的主题；
- 好消息是影响面被限制在它声明的 `Pages` 内：**碰不到登录页与后台**（见上文「主题能改登录页吗」）。

**为什么数据库表名是大写的？**

命名规范要求 API 字段与数据库表名/列名使用 **PascalCase**（`DECISIONS.md` D81），
所以表是 `Users` / `StorageConfigs` / `OperationLogs`，列是 `UID` / `UserUID` / `CreatedAt`。

需要注意的副作用：**PostgreSQL 会把未加引号的标识符折叠成小写**。
应用层由 GORM 生成的 SQL 始终带引号，因此不受影响；
但你若手写 SQL（迁移脚本、DBA 临时查询）就必须自己加双引号：

```sql
-- ✅ 正确
SELECT "UID", "Email" FROM "Users" WHERE "Status" = 'active';

-- ❌ 会报 relation "users" does not exist
SELECT uid, email FROM Users WHERE status = 'active';
```

SQLite 对 ASCII 大小写不敏感，两种写法都能跑，所以这个问题**只在 PostgreSQL 下暴露**。

**为什么修改了「新建用户默认配额」，老用户的配额没变？**

这是刻意的：`user.defaultCapacityBytes` 只决定**此后新建用户**的初始配额，
不会回溯修改已有用户（否则会意外改变现有用户的上限）。要调整已有用户，请在用户管理页逐个修改。

**上传的图片会被去重吗？**

不会。每一次上传都是真实上传。内容分发与 URL 形态由图床（插件）负责，
很多驱动会用「年月日 + 随机名」生成路径，去重反而会破坏图床自身的分发规则。

## 许可证与致谢

本项目以 **MIT License** 发布，见 [LICENSE](./LICENSE)。

致谢（以下项目为本项目提供了能力基础或设计参考，本项目与它们均无隶属关系）：

| 项目 | 说明 |
|---|---|
| [PicGo](https://github.com/Molunerfinn/PicGo) | 桌面端实现。本项目的**运维形态、插件事件约定**（如 `remove` 事件）与魔法文件名实现思路均参考其主进程代码 |
| [PicGo-Core](https://github.com/PicGo/PicGo-Core) | **核心依赖**。本项目通过 Node 侧车宿主它，上传、多配置图床、插件安装与配置表单 schema 全部由它提供；本项目在其 `PicGo-Web` 分支上维护了一小组增量改动 |
| [lsky-pro（兰空图床）](https://github.com/lsky-org/lsky-pro) | 功能骨架参考：用户/配额/相册/上传限制的组织方式；并提供 **Lsky API v1 兼容契约**的来源 |
| [skyImage](https://github.com/nxtcorex/skyImage) | 工程实践参考：Go + React 的分层与安装向导、Lsky 兼容层的落地方式、前后端技术栈选型 |

三份不适用声明：

- 本项目**不**包含任何图片存储或分发实现，这些功能由 `picgo-core` 与其插件提供；
- 本项目**不**包含商业化模块（商城、支付、兑换码、工单、会员），参考项目中相关设计的结论已明确排除；
- 参考项目的代码未被复制进本项目，仅作为设计与取舍的对照。

---

## 与决策的偏差

### 本轮已同步（主题化范围 + 背景图）

本轮的主题化讨论经历过两次范围调整（先「整个前端主题化」→ 收敛为「只做首页」→ 最终定为「**页面级可选覆盖层，主题自行注册接管页面**」），
并以「**预留接管范围**」收尾。本文档已按最终结论改写：

| # | 事项 | 最终结论 |
|---|---|---|
| 1 | **前端是否内置** | ✅ **内置**：内置 SPA 用 `go:embed web/dist` 打进二进制（单二进制、无外部依赖）。主题只提供**它声明接管的页面** |
| 2 | **主题的定位与范围** | ✅ **主题 = 可选的页面覆盖层**，在 `manifest.json` 的 `Pages` 里**自行注册**要接管的业务页面（`/`、`/upload`、`/gallery`…）；未注册的走内置 SPA。**默认主题只注册 `/`**。分发逻辑一次写通用，**后续加页面不改 Go 代码**（D94 / D94.2 / D99.1） |
| 3 | **认证页与后台** | ✅ **永久内置、不可接管**：`/login`、`/first-login`、`/forgot-password`、`/reset-password`、`/logout`、`/admin/**`。主题声明也无效（校验拒绝 + 写 `theme.error`）。理由：防凭据钓鱼（D94.2） |
| 4 | **资源前缀严格分开** | ✅ 主题资源走 **`/theme-assets/**`**（构建时 `base: '/theme-assets/'`）；**`/assets/**` 永远属于内置 SPA**；`/themes/**` 返回 **404**（D99.2） |
| 5 | **主题配置存放** | ✅ 独立表 **`ThemeConfigs`**（每行一键，含 `UpdatedBy` 可审计）；只有 `theme.active` 在 `SystemSettings`（D95） |
| 6 | **主题兜底** | ✅ 二进制内嵌默认主题；首启若 `data/themes/` 为空则 seed；运行中主题缺失/损坏/`Pages` 非法 → 回退内嵌默认主题，**永不白屏**（D94.4） |
| 7 | **背景图** | ✅ **单一 URL，不做任何判断**（D97，取代原 D85 的「三种模式 + 横竖定向 + ACG 后端缓存」）。本文档旧的「背景图支持纯色 / 固定图片 / 随机二次元图」描述**已删除** |

> 与上述结论相关的旧描述（「前端不内置」「主题接管整个前端」「认证页由内置主题渲染」
> 「主题目录带 `dist/` 子目录」「`/assets/**` 指向当前主题」「`site.background.*`」「`acgCache`」
> 「背景图三种模式」）在本文档中**均已不存在**。

### 上一轮已同步项（保留作记录）

更早一轮重写时登记的 7 条跨文档残留项，**已由文档负责人全部同步到位**：

| # | 事项 | 结果 |
|---|---|---|
| 1 | 内部 API 前缀统一为 `/api/web/v1/**`（Lsky 独占 `/api/v1/**`） | ✅ `DECISIONS.md` D80、`DATA-MODEL.md`、`API.md`、`ARCHITECTURE.md`、`PLAN.md`、`OPERATIONS.md` 全部一致 |
| 2 | 内部相册路径回到 `/api/web/v1/albums`（取消「让位」） | ✅ 已同步（`DATA-MODEL.md` §4.3 亦已改） |
| 3 | 全部表名/列名/API 字段改为 PascalCase | ✅ `DECISIONS.md`（含 D78 表清单、D36/D38 示意、D65 映射表、D16、D45~D47 引用）与全部文档一致 |
| 4 | OAuth 回调地址示例 | ✅ 已改为 `/api/web/v1/auth/oauth/github/callback` |
| 5 | D77.2「路径带 `/api/v1`」 | ✅ 已改为 `/api/web/v1` |
| 6 | PicGo-Core 补丁状态 | ✅ `DECISIONS.md` §十 已改为「**已落补丁（已实现并验证，提交 `6419c2f`）**」，并附实测证据 |
| 7 | 「共享前缀 + 相册让位」的历史描述 | ✅ 全部文档仅作为**历史记录**保留，规范描述已改为「两个前缀天然隔离」 |

### 本文档自身的假设（无冲突，仅备查）

| # | 事项 | 说明 |
|---|---|---|
| 1 | 镜像仓库与镜像名 | `DECISIONS.md` D76 只规定「提供 `docker-compose.yml`」，未指定镜像名；本文档暂用 `ghcr.io/yeqingky/picgo-web`，正式发布时若改用其他仓库需同步快速开始一节的下载地址与 compose 里的 `image` |
| 2 | PG 覆盖文件名与服务名 | 本文档用 `docker-compose.pgsql.yml` 与服务名 `postgres`，与 D76 一致 |

### 待其他文档同步（不阻塞，已登记）

以下是本文档定稿时发现的、位于**其他文档**的未同步项。本文档不擅自修改它们，仅登记以便统一：

| # | 位置 | 现状 | 按决策应为 |
|---|---|---|---|
| 1 | `docs/DATA-MODEL.md` §7.4 | 缺 `category=theme` 的五个上限键（已核实：全文无 `theme.max*`） | **D96**：补 `theme.maxPackageBytes` / `theme.maxExtractBytes` / `theme.maxFileBytes` / `theme.maxFiles` / `theme.maxManifestBytes`（本文档不列这些键，仅登记） |
| 2 | `docs/API.md` §10 的 `POST /themes/install` | 校验表仍写「≤ 50 MiB、解压 ≤ 200 MiB、≤ 5000 文件」，且只有 7 条 | **D96**：对齐为 64 MiB / 512 MiB 总 / 128 MiB 单文件 / 10000 文件，并补上**拒绝 symlink entry** 与**强制权限位**两条（D96 共 9 条） |
| 3 | `docs/API.md` §10 的 `GET /themes` 示例 | 字段写 `Tags` / `Repo` / `SettingCount` | **D98**：manifest 字段为 `URL`（**无 `Tags`**）；条目数出自 `Configuration.Items`，建议把 `SettingCount` 改名为 `ConfigItemCount` 以免歧义 |

> `docs/DESIGN.md` 曾列入本表（§6 旧背景图三模式），已由对应负责人同步完毕（现为单一 `BackgroundURL`，
> 并含 `/admin/themes` 主题管理页、`Pages` 接管范围说明），故从表中移除。

### 本文档有意留白的内容

- **不使用 systemd / 裸机二进制作为官方部署路径**：D76 明确只提供 docker-compose。
- **不写数据库表结构**：见 [`docs/DATA-MODEL.md`](./docs/DATA-MODEL.md)（唯一真源）。
- **不写接口清单**：见 [`docs/API.md`](./docs/API.md)。
- **不写内部机制细节**（队列、配额、限流、日志保留）：见 [`docs/OPERATIONS.md`](./docs/OPERATIONS.md)。
