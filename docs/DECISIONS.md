# 决策记录（Decisions）

> 本文档记录 PicGo-Web 在需求讨论阶段确定的**全部关键决策**，是 `ARCHITECTURE.md` /
> `DATA-MODEL.md` / `API.md` / `PICGO-INTEGRATION.md` / `PLAN.md` 的**上位约束**。
> 后续任何文档与本文档冲突，以本文档为准。
>
> 状态标记：✅ 已定 / ⬜ 待定（见文末）

---

## 一、项目定位

| # | 决策 | 说明 |
|---|---|---|
| D1 | ✅ 定位：**自用为主，其次朋友/团队之间分享使用** | 不是商业化图床 SaaS |
| D2 | ✅ 后端**不做存储、不做图片分发**，全部交给 PicGo | 我们只管「元数据 + 管理 + 调度」 |
| D3 | ✅ 参考 lsky-pro 的功能骨架，但**砍掉存储与分发** | 见 §十一「明确不做」 |
| D4 | ✅ 存储驱动**只有管理员能配置**，不涉及存储驱动租户 | 一套全局驱动池，所有用户共用 |
| D5 | ✅ 做**多用户** + **存储空间配额** | 这是唯二的「多租户」维度 |

---

## 二、架构

### D6 ✅ 三进程结构（**端口已避开冲突**）

```
Browser (React SPA)
   │
   ▼
Go server (Gin)  :8080   ← 主进程：鉴权 / DB / REST / SSE / 托管前端
   │  HTTP + SSE (127.0.0.1:36678, X-Agent-Token)
   ▼
picgo-agent (Node/TS)     ← 侧车：持有 picgo-core 实例、插件、上传
   ▼
picgo-core → 图床（GitHub / S3 / WebDAV / ...）
```

**端口分配**（避开 picgo-core 内置 HTTP server 的默认端口 `36677`）：

| 进程 | 默认端口 | 说明 |
|---|---|---|
| Go server | **`8080`** | 对外唯一入口（`PICGO_WEB_LISTEN`） |
| picgo-agent | `36678` | 仅 `127.0.0.1`，带 `X-Agent-Token` |
| picgo-core 内置 server | `36677` | **默认不启用**；需启用时另行配置，与上面两者不冲突 |

**为什么必须有 Node 侧车**：`picgo-core` 的插件是 npm 包 + `require()` 动态加载，
只能在 Node 运行时里跑。GUI（Electron）与 CLI 都是 Node 宿主，本项目沿用同一模式。

### D7 ✅ Go 托管 agent 子进程，同时支持独立部署

- 默认 Go 启动时拉起 agent（`PICGO_WEB_AGENT_AUTOSTART=true`），退出时关闭
- 也支持 agent 单独部署（`PICGO_WEB_AGENT_AUTOSTART=false` + 指定 `PICGO_WEB_AGENT_URL`）
- Go 对 agent 做健康探测（`GET /healthz`，2s 超时），失败退避重启（最多 5 次）
- agent 不可用时，依赖它的接口返回 `503 / 50002`

### D8 ✅ agent 侧维护**单个** `PicGo` 实例

- 全部上传通过同一个实例，**默认串行的队列**（见 D35）
- 切驱动用 `picgo.setConfig()`（**只改内存、不落盘**），不污染 `config.json`
- 需要并发时才打 picgo-core 补丁（见 §十）

---

## 三、技术栈

| # | 层 | 选型 |
|---|---|---|
| D9 | ✅ Go Web 框架 | `gin-gonic/gin` v1.12 |
| D10 | ✅ ORM | `gorm.io/gorm` v1.31 |
| D11 | ✅ 数据库 | **SQLite（默认）** `github.com/glebarez/sqlite`（纯 Go，免 CGO）<br>**PostgreSQL（可选）** `gorm.io/driver/postgres`（pgx） |
| D12 | ✅ 数据库切换 | 环境变量 / `.env`：`PICGO_WEB_DB_DRIVER=sqlite\|postgres` |
| D13 | ✅ 前端 | React 19 + TypeScript + Vite 7 + Tailwind CSS v4 + Radix UI + React Router v7 + Zustand + Axios |
| D14 | ✅ UI 组织 | shadcn 风格：`src/components/ui/*` = Radix 原语 + Tailwind 封装 |
| D15 | ✅ 侧车 | Node 24 + TypeScript + `hono`（路由）+ `picgo@3.0.2` |
| D16 | ✅ 迁移 | 自研 `SchemaMeta` + `schemaMigrations` 升序迁移表 |
| D17 | ✅ 日志库 | `log/slog`（JSON Handler） |

---

## 四、数据与配置

### D18 ✅ 配置三级分层

```
第 1 层  环境变量 / .env        —— 启动引导类，只读、不可热更
         （监听地址、数据库连接、数据目录、加密主密钥、agent 地址与令牌、日志级别）

第 2 层  数据库 settings 表      —— 业务配置，运行时可改，**DB 为真相源**
         （站点设置、上传限制、SMTP、OAuth、npm 源、默认配额、安全策略）
         敏感值 AES-256-GCM 加密入库（encrypted = true）

第 3 层  代码默认值              —— internal/config/defaults.go 兜底
```

读取统一走 `SettingsService.GetInt/GetString/GetJSON(key, def)`，实现
**「DB 覆盖 → 代码默认值」** 两级合并；写入后触发 `onChanged` 回调。

### D19 ✅ 加密主密钥不进数据库

它用于解密库中的密文，与密文同库存储等于没有加密。来源优先级：
1. `PICGO_WEB_SECRET_KEY` 环境变量（容器推荐）
2. 首次启动生成 `data/secret.key`（权限 0600）

### D20 ✅ 用户配额口径

- `users.capacity_bytes` = 该用户**总配额上限**（字节）
- `users.used_bytes` = 已用字节数（上传成功时按文件大小累加）
- `capacity_bytes = 0` 视为**不限额**
- 上传前校验 `used_bytes + 本次总大小 > capacity_bytes` → 拒绝（**`40302` 配额不足**；`40301` 保留给权限不足）
- **管理员（`role = admin`）跳过配额校验**
- 管理员可在后台**逐个**调整用户配额

### D21 ✅ 新建用户的默认配额由设置决定

| 键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `user.defaultCapacityBytes` | int | `5368709120`（5 GiB） | 新建用户自动分配的配额 |
| `user.unlimitedCapacity` | bool | `false` | `true` 时新用户默认不限额 |
| `user.defaultStatus` | string | `active` | 新用户默认状态 |

**修改此设置只影响之后新建的用户，不回溯修改已有用户。**

### D72 ✅ 删除即退还配额

- 删除图片记录时，`users.used_bytes` **减去该图片的 `size`**
- **与是否真正删除远端文件无关**（D66 已取消去重，无需引用计数；
  即使远端删除失败或该驱动不支持删除，配额照退）
- 语义：`used_bytes` = 该用户**当前持有**的图片体积合计
- 边界：减少后若为负（历史数据异常）则归零并记一条警告日志

### D64 ✅ 存储驱动支持添加**多个同类型实例**

- 同一种驱动类型（如 WebDAV / GitHub）可以添加**多条**配置
  （例：三个不同的 WebDAV 服务器、两个不同的 GitHub 仓库）
- **后端用 `uid` 作为唯一标识，前端显示 `name`**
  - 例：`uid = st_01HXX...`，`name = "我的坚果云 WebDAV"`
- 所有引用存储配置的地方（上传、去重、日志、API）**一律用 `uid`**，
  不允许用驱动类型或 name 做唯一判断

### D65 ✅ 存储配置 ↔ picgo-core 的字段映射

| 本项目 | picgo-core | 说明 |
|---|---|---|
| `StorageConfigs.UID` | `uploader.<type>.configList[]._id` | 唯一标识（后端主键语义） |
| `StorageConfigs.Name` | `..._configName` | 展示名 |
| `StorageConfigs.Type` | `<type>` | 驱动类型（`webdav` / `github` / ...） |
| `StorageSecrets.EncryptedPayload`（AES 加密） | 其余业务字段 | 驱动凭据（token / secret / 密码……）。**凭据按 D78 拆到独立表，不在 `StorageConfigs` 里** |

> picgo-core 的 `uploaderConfig` 本身就支持同类型多配置（`listUploaderTypes` /
> `getConfigList(type)` / `createOrUpdate(type, configName, patch)`），
> 所以这一层是**直接映射**，不需要额外抽象。

### D22 ✅ `config.json` 采用**键级合并**，不可整体重建

> ⚠️ 修正早期设计中的一个错误。

插件会往 picgo config 里写自己的状态（例：`picgo-plugin-github-plus` 会写
`uploaded: [...]` 图片账本、`[PluginName].lastSync`）。因此：

- **只覆盖我们管的键**：`picBed.*` / `uploader.*` / `picgoPlugins.*` / `settings.*`
- **其余键原样保留**（插件私有键一律不动）
- 启动时执行 reconcile：DB 中所有 `enabled` 存储配置按 `updated_at` 升序推给 agent，
  最后激活 `is_default` 那条（幂等）

---

## 五、鉴权与用户

