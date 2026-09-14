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
| ❌ 标签 / 公开画廊 / 分享页 | 用「相册 + 重命名」整理（D55/D89） |
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
├── deploy/               部署产物（docker / vendor）
├── themes/               默认主题的打包产物（构建生成，不提交）
├── docker-compose.yml    面向用户的唯一官方部署方式（D76）
├── docker-compose.pgsql.yml  PostgreSQL 覆盖文件
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
    │   └── migrate_v1.go        v1：建 21 张表 + 全部索引
    ├── model/                   21 张表的 GORM 模型（按域分文件，**每个都实现 TableName()**）
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

待 W7/W8 实现。设计规范见 `docs/DESIGN.md`（唯一真源）。

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

## 6. 主题系统（D94–D99）

**模型**：内置 SPA（`go:embed web/dist`）提供全部页面的默认实现；
**主题是可选的页面覆盖层**，在 `manifest.json` 的 `Pages` 里**自行注册**要接管的页面。

| 可注册 | `/`（首页）、`/upload`、`/gallery`、`/albums`、`/jobs`、`/logs`、`/settings` |
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

```bash
make help              # 查看全部目标
make deps              # 安装三端依赖（含 PicGo-Core 的 install + build）
make dev               # 启动 server + agent + web

# ---- 单元测试与检查 ----
make check             # 串行跑三端全部门禁
make check-server      # go vet ./... && go test ./... && CGO_ENABLED=0 go build
make check-core        # PicGo-Core: pnpm lint && pnpm test
make fmt               # gofmt -s -w

# ---- 构建 ----
make build             # 三端产物
make theme             # 构建默认主题 → themes/default/
make pack              # 发布包
```

**改完代码必须跑**：

```bash
cd server && go vet ./... && go test ./... && CGO_ENABLED=0 go build ./cmd/picgo-web
```

### 已实现的测试（W2）

| 测试 | 覆盖的验收点 |
|---|---|
| `internal/database/dsn_test.go` | DSN 拼装、密码脱敏、DBDSN 覆盖、不支持驱动 |
| `internal/database/migrate_test.go` | **21 张表全建**、**命名 PascalCase 回归**、列名 PascalCase、迁移幂等、迁移表有序、13 个组合索引存在 |
| `internal/crypto/aes_test.go` | 加解密往返、nonce 随机化、错密钥/篡改失败、密钥生成与复用、文件权限 0600 |
| `internal/id/ulid_test.go` | 各前缀、2 万次并发唯一性、同毫秒单调递增 |
| `internal/settings/service_test.go` | **三级兜底**、Source 追踪、secret 加密与掩码、未注册键拒绝、onChanged、重启持久化、类型强转 |

---

## 8. PicGo-Core fork

| 项 | 值 |
|---|---|
| 仓库 | `Github-me:YeqingKy/PicGo-Core`，本地 `../PicGo-Core` |
| 分支 | **`PicGo-Web`**（基线 `dev` @ v3.0.2，提交 `6419c2f`） |
| 策略 | **自维护，不提交上游**；改动**只增不改**（D48–D51） |
| 补丁 | `UploadOptions.uploader`（按次指定图床）、`contextData`（事件归属）、per-context 配置覆盖、`Lifecycle.step` 改局部变量 |
| 注意 | `dist/` 被 gitignore **且无 `prepare: build`** → **git 依赖不可用**，必须 `file:` 依赖或 tarball |

详见 `../PicGo-Core/FORK-NOTES.md` 与 `docs/PICGO-INTEGRATION.md`。

---

## 9. 提交前检查清单

- [ ] `make check`（或至少 `check-server`）全绿
- [ ] 新增表/字段后：**同步 `docs/DATA-MODEL.md`** 与 `model.AllModels()`
- [ ] 新增配置键后：**同步 `config/defaults.go`**（不需要迁移）
- [ ] 新增接口后：**同步 `docs/API.md`**
- [ ] 新增日志类型后：**同步 `model.LogTypes()`** 与 `docs/OPERATIONS.md`
- [ ] 改动涉及设计/架构决策时：**在 `docs/DECISIONS.md` 追加新编号**，不要改历史条目
- [ ] **本文件（AGENTS.md）已同步更新**

---

## 10. 当前进度

| 工作流 | 状态 |
|---|---|
| W0 PicGo-Core fork 与补丁 | ✅ 已完成（`6419c2f`，250 单测 + lint 通过） |
| W1 契约与骨架 | ✅ 已完成（docs 九份 + 根配置 + Makefile + compose） |
| **W2 Go 基础设施** | ✅ **已完成**（config/logger/database/model/repository/crypto/id/settings/response/middleware/server + 测试） |
| W3 Go 鉴权与用户 | ⬜ 待开始 |
| W4 Agent 内核 | ⬜ 待开始 |
| W5 Go 业务（存储/上传队列/图库/相册） | ⬜ 待开始 |
| W6 Go 日志与邮件 | ⬜ 待开始 |
| W7 前端基座 | ⬜ 待开始 |
| W8 前端页面 | ⬜ 待开始 |
| W9 Lsky 兼容 + 静态托管 | ⬜ 待开始 |
| W10 主题系统 | ⬜ 待开始 |
