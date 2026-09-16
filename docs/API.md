# 接口契约（API）

> **本文档是 `server/`（Go）、`web/`（React）、`picgo-agent/`（Node）三方的唯一契约。**
> 任何一方改动接口，必须先改本文件。
>
> | 关系 | 文档 |
> |---|---|
> | 上位约束（最高） | [`DECISIONS.md`](./DECISIONS.md) |
> | 表结构真源 | [`DATA-MODEL.md`](./DATA-MODEL.md) |
> | PicGo 集成与补丁 | [`PICGO-INTEGRATION.md`](./PICGO-INTEGRATION.md) |
>
> **命名约定（D81）**：**API JSON 字段一律 PascalCase（大驼峰）**，缩写词全大写
> （`UID` / `URL` / `ID` / `API`）；
> 对外标识**一律用 `UID`**（ULID 字符串），**绝不暴露自增 `ID`**（D77.1）；
> 时间统一 **Unix 秒（int64）**；枚举值统一为**小写字符串**（如 `success` / `failed`）。
>
> **硬性例外（D81.3）**：Lsky 兼容层（§12）保持 **snake_case 与 `{status, message, data}`**，
> 因为它是外部冻结契约，**不受 D81 影响**。

---

## 0. 通用约定

### 0.1 三个 API 空间（**前缀天然隔离，互不冲突**）

| 空间 | 前缀 | 消费者 | 响应信封 | 鉴权 |
|---|---|---|---|---|
| **内部 API** | **`/api/web/v1/**`** | 本项目前端、第三方脚本 | `{Code, Message, Data}`（`Code` 为**整数**） | JWT / Cookie / API Token |
| **Lsky 兼容 API** | **`/api/v1/**`** | 第三方 Lsky 客户端与 PicGo 插件 | `{status, message, data}`（snake_case） | Lsky Token（本质是 API Token） |
| **agent 内部 API** | `http://127.0.0.1:36678/**` | 仅 Go 服务端 | `{Code, Message, Data}`（`Code` 为**字符串**） | `X-Agent-Token` |

**两套对外前缀为什么不冲突（D80）**：

- 内部 API 全部挂在 **`/api/web/v1/**`**
- Lsky 兼容层**独占 `/api/v1/**`**（外部冻结契约，内部 API **永不得占用**）
- 两者前缀不同 → **不存在路径重叠**，无需任何「让位」或分流逻辑

**Lsky 保留集（外部冻结契约，内部 API 永远不得占用）**

```
POST   /api/v1/tokens
DELETE /api/v1/tokens
GET    /api/v1/profile
GET    /api/v1/strategies
POST   /api/v1/upload
GET    /api/v1/images
DELETE /api/v1/images/{key}
GET    /api/v1/albums
DELETE /api/v1/albums/{id}
```

> **启动时冲突检测（防护，而非补救）**：路由注册完成后做一次校验，
> 若任一**内部路由**落在上述 Lsky 保留集内，**直接 `panic`**。
> 目的：防止将来新增内部接口时误用 `/api/v1` 前缀而静默覆盖 Lsky 契约。
>
> **历史记录**：早期设计曾让两套 API **共享** `/api/v1`，导致内部相册需让位至
> `/api/v1/gallery/albums`。**D80 之后该让位已取消**；相册功能本身后来亦被移除（D101），
> 内部 API 不再占用任何相册路径。

### 0.2 统一响应体（内部 API）

成功：

```json
{ "Code": 0, "Message": "ok", "Data": {} }
```

失败：

```json
{ "Code": 40101, "Message": "邮箱或密码错误", "Data": null }
```

- `Code`：**整数**，`0` 表示成功，非 `0` 见 §0.3
- `Message`：面向用户的中文提示（可直接 toast）
- `Data`：无内容时为 `null`

### 0.3 错误码表

| Code | 含义 | 典型 HTTP |
|---|---|---|
| `0` | 成功 | 200 |
| `40001` | 参数校验失败 | 400 |
| `40101` | 邮箱或密码错误 | 401 |
| `40102` | 未登录或令牌无效 | 401 |
| `40103` | 令牌已过期 | 401 |
| `40104` | 账号被禁用 | 403 |
| `40301` | 权限不足（非管理员 / 越权访问他人资源 / 需先改密） | 403 |
| `40302` | **存储配额不足**（D20） | 403 |
| `40401` | 资源不存在 | 404 |
| `40901` | 资源冲突（重名 / 已存在 / 不可删） | 409 |
| `42901` | 请求过于频繁（登录失败超限 / 上传超限 / 队列已满） | 429 |
| `50001` | 服务器内部错误 | 500 |
| `50002` | **picgo-agent 不可用**（D7） | 503 |
| `50003` | 上传失败 | 500 |
| `50004` | 插件操作失败 | 500 |

### 0.4 HTTP 状态码约定

HTTP 状态码只表达**传输语义**，业务语义看 `Code`：

| HTTP | 场景 |
|---|---|
| `200` | 业务成功（含 `Code != 0` 但语义为「部分成功」的场景，如批次内有失败项） |
| `400` | 请求格式 / 参数错误 |
| `401` | 未认证 |
| `403` | 已认证但无权限，或配额不足 |
| `404` | 资源不存在 |
| `409` | 冲突 |
| `429` | 限流 |
| `500` | 服务端错误 |
| `503` | agent 不可用（**注意**：`/uploads` 等在 agent 未就绪时也可能返回此码） |

### 0.5 鉴权

三种方式，按以下**顺序**匹配（中间件实现，命中即止）：

| # | 形式 | 用途 | 有效期 |
|---|---|---|---|
| 1 | `Authorization: Bearer <JWT>` | 前端 / 短期调用 | 15 分钟（`security.accessTokenTtlMinutes`） |
| 2 | `Authorization: Bearer pcw_<random>` | 长期令牌、Lsky 客户端、CLI | 按 `ExpiresAt`，`0` = 永久（D31） |
| 3 | Cookie `pcw_at` + `pcw_rt` | 浏览器会话（自动携带） | access 15min / refresh 7 天 |

- 令牌 2 即 `APITokens` 表记录；**Lsky 兼容层复用同一套令牌**（见 §12.1），不另建体系。
- Cookie 属性：`httpOnly`、`SameSite=Lax`、生产 HTTPS 时 `Secure`（D30）。
- **无需鉴权**的端点仅：
  `GET /healthz`、`POST /api/web/v1/auth/login`、`POST /api/web/v1/auth/refresh`、
  `GET /api/web/v1/auth/oauth/github/start`、`GET /api/web/v1/auth/oauth/github/callback`、
  `POST /api/v1/tokens`、`GET /api/v1/strategies`。

### 0.6 分页

请求：`Page`（从 1 开始，默认 1）、`PageSize`（默认 20，上限 100）。

响应的 `Data`：

```jsonc
{ "Items": [], "Total": 128, "Page": 1, "PageSize": 20 }
```

### 0.7 标识、时间与空值

| 项 | 约定 |
|---|---|
| 资源标识 | `UID` 字符串，形如 `up_01JD9X...`；路径参数写作 `{Uid}`（**参数名本身也是 PascalCase**，见 §0.8） |
| 时间 | `int64` Unix 秒；字段名 `CreatedAt` / `UpdatedAt` / `FinishedAt` 等 |
| 可空字符串 | 返回 `""` 而非 `null`（Go 零值语义，前端无需判 null） |
| 可空对象 | 返回 `null` |
| 未知枚举值 | **前端必须容错**：遇未知 `Status` / `Type` / `Kind` 按「未知」降级展示，不得崩溃（D77 允许新增取值） |
| 请求体额外字段 | 服务端**忽略**而非报错（向后兼容，D77.2） |

### 0.8 命名规则速查（D81）

| 层 | 规则 | 示例 |
|---|---|---|
| **API JSON 字段** | **PascalCase**，缩写全大写 | `AccessToken` / `JobUID` / `StorageUID` / `UserUID` / `ImageURL` |
| **API Query 参数名** | **PascalCase**（与响应字段保持一致，避免 `Page`/`Page` 语义割裂） | `?Page=1&PageSize=20&Keyword=xxx&StorageUID=st_01` |
| **API 路径段** | **小写复数**（避免大小写敏感的代理/中间件问题） | `/api/web/v1/storage/configs`、`/uploads`、`/logs` |
| **枚举取值** | **小写字符串**（字符串枚举，原样保留） | `Status=success`、`Scope=mine`、`Type=user.create` |
| **settings 配置键** | **dot.lowerCamel**（KV 表的字符串 key，**不是列名**） | `site.name`、`upload.rateLimit.perHour` |
| **环境变量** | **UPPER_SNAKE_CASE** | `PICGO_WEB_LISTEN`、`PICGO_WEB_DB_DRIVER` |
| **picgo 侧字段名** | **原样保留，永不转换** | `picBed` / `picgoPlugins` / `_id` / `_configName` / `fileName` / `sha` |
| **Lsky 兼容层** | **snake_case + 小写信封**（外部冻结契约） | `strategy_id` / `capacity` / `useCapacity` / `{status, message, data}` |

> **为什么 Query 参数也用 PascalCase**：它们与响应字段是同一套词汇
> （`Page` / `PageSize` / `Keyword` / `StorageUID`）。若用 camelCase，前端类型与请求构造会出现
> 两套拼写，反而更容易出错。**唯一例外**是 Lsky 层（`page` / `per_page` / `album_id`）。

---

## 1. 认证 Auth

> 前缀：`/api/web/v1/auth/**`
>
> 约束：**只认邮箱**（D23）、**不做自助注册**（D25）、OAuth2 **只接 GitHub**（D26）
> 且**必须先绑定才能登录**（D27）。

### `POST /api/web/v1/auth/login`

密码登录（D24）。

```jsonc
// req
{ "Email": "admin@example.com", "Password": "xxx" }

// data
{
  "AccessToken": "eyJhbGciOi...",
  "ExpiresIn": 900,
  "User": {
    "UID": "us_01JD9X...", "Email": "admin@example.com", "Role": "admin",
    "Status": "active", "MustChangePassword": false,
    "Nickname": "管理员", "AvatarURL": "",
    "CapacityBytes": 0, "UsedBytes": 1263616,
    "LastLoginAt": 1789347956, "CreatedAt": 1789000000
  }
}
```

- 成功同时下发 `pcw_at` / `pcw_rt` Cookie。
- 密码错误 → `40101`；账号禁用 → `40104`。
- 失败次数超 `security.loginMaxAttempts`（默认 5 次 / `security.loginWindowMinutes` 分钟，
  按 `Email + ClientIP` 统计 `LoginAttempts`）→ `42901`。
- 每次尝试均写 `LoginAttempts`（限流依据，不对管理员展示）；成功写 `OperationLogs:auth.login`。凭据错误（含触发限流）**不写** `auth.failed`（防暴力枚举刷爆日志页）；被禁用账号的登录尝试仍写 `auth.failed`。
- `MustChangePassword = true` 时（D32 首启引导 / 管理员重置密码），
  **除 `/auth/me`、`/auth/password`、`/auth/logout` 外所有接口返回 `40301`**，
  `Message` 为「请先修改密码」。

### `POST /api/web/v1/auth/refresh`

无 body，用 `pcw_rt` Cookie。**轮换**：旧 refresh token 立即失效（防重放）。

```jsonc
// data
{ "AccessToken": "eyJ...", "ExpiresIn": 900 }
```

失败（无 Cookie / 已吊销 / 已过期）→ `40102` / `40103`，并清除 Cookie。

### `POST /api/web/v1/auth/logout`

吊销当前 refresh token，清 Cookie。写 `OperationLogs:auth.logout`。`Data: null`。

### `GET /api/web/v1/auth/me`

`Data` = `User`（结构见 §1 login 中的 `User`，另含 `HasPassword` 与 `Identities` 摘要）。

```jsonc
{ "UID": "us_01JD9X...", "Email": "...", "Role": "user", "Status": "active",
  "MustChangePassword": false, "Nickname": "", "AvatarURL": "",
  "CapacityBytes": 5368709120, "UsedBytes": 1263616,
  "HasPassword": true, "LastLoginAt": 0, "CreatedAt": 0 }
```

- `CapacityBytes = 0` 表示**不限额**（D20）。

### `PATCH /api/web/v1/auth/password`

```jsonc
// req
{ "OldPassword": "x", "NewPassword": "y" }   // 首次强制改密时 OldPassword 可省略
// data: null
```

- 成功后 `MustChangePassword` 置 `false`，并**吊销该用户全部 refresh token**（强制重新登录）。
- 写 `OperationLogs:user.update`。

### `GET /api/web/v1/auth/identities`

已绑定的第三方身份（D27 的绑定管理入口）。

```jsonc
// data
{
  "HasPassword": true,
  "Identities": [
    { "Provider": "github", "ProviderLogin": "octocat", "ProviderEmail": "a@b.c",
      "AvatarURL": "https://...", "BoundAt": 1789000000 }
  ]
}
```

### `POST /api/web/v1/auth/oauth/github/start?Redirect=/gallery`

→ `302` 到 GitHub 授权页。`state` 用 HMAC 签名并携带 `Redirect`（防 CSRF）。
未启用（`oauth.github.enabled = false`）→ `40401`。

### `GET /api/web/v1/auth/oauth/github/callback?code=&state=`

第三方回调。分支逻辑（D27）：

| 情形 | 行为 |
|---|---|
| `OAuthIdentities` 命中 | 登录成功 |
| 未命中，但 `oauth.autoBindByEmail = true` 且邮箱匹配已有用户 | 自动绑定并登录 |
| 未命中且邮箱不匹配 | **拒绝** → `302 /login?error=not_bound`（**不自动建号**） |

成功：`302` 到 `Redirect`（默认 `/`），URL 片段带 `#access_token=<jwt>`。
失败：`302` 到 `/login?error=<reason>`，`reason ∈ not_bound | disabled | state_invalid | exchange_failed`。

> 唯一标识取 GitHub 的数字 `id`（稳定不变），存入 `OAuthIdentities.ProviderUserID`（D28）。

### `POST /api/web/v1/auth/oauth/github/bind`

需登录。返回 GitHub 授权跳转信息（前端 `window.location` 过去，回调后完成绑定）。

```jsonc
// data
{ "AuthorizeURL": "https://github.com/login/oauth/authorize?...", "State": "..." }
```

- 已绑定过 → `40901`。

### `DELETE /api/web/v1/auth/oauth/github`

解绑。若该用户**无密码且无其他身份** → `40901`（防锁死账号）。`Data: null`。

### 1.1 首启引导与强制改密（D32）

| 时机 | 行为 |
|---|---|
| 启动时 `Users` 表为空 | 创建管理员：`Role=admin`、`MustChangePassword=true`；随机密码打印到日志**并**写入 `data/initial-admin-password.txt`（权限 0600） |
| 管理员登录后 | `MustChangePassword=true`，前端强制跳改密页；后端拦截其他接口 |
| 管理员重置他人密码 | 同样置 `MustChangePassword=true`（见 §2） |

---

## 2. 用户 Users（admin）

> 前缀：`/api/web/v1/users/**`
>
> **全部端点需要 `Role = admin`**（否则 `40301`）。
> **没有用户组概念**（D53）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/web/v1/users` | 列表，分页 |
| POST | `/api/web/v1/users` | 创建用户（D25 的唯一建号入口） |
| GET | `/api/web/v1/users/{Uid}` | 详情 |
| PATCH | `/api/web/v1/users/{Uid}` | 更新（含配额、状态、角色、密码重置） |
| DELETE | `/api/web/v1/users/{Uid}` | 注销账号（D34） |

**Query（列表）**：`Page` `PageSize` `Keyword`（匹配 `Email` / `Nickname`）`Role` `Status` `Sort` `Order`

**`User` 对象**

```ts
{
  UID: string; Email: string; Role: 'admin' | 'user';
  Status: 'active' | 'disabled'; MustChangePassword: boolean;
  Nickname: string; AvatarURL: string; Homepage: string;
  CapacityBytes: number;   // 0 = 不限额
  UsedBytes: number;
  ImageCount: number;
  LastLoginAt: number; CreatedAt: number; UpdatedAt: number;
}
```

> 字段来源：`Users` + `UserProfiles` 两张表（表结构真源：[`DATA-MODEL.md`](./DATA-MODEL.md) §2.1、§2.2）。

### `POST /api/web/v1/users`

```jsonc
// req
{ "Email": "a@b.c", "Password": "初始密码", "Role": "user", "Nickname": "",
  "CapacityBytes": 0 }   // CapacityBytes 省略 → 用 user.defaultCapacityBytes（D21）
