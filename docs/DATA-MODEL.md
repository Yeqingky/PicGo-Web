# 数据模型

> **上位约束**：本文档必须服从 [`DECISIONS.md`](./DECISIONS.md)（尤其 D18–D22、D64–D66、D72、D77、D78）。
> 本文档是**表结构的唯一真源**，其他文档引用表/字段时以本文档为准。

## 0. 总则

### 0.1 双数据库方言

支持 **SQLite（默认）** 与 **PostgreSQL**，通过环境变量切换，表名列名语义完全一致。

```dotenv
PICGO_WEB_DB_DRIVER=sqlite        # sqlite | postgres，默认 sqlite
PICGO_WEB_DB_DSN=                 # 可选，非空时完全覆盖下面的拼装结果

PICGO_WEB_SQLITE_PATH=./data/picgo-web.db

PICGO_WEB_PG_HOST=127.0.0.1
PICGO_WEB_PG_PORT=5432
PICGO_WEB_PG_USER=picgo
PICGO_WEB_PG_PASSWORD=
PICGO_WEB_PG_DBNAME=picgo_web
PICGO_WEB_PG_SSLMODE=disable
PICGO_WEB_PG_TIMEZONE=Asia/Shanghai

PICGO_WEB_DB_MAX_OPEN_CONNS=25
PICGO_WEB_DB_MAX_IDLE_CONNS=5
PICGO_WEB_DB_CONN_MAX_LIFETIME=1h
```

```go
switch cfg.DBDriver {
case "postgres":
    dialector = postgres.Open(dsn)   // gorm.io/driver/postgres（pgx，纯 Go）
default:
    dialector = sqlite.Open(dsn)     // github.com/glebarez/sqlite（纯 Go，无 CGO）
}
```

**硬性要求**：`CGO_ENABLED=0` 必须可编译。

### 0.2 跨方言一致性规则

| 关注点 | 统一做法 | 原因 |
|---|---|---|
| 内部主键 | `uint64` + `primaryKey;autoIncrement` | SQLite `INTEGER PRIMARY KEY` / PgSQL `BIGSERIAL`，GORM 自动适配 |
| **对外标识** | **`UID` `VARCHAR(32)` + `uniqueIndex`**（ULID） | 与自增 ID 解耦；API 一律用 `UID`；便于将来分库分表/归档时 ID 会变而 UID 不变 |
| **命名** | **表名/列名一律 PascalCase（大驼峰）**，缩写词全大写 | 见 **D81**（主人指定）；GORM 需 `NoLowerCase: true` + 每个模型显式 `TableName()` |
| 时间 | **一律 `int64` Unix 秒**（`CreatedAt` / `UpdatedAt` / …） | 彻底避开时区与方言时间类型差异 |
| JSON 语义的列 | **`string` + `gorm:"type:text"`**，由 **service 层显式 `json.Marshal` / `Unmarshal`** | SQLite 无 jsonb；不用 `gorm:"serializer:json"`，避免零值 / nil map 在两方言下的行为差异。列的 Go 类型保持 `string`，服务层负责类型安全。**例外**：`SystemSettings.Value` / `UserSettings.Value` / `ThemeConfigs.Value` 存 JSON 编码的标量或对象，同样由 service 层编解码 |
| 布尔 | `bool` | 两方言均可 |
| 枚举 | **`VARCHAR` 存字符串** | 新增取值不涉及 DDL；可读；避免数字魔法值（D77） |
| 外键 | **只建索引，不建 `FOREIGN KEY` 约束** | 两方言行为差异大；阻碍分表/归档；一致性由应用层保证 |
| 删除 | **硬删除**（D46） | 无软删除，无回收站 |
| 扩展位 | 主要业务表带 **`Metadata` JSON 列** | 新字段可先落此，无需迁移（D77） |
| NOT NULL | 除语义必需外**允许 NULL** | 避免将来加列要回填历史数据 |

> 明确**不使用** `gorm.io/datatypes`（依赖 MySQL 方言）。

#### GORM 配置（实现时必读）

```go
// internal/database/gorm.go
gorm.Open(dialector, &gorm.Config{
    NamingStrategy: schema.NamingStrategy{
        NoLowerCase: true,   // ← 关键：不做 snake_case 转换
    },
})
```

- **每个模型显式实现 `TableName()`**，不依赖词形变化库（避免 inflection 单复数意外）：
  ```go
  func (User) TableName() string { return "Users" }
  func (SchemaMeta) TableName() string { return "SchemaMeta" }  // 单数，刻意
  ```
- ⚠️ **PostgreSQL 会把未加引号的标识符折成小写**。GORM 生成的 SQL 总带引号，
  应用层无影响；但**手写原生 SQL 必须自己加双引号**，否则报
  `relation "users" does not exist`。
- SQLite 标识符对 ASCII 大小写不敏感，不影响本地开发。

### 0.3 分表原则（D78）

**同一种语义只放一张表；不同生命周期 / 敏感级 / 体量的内容一律拆表。**

| 拆表依据 | 本项目实例 |
|---|---|
| 敏感级不同 | `StorageSecrets` 独立于 `StorageConfigs` —— 日常查询永远选不到密钥 |
| 体量不同 | `UploadResults`（完整上传返回值，大 JSON）独立于 `Uploads`（热点列表字段） |
| 写入频率不同 | `LoginAttempts` / `JobLogs` / `OperationLogs` 高频写，独立于主表 |
| 生命周期不同 | `JobLogs`（短期清理）vs `OperationLogs`（保留 180 天） |
| 可选性不同 | `UserProfiles`（昵称/头像）独立于 `Users`（邮箱/密码/角色） |
| 一对多关系 | `EmailLogs` 独立成表，不塞进 JSON |