| # | 决策 | 说明 |
|---|---|---|
| D23 | ✅ **只认邮箱作为登录标识**（不用用户名） | `users.email` 唯一 |
| D24 | ✅ 密码登录：邮箱 + 密码，bcrypt cost 12 | |
| D25 | ✅ **不做自助注册** | 用户由管理员在后台创建，或通过邮件邀请 |
| D26 | ✅ OAuth2 **只接 GitHub** | |
| D27 | ✅ OAuth2 **必须先绑定才能用于登录** | 即 OAuth2 是已注册用户的便捷登录方式，**不是注册入口** |
| D28 | ✅ OAuth2 接入原则：**平台必须提供稳定的唯一标识** | GitHub `id` 满足；没有唯一校验值的平台不接入 |
| D29 | ✅ SMTP 邮件 | 用途：邮件邀请、找回密码；**每次发信都要写操作日志** |
| D30 | ✅ 会话：JWT access token（15min）+ refresh token（7天，存哈希） | Cookie httpOnly + SameSite=Lax |
| D31 | ✅ API Token：`pcw_<random>`，DB 存 SHA-256 | 明文仅创建时返回一次 |
| D32 | ✅ 首启引导：无用户时创建管理员，随机密码写日志 + `data/initial-admin-password.txt`（0600），强制首次登录改密 | |
| D33 | ✅ 图片可见性：**各自私有**，管理员可见全部 | 见 D71 |
| D34 | ✅ 账号注销功能存在（有对应操作日志类型） | |

### D71 ✅ 管理员图片页：同一页面 + 顶部 Tab 切换

> 主人选择：「同一页面，顶部 Tab 切换」

- 路由只有一个（`/images`）
- 顶部有分段控件 / Tab：**「我的图片」｜「全部图片」**
  - 普通用户：**只渲染「我的图片」**，不显示切换器
  - 管理员：默认落在「我的图片」，可切到「全部图片」
- 两个 Tab **共用同一套**列表 / 筛选 / 批量操作组件（只换数据源与权限判定）
- 目的：避免误操作他人的图（见 §附录）

---

## 六、上传与队列

### D66 ✅ **不做内容去重**

> 主人的最终决定：「算了 不做去重吧 要考虑的太多 因为可能还有存储驱动用年月日作为url
> 这时候做去重不太好 内容分发和url不是我们这个程序负责」

**每一次上传都是真实上传，不做任何 md5/sha256 去重。**

理由（已记入以避免后人反复推翻）：

1. **URL 形态由图床决定**，不由我们决定。很多驱动用「年月日 + 随机名」生成路径与 URL，
   同一个内容在不同时间上传本来就该得到不同 URL；去重会**破坏图床自身的内容分发规则**。
2. 内容分发与 URL 的生成属于**图床（PicGo 驱动）的职责**，不属于本项目的职责边界。
3. 去重会引入引用计数、配额口径、远端删除安全等一系列连锁复杂度，
   与「自用为主」的定位不相称。

**后果**：
- `Uploads` 表**不建** `SHA256` 唯一索引；`SHA256` 仅作元数据记录（可选，便于用户自查）
- 删除逻辑**不需要引用计数**（见 D72 修订）
- 配额按「用户当前持有的记录」统计，语义简单直白

### D35 ✅ 后端队列 + 并发限制，默认 **1 并发**

> 主人原话：「项目本身也要在后端做队列 做并发限制 如果修改起来麻烦 就1并发 一个一个跑 前端多等会」

- 队列、并发限制、重试、进度、优雅关闭**全部在 Go 侧实现**
- 默认 `upload.concurrency = 1`（严格串行）
- `> 1` 需要 picgo-core 补丁（见 §十）；**切换只改配置值，不动业务代码**

### D36 ✅ 任务粒度

```
POST /api/web/v1/uploads  （一次请求 = 1 个 Job，含 N 个文件）
        │
        ▼
   Jobs 表 (JobUID)  →  拆成 N 个 JobItems  →  worker pool
```

**并发限制作用在 item 层**（粒度最合适）；进度 = 已完成 item / 总 item。

### D37 ✅ job 状态只有 4 个，**不要 `partial`**

`queued` → `running` → `succeeded` / `failed`

- 只要有 item 失败 → job = `failed`
- 成功/失败/跳过数写进 `result`：`{total, succeeded, failed, skipped}`
  （**注**：D66 取消去重后已无「跳过」场景，`skipped` 恒为 0，保留字段符合 D77「宁可先留着不用」）
- **成功项的 URL 照样回传，不丢数据**

### D38 ✅ **一个批次只能选一个驱动**

- `POST /api/web/v1/uploads` 带一个 `StorageUID`（字符串 ULID；D64 要求所有存储引用一律用 UID，**不使用**数字形式的 ID）
- 要发到不同驱动 → 前端**自己拆成多个批次**并行请求
- → **driver 信息（Type / ConfigName）放在 Job 层，`JobItems` 不重复存**

### D39 ✅ agent 的 `/api/upload` 设计为**单文件 + 同步**

```jsonc
// POST /api/upload
{ "path": "/data/uploads/2026/02/abc.png",
  "uploader": { "type": "github", "configName": "work" },
  "jobId": "u_01HXX", "itemId": 42 }
// → 阻塞直到上传完成
{ "id": 42, "url": "https://...", "fileName": "abc.png",
  "width": 800, "height": 600, "size": 12345, "raw": { /* 完整 IImgInfo */ } }
```

**单文件的好处**：`uploadProgress` 的四档 `0/30/60/100` 就变成**这个文件自己的进度**，
聚合后进度条相当顺滑。

### D40 ✅ 上传限流：做；**管理员跳过** —— ⚠️ **已被 D73 取代**

> D40 仅保留作历史记录（键名、默认值、开关均由 D73 补全）；**实现以 D73 为准**。

- 不单独做 API 限流
- 上传限流按用户（登录用户）计量，阈值存 settings 表
- **管理员直接跳过**（与跳过配额校验一致）

### D73 ✅ 上传限流：**只按张数**，且**默认禁用**

- 维度：**只统计上传张数**（不做流量维度）
- 作用域：按登录用户（本项目没有游客上传）
- **管理员（`role = admin`）直接跳过**
- ⚠️ **默认关闭**（`enabled = false`）；开关与阈值**全部存数据库**，
  管理员在后台「系统设置 → 上传」中开启并调参，**改代码不需要重启**
- 刷新时机：设置写入后立即生效（`SettingsService.onChanged` 回调更新内存限流器）

| 键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `upload.rateLimit.enabled` | bool | **`false`** | 总开关（默认禁用） |
| `upload.rateLimit.perHour` | int | `100` | 每用户每小时最多上传张数 |
| `upload.rateLimit.perDay` | int | `500` | 每用户每天最多上传张数 |
| `upload.rateLimit.action` | string | `reject` | 超限行为：`reject`（拒绝）/ `log`（仅记录不拦） |

### D41 ✅ 失败重试 + 超时 + 优雅关闭

| 键 | 默认 | 说明 |
|---|---|---|
| `upload.retryTimes` | `1` | 单文件失败重试次数 |
| `upload.retryBackoffMs` | `2000` | 重试退避基数（指数增长） |
| `upload.queueMaxLength` | `1000` | 队列上限，超出返 `42901` |
| `upload.itemTimeoutSeconds` | `300` | 单文件超时 |
| `upload.shutdownGraceSeconds` | `30` | 优雅关闭等待时长 |

---

## 七、魔法路径 / 魔法文件名

### D42 ✅ 图片命名**完全由本项目决定**

### D43 ✅ 魔法路径 / 魔法文件名**每个存储配置各自设置**

> 主人原话：「每个存储配置各自设置」

- 不设全局默认模板，**每个存储驱动配置里独立配置**模板
- 理由：不同图床的路径习惯不同（GitHub 要 `img/`，WebDAV 要 `photos/2026/02`）

### D44 ✅ 实现机制（实测验证）

| 维度 | 机制 | 通用性 |
|---|---|---|
| **魔法文件名** | `picgo.helper.beforeUploadPlugins.register('renameFn', { handle: (ctx) => { ctx.output[i].fileName = '...' } })` | ✅ **完全通用**，所有驱动生效 |
| **魔法路径** | 驱动支持则塞路径（多数驱动是 `config.path + fileName` 拼接，塞进 `fileName` 可造子目录） | ⚠️ **看驱动** |
| **不支持时降级** | 路径转成文件名前缀，如 `2026-02-14_abc.png` | |

- agent 从驱动的 config schema 里有没有 `path`/`root`/`basePath` 类字段**自动推断能力**
- 该能力在「存储配置」界面上以提示形式告知管理员
- 参考实现：`PicGo/src/main/apis/app/uploader/index.ts` 的 `renameFn`

---

### D70 ✅ 魔法路径模板变量（常用集）

| 变量 | 含义 |
|---|---|
| `{Y}` `{m}` `{d}` | 年（4位）/ 月（2位）/ 日（2位） |
| `{H}` `{i}` `{s}` | 时 / 分 / 秒（2位） |
| `{timestamp}` | Unix 秒秒级时间戳 |
| `{filename}` | 原始文件名（不含扩展名） |
| `{md5}` / `{md5-8}` | 文件 MD5 全量 / 前 8 位 |
| `{sha256-8}` | SHA-256 前 8 位（仅元数据参考；**D66 已取消去重，它不是去重键**） |
| `{uid}` | 上传者用户 ID |
| `{uniqid}` | 随机串（短） |
| `{extname}` | 扩展名（含点，如 `.png`） |