// data = User
```

- 邮箱已存在 → `40901`。
- `user.unlimitedCapacity = true` 时 `CapacityBytes` 记 `0`（不限额）。
- 写 `OperationLogs:user.create`。
- 可选 `SendInviteEmail`（默认 `false`）：为 `true` 时用 `mail.*` 发邀请邮件，
  并写 `EmailLogs` + `OperationLogs:mail.send`（D29）。

### `PATCH /api/web/v1/users/{Uid}`

```jsonc
// req（全部字段可选，仅提交需变更项）
{ "Email": "new@b.c", "Nickname": "小明", "Role": "admin",
  "Status": "disabled", "CapacityBytes": 10737418240,
  "NewPassword": "重置后密码", "MustChangePassword": true }
// data = User
```

| 约束 | 行为 |
|---|---|
| 修改自己的 `Role` | `40301`（不可自我降权/提权） |
| 把最后一个 admin 降为 `user` 或禁用 | `40901` |
| 传 `NewPassword` | 重置密码并置 `MustChangePassword`、吊销其全部 refresh token |
| `CapacityBytes` 小于其 `UsedBytes` | 允许（不追溯删图），但响应 `Message` 提示已超额 |

写 `OperationLogs:user.update`（含变更前后字段，**密钥类一律脱敏**）。

### `DELETE /api/web/v1/users/{Uid}`

```jsonc
// data
{ "DeletedUploads": 42, "FreedBytes": 12345678, "RemoteDeleteFailed": 3 }
```

- **硬删除**（D46）：逐条走图片删除流程（含可选远端删除与配额退还，D72、D47），
  再删 `UserProfiles` / `OAuthIdentities` / `RefreshTokens` / `APITokens` /
  `UserSettings`，最后删 `Users` 记录。
- 不可删自己；不可删最后一个 admin（`40901`）。
- 写 `OperationLogs:user.delete`，`Detail` 记录删除统计。

---

## 3. 存储 Storage

> 前缀：`/api/web/v1/storage/**`
>
> **全部端点需要 `Role = admin`**（D4：存储驱动只有管理员能配置）。
> 同一驱动类型可配置**多条**实例（D64）；**对外一律用 `UID`**。

### 3.1 驱动能力探测

### `GET /api/web/v1/storage/drivers`

可用驱动类型列表 + **服务端求值后的配置字段 schema**（来自 agent 探测，见 §13）。

```jsonc
// data
{
  "Drivers": [
    {
      "Type": "github",
      "Name": "GitHub",
      "Builtin": true,
      "GuiOnly": false,
      "Config": [
        { "Name": "repo", "Type": "input", "Required": true, "Alias": "仓库名",
          "Message": "格式 username/reponame", "Default": "" },
        { "Name": "branch", "Type": "input", "Required": true, "Alias": "分支名",
          "Default": "master" },
        { "Name": "token", "Type": "password", "Required": true, "Alias": "Token", "Default": "" },
        { "Name": "path", "Type": "input", "Required": false, "Alias": "存储路径", "Default": "" },
        { "Name": "customUrl", "Type": "input", "Required": false, "Alias": "自定义域名", "Default": "" }
      ],
      "Capabilities": {
        "SupportsPathTemplate": true,
        "SupportsRemoteDelete": true,
        "ServerRenames": false,
        "ConfigFields": ["repo", "branch", "token", "path", "customUrl"],
        "PathFieldNames": ["path"],
        "DetectedAt": 1789347956,
        "PicgoVersion": "3.0.2"
      },
      "ConfigCount": 2
    }
  ]
}
```

**⚠️ 注意 `Config[].Name` 是驱动字段名，保持原样（`repo` / `token` / `path`）**——
它是 picgo 与插件定义的，不适用 D81（见 D81.3 第 5 条）。
而**外层**的 `Type` / `Name` / `Config` / `Capabilities` 是我们的字段，用 PascalCase。

`Config` 字段结构（**已由服务端 `evaluatePluginConfig` 求值**，前端永不执行插件代码）：

```ts
type DriverConfigField = {
  Name: string           // ⚠️ 驱动字段名，原样（snake/camel/任意，由插件定义）
  Type: 'input' | 'password' | 'list' | 'checkbox' | 'confirm' | 'editor' | string
  Required: boolean
  Alias?: string          // 展示标签（已 i18n）
  Message?: string        // 提示文案
  Prefix?: string
  Default?: unknown       // 函数形态已被求值成静态值
  Choices?: (string | { Name?: string; Value: unknown })[]
  DependsOn?: string[]    // 需要联动重求值的依赖字段
}
```

### `POST /api/web/v1/storage/drivers/schema`

按当前表单值**重新求值** schema（处理 `DependsOn` 联动）。

```jsonc
// req
{ "Type": "github", "Answers": { "repo": "org/repo" } }
// data
{ "Type": "github", "Name": "GitHub", "Config": [ /* DriverConfigField[] */ ] }
```

> 联动**必须回到服务端求值**，因为 `choices` / `default` 可能是插件函数，
> 前端不执行插件代码（见 [`PICGO-INTEGRATION.md`](./PICGO-INTEGRATION.md)）。

### 3.2 存储配置 CRUD

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/web/v1/storage/configs` | 列表 |
| POST | `/api/web/v1/storage/configs` | 新建（可多条同类型） |
| GET | `/api/web/v1/storage/configs/{Uid}` | 详情 |
| PATCH | `/api/web/v1/storage/configs/{Uid}` | 更新元数据与模板（**不含密钥**） |
| PUT | `/api/web/v1/storage/configs/{Uid}/secrets` | **单独更新凭据**（体现密钥与元数据分离，D78） |
| DELETE | `/api/web/v1/storage/configs/{Uid}` | 删除 |
| POST | `/api/web/v1/storage/configs/{Uid}/activate` | 置为全局默认 |
| POST | `/api/web/v1/storage/configs/{Uid}/test` | 连通性测试 |

**`StorageConfig` 对象**（**响应中绝不含任何密钥**）

```ts
{
  UID: string;                    // st_ 前缀 ULID
  Name: string;                   // 展示名，全局唯一
  Type: string;                   // 驱动类型：github / webdav / s3 / ...
  PicgoConfigName: string;        // 映射 picgo _configName（D65）；**创建后只读**
  Enabled: boolean;
  IsDefault: boolean;             // 全局同时只有一条为 true
  PathTemplate: string;           // 魔法路径（D43，每配置独立）
  FileTemplate: string;           // 魔法文件名
  Capabilities: {                 // 运行时探测结果（只读）
    SupportsPathTemplate: boolean
    SupportsRemoteDelete: boolean
    ServerRenames: boolean       // 该图床无视魔法文件名（服务端命名，如 NodeImage）——上传结果实测回写
    ConfigFields: string[]        // 该驱动声明的配置字段名（原样）
    PathFieldNames: string[]      // 推断 SupportsPathTemplate 的依据字段
    DetectedAt: number
    PicgoVersion: string
  };
  HasSecrets: boolean;            // 是否已配置凭据（只给布尔，不给值）
  SecretFields: string[];         // 已填写的密钥字段名列表（**只给字段名**）
  Metadata: Record<string, unknown>;
  UploadCount: number;            // 引用该配置的图片数（删除前提示用）
  CreatedAt: number; UpdatedAt: number;
}
```

**为什么 `HasSecrets` / `SecretFields` 而不是回传密钥**：凭据存在独立的 `StorageSecrets`
表（AES-256-GCM 密文），任何列表查询都**不可能**误带出密钥（D78）。
前端展示「已配置」态用 `HasSecrets`；编辑时密钥字段留空 = **不修改**。

> **`PicgoConfigName` 创建后只读**（D64 推论）：picgo 的 `createOrUpdate` 是
> 「未命中即新建」语义，允许改名会把同一条配置裂成两条。
> 因此 `PATCH` **不接受** `PicgoConfigName`；展示名要改就改 `Name`。

### `GET /api/web/v1/storage/configs`

Query：`Page` `PageSize` `Enabled` `Type` `Keyword`

### `POST /api/web/v1/storage/configs`

```jsonc
// req
{
  "Name": "我的坚果云 WebDAV",
  "Type": "webdav",
  "PicgoConfigName": "Default",     // 省略则用 "Default"
  "Enabled": true,
  "IsDefault": false,
  "PathTemplate": "photos/{Y}/{m}",
  "FileTemplate": "{uniqid}{extname}",
  "Config": {                        // 明文提交，服务端加密后写 StorageSecrets
    "url": "https://dav.jianguoyun.com/dav",
    "username": "me@example.com",
    "password": "app-password"
  }
}
// data = StorageConfig
```

- `Name` 重复 → `40901`；`(Type, PicgoConfigName)` 重复 → `40901`。
- `IsDefault = true` → 同事务内把其他行置 `false`，并让 agent 切换当前上传器。
- 服务端随后调用 agent 同步（`POST /api/uploaders/configs`），详见 [`OPERATIONS.md`](./OPERATIONS.md)。
- 提交的 `Config` 中**未在驱动 schema 中声明的字段**会被忽略并记警告。
- 写 `OperationLogs:storage.create`（`Detail` 中密钥一律脱敏为 `******`）。
- `Capabilities` 由服务端在保存后向 agent 探测一次并缓存（**不硬编码驱动名列表**，D77.2）。

### `PATCH /api/web/v1/storage/configs/{Uid}`

```jsonc
// req（全部可选）
{ "Name": "改名", "Enabled": false, "IsDefault": true,
  "PathTemplate": "img/{Y}/{m}/{d}", "FileTemplate": "{md5-8}{extname}" }
// data = StorageConfig
```

- **不含密钥**；改凭据用 `PUT .../secrets`。
- **不接受 `PicgoConfigName`**（创建后只读，见上）。
- 模板变量见 D70（详见 [`PICGO-INTEGRATION.md`](./PICGO-INTEGRATION.md)）：
  `{Y}{m}{d}{H}{i}{s}{timestamp}{filename}{md5}{md5-8}{sha256-8}{uid}{uniqid}{extname}`
- 驱动 `Capabilities.SupportsPathTemplate = false` 时仍可保存 `PathTemplate`，
  但服务端会在响应 `Metadata.warnings` 中给出「该驱动不支持远端路径，将降级为文件名前缀」。
- 写 `OperationLogs:storage.update`。

### `PUT /api/web/v1/storage/configs/{Uid}/secrets`

**单独更新凭据**，与元数据更新分离（D78：敏感级不同）。

```jsonc
// req —— 只提交需要变更的字段，服务端做 merge 后整体重新加密
{ "Config": { "password": "new-app-password" } }
// data
{ "UID": "st_01JD9X...", "HasSecrets": true, "SecretFields": ["url", "username", "password"] }
```

- 传 `{ "Config": {} }` = 不修改任何凭据。
- 清空某字段：显式传 `""`。
- 成功后在 agent 侧覆盖该配置（`createOrUpdate`）。
- 写 `OperationLogs:storage.update`（`Detail` 中仅记录**变更的字段名**，不含值）。

### `DELETE /api/web/v1/storage/configs/{Uid}`

Query：`?Force=false`

```jsonc
// data
{ "Deleted": true, "AffectedUploads": 42, "DefaultSwitchedTo": "st_01JD9Y..." }
```

- 有图片引用该配置且 `Force = false` → `40901`，`Message` 为「有 42 张图片使用该存储配置」。
- 删除时同时删 `StorageSecrets` 行，并调 agent `DELETE /api/uploaders/configs`。
- 若删的是 `IsDefault`，自动把另一条 `Enabled` 的置为默认（无候选则置空并告警）。
- 写 `OperationLogs:storage.delete`。

### `POST /api/web/v1/storage/configs/{Uid}/activate`

`Data` = `StorageConfig`。同事务把其他行 `IsDefault` 置 `false`，并调 agent 切换当前上传器。
写 `OperationLogs:storage.update`（`Detail.action = "activate"`）。

### `POST /api/web/v1/storage/configs/{Uid}/test`

连通性测试（由 agent 执行一次轻量请求）。

```jsonc
// data
{ "Ok": true, "Message": "连接成功", "LatencyMs": 231, "DriverVersion": "3.0.2" }
```

失败时 `Ok = false` 且 `Message` 含原因（**HTTP 仍为 200**，因为「测试本身执行成功」）。
写 `OperationLogs:storage.update`（`Detail.action = "test"`）。

> agent 侧实现见 §13.3 的 `POST /api/uploaders/test`。

---

## 4. 图库 Gallery

> 前缀：`/api/web/v1/uploads/**`

### 4.1 上传

### `POST /api/web/v1/uploads`

`multipart/form-data`。**一次请求 = 1 个 job = 1 个驱动**（D38）。

| 字段 | 必填 | 说明 |
|---|---|---|
| `Files` | ✅ | 可多值（同一字段名重复），单文件上限 `upload.maxSizeBytes` |
| `StorageUID` | ✅ | 目标存储配置的 `UID` |
| `KeepLocal` | ⬜ | 是否保留本地暂存文件（覆盖全局 `upload.keepLocalCopy`） |

> ⚠️ **multipart 表单字段名用 PascalCase**（`Files` / `StorageUID` / `KeepLocal`），
> 与 JSON 字段命名规则一致。**唯一例外**：Lsky 的 `POST /api/v1/upload` 用 `file` /
> `strategy_id` / `album_id` / `permission`（外部契约，见 §12.3）。

```jsonc
// data —— 立即返回，上传在后台队列中推进
{
  "JobUID": "job_01JD9X...",
  "StorageUID": "st_01JD9X...",
  "Items": [
    { "Seq": 1, "FileName": "a.png", "UploadUID": "up_01JD9A...", "Status": "queued" },
    { "Seq": 2, "FileName": "b.jpg", "UploadUID": "up_01JD9B...", "Status": "queued" }
  ]
}
```

**校验顺序**（任一失败即整体拒绝，不产生 job）：

1. 扩展名白名单 `upload.allowedExts`（`upload.blockSvg = true` 时额外拒 `svg`；该配置默认 `true`）
2. 单文件大小 ≤ `upload.maxSizeBytes`
3. 存储配置存在、`Enabled = true`
4. **配额**：`非管理员` 且 `UsedBytes + 本次总大小 > CapacityBytes`（`CapacityBytes > 0` 时）
   → `40302`（管理员跳过，D20）
5. **上传限流**：`upload.rateLimit.enabled = true` 时，按张数校验（管理员跳过，D73）
   → `42901`
6. 队列长度（`queued + running` 的 job 数）≥ `upload.queueMaxLength` → `42901`（D41）

**进度获取**：SSE `GET /api/web/v1/events`
（`upload.progress` / `upload.finished` / `upload.failed`），
或轮询 `GET /api/web/v1/jobs/{JobUID}`。

> `Uploads.Source` 记为 `web`；配额与限流口径见 [`OPERATIONS.md`](./OPERATIONS.md)。

### `POST /api/web/v1/uploads/from-url`

服务端拉取远程图片再交给队列上传。

```jsonc
// req
{ "URLs": ["https://example.com/a.png"], "StorageUID": "st_01..." }
// data 同 POST /uploads
```