## 1. 表清单（20 张）

```
身份鉴权  Users / UserProfiles / OAuthIdentities / RefreshTokens / APITokens / LoginAttempts
存储配置  StorageConfigs / StorageSecrets
主题配置  ThemeConfigs
媒体资源  Uploads / UploadResults
任务执行  Jobs / JobItems / JobLogs
审计记录  OperationLogs / EmailLogs
系统配置  SystemSettings / UserSettings
插件缓存  Plugins
迁移版本  SchemaMeta
```

**模型 ↔ 表名映射**（每个模型显式实现 `TableName()`，不依赖词形变化库）：

| 模型 | 表名 | 模型 | 表名 |
|---|---|---|---|
| `User` | `Users` | `Job` | `Jobs` |
| `UserProfile` | `UserProfiles` | `JobItem` | `JobItems` |
| `OAuthIdentity` | `OAuthIdentities` | `JobLog` | `JobLogs` |
| `RefreshToken` | `RefreshTokens` | `OperationLog` | `OperationLogs` |
| `APIToken` | `APITokens` | `EmailLog` | `EmailLogs` |
| `LoginAttempt` | `LoginAttempts` | `SystemSetting` | `SystemSettings` |
| `StorageConfig` | `StorageConfigs` | `UserSetting` | `UserSettings` |
| `StorageSecret` | `StorageSecrets` | `Plugin` | `Plugins` |
| `ThemeConfig` | `ThemeConfigs` | | |
| `Upload` | `Uploads` | `SchemaMeta` | `SchemaMeta`（单数，刻意） |
| `UploadResult` | `UploadResults` | | |

---

## 2. 身份鉴权域

### 2.1 `Users` — 身份核心

只放**认证与授权必需**的字段；展示类信息在 `UserProfiles`。

```go
type User struct {
    ID                 uint64 `gorm:"primaryKey;autoIncrement"`
    UID                string `gorm:"size:32;uniqueIndex;not null"`
    Email              string `gorm:"size:255;uniqueIndex;not null"`
    PasswordHash       string `gorm:"size:255"`                        // bcrypt cost=12；纯 OAuth 用户为空
    Role               string `gorm:"size:16;not null;default:user"`   // admin | user
    Status             string `gorm:"size:16;not null;default:active"` // active | disabled
    CapacityBytes      int64  `gorm:"not null;default:0"`              // 0 = 不限额（D20）
    UsedBytes          int64  `gorm:"not null;default:0"`
    MustChangePassword bool   `gorm:"not null;default:false"`
    LastLoginAt        int64  `gorm:"not null;default:0"`
    Metadata           string `gorm:"type:text"`                       // JSON 扩展位
    CreatedAt          int64  `gorm:"not null"`
    UpdatedAt          int64  `gorm:"not null"`
}
```

**为什么拆 `UserProfiles`**：邮箱/密码/角色是**认证数据**（查询少、绝不能泄露），
昵称/头像是**展示数据**（详情页才读）。拆开后列表查询天然不会带出敏感列。

### 2.2 `UserProfiles` — 展示信息（1:1）

```go
type UserProfile struct {
    ID        uint64 `gorm:"primaryKey;autoIncrement"`
    UserUID   string `gorm:"size:32;uniqueIndex;not null"`
    Nickname  string `gorm:"size:128"`
    AvatarURL string `gorm:"size:512"`
    Homepage  string `gorm:"size:512"`
    Locale    string `gorm:"size:16;default:zh-CN"`
    Metadata  string `gorm:"type:text"`
    CreatedAt int64  `gorm:"not null"`
    UpdatedAt int64  `gorm:"not null"`
}
```

### 2.3 `OAuthIdentities` — 已绑定的第三方身份

一个用户可绑定多个；**必须先绑定才能用于登录**（D27）。

```go
type OAuthIdentity struct {
    ID             uint64 `gorm:"primaryKey;autoIncrement"`
    UID            string `gorm:"size:32;uniqueIndex;not null"`
    UserUID        string `gorm:"size:32;index;not null"`
    Provider       string `gorm:"size:32;not null"`      // 本项目只支持 github（D26）
    ProviderUserID string `gorm:"size:191;not null"`     // GitHub 的数字 ID（稳定唯一标识，D28）
    ProviderLogin  string `gorm:"size:191"`              // GitHub username（仅展示）
    ProviderEmail  string `gorm:"size:255"`
    AvatarURL      string `gorm:"size:512"`
    Metadata       string `gorm:"type:text"`
    CreatedAt      int64  `gorm:"not null"`
    UpdatedAt      int64  `gorm:"not null"`
}
// uniqueIndex: (Provider, ProviderUserID)
```

### 2.4 `RefreshTokens` — 会话（可吊销、可轮换）

```go
type RefreshToken struct {
    ID        uint64 `gorm:"primaryKey;autoIncrement"`
    UID       string `gorm:"size:32;uniqueIndex;not null"`
    UserUID   string `gorm:"size:32;index;not null"`
    TokenHash string `gorm:"size:64;uniqueIndex;not null"` // sha256 hex
    UserAgent string `gorm:"size:255"`
    ClientIP  string `gorm:"size:64"`
    ExpiresAt int64  `gorm:"index;not null"`
    RevokedAt int64  `gorm:"not null;default:0"`
    CreatedAt int64  `gorm:"not null"`
}
```