- 魔法文件名与魔法路径**共用同一套变量**
- 模板为空 → 回退到默认（文件名 = `{uniqid}{extname}`，路径 = 无）
- 模板中未知变量 `{xxx}` **原样保留**并记录警告（不报错，不阻断上传）
- 非法路径字符（`\ / : * ? " < > |`）在应用模板后被替换为 `_`

### D68 ✅ 外链复制提供三种格式

| 格式 | 输出 |
|---|---|
| Markdown | `![{filename}]({url})` |
| 直链 | `{url}` |
| HTML | `<img src="{url}" alt="{filename}" />` |

- 前端图库/详情页提供「复制」按钮 + 格式选择
- 支持批量多选后一次性复制（多行拼接）

---

## 八、操作日志系统

### D45 ✅ 统一操作日志

> 主人原话：「首先一个日志的uid 然后是类型 比如 上传 邮件发送 账号创建 账号注销 新建存储 删除存储
> 然后是状态 成功/失败 失败就写日志 …json日志 最后是时间 支持通过类型和关键词过滤/搜索」

```go
// ⚠️ 字段以 DATA-MODEL.md §6.1 的 OperationLogs 表为准（唯一真源）。
// 下列仅为语义示意，实现请看表定义：
type OperationLog struct {
    UID        string // 日志唯一 ID（log_ 前缀 ULID）
    Type       string // 操作类型
    Status     string // success | failed
    UserUID    string // 操作者（系统操作为空）
    Username   string // 冗余，便于展示与搜索
    TargetType string // upload | user | storage | plugin | ...
    TargetUID  string // 操作对象 uid
    Detail     string // JSON：成功时的上下文
    Error      string // 失败原因（失败时必填）
    ClientIP   string
    UserAgent  string
    CreatedAt  int64  // Unix 秒
}
```

- **存储为 JSON**（`Detail` / `Error` 为 JSON 字符串）
- **支持按类型过滤 + 关键词搜索**（匹配 `Target` / `Detail` / `Error` / `Username`）
- 索引：`(type, created_at)`、`(user_id)`、`(created_at)`

**已确定的类型清单**（后续可扩）：

| Type | 触发点 |
|---|---|
| `upload` | 上传成功 / 失败 |
| `mail.send` | 邮件发送成功 / 失败 |
| `user.create` | 账号创建 |
| `user.delete` | 账号注销 |
| `user.update` | 账号修改（配额/状态/密码/角色） |
| `storage.create` | 新建存储配置 |
| `storage.delete` | 删除存储配置 |
| `storage.update` | 修改存储配置 |
| `plugin.install` / `plugin.uninstall` / `plugin.update` | 插件操作 |
| `auth.login` / `auth.logout` / `auth.failed` | 登录相关 |
| `image.delete` | 删除图片 |
| `image.update` | 重命名 / 移动相册 |

> 与上传队列的 `Jobs` / `JobLogs` 区分：`Jobs` 是**任务执行过程**（含实时进度与逐行日志）；
> `OperationLogs` 是**审计级结果记录**（一次操作一条，只记结果与关键上下文）。

---

### D74 ✅ 操作日志保留 180 天，自动清理

- 默认保留 **180 天**，可在设置中修改（`log.retentionDays`）；设为 `0` 表示永久保留
- 每日定时任务清理超期记录
- **清理动作本身也写一条日志**（`system.log.cleanup`，记录删除条数）
- 与早期文档中的 `security.auditRetentionDays` 统一为同一个键

---

## 九、删除语义

### D46 ✅ 硬删除 + 日志留痕

- 删除即从数据库移除记录（**不做软删除、不做回收站**）
- 同时写一条 `OperationLogs`（谁、何时、哪张图、远端是否删成功）可追溯
- 与 lsky-pro 一致，实现最简单

### D47 ✅ 远端文件删除：**插件有 `remove` 才生效**

> 主人选择：「支持，插件有 remove 才生效」

- `picgo-core` **没有**删除 API（实测 `deleteFile`/`deleteImage`/`removeObject` 均为 0），
  但存在**事实约定**：
  ```ts
  picgo.emit('remove', files, guiApi)      // GUI 侧（picgoCoreIPC.ts:158）
  ctx.on('remove', onRemove)               // 插件侧（github-plus/dist/index.js:253）
  ```
- agent 侧实现：
  1. 造一个 **`guiApi` shim**（提供 `showNotification` 等插件会解构的字段）
  2. `emit('remove', [imgInfo], shim)`
  3. **从 shim 捕获插件上报的文案反推成功/失败**（因为 `emit` 无返回值、插件是 async 且无人 await）
- **必要条件**：必须持久化上传返回的**完整 output 对象**，
  存放于 **`UploadResults.RawOutput`**（按 D78「体量不同」拆表，**不在 `Uploads` 表里**），
  否则插件回写的字段（如 GitHub 的 `sha`）丢失后**再也删不掉**

| 驱动情况 | 行为 |
|---|---|
| 插件实现了 `remove` | 真删远端文件 |
| 插件没实现 `remove` | 只删本地记录，UI 标记「该驱动不支持远端删除」（`capabilities.supportsRemoteDelete: false`） |

---

## 十、PicGo-Core fork 策略

| # | 决策 | 说明 |
|---|---|---|
| D48 | ✅ **自维护分支，不提交上游** | 不受上游代码风格与 review 约束 |
| D49 | ✅ 分支名：**`PicGo-Web`** | 基于上游 `dev`（v3.0.2） |
| D50 | ✅ fork 仓库：`Github-me:YeqingKy/PicGo-Core` | 本地已在 `PicGo/PicGo-Core` |
| D51 | ✅ 上游更新时以 `dev` 为基线 rebase | 冲突面仅限补丁涉及的几处 |

### 已落补丁（**已实现并验证**，提交 `6419c2f`）

仅 `concurrency > 1` 时才会走到；`concurrency = 1`（默认）走 `picgo.setConfig()` 老路径，不碰补丁。

| # | 补丁 | 落地位置 | 必要性 |
|---|---|---|---|
| P1 | `UploadOptions.uploader?: { type, configName? }` —— 按次指定目标 | `src/types/index.ts` | 并发必需 |
| P2 | `createContext` 加 **per-ctx 配置覆盖**（`getConfig` 先查覆盖表） | `src/utils/createContext.ts` | 并发必需；让插件读 `picBed.xxx` 自动生效，**插件零适配** |
| P3 | `Lifecycle.step` 实例字段 → **局部变量**；新增 `applyUploaderOverride` | `src/core/Lifecycle.ts` | 修并发串扰的真 bug + 应用覆盖 |
| P4 | `UploadOptions.contextData?: Record<string, unknown>` | `src/types/index.ts` + `createContext` | 让全局 `finished`/`failed` 事件能归属到具体 Job |

**实测结论**：
- 改前：两个并发批次都落到同一个 bucket（**串了**）
- 改后：分别落到各自的 bucket（**隔离**）
- 全量回归 **250 passed / 0 failed**；`pnpm lint` 通过
- 非法 `uploader` 目标（type/configName 不存在）**抛错**并给出明确消息
- 不带 `uploader` 时行为与上游完全一致

详见 `PicGo-Core/FORK-NOTES.md` 与 `docs/PICGO-INTEGRATION.md` §8。

### 依赖方式

| 阶段 | 方式 |
|---|---|
| 开发期 | `picgo-agent` 用 `"picgo": "file:../../PicGo-Core"`；Makefile 保证先 `pnpm build` |
| 构建/部署 | `pnpm build && pnpm pack` 出 tarball，Docker 里用 `file:./vendor/picgo-3.0.2-custom.tgz` |

> 注意：`PicGo-Core` 的 `dist/` 被 gitignore 且**没有 `prepare: build`**，
> 因此 **git 依赖不可用**，必须用 `file:` 或 tarball。

---

## 十一、Lsky API 兼容

### D52 ✅ 只做 **Lsky v1 契约**

实现以下端点（与 lsky-pro 2.x / skyImage 一致），返回 `{status, message, data}` 信封：

#### 挂载路径（与其他 API 完全隔离）

**已实证的事实**：生态里全部兰空插件的 server 地址都是**用户配置**的，且都是**字符串拼接**
（`${serverUrl}/api/v1/upload`、`Url + '/api/v1/upload'`），**没有一个用 `new URL()`**。
实测插件：`picgo-plugin-lankong`、`picgo-plugin-lskypro-own`、`picgo-plugin-lskypro`、
`picgo-plugin-lsky-uploader`。

因此 Lsky 层可挂在**任意前缀**下（我们选择让 Lsky 用最「正统」的 `/api/v1`，
用户填**裸域名**即可，零后缀，兼容性最好）。

本项目最终采用（**D80**）：

| 项 | 值 |
|---|---|
| **Lsky 兼容层挂载点** | **`/api/v1/**`**（独占，外部冻结契约） |
| **内部 API 前缀** | **`/api/web/v1/**`**（见 D80） |
| 冲突 | **无**。两套 API 前缀不同，不会重叠 |
| 启动保护 | 路由注册后仍做**冲突检测**，发现与 Lsky 保留集重叠则 panic，防将来新增接口时静默覆盖 |