- **SSRF 防护**：解析域名取真实 IP，拒绝回环 / 私有网段 / 链路本地地址；
  可用 `PICGO_WEB_ALLOW_PRIVATE_FETCH=true` 关闭该限制（自建内网场景）。
- 远程响应体超 `upload.maxSizeBytes` 即中止。
- 单次最多 20 个 URL。

### 4.2 查询与整理

### `GET /api/web/v1/uploads`

Query：

| 参数 | 说明 |
|---|---|
| `Page` `PageSize` | 分页 |
| `Keyword` | 匹配 `FileName` / `OriginalName` / `AliasName` |
| `StorageUID` | 按存储配置筛选 |
| `Status` | `pending` / `success` / `failed` |
| `Scope` | **`mine`（默认）** / `all`——见下 |
| `Sort` | `CreatedAt`（默认）/ `Size` / `FileName` |
| `Order` | `desc`（默认）/ `asc` |

**`Scope` 语义（D33 + D71 + 附录）**

| 调用者 | `Scope` | 结果 |
|---|---|---|
| 普通用户 | 只能 `mine`（传 `all` 被静默降级为 `mine`） | 仅自己的图片 |
| 管理员 | `mine` | **仅自己的**图片 |
| 管理员 | `all` | 全部用户的图片 |

> 前端「我的图片 / 全部图片」Tab 切换即映射为 `Scope`（D71）。
> **管理员默认落在 `mine`**，避免误操作他人图片。

`Data` = 分页，`Items` 为 `Upload[]`。

### `GET /api/web/v1/uploads/{Uid}`

`Data` = `Upload`。非本人且非管理员 → `40301`。

### `PATCH /api/web/v1/uploads/{Uid}`

```jsonc
// req（全部可选）
{ "AliasName": "封面图" }
// data = Upload
```

- 重命名写 `AliasName`（**不改远端文件名**，因为远端 URL 由图床决定，D66/D42 边界）。
- 写 `OperationLogs:image.update`。

### `DELETE /api/web/v1/uploads/{Uid}`

Query：`DeleteRemote`（默认 `false`）

```jsonc
// data
{
  "Deleted": true,
  "RemoteDeleted": false,
  "RemoteDeleteSupported": true,   // 该驱动 capability
  "RemoteDeleteError": "插件未实现 remove 事件",   // 不支持或失败时给原因
  "FreedBytes": 12345
}
```

流程（D46 + D47 + D72）：

1. 权限校验（本人或管理员）
2. `DeleteRemote = true` **且** 驱动 `Capabilities.SupportsRemoteDelete = true`
   → 调 agent `POST /api/delete`（走 `remove` 事件通道）
3. **硬删除** `Uploads` + `UploadResults` 行
4. **退还配额**：`Users.UsedBytes -= Size`（与远端是否删成功**无关**，D72）
5. 写 `OperationLogs:image.delete`（`Detail` 含 `RemoteDeleted` 与失败原因）

- 驱动不支持远端删除时：`RemoteDeleted = false`，`RemoteDeleteSupported = false`，
  响应 `Message` 提示「该存储驱动不支持远端删除，已仅删除记录」。
- 远端删除失败**不阻断**本地删除（图床残留属预期，自用场景可接受）。

### `POST /api/web/v1/uploads/batch-delete`

```jsonc
// req
{ "UIDs": ["up_01...", "up_01..."], "DeleteRemote": false }
// data
{
  "Total": 2, "Deleted": 2, "Skipped": 0, "FailedRemoteDelete": 1,
  "FreedBytes": 24690,
  "Items": [ { "UID": "up_01...", "Deleted": true, "RemoteDeleted": false, "Error": "" } ]
}
```

- 单次上限 200 条。
- 逐条独立处理，**部分失败不整体回滚**；每条各写一条 `OperationLogs:image.delete`。

### `GET /api/web/v1/uploads/stats`

```jsonc
// data
{
  "Scope": "mine",
  "Total": 128, "TotalSize": 10485760,
  "SuccessCount": 120, "FailedCount": 8, "PendingCount": 0,
  "TodayCount": 3, "WeekCount": 21,
  "ByStorage": [ { "StorageUID": "st_01...", "Name": "GitHub 主仓", "Count": 100 } ],
  "ByExtension": [ { "Extension": "png", "Count": 90 } ]
}
```

### `GET /api/web/v1/uploads/{Uid}/link`

外链格式化（D68）。

Query：`Format` ∈ `markdown`（默认）| `url` | `html`

```jsonc
// data
{
  "UID": "up_01...",
  "Format": "markdown",
  "Text": "![a.png](https://cdn.example.com/2026/02/a.png)",
  "URL": "https://cdn.example.com/2026/02/a.png"
}
```

| Format | `Text` |
|---|---|
| `url` | `{URL}` |
| `markdown` | `![{FileName}]({URL})` |
| `html` | `<img src="{URL}" alt="{FileName}" />` |

`{FileName}` 取 `AliasName || OriginalName || FileName`。

### `POST /api/web/v1/uploads/links`

批量复制（D68：支持多选后一次性复制，多行拼接）。

```jsonc
// req
{ "UIDs": ["up_01...", "up_01..."], "Format": "markdown" }
// data
{ "Format": "markdown", "Text": "![a.png](...)\n![b.jpg](...)",
  "Items": [ { "UID": "up_01...", "URL": "..." } ] }
```

**`Upload` 对象**（字段与 [`DATA-MODEL.md`](./DATA-MODEL.md) 的 `Uploads` 表一一对应）

```ts
{
  UID: string;
  UserUID: string;
  StorageUID: string;
  FileName: string;              // 最终文件名（含扩展名，含魔法文件名结果）
  OriginalName: string;          // 原始上传文件名
  AliasName: string;             // 用户重命名（展示优先）
  Size: number;                  // 字节
  MimeType: string;
  Extension: string;             // 不含点，小写
  Width: number; Height: number;
  SHA256: string;                // 仅记录，不做去重（D66）
  URL: string; ThumbURL: string;
  Status: 'pending' | 'success' | 'failed';
  Error: string;
  Source: 'web' | 'api' | 'lsky';
  JobUID: string;
  Metadata: Record<string, unknown>;
  CreatedAt: number; UpdatedAt: number;
}
```

> **列表响应中的附加只读字段**（不影响上表字段语义）：
> - `StorageName: string` —— 便于前端直接展示存储名，免二次查询
> - `UserEmail: string` —— **仅当 `Scope=all`** 时返回（管理员视图）

> `UploadResults` 表（完整 picgo 返回值，含插件回写的 `sha` 等）**不通过任何端点直接暴露**；
> 它只服务于远端删除流程（D47）。是否开放「查看原始返回值」留给后续版本。

---

## 5.（已移除，D101）相册 Albums

> 相册功能已整体移除（D101）：`Albums` 表、本组端点、前端 `/albums` 页均不存在。
> Lsky 兼容层的 `/api/v1/albums` 仍在（外部冻结路径，D80），但只返回**伪造响应**：
> 列表恒为空、删除恒成功（见 §12.3）。
>
> 本章节编号保留，避免既有交叉引用（§6+）失配。

---

## 6. 插件 Plugins

> 前缀：`/api/web/v1/plugins/**`
>
> **全部端点需要 `Role = admin`**。插件的真实状态在 agent 的 `node_modules`（真相源），
> `Plugins` 表仅为展示缓存。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/web/v1/plugins` | 已安装列表 |
| GET | `/api/web/v1/plugins/search?Q=` | npm registry 搜索 |
| GET | `/api/web/v1/plugins/{Name}/readme` | README 原文 |
| POST | `/api/web/v1/plugins/install` | 安装（异步，返回 job） |
| POST | `/api/web/v1/plugins/uninstall` | 卸载（异步） |
| POST | `/api/web/v1/plugins/update` | 更新（异步） |
| PATCH | `/api/web/v1/plugins/{Name}` | 启用 / 禁用 |

### `GET /api/web/v1/plugins?Refresh=false`

```jsonc
// data
{
  "Items": [
    {
      "Name": "picgo-plugin-github-plus",
      "Version": "1.2.3",
      "Description": "GitHub 图床增强",
      "Author": "zwing",
      "Homepage": "https://github.com/zWing-org/picgo-plugin-github-plus",
      "Uploader": "githubPlus",
      "Transformer": "",
      "Enabled": true,
      "GuiOnly": false,
      "InstalledAt": 1789000000
    }
  ],
  "PendingJobs": [ /* Job[]，见 §8 */ ]
}
```

- `Refresh=true` → 强制重新探测并刷新 `Plugins` 缓存表。
- **`GuiOnly = true`** 表示该插件含 `guiMenu` / `commands`（Electron 专属能力），
  Web 端**无法执行**——前端必须置灰并提示「该能力仅在桌面端可用」。

> ⚠️ `Uploader` / `Transformer` 的值是**插件的注册名**（如 `githubPlus`），
> 属 picgo 侧标识，**原样保留**。

### `GET /api/web/v1/plugins/search?Q=picgo-plugin-`

```jsonc
// data
{ "Items": [ { "Name": "picgo-plugin-webp", "Version": "1.0.2",
               "Description": "...", "Author": "...", "Homepage": "...",
               "Installed": false } ] }