### 2.5 `APITokens` — 长期令牌

```go
type APIToken struct {
    ID         uint64 `gorm:"primaryKey;autoIncrement"`
    UID        string `gorm:"size:32;uniqueIndex;not null"`
    UserUID    string `gorm:"size:32;index;not null"`
    Name       string `gorm:"size:64;not null"`
    TokenHash  string `gorm:"size:64;uniqueIndex;not null"` // sha256(明文)
    Prefix     string `gorm:"size:16;not null"`             // pcw_xxxxxxxx，用于展示
    LastUsedAt int64  `gorm:"not null;default:0"`
    ExpiresAt  int64  `gorm:"not null;default:0"`           // 0 = 永不过期
    CreatedAt  int64  `gorm:"not null"`
}
```

### 2.6 `LoginAttempts` — 登录尝试（限流 + 审计）

**独立表**（而不是计数器列）：写入频繁、需要按时间窗查询、可定期清理。

```go
type LoginAttempt struct {
    ID        uint64 `gorm:"primaryKey;autoIncrement"`
    Email     string `gorm:"size:255;index;not null"`
    ClientIP  string `gorm:"size:64;index;not null"`
    Success   bool   `gorm:"not null;default:false"`
    UserAgent string `gorm:"size:255"`
    CreatedAt int64  `gorm:"not null;index"`
}
```

用途：`security.loginMaxAttempts`（默认 5 次 / 5 分钟）判定，按 `(email, client_ip)` 统计近窗成功与否。

---

## 3. 存储配置域

### 3.1 `StorageConfigs` — 存储配置元数据（**不含任何密钥**）

同一驱动类型可有**多条**实例（D64）。

```go
type StorageConfig struct {
    ID             uint64 `gorm:"primaryKey;autoIncrement"`
    UID            string `gorm:"size:32;uniqueIndex;not null"`        // st_ 前缀的 ULID
    Name           string `gorm:"size:64;not null"`                    // 展示名，全局唯一
    Type           string `gorm:"size:64;index;not null"`              // 驱动类型：github / webdav / s3 / ...
    PicgoConfigName string `gorm:"size:64;not null;default:Default"`   // 映射 picgo _configName（D65）
    Enabled        bool   `gorm:"not null;default:true"`
    IsDefault      bool   `gorm:"not null;default:false"`              // 全局同时只有一条为 true
    PathTemplate   string `gorm:"size:255"`                            // 魔法路径（D43，每配置独立）
    FileTemplate   string `gorm:"size:255"`                            // 魔法文件名
    Capabilities   string `gorm:"type:text"`                           // JSON：运行时探测的驱动能力
    Metadata       string `gorm:"type:text"`                           // JSON 扩展位
    CreatedAt      int64  `gorm:"not null"`
    UpdatedAt      int64  `gorm:"not null"`
}
// uniqueIndex: (Type, PicgoConfigName)
```

`Capabilities` JSON 结构（运行时由 agent 探测并缓存，**不硬编码驱动类型名** — D77）：

```jsonc
{
  "SupportsPathTemplate": true,      // 驱动是否支持自定义远端路径（D44）
  "SupportsRemoteDelete": true,      // 插件是否实现了 remove 事件（D47）
  "ServerRenames": false,            // 图床是否无视传入文件名、服务端自行命名（如 NodeImage 短链）
                                     // —— picgo 协议不声明，由上传结果运行时探测回写（只置位不回退）
  "ConfigFields": ["repo", "token", "path", "branch", "customUrl"],  // 该驱动的配置字段
  "PathFieldNames": ["path", "root", "basePath"],                    // 推断依据
  "DetectedAt": 1789347956,
  "PicgoVersion": "3.0.2"
}
```

> **JSON 列内部的键也用 PascalCase**（D81）：这些列会经 API 直接暴露，
> 内外命名不一致会让调试变痛苦。
> **唯一例外**：驱动自身的配置字段名（`ConfigFields` 里的 `repo` / `token` / `path` …）
> 是 **picgo 与插件定义的**，一律保持原样，不得转换。

### 3.2 `StorageSecrets` — 凭据（1:1，加密）

**独立表的原因（D78 敏感级不同）**：凭据是 AES-256-GCM 密文，
放在独立表后，任何针对 `StorageConfigs` 的列表/统计查询都**不可能误带出密钥**。

```go
type StorageSecret struct {
    ID               uint64 `gorm:"primaryKey;autoIncrement"`
    StorageUID       string `gorm:"size:32;uniqueIndex;not null"`
    EncryptedPayload string `gorm:"type:text;not null"` // AES-256-GCM(JSON)，含 token/secret/password
    KeyVersion       int    `gorm:"not null;default:1"` // 主密钥轮换支持
    CreatedAt        int64  `gorm:"not null"`
    UpdatedAt        int64  `gorm:"not null"`
}
```

**主密钥不进数据库**（D19）：来源 `PICGO_WEB_SECRET_KEY` 环境变量，
或首次启动生成 `data/secret.key`（0600）。

---

### 3.3 `ThemeConfigs` — 主题配置（D95，**不在 SystemSettings**）