**Lsky 独占的路径（外部冻结契约，内部 API 永不得占用）**：

```
/api/v1/tokens        /api/v1/profile      /api/v1/strategies
/api/v1/upload        /api/v1/images/**    /api/v1/albums/**
```

- 新增内部接口**一律挂在 `/api/web/v1/**`**，不需再考虑避让 Lsky
- 前一版设计曾让内部相册让位至 `/api/v1/gallery/albums`；**D80 后不再需要**，
  内部相册回到普通路径 `/api/web/v1/albums`

| Method | Path | 说明 |
|---|---|---|
| POST | `/api/v1/tokens` | 邮箱 + 密码换 token |
| DELETE | `/api/v1/tokens` | 清空当前用户 token |
| GET | `/api/v1/profile` | 容量 / 用量 |
| GET | `/api/v1/strategies` | 可用存储列表 |
| POST | `/api/v1/upload` | multipart 上传 |
| GET | `/api/v1/images` | 分页列表 |
| DELETE | `/api/v1/images/:key` | 按 key 删除 |
| GET | `/api/v1/albums` | 相册列表 |
| DELETE | `/api/v1/albums/:id` | 删除相册 |

**收益**：生态里主流兰空插件可直接接入（已核实 npm 上存在
`picgo-plugin-lankong`、`picgo-plugin-lskypro`、`picgo-plugin-lsky-uploader`、
`picgo-plugin-lskypro-own`），让 PicGo 桌面端 / PicList / uPic / ShareX 能直接把我们当图床。

---

## 十二、明确不做

| # | 不做 | 原因 |
|---|---|---|
| D53 | ❌ **用户组（Groups）** | 定位不是商业化图床，只需「多用户 + 存储空间」 |
| D54 | ❌ **公开画廊 / 图片广场** | 定位是自用/小范围分享 |
| D55 | ❌ **标签（Tags）** | lsky 与 skyImage 都没有，用「相册 + 重命名 + 可见性」整理 |
| D56 | ❌ **API 限流**（但上传限流做，见 D40） | 自用场景无必要 |
| D57 | ❌ **独立的中文用户名** | 只认邮箱 |
| D58 | ❌ **自助注册** | 管理员建号 / 邮件邀请 |
| D59 | ❌ **后端存储与图片分发** | 交给 PicGo（这是本项目的核心取舍） |
| D60 | ❌ **商业化模块**：商城、支付（支付宝/微信/Stripe/epay）、兑换码、工单、会员 | skyImage 有，本项目不做 |
| D61 | ❌ **Passkey / WebAuthn** | 不做 |
| D62 | ❌ **数据水印 / 图片审核** | 后端不落盘，无水印可做；审核另议 |
| D63 | ❌ **PicGo Cloud**（官方商业云相册） | 与本项目自建图库重复 |
| D88 | ❌ **自助注册**（参考图的「免费注册」） | D25 已定：管理员建号 / 邮件邀请 |
| D89 | ❌ **探索广场 / 公开用户主页 / 分享页** | D33 已定：图片各自私有，分享靠直接发图床链接 |
| D90 | ❌ **套餐 / 订阅 / 订单 / 支付** | D60 已定：无商业化模块 |
| D91 | ❌ **广告位** | 自用场景无意义 |
| D92 | ❌ **后台注入自定义 CSS / JS** | 安全风险（等价于任意脚本注入），且破坏可控性 |
| D93 | ❌ **生成缩略图** | D84 已定：后端不碰图片 |
| D69 | ❌ **从 lsky-pro 导入历史数据** | 专注新站；历史图片本就在各图床上，重建记录的价值不高 |

---

## 十三、仍待定
| # | 待定项 | 影响 |
|---|---|---|
| O1 | ✅ 已定：**不做去重**（D66） | — |
| O2 | ✅ 已定：保留 **180 天**，自动清理（D74） | — |
| O11 | ✅ 已定：**不做备份/恢复**（D82）。数据库文件与 `data/` 目录由运维自行备份（Docker 卷），本项目不提供导出导入界面 | — |
| O12 | ⬜ **多语言实际翻译进度**：先只做 `zh-CN`，后续按需加 | 非阻塞 |
| O3 | ✅ 已定：Markdown + 直链 + HTML 三种（D68） | — |
| O4 | ✅ 已定：**不做导入**（D69） | — |
| O5 | ✅ 已定：中文界面 + 文案 key 化，预留 en（D75） | — |
| O6 | ✅ 已定：用**常用集**变量（D70） | — |
| O7 | ✅ 已定：**只按张数**限流，管理员跳过（D73） | — |
| O8 | ✅ 已定：**删除即退还配额**（D72） | — |
| O9 | ✅ 已定：**只提供 docker-compose**（D76） | — |
| O10 | ✅ 已定：同一页面 + 顶部 Tab 切换（D71） | — |

---

## 十四、工程约定

### D80 ✅ API 路径分层：内部 `/api/web/v1/**`，Lsky 独占 `/api/v1/**`

> 主人原话：「把 PicGo-Web 的 api 路径全部改了吧 用一个单独的目录路径」

| 挂载点 | 归属 | 说明 |
|---|---|---|
| **`/api/web/v1/**`** | **PicGo-Web 内部 API** | 供本项目前端与 API Token 使用 |
| **`/api/v1/**`** | **Lsky 兼容层** | 外部冻结契约，**内部 API 永不占用** |
| `/assets/**` `/healthz` `/` | 静态资源 / 健康检查 / SPA | |

- **前缀不同，不会冲突**；前一版设计的「内部相册让位至 `/gallery/albums`」**已取消**，
  内部相册回到普通路径 **`/api/web/v1/albums`**
- Lsky 保留集（内部 API **永远不得**占用）：
  `/api/v1/{tokens,profile,strategies,upload,images,albums}`
- 启动时保留**路由冲突检测**：若内部路由与 Lsky 保留集重叠，**直接 panic**
- 前端 Axios `baseURL = '/api/web/v1'`
- Vite dev proxy 仍代理 `'/api'` → Go 服务（前缀已区分，一条规则够用）

### D81 ✅ 命名规范：PascalCase（大驼峰）

> 主人原话：「项目中 api 变量 数据库等尽量使用大驼峰命名法」

#### D81.1 适用范围

| 层 | 命名 | 示例 |
|---|---|---|
| **API JSON 字段** | **PascalCase** | `AccessToken` / `JobUID` / `StorageUID` / `CreatedAt` |
| **数据库表名** | **PascalCase 复数** | `Users` / `StorageConfigs` / `OperationLogs` / `JobItems` |
| **数据库列名** | **PascalCase** | `UID` / `UserUID` / `FileName` / `CreatedAt` |
| Go 结构体字段 | PascalCase（本来就是） | `UserUID` / `ImageURL` |
| 前端 TS **类型字段** | **PascalCase**（与 API JSON 一致） | `interface Upload { UID: string; JobUID: string }` |
| 前端 TS **变量 / 函数 / store** | camelCase（**不变**） | `const accessToken = res.AccessToken` / `fetchUploads()` |
| Go 局部变量 / 函数 | camelCase（不变） | `func handleUpload()` |

#### D81.2 缩写词一律全大写

`UID` / `URL` / `ID` / `API` / `HTTP` / `TLS` / `SMTP` / `CSRF` / `JSON`
（符合 Go 社区惯例，golint / staticcheck 推荐）。

```
✅ UserUID   JobUID   StorageUID   ImageURL   APIKey   ID   ThumbURL
❌ UserUid   JobUid   StorageUid   ImageUrl   ApiKey   Id   ThumbUrl
```

#### D81.3 例外与硬约束

1. **Lsky 兼容层是外部冻结契约，必须保持它原有的 snake_case**：
   `strategy_id` / `capacity` / `useCapacity` 等**不得改成大驼峰**；
   包络键 `{status, message, data}` 也保持小写。
2. **环境变量不变**（仍为 `UPPER_SNAKE_CASE`）：`PICGO_WEB_LISTEN`、`PICGO_WEB_DB_DRIVER` …
3. **`settings` 配置键不变**（仍为 `dot.lowerCamel`）：`site.name`、`upload.rateLimit.perHour` …
   它只是 KV 表的字符串 key，不是列名，改它得不偿失。
4. **URL 路径段保持小写复数**：`/api/web/v1/storage/configs`、`/uploads`、`/albums`、`/logs`
   （避免大小写敏感的代理/中间件问题）。
5. **picgo 侧字段名一律跟随 picgo-core，不改**：
   `_id` / `_configName` / `picBed` / `picgoPlugins` 等保持原样。
   **我们 DB 里用 `UID` / `Name`，转 picgo 时映射**（见 D65）。

#### D81.4 GORM 实现要点（容易踩坑）

```go
// internal/database/gorm.go
gorm.Open(dialector, &gorm.Config{
    NamingStrategy: schema.NamingStrategy{
        NoLowerCase: true,   // ← 关键：不做 snake_case 转换
    },
})
```