```

- 搜索源取自 `picgo.npmRegistry` 设置。
- 单次最多 20 条。

### `GET /api/web/v1/plugins/{Name}/readme`

```jsonc
// data
{ "Name": "picgo-plugin-github-plus", "Content": "# 标题\n...", "Truncated": false }
```

- Markdown 原文。**前端必须转义/净化后渲染**（防 XSS）。
- 上限 256 KiB，超出 `Truncated = true`。
- 未找到 README → `40401`。

### `POST /api/web/v1/plugins/install`

```jsonc
// req
{ "Names": ["github-plus", "picgo-plugin-webp"] }
// data
{ "JobUID": "job_01JD9X..." }
```

插件名支持四种写法：完整名 `picgo-plugin-x`、短名 `x`、scope `@s/picgo-plugin-y`、本地路径 `./p`。

### `POST /api/web/v1/plugins/uninstall`

```jsonc
// req
{ "Names": ["github-plus"] }
// data
{ "JobUID": "job_01JD9X..." }
```

### `POST /api/web/v1/plugins/update`

```jsonc
// req
{ "Names": [] }     // 空数组 = 更新全部已装插件
// data
{ "JobUID": "job_01JD9X..." }
```

### `PATCH /api/web/v1/plugins/{Name}`

```jsonc
// req
{ "Enabled": false }
// data
{ "Name": "picgo-plugin-github-plus", "Enabled": false }
```

写回 picgo 的 `picgoPlugins[name]`。禁用后立即生效（agent 内部重载插件注册表）。

> ⚠️ `picgoPlugins` 是 picgo 的 config 键名，**原样**；`{Name}` 路径参数是插件包名。

### 6.1 异步与重启语义（**前端必须实现**）

插件安装/卸载/更新耗时较长，**一律异步**：

1. 返回 `JobUID`，前端进入「任务面板」跟踪
2. 通过 SSE `job.log` 实时展示逐行输出（npm 输出）
3. **任务成功后 agent 会重启自身进程**以使插件生效
4. 重启期间上传类接口短暂返回 `503 / 50002`
5. 前端需展示「正在重启内核」提示，并轮询 `GET /healthz` 直到 `agent = "up"` 再恢复操作

> 重启属于**预期行为**（低频操作换取状态绝对干净），不是错误。

---

## 7. PicGo 内核

> 前缀：`/api/web/v1/picgo/**`
>
> **全部端点需要 `Role = admin`**；这些是「逃生舱口」，日常应优先用 §3 的存储配置接口。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/web/v1/picgo/config` | 投影后的 config（**密钥字段脱敏**） |
| PATCH | `/api/web/v1/picgo/config` | 点路径合并 |
| GET | `/api/web/v1/picgo/uploader` | 当前上传器与 transformer |
| POST | `/api/web/v1/picgo/uploader/use` | 切换当前上传器 |
| GET | `/api/web/v1/picgo/transformers` | transformer 列表 |
| GET | `/api/web/v1/picgo/logs?Tail=200` | `picgo.log` 尾部 |
| POST | `/api/web/v1/picgo/resync` | 以 DB 为真相源重建配置投影 |

### `GET /api/web/v1/picgo/config`

```jsonc
// data
{
  "Config": {
    "picBed": { "uploader": "github", "current": "github",
                "github": { "repo": "org/repo", "token": "******", "path": "img/" } },
    "picgoPlugins": { "picgo-plugin-github-plus": true },
    "settings": { "npmRegistry": "https://registry.npmmirror.com" }
  },
  "RedactedFields": ["picBed.github.token"]
}
```

> ⚠️ **`Config` 的内部键（`picBed` / `picgoPlugins` / `settings` / 驱动字段）是 picgo 的原生结构，
> 一律原样透传，不适用 D81**（D81.3 第 5 条）。
> 只有**外层**字段 `Config` / `RedactedFields` 是我们的命名。

### `PATCH /api/web/v1/picgo/config`

```jsonc
// req —— Patch 是点路径 map，键名是 picgo config 的点路径（原样）
{ "Patch": { "picBed.uploader": "github" } }
// data
{ "Applied": ["picBed.uploader"] }
```

> ⚠️ 这是**低层逃生舱口**。常规改配置请走 §3，否则 DB 与 picgo 配置会不一致。

### `GET /api/web/v1/picgo/uploader`

```jsonc
// data
{
  "Current": { "Type": "github", "ConfigName": "Default", "StorageUID": "st_01..." },
  "Transformer": "path"
}
```

`StorageUID` 为反向映射结果（`Type + ConfigName` → `StorageConfigs.UID`，D65）；未登记则为 `""`。
`Type` / `ConfigName` 的**值**是 picgo 的标识，原样。

### `POST /api/web/v1/picgo/uploader/use`

```jsonc
// req
{ "StorageUID": "st_01JD9X..." }                      // 推荐：用 UID
// 或 { "Type": "github", "ConfigName": "Default" }   // 低层用法
// data
{ "Current": { "Type": "github", "ConfigName": "Default", "StorageUID": "st_01..." } }
```

### `GET /api/web/v1/picgo/transformers`

```jsonc
// data
{ "Items": [ { "Type": "path", "Name": "Path" }, { "Type": "base64", "Name": "Base64" } ],
  "Current": "path" }
```

### `GET /api/web/v1/picgo/logs?Tail=200`

```jsonc
// data
{ "Lines": ["2026-02-14 09:30:15 [PicGo SUCCESS] https://..."],
  "Path": "/data/picgo/picgo.log", "Total": 1234 }
```

### `POST /api/web/v1/picgo/resync`

以 DB 为真相源重建 picgo 配置投影（**键级合并**，不整体覆盖，D22）。

```jsonc
// data
{
  "SyncedConfigs": 3,
  "Activated": "st_01JD9X...",
  "PreservedPluginKeys": ["uploaded", "picgo-plugin-github-plus"]
}
```

> `PreservedPluginKeys` 列出被**保留**的插件私有键（如 github-plus 的 `uploaded` 图片账本），
> 用于向管理员证明 reconcile 没有破坏插件状态。写 `OperationLogs:setting.update`。

---

## 8. 任务与事件 Jobs / Events

> 前缀：`/api/web/v1/jobs/**`、SSE `/api/web/v1/events`
>
> 仅暴露**传输语义**：agent 不可用时返回 `503 / 50002`。

### `GET /api/web/v1/jobs`

Query：`Page` `PageSize` `Kind` `Status` `Scope`（`mine` 默认 / `all`，`all` 仅管理员）

```jsonc
// data = 分页，Items 为 Job[]
```

**`Job` 对象**（字段与 [`DATA-MODEL.md`](./DATA-MODEL.md) 的 `Jobs` 表一一对应）

```ts
{
  UID: string;                       // job_ 前缀
  Kind: 'upload' | 'plugin.install' | 'plugin.uninstall' | 'plugin.update' | 'config.sync' | string;
  Status: 'queued' | 'running' | 'succeeded' | 'failed';
  Progress: number;                  // 0..100
  UserUID: string;
  StorageUID: string;                // 上传类任务（D38 一批一驱动）
  TotalItems: number;
  SucceededItems: number;
  FailedItems: number;
  SkippedItems: number;              // D66 取消去重后恒为 0（保留字段，D77）
  Payload: Record<string, unknown>;  // 请求上下文（密钥已脱敏）
  Result: Record<string, unknown> | null;
  Error: string;
  CreatedAt: number; StartedAt: number; FinishedAt: number;
}
```

> **状态只有 4 个，没有 `partial`**（D37）：只要有 item 失败 → `Status = "failed"`，
> 但**成功项的结果照样在 `Result` 中回传**，不丢数据。

> **插件类任务的持久化语义**：`plugin.*` 任务由 picgo-agent 执行（执行期状态在
> agent 内存，重启即丢），但 UID 与 agent 一致，状态与逐行日志由 Go 侧投影
> 到 `Jobs` / `JobLogs` 表（详情/日志/列表都以 Go 表为查询真相源）。
> 结算三保险：agent 事件即时落库 + 每 15s 向 agent **周期对账**非终结任务
> （agent 已丢失 → 置失败「内核重启导致任务结果未知」）+ Go 启动恢复。
> `upload` 任务则由 Go 全程管理（`RecoverInterrupted` 重置为 `queued` 交 worker 重试）。

`Result` 示例（上传任务）：

```jsonc
{ "Total": 3, "Succeeded": 2, "Failed": 1, "Skipped": 0,
  "Items": [ { "Seq": 1, "URL": "https://...", "FileName": "a.png" },
             { "Seq": 2, "URL": "https://...", "FileName": "b.jpg" },
             { "Seq": 3, "URL": "", "FileName": "c.gif", "Error": "图床返回 422" } ] }
```

### `GET /api/web/v1/jobs/{Uid}`

`Data` = `Job`，附 `Items`：

```jsonc
{
  "UID": "job_01...", "Kind": "upload", "Status": "running", "Progress": 66,
  "...": "...",
  "Items": [
    { "Seq": 1, "UploadUID": "up_01...", "FileName": "a.png", "Status": "succeeded",
      "Attempts": 1, "Error": "", "StartedAt": 1789000001, "FinishedAt": 1789000003 },
    { "Seq": 2, "UploadUID": "up_01...", "FileName": "b.jpg", "Status": "running",
      "Attempts": 1, "Error": "", "StartedAt": 1789000003, "FinishedAt": 0 }
  ]
}
```

> `JobItems` 是 `Jobs` 的**批内子项**，按 `Seq`（批次内稳定序号）定位，
> 不单独设 `UID`——它的身份由「父 job 的 `UID` + `Seq`」构成。
> 这与 D77.1「对外用 `UID`」不冲突：`Seq` 只在父资源上下文内有意义，不跨批次引用。

### `GET /api/web/v1/jobs/{Uid}/logs?AfterSeq=0&Limit=500`

Query：`AfterSeq`（增量拉取，返回 `Seq > AfterSeq` 的行）、`Limit`（默认 500，上限 2000）

```jsonc
// data
{ "Items": [ { "Seq": 1, "Line": "[npm] added 12 packages", "CreatedAt": 1789000001 } ],
  "HasMore": false, "LastSeq": 12 }
```

> 任务日志（`JobLogs`）保留 `log.jobRetentionDays`（默认 7 天），
> 与操作日志（`OperationLogs`，180 天）是**两套不同生命周期的数据**（D78）。

### `DELETE /api/web/v1/jobs/{Uid}`

仅清理**已结束**（`succeeded` / `failed`）的任务及其 `JobItems` / `JobLogs`。
运行中 → `40901`。`Data: null`。

### 8.1 `GET /api/web/v1/events`（SSE）

`Content-Type: text/event-stream`。

```
event: upload.progress
data: {"JobUID":"job_01X","UploadUID":"up_01A","Seq":1,"FileName":"a.png","Progress":60}

event: upload.finished
data: {"JobUID":"job_01X","UploadUID":"up_01A","Seq":1,"FileName":"a.png","URL":"https://cdn/x.png","ThumbURL":""}

event: upload.failed
data: {"JobUID":"job_01X","UploadUID":"up_01A","Seq":2,"FileName":"b.jpg","Error":"图床返回 422","Attempts":2}

event: job.log
data: {"JobUID":"job_01X","Seq":7,"Line":"[npm] added 12 packages","CreatedAt":1789000001}

event: job.finished
data: {"JobUID":"job_01X","Kind":"upload","Status":"failed","Progress":100,
       "TotalItems":3,"SucceededItems":2,"FailedItems":1,"SkippedItems":0}

event: job.started
data: {"JobUID":"job_01X","Kind":"upload","Status":"running"}

event: system.notice
data: {"Level":"warn","Message":"内核正在重启，请稍候"}

event: ping
data: {"Ts":1789000001}
```

| 事件名（**小写点分，原样**） | 触发 |
|---|---|
| `job.started` | job 从 `queued` 转 `running` |
| `upload.progress` | 单文件进度变化（`Progress` 0..100） |
| `upload.finished` | 单文件成功 |
| `upload.failed` | 单文件最终失败（已耗尽重试） |
| `job.log` | 任务逐行日志（npm 输出、上传过程） |
| `job.finished` | job 结束（`succeeded` 或 `failed`） |
| `system.notice` | **服务端提示**：优雅重启、agent 重启、配置 syncPending 等。`Data` = `{Level, Message}`（`Level` ∈ `info`/`warn`/`error`） |
| `ping` | 每 **25 秒** 保活，用于检测断线（负载 `{"Ts": <Unix秒>}`） |

> **事件名保持小写点分**（`upload.progress` 等）：SSE 事件名是协议层标识符，
> 不是 JSON 字段，与 D81 无关。但**事件体内部的字段用 PascalCase**。

**鉴权**：浏览器走 Cookie（`pcw_at`）；非浏览器客户端可用 `?Token=<jwt>` 或
`?Token=pcw_<api-token>`（**仅此场景允许 query 传令牌**）。

**投递范围**：只推送给「任务所属者」与管理员（按 job 的 `UserUID` 过滤），
避免越权看到他人上传进度。

**客户端要求**：断线后指数退避重连，并重连成功后用 `GET /jobs?Status=running`
+ `GET /jobs/{Uid}` 做一次**状态对齐**（SSE 不保证重放丢失事件）。

---

## 9. 操作日志 Operation Logs

> 前缀：`/api/web/v1/logs/**`
>
> 统一操作日志（D45）；保留 **180 天**（D74）。**全部端点需要 `Role = admin`**。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/web/v1/logs` | 操作日志列表（类型过滤 + 关键词搜索） |
| GET | `/api/web/v1/logs/{Uid}` | 单条详情 |
| GET | `/api/web/v1/logs/types` | 可过滤的类型清单 |
| GET | `/api/web/v1/logs/emails` | 邮件发送日志 |
| GET | `/api/web/v1/logs/emails/{Uid}` | 单封邮件记录详情 |

### `GET /api/web/v1/logs`

Query：

| 参数 | 说明 |
|---|---|
| `Page` `PageSize` | 分页 |
| `Type` | 精确匹配类型（可重复传多值 = OR） |
| `Status` | `success` / `failed` |
| `Keyword` | 关键词搜索：匹配 `Username` / `TargetUID` / `Detail` / `Error` |
| `UserUID` | 按操作者过滤 |
| `TargetType` `TargetUID` | 按操作对象过滤 |
| `From` `To` | 时间范围（Unix 秒） |
| `Sort` `Order` | 默认 `CreatedAt desc` |

```jsonc
// data = 分页，Items 为 OperationLog[]
```

**`OperationLog` 对象**（字段与 [`DATA-MODEL.md`](./DATA-MODEL.md) 的 `OperationLogs` 表一一对应）

```ts
{
  UID: string;                     // log_ 前缀 ULID
  Type: string;                    // upload | mail.send | user.create | ...（小写点分，原样）
  Status: 'success' | 'failed';
  UserUID: string;                 // 操作者；系统操作为 ""
  Username: string;                // 冗余，便于展示与搜索
  TargetType: string;              // upload | user | storage | plugin | setting | ""
  TargetUID: string;
  Detail: Record<string, unknown> | null;   // JSON：成功时的上下文（密钥已脱敏）
  Error: string;                   // 失败原因（失败时必填）
  ClientIP: string;
  UserAgent: string;
  CreatedAt: number;
}
```

### `GET /api/web/v1/logs/types`

返回稳定的类型标识与对象类型。**不返回本地化展示文案**：数据库的
`OperationLogs.Type`、日志列表的 `Type` 与本接口的 `Type` 使用同一个小写字符串值,
由前端按当前语言映射为展示文案。

```jsonc
// data
{
  "Types": [
    { "Type": "upload",                 "TargetType": "upload" },
    { "Type": "image.delete",           "TargetType": "upload" },
    { "Type": "image.update",           "TargetType": "upload" },
    { "Type": "mail.send",              "TargetType": "email" },
    { "Type": "user.create",            "TargetType": "user" },
    { "Type": "user.delete",            "TargetType": "user" },
    { "Type": "user.update",            "TargetType": "user" },
    { "Type": "storage.create",         "TargetType": "storage" },
    { "Type": "storage.update",         "TargetType": "storage" },
    { "Type": "storage.delete",         "TargetType": "storage" },
    { "Type": "plugin.install",         "TargetType": "plugin" },
    { "Type": "plugin.uninstall",       "TargetType": "plugin" },
    { "Type": "plugin.update",          "TargetType": "plugin" },
    { "Type": "auth.login",             "TargetType": "user" },
    { "Type": "auth.failed",            "TargetType": "user" },
    { "Type": "auth.logout",            "TargetType": "user" },
    { "Type": "setting.update",         "TargetType": "setting" },
    { "Type": "system.log.cleanup",     "TargetType": "" },
    { "Type": "theme.install",           "TargetType": "theme" },
    { "Type": "theme.uninstall",         "TargetType": "theme" },
    { "Type": "theme.activate",          "TargetType": "theme" },
    { "Type": "theme.rescan",            "TargetType": "theme" },
    { "Type": "theme.settings.update",   "TargetType": "theme" },
    { "Type": "theme.settings.clear",    "TargetType": "theme" },
    { "Type": "theme.error",             "TargetType": "theme" }
  ]
}
```

> **`Type` 的取值是小写点分字符串枚举，原样保留**（D81 例外）——
> 它们是历史数据与日志检索的键，改了会让旧记录搜不到。
> 清单为**静态声明**（便于前端渲染筛选项）；后端写入时**不校验类型白名单**，
> 以便新功能直接写新类型而无需改接口（D77）。未知类型也必须由前端降级展示,
> 不能因为缺少翻译而崩溃。

### `GET /api/web/v1/logs/emails`

Query：`Page` `PageSize` `ToAddress` `Template` `Status` `From` `To`

**`EmailLog` 对象**（字段与 [`DATA-MODEL.md`](./DATA-MODEL.md) 的 `EmailLogs` 表一一对应）

```ts
{
  UID: string;
  ToAddress: string;
  Subject: string;
  Template: string;              // invite | reset_password | ...（小写，原样）
  Status: 'success' | 'failed';
  Error: string;
  RelatedUserUID: string;
  CreatedAt: number;
}
```

> **不保存邮件正文**（用户明确要求）。日志只记录「发给谁 / 主题 / 模板 / 结果」。

---

## 10. 系统 System

### `GET /healthz`（**无需鉴权**）

```jsonc
{ "status": "ok", "version": "0.1.0", "agent": "up", "uptime": 1234 }
```

- `agent` ∈ `up` | `down` | `restarting`（D7）。
- 该端点**必须轻量**（不查库、不调 agent），供容器 healthcheck 与前端轮询使用。
- **注意**：这是唯一一个**不使用信封**的端点（供容器探针直接解析），
  字段名保持小写以兼容容器/K8s 惯例。

### `GET /api/web/v1/system/info`

需登录。

```jsonc
{
  "Version": "0.1.0",
  "AgentStatus": "up",
  "Picgo": { "ConfigPath": "/data/picgo/config.json", "Version": "3.0.2", "PluginCount": 3 },
  "Storage": {
    "DriverCount": 2, "EnabledDriverCount": 2, "DefaultStorageUID": "st_01..."
  },
  "Upload": {
    "Concurrency": 1, "QueueLength": 0, "MaxSizeBytes": 20971520,
    "AllowedExts": ["jpg", "png", "gif", "webp"],
    "RateLimitEnabled": false
  },
  "Server": {
    "DatabaseDriver": "sqlite", "Timezone": "Asia/Shanghai",
    "SiteBaseURL": "https://img.example.com"
  },
  "User": { /* 当前登录者的 User 摘要 */ }
}
```

### `GET /api/web/v1/system/stats`

需登录；管理员看全局，普通用户看自己。

```jsonc
// data
{
  "Scope": "all",       // mine（普通用户）/ all（管理员）
  "Users": { "Total": 5, "Active": 4, "Disabled": 1, "Admins": 1 },
  "Uploads": { "Total": 1280, "TotalSize": 5368709120, "TodayCount": 12,
               "PendingCount": 0, "FailedCount": 8 },
  "Jobs": { "Running": 1, "Queued": 3 },
  "Trend": [ { "Date": "2026-02-08", "Count": 10, "Size": 123456 }, /* ... 30 天 */ ],
  "ByStorage": [ { "StorageUID": "st_01...", "Name": "GitHub 主仓", "Count": 1000 } ]
}
```

> 按角色裁剪：
> - **`Users` 与 `ByStorage` 只对管理员返回**（普通用户的响应体里没有这两个字段，不是空值）；
>   `Scope` 恒存在，普通用户为 `mine`。前端据此渲染概览页的管理员区块（D102/D103）。
> - `Trend` 固定 30 条，已按**本地日**补齐空缺日期（时区位移在 SQL 里做），前端不做补零。
> - `Users.Active = Total - Disabled`（`Status = disabled` 的数量）。

### `POST /api/web/v1/system/picgo/resync`

与 §7 的 `POST /api/web/v1/picgo/resync` 等价（语义更明确的别名，需 admin）。
`Data` 同 §7。

> **本项目不提供备份 / 恢复端点**（D82）。数据库与 `data/` 目录由运维自行备份。

### 10.1 路由分发与静态资源托管（D94 / D99）

**模型**：内置 SPA（`go:embed web/dist`）提供**全部**页面的默认实现；
**主题是可选的页面覆盖层**，通过 `manifest.Pages` **自行注册**要接管的页面（D94）。
默认主题只注册 `Pages = ["/"]`（首页），因此首次行为 = 只有首页走主题。

**可被主题注册的业务页面**：`/`、`/overview`、`/upload`、`/gallery`、`/jobs`、`/settings`（D94.1 / D102 / D104；普通用户日志页 `/logs` 已移除）。
**永久保留、不可注册**：所有认证页 + `/admin/**`（安全底线，代码硬编码）。

#### 请求分发顺序（**一次写通用，后续加页面不改代码**）

| # | 判断 | 处理 |
|---|---|---|
| 1 | 保留路径：`/api/**`、`/healthz`、`/theme-assets/**`、`/assets/**`、`/themes/**`、`/favicon.ico` | 交给对应处理器 |
| 2 | **认证页保留列表**：`/login`、`/first-login`、`/forgot-password`、`/reset-password`、`/logout` | **内置 SPA**（主题**无法**注册，D94.2） |
| 3 | 命中**当前主题的 `Pages`**（最长前缀匹配） | **主题的 `index.html`**（当前路径原样传入，主题自行分流） |
| 4 | `/admin/**` | **内置 SPA**（主题**无法**接管） |
| 5 | 其他 | **内置 SPA**（SPA 回退） |

> 默认主题 `Pages = ["/"]` → 只有 `/`（精确）走主题。
> 某主题写 `["/", "/upload", "/gallery"]` → 这三个页面走主题，其余仍走内置 SPA，**Go 无需改动**。
> `"/*"` 表示接管所有非保留业务页面（语法允许，默认主题不这么做）。

#### 路径与缓存

| 路径 | 归属 | 缓存头 |
|---|---|---|
| `GET /` | 当前主题的 `index.html` | `Cache-Control: no-cache` |
| `GET /theme-assets/**` | 当前主题的 `assets/`（**防目录穿越**） | `public, max-age=31536000, immutable` |
| `GET /assets/**` | **内置 SPA** 的 `assets/`（embed） | `public, max-age=31536000, immutable` |
| `GET /favicon.ico` | 优先当前主题的 `assets/favicon.ico`，缺失回退内置 | `max-age=86400` |
| `GET /themes/**` | **404**（不暴露主题目录 / `manifest.json` / 源码） | — |
| 其他（SPA 回退） | 内置 SPA 的 `index.html` | `Cache-Control: no-cache` |
| `/api/**`、`/healthz` 未匹配 | `40401` JSON | — |

**MIME**：按扩展名映射（`js`/`mjs`→`text/javascript`，`css`→`text/css`，`svg`→`image/svg+xml`，
`json`→`application/json`，`woff2`→`font/woff2`，其余 `mime.TypeByExtension` 兜底，未知→`application/octet-stream`）。

#### 安全

- `/theme-assets/**` 与 `/assets/**` 路径规范化后必须落在各自目录内，否则 `40401`（**防 `..` 穿越**）。
- 主题**必须**用 `/theme-assets/...` 引用自己的资源（构建时 `base: '/theme-assets/'`）；
  `/assets/**` 永远指向内置 SPA，主题**不得**占用。
- 不提供目录列表；不返回 `manifest.json`（后台通过 `GET /api/web/v1/themes` 读取）。
- 主题缺失 / 损坏 / `Pages` 非法 → **回退内嵌默认主题**（其 `Pages = ["/"]`），写 `OperationLogs`（`theme.error`）。
- **永不白屏**：内嵌默认主题随二进制发布（`go:embed`）。

### `GET /api/web/v1/site/config`（**无需鉴权**）

站点公开信息 + **当前主题的元数据与设置**（D83 / D94 / D95）。前端首屏调用一次。

```jsonc
{
  "Site": {
    "Name": "PicGo Web",
    "Subtitle": "自建图床控制台",
    "Description": "在服务器上运行 PicGo，通过浏览器管理存储驱动、插件与图片",
    "Keywords": "picgo,图床,自建",
    "IconURL": "https://img.example.com/icon.png",
    "Notice": "**公告**：本周六维护",
    "Icp": "京ICP备00000000号",
    "BaseURL": "https://img.example.com",
    "Version": "0.1.0"
  },
  "Theme": {
    "ID": "default",
    "Name": "默认首页主题",
    "Version": "1.0.0",
    "Pages": ["/"],                  // ★ 该主题接管的路由前缀（D94.2）；缺省 ["/"]
    "AssetBase": "/theme-assets",    // 主题静态资源前缀（见 §10.1）
    // 已按「DB 值 → manifest Default → 零值」合并后的最终值
    "Settings": {
      "BackgroundURL": "https://api.yppp.net/api.php",
      "ShowHomeFeatures": true,
      "HomepageFeatures": [ { "Icon": "Server", "Title": "多存储驱动", "Desc": "同一驱动类型可添加多条实例" } ],
      "HomepageScenarios": [ { "Icon": "Users", "Title": "个人图床", "Desc": "把自己的图集中管理" } ],
      "HomepageFaq": [ { "Question": "支持哪些图床？", "Answer": "取决于已安装的 PicGo 插件与内置驱动。" } ]
    }
  },
  "Features": {
    "GitHubOAuthEnabled": true,      // 控制登录页是否显示 GitHub 按钮（D26/D27）
    "AllowSelfRegistration": false   // 恒为 false（D25），前端据此隐藏注册入口
  }
}
```

- **不返回任何敏感信息**（无 SMTP、无 OAuth Secret、无密钥、无 `password` 类型的主题设置明文）。
- `Theme.Settings` 的键集合**由当前主题的 `manifest.json` 决定**，服务端不硬编码；
  装了不同主题就会返回不同的键（前端按需读取，缺失时用自身兜底）。
- `BackgroundURL` 仅是一个 URL 字符串；**前端直接 `` <img src={BackgroundURL}> ``，不做任何判断**（D97）。
- 当前主题缺失/损坏时，`Theme` 为 `null`，同时返回 `ThemeError` 字段说明原因（前端显示引导页）。

### `GET /api/web/v1/themes`（**admin**）

列出**扫描文件系统**得到的全部主题（D94：不建表，文件系统即真相源）。

```jsonc
{
  "Active": "default",
  "Items": [
    {
      "ID": "default",
      "Name": "默认主题",
      "Version": "1.0.0",
      "Description": "PicGo-Web 默认主题",
      "Author": "YeqingKy",
      "Tags": ["现代", "亮暗双主题"],
      "Repo": "https://github.com/YeqingKy/PicGo-Web",
      "MinAppVersion": "0.1.0",
      "Pages": ["/"],                     // 接管的路由前缀（D94.2）
      "IsActive": true,
      "IsBuiltin": true,                  // 随镜像发布（default）
      "CanUninstall": false,              // 当前启用中 or IsBuiltin → false
      "SettingCount": 5,                  // manifest.Settings 的条目数
      "ScreenshotURL": "/api/web/v1/themes/default/screenshot",
      "Valid": true,
      "Error": ""                         // Valid=false 时说明原因（缺 manifest / ID 不匹配 / 缺 index.html）
    }
  ],
  "ScannedAt": 1789347956
}
```

### `POST /api/web/v1/themes/rescan`（**admin**）

重新扫描 `<dataDir>/themes/`。`Data` 同上（返回扫描结果）。写 `OperationLogs`（`theme.rescan`）。

### `POST /api/web/v1/themes/install`（**admin**，D96 zip / D100 Git）

统一安装入口，按 `Content-Type` 分发：

- **`multipart/form-data`**（带 `File` 字段）→ zip 安装（D96）：字段 `File`（zip，≤ 50 MiB）、`Overwrite`（bool，默认 false）；
- **`application/json`**（D100）→ Git 安装，请求体 `{ "URL": "https://github.com/Yeqingky/PicGo-Web-Theme.git", "Overwrite": false }`：
  - **仅支持 `https://`**（拒绝 http / file / ssh / 携带凭据的地址，`40001`）；
  - 浅克隆（`--depth 1`，默认分支）到临时目录，**与 zip 同一套清单校验**（manifest 七项 + index.html + 文件数/体积限额），任何一步失败都不落盘；
  - `.git` 不进入主题目录；克隆失败（网络 / 非 Git 仓库）返回 `40001`，单次克隆总时长上限 2 分钟；同步返回（不建 job）；
  - 成功响应多一个 `Branch` 字段（实际克隆到的分支）。

```jsonc
// 成功（两种来源同形状；Branch 仅 Git 安装返回）
{ "ID": "my-theme", "Name": "我的主题", "Version": "1.0.0", "Installed": true }
```

**安全校验（全部必做，缺一不可）**：

| # | 校验 | 失败返回 |
|---|---|---|
| 1 | 仅 `.zip`，压缩包 ≤ `theme.maxPackageBytes`（默认 50 MiB） | `40001` |
| 2 | **防 Zip Slip**：每个 entry 规范化后必须落在目标目录内，拒绝 `..` 与绝对路径 | `40001` |
| 3 | 解压体积 ≤ `theme.maxExtractBytes`（默认 200 MiB），文件数 ≤ `theme.maxFiles`（默认 5000） | `40001` |
| 4 | 根（或唯一顶层目录）下须有 `manifest.json`，`ID` 匹配 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` 且与目录名一致 | `40001` |
| 5 | 须存在 `index.html` | `40001` |
| 6 | 目录已存在且 `Overwrite=false` | `40901` |
| 7 | 目标 ID 为 `default` 且已有同名内置主题 | `40901`（需显式 `Overwrite=true`） |

- 解压失败时**清理已写入的部分文件**（原子性）。
- 写 `OperationLogs`（`theme.install`，`Detail` 含 ID / 版本 / 包大小）。
- ⚠️ **主题是服务器上的任意前端代码**，信任级别与插件同级，仅 admin 可操作。

### `DELETE /api/web/v1/themes/{ThemeID}`（**admin**）

卸载主题。约束：

| 情形 | 结果 |
|---|---|
| `ThemeID` 是**当前启用**的 | `40901`（先切到别的主题） |
| `ThemeID` 是 `default` | `40901`（它是兜底主题，不可卸载） |
| 主题目录不存在 | `40401` |

**不删除**该主题的配置值（`theme.<ID>.*` 保留，D95）；需显式清理见下。
写 `OperationLogs`（`theme.uninstall`）。

### `PUT /api/web/v1/themes/active`（**admin**）

```jsonc
// req
{ "ThemeID": "my-theme" }
// data
{ "Active": "my-theme", "Previous": "default" }
```

- 目标主题必须存在且 `Valid=true`，否则 `40401` / `40001`。
- **立即生效**（无需重启）；已打开的页面需刷新。
- 写 `OperationLogs`（`theme.activate`）。

### `GET /api/web/v1/themes/{ThemeID}/settings`（**admin**）

取某主题的设置（含 manifest 的字段定义 + 当前值 + 默认值 + 来源），供后台渲染表单：

```jsonc
{
  "ThemeID": "default",
  "Name": "默认主题",
  "Schema": [
    { "Name": "BackgroundURL", "Type": "input", "Alias": "背景图地址",
      "Message": "留空则不显示背景图", "Required": false,
      "Default": "https://api.yppp.net/api.php" },
    { "Name": "ShowHomeFeatures", "Type": "confirm", "Alias": "显示核心能力区块", "Default": true },
    { "Name": "HomepageFeatures", "Type": "repeater", "Alias": "核心能力",
      "ItemSchema": [
        { "Name": "Icon",  "Type": "input",    "Alias": "图标", "Required": true },
        { "Name": "Title", "Type": "input",    "Alias": "标题", "Required": true },
        { "Name": "Desc",  "Type": "textarea", "Alias": "描述", "Required": true }
      ],
      "Default": [] }
  ],
  "Values": {
    "BackgroundURL": { "Value": "https://api.yppp.net/api.php", "Default": "https://api.yppp.net/api.php", "Source": "default" },
    "ShowHomeFeatures": { "Value": true, "Default": true, "Source": "db" }
  }
}
```

- `Source` ∈ `db` | `default`（与 `GET /settings/system` 的约定一致）。
- `password` 类型的 `Value` 恒为掩码，另附 `HasValue`。

### `PUT /api/web/v1/themes/{ThemeID}/settings`（**admin**）

```jsonc
// req
{ "Values": { "BackgroundURL": "https://api.yppp.net/api.php", "ShowHomeFeatures": false } }
// data
{ "Updated": 2 }
```

- 只接受**该主题 manifest 里声明过的键**，未声明的键返回 `40001`（防脏写）。
- 类型按 manifest 的 `Type` 校验（如 `number` 必须是数字、`list` 必须在 `Choices` 内）。
- `password` 类型：值等于掩码时**跳过不更新**（保留原值）。
- 写 `OperationLogs`（`theme.settings.update`）；**不**记录值内容（可能含敏感串）。

### `DELETE /api/web/v1/themes/{ThemeID}/settings`（**admin**）

清理某主题的全部配置值（`theme.<ID>.*`）。**不允许**对当前启用主题操作。
写 `OperationLogs`（`theme.settings.clear`）。

### `GET /api/web/v1/themes/{ThemeID}/screenshot`（**admin**）

返回该主题的 `screenshot.png`（不存在则 `40401`）。用于后台主题列表预览图。

---

---

## 11. 设置 Settings

> 前缀：`/api/web/v1/settings/**`
>
> 配置三级分层与**完整键位表**见 [`DATA-MODEL.md`](./DATA-MODEL.md) §7.4（表结构真源）。
> 原则：**除启动引导类外，其余配置都在数据库里，运行时可改**（D18）。
>
> ⚠️ **配置键名保持 `dot.lowerCamel` 不变**（D81.3 第 3 条）：它们是 KV 表的字符串 key，
> **不是列名**，改它得不偿失。只有**响应体的外层字段**用 PascalCase。

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/api/web/v1/settings` | 需登录 | 用户级设置 + 站点公开信息 |
| PUT | `/api/web/v1/settings` | 需登录 | 更新**用户级**设置 |
| GET | `/api/web/v1/settings/system` | admin | 全部系统键位 + 当前生效值 |
| PUT | `/api/web/v1/settings/system` | admin | 更新系统设置 |
| GET | `/api/web/v1/settings/api-tokens` | 需登录 | 列出自己的 API Token |
| POST | `/api/web/v1/settings/api-tokens` | 需登录 | 创建（明文仅返回一次） |
| DELETE | `/api/web/v1/settings/api-tokens/{Uid}` | 需登录 | 吊销 |

### 11.1 配置键分组速查

> 完整键位与默认值**不在此重复**，见 [`DATA-MODEL.md`](./DATA-MODEL.md) §7.4。

| `Category` | 覆盖范围 | 示例键 |
|---|---|---|
| `site` | 站点名称、对外地址、描述、公告、备案号 | `site.name` / `site.baseUrl` |
| `user` | 新建用户默认配额与状态（D21） | `user.defaultCapacityBytes` / `user.unlimitedCapacity` |
| `upload` | 大小/扩展名/并发/重试/超时/限流（D35、D41、D73） | `upload.concurrency` / `upload.rateLimit.enabled` |
| `mail` | SMTP 连接与发件人（D29，含 `secret` 类） | `mail.host` / `mail.password` |
| `oauth` | GitHub OAuth（D26，含 `secret` 类） | `oauth.github.enabled` / `oauth.github.clientSecret` |
| `security` | 会话与登录限流（D30） | `security.accessTokenTtlMinutes` / `security.loginMaxAttempts` |
| `log` | 日志保留天数（D74） | `log.retentionDays` / `log.jobRetentionDays` |
| `picgo` | npm 源、代理、transformer、配置路径 | `picgo.npmRegistry` / `picgo.transformer` |
| `integration` | Lsky 兼容层开关与语义（D52） | `integration.lsky.enabled` / `integration.lsky.deleteRemoteOnDelete` |

### `GET /api/web/v1/settings`

```jsonc
// data
{
  "Site": { "name": "PicGo Web", "description": "", "notice": "", "icp": "",
            "baseUrl": "https://img.example.com", "allowSelfRegistration": false },
  "User": { "ui.theme": "system", "sidebar.collapsed": false },
  "Upload": { "maxSizeBytes": 20971520, "allowedExts": ["jpg", "png"], "blockSvg": true },
  "Features": { "oauthGithubEnabled": true, "mailEnabled": true }
}
```

> - **外层** `Site` / `User` / `Upload` / `Features` 是 PascalCase（我们的字段）。
> - **内层**的键名（`site.name` 域下的 `name` / `baseUrl`…）沿用配置文件风格的短名，
>   与配置键一一对应，便于前端直接双向绑定。
> - **仅返回非敏感且与前端渲染相关**的键；不返回 SMTP 凭据、OAuth secret 等。

### `PUT /api/web/v1/settings`

仅允许写 `user.*` 与 `ui.*` 前缀的键（用户级，落 `UserSettings` 表）。

```jsonc
// req
{ "ui.theme": "dark", "sidebar.collapsed": true }
// data
{ "Applied": ["ui.theme", "sidebar.collapsed"] }
```

- 写入未登记的键 → 允许（KV 表，D77），但返回 `Ignored` 列表提示拼写可能有误。
- 写 `ui.theme` 为等值变更时不产生日志；其余写 `OperationLogs:setting.update`。

### `GET /api/web/v1/settings/system`

返回**全部系统键位**及其元信息。

```jsonc
// data
{
  "Groups": [
    {
      "Category": "site",
      "Label": "站点",
      "Keys": [
        { "Key": "site.name", "Type": "string", "Value": "PicGo Web",
          "Default": "PicGo Web", "Source": "db", "Secret": false,
          "Label": "站点名称", "Description": "", "RequiresRestart": false },
        { "Key": "site.baseUrl", "Type": "string", "Value": "https://img.example.com",
          "Default": "", "Source": "db", "Secret": false, "Label": "站点地址",
          "Description": "用于生成 OAuth 回调地址", "RequiresRestart": false }
      ]
    },
    {
      "Category": "mail",
      "Label": "邮件",
      "Keys": [
        { "Key": "mail.password", "Type": "secret", "Value": "******",
          "HasValue": true, "Default": "", "Source": "db", "Secret": true,
          "Label": "SMTP 密码", "Description": "", "RequiresRestart": false }
      ]
    }
  ],
  "Meta": { "SchemaKeyCount": 42, "DBKeyCount": 30, "UpdatedAt": 1789000000 }
}
```

| 字段 | 说明 |
|---|---|
| `Key` | **配置键名，dot.lowerCamel，原样**（D81.3 第 3 条） |
| `Category` | `site` / `user` / `upload` / `mail` / `oauth` / `security` / `log` / `picgo` / `integration` |
| `Type` | `string` / `int` / `bool` / `json` / `secret` |
| `Value` | 当前生效值；`secret` 类型**恒为 `"******"`** |
| `HasValue` | 仅 `secret` 类：是否已设置（用于展示「已配置」态，**不泄露值**） |
| `Default` | 代码默认值（`secret` 类恒为空串） |
| `Source` | `db`（来自数据库） / `default`（用默认值） |
| `RequiresRestart` | 是否需重启才生效（当前均为 `false`；改 `picgo.configPath` 除外） |

### `PUT /api/web/v1/settings/system`

```jsonc
// req —— 键名是配置键（dot.lowerCamel），只提交需变更的键
{ "site.name": "我的图床",
  "upload.rateLimit.enabled": true, "upload.rateLimit.perHour": 200,
  "upload.rateLimit.perDay": 1000, "upload.rateLimit.action": "reject",
  "mail.password": "new-smtp-pass" }

// data
{ "Applied": ["site.name", "upload.rateLimit.enabled", "upload.rateLimit.perHour",
              "upload.rateLimit.perDay", "upload.rateLimit.action", "mail.password"],
  "Effects": ["rateLimitReloaded", "picgoConfigPushed"] }
```

**副作用触发规则**（`SettingsService.onChanged` 回调）：

| 变更键前缀 | 副作用 |
|---|---|
| `picgo.*` | 自动推送到 agent（`PATCH /api/config`） |
| `upload.rateLimit.*` | 刷新内存限流器（**无需重启**，D73）；含 `enabled` / `perHour` / `perDay` / `action` |
| `mail.*` | 重建 SMTP 连接池 |
| `oauth.*` | 刷新 OAuth 配置缓存 |
| `security.*` | 立即生效于后续签发 |
| `log.retentionDays` | 重排清理任务 |

- 写 `secret` 类传 `"******"` 表示**不修改**；传 `""` 表示清空。
- 写 `OperationLogs:setting.update`（`Detail` 含变更键名，**不含值**；`secret` 类记 `Changed: true`）。

### `GET /api/web/v1/settings/api-tokens`

```jsonc
// data
{ "Items": [ { "UID": "tk_01...", "Name": "CI 上传", "Prefix": "pcw_ab12cd34",
               "LastUsedAt": 1789347956, "ExpiresAt": 0, "CreatedAt": 1789000000 } ] }
```

> 永不过期用 `ExpiresAt = 0` 表示。**永不返回完整令牌**。

### `POST /api/web/v1/settings/api-tokens`

```jsonc
// req
{ "Name": "CI 上传", "ExpiresInDays": 0 }   // 0 = 永不过期
// data
{ "UID": "tk_01...", "Name": "CI 上传", "Token": "pcw_9f2b...", "Prefix": "pcw_9f2b",
  "ExpiresAt": 0, "CreatedAt": 1789000000 }
```

> ⚠️ **`Token` 明文仅在此响应中出现一次**，之后不可再获取（D31）。
> 前端必须显著提示用户立即保存。写 `OperationLogs:setting.update`。

### `DELETE /api/web/v1/settings/api-tokens/{Uid}`

吊销自己的令牌。`Data: null`。写 `OperationLogs:setting.update`。

---

## 12. Lsky API 兼容层

> **前缀：`/api/v1/**`（独占，外部冻结契约）**。D52：**只做 Lsky v1 契约**
> （与 lsky-pro 2.x / skyImage 对齐）。
> 目的：让 PicGo 桌面端 / PicList / uPic / ShareX **直接把本项目当图床用**。

### 12.1 与内部 API 的关键差异

| 差异 | 内部 API | Lsky 兼容层 |
|---|---|---|
| **路径前缀** | `/api/web/v1/**` | **`/api/v1/**`** |
| **响应信封** | `{Code:int, Message, Data}` | **`{status:bool, message, data}`** |
| **字段命名** | PascalCase（D81） | **snake_case**（外部冻结契约，**不受 D81 影响**） |
| **分页形状** | `{Items, Total, Page, PageSize}` | Laravel 形状 `{data[], current_page, last_page, ...}` |
| **时间格式** | Unix 秒（int64） | **字符串 `Y-m-d H:i:s`** |

**为什么可以独占 `/api/v1`（D80）**：第三方插件把路径**硬编码**为
`${server}/api/v1/upload`、`${server}/api/v1/images/{key}`，且要求 `server` 配置
**不能以 `/` 结尾**。**实测**（`picgo-plugin-lankong` / `lskypro-own` / `lskypro` /
`lsky-uploader`）它们全部使用**字符串拼接**，**没有一个用 `new URL()`**——
因此 Lsky 层挂在根级 `/api/v1` 是兼容性最好的选择（用户只需填**裸域名**，零后缀）。

**无冲突**：内部 API 在 `/api/web/v1`，两者前缀不同、互不重叠（§0.1）。
**启动时仍做冲突检测**，仅作为「防止将来新增内部接口时误用 `/api/v1`」的防护。

**鉴权复用**：Lsky 令牌**就是**本项目的 API Token（`APITokens` 表，D31），
不另建体系。客户端用 `Authorization: Bearer <token>`，值为 `pcw_<random>`。

> 说明：lsky-pro 原版用 Sanctum 生成 `1|xxxx` 形式的令牌，
> 但第三方插件只是**原样透传**该字符串到 `Authorization` 头，因此任意不透明串均可工作。
>
> **⚠️ 本节的 JSON 示例一律保持 snake_case**，这是刻意的（外部契约）。

### 12.2 响应信封示例

成功：

```jsonc
{ "status": true, "message": "success", "data": { } }
```

失败（HTTP 4xx/5xx）：

```jsonc
{ "status": false, "message": "Unauthenticated.", "data": null }
```

**错误码对应**（把内部 `Code` 映射到 Lsky 语义）：

| 内部 `Code` | HTTP | Lsky `message` |
|---|---|---|
| `40101` / `40102` / `40103` | 401 | `Unauthenticated.` |
| `40104` | 403 | `Account disabled.` |
| `40301` | 403 | `Forbidden.` |
| `40302` | 403 | `Insufficient storage capacity.` |
| `40401` | 404 | `Not Found.` |
| `40901` | 409 | 具体冲突原因 |
| `42901` | 429 | `Too Many Requests.` |
| `40001` | 422 | 具体校验错误 |
| `50001` ~ `50004` | 500 | 具体错误原因 |

### 12.3 端点清单

#### `POST /api/v1/tokens`

邮箱 + 密码换取令牌。

```jsonc
// req
{ "email": "user@example.com", "password": "xxx" }
// 完整响应体（含 Lsky 信封）
{ "status": true, "message": "success",
  "data": { "token": "pcw_9f2b7c...", "name": "lsky" } }
```

- 内部实现：校验密码 → 在 `APITokens` 新建一条（`Name = "lsky"`）→ 返回明文一次。
- 失败 → 401，`message = "These credentials do not match our records."`
- 同样受 `security.loginMaxAttempts` 限流，并写 `LoginAttempts` / `OperationLogs`。

#### `DELETE /api/v1/tokens`

清空当前用户的**全部** API Token（含 Lsky 创建的）。

```jsonc
{ "status": true, "message": "success", "data": null }
```

#### `GET /api/v1/profile`

```jsonc
// 完整响应体（含 Lsky 信封）
{
  "status": true, "message": "success",
  "data": {
    "id": "us_01JD9X...",
    "email": "user@example.com",
    "name": "小明",
    "capacityBytes": 5368709120,     // 字节；0 = 不限额
    "usedBytes": 1263616,
    "imageNum": 12,
    "albumNum": 0,                 // 恒为 0：相册功能已移除（D101），仅保留契约字段
    "capacity": 5242880,             // KB —— 兼容 lsky 字段与单位
    "useCapacity": 1234              // KB
  }
}
```

> 同时给出**字节**（本项目口径，精确）与 **KB**（lsky 口径，兼容）。见 §15。

#### `GET /api/v1/strategies`

**无需鉴权**（对齐 lsky：游客也可取策略列表）。返回可用的存储配置。

```jsonc
// 完整响应体（含 Lsky 信封）
{
  "status": true, "message": "success",
  "data": [
    { "id": "st_01JD9X...", "name": "GitHub 主仓", "intro": "github", "key": "github",
      "default": true }
  ]
}
```

- `id` 用本项目的 `StorageUID`（**不是数字**）。客户端会把 `strategy_id` 原样回传，
  因此 `POST /api/v1/upload` 需接受**字符串** id（见下）。

#### `POST /api/v1/upload`

`multipart/form-data`（**字段名是 Lsky 契约，保持 snake_case**）。

| 字段 | 必填 | 说明 |
|---|---|---|
| `file` | ✅ | 单文件（lsky 契约即单文件） |
| `strategy_id` | ⬜ | `StorageUID`；省略则用当前默认存储配置 |
| `album_id` | ⬜ | **收下但忽略**：相册功能已移除（D101），仅为不破坏客户端而兼容该字段 |
| `permission` | ⬜ | lsky 兼容字段，本项目**忽略**（图片公开性由图床决定，D33/D66） |

```jsonc
// 完整响应体（含 Lsky 信封）
{
  "status": true, "message": "上传成功",
  "data": {
    "key": "up_01JD9A...",
    "name": "a.png",
    "origin_name": "a.png",
    "pathname": "img/2026/02/a.png",
    "size": 12345,
    "mimetype": "image/png",
    "extension": "png",
    "width": 800, "height": 600,
    "url": "https://cdn.example.com/img/2026/02/a.png",
    "links": {
      "url": "https://cdn.example.com/img/2026/02/a.png",
      "html": "<img src=\"https://cdn.example.com/img/2026/02/a.png\" alt=\"a.png\" />",
      "markdown": "![a.png](https://cdn.example.com/img/2026/02/a.png)",
      "bbcode": "[img]https://cdn.example.com/img/2026/02/a.png[/img]",
      "markdown_with_link": "[![a.png](https://cdn.example.com/img/2026/02/a.png)](https://cdn.example.com/img/2026/02/a.png)",
      "thumbnail_url": ""
    }
  }
}
```

- **同步语义**：该端点**阻塞**直到上传完成（客户端期待立即拿到 URL），
  内部仍走同一个队列（`upload.concurrency` 生效，D35）。
- `links` 中 `url` / `html` / `markdown` 为本项目原生支持的三种（D68）；
  `bbcode` / `markdown_with_link` / `thumbnail_url` 为契约完整性而派生
  （前两者由 url 简单拼装；`thumbnail_url` 无缩略图时返回 `""`）。
- 返回的 `key` 是 `Uploads.UID`，供 `DELETE /api/v1/images/{key}` 使用。
- `pathname` 为**远端路径**（由图床驱动决定），本项目不解析也不保证其结构。

#### `GET /api/v1/images`

```jsonc
// 完整响应体（含 Lsky 信封）—— 对齐 Laravel 分页结构（lsky 客户端依赖）
{
  "status": true, "message": "success",
  "data": {
    "data": [
      { "key": "up_01JD9A...", "name": "a.png", "pathname": "img/2026/02/a.png",
        "size": 12345, "mimetype": "image/png", "extension": "png",
        "width": 800, "height": 600,
        "url": "https://cdn.example.com/img/2026/02/a.png",
        "links": { /* 同上 */ },
        "created_at": "2026-02-14 09:30:15", "updated_at": "2026-02-14 09:30:15" }
    ],
    "current_page": 1, "last_page": 7, "per_page": 20, "total": 128,
    "from": 1, "to": 20
  }
}
```

Query：`page` `per_page`（默认 20）`order`（`newest` / `oldest`）
（`album_id` 会被收下但**忽略**，D101）

> **注意**：Lsky 契约的时间字段是**字符串**（`Y-m-d H:i:s`），
> 外层信封的 `data.data` 是**双重 data**（Laravel 分页器形状）。内部 API 无此形状。

#### `DELETE /api/v1/images/{key}`

`{key}` = `Uploads.UID`。

```jsonc
{ "status": true, "message": "删除成功", "data": null }
```

- 走与 §4 相同的删除流程（硬删除 + `OperationLogs`）。
- **是否同时删远端**：Lsky 契约无此参数。默认 **`deleteRemote = false`**（保守，避免误删）；
  可由管理员在设置中开启 `integration.lsky.deleteRemoteOnDelete`（默认 `false`）改为同步远端删除。
- 不存在或非本人 → 404。

#### `GET /api/v1/albums`

**伪造响应**（D101）：相册功能已移除，但该路径属于外部冻结的保留集（D80）——
桌面端客户端会拉它填充下拉框，404 会让客户端报错。因此恒返回空列表：

```jsonc
// 完整响应体（含 Lsky 信封）
{ "status": true, "message": "success", "data": [] }
```

#### `DELETE /api/v1/albums/{id}`

**伪造响应**（D101）：无实际对象可删，恒返回成功。

```jsonc
{ "status": true, "message": "删除成功", "data": null }
```

### 12.4 收益与已验证的客户端

以下 npm 包**可直接接入**（已核实存在，且其请求路径与本契约一致）：

| 包 | 说明 |
|---|---|
| `picgo-plugin-lankong` | 兰空图床，支持 V1/V2；上传 `POST /api/v1/upload`、删除 `DELETE /api/v1/images/{key}` |
| `picgo-plugin-lskypro` | lskypro 图床 |
| `picgo-plugin-lsky-uploader` | lskypro 图床 |
| `picgo-plugin-lskypro-own` | 支持 V1/V2 |

**配置方式**：在插件的「服务器地址」中填入本项目地址（**不要以 `/` 结尾**），
令牌填 `POST /api/v1/tokens` 返回的 `pcw_...`。

---

## 13. picgo-agent 内部契约

> **仅监听 `127.0.0.1:36678`**（D7）。
> 所有请求必须带 `X-Agent-Token: <PICGO_WEB_AGENT_TOKEN>`（Go 启动时生成并注入 agent env），
> 缺失或错误 → `401`。
>
> 统一响应体 **`{Code, Message, Data}`，`Code` 为字符串**：
> `OK` / `ERR_PARAM` / `ERR_PICGO` / `ERR_INTERNAL` / `ERR_NOT_FOUND`。
>
> **JSON 字段命名**：agent 是**我们自己的代码**，因此**外层字段用 PascalCase**（D81）。
> **唯一例外**：从 picgo 读来的**原生配置结构与驱动字段名**（`picBed` / `picgoPlugins` /
> `_id` / `_configName` / `repo` / `token` …）**必须原样透传、不得转换**（D81.3 第 5 条）。
>
> 该契约由 Go 侧**单向消费**；agent 内部实现可重构（D77.2：Go 只依赖契约）。

### 13.1 健康与生命周期

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | **免 token**。`{ Ok, PicgoVersion, ConfigPath, Uptime, PID }` |
| POST | `/api/shutdown` | 优雅退出（Go 关闭子进程时调用）。`{ Code: "OK" }` |

`GET /healthz` → `Data`：

```jsonc
{ "Ok": true, "PicgoVersion": "3.0.2", "ConfigPath": "/data/picgo/config.json",
  "Uptime": 3600, "PID": 12345, "PluginsLoaded": 3 }
