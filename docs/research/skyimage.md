# skyImage 代码侦察报告

> 项目根目录（本地克隆）：`/tmp/pi-github-repos/runtime-Na2Ckq/9c2bd2707261b48f83d2863845090a86e6454db57c7fc3c26a77c9be1e2d5214`
> 技术栈：Go 1.25 + Gin + GORM（`go.mod`）；React 18 + Vite + TypeScript + TanStack Query + Zustand + Radix UI（`package.json`）
> 版本：`internal/version/version.go:4` → `Version = "0.2.4"`，`About = "skyImage-云端图床"`

---

## 1. 数据模型

所有模型集中在 `internal/data/models.go`（422 行）+ `internal/data/user_notification.go` + `internal/data/audit_profile.go`。
注册顺序见 `internal/data/database.go:69-95`（`AllModels()`），跨库迁移表清单见 `internal/data/database.go:98-125`（`MigrateTables()`）。

> 注意：**GORM tag 里没有 `uniqueIndex`/`index` 的字段就是普通列**；默认值以 gorm `default:` 为准；`datatypes.JSON` 落库为 `json` 类型。

### 1.1 `groups` — Group（`models.go:9-21`）

| 列 | Go 类型 | gorm tag | 说明 |
|---|---|---|---|
| id | uint | primaryKey | 自增 |
| name | string | size:64;not null;unique | 组名唯一（legacy 导入会做去重改名） |
| is_default | bool | default:false | 默认用户组 |
| is_guest | bool | default:false | 游客组 |
| configs | datatypes.JSON | type:json | 组策略：`max_file_size`、`max_capacity`、`default_visibility`、`upload_rate_minute`、`upload_rate_hour`（默认见 `installer/service.go:378-392`） |
| created_at / updated_at | time.Time | | |

### 1.2 `users` — User（`models.go:23-57`）

| 列 | Go 类型 | gorm tag | 说明 |
|---|---|---|---|
| id | uint | primaryKey;**autoIncrement:false** | 16 位公开 ID（`users/id.go`、`data/user_id_migrate.go`）；JSON 序列化为字符串 `json:"id,string"` |
| group_id | *uint | index | 指向 groups |
| name | string | size:128;not null | |
| email | string | size:255;uniqueIndex;not null | |
| password | string | column:password;size:255;default:'' | 即 `PasswordHash`，bcrypt |
| is_super_admin | bool | column:is_super_admin;default:false | 超管 |
| url | string | size:255 | 个人主页 |
| capacity | float64 | default:0 | 容量配额（字节） |
| capacity_bonus | float64 | column:capacity_bonus;default:0 | 相对角色组的容量增减（字节） |
| use_capacity | float64 | column:use_capacity;default:0 | 已用容量（`UsedCapacity`） |
| configs | datatypes.JSON | type:json | 用户偏好：`default_visibility`、`default_strategy`、`theme_preference`、`login_notification`、`public_profile`（前端解析见 `src/state/auth.ts`） |
| is_adminer | bool | column:is_adminer;default:false | `IsAdmin` |
| status | uint8 | default:1 | 1 正常 / 0 禁用 |
| email_verified_at | *time.Time | column:email_verified_at | |
| image_num | uint64 | column:image_num;default:0 | `ImageCount` |
| album_num | uint64 | column:album_num;default:0 | `AlbumCount` |
| registered_ip | string | column:registered_ip;size:64 | |
| remember_token | string | column:remember_token;size:255 | |
| membership_expires_at | *time.Time | index | 付费会员到期（shop） |
| membership_previous_group_id | *uint | index | 会员到期后要回退到的组 |
| membership_unit_price_micros | int64 | default:0 | 会员单价（price_cents*1e6/duration_days） |
| membership_active_product_id | *uint | | 当前会员商品 ID |
| created_at / updated_at | time.Time | | |
| Group | Group | foreignKey:GroupID | 关联 |

### 1.3 `user_oauth_bindings` — UserOAuthBinding（`models.go:59-74`）

| 列 | 类型 | tag |
|---|---|---|
| id | uint | primaryKey |
| user_id | uint | index;not null |
| provider | string | size:32;not null;uniqueIndex:idx_oauth_provider_uid |
| provider_user_id | string | size:255;not null;uniqueIndex:idx_oauth_provider_uid |
| provider_email | string | size:255 |
| provider_name | string | size:255 |
| avatar_url | string | size:512 |
| created_at / updated_at | time.Time | |

唯一约束为组合索引 `(provider, provider_user_id)`。

### 1.4 `user_passkeys` — UserPasskey（`models.go:79-93`）

| 列 | 类型 | tag |
|---|---|---|
| id | uint | primaryKey |
| user_id | uint | index;not null |
| name | string | size:128;not null |
| credential | string | type:text;not null（完整 webauthn.Credential JSON，字节级不可改） |
| last_used_at | *time.Time | |
| created_at / updated_at | time.Time | |

### 1.5 `passkey_challenges` — PasskeyChallenge（`models.go:96-107`）

| 列 | 类型 | tag |
|---|---|---|
| id | string | primaryKey;size:64 |
| user_id | uint | index;not null |
| action | string | size:16;not null（register / login） |
| session_data | string | type:text;not null |
| expires_at | time.Time | index;not null |
| created_at | time.Time | |

### 1.6 `oauth_states` — OAuthState（`models.go:110-121`）

| 列 | 类型 | tag |
|---|---|---|
| id | string | primaryKey;size:64 |
| provider | string | size:32;not null |
| mode | string | size:16;not null |
| user_id | uint | default:0 |
| code_verifier | string | size:128;default:'' |
| expires_at | time.Time | index;not null |
| created_at | time.Time | |

### 1.7 `files` — FileAsset（`models.go:124-161`）★核心图片/文件表