- **`NoLowerCase: true`** 后 `User` → 表 `Users`，`UserUID` → 列 `UserUID`
- **每个模型显式实现 `TableName()`**，不依赖词形变化库：

  ```go
  func (User) TableName() string { return "Users" }
  func (OAuthIdentity) TableName() string { return "OAuthIdentities" }
  func (APIToken) TableName() string { return "APITokens" }
  func (SchemaMeta) TableName() string { return "SchemaMeta" }  // 单数，刻意
  ```

- ⚠️ **PostgreSQL 会把未加引号的标识符折成小写**。GORM 生成的 SQL 总是带引号
  （`SELECT * FROM "Users" WHERE "UID" = $1`），因此应用层无影响；
  但**手写原生 SQL、迁移脚本、DBA 临时查询必须自己带双引号** ——
  写 `SELECT * FROM Users` 在 PgSQL 下会报 `relation "users" does not exist`。
  迁移里需要原生 SQL 时必须写成
  `tx.Exec(``CREATE INDEX ... ON "Uploads" ("UserUID")``)`。
- SQLite 标识符对 ASCII 大小写不敏感，两种写法都能跑，不影响本地开发。

### D82 ✅ 不做备份 / 恢复

项目**不提供**数据库与配置的导出/导入功能（无界面、无端点、无定时备份任务）。

- 备份由运维自行完成：Docker 部署时把 `./data` 目录（含数据库、`picgo/` 配置、`secret.key`）
  一起备份即可；README 里给出这个提示
- 理由：属于运维职责；跨数据库方言的导出导入会引入大量兼容成本，与「自用为主」的定位不匹配

### D83 ✅ 首页为**数据驱动的落地页**（公开可访问）

> 参考 XTheme（lsky-pro 主题）的首页结构 + 主人提供的三张截图。

| 项 | 内容 |
|---|---|
| 访问权限 | **公开**（未登录可访问）；其余页面全部需登录 |
| 结构 | Hero（背景图 + 站点名 + 上传放置区 + CTA）→ 核心能力（Bento）→ 应用场景 → FAQ 手风琴 → CTA 横幅 → Footer |
| **内容可配置** | `Features` / `Scenarios` / `Faq` 三组**后台可编辑的 Repeater**（图标 + 标题 + 描述 / 问题 + 答案） |
| 上传区 | Hero 内嵌 `UploadDropzone`；未登录点击/拖入 → 跳登录并带 `redirect=/upload` |
| 无自助注册 | 参考图的「免费注册」CTA 改为「登录」（D25） |

**新增站点级配置键**（`category=site`，KV 表，无需迁移）：
`site.subtitle` / `site.iconUrl` / `site.keywords`

**首页内容与背景图不在 `site.*`**，而是**主题的配置项**（D95 归类原则：主题的画法归主题）：
由主题 `manifest.Configuration.Items` 声明，存在 **`ThemeConfigs`** 表。
默认主题（`ID = default`）声明的就是 `BackgroundURL` / `ShowHomeFeatures` /
`HomepageFeatures` / `HomepageScenarios` / `HomepageFaq`。

**新增公开端点**（无需鉴权）：`GET /api/web/v1/site/config`（站点信息 + **当前主题的元数据与设置**）。

> 早期版本曾把首页配置写成 `site.homepage.*`、背景图写成 `site.background.*`，
> **已被 D94/D95/D97 迁移掉**，不再存在。

详见 [`DESIGN.md`](./DESIGN.md) §4.2 / §5 / §5.8。

### D84 ✅ **不做缩略图**，图库直接引用图床 URL

> 主人选择：「不做缩略图，直接引图床 URL」

- 后端**不生成、不缓存、不存储**任何缩略图（与「不做存储、不做分发」一致）
- 图库卡片直接用 `<img src={Uploads.URL} loading="lazy">`，用 CSS + `Width`/`Height` 预留占位防抖动
- 代价（已知并接受）：图多时流量大、图床慢则图库慢、图床不可达时图库看不到内容
- 降级：图片 `onError` → 显示占位图 + 可点击的原链接
- `Uploads.ThumbURL` 列**保留但不使用**（D77「宁可先留着不用」），**前端不得依赖**

### D85 ⚠️ **已被 D97 取代**：背景图三种模式（原设计）

> 保留作历史记录。**实现以 D97 为准**：背景图改为单一 URL，无模式判断。

#### （原 D85 内容）背景图三种模式；ACG API 由**前端直连**

| 模式 | 说明 |
|---|---|
| `none` | 纯色 + 细微图案 |
| `fixed` | 固定图片（URL 数组，首页与登录页各自独立） |
| `acg` | 前端**直接调用** ACG API（`https://api.yppp.net/*`）随机取图 |

**为什么可以前端直连**（已实测核实，不是推测）：

1. **限流是「按 IP」的**（主人确认）→ 每个用户用自己浏览器的 IP，各占各的额度，**互不影响**
2. 一次页面加载只发 **1 个请求**，远低于「20 次 / 10 秒」阈值
3. ACG API **已开 CORS**（实测响应头 `Access-Control-Allow-Origin: *`），浏览器可直接 `fetch`

**实现方式**

```
前端 fetch(`${AcgApiUrl}/${Orientation === 'portrait' ? 'pe' : 'pc'}.php?return=json`)
  → { code: "200", acgurl, width, height, size }        // 实测格式
  → 用 acgurl 作背景；width/height 用于校验方向、避免布局抖动
  → 结果写入 sessionStorage（TTL 由配置的缓存时长决定，默认 30 分钟）
     避免页面间跳转时重复请求
```

- **降级链**：`acg` 请求失败 → 回退 `fixed` 的第一张 → 再失败回退 `none`
- **不占用后端资源**：无定时任务、无缓存表、无代理端点
- **不需要 CORS 代理**（上游已提供）
- **上游明确要求「不要频繁调用 API 抓取图片」** → 前端**必须做会话缓存**，
  仅在缓存过期或用户点「换一张」时才重新请求

**最简替代实现**（可选，代价是拿不到宽高）：
直接 `` `<img src="${AcgApiUrl}/pc.php?_=${Date.now()}">` ``，无需 JS 解析、无需 CORS。

**（原设计的配置键，已被 D97 取消）**：`site.background.mode` / `homepageImages` / `authImages` /
`acgApiUrl` / `acgOrientation` / `acgCacheMinutes` —— **全部废弃**，
最终只有一个 `BackgroundURL`（主题配置项）。

> 后端**不保存任何 ACG 运行时缓存**（先前的 `site.background.acgCache` 键已取消）。

详见 [`DESIGN.md`](./DESIGN.md) §6。

### D86 ✅ 页面访问范围

| 范围 | 页面 |
|---|---|
| **公开**（无需登录） | `/`（首页）、`/login`、`/forgot-password`、`/reset-password`、`/first-login` |
| **需登录** | `/upload`、`/gallery`、`/gallery/:uid`、`/albums`、`/jobs`、`/settings`、`/logs` |
| **需管理员** | `/admin/users`、`/admin/storage`、`/admin/plugins`、`/admin/site`、`/admin/logs` |

- **没有游客上传**（与 D40/D73 一致：限流按登录用户计量）
- 首页的「立刻上传」CTA 指向 `/login?redirect=/upload`

### D87 ✅ 前端设计参考与取舍

参考 **XTheme**（lsky-pro 主题）的**设计意图**，**不复制其代码**：

| 采纳 | 不采纳 |
|---|---|
| 设计 token（shadcn/ui neutral 调色板 + 强调色 `#007AFF` + `--radius: 0.5rem`） | NaiveUI 组件库（本项目用 Radix UI，D14） |
| 首页结构（Hero / 能力 / 场景 / FAQ / CTA） | `/register` 自助注册（D25） |
| 首页内容数据驱动（Repeater） | `/explore` 探索广场、`/shares/:slug` 分享页（D33 图片各自私有） |
| 背景图三种模式 | `/user/plans`、`/user/orders` 套餐与订单（D60 无商业化） |
| 瀑布流图库 + 无限滚动 | 广告设置、自定义 CSS/JS 注入（安全与可控性） |

- 组件一律为 **shadcn 风格自研**（`components/ui/*` = Radix + Tailwind）
- 详细规范见 [`DESIGN.md`](./DESIGN.md)（含完整 token、路由表、组件清单、交互规范）

### D94 ✅ 前端**内置**；主题为**可选页面覆盖层**，由 manifest **自行注册接管的页面**

> 主人原话：「还是要叫主题 就是提供几个点 首页 上传页 等等 让主题配置文件自行注册」

#### 核心模型

```
内置 SPA（go:embed web/dist）—— 提供**全部**页面的默认实现
      ▲
      │  主题 = 可选的「页面覆盖层」
      │  通过 manifest 的 Pages 声明「我要接管哪几个页面的渲染」
      │  未声明的页面 → 一律走内置 SPA
```