```

### 13.2 配置

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/config` | 完整 picgo config（**未脱敏**，仅内网） |
| PUT | `/api/config` | 整体替换 `{ Config }`（**危险，日常不用**） |
| PATCH | `/api/config` | 点路径合并 `{ Patch: { "picBed.uploader": "github" } }` |

`PATCH /api/config` → `Data`：

```jsonc
{ "Applied": ["picBed.uploader"], "PreservedPluginKeys": ["uploaded", "picgo-plugin-github-plus"] }
```

> ⚠️ **`Patch` 的键是 picgo config 的点路径，原样**（`picBed.uploader`），
> 因为它直接作用于 picgo 的配置对象结构。
>
> **键级合并（不只是 PATCH 语义，而是硬约束，D22）**：agent **只允许**修改我们管辖的键——
> `picBed.*` / `uploader.*` / `picgoPlugins.*` / `settings.*`；
> 其余键（插件私有状态，如 `uploaded`）**必须原样保留且不得删除**。
> `PUT /api/config` 会**拒绝**请求体中缺少我们管辖键的替换，防误毁插件状态。

### 13.3 图床与配置表单

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/uploaders` | 全部 uploader（含能力探测） |
| POST | `/api/uploaders/schema` | `DependsOn` 联动**重求值** |
| **POST** | **`/api/uploaders/test`** | **连通性测试**基元：`{Type, ConfigName?, Config?, Answers?}` → `{Ok, Message, LatencyMs, Detail?}`。支持两种模式：传 `Config` 则**不落盘**直接测（用于新建表单）；只传 `ConfigName` 则测已保存的配置 |
| GET | `/api/uploaders/configs?type=github` | 某类型的多配置列表 |
| POST | `/api/uploaders/configs` | 新建/更新配置（`createOrUpdate`） |
| DELETE | `/api/uploaders/configs?type=&configName=` | 删除配置 |
| POST | `/api/uploader/use` | 切换当前上传器 |
| GET | `/api/transformers` | transformer 列表 |

> **`POST /api/uploaders/test` 的实现方式**：picgo-core 没有现成的「测试驱动」 API。
> agent 采用「用该配置真实上传一份内置的 1x1 像素 PNG（或平台最小合法文件）」的方式探测，
> 并在结束后尽量清理。若驱动不允许删除，则保留一个 `_picgo-web-connectivity-test` 前缀的小文件，
> 该行为需在 UI 上向管理员说明。不同驱动也可通过 `Capabilities` 标记是否有更轻量的探测方式。

`GET /api/uploaders` → `Data`：

```jsonc
{
  "Uploaders": [
    {
      "Type": "github",
      "Name": "GitHub",
      "Builtin": true,
      "GuiOnly": false,
      "Config": [ /* DriverConfigField[]，已 evaluatePluginConfig 求值 */ ],
      "Capabilities": {
        "SupportsPathTemplate": true,
        "SupportsRemoteDelete": true,
        "ServerRenames": false,
        "ConfigFields": ["repo", "branch", "token", "path", "customUrl"],
        "PathFieldNames": ["path"],
        "DetectedAt": 1789347956,
        "PicgoVersion": "3.0.2"
      },
      "ConfigNames": ["Default", "Work"]
    }
  ],
  "Current": { "Type": "github", "ConfigName": "Default" },
  "Transformer": "path"
}
```

#### 能力探测规则（**不硬编码驱动名**，D77.2）

| 能力 | 判定方式 |
|---|---|
| `SupportsPathTemplate` | 该驱动 `config(ctx)` schema 中存在 `path` / `root` / `basePath` 类字段（字段名列表见 `PathFieldNames`） |
| `SupportsRemoteDelete` | 通过 `picgo.eventNames()` / 插件实例检查是否存在 `remove` 监听器；无法判定时保守返回 `false` |
| `GuiOnly` | 插件实例含 `guiMenu` 或 `commands`（Electron 专属能力） |

> 探测结果由 agent 计算，Go 侧缓存进 `StorageConfigs.Capabilities`
> （[`DATA-MODEL.md`](./DATA-MODEL.md) §3.1）。

`POST /api/uploaders/schema`：

```jsonc
// req
{ "Type": "github", "Answers": { "repo": "org/repo" } }
// data
{ "Type": "github", "Name": "GitHub", "Config": [ /* 重新求值后的字段 */ ] }
```

> **为什么必须回服务端求值**：插件的 `config(ctx)` 中 `default` / `choices` 可能是**函数**
> （依赖其他字段的当前值）。求值时需用 **schema-only context** 隐藏该 uploader 自身的
> 已有配置，否则插件读自己的历史值会把 `default` 覆盖成旧值（见
> [`PICGO-INTEGRATION.md`](./PICGO-INTEGRATION.md)）。

`GET /api/uploaders/configs?type=github` → `Data`：

```jsonc
{ "Type": "github", "DefaultConfigName": "Default",
  "Configs": [
    { "_id": "st_01JD9X...", "_configName": "Default",
      "_createdAt": 1789000000, "_updatedAt": 1789000000,
      "repo": "org/repo", "branch": "main", "token": "ghp_xxx", "path": "img/" }
  ] }