```go
type ThemeConfig struct {
    ID        uint64 `gorm:"primaryKey;autoIncrement"`
    ThemeID   string `gorm:"size:64;index;not null"`              // 主题 ID（如 default）
    Key       string `gorm:"size:128;not null"`                   // 配置项名（如 BackgroundURL）
    Value     string `gorm:"type:text"`                           // JSON 编码的值
    ValueType string `gorm:"size:16;not null;default:string"`      // string|text|number|switch|select|json（与 manifest 的 Type 对应）
    UpdatedBy string `gorm:"size:32"`                             // 操作者 UserUID
    CreatedAt int64  `gorm:"not null"`
    UpdatedAt int64  `gorm:"not null"`
}
// uniqueIndex: (ThemeID, Key)
```

**为什么独立表**（D78 拆表原则）：**生命周期不同**（随主题装卸而生灭）、**一对多关系**（一主题多键）、
**可审计**（每行带 `UpdatedBy`/`UpdatedAt`，能回答「谁在何时改了哪个键」）。

| 行为 | 规则 |
|---|---|
| 键集合 | 由该主题 `manifest.json` 的 `Configuration.Items` 声明，**Go 侧不硬编码** |
| 读取 | **DB 值 → manifest `Default` → 类型零值**（三级兜底） |
| 写入 | 只接受**该主题声明过的键**；类型按 `Type` 校验；未声明的键 → `40001` |
| 并发 | 每键独立行，两个管理员改不同键**不互相覆盖** |
| 换主题 | 旧主题的值保留（`ThemeID` 不同），切回来仍生效 |
| 卸载主题 | **不自动删值**；提供显式清理（`WHERE ThemeID = ?`） |

**`theme.active` 不在此表** —— 它是站点级选择，存 `SystemSettings`（见 §7.4）。

## 4. 媒体资源域

### 4.1 `Uploads` — 图片元数据（热点表）

```go
type Upload struct {
    ID           uint64 `gorm:"primaryKey;autoIncrement"`
    UID          string `gorm:"size:32;uniqueIndex;not null"`     // up_ 前缀的 ULID
    UserUID      string `gorm:"size:32;index;not null"`
    StorageUID   string `gorm:"size:32;index;not null"`           // 引用 storage_configs.uid

    FileName     string `gorm:"size:255;not null"`                // 最终文件名（含扩展名）
    OriginalName string `gorm:"size:255"`                         // 原始上传文件名
    AliasName    string `gorm:"size:255"`                         // 用户重命名（展示优先用 alias）

    Size         int64  `gorm:"not null;default:0"`               // 字节
    MimeType     string `gorm:"size:127"`
    Extension    string `gorm:"size:32;index"`
    Width        int    `gorm:"not null;default:0"`
    Height       int    `gorm:"not null;default:0"`
    SHA256       string `gorm:"size:64;index"`                    // 仅记录，不建唯一索引（D66 不去重）

    URL          string `gorm:"size:1024"`
    ThumbURL     string `gorm:"size:1024"`

    Status       string `gorm:"size:16;index;not null;default:pending"` // pending|success|failed
    Error        string `gorm:"type:text"`
    Source       string `gorm:"size:16;not null;default:web"`      // web | api | lsky
    JobUID       string `gorm:"size:32;index"`                     // 归属批次

    Metadata     string `gorm:"type:text"`
    CreatedAt    int64  `gorm:"not null;index"`
    UpdatedAt    int64  `gorm:"not null"`
}
```

> ⚠️ **本表没有「可见性 / visibility」列，这是刻意设计（D33）**。
> 图片归属完全靠 `UserUID` 过滤（普通用户只看自己的，管理员用 `scope=mine|all` 切换）。
> 因为后端不存储、不分发图片，图片本身的公开性由**图床（PicGo 驱动）**决定，
> 本项目无权也无法控制。**不要后续添加 `visibility` 列**——那会与 D33 的语义重复且无法真正生效。

> ⚠️ **没有 `(storage_uid, sha256)` 唯一索引** —— D66 已取消去重。

### 4.2 `UploadResults` — 完整上传返回值（1:1）

**拆表原因（D78 体量不同）**：picgo 的 `IImgInfo` 完整对象可能很大，
且**只在删除远端文件时才需要**（D47 要求把插件回写的 `sha` 等字段留住）。
热点表 `Uploads` 不带这个字段，列表查询更轻。

```go
type UploadResult struct {
    ID        uint64 `gorm:"primaryKey;autoIncrement"`
    UploadUID string `gorm:"size:32;uniqueIndex;not null"`
    RawOutput string `gorm:"type:text;not null"` // JSON：完整 IImgInfo[]
    FilePath  string `gorm:"size:512"`           // 本地暂存路径（若仍存在）
    CreatedAt int64  `gorm:"not null"`
}
```

`RawOutput` 示例（含插件回写字段，删除时必需）：

```jsonc
// ⚠️ 这里的字段名是 picgo-core 的 IImgInfo 定义，一律保持原样，
// 不适用 D81 大驼峰（见 D81.3 第 5 条）。
[{ "fileName": "a.png", "extname": ".png", "imgUrl": "https://...",
   "width": 800, "height": 600, "size": 12345, "type": "github",
   "sha": "abc123..." }]
```

> **为何原样存**：删除远端文件时要把它原封不动交回插件
> （D47 的 `remove` 事件），字段名被改过插件就认不出来了。

### 4.3（已移除，D101）~~`Albums`~~ — 相册

> 相册功能已整体移除（D101）：本表不再建，初始迁移直接不含它。
> 章节号保留，避免既有交叉引用失配。

---

## 5. 任务执行域

### 5.1 `Jobs` — 批次任务