| 列 | Go 类型 | gorm tag | 说明 |
|---|---|---|---|
| id | uint | primaryKey | |
| user_id | uint | index | 上传者 |
| group_id | *uint | index | 上传时所处用户组 |
| strategy_id | uint | index | 所属存储策略 |
| key | string | size:64;**uniqueIndex**;not null | 对外唯一 key（Lsky 兼容删图用它） |
| path | string | size:512;not null | 绝对路径 / 远程对象 URL |
| relative_path | string | size:512;default:'' | 相对路径（后补列，`database.go:161`） |
| public_url | string | size:2048;default:'' | 公开访问 URL（后补列） |
| name | string | size:255;not null | 存储文件名 |
| original_name | string | size:255 | 原始文件名 |
| size | int64 | not null | 字节 |
| mime_type | string | size:64 | |
| extension | string | size:32 | |
| checksum_md5 | string | size:32 | |
| checksum_sha1 | string | size:40 | |
| width / height | int | default:0 | 宽高 |
| visibility | string | size:16;default:'private' | public / private（仅影响画廊展示） |
| storage_provider | string | size:32;default:'local' | 驱动快照 local/s3/minio/webdav/ftp/sftp |
| thumbnail_path | string | size:512;default:'' | 缩略图绝对路径/URL |
| thumbnail_relative_path | string | size:512;default:'' | |
| thumbnail_public_url | string | size:2048;default:'' | |
| thumbnail_storage_provider | string | size:32;default:'' | |
| thumbnail_strategy_id | *uint | index | 缩略图可存独立策略 |
| audit_status | string | size:16;default:'none' | none/pending/approved/rejected |
| audit_result | datatypes.JSON | type:json | |
| audit_checked_at / audit_reviewed_at | *time.Time | | |
| uploaded_ip | string | size:64 | |
| created_at / updated_at | time.Time | | |
| User / Strategy | User / Strategy | foreignKey | |

### 1.8 `strategies` — Strategy（`models.go:164-178`）

| 列 | 类型 | tag |
|---|---|---|
| id | uint | primaryKey |
| key | uint8 | column:key |
| name | string | size:64;not null |
| intro | string | size:255 |
| configs | datatypes.JSON | type:json（存储驱动完整配置，**敏感字段明文**） |
| created_at / updated_at | time.Time | |
| Files | []FileAsset | foreignKey:StrategyID |
| Groups | []Group | many2many:group_strategy |

**configs 明文保存，无加密。** 解析出的字段全清单见 `internal/files/service.go:97-147`（`strategyConfig`）：driver、root、url/base_url/baseUrl、pattern/path_template、query、allowed_extensions、webdav_*、ftp_*、s3_*、sftp_*（含 `sftp_private_key`、`sftp_password`）、compression/target_format、thumbnail_*、image_audit_profile_id 等；S3 的 `s3_secret_key`、FTP/SFTP/WebDAV 密码都是明文。
输出侧只在 API 层做脱敏 `redactSettings`/`redactSecret`（`internal/api/admin_handlers.go:106-131`）。

### 1.9 `group_strategy` — GroupStrategy（`models.go:180-187`）
| 列 | 类型 | tag |
|---|---|---|
| group_id | uint | primaryKey |
| strategy_id | uint | primaryKey |

### 1.10 `configs` — ConfigEntry（`models.go:189-198`）★站点设置 KV 表
| 列 | 类型 | tag |
|---|---|---|
| key | string | primaryKey |
| value | string | type:text |
| updated_at / created_at | time.Time | |

### 1.11 `installer_states` — InstallerState（`models.go:200-212`）
id PK；is_completed(bool,index)；version(size:32)；site_name(size:128)；completed_at；created_at/updated_at。

### 1.12 `sessions` — SessionEntry（`models.go:214-224`）
id(string,primaryKey,size:64)；user_id(uint,index,not null)；expires_at(time,index,not null)；created_at/updated_at。