```

> ⚠️ `Configs[]` 的元素是 **picgo 的原生配置项**（`_id` / `_configName` 及驱动字段），
> **键名原样保留、不得转换**（D81.3 第 5 条）。我们的**外层**键（`Type` / `Configs` /
> `DefaultConfigName`）才是 PascalCase。
>
> ⚠️ agent 侧配置**未脱敏**（明文），因为它就是 picgo 的真实存储。
> **脱敏是 Go 侧的职责**——Go 绝不可把该响应原样回传给前端。

`POST /api/uploaders/configs`：

```jsonc
// req —— Config 的键是驱动字段名（原样）
{ "Type": "github", "ConfigName": "Work", "Activate": false,
  "Config": { "repo": "org/repo2", "branch": "main", "token": "ghp_yyy", "path": "img/" } }
// data
{ "Type": "github", "Config": { "_id": "st_01JD9Y...", "_configName": "Work" } }
```

- `_id` 由 picgo 生成（`uploaderConfig.createOrUpdate`）；**Go 侧把它作为 `StorageConfigs.UID`**（D65）。
- 已存在同 `_configName` 则更新（`createOrUpdate` 语义）。

### 13.4 插件

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/plugins` | 已装插件（含 `GuiOnly`） |
| GET | `/api/plugins/{name}/readme` | `{ Content, Path }` |
| POST | `/api/plugins/install` | `{ Names: [] }` → `{ JobUID }` |
| POST | `/api/plugins/uninstall` | `{ Names: [] }` → `{ JobUID }` |
| POST | `/api/plugins/update` | `{ Names: [] }` → `{ JobUID }` |
| PATCH | `/api/plugins/{name}` | `{ Enabled }` |

