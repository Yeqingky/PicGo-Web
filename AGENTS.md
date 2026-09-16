# AGENTS.md — PicGo-Web

> 面向开发者与 AI 的项目级规范。**动手前先读本文件 + `docs/README.md`（文档索引）。**
> 本文件优先级高于全局约定；与 `docs/DECISIONS.md` 冲突时以 `docs/DECISIONS.md` 为准。

---

## 1. 仓库职责

PicGo-Web 把桌面端 [PicGo](https://github.com/Molunerfinn/PicGo) 的能力搬到服务器：
后端负责**账号、配额、队列、元数据与审计**，而**图片的存储与分发完全由图床（PicGo 内核及其插件）承担**。

| 不做 | 原因 |
|---|---|
| ❌ 后端存储图片 | 交给 picgo-core 的驱动（D2/D59） |
| ❌ 图片分发 / 缩略图 / 水印 | 同上；URL 形态由图床决定（D66/D84） |
| ❌ 用户组 / 角色组 | 定位是自用 / 小团队，只需「多用户 + 存储配额」（D53） |
| ❌ 标签 / 公开画廊 / 分享页 / 相册 | 整理靠「重命名 + 筛选」（D55/D89；相册已于 D101 移除） |
| ❌ 自助注册 / 套餐 / 支付 / 兑换码 / 工单 | 无商业化模块（D25/D60/D88/D90） |
| ❌ 内容去重 | URL 形态由图床决定，去重会破坏其分发规则（D66） |
| ❌ 备份 / 恢复功能 | 由运维备份 `data/` 目录（D82） |

---

## 2. 三端结构与代码导航

```
PicGo-Web/
├── docs/                 文档集（见 docs/README.md）
├── server/               Go 后端（主开发语言）
├── picgo-agent/          Node 侧车（持有 picgo-core 实例）
├── web/                  React 前端（内置 SPA + 默认首页主题源码）
├── themes/               默认主题的打包产物（构建生成，不提交）
├── Dockerfile            生产镜像（多阶段构建，位于根目录：web → agent → Go → alpine 运行时）
├── docker-compose.yml    面向用户的唯一官方部署方式（D76）
├── docker-compose.pgsql.yml  PostgreSQL 覆盖文件
├── docker-compose-dev.yml    开发环境（**直接用基础镜像跑源码，无 dev Dockerfile**）
└── Makefile              统一入口（make help）
```

### 2.1 `server/`（Go）

```
server/
├── cmd/picgo-web/main.go        入口：config → logger → dirs → 主密钥 → db → migrate → settings → HTTP
└── internal/
    ├── config/                  启动引导配置（env/.env）+ defaults.go（全部业务配置键的代码默认值）
    ├── logger/                  log/slog JSON 日志 + context 透传（request id / user uid）
    ├── database/                双方言连接 + 命名策略 + 版本化迁移
    │   ├── open.go              GORM 打开 + 连接池 + NamingStrategy{NoLowerCase: true}
    │   ├── dsn.go               DSN 拼装（sqlite / postgres）+ 日志脱敏
    │   ├── schema_meta.go       SchemaMeta 读写
    │   ├── migrations.go        迁移表定义 + 升序执行（单事务）
    │   └── migrate_v1.go        v1：建 20 张表 + 全部索引
    ├── model/                   20 张表的 GORM 模型（按域分文件，**每个都实现 TableName()**）
    ├── repository/              纯数据访问，不写业务逻辑
    ├── crypto/                  AES-256-GCM + 主密钥加载/生成
    ├── id/                      ULID + 前缀（up_ / st_ / job_ / log_ …）
    ├── settings/                运行时配置服务（三级兜底 + 加密 + onChanged）
    ├── response/                统一信封 {Code, Message, Data} + 错误码
    ├── middleware/              RequestID / Recovery / AccessLog / SecurityHeaders
    └── server/                  HTTP 装配 + 优雅关闭
```

**分层铁律**：`handler → service → repository`。**handler 不得出现 `*gorm.DB`**（D77.2）。

### 2.2 `picgo-agent/`（Node/TS）

待 W4 实现。职责：持有**单个** `PicGo` 实例、封装 `uploaderConfig` 与 `pluginHandler`、
提供单文件同步上传与远端删除端点、SSE 推送任务日志。契约见 `docs/API.md` §13。

### 2.3 `web/`（React）

内置 SPA（W7/W8 已完成），经 `go:embed` 嵌入二进制。设计规范见 `docs/DESIGN.md`（唯一真源）。

- 路由与导航：`src/router.tsx` + `src/lib/navigation.ts`（新增页面只改这两处）
- **`/` 不归 SPA**：`/` 由主题渲染（D94/D102），控制台概览页固定 `/overview`（`src/features/overview/`，D103）
- 首页落地页属于主题，源码在独立仓库 `Github-me:YeqingKy/PicGo-Web-Theme`（D100）; 内嵌默认主题的后台预览图为 `server/internal/theme/embedded/screenshot.png`, manifest 的 `Preview` 使用该文件; 旧磁盘主题缺图时必须回退内嵌预览图
- 管理后台表单的 "未保存" 状态必须比较当前值与服务端初始值; 用户恢复原值后应立即清除 dirty 标记.
- 侧栏底部必须保留存储空间、服务器状态圆点和当前版本号; SSE 断连不得在内容区顶部插入提示条.
- GitHub 新版本提示仅在最新正式 Release 高于当前版本时显示; 无 Release、当前版本更高、请求失败或版本格式无效时不显示. 版本格式为 `x.x.x` 或 `x.x.x-fixN`, `fixN` 表示错误修复版本.
- 任务详情的长 JSON 展开按钮只显示 i18n 文案「展开全部 / Show all」, 不显示总行数.
- 图库页面的内置 UI 文案必须在 `zh-CN` 与 `en` 中都有翻译; 用户文件名、存储名称等服务端数据值保持原样.
- 图库管理员多选操作框必须与 "我的图片 / 全部图片" Tab 同行, 并在右侧对齐; 该行高度固定, 只显示复制/删除图标且不提供清除选择按钮.
- 站点设置页不显示首页或背景图配置引导提示; 相关配置仍由主题设置管理.
- 顶栏右侧控件顺序固定为命令面板、语言切换、主题切换、用户菜单; 语言选择使用 `src/i18n` 的 locale API 并持久化到 `localStorage`.
- 命令面板打开时不默认高亮第一项; 仅键盘或鼠标选择后显示选中态.
- 命令面板搜索框与关闭按钮不显示圆角焦点框.

---

## 3. 数据库约定（**最重要**）

### 3.1 命名：全 PascalCase（D81）

| 层 | 命名 | 示例 |
|---|---|---|
| 表名 | PascalCase 复数 | `Users` / `StorageConfigs` / `OperationLogs` |
| 列名 | PascalCase | `UID` / `UserUID` / `CapacityBytes` / `CreatedAt` |
| API JSON 字段 | PascalCase | `AccessToken` / `JobUID` / `StorageUID` |
| 缩写词 | **全大写** | `UID` `URL` `ID` `API` `HTTP` |
| Go 局部变量/函数 | camelCase | `func handleUpload()` |
| TS 变量/函数/store | camelCase | `const accessToken = res.AccessToken` |
| TS 类型字段 | PascalCase | `interface Upload { UID: string }` |

**硬性例外（绝不能改）**：
- **Lsky 兼容层**（`/api/v1/**`）保持 snake_case 与 `{status, message, data}` 信封（外部冻结契约）
- 环境变量保持 `UPPER_SNAKE_CASE`（`PICGO_WEB_LISTEN`）
- `settings` 配置键保持 `dot.lowerCamel`（`site.name` / `upload.rateLimit.perHour`）
- picgo 侧字段名（`picBed` / `_configName` / IImgInfo 的 `fileName` / 驱动的 `repo`）一律原样
- 操作日志 `Type` 取值是字符串枚举（`upload` / `mail.send` / `theme.install`）

### 3.2 GORM 必做配置（D81.4）

```go
gorm.Open(dialector, &gorm.Config{
    NamingStrategy: schema.NamingStrategy{
        NoLowerCase: true,   // ← 漏了这行，User 会被折成 users 表、UserUID 折成 user_uid
    },
})
```

**每个模型显式实现 `TableName()`**，不依赖词形变化库。

### 3.3 PostgreSQL 引号坑（D81.4）

PgSQL 会把**未加引号**的标识符折成小写。GORM 生成的 SQL 总带引号，应用层无影响；
但**手写原生 SQL / 迁移脚本必须自己加双引号**，否则报 `relation "uploads" does not exist`：

```go
tx.Exec(`CREATE INDEX IF NOT EXISTS idx_x ON "Uploads" ("UserUID", "CreatedAt" DESC)`)
```

迁移里有 `createIndex` 助手统一处理，**新增索引请用组合索引表（`migrate_vN.go`）而不是手写**。

### 3.2.1 ⚠️ 手写 SQL 片段的列名也必须是 PascalCase（**踩过**）

GORM 的 `Where("...")` / `Select("...")` / `Order("...")` / `Group("...")` 参数是**原样拼进 SQL** 的，
GORM **不会**帮你做命名转换。因此必须写模型字段名：

```go
// ✅ 正确
db.Where("UserUID = ?", uid)
db.Select("(CreatedAt + ?) / 86400 * 86400 AS Day", offset).Group("Day")
db.Order("CreatedAt DESC")

// ❌ 错误：SQLite 大小写不敏感所以**本地测不出来**，PostgreSQL 直接报
//    column "user_uid" does not exist
db.Where("user_uid = ?", uid)
db.Order("created_at DESC")
```

**为什么危险**：SQLite 对 ASCII 大小写不敏感，`user_uid` 能匹配到 `UserUID` 列 →
本地与 CI 全绿；一到 PostgreSQL 就炸。**新增任何手写 SQL 片段后，务必在 PgSQL 上跑一次。**

### 3.2.2 ⚠️ 跨进程传递的路径必须是绝对路径（**踩过**）

Go 与 picgo-agent 是**两个进程、不同 cwd**：
Go 的 cwd 是启动目录，agent 的 cwd 是 `picgo-agent/`（它要据此找 `node_modules`）。

```go
// ✅ 正确：传给 agent 前绝对化
root := cfg.UploadsDir()
if abs, err := filepath.Abs(root); err == nil { root = abs }

// ❌ 错误：agent 会解析成 picgo-agent/data/uploads/... → 「文件不存在」
uploadPath := filepath.Join(cfg.UploadsDir(), name)  // "./data/uploads/x.png"
```

**规则**：凡是写入 `Uploads.FilePath` / `UploadResults.FilePath` / 传给 agent `UploadRequest.Path`
的路径，**一律绝对路径**。本地文件路径不经 API 暴露，绝对化不影响安全性。

### 3.4 迁移（D16/D77）

- **只追加，不修改已发布条目**：`schemaMigrations` 末尾加一条 + `SchemaVersion` +1
- 每条迁移在**单个事务**内执行，失败回滚
- 已发布迁移的 `Up` 函数与语义**永不修改**
- 幂等：连跑两次 `SchemaMeta.Version` 不变（有测试覆盖）

### 3.5 其他表设计约定

| 约定 | 说明 |
|---|---|
| 时间 | 一律 `int64` Unix 秒（用 `model.Now()`），**禁止 `time.Time` 列** |
| JSON 语义的列 | `string` + `gorm:"type:text"`，由 service 层显式 marshal/unmarshal |
| 枚举 | `VARCHAR` 存字符串，**不建 DB enum**（新增取值不需 DDL） |
| 外键 | **只建索引，不建 `FOREIGN KEY` 约束**（两方言差异大且阻碍归档） |
| 对外标识 | 一律 `UID`（`id.WithPrefix()`），**不暴露自增 ID** |
| 新配置项 | 写 `config/defaults.go` + KV 表，**不需要迁移** |
| 新内容域 | **新建表**，不要塞进现有表的 JSON 列（D78） |
| 只增不删 | 字段/表只增不改；废弃字段保留并标 `Deprecated` |

---

## 4. API 约定

### 4.1 两套 API，前缀隔离（D80）

| 前缀 | 归属 | 信封 |
|---|---|---|
| **`/api/web/v1/**`** | PicGo-Web 内部 API | `{Code, Message, Data}`（PascalCase） |
| **`/api/v1/**`** | **Lsky v1 兼容层**（外部冻结契约） | `{status, message, data}`（snake_case） |
| `/healthz` | 健康检查 | **无信封、字段小写**（供容器探针） |

**Lsky 保留集**（内部 API 永不占用）：`/api/v1/{tokens,profile,strategies,upload,images,albums}`。

### 4.2 错误码

`40001` 参数校验 · `40101` 凭据错误 · `40102` 未登录 · `40103` 令牌过期 · `40104` 账号禁用 ·
`40301` **权限不足** · `40302` **配额不足** · `40401` 不存在 · `40901` 冲突 · `42901` 限流 ·
`50001` 内部错误 · `50002` agent 不可用 · `50003` 上传失败 · `50004` 插件失败 · `50005` 主题失败

⚠️ **`40301` 与 `40302` 不可混用**（D20）。

### 4.3 分页

`Data` = `{Items, Total, Page, PageSize}`；`Items` 为 nil 时返回 `[]` 而非 `null`。

操作日志的 `OperationLogs.Type`、`GET /logs/types` 的 `Type` 与筛选参数必须使用同一稳定字符串值。
不要在后端或数据库保存/返回本地化类型名称；管理后台由 `web/src/i18n` 按当前语言映射展示文案。

---

## 5. 配置分层（D18）

```
第 1 层  环境变量 / .env    → internal/config（只读，启动引导类）
第 2 层  SystemSettings 表   → internal/settings（DB 为真相源，可在后台改）
第 3 层  代码默认值          → internal/config/defaults.go
```

- 读取统一走 `SettingsService.GetInt/GetString/GetBool/GetStringSlice/GetJSON`
- 写入用 `Set(key, value, by)`；**未注册的键会被拒绝**（防脏写）
- 写入触发 `OnChanged` 回调 → 供推送 agent、刷新限流器等
- 敏感值（`TypeSecret`）AES-256-GCM 加密入库，**对外一律返回掩码**，内部用 `GetRaw()`

**主密钥绝不进数据库**（D19）：来源 `PICGO_WEB_SECRET_KEY` → `<dataDir>/secret.key`（自动生成，0600）。

---

## 6. 主题系统（D94–D100）

**模型**：内置 SPA（`go:embed web/dist`）提供全部页面的默认实现；
**主题是可选的页面覆盖层**，在 `manifest.json` 的 `Pages` 里**自行注册**要接管的页面。

**默认主题源码在独立仓库 `Github-me:YeqingKy/PicGo-Web-Theme`**（D100）：内嵌兜底副本（`server/internal/theme/embedded/`）用 `make theme-sync` 从那里同步；seed 优先级：`PICGO_WEB_THEME_SEED` 目录 → `theme.defaultGitURL`（Git 拉取，失败回退）→ 内嵌副本。安装通道：`POST /themes/install` 按 `Content-Type` 分发（multipart = zip；JSON = https Git 地址，仅 https、不携带凭据、浅克隆）。默认主题 Hero 首屏高度使用 `100svh`，背景图绝对定位覆盖 Hero 并保持 `object-fit: cover`，确保不同视口自适应铺满；页内锚点跳转使用平滑滚动，并尊重 `prefers-reduced-motion`。

| 可注册 | `/`（首页）、`/overview`（概览，内置 SPA 的登录后落地页）、`/upload`、`/gallery`、`/jobs`、`/settings` |
| **内置 SPA 不占用 `/`** | 概览页固定 `/overview`（D102）；侧边栏「概览」、登录后默认跳转、403/404 返回按钮一律指向 `/overview`，**不得**写 `/` |
| 概览页数据 | `GET /api/web/v1/system/stats`（复用，不新增端点）；管理员区块看 `Role === 'admin'`，数据侧由后端裁剪（D103） |
| 上传队列 SSE | `upload.progress` / `upload.finished` / `upload.failed` 由 `AppShell` 在建立共享连接时绑定；SSE 重连前须通过 `/auth/me` 触发过期 access token 刷新；不得遗漏初始化, 否则队列会永久显示 "上传中". |
| 日志权限 | `/admin/logs` 与 `/api/web/v1/logs/**` 仅管理员可见；普通用户不显示日志入口. |
|---|---|
| **不可注册（永久保留，代码硬编码）** | 全部认证页 + `/admin/**` —— 安全底线，manifest 声明无效 |
| 资源前缀 | 主题 `/theme-assets/**`；内置 SPA `/assets/**`（**必须分离**） |
| 配置存储 | **独立表 `ThemeConfigs`**（每行一键）；`theme.active` 在 `SystemSettings` |
| 兜底 | 二进制内嵌默认主题；缺失/损坏 → 回退，**永不白屏** |

主题配置是**两套命名**（`string`/`text`/`number`/`switch`/`select`/`json`），
与插件 schema（`input`/`password`/`list`/`checkbox`/`confirm`/`editor`）不同，
前端需要一个**适配层**规约到同一组内部类型。

---

## 7. 开发与验证命令

> ⚠️ **本项目只提供 docker compose**（D76）。开发也不例外 ——
> 宿主**不需要**装 Go / Node / pnpm，全部在容器里。
> 不要建议「宿主直跑 `go run`」「`pnpm dev`」「`make build`」这类方式（相关目标已移除）。

```bash
make help              # 查看全部命令

# ---- 开发环境（三容器 + 热重载）----
make dev               # 起（前台，Ctrl-C 停止）
make dev-up            # 起（后台）→ web:5173 / server:8080 / agent:36678
make dev-down          # 停（保留数据）
make dev-reset         # 停并删除数据（需确认；不可恢复）
make dev-logs-server   # 看后端日志（**管理员初始密码在这里**）
make dev-shell-server  # 进后端容器（跑 go test / make 等）

# 开发环境**不构建镜像**：直接用基础镜像（node:24-alpine / golang:1.25-alpine）
# 跑源码，依赖在容器启动时自动安装（pnpm install），改完 package.json 重启即可；
# node_modules / Go 模块与构建缓存 / corepack 缓存全在宿主目录 ./data-dev/ 下
# （全部 bind mount，不用存储卷；dev 容器以 root 运行，无需 chown）。

# ---- 门禁（都在容器内跑）----
make check             # 三端：server + agent + web
make check-server      # go vet ./... && go test ./... && CGO_ENABLED=0 go build
make check-agent       # pnpm lint && pnpm typecheck && pnpm test
make check-web         # pnpm lint && pnpm typecheck && pnpm build

# ---- 端到端 ----
make e2e               # 全部（鉴权 + 主题 + Lsky）
make e2e-auth          # 鉴权与用户
make e2e-theme         # 主题系统（73 项）
make e2e-lsky          # Lsky 兼容层（20 项，含信封一致性）

# ---- 生产 ----
make up / up-pgsql / up-build / down / logs
```

**数据目录**：开发用 `./data-dev/`（生产 `./data/`），两者隔离。
`node_modules` 与 Go 模块缓存放 `./data-dev/` 下的**宿主子目录**（bind mount，
**不用存储卷**；容器内装、容器内用，避免宿主 Linux/WSL 与容器 Alpine 的平台差异）。
dev 容器以 root 运行，无需 chown；`make dev-up` 会自动 mkdir 全部挂载目录。

**改完代码必须跑**（通过容器，不要用宿主 Go）：

```bash
make check-server
```

### picgo-core 的来源

侧车依赖 **`@yeqingky/picgo-core`**（npm 包，PicGo-Core 的 `PicGo-Web` fork）：

- **不再需要**本地 `PicGo-Core` 源码、**不再需要** `make vendor` 打 tarball
- 升级：改 `picgo-agent/package.json` 的版本 → 重启 dev 容器（`make dev-up`）自动装新版本
- 生产镜像构建时**会断言补丁存在**（dist 里必须出现 `contextData`），
  缺失即构建失败 —— 防「装了无补丁版本、上传静默用错图床」这类极难排查的问题
- `picgo-agent/src/**` 里的 import 一律写 `from '@yeqingky/picgo-core'`（**不是** `from 'picgo'`）

### 已实现的测试（W2）

| 测试 | 覆盖的验收点 |
|---|---|
| `internal/database/dsn_test.go` | DSN 拼装、密码脱敏、DBDSN 覆盖、不支持驱动 |
| `internal/database/migrate_test.go` | **20 张表全建**、**命名 PascalCase 回归**、列名 PascalCase、迁移幂等、迁移表有序、12 个组合索引存在 |
| `internal/crypto/aes_test.go` | 加解密往返、nonce 随机化、错密钥/篡改失败、密钥生成与复用、文件权限 0600 |
| `internal/id/ulid_test.go` | 各前缀、2 万次并发唯一性、同毫秒单调递增 |
| `internal/settings/service_test.go` | **三级兜底**、Source 追踪、secret 加密与掩码、未注册键拒绝、onChanged、重启持久化、类型强转 |

---

## 7.5 Lsky v1 兼容层（`/api/v1/**`）

**目的**：让 PicGo 桌面端 / PicList / uPic / ShareX **直接把本站当图床**。

| 项 | 值 |
|---|---|
| 前缀 | **`/api/v1/**`**（与内部 `/api/web/v1` 隔离，D80） |
| 信封 | **`{status: bool, message, data}`** |
| 字段命名 | **snake_case**（外部冻结契约，**不受 D81 约束**） |
| 时间格式 | **字符串 `Y-m-d H:i:s`**（内部是 Unix 秒） |
| 分页 | **Laravel 形状**（`data.data` 双重 data + `current_page`/`last_page`/…） |
| 鉴权 | **只认 API Token**（`pcw_...`）；不认 Cookie / 内部 JWT |
| 令牌签发 | `POST /tokens` 每次签发**新的**并**吊销同名旧令牌**（客户端会反复登录） |

### 三条血泪教训（代码里都有注释，改动前先读）

1. **信封必须隔离到「未匹配路由」级别**：`/api/v1/**` 的 404 也必须是 Lsky 信封，
   否则客户端拿不到 `status`，表现为「调用成功但报未知错误」。
   实现分散在 `internal/lsky/auth.go`（鉴权失败）与 `internal/theme/routes.go`（NoRoute）。
2. **`POST /tokens` 要能重复调用**：内部 `CreateAPIToken` 拒绝同名令牌（防用户混淆），
   直接调用第二次就 409。Lsky 层必须先吊销同名旧令牌再签发。
3. **启动时做路由冲突检测**：`lsky.DetectConflicts` 检查 `/api/v1/**` 下是否有非契约路径，
   发现即 fail fast（防将来有人把内部接口误挂到保留区）。

### 契约路径（9 条，`lsky.ReservedPaths` 是外部冻结的保留集）

```
POST   /api/v1/tokens        邮箱+密码换令牌
DELETE /api/v1/tokens        清空当前用户全部令牌
GET    /api/v1/profile       资料（字节 + KB 双套字段）
GET    /api/v1/strategies    存储列表（免鉴权）
POST   /api/v1/upload        multipart 上传（**同步**返回 URL）
GET    /api/v1/images        Laravel 分页
DELETE /api/v1/images/{key}  按 UID 删除
GET    /api/v1/albums        **伪造响应**（D101）：恒空列表
DELETE /api/v1/albums/{id}   **伪造响应**（D101）：恒成功
```

> 相册功能已于 D101 移除；`/api/v1/albums` 属外部冻结的保留路径（客户端硬编码），
> 故保留端点但只返回伪造响应，`album_id` 参数收下但忽略，`profile.albumNum` 恒为 `0`。

**验证**：`make e2e-lsky`（20 项，含信封一致性 11 个错误场景）。

## 8. PicGo-Core fork

| 项 | 值 |
|---|---|
| **消费方式** | **npm 包 `@yeqingky/picgo-core`**（当前 `^1.0.0`，由 `picgo-agent/package.json` 声明） |
| 源码仓库 | `Github-me:YeqingKy/PicGo-Core`，分支 **`PicGo-Web`**（基线 `dev` @ v3.0.2） |
| 发布策略 | fork 的**版本线独立**，从 `1.0.0` 起（不与上游 v3.x 同步号），见其 `FORK-NOTES.md` §5 |
| 上游策略 | **自维护，不提交上游**；改动**只增不改**，只在显式传参时生效（D48–D51） |
| 补丁 | `UploadOptions.uploader`（按次指定图床）、`contextData`（事件归属）、per-context 配置覆盖、`Lifecycle.step` 改局部变量 |

**本项目不再需要本地 PicGo-Core 源码**：

- ❌ 不用 `file:../../PicGo-Core`（本地路径）
- ❌ 不用 `make vendor` 打 tarball
- ✅ 直接 `pnpm install` 从 npm 拉 —— 镜像构建与本地开发都一样
- ✅ 镜像构建期**断言补丁存在**（dist 里必须含 `contextData`），缺失即构建失败

**import 写法**（改了会编译失败或装错包）：

```ts
import { PicGo, evaluatePluginConfig } from '@yeqingky/picgo-core'   // ✅
import type { IPicGo, IImgInfo } from '@yeqingky/picgo-core'         // ✅
import { PicGo } from 'picgo'                                        // ❌ 旧写法
```

升级流程：改版本号 → 重新发 fork → 重启 dev 容器（`make dev-up`）/ 生产 `docker compose up -d --build`。

详见 `docs/PICGO-INTEGRATION.md` §8 与 PicGo-Core 仓库的 `FORK-NOTES.md`。

---

## 9. 提交前检查清单

- [ ] `make check`（或至少 `check-server`）全绿
- [ ] 新增表/字段后：**同步 `docs/DATA-MODEL.md`** 与 `model.AllModels()`
- [ ] 新增配置键后：**同步 `config/defaults.go`**（不需要迁移）
- [ ] 新增接口后：**同步 `docs/API.md`**
- [ ] 新增日志类型后：**同步 `model.LogTypes()`** 与 `docs/OPERATIONS.md`
- [ ] 改动涉及设计/架构决策时：**在 `docs/DECISIONS.md` 追加新编号**，不要改历史条目
- [ ] 改了 Lsky 层后跑 `make e2e-lsky`（信封一致性极易被改坏）
- [ ] 改了主题分发/zip 安装后跑 `make e2e-theme`
- [ ] **不要**新增宿主直跑方式（部署只走 docker compose，D76）
- [ ] **本文件（AGENTS.md）已同步更新**

---

## 10. 当前进度

| 工作流 | 状态 | 关键产出 |
|---|---|---|
| W0 PicGo-Core fork 与补丁 | ✅ | 分支 `PicGo-Web`（基线 v3.0.2），补丁 4 处，250 单测 |
| W1 契约与骨架 | ✅ | docs 九份 + Makefile + .env.example + compose + AGENTS.md |
| W2 Go 基础设施 | ✅ | config/logger/database/model/repository/crypto/id/settings/response/middleware/server |
| W3 Go 鉴权与用户 | ✅ | auth/JWT/refresh 轮换/API Token/限流/防枚举/首启引导 + `/auth/*` `/users/*` |
| W4 picgo-agent 侧车 | ✅ | hono 侧车 + 上传/图床/插件/删除/SSE/job，**168 单测** |
| W5 Go 业务（存储/上传队列/图库） | ✅ | agent 客户端 + 事件总线 + 队列（Job/JobItem）+ 配额/限流/重试/恢复 + 图库/外链 |
| W6 Go 日志·邮件·删除·清理 | ✅ | 日志查询 + SMTP + EmailLogs + 远端删除 + 每日清理 + 插件管理 |
| W7 前端基座 | ✅ | 设计 token + 30 个 UI 组件 + http/sse + store + 路由守卫 + 登录页 |
| W8 前端页面 | ✅ | 上传/图库/任务/日志/存储/插件/主题/站点/用户/设置 |
| W9 Lsky 兼容 + 静态托管 | ✅ **已完成** | 静态托管（`/theme-assets` vs `/assets`、SPA 回退、防穿越）+ **Lsky v1 兼容层**（9 端点、Lsky 信封、启动冲突检测）+ Dockerfile |
| W10 主题系统 | ✅ | manifest/Pages 分发/seed/内嵌兜底/ThemeConfigs/zip 安装 9 条校验 |

### 已实现的能力（端到端验证通过）

```
认证      邮箱登录 · JWT · refresh 轮换 · API Token · GitHub OAuth（需先绑定）· 限流 · 首启引导
存储      多实例配置（同类型多条）· 密钥 AES-GCM 加密 · 能力探测 · 连通性测试 · reconcile 投影
上传      队列（Job/JobItem）· 并发度可配（默认 1）· 配额 · 限流（默认禁用）· 重试 · 超时 · 重启恢复
          默认禁止 SVG（`upload.blockSvg = true`）· 魔法路径/文件名 · SSRF 防护 · SHA-256 记录（不去重）
图库      我的/全部（管理员 Tab）· 筛选 · 批量 · 灯箱 · 外链三种格式 · 硬删除 + 可选远端删除 · 配额退还
任务      Job 列表/详情/日志（SSE 实时）
日志      管理员查看全站操作日志 · 类型清单 · 邮件日志（不存正文）· 每日清理
邮件      SMTP（ssl/starttls/none）· 找回密码（UserSettings KV 承载令牌）
插件      列表/搜索/README/安装/卸载/更新/启停（异步 job + 实时日志）
主题      manifest.Pages 自行注册 · 最长前缀匹配 · 认证页与后台永久保留 · 内嵌兜底 · zip 安装 · Git 安装（D100，默认主题源码在 PicGo-Web-Theme 仓库）
静态      /assets（内置 SPA）· /theme-assets（主题）· SPA 回退 · 路径穿越防护
前端      概览（`/overview`：资源卡片 / 配额环 / 30 天趋势 / 我的信息，D103）· 上传队列（进度插值）· 图库（无缩略图）· 任务 · 日志 · 存储 · 插件 · 主题 · 站点 · 用户 · 设置
```

### 未完成

| 项 | 说明 |
|---|---|
| 前端 e2e 测试 | 只有 typecheck/lint/build；端到端验证靠 `scripts/e2e-*.sh`（服务端视角） |
| Docker 构建实测 | 根目录 `Dockerfile` 已写好并通过指令自检，但**本机无 docker，未实际 build 过** |
| 图片审核 / 水印 | 明确不做（D62）：后端不落盘，无字节流可处理 |