### 1.13 `api_tokens` — ApiToken（`models.go:226-239`）
id PK；user_id(index,not null)；token(size:255,uniqueIndex,not null，**存 SHA-256 哈希**，`data/api_token_security.go`）；expires_at(index,not null)；last_used_at(*time,index)；created_at/updated_at；User 关联。
永不失效用 9999 年表示（`api_token_security.go:29-38`）。

### 1.14 `albums` — Album（`models.go:241-255`）
id PK；user_id(index,not null)；name(size:255,not null)；intro(size:512)；image_num(uint64,column:image_num,default:0)；created_at/updated_at；User 关联。

### 1.15 `redeem_codes` — RedeemCode（`models.go:257-278`）
id PK；code(size:64,uniqueIndex,not null)；reward_type(size:16,default:'group'|capacity)；group_id(*uint,index)；capacity_delta(float64,default:0)；max_uses(int,default:0)；used_count(int,default:0)；allow_multi_redeem(bool,default:false)；enabled(bool,default:true)；note(size:255)；created_by(uint,index,not null)；created_at/updated_at；Group/Creator 关联。

### 1.16 `redeem_code_usages` — RedeemCodeUsage（`models.go:280-291`）
id PK；redeem_code_id(index,not null)；user_id(index,not null)；created_at；User 关联。

### 1.17 `shop_products` — ShopProduct（`models.go:293-310`）
id PK；name(size:128,not null)；description(size:512)；price_cents(int64,not null,default:0)；currency(size:8,default:'CNY')；duration_days(int,not null,default:30)；group_id(uint,index,not null)；enabled(bool,default:true,index)；sort(int,default:0)；created_at/updated_at；Group 关联。

### 1.18 `shop_orders` — ShopOrder（`models.go:327-351`）
id PK；order_no(size:64,uniqueIndex,not null)；user_id(index,not null)；product_id(index,not null)；product_name(size:128,not null)；price_cents(int64,not null)；currency(size:8,default:'CNY')；duration_days(int,not null)；group_id(uint,index,not null)；status(size:16,index,default:'pending')；provider(size:16,index,not null)；provider_trade_no(size:128,default:'')；paid_at/fulfilled_at/membership_expires_at(*time)；notify_raw(text,`json:"-"`)；created_at/updated_at；User/Product/Group 关联。
状态常量：pending/paid/closed/failed；支付渠道：epay/alipay/wechat/stripe（`models.go:311-325`）。

### 1.19 `tickets` — Ticket（`models.go:368-385`）
id PK；ticket_no(size:32,uniqueIndex,not null)；user_id(index,not null)；subject(size:255,not null)；status(size:16,index,default:'open')；priority(size:16,index,default:'normal')；last_reply_at/closed_at(*time)；created_at/updated_at；User 关联。
状态：open/pending/resolved/closed；优先级 low/normal/high/urgent。

### 1.20 `ticket_messages` — TicketMessage（`models.go:387-400`）
id PK；ticket_id(index,not null)；user_id(index,not null)；body(text,not null)；is_staff(bool,default:false)；created_at；User 关联。

### 1.21 `ticket_attachments` — TicketAttachment（`models.go:402-421`）
id PK；ticket_id(index,not null)；message_id(*uint,index)；user_id(index,not null)；strategy_id(uint,index,not null)；key(size:64,uniqueIndex,not null)；path(size:512,not null)；relative_path(size:512,index,not null)；name(size:255,not null)；size(int64,not null)；mime_type(size:128)；storage_provider(size:32,default:'local')；created_at；User/Ticket 关联。

### 1.22 `user_notifications` — UserNotification（`internal/data/user_notification.go:8-22`）
id PK；user_id(index,not null)；type(size:64,not null)；title(size:255,not null)；message(text)；metadata(datatypes.JSON json)；read_at(*time,index)；created_at/updated_at；User 关联。

### 1.23 `audit_profiles` — AuditProfile（`internal/data/audit_profile.go:7-19`）
id PK；name(size:128,not null)；provider(size:64,not null)；configs(datatypes.JSON json，含腾讯云 secret_id/secret_key 明文)；created_at/updated_at。

---

## 2. 设置与配置系统

### 2.1 env 变量清单（`internal/config/config.go`）

| 变量 | Config 字段 | 默认值（`setDefaults()` 50-89 行） | 含义 |
|---|---|---|---|
| HTTP_ADDR | HTTPAddr | `:8080` | 监听地址 |
| DATABASE_TYPE | DatabaseType | `""` | sqlite/mysql/postgres |
| DATABASE_PATH | DatabasePath | `""` | SQLite 路径 |
| DATABASE_HOST/PORT/NAME/USER/PASSWORD | 同名 | `""` | MySQL/PG 连接 |
| STORAGE_PATH | StoragePath | `storage/uploads` | 本地存储根 |
| PUBLIC_BASE_URL | PublicBaseURL | `http://localhost:8080` | 公开访问基址 |
| LEGACY_DSN | LegacyDSN | 无默认 | 旧库 DSN（未在 .env.example 出现，仅 struct 有） |
| FRONTEND_DIST | FrontendDist | 无默认（example 为 `dist`） | 前端产物目录 |
| ALLOW_REGISTRATION | AllowRegistration | `true` | 是否允许注册（env 级兜底） |
| CORS_ALLOWED_ORIGINS | CORSAllowedOrigins | `""`（逗号分隔） | CORS 白名单 |
| TRUSTED_PROXIES | TrustedProxies | `""`（逗号分隔 CIDR/IP） | gin 可信代理 |
| DEMO_MODE | DemoMode | `false` | 演示站模式 |
| SITE_NAME | SiteName | `SkyImage Demo` | 仅演示站 |
| ADMIN_USERNAME/EMAIL/PASSWORD | 同名 | `demo_admin` / `demo@example.com` / `DemoPass123!` | 仅演示站 |
| DEMO_USER_USERNAME/EMAIL/PASSWORD | 同名 | `demo_user` / `user@example.com` / `UserPass123!` | 仅演示站 |
| SKIP_INSTALL | SkipInstall | `false` | 演示站跳过安装 |
| INSTALL_PASSWORD | InstallPassword | `""` | 安装向导密码，空则每次启动随机生成 |
| GIN_MODE | （直接读 os.Getenv） | release | debug/test |

`.env.example` 全文与实际变量一致（含注释），另注明 `GIN_MODE` 与 CORS/代理说明。`.env.demo.example` 是演示站扩展版。

### 2.2 站点设置存储与读取

- **存储**：全部放数据库 `configs` 表（KV），键前缀分类：`site.*`、`features.*`、`mail.*`、`oauth.*`、`captcha.*`、`tickets.*`、`images.load_rows`、`storage.root`、`account.disabled_notice` 等。初始写入清单见 `internal/installer/service.go:200-278`（默认设置批量 upsert）。
- **读取**：`internal/admin/service.go:135-166`（`GetSettings`/`UpdateSettings`，按 key upsert）。
- **对外暴露**：`GET /api/site/config`（`internal/api/site_handlers.go:29-96`）返回 title/description/logo/about/features 等；法务文本 `GET /api/site/legal/:type`（`site_handlers.go:98`）。
- **管理后台读写**：`internal/api/admin_settings_handlers.go`（站点/法务 51-296、通用 308-452、工单 454-576、邮件 578-770、验证码 772-1056、OAuth 1058-1266）；敏感字段读到前端时脱敏（`admin_handlers.go:106-131`），更新时用指针字段区分“未提交/清空”（如 `siteSettingsUpdatePayload`）。
- **配置来源边界**：
  - **.env（进程级）**：数据库连接、HTTP_ADDR、STORAGE_PATH、PUBLIC_BASE_URL、FRONTEND_DIST、CORS、信任代理、DEMO_*、INSTALL_PASSWORD、GIN_MODE。
  - **数据库 `configs`（运行时可改）**：站点信息、注册开关、邮件 SMTP、验证码、OAuth、工单、图片展示参数、`storage.root`。
  - 数据库连接由安装向导通过 `config.SaveDatabaseEnv`（`config.go:151-205`）写回 `.env`，故需要 `.env` 持久化挂载（docker-compose 注释明确说明）。

---

## 3. HTTP 接口面

路由总注册入口 `internal/api/server.go:344-361`（`registerRoutes`）。全局中间件：`gin.Logger`、`gin.Recovery`、`middleware.CORS`（`server.go:88-92`）。

### 3.1 路由清单（按模块）

**基础/静态**
| Method | Path | Handler | 文件 |
|---|---|---|---|
| GET | `/api/health` | `healthHandler` | `server.go:246,346` |
| GET | `/robots.txt` | `robotsHandler` | `server.go:346,360` |
| GET | `/favicon.ico` | `handleFavicon` | `site_handlers.go:24` |
| GET/HEAD | `/<publicSegment>/*filepath` | 本地文件/缩略图分发 | `server.go:468-469` |
| StaticFS | `/assets` | 前端资源 | `server.go:371` |
| NoRoute | 前端 SPA 回退 / 本地文件 | | `server.go:381` |

**安装向导 `/api/installer`**（`installer_handlers.go:15-26`）
| Method | Path | Handler |
|---|---|---|
| GET | `/api/installer/status` | `getInstallerStatus` |
| GET | `/api/installer/defaults` | `getInstallerDefaults` |
| POST | `/api/installer/verify` | `postInstallerVerify` |
| POST | `/api/installer/run` | `postInstallerRun`（需 `X-Install-Password`） |
| POST | `/api/installer/legacy/test` | `postInstallerLegacyTest` |
| POST | `/api/installer/legacy/import` | `postInstallerLegacyImport` |

**认证 `/api/auth`**（`auth_handlers.go:62-77`）
POST `/login`、`/register`、`/send-verification-code`、`/forgot-password`、`/reset-password`；GET `/reset-password-status`、`/needs-setup`、`/registration-status`、`/captcha-config`；POST `/logout`（登录+CSRF）；GET `/me`（登录）。

**OAuth**（`oauth_handlers.go:18-29`）
GET `/auth/oauth/providers`、`/auth/oauth/:provider/start`、`/auth/oauth/:provider/callback`（可选登录）；GET `/auth/oauth/bindings`、POST `/auth/oauth/:provider/bind`、DELETE `/auth/oauth/:provider`（登录+CSRF）。

**Passkey**（`passkey_handlers.go:20-34`）
POST `/auth/passkeys/login/begin`、`/login/complete`；POST `/auth/passkeys/register/begin`、`/register/complete`（+CSRF）；GET `/auth/passkeys`；PATCH `/auth/passkeys/:id`、DELETE `/auth/passkeys/:id`（+CSRF）。

**账户 `/api/account`**（`account_handlers.go:25-46`，全部需登录）
GET `/profile`、`/api-tokens`、`/notifications`；PUT `/profile`、DELETE `/profile`、POST `/api-token`、PATCH `/api-token/:id`、DELETE `/api-token/:id`、DELETE `/api-token`、PATCH `/notifications/:id/read`、POST `/notifications/read-all`、DELETE `/notifications`、POST `/redeem`（写操作 +CSRF）。

**工单**（`ticket_handlers.go:69-89`）
用户侧：GET `/api/account/tickets`、`/tickets/attachment-strategy`、`/tickets/:id`；POST `/tickets`、`/tickets/:id/replies`、`/tickets/:id/close`、`/tickets/:id/attachments`（写 +CSRF）。
管理侧：GET `/api/admin/tickets`、`/tickets/:id`；PATCH `/tickets/:id`；POST `/tickets/:id/replies`、`/tickets/:id/attachments`。

**管理 `/api/admin`**（`admin_handlers.go:26-88`，全部 `auth+RequireAdmin+RequireCSRF`）
GET `/metrics`、`/trends`、`/settings`、PUT `/settings`；
用户：GET `/users`、`/users/:id`、POST `/users`、DELETE `/users/:id`、PATCH `/users/:id/status`、POST `/users/:id/admin`、PATCH `/users/:id/group`、PATCH `/users/:id/capacity-bonus`；
组：GET/POST `/groups`、PUT/DELETE `/groups/:id`；
兑换码：GET/POST `/redeem-codes`、PUT/DELETE `/redeem-codes/:id`、GET `/redeem-codes/:id/usages`；
策略：GET/POST `/strategies`、PUT/DELETE `/strategies/:id`；
审核：GET/POST `/audits`、PUT/DELETE `/audits/:id`；
图片：GET `/images`、DELETE `/images/:id`、PATCH `/images/:id/visibility`、`/images/:id/audit-status`、`/images/batch/visibility`、POST `/images/batch/delete`；
系统设置：GET/PATCH `/system/site`、GET/PUT `/system/site/legal/:type`、GET/PATCH `/system/general`、`/system/tickets`、`/system/email`（+POST `/system/email/test`）、`/system/captcha`（+POST `/system/captcha/test`、`test-turnstile`）、`/system/oauth`、GET `/system/database`、POST `/system/database/test`、`/system/database/migrate`。
商店（`shop_handlers.go:235-243`）：GET/POST `/shop/products`、PUT/DELETE `/shop/products/:id`、GET `/shop/orders`、GET/PUT `/system/payment`。

**商店/支付**（`shop_handlers.go:17-33`）
公开：GET `/api/shop/products`、`/api/shop/providers`；
登录：POST `/api/shop/orders`、GET `/api/shop/orders`、`/api/shop/orders/:orderNo`、`/api/shop/membership`；
回调无鉴权：POST/GET `/api/pay/notify/:provider`、GET `/api/pay/return/:provider`。

**文件 `/api/files`**（`file_handlers.go:14-25`，登录）
GET ``、`/trends`、`/strategies`；POST ``（上传）；GET `/ :id`；DELETE `/:id`；PATCH `/:id/visibility`、`/batch/visibility`；POST `/batch/delete`。

**站点/公开**（`site_handlers.go:18-25`）
GET `/api/site/config`、`/api/site/legal/:type`、`/api/site/turnstile/:scenario`、`/api/gallery/public`、`/api/users/:id/public`。

### 3.2 Lsky Pro 兼容接口（`internal/api/lsky_v1_routes.go` 全文 38 行）

前缀 `/api/v1`，刻意对齐 **Lsky Pro API v1/v2 客户端协议**（handler 注释：`lsky_v1_handlers.go:22`“Lsky v2 API 兼容处理器”）：

| Method | Path | Handler | 鉴权 |
|---|---|---|---|
| POST | `/api/v1/tokens` | `CreateToken`（邮箱+密码换 token，含限流+验证码） | 无 |
| DELETE | `/api/v1/tokens` | `DeleteTokens` | Bearer |
| GET | `/api/v1/profile` | `GetProfile` | Bearer |
| GET | `/api/v1/strategies` | `GetStrategies` | 无 |
| POST | `/api/v1/upload` | `UploadImage`（multipart，兼容 Lsky 返回结构） | Bearer |
| GET | `/api/v1/images` | `GetImages`（分页列表） | Bearer |
| DELETE | `/api/v1/images/:key` | `DeleteImage`（按 `key` 删除） | Bearer |
| GET | `/api/v1/albums` | `GetAlbums` | Bearer |
| DELETE | `/api/v1/albums/:id` | `DeleteAlbum` | Bearer |

即：**实现了 Lsky 的 token、profile、strategies、upload、images、albums 全套核心接口**，返回体格式为 Lsky 风格 `{status,message,data}`（`lsky_v1_handlers.go:56-65` 等）。这是本项目刻意做 PicGo/Lsky 客户端兼容的证据（PicGo 的 lsky-pro 插件就是打这套 API）。

### 3.3 中间件清单（`internal/middleware/`）

| 文件 | 中间件 | 作用 |
|---|---|---|
| `auth.go:15-71` | `Auth` | 强制认证：先 Bearer Token（`sk_` 开头，DB 存哈希，兼容旧明文），否则 Cookie Session；禁用账号 403 |
| `auth.go:73-144` | `OptionalAuth` | 有则注入用户，无/无效也放行 |
| `auth.go:146-207` | `RequireAdmin`、`RequireSuperAdmin`、`CurrentUser` | 权限校验与取当前用户 |
| `csrf.go:14-53` | `RequireCSRF` | 双提交 Cookie（`skyimage_csrf`）+ Header `X-CSRF-Token`，校验 Origin（放行同源、localhost 跨端口、私网 Origin→localhost API） |
| `cors.go:11-82` | `CORS` | 白名单 Origin 带凭证；OPTIONS 预检对公开资源宽松放行 |

Session Cookie 名 `skyimage_session`（`internal/session/manager.go:13`），滑动续期 24h（默认）。CSRF Cookie 名 `skyimage_csrf`（`csrf.go:11`），登录时下发（`auth_handlers.go:413-437`）。

### 3.4 鉴权方式

**两者并存**：
1. **Cookie + Session**（浏览器）：`skyimage_session`，数据存 `sessions` 表（多实例可用），滑动窗口。
2. **API Token**（程序/CLI）：`Authorization: Bearer sk_...`，DB 只存 SHA-256（`data/api_token_security.go`），支持 `last_used_at` 更新、旧明文自动迁移。
管理接口另叠加 `RequireAdmin`/`RequireSuperAdmin`。

---

## 4. 前端形态与功能

### 4.1 目录结构（`src/`）

- `src/features/`：按业务域切分页面
- `src/components/`：通用组件（含 `ui/`）
- `src/components/ui/`：**shadcn/ui 风格**（Radix UI + CVA + tailwind-merge，`components.json` 存在；共 23 个组件：button/card/dialog/dropdown-menu/sidebar/table/toast…）
- `src/layouts/AppShell.tsx`：后台外壳（侧边栏）
- `src/state/auth.ts`：唯一 Zustand store
- `src/context/search-provider.tsx`：Context，仅命令面板搜索
- `src/lib/`：`api.ts`（约 1800 行，所有请求）、`navigation.ts`（导航定义）、`file-url.ts`、`passkeys.ts`、`turnstile.ts`、`cap.ts`、`geetest.ts`、`theme-palettes.ts`、`utils.ts`
- `src/i18n.tsx`：内置国际化

### 4.2 路由级页面清单（`src/App.tsx`）

**公开/未登录**
- `/installer`（安装向导，已装则 404）
- `/login`、`/register`、`/forgot-password`、`/reset-password`
- `/terms`、`/privacy`
- `/`（HomeEntry，`enableHome=false` 跳登录）
- `/u/:id`（公开用户页）、`/shop`（公开商城）

**登录后 `/dashboard/*`**（`ProtectedRoute` + `AppShell`）
- `dashboard`（DashboardPage）、`upload`（上传）、`images`（我的图片）、`shop`、`orders`、`tickets`、`tickets/new`、`tickets/:id`、`settings`（个人设置）、`notifications`、`gallery`（公开图库）、`api`（API 文档）、`api-tokens`、`api-tokens/new`、`api-tokens/:id`、`about`

**管理后台 `/dashboard/admin/*`**（`AdminRoute`）
- `console`、`groups`(+new/:id)、`redeem-codes`(+new)、`shop/products`(+new/:id)、`shop/orders`、`tickets`(+:id)、`users`(+new/:id)、`images`、`audits`(+new/:id)、`strategies`(+new/:id)
- 设置：`settings/site`、`settings/email`、`settings/system`、`settings/tickets`、`settings/captcha`、`settings/oauth`、`settings/payment`、`settings/database`

导航定义与分组（“我的/公开/系统/系统设置”）见 `src/lib/navigation.ts:59-197`，支持按设置隐藏侧栏项（`HiddenRoute`）。

### 4.3 状态管理

- **服务端状态**：`@tanstack/react-query`（App.tsx、各页面 useQuery/useMutation）。
- **客户端全局状态**：**Zustand**，仅一个 store `useAuthStore`（`src/state/auth.ts`），持久化到 localStorage key `skyimage-auth`，保存 user 快照（含 isAdmin/isSuperAdmin/capacity/configs 解析出的偏好）。**没有其它 Zustand store**（无主题 store，主题用 `next-themes` + ThemeProvider）。
- 其它 Context：`search-provider.tsx`。

### 4.4 API 调用组织

单文件 `src/lib/api.ts`（46KB）：axios 实例 `apiClient`（baseURL `VITE_API_BASE_URL||/api`，`withCredentials`）。
- 请求拦截：写操作自动从 Cookie `skyimage_csrf` 读 token 塞 `X-CSRF-Token`；`/installer/` 请求自动带 `X-Install-Password`（sessionStorage）。
- 响应拦截：统一错误/禁用账号处理。
- 按域导出函数（`fetchProfile`、`uploadFile`、`fetchFiles`、`fetchAdminMetrics`、`fetchStrategies`、`saveStrategy`、`createTicket`…约 130 个），组件直接调用；Lsky 兼容接口不在此文件（由第三方客户端直连 `/api/v1`）。

### 4.5 UI 依赖

Radix UI 全家桶、lucide-react、recharts（图表）、@dnd-kit（拖拽排序）、@fancyapps/ui（灯箱）、sonner + 自研 toast、vaul（drawer）、react-day-picker、react-hook-form + zod、marked + dompurify（Markdown）、cmdk（命令面板）。

---

## 5. installer 与 legacy 导入

### 5.1 安装向导（`internal/installer/`）

- `service.go`：核心。`RunInput` 含数据库配置 + `SiteName/AdminName/AdminEmail/AdminPassword`（`service.go:55-71`）。
- 前端向导步骤（`src/features/installer/InstallerPage.tsx:34`）：`mode`（全新安装 / 导入旧数据）→ `product` → `legacy`（旧库连接，仅导入模式）→ `database`（DB 选择）→ `site`（站点名+管理员）→ `result`。
- `Run` 流程（`service.go:167-330`）：
  1. 校验是否已安装；
  2. `buildTargetConfig` 组出目标 DB 配置（sqlite 默认 `storage/data/skyImage.db`）；
  3. 需要切库则 `data.NewDatabase` 连接；
  4. **单事务内**：`ensureDefaultGroup`（建默认组，带默认 configs）→ `ensureDefaultStrategy`（建“本地存储”策略，configs={driver:local, root:StoragePath, url:PublicBaseURL}）→ 建超管用户（bcrypt，`users.CreateUserWithGeneratedID`）→ 批量 upsert `configs` 默认设置（200-278 行）→ 写 `installer_states`；
  5. 事务外 `config.SaveDatabaseEnv` 写 `.env`；
  6. `switchDB`/`SetRuntime` 热切换运行时连接。
- 安装密码：`InstallPassword`（env 或启动随机 10 位，`service.go:119-157`）、`VerifyInstallPassword`（恒定时间比较）；入口在 `installer_handlers.go`。
- `defaults.go`：默认条款/隐私文本等。
- `legacy.go`：旧库探测与导入编排（见下）。

### 5.2 legacy 导入（`internal/legacy/`）

- `source.go`：只读连接 Lsky Pro 库，支持 **MySQL / PostgreSQL / SQL Server / SQLite** 四种源（`source.go:29-38`）；强制只读（MySQL 会话 `transaction_read_only`、SQLite `mode=ro`、PG/SQL Server 只读事务）。`lskyTables = {groups, users, albums, strategies, images, configs}`（`source.go:434`）。
- `importer.go`（1129 行）：事务化分批导入，流程 `prepareStrategies → precopyImages →（事务）strategies → groups → group_strategy → users → albums → images → configs`（`importer.go:119-134`）。
- **表/字段映射**：
  - **strategies 表**：逐字段复制 id/key/name/intro/configs/created_at/updated_at；本地驱动 configs 重写为新站 root/base_url 并保留 `legacy_root/legacy_url`（`importer.go:212-235`）；云驱动原样保留。
  - **groups 表**：id/name/is_default/is_guest/configs；name 冲突自动加后缀去重（`importer.go:254-279`）；configs 映射 `maximum_file_size→max_file_size`、`limit_per_minute/hour→upload_rate_*`、`user_initial_capacity(KB)→max_capacity`（`importer.go:306-328`）。
  - **group_strategy**：直接复制组-策略关联（`importer.go:348-388`）。
  - **users 表**：id/group_id/name/email/password/remember_token/url/capacity/configs/is_adminer/image_num/album_num/registered_ip/status/email_verified_at/created_at/updated_at；**bcrypt 密码原样保留**；capacity(KB→字节) 写 capacity 且 `CapacityBonus = capacity - user_initial_capacity`；`default_permission(0/1)→default_visibility`、`default_strategy` 保留（`importer.go:390-500`）；email 已存在则合并（不重复建号，images 重指）。
  - **albums 表**：id/user_id/name/intro/image_num/created_at/updated_at（`importer.go:502-538`）。
  - **images 表 → files 表**：id/user_id/group_id/strategy_id/key/path/name/origin_name/alias_name/size/mimetype/extension/md5/sha1/width/height/permission/is_unhealthy/uploaded_ip/created_at/updated_at；`permission==1→public`，`is_unhealthy→audit_status=rejected`；`path/name` 拼成 relative_path；可选按 `imageDir` 把原图拷进新 StoragePath，路径经 `sanitizeImportRelPath` 安全校验（拒绝 `..`、绝对路径、保留段 api/assets/robots.txt）；孤儿图片（user_id NULL）归给超管（`importer.go:541-744`、`installer/legacy.go:138-158`）。
  - **configs 设置映射**（`importer.go:867-925`）：`app_name→site.name/title`、`site_description→site.description`、`is_enable_gallery→features.gallery`、`is_enable_api→features.api`、`is_enable_registration→features.allow_registration`、`is_user_need_verify→mail.register.verify`、`mail` JSON→`mail.smtp.*`。
- 只读探测 `Probe`（`source.go`）返回各表行数、app_name/app_version、是否只读就绪。
- 导入需先用安装时管理员邮箱+密码授权（`installer/legacy.go:110-135`）。

---

## 6. 其他模块职责

| 包 | 职责（1-3 句） | 存储/图片分发相关 |
|---|---|---|
| `internal/files/` | **核心存储与分发**。`service.go`(2192行) 上传/删除/公开 URL/缩略图；`storage_s3.go`/`storage_webdav.go`/`storage_ftp.go`/`storage_sftp.go` 四类远程驱动；`image_processor.go` 压缩/转码/缩略图/读尺寸；`limiter.go` 每用户分钟/小时上传限流；`audit.go` 图片审核（UAPI NSFW、腾讯云 CI）；`embed.go` 生成 HTML/Markdown 嵌入码；`notifications.go` 审核/管理删除通知 | ✅ 是 |
| `internal/captcha/` | 统一验证码服务，支持 Cloudflare Turnstile、Geetest、Cap 三种 provider，管理后台可切换 | ❌ |
| `internal/oauth/` | GitHub/Google/Discord/自定义 OAuth 登录与绑定，`oauth_states` 存 PKCE/CSRF | ❌ |
| `internal/passkey/` | WebAuthn Passkey 注册/登录/管理 | ❌ |
| `internal/mail/` | SMTP 发信（注册验证、找回密码、工单通知、测试），模板在 `templates.go` | ❌ |
| `internal/notifications/` | 站内通知（图片被删/管理员删图等），`user_notifications` 表 | 间接（删除事件） |
| `internal/payment/` | 支付渠道实现：epay / alipay / wechat / stripe，统一 `provider.go` | ❌ |
| `internal/redeem/` | 兑换码（角色组 / 容量增减）创建、兑换、使用记录 | ❌ |
| `internal/shop/` | 会员商品、订单、支付回调、会员到期回收组 | ❌ |
| `internal/tickets/` | 工单 CRUD、附件上传/权限（附件走 files 存储）、状态流转 | 间接（附件存储） |
| `internal/verification/` | 内存态邮箱验证码 + 密码重置 token | ❌ |
| `internal/session/` | DB 会话管理（`skyimage_session`，滑动续期） | ❌ |
| `internal/version/` | 版本号/About 常量 | ❌ |
| `internal/turnstile/` | 旧版独立 Turnstile 服务，已被 captcha 统一（`database.go:243` 有 turnstile→captcha 配置迁移） | ❌ |
| `internal/users/` | 用户服务：注册/登录/查找/16 位 ID 生成、偏好 | 间接（配额） |
| `internal/admin/` | 管理服务：仪表盘、设置 KV、用户组、策略、审核配置、图片管理 | 部分（策略/图片） |

---

## 7. 砍掉「后端存储 + 图片分发」后，应删除/改造的部分

假设：**文件存储与图片分发全部交给 PicGo**（后端不再落盘、不再代理/分发图片，只承担用户/元数据/管理）。

### 7.1 应当删除（整体下线）

**Go 包 / 文件**
- `internal/files/storage_s3.go`、`storage_webdav.go`、`storage_ftp.go`、`storage_sftp.go`、`storage_*` 全删
- `internal/files/service.go` 中的存储侧方法：`Upload`、`storeObject*/storeS3Object/storeWebDAVObject/storeFTPObject/storeSFTPObject`、`StoreBytes`、`ProcessBytesWithStrategy`、`DeleteStoredObject*`、`OpenStoredObject`、`FetchProxyObject`、`attachThumbnail`、`buildRelativePath`、`resolveStrategy*`、`parseStrategyConfig` 及 `strategyConfig` 结构
- `internal/files/image_processor.go`（压缩/转码/缩略图）
- `internal/files/limiter.go`（上传限流仅对上传有意义，可删或并入 PicGo 侧）
- `internal/files/audit.go`（图片审核依赖上传字节流；若 PicGo 不回传字节则删）
- `internal/api/file_handlers.go`（`/api/files` 上传/删除/可见性）
- `internal/api/lsky_v1_handlers.go` 中的 `UploadImage`、`DeleteImage`（若 PicGo 自己实现上传则删）
- `internal/api/server.go` 中：`serveLocalFileByRelative`、`serveWebDAVFile`、`tryServeLocalFile`、`tryServeTicketAttachment`、`registerStaticAssets`、`pathPrefix/extractConfigHosts/rejectIf*DomainMismatch`、`getMimeTypeByExtension`、`buildWebDAVObjectURL` 等分发逻辑
- `internal/dbmigrate` 中图片相关表复制（如果 files 表保留元数据则保留）
- 安装向导默认策略创建 `ensureDefaultStrategy`（`installer/service.go:441-466`）

**数据表**
- `strategies`（存储策略）、`group_strategy`（组-策略关联）—— 若完全不管理存储
- `audit_profiles`（若审核下线）
- `files` 表：若只保留元数据可**改造**而非删除（见 7.2）

**DB 字段（`files` 表）**：`strategy_id`、`path`、`relative_path`、`public_url`、`storage_provider`、`thumbnail_path/relative_path/public_url/storage_provider/strategy_id`——改为只存 `public_url`（PicGo 返回）与必要元数据。

**配置项**：`storage.root`、所有 `*_strategy_id`、组 configs 的 `default_visibility`（可留）、策略验证逻辑 `validateStrategyConfigs`（`admin/service.go:449-527`）。

**前端页面**
- `src/features/files/UploadPage.tsx`、`MyImagesPage.tsx`、`components/ImageGrid.tsx`、`FileTable.tsx`
- `src/features/gallery/GalleryPage.tsx`（若画廊展示也交 PicGo）
- `src/features/admin/AdminStrategiesPage.tsx`、`AdminStrategyEditorPage.tsx`、`AdminImagesPage.tsx`
- `src/features/admin/AdminAuditsPage.tsx`、`AdminAuditEditorPage.tsx`（若审核下线）
- `src/lib/file-url.ts`、`src/lib/api.ts` 中 `uploadFile/fetchFiles/deleteFile/updateFileVisibility/.../fetchStrategies/saveStrategy/...` 全部存储相关导出
- 导航项：`/dashboard/upload`、`/dashboard/images`、`/dashboard/gallery`、`/dashboard/admin/images`、`/dashboard/admin/strategies`、`/dashboard/admin/audits`（`src/lib/navigation.ts:61-84`）

**接口**
- `/api/files*` 全部
- `/api/admin/strategies*`、`/api/admin/images*`、`/api/admin/audits*`
- `/api/v1/upload`、`/api/v1/images*`、`/api/v1/strategies`
- `/api/site/turnstile/:scenario` 无关，保留
- 静态文件分发路由 `/<publicSegment>/*filepath`、`tryServeLocalFile`

**中间件**：本身无需删；但 `serveLocalFileByRelative` 内嵌的“策略域名校验”“缩略图鉴权”“演示站私有图片鉴权”逻辑随分发一起删。CORS 中间件里针对图片的宽松预检可简化。

### 7.2 应当改造

- **`files` 表 → 轻量图片元数据表**：保留 `id/user_id/album_id(?)/key/name/original_name/size/mime_type/extension/width/height/checksum_*/visibility/created_at`，新增/改用**外部存储返回的 URL 字段**（PicGo 上传后写回 `public_url`）；删掉策略/缩略图/本地路径列。
- **上传接口**：从“后端接收 multipart 并落盘”改为“后端签发/校验 → 前端或客户端直接调用 PicGo 上传 → 回调/回写元数据”。`POST /api/v1/upload` 若要继续兼容 Lsky/PicGo 客户端，需改为转发到 PicGo 或保留为纯元数据登记。
- **相册 `albums`**：可保留，`image_num` 统计改由 PicGo 或后端计数。
- **工单附件 `ticket_attachments`**：改为引用 PicGo 外部 URL，删除 `strategy_id/path/relative_path/storage_provider`，附件鉴权从“本地文件权限”改为“URL 签名/私有桶”。
- **用户组 `groups.configs`**：`max_capacity/capacity_bonus/use_capacity` 的容量管理若仍要，需由 PicGo 侧回传用量；`max_file_size` 可下发给 PicGo 作校验。
- **安装向导**：去掉“默认本地策略”步骤；DB 配置保留。
- **`admin.Service.ListStrategies`/`FindStrategyByID`** 及其调用点（server 静态挂载、files 分发）全部下线。
- **`internal/files/embed.go`**（HTML/Markdown 嵌入码）可保留，因为 URL 来自 PicGo。

### 7.3 保留（与存储无关）

用户/认证/会话/API Token、用户组、兑换码、商城/支付、工单（除附件存储）、通知、OAuth、Passkey、验证码、邮件、站点设置、审计日志类功能。

---

## 8. 工程实践参考

### 8.1 go.mod 关键依赖（`go.mod:1-39`）

| 依赖 | 版本 | 用途 |
|---|---|---|
| `github.com/gin-gonic/gin` | v1.10.0 | HTTP 框架 |
| `gorm.io/gorm` | v1.31.1 | ORM |
| `gorm.io/driver/{sqlite,mysql,postgres}` | v1.6.0 各 | 三种目标库 |
| `github.com/glebarez/sqlite` / `go-sqlite` | v1.11.0 / v1.21.2 | 纯 Go SQLite（无 CGO） |
| `github.com/go-sql-driver/mysql` | v1.9.3 | MySQL |
| `github.com/jackc/pgx/v5` | v5.9.2 | PG 驱动（legacy 导入也用它） |
| `github.com/microsoft/go-mssqldb` | v1.8.2 | SQL Server（仅 legacy 导入） |
| `github.com/spf13/viper` | v1.18.2 | 配置读取 |
| `golang.org/x/crypto` | v0.54.0 | bcrypt |
| `golang.org/x/image` | v0.44.0 | 图片处理 |
| `github.com/HugoSmits86/nativewebp` | v1.3.0 | WebP 编解码 |
| `github.com/aws/aws-sdk-go-v2`(+config/credentials/service/s3) | v1.41.5 等 | S3/MinIO 存储 |
| `github.com/pkg/sftp` | v1.13.10 | SFTP 存储 |
| `github.com/jlaffaye/ftp` | v0.2.0 | FTP 存储 |
| `github.com/go-webauthn/webauthn` | v0.17.4 | Passkey |
| `github.com/google/uuid` | v1.6.0 | UUID |
| `gorm.io/datatypes` | v1.2.0 | JSON 列 |

Go 版本 `go 1.25.0`。

### 8.2 部署形态

**Dockerfile**（3 阶段）：`node:18-alpine` 装 pnpm+构建前端 → `golang:1.25-alpine`（CGO，cgo sqlite）编译 `cmd/api` → `alpine:latest` 运行。暴露 8080。ENV 只设 `HTTP_ADDR/STORAGE_PATH/FRONTEND_DIST/ALLOW_REGISTRATION/CORS_ALLOWED_ORIGINS/GIN_MODE/TZ`，**故意不烘焙数据库配置**（注释说明 env 优先级会覆盖 .env）。`CMD docker-entrypoint.sh`。

**docker-compose.yml**：单服务 `fishcpy/skyimage:latest`，端口 8080，挂载 `./storage/data`、`./storage/uploads`、`./.env:/app/.env`（数据库配置持久化），healthcheck 打 `/api/health`。
另有 `docker-compose.demo.yml` / `Dockerfile.demo` / `docker-entrypoint-demo.sh` / `.env.demo.example` 用于演示站。

### 8.3 `.env.example` 全部变量

```
HTTP_ADDR=:8080
STORAGE_PATH=storage/uploads
PUBLIC_BASE_URL=http://localhost:8080
ALLOW_REGISTRATION=true
LEGACY_DSN=
FRONTEND_DIST=dist
INSTALL_PASSWORD=                      # 留空则每次启动随机生成
# GIN_MODE=release                     # 注释：release(default)|debug|test
CORS_ALLOWED_ORIGINS=http://localhost:5173,http://127.0.0.1:5173
TRUSTED_PROXIES=
DATABASE_TYPE=                         # sqlite|mysql|postgres（安装向导写入）
DATABASE_PATH=
DATABASE_HOST=
DATABASE_PORT=
DATABASE_NAME=
DATABASE_USER=
DATABASE_PASSWORD=
```

---

## Start Here

1. `internal/data/models.go` —— 全部 23 张表的字段真源，改造数据模型必读。
2. `internal/api/lsky_v1_routes.go` + `internal/api/lsky_v1_handlers.go` —— PicGo/Lsky 兼容契约，理解“为什么 PicGo 能直接用”的关键。
3. `internal/files/service.go` —— 后端存储与图片分发的全部实现，是“要砍掉”的核心面。
4. `internal/api/server.go:344-500` —— 路由注册 + 本地文件分发/静态挂载逻辑。
5. `src/lib/api.ts` + `src/App.tsx` + `src/lib/navigation.ts` —— 前端接口调用与页面/导航全貌。

## 关键风险/开放问题

- **策略/审核密钥明文入库**（`strategies.configs`、`audit_profiles.configs`、`configs` 表中的 SMTP/验证码/OAuth secret），API 仅输出脱敏。砍存储时要注意迁移这些明文密钥。
- **Lsky 兼容接口的返回格式**由 `lsky_v1_handlers.go` 手工拼装（`{status,message,data}`），与内部 REST 格式不同；替换上传后端时须保持契约。
- **图片可见性语义**：`visibility` 只影响画廊展示，直链仍可访问（`server.go` 注释明确），与“私有图片”预期可能不一致。
- **`relative_path`/`public_url` 是后补列**（`database.go:161-224`），历史库可能缺失，改造时需兼容迁移。
- **演示站模式**（DemoMode）会在分发层强制登录/属主校验，砍掉分发后这套逻辑无宿主。