```go
type Job struct {
    ID              uint64 `gorm:"primaryKey;autoIncrement"`
    UID             string `gorm:"size:32;uniqueIndex;not null"`       // job_ 前缀
    Kind            string `gorm:"size:32;index;not null"`             // upload|plugin.install|...
    Status          string `gorm:"size:16;index;not null"`             // queued|running|succeeded|failed
    Progress        int    `gorm:"not null;default:0"`                 // 0..100

    UserUID         string `gorm:"size:32;index"`
    StorageUID      string `gorm:"size:32;index"`                      // 一批一驱动（D38）

    TotalItems      int    `gorm:"not null;default:0"`
    SucceededItems  int    `gorm:"not null;default:0"`
    FailedItems     int    `gorm:"not null;default:0"`
    SkippedItems    int    `gorm:"not null;default:0"`

    Payload         string `gorm:"type:text"`                          // JSON 请求上下文
    Result          string `gorm:"type:text"`                          // JSON 汇总结果
    Error           string `gorm:"type:text"`

    CreatedAt       int64  `gorm:"not null;index"`
    StartedAt       int64  `gorm:"not null;default:0"`
    FinishedAt      int64  `gorm:"not null;default:0"`
}
```

> job 状态**只有 4 个**，不要 `partial`（D37）：有 item 失败即 `failed`，
> 成功项的 URL 照样在 `Result` 里回传。

### 5.2 `JobItems` — 批次内单个文件

```go
type JobItem struct {
    ID         uint64 `gorm:"primaryKey;autoIncrement"`
    JobUID     string `gorm:"size:32;index;not null"`
    Seq        int    `gorm:"not null"`                        // 批次内顺序，保证展示稳定
    UploadUID  string `gorm:"size:32;index"`
    FileName   string `gorm:"size:255"`
    Status     string `gorm:"size:16;index;not null"`           // queued|running|succeeded|failed
    Attempts   int    `gorm:"not null;default:0"`
    Error      string `gorm:"type:text"`
    StartedAt  int64  `gorm:"not null;default:0"`
    FinishedAt int64  `gorm:"not null;default:0"`
}
```

**并发限制作用在 item 层**（D36）。driver 信息只在 `Jobs` 层（D38 一批一驱动），
`JobItems` 不重复存。

### 5.3 `JobLogs` — 任务执行日志（可短期清理）

**拆表原因（D78 生命周期不同）**：任务日志是逐行输出（npm install、上传过程），
体量大、写完很快就不再需要，与 `OperationLogs`（审计、保留 180 天）完全不同。

```go
type JobLog struct {
    ID        uint64 `gorm:"primaryKey;autoIncrement"`
    JobUID    string `gorm:"size:32;index;not null"`
    Seq       int    `gorm:"not null"`
    Line      string `gorm:"type:text"`
    CreatedAt int64  `gorm:"not null"`
}
```

---

## 6. 审计记录域

### 6.1 `OperationLogs` — 统一操作日志（D45）

```go
type OperationLog struct {
    ID         uint64 `gorm:"primaryKey;autoIncrement"`
    UID        string `gorm:"size:32;uniqueIndex;not null"`     // 日志唯一 ID（log_ 前缀 ULID）
    Type       string `gorm:"size:64;index;not null"`           // upload | mail.send | user.create | ...
    Status     string `gorm:"size:16;index;not null"`           // success | failed

    UserUID    string `gorm:"size:32;index"`                    // 操作者（系统操作为空）
    Username   string `gorm:"size:255"`                         // 冗余，便于展示与搜索
    TargetType string `gorm:"size:32;index"`                    // upload | user | storage | plugin | ...
    TargetUID  string `gorm:"size:32;index"`                    // 操作对象 uid

    Detail     string `gorm:"type:text"`                        // JSON：成功时的上下文
    Error      string `gorm:"type:text"`                        // 失败原因（失败时必填）
    ClientIP   string `gorm:"size:64"`
    UserAgent  string `gorm:"size:255"`
    CreatedAt  int64  `gorm:"not null;index"`
}
```

**已确定的类型清单**（可扩展，新增无需迁移）：

`OperationLogs.Type` 只保存稳定的小写字符串标识, 不保存语言相关的显示名称。
前端根据该标识通过 i18n 映射展示文案; 新增类型不应把中文或其它语言写入数据库。

| Type | 触发点 |
|---|---|
| `upload` | 上传成功 / 失败 |
| `image.delete` | 删除图片（含远端是否删成功） |
| `image.update` | 重命名 |
| `mail.send` | 邮件发送成功 / 失败 |
| `user.create` | 账号创建 |
| `user.delete` | 账号注销 |
| `user.update` | 修改配额 / 状态 / 密码 / 角色 |
| `storage.create` | 新建存储配置 |
| `storage.update` | 修改存储配置 |
| `storage.delete` | 删除存储配置 |
| `plugin.install` | 安装插件 |
| `plugin.uninstall` | 卸载插件 |
| `plugin.update` | 更新插件 |
| `auth.login` | 登录成功 |
| `auth.failed` | 登录失败 |
| `auth.logout` | 登出 |
| `setting.update` | 修改系统设置 |
| `system.log.cleanup` | 日志清理任务自身 |

**保留策略**：默认 180 天（D74），`log.retentionDays` 可改，`0` = 永久。
**过滤/搜索**：按 `type` 过滤 + 关键词匹配（`Username` / `TargetUID` / `Detail` / `Error`）。

### 6.2 `EmailLogs` — 邮件发送记录

> 主人要求：「邮件也需要做日志 发了什么邮件 不需要详细邮件原文件」