| 项 | 内容 |
|---|---|
| **名称** | 仍然叫**主题** |
| **本质** | 主题是**可选的页面覆盖层**，不是「另一套完整前端」 |
| **注册方式** | 主题在 `manifest.json` 里用 `Pages` **自行注册**要接管的页面（精确路径列表） |
| **可注册的页面** | `/`（首页）、`/upload`（上传页）、`/gallery`（图库）、`/albums`、`/jobs`、`/logs`、`/settings` 等**业务页面** |
| **不可注册（永久保留）** | `/login`、`/first-login`、`/forgot-password`、`/reset-password`、`/logout`、`/admin/**` —— **安全底线，代码硬编码，manifest 声明无效** |
| **默认主题** | 只注册 `["/"]`（首页） |
| **实现方式** | Go 的分发逻辑**与具体页面无关**，只看 `Pages`；因此「支持哪些页面」**由主题自己决定** |
| **默认交付** | 默认主题只注册 `["/"]`（首页）；**机制上**上传页/图库页等**任何主题都可自行注册**（无需改 Go） |

#### D94.1 页面可注册清单（业务页面）

| 页面 | 路径 | 可被主题注册 | 备注 |
|---|---|---|---|
| 首页（落地页） | `/` | ✅ | 默认主题注册的就是它 |
| 上传页 | `/upload` | ✅ | |
| 图库 | `/gallery`、`/gallery/:UID` | ✅ | 用 `/gallery` 前缀即可覆盖子路径 |
| 相册 | `/albums` | ✅ | |
| 任务 | `/jobs` | ✅ | |
| 操作日志 | `/logs` | ✅ | |
| 个人设置 | `/settings` | ✅ | |
| **认证页** | `/login`、`/first-login`、`/forgot-password`、`/reset-password`、`/logout` | ❌ **永久保留** | 防凭据钓鱼（见下） |
| **后台** | `/admin/**` | ❌ **永久保留** | |
| API / 健康检查 | `/api/**`、`/healthz` | ❌ | 非页面 |
| 静态资源 | `/assets/**`、`/theme-assets/**`、`/themes/**`、`/favicon.ico` | ❌ | 属保留路径 |

#### D94.2 页面注册：`manifest.json` 的 `Pages`

```jsonc
{
  "ID": "default",
  "Name": "默认主题",
  "Pages": ["/"],                              // 只注册首页
  // 若某主题要接管首页 + 上传页 + 图库：
  // "Pages": ["/", "/upload", "/gallery"],
  "Configuration": { /* ... */ }
}
```

| 规则 | 说明 |
|---|---|
| 缺省值 | 不写 `Pages` → 等价于 `["/"]` |
| 匹配方式 | **最长前缀匹配**；`/gallery` 同时覆盖 `/gallery` 与 `/gallery/:UID` |
| `"/"` 的含义 | **精确匹配 `/` 一个路径**（它*不是*「接管全部」） |
| `"/*"` | 通配：接管**所有非保留业务页面**（等于整站主题化；语法允许，默认主题不用） |
| 保留路径 | `/api/**`、`/healthz`、`/theme-assets/**`、`/assets/**`、`/themes/**`、`/favicon.ico` |
| ⚠️ **认证页 + `/admin/**`** | **代码内硬编码保留**，manifest 注册了也会**校验失败**（主题不合法） |
| 校验 | 每项须以 `/` 开头、不含 `..`、只允许 `/*` 一种通配、**不得命中保留路径或认证页列表**；非法 → 主题不合法 + `theme.error` |

**为什么认证页与 `/admin/**` 必须永久保留（不可注册）**：

> 主题 = 第三方前端代码。若允许它注册 `/login`，它能**伪造登录框并把密码 POST 到自己服务器**。
> 受害者是**普通用户**（他们从没选过主题），而管理员才是装主题的人。
> 因此这两类路径**写死在代码里的保留列表**，`manifest` 声明无效 —— 这是安全默认值，**不是可配置项**（D77 只追加原则不适用于此处）。

#### D94.3 主题目录结构

```
<dataDir>/themes/<ThemeID>/
├── manifest.json       # 元数据 + 配置 schema（D98）+ Pages 注册（D94.2）
├── index.html           # 入口
├── assets/              # 主题自己的 js / css / 图片（带内容哈希）
│   ├── index-<hash>.js
│   └── index-<hash>.css
└── screenshot.png       # 可选，后台主题列表预览图
```

> **多页面主题**：仍然只有一个 `index.html` —— 主题自己是 SPA，内部用自己的路由区分
> `/`、`/upload`、`/gallery`；Go 只负责「这个路径交给主题的 index.html」，
> 并把**当前路径**原样传给前端（主题读 `location.pathname` 自行分流）。
> 这样主题**不需要**为每个页面提供独立 HTML，也不必做成 MPA。

#### D94.4 解析与生效

| 项 | 规则 |
|---|---|
| 主题根目录 | **`<dataDir>/themes/`**（数据真相源） |
| 当前主题 | `SystemSettings` 的 `theme.active`（默认 `default`） |
| 内嵌兜底 | 二进制内嵌一份默认主题（`go:embed`）；**主题缺失/损坏/`Pages` 非法 → 回退内嵌默认主题**，永不白屏 |
| seed | 启动时若 `<dataDir>/themes/` **为空**，把内嵌默认主题解压一份；非空则不动（升级不覆盖） |
| 主题列表 | **扫描文件系统**读 `manifest.json`，**不建表**（通常 1~3 个） |
| 切换主题 | 改 `theme.active` → **立即生效**（无需重启）；已打开页面刷新即可 |
| 缓存 | 主题的 `index.html` **不缓存**（`no-cache`）；`/theme-assets/**` 内容哈希 → `immutable` |
| 损坏记录 | 主题损坏 / 回退内嵌 / `Pages` 非法 → 写 `OperationLogs`（`theme.error`） |

#### D94.5 为什么这样设计

| 收益 | 说明 |
|---|---|
| **主题粒度自由** | 可以只换首页、只换上传页、或换整套前台 —— 由主题自己注册决定 |
| **加页面零成本** | 想让某个页面可被主题接管，**只要该页面路径在允许清单里**（D94.1），Go 已通用处理 |
| **风险可控** | 认证页与后台永久内置，主题碰不到凭据与运维入口 |
| **默认体验不变** | 默认主题只注册首页，其他页面就是内置 SPA 的原样 |
| **单二进制** | 内置 SPA 仍 `go:embed`，无外部依赖 |

### D99 ✅ 路由分发与**资源前缀划分**

#### D99.1 请求分发算法（**一次写通用，加页面不改代码**）

```
收到请求 path
  │
  ├─ 1. 保留路径?  （/api/**, /healthz, /theme-assets/**, /assets/**, /themes/**, /favicon.ico）
  │      → 交给对应处理器（API / 健康检查 / 静态资源 / favicon）
  │
  ├─ 2. 认证页保留路径? （/login, /first-login, /forgot-password, /reset-password, /logout）
  │      → 内置 SPA 的 index.html（**主题无法接管**，见 D94.2）
  │
  ├─ 3. 查当前主题的 Pages 接管范围（最长前缀匹配）
  │      ├─ 命中 → 主题的 index.html（+ 该主题的资源前缀）
  │      └─ 未命中 ↓
  │
  ├─ 4. /admin/** ?
  │      → 内置 SPA 的 index.html（**主题无法接管**，永久保留）
  │
  └─ 5. 其他 → 内置 SPA 的 index.html（SPA 回退）
```

> 第 3 步是**唯一**与主题相关的判断，且只依赖 `manifest.Pages`。
> **首次实现的默认主题 `Pages = ["/"]`**，因此只有 `/` 走主题；
> 未来某主题写 `["/", "/gallery"]`，`/gallery` 自动走主题，**Go 无改动**。

#### D99.2 资源前缀划分（避免冲突）

| 路径 | 归属 | 缓存 |
|---|---|---|
| **`/theme-assets/**`** | **当前主题**的 `assets/`（防目录穿越） | `immutable, max-age=31536000` |
| **`/assets/**`** | **内置 SPA** 的 `assets/`（embed） | `immutable, max-age=31536000` |
| `/favicon.ico` | 优先当前主题的 `assets/favicon.ico`，缺失回退内置 | `max-age=86400` |
| `/api/**`、`/healthz` | Go 后端，**不参与** SPA 回退；未匹配 → `40401` JSON | — |
| `/themes/**` | **404**（不暴露主题目录、`manifest.json`、源码） | — |

**关键约束**

1. 主题的 `index.html` 引用资源**必须走 `/theme-assets/...`**（构建时配 `base: '/theme-assets/'`）。
2. `/assets/**` **永远指向内置 SPA**，主题**不得**占用（否则会覆盖后台/登录页资源）。
3. `/theme-assets/**` 做规范化路径校验，必须落在当前主题的 `assets/` 内，否则 `40401`。
4. **主题接管范围的校验在装载时做**（D94.2），非法 → 主题不合法、回退内嵌默认主题。
5. 首页 `index.html` **永远返回**（即使未登录）；鉴权由内置 SPA 的路由守卫 + 后端 API 各自负责。
6. 主题的 `index.html` 若需站点信息与配置，调 `GET /api/web/v1/site/config`（D95）。

### D95 ✅ 主题配置存**独立表 `ThemeConfigs`**（每行一个键）