`GET /api/plugins` → `Data`：

```jsonc
{ "Plugins": [
    { "Name": "picgo-plugin-github-plus", "Version": "1.2.3", "Enabled": true,
      "GuiOnly": false, "Uploader": "githubPlus", "Transformer": "",
      "Description": "GitHub 图床增强", "Author": "zwing", "Homepage": "https://..." }
  ],
  "Disabled": ["picgo-plugin-x"] }
```

> 安装/卸载/更新完成后，agent **重启自身进程**以使插件生效（由 Go 重新拉起）。
> 重启前会先通过 `GET /api/events` 推一条 `system.notice`。

### 13.5 上传（**单文件 + 同步**，D39）

#### `POST /api/upload`

**单文件、阻塞直到完成**。

```jsonc
// req
{
  "Path": "/data/uploads/2026/02/a.png",   // 本地文件路径
  "Uploader": { "Type": "github", "ConfigName": "Default" },  // 省略则用当前激活配置
  "JobUID": "job_01JD9X...",               // 仅用于日志与事件归属
  "Seq": 1,
  "FileName": "20260214_a.png",            // 可选：魔法文件名（覆盖改名结果）
  "PathTemplate": "img/{Y}/{m}",           // 可选：魔法路径模板（D43/D70）
  "FileTemplate": "{uniqid}{extname}"      // 可选：魔法文件名模板
}

// data —— 上传成功后
{
  "Seq": 1,
  "URL": "https://cdn.example.com/img/2026/02/a.png",
  "ThumbURL": "",
  "FileName": "20260214_a.png",
  "Extname": ".png",
  "Width": 800, "Height": 600, "Size": 12345,
  "ContentType": "image/png",
  "UploaderType": "github",
  "Raw": { /* 完整 IImgInfo，含插件回写字段（如 github 的 sha）——供删除使用，D47 */ }
}
```

失败 → `Code = "ERR_PICGO"`，`Message` 为原因，`Data` 含：

```jsonc
{ "Seq": 1, "Error": "图床返回 422", "Raw": null }
```

**关键语义**：

| 项 | 说明 |
|---|---|
| 单文件 | 一次只传一个文件。进度事件天然归属该文件（四档 `0/30/60/100` 即该文件进度） |
| 同步 | 阻塞到完成；**上传失败不返回 5xx**，而是 `Code = "ERR_PICGO"`（HTTP 200），便于 Go 逐项记录失败原因 |
| 魔法路径 | agent 在 `beforeUploadPlugins` 钩子中改写 `ctx.output[i].fileName`（见 [`PICGO-INTEGRATION.md`](./PICGO-INTEGRATION.md)）；驱动不支持路径时降级为文件名前缀 |
| `Raw` | **必须回传**。Go 侧存入 `UploadResults.RawOutput`，否则插件回写的字段丢失后再也无法删远端（D47） |
| 并发 | agent 内部串行（队列并发度由 Go 控制，默认 1，D35）；`Concurrency > 1` 需 picgo-core 补丁 |

> ⚠️ **`Raw` 内部的字段名是 picgo 的 `IImgInfo` 定义**（`fileName` / `imgUrl` / `extname` /
> `sha` …），**一律原样保留、不得转换为 PascalCase**（D81.3 第 5 条），
> 因为它会被原封不动交回插件用于删除（§13.5 的 `/api/delete`）。

**agent 如何把 `Uploader` 交给 picgo（**已落地的补丁**）**：

```ts
// picgo-agent 内部实现（伪代码）
const output = await picgo.upload([req.Path], {
  uploader: { type: req.Uploader?.Type, configName: req.Uploader?.ConfigName },
  contextData: { jobUid: req.JobUID, seq: req.Seq }   // 用于把全局事件归属到本次上传
})
```

| 场景 | picgo 行为 | agent 应对 |
|---|---|---|
| 正常上传成功 | 返回 `IImgInfo[]` | 取第一个元素作为 `Raw`，提取 `URL` / `Width` / `Height` … |
| 上传失败（网络/图床报错） | **不 reject、不返回 `Error`**，而是**吞掉异常并返回可能为空的数组**，同时 emit `failed` 事件 | **必须同时监听 `failed` 事件**才能判定失败（见下） |
| `Uploader.Type` 或 `ConfigName` **不存在** | **抛出异常**（补丁的前置校验） | 捕获后返回 **HTTP 400 / `ERR_PARAM`**，`Message` 指明哪个目标不存在 |

> **两个失败路径必须都覆盖**（这是实测结论，极易踩坑）：
> 1. **目标非法** → picgo **抛错** → agent 立即 `400 ERR_PARAM`
> 2. **上传失败** → picgo **返回空数组 + emit `failed`** → agent 判 `output.length === 0`
>    或监听 `failed` 事件后返回 `ERR_PICGO`

**补丁说明（`PicGo-Web` 分支，见 [`PICGO-INTEGRATION.md`](./PICGO-INTEGRATION.md) §8）**：

- `UploadOptions.uploader` —— 按次指定图床，**不修改共享 config**，使并发上传到不同驱动成为可能
- `UploadOptions.contextData` —— 透传到 `ctx.contextData`，用于把全局 `finished` / `failed`
  事件归属到具体 job（picgo 的事件是全局的，并发下无法区分来源）
- `Concurrency = 1`（默认）时 agent 走 `picgo.setConfig()`（内存、不落盘）串行切换，
  **完全不依赖补丁**；只有 `Concurrency > 1` 才走补丁路径

#### `POST /api/delete`

远端删除（走 picgo 的 `remove` 事件约定，D47）。

```jsonc
// req
{
  "UploaderType": "github",
  "Items": [ { /* 该图片上传时的完整 IImgInfo（即 UploadResults.RawOutput） */ } ]
}

// data
{ "RemoteDeleted": true, "Supported": true, "Message": "成功同步删除" }
```

**实现语义（必须了解其不可靠性）**：

- picgo-core **没有**删除 API，只能 `picgo.emit('remove', files, guiApi)`。
- 插件侧是 `ctx.on('remove', onRemove)`，**是 async 且 `emit` 无返回值、无人 await**。
- 因此 agent 需：
  1. 构造 **`guiApi` shim**（提供插件会解构的字段，主要是 `showNotification`）
  2. `emit('remove', items, shim)`
  3. 从 shim 捕获到的 `showNotification` 文案**反推成功/失败**
- 驱动未实现 `remove` → `Supported = false`、`RemoteDeleted = false`。

| 响应 | 含义 |
|---|---|
| `Supported=false` | 该驱动不支持远端删除，Go 侧仅删本地记录并在日志中说明 |
| `Supported=true, RemoteDeleted=false` | 插件实现了但执行失败，`Message` 含原因 |
| `Supported=true, RemoteDeleted=true` | 远端已删除 |

> ⚠️ `Items[]` 里的元素是 picgo 的 `IImgInfo`（原样字段名），**不是**我们的 `Upload` 对象。