**拆表原因**：邮件是独立的一对多事件，且前端要单独列表展示。

```go
type EmailLog struct {
    ID        uint64 `gorm:"primaryKey;autoIncrement"`
    UID       string `gorm:"size:32;uniqueIndex;not null"`
    ToAddress string `gorm:"size:255;index;not null"`
    Subject   string `gorm:"size:255"`
    Template  string `gorm:"size:64;index"`        // invite | reset_password | ...
    Status    string `gorm:"size:16;index;not null"` // success | failed
    Error     string `gorm:"type:text"`
    RelatedUserUID string `gorm:"size:32;index"`
    CreatedAt int64  `gorm:"not null;index"`
}
```

> **不存邮件正文**（用户明确要求）。

---

## 7. 系统配置域

### 7.1 `SystemSettings` — 站点级配置（KV）

**用 KV 表而不是列的原因（D77）**：新增配置项**不需要迁移**。

```go
type SystemSetting struct {
    ID        uint64 `gorm:"primaryKey;autoIncrement"`
    Key       string `gorm:"size:128;uniqueIndex;not null"`
    Value     string `gorm:"type:text"`
    ValueType string `gorm:"size:16;not null;default:string"` // string|int|bool|json|secret
    Encrypted bool   `gorm:"not null;default:false"`
    Category  string `gorm:"size:32;index"`                   // site|upload|mail|oauth|security|log|picgo|user
    UpdatedBy string `gorm:"size:32"`
    CreatedAt int64  `gorm:"not null"`
    UpdatedAt int64  `gorm:"not null"`
}
```

### 7.2 `UserSettings` — 用户级偏好（KV）

```go
type UserSetting struct {
    ID        uint64 `gorm:"primaryKey;autoIncrement"`
    UserUID   string `gorm:"size:32;index;not null"`
    Key       string `gorm:"size:128;not null"`
    Value     string `gorm:"type:text"`
    ValueType string `gorm:"size:16;not null;default:string"`
    CreatedAt int64  `gorm:"not null"`
    UpdatedAt int64  `gorm:"not null"`
}
// uniqueIndex: (UserUID, Key)
```

### 7.3 配置读取

三级分层（D18）：

```
环境变量 / .env      启动引导类，只读
      ↓ 覆盖
system_settings 表   业务配置，DB 为真相源
      ↓ 覆盖
代码默认值            internal/config/defaults.go
```

统一走 `SettingsService.GetInt/GetString/GetJSON/Set(key, def)`；
写入后触发 `onChanged` 回调（写 `picgo.*` 时自动推送到 agent，
写 `upload.rateLimit.*` 时刷新内存限流器）。

### 7.4 配置键位表

#### 站点 `category=site`

| 键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `site.name` | string | `PicGo Web` | 站点名称 |
| `site.subtitle` | string | `""` | 副标题（首页 Hero 与顶栏） |
| `site.baseUrl` | string | `""` | 对外访问地址（OAuth 回调、图片直链拼接） |
| `site.description` | string | `""` | 站点描述（`meta description`） |
| `site.keywords` | string | `""` | 站点关键词（`meta keywords`） |
| `site.notice` | string | `""` | 站点公告（Markdown） |
| `site.icp` | string | `""` | 备案号 |
| `site.iconUrl` | string | `""` | 站点图标 URL（favicon） |

#### 主题配置：**独立表 `ThemeConfigs`**（D95，不在 SystemSettings）

> ⚠️ 主题配置**不存 `SystemSettings`**，而是独立的 `ThemeConfigs` 表（见 §3.3）。
> 理由（D78 拆表原则）：**生命周期不同**（随主题装卸而生灭）+ **一对多关系**（一主题多键）+ **可审计**（每行带 `UpdatedBy`）。
>
> `SystemSettings` 里**只保留一个**主题相关的键：

| 键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `theme.active` | string | `default` | **当前启用的主题 ID**。它是站点级选择（不随主题装卸变化），故留在 `SystemSettings` |

**每个主题的具体配置键**由该主题 `manifest.json` 的 `Configuration.Items` 声明，**Go 侧不硬编码**（D95/D98）。
以默认主题为例：

| 键（`ThemeID = default`） | 类型 | 默认 | 说明 |
|---|---|---|---|
| `BackgroundURL` | string | `https://api.yppp.net/api.php` | 背景图地址。**单一 URL，无模式判断**（D97）；留空或加载失败则不显示背景 |
| `ShowHomeFeatures` | bool | `true` | 是否显示首页「核心能力」区块 |
| `HomepageFeatures` | json | `[]` | 核心能力卡片：`[{Icon, Title, Desc}]`，建议 4~5 条 |
| `HomepageScenarios` | json | `[]` | 应用场景：`[{Icon, Title, Desc}]`，建议 3~4 条 |
| `HomepageFaq` | json | `[]` | 常见问题：`[{Question, Answer}]` |

**读取顺序**：**DB 值（`ThemeConfigs`） → manifest 的 `Default` → 类型零值**（三级兜底，与 D18 一致）。
**换主题不丢值**（`ThemeID` 前缀不同）；**卸载主题不自动删值**，另有显式清理端点。

> **哪些主题信息不入库**：`ID` / `Name` / `Description` / `Author` / `Version` / `URL` / `Preview` /
> **`Pages`（接管范围，D94.2）** / `MinAppVersion` / `Configuration.Type` / `Configuration.Items`（字段定义）
> 全部以 **manifest 文件为真相源**，**不落库**（D94：「不建 Themes 表」）。
> 只有**用户填写的配置值**进 `ThemeConfigs`；只有**当前选了哪个主题**进 `SystemSettings.theme.active`。