> 参考 [Komari](https://github.com/komari-monitor/komari) 的主题配置表，本项目改用**每行一个键**，
> 以符合 D78（不同内容分表）并支持逐键审计。

```go
// ThemeConfigs
type ThemeConfig struct {
    ID        uint64 `gorm:"primaryKey;autoIncrement"`
    ThemeID   string `gorm:"size:64;index;not null"`              // default / my-theme
    Key       string `gorm:"size:128;not null"`                   // BackgroundURL
    Value     string `gorm:"type:text"`                           // JSON 编码的值
    ValueType string `gorm:"size:16;not null;default:string"`      // 与 manifest 的 Type 对应
    UpdatedBy string `gorm:"size:32"`                             // 操作者 UserUID
    CreatedAt int64  `gorm:"not null"`
    UpdatedAt int64  `gorm:"not null"`
}
// uniqueIndex: (ThemeID, Key)
```

| 依据（D78 拆表原则） | 说明 |
|---|---|
| **生命周期不同** | 主题配置随主题装卸而生灭；站点设置随站点而生死 |
| **一对多关系** | 一个主题 → 多个配置键，天然适合独立表 |
| **可审计** | 每行带 `UpdatedBy` / `UpdatedAt`，能回答「谁在何时改了哪个键」 |

**读写规则**

| 项 | 规则 |
|---|---|
| 键集合 | **由主题 `manifest.json` 的 `Configuration.Items` 声明**，Go 侧**不硬编码** |
| 读取顺序 | **DB 值 → manifest 的 `Default` → 类型零值**（三级兜底，与 D18 一致） |
| 写入 | 只接受**该主题声明过的键**；类型按 `Type` 校验；未声明的键 → `40001` |
| `Source` | API 返回 `db` 或 `default`，前端显示来源徽章 |
| 换主题 | 旧主题的值**保留**（`ThemeID` 不同），切回来仍生效 |
| 卸载主题 | **不自动删值**；提供显式「清理该主题配置」（`WHERE ThemeID = ?`） |
| 并发 | 每键独立行，两个管理员改不同键**不互相覆盖** |

> **`theme.active` 仍在 `SystemSettings`**（站点级选择——当前用哪个主题，不属于任何主题的 schema）。

### D96 ✅ 主题安装：**zip 上传 + 文件系统**双通道

| 通道 | 说明 |
|---|---|
| **后台 zip 上传** | `POST /api/web/v1/themes/install`（admin，multipart） |
| **文件系统放置** | 直接把主题目录放进 `<dataDir>/themes/`，后台点「重新扫描」 |
| **重新扫描** | `POST /api/web/v1/themes/rescan`（admin） |

**zip 上传的安全校验（必须全做）**：

| # | 校验 | 说明 |
|---|---|---|
| 1 | 仅 `.zip`；压缩包 ≤ `theme.maxPackageBytes`（默认 64 MiB） | |
| 2 | **防 Zip Slip**：逐个 entry 用 **`filepath.Rel`** 判定规范化后的目标必须落在目标目录内，拒绝 `..` 与绝对路径 | 见下方改进表 |
| 3 | **拒绝符号链接 entry**（`Mode()&os.ModeSymlink != 0`） | 防「先建软链再写到链外」 |
| 4 | 单文件 ≤ `theme.maxFileBytes`（128 MiB）、总计 ≤ `theme.maxExtractBytes`（512 MiB）、文件数 ≤ `theme.maxFiles`（10000） | 防 zip bomb |
| 5 | **强制权限位**：目录 `0755`、文件 `0644`，**忽略 zip 里声明的 mode** | 防 setuid / 可执行位 |
| 6 | `manifest.json` ≤ `theme.maxManifestBytes`（1 MiB）；`ID` / `Name` / `Configuration.Type` / `index.html` 全部校验（D98） | |
| 7 | 目标目录已存在且 `Overwrite=false` → `40901` | |
| 8 | **原子性**：先解压到 `.tmp-<随机>/`，全部校验通过后再 `rename`；失败则删临时目录 | 不留半个主题 |
| 9 | 全部主题操作写 `OperationLogs` | 见下表 |

**比 Komari 更严的两处**（夜轻酱审其源码后的改进）：

| # | Komari 的做法 | 本项目 |
|---|---|---|
| 1 | 用 `strings.HasPrefix(path, clean(themeDir)+os.PathSeparator)` 判路径 | 改用 **`filepath.Rel`**（前缀比较在边界情况易误判：base 本身、盘符、UNC、Windows 大小写） |
| 2 | 用 `f.FileInfo().Mode()` 落权限 | **强制 `0644`/`0755`**，忽略 zip mode；并**显式拒绝 symlink entry** |

**其他规则**：

- **不能卸载当前启用的主题**（先切到别的主题）
- **不能卸载 `default`**（内嵌兜底的锚点）
- 目录名与 `manifest.ID` 不一致 → 视为损坏，不进列表，写 `theme.error`
- **主题 = 服务器上的任意前端代码**（影响公开首页）→ 仅 admin 可操作，UI 与文档明示

**新增操作日志类型**（D45 清单追加）：
`theme.install` / `theme.uninstall` / `theme.activate` / `theme.rescan` /
`theme.settings.update` / `theme.settings.clear` / `theme.error`

### D98 ✅ 主题 `manifest.json` 规范

```jsonc
{
  "ID": "default",                        // 必填，目录名，^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$
  "Name": "默认首页主题",                  // 必填；支持多语言对象
  "Description": "PicGo-Web 默认首页主题",  // 可选；支持多语言对象
  "Author": "YeqingKy",                   // 可选；支持多语言对象
  "Version": "1.0.0",
  "URL": "https://github.com/YeqingKy/PicGo-Web",
  "Preview": "screenshot.png",            // 可选，相对主题根的预览图
  "MinAppVersion": "0.1.0",               // 可选
  "Pages": ["/"],                         // ★ 接管的路由前缀；缺省 = ["/"]（详见 D94.2）
  "Configuration": {
    "Type": "managed",                    // 只实现 managed
    "Items": [                            // 即主题的配置 schema
      { "Key": "BackgroundURL", "Name": "背景图地址", "Type": "string",
        "Required": false, "Default": "https://api.yppp.net/api.php",
        "Help": "留空则不显示背景图" },
      { "Key": "ShowHomeFeatures", "Name": "显示核心能力区块", "Type": "switch", "Default": true },
      { "Key": "HomepageFeatures", "Name": "核心能力", "Type": "json", "Default": [] },
      { "Key": "HomepageFaq", "Name": "常见问题", "Type": "json", "Default": [] }
    ]
  }
}
```

#### 多语言文本（参考 Komari 的 `IsLocalizedText`）

`Name` / `Description` / `Author` / `Help` / `Items[].Name` 允许两种形态：

```jsonc
"Name": "默认主题"                                       // 单字符串
"Name": { "zh-CN": "默认主题", "en": "Default Theme" }   // 多语言对象
```

服务端按请求的 `Accept-Language`（回退 `zh-CN` → 对象里第一个非空值）解析成单字符串后返回；
两者都为空 → `Name` 视为缺失，主题**不合法**。

#### `Configuration.Type`

| 取值 | 状态 |
|---|---|
| `managed`（默认，省略即为它） | ✅ **实现**：由 `Items` 声明 schema，后台自动渲染表单 |
| `raw` | ❌ 不实现（等于任意 HTML/JS 注入，与 D92 冲突） |
| `redirect` | ❌ 不实现（v1 不做外部托管） |
| 其它未知值 | ❌ 装载失败，报「不支持的主题类型: xxx」，写 `theme.error` |

#### 配置项类型（`Items[].Type`）

| Type | 渲染 | 默认值 |
|---|---|---|
| `string` | 单行输入 | `""` |
| `text` | 多行输入 | `""` |
| `number` | 数字输入 | `0` |
| `switch` | 开关 | `false` |
| `select` | 下拉选择（需 `Options`，逗号分隔） | `Options` 第一项 |
| `json` | JSON 编辑器 / repeater（有 `ItemSchema` 时） | `[]` |

> 与**插件 schema**（`IPluginConfig`：`input`/`password`/`list`/`checkbox`/`confirm`/`editor`）
> 是两套命名（沿用 Komari 风格），前端实现一个薄的**适配层**把它们规约到同一组内部字段类型
> （见 `DESIGN.md` §7）。

#### 校验清单（`validateThemeManifest`）

| # | 校验 | 失败后果 |
|---|---|---|
| 1 | `ID` 非空、匹配 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`、**与目录名一致** | 主题不合法 |
| 2 | `Name` 至少一种语言非空 | 主题不合法 |
| 3 | `Configuration.Type` ∈ {空, `managed`} | 主题不合法（写 `theme.error`） |
| 4 | `Items[].Key` 非空、唯一、匹配 `^[A-Za-z][A-Za-z0-9_]{0,63}$` | 主题不合法 |
| 5 | `index.html` 存在 | 主题不合法 |
| 6 | `manifest.json` ≤ 1 MiB（`theme.maxManifestBytes`） | 拒绝安装 |
| 7 | **`Pages`**（D94.2）：每项以 `/` 开头；无 `..`；只允许 `/*` 这一种通配；**不得命中保留路径或认证页保留列表** | 主题不合法（写 `theme.error`） |

> 第 7 条是**扩展预留**的关键：首次实现的默认主题只需写 `["/"]`，
> 但校验与分发逻辑**一次性写通用**，将来加页面不改代码。

### D97 ✅ 背景图只需**一个 URL**，不做任何模式判断

> 主人原话：「背景图片直接引用 https://api.yppp.net/api.php 就行了 不需要做任何判断」

**取代原 D85**（三种模式 + 横竖定向 + 缓存 + 代理端点，**全部废弃**）。

```
主题配置项：BackgroundURL
默认值：    https://api.yppp.net/api.php
首页用法：  <img src={BackgroundURL} alt="" aria-hidden />
```

**为什么一个 URL 就够**：`api.php` 是上游的**自适应端点**——它自己按 `User-Agent`
判断并 302 到横图（`pc.php`）或竖图（`pe.php`），再 302 到图片。
所以**不需要**选横竖、不需要 `fetch`、不需要 CORS、不需要解析 JSON。

**明确不做**：

| 不做 | 原因 |
|---|---|
| ❌ 模式切换（`none`/`fixed`/`acg`） | 一个 URL 就够 |
| ❌ 横竖图判断 | 上游 `api.php` 按 UA 自行处理 |
| ❌ `fetch` + JSON 解析 | `<img>` 直连即可，302 由浏览器自动跟随 |
| ❌ 后端缓存 / 定时任务 / 代理端点 | 限流按 IP，用户各自直连互不影响 |
| ❌ 会话缓存逻辑 | 用户明确要求不做判断 |

**已知行为（写进文档，不写代码）**：同一 URL 会被浏览器缓存，刷新可能看到同一张图。
需要换图时管理员自行把 URL 改成带参形式（如 `/api.php?t=...`）——**代码不做处理**。

**唯一配置项**：`BackgroundURL`（string，默认 `https://api.yppp.net/api.php`）。
留空或加载失败 → 首页不显示背景图（浏览器 `onError` 的自然行为，非业务逻辑）。

### D77 ✅ **可扩展性设计原则**（强制）

> 主人原话：「项目尽量为后续的可扩展扩容来做考虑 避免后续想要加新功能
> 因为数据库/程序设定导致是不兼容要大改」

**D77.1 数据库层**

| 原则 | 具体做法 |
|---|---|
| 只追加，不复用 | 字段/表只增不改。废弃字段保留（标记 `// Deprecated`）而不是删除或改语义 |
| 迁移只前进 | `schemaMigrations` 只追加条目，已发布条目永不修改 |
| 公开标识与主键解耦 | 对外一律用 `uid`（ULID 字符串）；内部自增 `id` 仅用于外键与索引 |
| 预留元数据位 | 主要业务表带 `metadata` JSON 列（`gorm:"serializer:json"`），新字段无需迁移即可先落在此 |
| 配置入 KV 表 | 站点/用户配置存 KV 表，**加新配置项不需要迁移** |
| 枚举存字符串 | 状态/类型一律 `VARCHAR` 存字符串（不用 DB enum、不用数字魔法值），新增取值不涉及 DDL |
| 不设过多 NOT NULL | 除语义必需外允许 `NULL`，避免新功能加列时要回填历史数据 |
| 不设跨表强外键约束 | 只建索引不建 `FOREIGN KEY`（SQLite 与 PgSQL 行为差异大，且阻碍分表/归档）；由应用层保证一致性 |

**D77.2 应用层**

| 原则 | 具体做法 |
|---|---|
| 接口版本化 | 内部路径带 **`/api/web/v1`**（D80）；响应信封保留扩展位，新增字段一律 `omitempty` |
| 分层不穿透 | `handler → service → repository`，handler 不直接碰 `*gorm.DB` |
| 侧车契约稳定 | agent 的 HTTP 契约单独成文（`API.md` §agent），Go 只消费契约不依赖内部实现 |
| 驱动能力可探测 | 驱动是否支持魔法路径/远端删除等能力，由**运行时探测 + 能力标志**表达，不硬编码类型名列表 |
| 事件留钩子 | 关键动作（上传后/删除后/建号后）发内部事件，后续功能可订阅而不改主流程 |
| 前端路由可插拔 | 导航项由配置数组驱动，新页面只需往数组加一项 |

**D77.3 明确禁止的反模式**

- ❌ 把多种内容塞进一个表的 JSON 列（见 D78）
- ❌ 用数字型 enum 落库（如 `status = 3`）
- ❌ 把 `id` 直接暴露给前端并当作稳定标识
- ❌ 在业务代码里 `switch` 驱动类型名来实现差异（应查能力表）
- ❌ 为了当前功能删减未来可能的字段位（宁可先留着不用）

### D78 ✅ **数据库分表原则**：不同内容分到不同表

> 主人原话：「数据库尽量将不同内容分到不同的表单」

**同一种语义只放一张表；不同生命周期/不同敏感级/不同体量的内容一律拆表。**

拆表依据（满足任一即拆）：

| 依据 | 例子 |
|---|---|
| **敏感级不同** | 凭据（token/密码）单独表，日常查询永远不会误选到密钥 |
| **体量不同** | 完整上传返回值为大 JSON，与热点列表字段拆开 |
| **写入频率不同** | 日志/会话高频写，与主表低频写拆开 |
| **生命周期不同** | 任务执行日志（可短期清理）vs 审计日志（长期保留） |
| **可选性不同** | 用户档案（昵称/头像）与身份（邮箱/密码/角色）拆开 |
| **一对多关系** | 邮箱发送记录、登录尝试记录各自成表，不塞进 JSON |

完整表清单与字段见 [`DATA-MODEL.md`](./DATA-MODEL.md)。共 **20 张表**，按域分组：

```
身份鉴权  Users / UserProfiles / OAuthIdentities / RefreshTokens / APITokens / LoginAttempts
存储配置  StorageConfigs / StorageSecrets
媒体资源  Uploads / UploadResults / Albums
任务执行  Jobs / JobItems / JobLogs
审计记录  OperationLogs / EmailLogs
系统配置  SystemSettings / UserSettings
插件缓存  Plugins
迁移版本  SchemaMeta
```

### D79 ✅ 开发文档重组

`docs/` 重组为：

| 文件 | 用途 | 读者 |
|---|---|---|
| `README.md`（docs 内） | 文档索引与阅读顺序 | 全部 |
| `DECISIONS.md` | **决策记录（最高约束）** | 全部，改动前必读 |
| `ARCHITECTURE.md` | 架构总览、拓扑、技术栈、扩展点 | 开发 |
| `DATA-MODEL.md` | 表设计与迁移规则 | 开发 |
| `API.md` | 对外 REST + SSE + Lsky 兼容契约 | 开发 |
| `PICGO-INTEGRATION.md` | picgo-core 集成、补丁、魔法路径 | 开发 |
| `OPERATIONS.md` | 日志/任务/配额/限流/删除 等运行机制 | 开发 |
| `PLAN.md` | 分工、里程碑、验收 | 开发 |

根 `README.md`：
- **面向用户**：只写 `docker compose` 部署
- **面向开发**：开发/构建/测试/lint 命令单独一节

### D75 ✅ 前端 i18n：中文界面 + 文案 key 化

- 界面语言：**中文**
- 所有面向用户的文案**走 i18n key**（不硬编码），默认只提供 `zh-CN`
- 预留 `en` 目录结构，后续加语言不用重构
- 理由：PicGo 生态（PicGo GUI / PicGo-Core）本身就全面 i18n，保持一致；
  且成本极低（写 key 与写中文的工量差不多）

### D76 ✅ 部署形态：**只提供 docker-compose（面向用户）**

| 产物 | 面向 | 说明 |
|---|---|---|
| `docker-compose.yml` | **用户** | 唯一对外部署方式。默认全部内置（Go + agent + SQLite） |
| `docker-compose.pgsql.yml` | 用户（可选） | 覆盖文件，切 PostgreSQL |
| `deploy/docker/Dockerfile` | 构建 | 多阶段：前端构建 → agent 构建 → Go 编译（`CGO_ENABLED=0`）→ 运行时 |
| `README.md` | 用户 + 开发 | 部署**只写 docker compose**；开发/测试命令单独一节 |

- 镜像内**包含 Node 运行时**（agent 需要），不用无头 Electron
- 数据卷 `./data`：数据库 + `picgo/` 配置 + 上传暂存 + `secret.key`
- agent 与 Go 同镜像、同容器，由 Go 拉起（可拆为两个服务）
- **不提供** systemd / 裸机二进制作为官方部署路径（可自行编译，但不在 README 主打）

---

## 附录：管理员权限的两个例外（汇总）

本项目对管理员有两处统一的「跳过」约定，务必在实现时保持一致：

1. **跳过存储配额校验**（D20）
2. **跳过上传限流**（D40）

理由：管理员是运维角色，不应被业务配额与限流卡住。

## 附录：管理员页面隔离要求（D33 补充）

> 主人原话：「私自可见 但管理员页面要把用户图片和自己的图片隔离开 分为两个页面或者最上方按钮切换」

- 普通用户：只能看到**自己的**图片
- 管理员：能看全部，但 UI 上必须**把「我的图片」与「全部用户图片」明确分开**
  （两个页面，或同页面顶部按钮切换）——避免误操作他人的图
- 具体形态已定：**同一页面 + 顶部 Tab 切换**（D71）