### 13.6 任务、事件与日志

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/jobs` | Job 列表 |
| GET | `/api/jobs/{uid}` | Job 详情 |
| DELETE | `/api/jobs/{uid}` | 清理已完成 Job |
| GET | `/api/events` | SSE |
| GET | `/api/logs?tail=200` | `picgo.log` 尾部 |

**Job 对象（agent 侧）**

```ts
{
  UID: string; Kind: string;
  Status: 'queued' | 'running' | 'succeeded' | 'failed';
  Progress: number;                 // 0..100
  Payload: Record<string, unknown>;
  Result: Record<string, unknown> | null;
  Error: string;
  CreatedAt: number; StartedAt: number; FinishedAt: number;
}
```

> agent 侧只维护**执行期**状态（内存 + 少量落盘），**不承担审计职责**——
> Go 侧的 `Jobs` / `JobItems` / `OperationLogs` 才是持久化真相源。

**`GET /api/events`（SSE）** 事件与 §8.1 同名同结构
（`upload.progress` / `upload.finished` / `upload.failed` / `job.log` / `job.finished` /
`job.started` / `system.notice` / `ping`），25 秒 `ping` 保活。
Go 侧订阅后转发并入自己的 SSE Hub。

> 事件体字段用 PascalCase（与 §8.1 一致）；**事件名保持小写点分**。

---

## 14. 前端 API 客户端约定

| 项 | 约定 |
|---|---|
| Axios 实例 | `web/src/lib/http.ts`，**`baseURL = '/api/web/v1'`**（D80），`withCredentials = true` |
| 类型定义 | `web/src/types/api.ts`，**必须与本文件手动保持同步**；每个实体对应一个 TS 类型 |
| **类型字段命名** | **PascalCase**，与本文档完全一致（D81）：`interface Upload { UID: string; JobUID: string }` |
| **变量 / 函数 / store 命名** | **camelCase**（不变，符合 JS 生态）：`const accessToken = res.AccessToken`、`fetchUploads()` |
| 响应拦截器 | `Code !== 0` → 抛出 `ApiError(Code, Message, Data)`；包括 HTTP 错误状态中的统一信封；网络/5xx → 抛 `ApiError(-1, ...)` |
| 401 处理 | HTTP 401 或 `Code=40102/40103` → 尝试**静默刷新**（`POST /auth/refresh`）并重放原请求；刷新失败 → 清登录态并跳 `/login` |
| `40104` | 账号禁用 → 清登录态，显示「账号已被禁用」 |
| 强制改密 | 收到 `40301` 且 `Message` 含「请先修改密码」→ 跳 `/password` |
| SSE | 统一走 `web/src/lib/sse.ts`（基于 `EventSource`，带心跳看护 + 指数退避重连；重连前用 `GET /auth/me` 触发过期 access token 刷新；重连后状态对齐），URL 为 `/api/web/v1/events` |
| 上传 | `FormData` + `onUploadProgress`（浏览器侧进度）+ SSE（服务端队列进度），**以 SSE 为准**；表单字段名用 PascalCase（`Files` / `StorageUID`） |
| 错误提示 | 直接 toast `Message`（后端已给中文可读文案） |
| i18n | 所有面向用户文案走 i18n key（D75）；后端返回的 `Message` 可直接展示，不走 i18n |

**Lsky 兼容层不写入 `types/api.ts`**：它是给第三方客户端用的，前端不调用。

**`ApiError` 形状**

```ts
class ApiError extends Error {
  Code: number          // 0 表示成功以外的业务码；-1 表示网络层错误
  Data: unknown         // 后端 Data 原样
  HTTPStatus: number    // HTTP 状态码
}
```

**枚举容错要求**（D77）：前端渲染 `Status` / `Type` / `Kind` 等枚举时，
必须为**未知取值**提供降级展示（显示原始字符串 + 中性样式），不得因新增枚举值而崩溃。

**PascalCase 与 camelCase 的衔接示例**

```ts
// 类型层用 PascalCase（与 API 一致，零转换）
interface Upload {
  UID: string
  FileName: string
  StorageUID: string
  CreatedAt: number
}

// 使用层用 camelCase（JS 惯例）
export async function fetchUploads(params: { page: number; pageSize: number }) {
  const { Data } = await http.get<PageResult<Upload>>('/uploads', { params })
  const totalSize = Data.Items.reduce((sum, item) => sum + item.Size, 0)
  return { items: Data.Items, totalSize }
}
```

> **不要**在类型层做 camelCase 转换（如 `uid` / `fileName`）——
> 那会引入一层映射代码和命名不一致（D81 的初衷就是消除这种割裂）。

---

## 15. 与决策的偏差

> 本节记录编写过程中发现的疑问与已裁决结论。**实现时照此执行即可**。

### 15.1 ✅ 已由 D80 解决：API 路径分层（**历史记录**）

**曾经的冲突**：早期 D52 要求 Lsky 端点在 `/api/v1/**`，而 D77.2 要求内部接口也在 `/api/v1/**`，
两者在 `GET /api/v1/albums` / `DELETE /api/v1/albums/{id}` **直接撞车**（内部用 `UID`，
Lsky 用数字 `id`），语义也不同。

**当时的临时方案**（已废弃）：共享 `/api/v1`，内部相册让位至 `/api/v1/gallery/albums`。

**最终裁决（D80）**：

| 项 | 值 |
|---|---|
| 内部 API | **`/api/web/v1/**`** |
| Lsky 兼容层 | **`/api/v1/**`**（独占） |
| 冲突 | **不存在**（前缀天然隔离） |
| 防护 | 启动时做**路由冲突检测**：内部路由若落入 Lsky 保留集则 **panic** |

> 表中曾经的「内部相册路径」一行已随相册功能移除（D101）而作废。

**实证依据**：生态里全部兰空插件都是**字符串拼接** `${serverUrl}/api/v1/upload`
（实测 `picgo-plugin-lankong` / `lskypro-own` / `lskypro` / `lsky-uploader`，
**无一使用 `new URL()`**），因此 Lsky 层可安全独占根级 `/api/v1`。

### 15.2 ✅ 已裁决：登录字段用 `Email`

D23 只规定「只认邮箱」，未规定字段名。**统一用 `Email`**（PascalCase，不用 `Username`），
所有文档与前端类型以此为准。

### 15.3 ✅ 已裁决：Lsky 配额双单位返回

内部口径为**字节**（D20）；lsky-pro 的 `capacity` / `useCapacity` 为 **KB**。
§12.3 的 `GET /profile` **同时返回两套**（`capacityBytes` / `usedBytes` 字节 +
`capacity` / `useCapacity` KB）。这是刻意的字段冗余（兼容第三方客户端），不是笔误。

### 15.4 ✅ 已裁决：Lsky 删除默认不删远端

Lsky 的 `DELETE /api/v1/images/{key}` 契约里**没有**远端删除参数。
默认 **`deleteRemote = false`**（保守），由配置键
`integration.lsky.deleteRemoteOnDelete`（默认 `false`）控制。
该键**已登记至** [`DATA-MODEL.md`](./DATA-MODEL.md) §7.4 的 `category=integration` 表。

### 15.5 ✅ 已裁决：存储参数用 `StorageUID`

D38 原文写 `storageConfigId`，与 D64（一律用 `UID`）冲突。
**按 D64 采用 `StorageUID`**（PascalCase + 字符串 ULID）。Lsky 契约里 `strategy_id`
的值也是 `StorageUID` 字符串。

### 15.6 ✅ 已裁决：新增端点与配置键（均已登记）

| 新增项 | 位置 | 登记处 |
|---|---|---|
| `POST /api/uploaders/test`（agent） | §13.3 | 本文档 |
| `system.notice` SSE 事件 | §8.1 | 本文档 |
| `integration.lsky.enabled` | §11 / §12 | DATA-MODEL §7.4 |
| `integration.lsky.deleteRemoteOnDelete` | §15.4 | DATA-MODEL §7.4 |
| `integration.lsky.tokenTtlDays` | §12 | DATA-MODEL §7.4 |

### 15.7 ✅ 已裁决：`JobItems` 不暴露 `UID`

- D77.1 要求「对外一律用 `UID`」；[`DATA-MODEL.md`](./DATA-MODEL.md) 的 `JobItems` 表
  **未定义 `UID` 列**（只有自增 `ID`）。
- 裁决：用 **「父 job 的 `UID` + `Seq`」** 定位批内子项，**不暴露自增 `ID`**
  （满足 D77 的核心意图：不泄露内部主键）。`Seq` 仅在批次内唯一，不跨批次引用。
- 同步要求：`JobItems` **不新增 `UID`**，保持现状。

### 15.8 ✅ 已修正：`DATA-MODEL.md` §4.3 的相册路径说明

`DATA-MODEL.md` §4.3 原写「对外路径为 `/api/v1/gallery/albums`」，与 D80 冲突。
**已修正**为 `/api/web/v1/albums`；相册功能本身后来已整体移除（D101），
该说明与对应表结构均已删除。

该修正只涉及文档表述，不改变任何表结构。

### 15.9 ✅ 已修正：`DECISIONS.md` 中的过期路径

上一轮发现的 `DECISIONS.md` 中的 D36 示意图与 D38 正文仍写 `/api/v1/uploads`，
以及 §4.3 相册注、§7.4 OAuth 回调地址、D77.2 版本段等旧写法，
**已由文档负责人全部同步为 `/api/web/v1/**` 与 PascalCase 字段名**。

### 15.10 ✅ 本轮新增的落地解释（非偏差，供实现参考）

| # | 事项 | 处理 |
|---|---|---|
| 1 | **Query 参数名用 PascalCase** | D81 只规定了「JSON 字段」与「路径段」，未明说 Query。本文档统一用 **PascalCase**（`Page` / `PageSize` / `Keyword` / `StorageUID`），与响应字段保持一致，避免前端两套拼写。**唯一例外**是 Lsky 层（`page` / `per_page`）。 |
| 2 | **multipart 表单字段名用 PascalCase** | 同上（`Files` / `StorageUID`）。**唯一例外**是 Lsky 的 `file` / `strategy_id`（§12.3）。 |
| 3 | **SSE 事件名保持小写点分** | 事件名是协议层标识符（`upload.progress`），不是 JSON 字段；**事件体内部字段**用 PascalCase。 |
| 4 | **`GET /healthz` 不使用信封** | 供容器/K8s 探针直接解析，字段名保持小写（`status` / `version` / `agent` / `uptime`）。这是唯一例外。 |
| 5 | **agent 的 `/api/config` 与 `.../configs` 内部键原样** | picgo 原生结构（`picBed` / `picgoPlugins` / `_id` / `_configName` / 驱动字段）透传不转换；只有我们的外层键用 PascalCase。 |
| 6 | **`Uploads.Sha256` 仅记录，无唯一索引** | D66 已取消去重，因此不存在「重复上传被拒绝」的行为——每次上传都真实打到图床。 |

---

## 附：端点索引

### 内部 API（`/api/web/v1/**`）

```
认证   POST   /api/web/v1/auth/login                (免鉴权)
       POST   /api/web/v1/auth/refresh              (免鉴权)
       POST   /api/web/v1/auth/logout
       GET    /api/web/v1/auth/me
       PATCH  /api/web/v1/auth/password
       GET    /api/web/v1/auth/identities
       POST   /api/web/v1/auth/oauth/github/start   (免鉴权)
       GET    /api/web/v1/auth/oauth/github/callback(免鉴权)
       POST   /api/web/v1/auth/oauth/github/bind
       DELETE /api/web/v1/auth/oauth/github

用户   GET    /api/web/v1/users
       POST   /api/web/v1/users
       GET    /api/web/v1/users/{Uid}
       PATCH  /api/web/v1/users/{Uid}
       DELETE /api/web/v1/users/{Uid}

存储   GET    /api/web/v1/storage/drivers
       POST   /api/web/v1/storage/drivers/schema
       GET    /api/web/v1/storage/configs
       POST   /api/web/v1/storage/configs
       GET    /api/web/v1/storage/configs/{Uid}
       PATCH  /api/web/v1/storage/configs/{Uid}
       PUT    /api/web/v1/storage/configs/{Uid}/secrets
       DELETE /api/web/v1/storage/configs/{Uid}
       POST   /api/web/v1/storage/configs/{Uid}/activate
       POST   /api/web/v1/storage/configs/{Uid}/test

图库   POST   /api/web/v1/uploads
       POST   /api/web/v1/uploads/from-url
       GET    /api/web/v1/uploads
       GET    /api/web/v1/uploads/{Uid}
       PATCH  /api/web/v1/uploads/{Uid}
       DELETE /api/web/v1/uploads/{Uid}
       POST   /api/web/v1/uploads/batch-delete
       GET    /api/web/v1/uploads/stats
       GET    /api/web/v1/uploads/{Uid}/link
       POST   /api/web/v1/uploads/links

插件   GET    /api/web/v1/plugins
       GET    /api/web/v1/plugins/search
       GET    /api/web/v1/plugins/{Name}/readme
       POST   /api/web/v1/plugins/install
       POST   /api/web/v1/plugins/uninstall
       POST   /api/web/v1/plugins/update
       PATCH  /api/web/v1/plugins/{Name}

内核   GET    /api/web/v1/picgo/config
       PATCH  /api/web/v1/picgo/config
       GET    /api/web/v1/picgo/uploader
       POST   /api/web/v1/picgo/uploader/use
       GET    /api/web/v1/picgo/transformers
       GET    /api/web/v1/picgo/logs
       POST   /api/web/v1/picgo/resync

任务   GET    /api/web/v1/jobs
       GET    /api/web/v1/jobs/{Uid}
       GET    /api/web/v1/jobs/{Uid}/logs
       DELETE /api/web/v1/jobs/{Uid}
       GET    /api/web/v1/events          (SSE)

日志   GET    /api/web/v1/logs
       GET    /api/web/v1/logs/types
       GET    /api/web/v1/logs/emails
       GET    /api/web/v1/logs/emails/{Uid}
       GET    /api/web/v1/logs/{Uid}

站点   GET    /api/web/v1/site/config        (免鉴权；含当前主题的设置，D83/D95)

主题   GET    /api/web/v1/themes                      (admin)
       POST   /api/web/v1/themes/rescan               (admin)
       POST   /api/web/v1/themes/install              (admin, multipart zip 或 JSON Git 地址, D96/D100)
       DELETE /api/web/v1/themes/{ThemeID}            (admin)
       PUT    /api/web/v1/themes/active               (admin)
       GET    /api/web/v1/themes/{ThemeID}/settings   (admin)
       PUT    /api/web/v1/themes/{ThemeID}/settings   (admin)
       DELETE /api/web/v1/themes/{ThemeID}/settings   (admin)
       GET    /api/web/v1/themes/{ThemeID}/screenshot (admin)

静态   GET    /assets/**                          (当前主题的静态资源)
       GET    /favicon.ico                        (当前主题的 favicon)
       GET    /*                                  (SPA 回退 → 当前主题的 index.html)

系统   GET    /healthz                   (免鉴权，无信封)
       GET    /api/web/v1/system/info
       GET    /api/web/v1/system/stats
       POST   /api/web/v1/system/picgo/resync

设置   GET    /api/web/v1/settings
       PUT    /api/web/v1/settings
       GET    /api/web/v1/settings/system
       PUT    /api/web/v1/settings/system
       GET    /api/web/v1/settings/api-tokens
       POST   /api/web/v1/settings/api-tokens
       DELETE /api/web/v1/settings/api-tokens/{Uid}
```

### Lsky 兼容层（`/api/v1/**`，snake_case 信封）

```
       POST   /api/v1/tokens                    (免鉴权)
       DELETE /api/v1/tokens
       GET    /api/v1/profile
       GET    /api/v1/strategies                (免鉴权)
       POST   /api/v1/upload
       GET    /api/v1/images
       DELETE /api/v1/images/{key}
       GET    /api/v1/albums                   (伪造响应：恒空列表，D101)
       DELETE /api/v1/albums/{id}              (伪造响应：恒成功，D101)
```

### picgo-agent（`127.0.0.1:36678`，需 `X-Agent-Token`）

```
       GET    /healthz                          (免 token)
       POST   /api/shutdown
       GET    /api/config
       PUT    /api/config
       PATCH  /api/config
       GET    /api/uploaders
       POST   /api/uploaders/schema
       POST   /api/uploaders/test
       GET    /api/uploaders/configs
       POST   /api/uploaders/configs
       DELETE /api/uploaders/configs
       POST   /api/uploader/use
       GET    /api/transformers
       GET    /api/plugins
       GET    /api/plugins/{name}/readme
       POST   /api/plugins/install
       POST   /api/plugins/uninstall
       POST   /api/plugins/update
       PATCH  /api/plugins/{name}
       POST   /api/upload
       POST   /api/delete
       GET    /api/jobs
       GET    /api/jobs/{uid}
       DELETE /api/jobs/{uid}
       GET    /api/events                       (SSE)
       GET    /api/logs
```