#### 用户 `category=user`（D21）

| 键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `user.defaultCapacityBytes` | int | `5368709120`（5 GiB） | **新建用户默认配额**；只影响此后新建的用户 |
| `user.unlimitedCapacity` | bool | `false` | `true` 时新用户不限额 |
| `user.defaultStatus` | string | `active` | 新用户默认状态 |
| `user.allowSelfRegistration` | bool | `false` | 自助注册开关（**默认关闭**，D25）<br>⚠️ **预留开关**：D25 明确不做自助注册流程，本键仅为将来可能的邀请制/半公开场景预留，**实现时不要为它写注册流程** |

#### 上传 `category=upload`

| 键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `upload.maxSizeBytes` | int | `20971520` | 单文件上限（20 MiB） |
| `upload.allowedExts` | json | `["jpg","jpeg","png","gif","webp","bmp","svg","ico","avif"]` | 扩展名白名单 |
| `upload.blockSvg` | bool | `true` | 默认禁止上传 SVG；显式关闭后才允许（仍需在白名单中） |
| `upload.concurrency` | int | `1` | 队列并发度；`>1` 需 picgo-core 补丁（D35） |
| `upload.retryTimes` | int | `1` | 单文件失败重试次数 |
| `upload.retryBackoffMs` | int | `2000` | 重试退避基数（指数增长） |
| `upload.queueMaxLength` | int | `1000` | 队列上限，超出返 `42901` |
| `upload.itemTimeoutSeconds` | int | `300` | 单文件超时 |
| `upload.shutdownGraceSeconds` | int | `30` | 优雅关闭等待时长 |
| `upload.rateLimit.enabled` | bool | **`false`** | 上传限流总开关（**默认禁用**，D73） |
| `upload.rateLimit.perHour` | int | `100` | 每用户每小时最多张数 |
| `upload.rateLimit.perDay` | int | `500` | 每用户每天最多张数 |
| `upload.rateLimit.action` | string | `reject` | `reject`（拒绝）/ `log`（仅记录） |
| `upload.keepLocalCopy` | bool | `false` | 上传后是否保留本地暂存文件 |
| `upload.keepLocalDays` | int | `7` | 保留天数（`keepLocalCopy=false` 时忽略） |

#### 邮件 `category=mail`

| 键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `mail.enabled` | bool | `false` | 邮件功能总开关 |
| `mail.host` | string | `""` | SMTP 主机 |
| `mail.port` | int | `465` | 端口 |
| `mail.encryption` | string | `ssl` | `ssl` / `starttls` / `none` |
| `mail.username` | string | `""` | |
| `mail.password` | secret | `""` | **加密存储** |
| `mail.fromAddress` | string | `""` | 发件地址 |
| `mail.fromName` | string | `PicGo Web` | 发件人显示名 |

#### OAuth `category=oauth`

| 键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `oauth.github.enabled` | bool | `false` | 是否启用 GitHub 登录按钮（D26） |
| `oauth.github.clientId` | string | `""` | |
| `oauth.github.clientSecret` | secret | `""` | **加密存储** |
| `oauth.autoBindByEmail` | bool | `false` | 同邮箱自动绑定到已有账号（默认关，更安全） |

> 回调地址展示给管理员：`<site.baseUrl>/api/web/v1/auth/oauth/github/callback`

#### 安全 `category=security`

| 键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `security.sessionTtlHours` | int | `168` | refresh token 有效期（7 天） |
| `security.accessTokenTtlMinutes` | int | `15` | access token 有效期 |
| `security.loginMaxAttempts` | int | `5` | 登录失败次数阈值 |
| `security.loginWindowMinutes` | int | `5` | 登录限流时间窗 |

#### 日志 `category=log`

| 键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `log.retentionDays` | int | `180` | 操作日志保留天数（`0` = 永久，D74） |
| `log.jobRetentionDays` | int | `7` | 任务日志保留天数 |

#### PicGo `category=picgo`

| 键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `picgo.npmRegistry` | string | `https://registry.npmmirror.com` | 插件安装源 |
| `picgo.npmProxy` | string | `""` | npm 代理 |
| `picgo.uploadProxy` | string | `""` | 上传代理 |
| `picgo.transformer` | string | `path` | 全局 transformer |
| `picgo.configPath` | string | `<dataDir>/picgo/config.json` | picgo 配置路径 |

#### 对外集成 `category=integration`

| 键 | 类型 | 默认 | 说明 |
|---|---|---|---|
| `integration.lsky.enabled` | bool | `true` | 是否挂载 Lsky v1 兼容层（D52） |
| `integration.lsky.deleteRemoteOnDelete` | bool | `false` | Lsky 的 `DELETE /images/{key}` 是否同步删远端（该契约本身无此参数，故用开关表达） |
| `integration.lsky.tokenTtlDays` | int | `365` | Lsky token 有效期 |

---

## 8. 其他

### 8.1 `Plugins` — 插件缓存

npm 包的真实状态在 `node_modules`（agent 是真相源），此表只做展示缓存。

```go
type Plugin struct {
    ID          uint64 `gorm:"primaryKey;autoIncrement"`
    Name        string `gorm:"size:191;uniqueIndex;not null"` // picgo-plugin-x / @s/x
    Version     string `gorm:"size:32"`
    Description string `gorm:"type:text"`
    Author      string `gorm:"size:191"`
    Homepage    string `gorm:"size:512"`
    Uploader    string `gorm:"size:64"`
    Transformer string `gorm:"size:64"`
    Enabled     bool   `gorm:"not null;default:true"`
    GuiOnly     bool   `gorm:"not null;default:false"` // 含 guiMenu/commands，Web 端不可用
    Metadata    string `gorm:"type:text"`
    InstalledAt int64  `gorm:"not null"`
    UpdatedAt   int64  `gorm:"not null"`
}
```

刷新时机：启动、插件任务成功后、`GET /plugins?refresh=true`。

### 8.2 `SchemaMeta` — 迁移版本

```go
type SchemaMeta struct {
    ID        uint64 `gorm:"primaryKey"`
    Version   int64  `gorm:"not null"`
    UpdatedAt int64  `gorm:"not null"`
}
```

---

## 9. 迁移

### 9.1 机制

```go
// internal/database/migrations.go
var schemaMigrations = []Migration{
    {Version: 1, Name: "init",       Up: migrateV1},
    // 只追加，不修改已发布条目（D77）
}
```

启动流程：

1. 读 `SchemaMeta.Version`（无表视为 `0`）
2. 按 `Version` **升序**执行大于当前版本的迁移
3. 每条迁移在**单个事务**内完成
4. 成功后写回 `SchemaMeta.Version`
5. 失败则回滚并终止启动（`PICGO_WEB_DB_AUTO_MIGRATE=false` 时仅告警）

需要写原生 SQL 时按方言分支。**注意：标识符必须加双引号**（D81.4 —— PostgreSQL
会把未加引号的标识符折成小写，不加引号会报 `relation "uploads" does not exist`）：

```go
func migrateV3(tx *gorm.DB) error {
    if tx.Dialector.Name() == "postgres" {
        return tx.Exec(`CREATE INDEX IF NOT EXISTS idx_uploads_user_created ON "Uploads" ("UserUID", "CreatedAt" DESC)`).Error
    }
    return tx.Exec(`CREATE INDEX IF NOT EXISTS idx_uploads_user_created ON "Uploads" ("UserUID", "CreatedAt")`).Error
}
```

> SQLite 对 ASCII 大小写不敏感，两种写法等价；为一致性**建议两方言统一加双引号**。

### 9.2 版本号约定

`SchemaMeta.Version` 与程序版本**解耦**（整数序列，只增不减）。
发布时在 `CHANGELOG.md` 记录「程序版本 ↔ schema 版本」对应关系。

---

## 10. 索引清单

> 列名均为 PascalCase（D81）。索引名保持 snake_case 前缀 `idx_`，
> 仅为运维可读性，不参与 ORM 映射。

```
Users(Email) unique                      登录查询
Users(UID) unique                        API 引用
Users(Status)                            管理列表

UserProfiles(UserUID) unique

OAuthIdentities(Provider, ProviderUserID) unique
OAuthIdentities(UserUID)

RefreshTokens(TokenHash) unique          刷新校验
RefreshTokens(UserUID)
RefreshTokens(ExpiresAt)                 过期清理

APITokens(TokenHash) unique              鉴权
APITokens(UserUID)

LoginAttempts(Email, ClientIP, CreatedAt)  限流窗口统计

StorageConfigs(UID) unique
StorageConfigs(Type, PicgoConfigName) unique
StorageConfigs(Enabled, IsDefault)       启动 reconcile
StorageSecrets(StorageUID) unique

ThemeConfigs(ThemeID, Key) unique         主题配置读取
ThemeConfigs(ThemeID)                     卸载时清理

Uploads(UID) unique
Uploads(UserUID, CreatedAt DESC)         图库列表（主查询）
Uploads(StorageUID)                      按驱动筛选
Uploads(Status)                          失败重试
Uploads(JobUID)                          批次内图片
Uploads(SHA256)                          自查（非唯一）
UploadResults(UploadUID) unique

Jobs(UID) unique
Jobs(Status, CreatedAt)                  任务面板
Jobs(UserUID, CreatedAt)
JobItems(JobUID, Seq)                    批次内顺序
JobLogs(JobUID, Seq)

OperationLogs(UID) unique
OperationLogs(Type, CreatedAt)           类型过滤
OperationLogs(CreatedAt)                 保留清理
OperationLogs(UserUID)
OperationLogs(TargetType, TargetUID)

EmailLogs(UID) unique
EmailLogs(ToAddress)
EmailLogs(CreatedAt)

SystemSettings(Key) unique
UserSettings(UserUID, Key) unique

Plugins(Name) unique
```

---

## 11. 扩展性检查清单

新增功能前对照（D77）：

- [ ] 新字段能否放 `Metadata` JSON，避免迁移？
- [ ] 是新配置项吗？→ 写 `SystemSettings` / `UserSettings`，**不需要迁移**
- [ ] 是新枚举取值吗？→ 字符串列直接加值，**不需要 DDL**
- [ ] 是新内容域吗？→ **新建独立表**（D78），不要塞进现有表的 JSON
- [ ] 对外暴露了吗？→ 用 `UID` 而非自增 `ID`
- [ ] 需要跨表引用吗？→ 存 `UID` 字符串 + 建索引，**不建外键约束**
- [ ] 删除/改语义了吗？→ **不允许**；新字段替代，旧字段标记 Deprecated 保留
- [ ] 驱动相关吗？→ 查 `Capabilities`，**不要在代码里 switch 驱动类型名**
