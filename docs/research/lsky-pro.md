# lsky-pro（兰空图床）源码侦察报告

> 仓库根目录：`/tmp/pi-github-repos/runtime-Na2Ckq/f47b64964af4505955c2b9a5bcd79aad56c9de8f3e6f0d66e1b0c8c4bf149881`
> 版本：`V 2.1`（`config/convention.php`，`ConfigKey::AppVersion`）
> 技术栈：Laravel 9 + PHP 8.0.2+ + Blade + Alpine.js + jQuery + Tailwind（`composer.json` / `package.json`）
> 所有结论均以下列源码为唯一依据。

---

## 1. 数据模型

数据库迁移全部位于 `database/migrations/`。以下逐表给出完整字段清单（依据 migration 文件；补充字段来自模型 `$casts` / `$fillable` / accessor）。

### 1.1 `groups` — `database/migrations/2014_10_10_000000_create_groups_table.php`
引擎 InnoDB，字符集 utf8mb4_unicode_ci。

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| `id` | BIGINT UNSIGNED AUTO_INCREMENT | 否 | — | 主键 |
| `name` | VARCHAR(64) | 否 | 无 | 角色组名称 |
| `is_default` | BOOLEAN(TINYINT) | 否 | `false` | 是否默认组 |
| `is_guest` | BOOLEAN | 否 | `false` | 是否为游客组 |
| `configs` | JSON | 否 | 无 | 组配置（键值对，见 §2 的 `GroupConfigKey`） |
| `created_at` | TIMESTAMP | 是 | NULL | |
| `updated_at` | TIMESTAMP | 是 | NULL | |

- 无额外索引/唯一约束（仅主键）。
- 模型 `app/Models/Group.php`：`configs` cast 为 `collection`；`is_default`/`is_guest` cast `bool`。
- `booted()`（Group.php 第 55-66 行）保证全局只有一个 `is_default=true`、一个 `is_guest=true`，并在保存时用 `Utils::parseConfigs()` 把默认配置与现有配置合并。
- 关系：`users()` hasMany User；`images()` hasMany Image；`strategies()` belongsToMany Strategy（中间表 `group_strategy`）。
- 常量：`POSITIONS`（水印位置 10 种）、`SCENES`（腾讯/阿里审核场景 6 种）。
- 权限相关字段全部塞在 `configs` JSON 中（见 §2.2），表本身没有独立权限列。

### 1.2 `users` — `database/migrations/2014_10_12_000000_create_users_table.php`
引擎 InnoDB，utf8mb4_unicode_ci。

| 列 | 类型 | 可空 | 默认 | 约束/说明 |
|---|---|---|---|---|
| `id` | BIGINT UNSIGNED AUTO_INCREMENT | 否 | — | 主键 |
| `group_id` | BIGINT UNSIGNED | 是 | NULL | 外键 → `groups.id`，`onDelete('set null')` |
| `name` | VARCHAR(255) | 否 | 无 | 姓名/昵称 |
| `email` | VARCHAR(255) | 否 | 无 | **UNIQUE**，登录名 |
| `password` | VARCHAR(255) | 否 | 无 | bcrypt |
| `remember_token` | VARCHAR(100) | 是 | NULL | |
| `is_adminer` | BOOLEAN | 否 | `false` | 是否管理员 |
| `capacity` | DECIMAL(20) | 否 | `0` | 总容量，**单位 KB** |
| `url` | VARCHAR(255) | 否 | `''` | 个人主页 |
| `configs` | JSON | 否 | 无 | 用户级配置（见 §2 `UserConfigKey`） |
| `image_num` | BIGINT UNSIGNED | 否 | `0` | 图片数量（冗余计数） |
| `album_num` | BIGINT UNSIGNED | 否 | `0` | 相册数量（冗余计数） |
| `registered_ip` | VARCHAR(255) | 否 | `''` | 注册 IP |
| `status` | TINYINT UNSIGNED | 否 | `1` | 状态（见 `UserStatus`，1 正常 / 0 冻结） |
| `email_verified_at` | TIMESTAMP | 是 | NULL | |
| `created_at` / `updated_at` | TIMESTAMP | 是 | NULL | |

- 模型 `app/Models/User.php`：`$fillable` 含 `capacity`、`configs`、`configs->default_strategy`、`status` 等；`$hidden` 隐藏 `password`、`remember_token`、`configs`、`group_id`、`is_adminer`；`$appends=['avatar']`。
- 创建钩子（User.php 第 89-97 行）：`group_id` 取 `is_default=true` 的组；`capacity` 取站点配置 `UserInitialCapacity`；`configs` 合并 `config('convention.user')`。
- accessor：`avatar`（cravatar）、`useCapacity`（`images()->sum('size')`）。
- 关系：`group()` belongsTo；`albums()` hasMany；`images()` hasMany。
- 使用 Laravel Sanctum `HasApiTokens`。

### 1.3 `personal_access_tokens` — `database/migrations/2019_12_14_000001_create_personal_access_tokens_table.php`

| 列 | 类型 | 可空 | 默认 | 约束/说明 |
|---|---|---|---|---|
| `id` | BIGINT UNSIGNED AI | 否 | — | 主键 |
| `tokenable_type` | VARCHAR(255) | 否 | 无 | morphs 生成，索引 |
| `tokenable_id` | BIGINT UNSIGNED | 否 | 无 | morphs 生成，索引 |
| `name` | VARCHAR(255) | 否 | 无 | |
| `token` | VARCHAR(64) | 否 | 无 | **UNIQUE**（存 sha256 hash） |
| `abilities` | TEXT | 是 | NULL | |
| `last_used_at` | TIMESTAMP | 是 | NULL | |
| `created_at` / `updated_at` | TIMESTAMP | 是 | NULL | |

### 1.4 `password_resets` — `database/migrations/2014_10_12_100000_create_password_resets_table.php`

| 列 | 类型 | 可空 | 默认 | 约束/说明 |
|---|---|---|---|---|
| `email` | VARCHAR(255) | 否 | 无 | 普通索引 |
| `token` | VARCHAR(255) | 否 | 无 | |
| `created_at` | TIMESTAMP | 是 | NULL | |

- 无主键。

### 1.5 `failed_jobs` — `database/migrations/2019_08_19_000000_create_failed_jobs_table.php`（框架自带）

| 列 | 类型 | 约束 |
|---|---|---|
| `id` | BIGINT UNSIGNED AI | 主键 |
| `uuid` | VARCHAR(255) | UNIQUE |
| `connection` | TEXT | |
| `queue` | TEXT | |
| `payload` | LONGTEXT | |
| `exception` | LONGTEXT | |
| `failed_at` | TIMESTAMP | `useCurrent()` |

### 1.6 `strategies` — `database/migrations/2021_12_11_184521_create_strategies_table.php`
InnoDB，utf8mb4_unicode_ci。

| 列 | 类型 | 可空 | 默认 | 说明 |
|---|---|---|---|---|
| `id` | BIGINT UNSIGNED AI | 否 | — | 主键 |
| `key` | TINYINT UNSIGNED | 否 | 无 | 策略类型，取值 `StrategyKey::*`（1..10） |
| `name` | VARCHAR(64) | 否 | 无 | 策略名称 |
| `intro` | VARCHAR(255) | 否 | `''` | 简介 |
| `configs` | JSON | 否 | 无 | 策略配置（`url` / `queries` / 驱动凭据等，见 §2.3） |
| `created_at` / `updated_at` | TIMESTAMP | 是 | NULL | |

- 模型 `app/Models/Strategy.php`：`configs` cast collection；`DRIVERS` 常量列出 10 种驱动中文名；`WEBDAV_AUTH_TYPES` 常量。
- `saving` 钩子（Strategy.php 第 45-70 行）：对 `root`、`url` 归一化；若 `key=Local` **创建/更新 public 目录符号链接**（`public/{rootName}` → `storage/app/uploads`），删除时移除符号链接。这是图片分发机制之一。
- 关系：`groups()` belongsToMany（中间表 group_strategy）；`images()` hasMany。

### 1.7 `albums` — `database/migrations/2021_12_11_185759_create_albums_table.php`
InnoDB，utf8mb4_unicode_ci。

| 列 | 类型 | 可空 | 默认 | 约束/说明 |
|---|---|---|---|---|
| `id` | BIGINT UNSIGNED AI | 否 | — | 主键 |
| `user_id` | BIGINT UNSIGNED | 否 | 无 | 外键 → `users.id`，`onDelete('cascade')` |
| `name` | VARCHAR(64) | 否 | 无 | 名称 |
| `intro` | VARCHAR(255) | 否 | `''` | 简介 |
| `image_num` | BIGINT UNSIGNED | 否 | `0` | 图片数量（冗余计数） |
| `created_at` / `updated_at` | TIMESTAMP | 是 | NULL | |

- 模型 `app/Models/Album.php`：`$hidden=['user_id']`；关系 `user()` belongsTo、`images()` hasMany；`scopeFilter` 支持 order/keyword。

### 1.8 `images` — `database/migrations/2021_12_11_191158_create_images_table.php`
InnoDB，utf8mb4_unicode_ci。**核心表。**

| 列 | 类型 | 可空 | 默认 | 约束/说明 |
|---|---|---|---|---|
| `id` | BIGINT UNSIGNED AI | 否 | — | 主键 |
| `user_id` | BIGINT UNSIGNED | 是 | NULL | 外键 → `users.id`，`set null`（游客上传为 NULL） |
| `album_id` | BIGINT UNSIGNED | 是 | NULL | 外键 → `albums.id`，`set null` |
| `group_id` | BIGINT UNSIGNED | 是 | NULL | 外键 → `groups.id`，`set null`（图片归属组） |
| `strategy_id` | BIGINT UNSIGNED | 是 | NULL | 外键 → `strategies.id`，`set null`（图片所属存储策略） |
| `key` | VARCHAR(255) | 否 | 无 | **UNIQUE**，随机 6+ 位短 key，用于访问/防盗链/缓存键 |
| `path` | VARCHAR(255) | 否 | 无 | 保存路径（目录部分） |
| `name` | VARCHAR(255) | 否 | 无 | 保存文件名（含扩展名） |
| `origin_name` | VARCHAR(255) | 否 | `''` | 原始上传文件名 |
| `alias_name` | VARCHAR(255) | 否 | `''` | 用户设置的别名（重命名） |
| `size` | DECIMAL(8,2 默认) | 否 | `0` | 图片大小，**单位 KB** |
| `mimetype` | VARCHAR(32) | 否 | 无 | MIME 类型 |
| `extension` | VARCHAR(32) | 否 | 无 | 扩展名（小写） |
| `md5` | VARCHAR(32) | 否 | 无 | 文件 MD5（去重依据） |
| `sha1` | VARCHAR(255) | 否 | 无 | 文件 SHA1（去重依据） |
| `width` | INT UNSIGNED | 否 | `0` | 宽 |
| `height` | INT UNSIGNED | 否 | `0` | 高 |
| `permission` | TINYINT | 否 | `0` | 访问权限（`ImagePermission`：1 公开 / 0 私有） |
| `is_unhealthy` | BOOLEAN | 否 | `false` | 是否被审核标记为违规 |
| `uploaded_ip` | VARCHAR(255) | 否 | `''` | 上传 IP（游客限频用） |
| `created_at` / `updated_at` | TIMESTAMP | 是 | NULL | |

- 模型 `app/Models/Image.php` 关键点：
  - `$hidden` 隐藏 `user_id/album_id/group_id/strategy_id`。
  - accessor `filename`（alias_name ?: origin_name）、`pathname`（`path/name` → 存储 key）、`url`、`thumbUrl`、`links`（输出 url/html/bbcode/markdown/markdown_with_link/thumbnail_url 六种格式）。
  - **URL 生成**（Image.php 第 169-181 行）：若组开启 `IsEnableOriginalProtection`，URL 为 `asset("{key}.{extension}")`（走本程序的输出路由）；否则为 `strategy.configs.url + pathname + queries`（直链到存储/CDN）。
  - `getThumbnailPathname()`：`{thumbnail_path}/{md5}.png|svg`，即 `public/thumbnails/`。
  - `deleting` 钩子：同策略下无相同 md5+sha1 时删除物理文件与缩略图、清缓存 `image_{key}`。
  - `creating` 钩子：生成唯一随机 `key`。
  - `filesystem()`：用 `ImageService::getAdapter($this->strategy)` 得到 Flysystem 适配器。
  - 关系：`user()`、`album()`、`group()`、`strategy()` 四个 belongsTo。

### 1.9 `configs` — `database/migrations/2021_12_11_200033_create_configs_table.php`

| 列 | 类型 | 可空 | 默认 | 约束/说明 |
|---|---|---|---|---|
| `name` | VARCHAR(32) | 否 | 无 | 配置名，**UNIQUE**（兼作业务主键） |
| `value` | LONGTEXT | 是 | NULL | 配置值（标量或 JSON 字符串） |
| `created_at` / `updated_at` | TIMESTAMP | 是 | NULL | |

- **无自增主键**。模型 `app/Models/Config.php`：`$incrementing=false`、`$keyType='string'`。
- 纯 key/value 形式；**没有**分组字段、没有类型字段。数组/对象值以 JSON 字符串存入 `value`（见 `Utils::config()` 的解析）。
- 通过 `Utils::config()`（`app/Utils.php`）读取并用 `Cache::rememberForever('configs')` 缓存，读取时对布尔类键做类型转换、对 `Mail`/`Group` 做 `json_decode`。
- 由 `DatabaseSeeder`（`database/seeders/DatabaseSeeder.php`）与 `InstallSeeder`（`database/seeders/InstallSeeder.php`）从 `config('convention.app')` 初始化。

### 1.10 `group_strategy`（多对多中间表）— `database/migrations/2022_01_20_201231_create_group_strategy_table.php`

| 列 | 类型 | 约束 |
|---|---|---|
| `group_id` | BIGINT UNSIGNED | 外键 → `groups.id`，`onDelete('cascade')` |
| `strategy_id` | BIGINT UNSIGNED | 外键 → `strategies.id`，`onDelete('cascade')` |

- **无主键、无唯一约束、无时间戳**（仅两个外键列）。
- 语义：某用户组可用的存储策略集合。上传时从 `$group->strategies()` 中选策略（见 §5）。

### 1.11 其他
- `database/factories/`、`database/seeders/` 见上。
- 无“标签（tag）”表；无“评论/分享/举报/目录”表。图片分类只有相册（Album）。

---

## 2. 枚举与配置键

枚举位于 `app/Enums/`（普通类常量，非 PHP 原生 enum）。

### 2.1 `ConfigKey.php` — 站点级配置键（存 `configs` 表）
文件：`app/Enums/ConfigKey.php`。默认值见 `config/convention.php` 的 `app` 段。

| 常量 | 值 | 含义 | 默认 |
|---|---|---|---|
| `IsEnableRegistration` | `is_enable_registration` | 是否启用注册 | 1 |
| `IsEnableGallery` | `is_enable_gallery` | 是否启用公共画廊 | 1 |
| `IsEnableApi` | `is_enable_api` | 是否启用 API | 1 |
| `AppName` | `app_name` | 程序名称 | Lsky Pro |
| `AppVersion` | `app_version` | 程序版本 | V 2.1 |
| `SiteKeywords` | `site_keywords` | 站点关键字 | — |
| `SiteDescription` | `site_description` | 站点描述 | — |
| `SiteNotice` | `site_notice` | 站点公告 | '' |
| `IcpNo` | `icp_no` | ICP 备案号 | '' |
| `IsAllowGuestUpload` | `is_allow_guest_upload` | 是否允许游客上传 | 1 |
| `UserInitialCapacity` | `user_initial_capacity` | 用户初始容量（KB） | 512000 |
| `IsUserNeedVerify` | `is_user_need_verify` | 注册账号是否需要邮箱验证 | 0 |
| `Mail` | `mail` | 邮件配置（JSON 对象） | smtp 默认 |
| `Group` | `group` | 角色组默认配置（JSON 对象） | 见 convention.group |

### 2.2 `GroupConfigKey.php` — 用户组级配置键（存 `groups.configs` JSON）
文件：`app/Enums/GroupConfigKey.php`。默认值见 `config/convention.php` 的 `group` 段。

| 常量 | 值 | 含义 | 默认 |
|---|---|---|---|
| `MaximumFileSize` | `maximum_file_size` | 单文件最大体积（KB） | 5120 |
| `ConcurrentUploadNum` | `concurrent_upload_num` | 前端并发上传数量 | 3 |
| `IsEnableScan` | `is_enable_scan` | 上传是否启用违规审查 | 0 |
| `ScanConfigs` | `scan_configs` | 审查配置（驱动 tencent/aliyun/nsfwjs） | — |
| `ScannedAction` | `scanned_action` | 违规后动作 `mark` / `delete` | mark |
| `IsEnableOriginalProtection` | `is_enable_original_protection` | 是否启用原图保护（走本程序代理输出） | 0 |
| `IsEnableWatermark` | `is_enable_watermark` | 是否启用水印 | 0 |
| `WatermarkConfigs` | `watermark_configs` | 水印配置 | — |
| `LimitPerMinute` | `limit_per_minute` | 每分钟上传数限制 | 20 |
| `LimitPerHour` | `limit_per_hour` | 每小时 | 100 |
| `LimitPerDay` | `limit_per_day` | 每天 | 300 |
| `LimitPerWeek` | `limit_per_week` | 每周 | 600 |
| `LimitPerMonth` | `limit_per_month` | 每月 | 999 |
| `AcceptedFileSuffixes` | `accepted_file_suffixes` | 允许后缀 | jpeg,jpg,png,gif,tif,bmp,ico,psd,webp,svg |
| `PathNamingRule` | `path_naming_rule` | 路径命名规则 | `{Y}/{m}/{d}` |
| `FileNamingRule` | `file_naming_rule` | 文件命名规则 | `{uniqid}` |
| `ImageCacheTtl` | `image_cache_ttl` | 原图代理输出缓存时间（秒） | 2626560 |
| `ImageSaveFormat` | `image_save_format` | 保存格式（空=原格式） | '' |
| `ImageSaveQuality` | `image_save_quality` | 保存质量（1-100） | 75 |

### 2.3 `StrategyKey.php` — 存储策略类型
文件：`app/Enums/StrategyKey.php`；中文名见 `app/Models/Strategy.php::DRIVERS`。

| 常量 | 值 | 驱动 |
|---|---|---|
| `Local` | 1 | 本地 |
| `S3` | 2 | AWS S3 |
| `Oss` | 3 | 阿里云 OSS |
| `Cos` | 4 | 腾讯云 COS |
| `Kodo` | 5 | 七牛云 Kodo |
| `Uss` | 6 | 又拍云 USS |
| `Sftp` | 7 | SFTP |
| `Ftp` | 8 | FTP |
| `Webdav` | 9 | WebDav |
| `Minio` | 10 | Minio |

> ⚠️ **【待砍除区】** 上面 10 种存储驱动即“后端存储”能力本体，全部应予移除或交给 PicGo（详见 §7）。每个驱动的配置键枚举文件：
> - `app/Enums/Strategy/LocalOption.php`：`url`, `root`
> - `app/Enums/Strategy/S3Option.php`：`url`, `access_key_id`, `secret_access_key`, `endpoint`, `region`, `bucket`
> - `app/Enums/Strategy/OssOption.php`：`url`, `access_key_id`, `access_key_secret`, `endpoint`, `bucket`
> - `app/Enums/Strategy/CosOption.php`：`url`, `app_id`, `secret_id`, `secret_key`, `region`, `bucket`
> - `app/Enums/Strategy/KodoOption.php`：`url`, `access_key`, `secret_key`, `bucket`
> - `app/Enums/Strategy/UssOption.php`：`url`, `operator`, `password`, `service`
> - `app/Enums/Strategy/SftpOption.php`：`url`, `root`, `host`, `username`, `password`, `private_key`, `passphrase`, `port`, `use_agent`
> - `app/Enums/Strategy/FtpOption.php`：`url`, `root`, `host`, `username`, `password`, `port`, `ssl`, `passive`
> - `app/Enums/Strategy/WebDavOption.php`：`url`, `base_uri`, `username`, `password`, `auth_type`, `prefix`
> - `app/Enums/Strategy/MinioOption.php`：`url`, `access_key`, `secret_key`, `endpoint`, `region`, `bucket`, `bucket_endpoint`
> 另有全局 `configs.url`（访问地址）与 `configs.queries`（URL 额外参数，见 `StrategyRequest`）。

### 2.4 `ImagePermission.php` — `app/Enums/ImagePermission.php`
`Public = 1`（公开）、`Private = 0`（私有）。

### 2.5 `UserStatus.php` — `app/Enums/UserStatus.php`
`Normal = 1`、`Frozen = 0`。

### 2.6 `PastedAction.php` — `app/Enums/PastedAction.php`
`Upload = 2`（粘贴后直接上传）、`Waiting = 1`（粘贴后等待）。

### 2.7 `UserConfigKey.php` — 用户级配置键（存 `users.configs`）
文件：`app/Enums/UserConfigKey.php`。

| 常量 | 值 | 含义 | 默认 |
|---|---|---|---|
| `DefaultAlbum` | `default_album` | 默认相册 id | 0 |
| `DefaultStrategy` | `default_strategy` | 默认存储策略 id | 0 |
| `DefaultPermission` | `default_permission` | 默认图片权限 | Private |
| `PastedAction` | `pasted_action` | 粘贴后动作 | Waiting |
| `IsAutoClearPreview` | `is_auto_clear_preview` | 上传后是否自动清空预览 | false |

### 2.8 `Watermark/*`
- `app/Enums/Watermark/Mode.php`：`Overlay = 1`（覆盖原图）、`Dynamic = 2`（访问时动态生成）。
- `app/Enums/Watermark/FontOption.php`（文字水印）：`text`, `color`, `size`, `font`, `position`, `angle`, `x`, `y`。
- `app/Enums/Watermark/ImageOption.php`（图片水印）：`image`, `position`, `width`, `height`, `rotate`, `opacity`, `x`, `y`。
- 水印位置取值见 `Group::POSITIONS`（10 种，含 `tiled` 平铺）。

### 2.9 `Scan/*`
- `app/Enums/Scan/TencentOption.php`：`secret_id`, `secret_key`, `region`, `endpoint`, `biz_type`。
- `app/Enums/Scan/AliyunOption.php`：`access_key_id`, `access_key_secret`, `region_id`, `scenes`, `biz_type`。
- `app/Enums/Scan/NsfwJsOption.php`：`api_url`, `attr_name`, `threshold`。
- 驱动名取值：`tencent` / `aliyun` / `nsfwjs`（`config/convention.php` 与 `GroupRequest`）。

### 2.10 `Mail/SmtpOption.php`
`transport=smtp`, `host`, `port`, `encryption`, `username`, `password`, `timeout`, `from_address`, `from_name`。

---

## 3. HTTP 接口面

路由文件：`routes/web.php`（web + admin）、`routes/api.php`（API v1）、`routes/auth.php`（认证）、`routes/image.php`（原图输出）。
注册：`app/Providers/RouteServiceProvider.php`（`api` 前缀 `/api`，web 无前缀）。
中间件别名：`app/Http/Kernel.php` 第 56-68 行，其中自定义 `auth.admin` → `AuthenticateWithAdmin`。
统一响应格式：`app/Http/Result.php` → `{status, message, data}`。

### 3.1 游客（未登录）

| Method | Path | Controller@action | 中间件/说明 |
|---|---|---|---|
| GET | `/` | 闭包 `view('welcome')` | `CheckIsInstalled` + `CheckIsEnableGuestUpload`；游客上传页 |
| ANY | `/install` | `Controller@install` | 安装向导（仅未安装时可访问） |
| POST | `/upload` | `Controller@upload` | **游客可用的 Web 上传**（`ImageService::store`） |
| GET | `/register` | `RegisteredUserController@create` | `guest` + `CheckIsEnableRegistration` |
| POST | `/register` | `RegisteredUserController@store` | 同上 |
| GET/POST | `/login` | `AuthenticatedSessionController@create/store` | `guest` |
| GET/POST | `/forgot-password` | `PasswordResetLinkController@create/store` | `guest` |
| GET/POST | `/reset-password/{token}` | `NewPasswordController@create/store` | `guest` |
| ANY | `/{key}.{extension}` | `Controller@output` | `routes/image.php`，`cache.headers`；**原图保护代理输出**，扩展名限定在 `accepted_file_suffixes` |
| GET | `/api/v1/strategies` | `Api\V1\StrategyController@index` | `CheckIsEnableApi`；游客取 guest 组的策略 |
| POST | `/api/v1/upload` | `Api\V1\ImageController@upload` | `CheckIsEnableApi` + `throttle` 未设；带 Authorization 时校验 Token，否则按游客 |
| POST | `/api/v1/tokens` | `Api\V1\TokenController@store` | `throttle:3,1`；邮箱密码换 Sanctum token |

### 3.2 已登录用户（`auth` 会话）

| Method | Path | Controller@action | 说明 |
|---|---|---|---|
| GET | `/dashboard` | `User\UserController@dashboard` | 用户首页，返回策略与组配置 |
| GET | `/gallery` | `Common\GalleryController@index` | `CheckIsEnableGallery`；公共画廊分页 |
| GET | `/settings` | `User\UserController@settings` | 设置页 |
| PUT | `/settings` | `User\UserController@update` | 改昵称/主页/密码/用户配置 |
| PUT | `/settings/set-strategy` | `User\UserController@setStrategy` | 设置默认策略 |
| GET | `/api` | `Common\ApiController@index` | `CheckIsEnableApi`；API 文档页 |
| GET | `/upload` | 闭包 `view('user.upload')` | 上传页 |
| GET | `/images` | `User\ImageController@index` | 图片管理页 |
| GET | `/user/images` | `User\ImageController@images` | 图片列表 JSON |
| GET | `/user/images/{id}` | `User\ImageController@image` | 单图详情 |
| DELETE | `/user/images` | `User\ImageController@delete` | 删除图片 |
| PUT | `/user/images/permission` | `User\ImageController@permission` | 批量设置公开/私有 |
| PUT | `/user/images/rename` | `User\ImageController@rename` | 重命名（alias_name） |
| PUT | `/user/images/movement` | `User\ImageController@movement` | 移动图片到相册 |
| GET | `/user/albums` | `User\AlbumController@albums` | 相册列表 |
| POST | `/user/albums` | `User\AlbumController@create` | 创建相册 |
| PUT | `/user/albums/{id}` | `User\AlbumController@update` | 修改相册 |
| DELETE | `/user/albums/{id}` | `User\AlbumController@delete` | 删除相册 |
| GET | `/verify-email*`, `/confirm-password`, POST `/logout` | 认证控制器 | `routes/auth.php` |

### 3.3 管理员（`auth` + `auth.admin`，前缀 `/admin`）

| Method | Path | Controller@action |
|---|---|---|
| GET | `/admin/console` | `Admin\ConsoleController@index`（统计仪表盘） |
| GET | `/admin/users` | `Admin\UserController@index` |
| GET | `/admin/users/{id}` | `Admin\UserController@edit` |
| PUT | `/admin/users/{id}` | `Admin\UserController@update` |
| DELETE | `/admin/users/{id}` | `Admin\UserController@delete` |
| GET | `/admin/images` | `Admin\ImageController@index`（高级筛选） |
| PUT | `/admin/images/{id}` | `Admin\ImageController@update`（占位，no-op） |
| DELETE | `/admin/images/{id}` | `Admin\ImageController@delete` |
| GET | `/admin/groups` | `Admin\GroupController@index` |
| GET/POST | `/admin/groups/create` | `Admin\GroupController@add/create` |
| GET/PUT/DELETE | `/admin/groups/{id}` | `Admin\GroupController@edit/update/delete` |
| DELETE | `/admin/groups/{id}/clear-cache` | `Admin\GroupController@clearCache`（清原图输出缓存） |
| GET | `/admin/strategies` | `Admin\StrategyController@index` |
| GET/POST | `/admin/strategies/create` | `Admin\StrategyController@add/create` |
| GET/PUT/DELETE | `/admin/strategies/{id}` | `Admin\StrategyController@edit/update/delete` |
| GET | `/admin/settings` | `Admin\SettingController@index` |
| PUT | `/admin/settings/save` | `Admin\SettingController@save` |
| POST | `/admin/settings/mail-test` | `Admin\SettingController@mailTest` |
| GET | `/admin/settings/check-update` | `Admin\SettingController@checkUpdate` |
| POST | `/admin/settings/upgrade` | `Admin\SettingController@upgrade` |
| GET | `/admin/settings/upgrade/progress` | `Admin\SettingController@upgradeProgress` |

### 3.4 API Token（`auth:sanctum`，前缀 `/api/v1`）

| Method | Path | Controller@action |
|---|---|---|
| GET | `/api/v1/images` | `Api\V1\ImageController@images`（分页 + filter） |
| DELETE | `/api/v1/images/{key}` | `Api\V1\ImageController@destroy`（按 key 删除） |
| GET | `/api/v1/albums` | `Api\V1\AlbumController@index` |
| DELETE | `/api/v1/albums/{id}` | `Api\V1\AlbumController@destroy` |
| DELETE | `/api/v1/tokens` | `Api\V1\TokenController@clear`（清空当前用户全部 token） |
| GET | `/api/v1/profile` | `Api\V1\UserController@index`（返回容量、用量等） |

### 3.5 控制器职责速览
- `Controller`（基类，`app/Http/Controllers/Controller.php`）：安装向导 `install`、**Web 上传 `upload`**、**原图代理输出 `output`**。
- `User/UserController`：dashboard / settings / update / setStrategy。
- `User/ImageController`：图片列表/详情/删除/权限/重命名/移动。
- `User/AlbumController`：相册 CRUD。
- `Common/GalleryController`：公共画廊。`Common/ApiController`：仅渲染文档页。
- `Admin/ConsoleController`：统计（今日/昨日/本周/本月上传量、30 天曲线）。
- `Admin/UserController`：用户 CRUD / 容量 / 冻结 / 改密 / 切换组。
- `Admin/ImageController`：全站图片检索（限定符 `name: album: group: strategy: email: extension: md5: sha1: ip: is: order:`）与删除。
- `Admin/GroupController`：用户组 CRUD、默认/游客组保护、清缓存。
- `Admin/StrategyController`：存储策略 CRUD、绑定用户组（`groups()` attach/sync）。
- `Admin/SettingController`：站点配置保存、邮件测试、检查更新、在线升级。
- `Api/V1/*`：见 3.4；`TokenController` 负责签发/清空 Sanctum token。

### 3.6 中间件（`app/Http/Middleware/`）
- `Authenticate`（`auth` 别名）：未登录跳登录。
- `AuthenticateWithAdmin`（`auth.admin` 别名）：继承 `Authenticate`，再校验 `is_adminer`，否则 403。
- `CheckIsInstalled`：无 `installed.lock` 时跳转 `/install`。
- `CheckIsEnableApi`：`ConfigKey::IsEnableApi` 关闭则 403/404。
- `CheckIsEnableGallery`：`ConfigKey::IsEnableGallery` 关闭则 403/404。
- `CheckIsEnableGuestUpload`：`IsAllowGuestUpload` 关闭且未登录则跳登录。
- `CheckIsEnableRegistration`：`IsEnableRegistration` 关闭则 403/404。
- `RedirectIfAuthenticated`（`guest` 别名）、`EncryptCookies`、`PreventRequestsDuringMaintenance`、`TrimStrings`、`TrustHosts`、`TrustProxies`、`VerifyCsrfToken`（框架标准中间件）。

---

## 4. 业务能力清单

| 能力域 | 功能 | 实现位置 |
|---|---|---|
| 安装 | Web 安装向导 + 环境检测 | `Controller@install` |
| 安装 | CLI 安装、写 .env、迁移、种子 | `app/Console/Commands/Install.php`（`lsky:install`） |
| 安装 | 初始化配置/默认组/默认本地策略 | `database/seeders/InstallSeeder.php`、`DatabaseSeeder.php` |
| 用户认证 | 注册（可开关）/登录/登出/记住我 | `routes/auth.php`、`Auth/*Controller`、`CheckIsEnableRegistration` |
| 用户认证 | 密码找回 / 重置 | `Auth/PasswordResetLinkController`、`NewPasswordController` |
| 用户认证 | 邮箱验证（可开关 `IsUserNeedVerify`） | `Auth/EmailVerification*`、`VerifyEmailController` |
| 用户认证 | API Token 签发（账号密码换 token） | `Api/V1/TokenController@store` |
| 用户 | 个人资料/改密/默认设置 | `User/UserController@update` |
| 用户 | 个人默认相册/默认策略/默认权限/粘贴动作 | `UserConfigKey`、`UserSettingRequest` |
| 用户 | 配额与用量统计 | `User.capacity`、`useCapacity`、`Api/V1/UserController@index` |
| 用户 | 账号冻结 | `UserStatus::Frozen`、`ImageService::store` 校验 |
| 上传 | Web 上传（游客/用户） | `Controller@upload` → `ImageService::store` |
| 上传 | API 上传（游客/Token/会话） | `Api/V1/ImageController@upload` |
| 上传 | 多存储策略（10 种驱动） | `ImageService::getAdapter`、`Strategy::DRIVERS` |
| 上传 | 组—策略多对多授权 | `group_strategy` 表、`Group::strategies()` |
| 上传 | 文件类型/大小校验 | `ImageService::store`、`GroupConfigKey` |
| 上传 | 用户容量限制 | `ImageService::store`（`size + 已用 > capacity`） |
| 上传 | 上传频率限制（分/时/天/周/月） | `ImageService::rateLimiter` |
| 上传 | 路径/文件命名规则（模板变量） | `ImageService::replacePathname`、`{Y}{m}{d}{uniqid}{md5}...` |
| 上传 | 图片压缩/转格式/质量 | `ImageService::store`（Intervention Image） |
| 上传 | MD5/SHA1 去重（同策略复用文件） | `ImageService::store` |
| 上传 | 缩略图生成 | `ImageService::makeThumbnail`、`lsky:thumbnails` 命令 |
| 图片管理 | 列表/筛选/排序 | `User/ImageController@images`、`Image::scopeFilter` |
| 图片管理 | 详情查看 | `User/ImageController@image` |
| 图片管理 | 重命名（别名） | `User/ImageController@rename` |
| 图片管理 | 公开/私有权限切换 | `User/ImageController@permission`、`ImagePermission` |
| 图片管理 | 移动到相册 | `User/ImageController@movement` |
| 图片管理 | 删除（同步删物理文件/缩略图/缓存） | `User/ImageController@delete`、`UserService::deleteImages`、`Image` deleting 钩子 |
| 图片管理 | 外链复制（url/html/bbcode/markdown 等） | `Image::links()` accessor |
| 相册 | 相册 CRUD | `User/AlbumController` |
| 相册 | 相册图片数量冗余维护 | `User/AlbumController`、`User/ImageController@movement` |
| 标签 | **无标签功能** | — |
| 用户组与权限 | 组 CRUD、默认组/游客组 | `Admin/GroupController` |
| 用户组与权限 | 组级上传限制/大小/后缀/命名规则 | `GroupConfigKey`、`Admin/GroupController`、`GroupRequest` |
| 用户组与权限 | 组可用策略绑定 | `Admin/StrategyController@create/update`（attach/sync groups） |
| 策略管理 | 策略 CRUD + 校验凭据 | `Admin/StrategyController`、`StrategyRequest` |
| 策略管理 | 本地策略符号链接管理 | `Strategy::booted()` saving/deleted |
| 站点设置 | 名称/关键字/描述/公告/ICP/开关初始化容量/邮件 | `Admin/SettingController@index/save`、`ConfigKey` |
| 站点设置 | 邮件测试 | `Admin/SettingController@mailTest`、`app/Mail/Test.php` |
| 容量限制 | 用户总容量、上传时校验 | `User.capacity`、`ImageService::store` |
| 图片审核 | 腾讯 IMS / 阿里云绿网 / nsfwjs 三驱动 | `ImageService::scan`、`Scan/*Option`、`GroupConfigKey::IsEnableScan` |
| 图片审核 | 违规标记或删除 | `ImageService::store`（`scanned_action`） |
| 水印 | 文字/图片水印、覆盖或动态 | `ImageService::stickWatermark`、`Watermark/*` |
| 邮件 | SMTP 配置、找回密码、验证邮件 | `ConfigKey::Mail`、`Utils::config`、`Auth/*` |
| 画廊 | 公共图片瀑布流 | `Common/GalleryController@index`、`CheckIsEnableGallery` |
| API | 开放 API v1（上传/图片/相册/资料/Token） | `routes/api.php`、`Api/V1/*` |
| 控制台 | 上传统计与曲线图 | `Admin/ConsoleController@index`（ECharts） |
| 升级 | 检查更新 / 在线打补丁 / 迁移 | `Admin/SettingController@checkUpdate/upgrade`、`UpgradeService`、`lsky:upgrade` |
| 运维 | 清理图片输出缓存 | `Admin/GroupController@clearCache` |

---

## 5. 关键业务流程

### 5.1 上传流程（`ImageService::store`，`app/Services/ImageService.php` 第 75-243 行）
1. 取 `file`；若未登录且 `IsAllowGuestUpload` 关闭 → 抛 `UploadException`。
2. 确定组：登录用户取 `user->group`，否则取 `is_guest=true` 的组。
3. 取组可用策略 `group->strategies()`；为空则报错“没有可用的储存”。
4. 后缀白名单校验（`AcceptedFileSuffixes`）；大小校验（`MaximumFileSize`，KB）。
5. 选策略：请求带 `strategy_id` 则从组内查找；否则取 `first()`（**“多策略选择逻辑”即默认取第一个，前端通过 `GET /api/v1/strategies` 展示下拉**）。写入 `image.strategy_id`。
6. 登录用户附加校验：邮箱验证（若 `IsUserNeedVerify`）、账号状态（`UserStatus::Normal`）、容量（`file size + images.sum(size) > capacity` → 空间不足）；设置默认相册（`DefaultAlbum`）、默认权限（`DefaultPermission`）。
7. 频率限制：`rateLimiter()`（见 5.2）。
8. 图片处理：跳过 ico/gif/svg；按 `ImageSaveQuality`/`ImageSaveFormat` 压缩转换；若 `IsEnableWatermark` 且 mode=Overlay 则烧录水印。
9. 用 `PathNamingRule + '/' + FileNamingRule` 经 `replacePathname()` 生成路径（模板变量 `{Y}{y}{m}{d}{timestamp}{uniqid}{md5}{md5-16}{str-random-16}{str-random-10}{filename}{uid}`）；`getimagesize` 取宽高。
10. 填充 md5/sha1/size/mimetype/extension/width/height/uploaded_ip；再用 Flysystem `writeStream` 写入；若同策略已有相同 md5+sha1 则跳过写文件、复用已有 path/name。
11. 保存记录 + 递增 `user.image_num`、`album.image_num`。
12. 违规审查（若开启，跳过 psd/ico/tif/svg）：`scan()`，违规按 `ScannedAction` 标记 `is_unhealthy` 或删除。
13. `makeThumbnail()` 生成缩略图；删除临时文件。

**游客上传入口**：`POST /upload`（web）与 `POST /api/v1/upload`（API）。游客图片 `user_id=NULL`，`group_id=游客组`，限频按 `uploaded_ip`。
**命名规则**见上模板；默认路径 `{Y}/{m}/{d}`，默认文件名 `{uniqid}`。

### 5.2 上传频率限制（`ImageService::rateLimiter`）
对早于 `-1 minute/hour/day/week/month` 的记录计数：
- 登录用户按 `user_id` 统计，游客按 `uploaded_ip + user_id IS NULL` 统计。
- 超过对应 `GroupConfigKey::LimitPer*` 即抛“每 X 内你最多可以上传 N 张图片”。

### 5.3 鉴权方式
- **会话**：`web` guard（`config/auth.php`，session driver），用于 Web 页面与 `/user/*`。
- **API Token（Sanctum）**：`POST /api/v1/tokens`（邮箱+密码）→ `User::createToken($email)->plainTextToken`（`Api/V1/TokenController@store`）；受保护路由用 `auth:sanctum`。Token 存 `personal_access_tokens.token`（sha256），`config/sanctum.php` 中 `expiration=null`（永不过期）、`guard=['web']`。
- **API 上传的混合鉴权**：`Api/V1/ImageController@upload` 若请求带 `Authorization` 头，则遍历所有 guard 尝试 `Auth::check()`，失败抛 `AuthenticationException`；不带则视为游客。
- `routes/api.php` 的 `POST /api/v1/upload` 未加 `auth:sanctum`，因此游客可直接调用。
- 登录限流：`Auth/LoginRequest` 5 次失败锁定；Token 签发 `throttle:3,1`。

### 5.4 图片访问 / **图片分发**机制（【待砍除区】）
lsky-pro 的图片分发有两套，全部在本地程序侧：

1. **直链输出**（默认）：`Image::url()` 直接拼接 `strategy.configs.url + pathname + queries`，指向存储桶/CDN 或本地符号链接 `public/{rootName}`（本地策略在 `Strategy::saving` 建立 symlink，`config/filesystems.php` 的 `links` 把 `public/i` → `storage/app/uploads`）。
2. **原图保护代理**：当组开启 `GroupConfigKey::IsEnableOriginalProtection` 时，`Image::url()` 返回 `asset("{key}.{extension}")`；请求命中 `routes/image.php` 的 `ANY /{key}.{extension}` → `Controller@output`（`app/Http/Controllers/Controller.php` 第 121-186 行）：
   - 校验 key + extension、组是否开启原图保护，否则 404。
   - 用 `image.filesystem()->read(pathname)` 从存储读取原始字节。
   - 若水印 mode=Dynamic，调用 `ImageService::stickWatermark` 动态烧水印。
   - 按 `ImageCacheTtl` 写入 `Cache`（键 `image_{key}`）。
   - psd/tif/bmp 转为 png；ico/svg 原样输出；以 `StreamedResponse` 返回，附 `cache.headers:public;max_age=2628000;etag`。
   - 缩略图由 `public/thumbnails/{md5}.png` 直接静态提供（`Image::thumbUrl()`）。
- 相关文件汇总（砍除/改造目标）：
  - `routes/image.php`（`/{key}.{extension}` 路由）
  - `Controller@output`、`Controller@upload`
  - `Image::url()` / `thumbUrl()` / `getThumbnailPathname()` / `filesystem()` / `deleting` 钩子
  - `ImageService::getAdapter` / `makeThumbnail` / `stickWatermark` / 全部 Strategy 适配器
  - `Strategy::booted()` 的符号链接逻辑
  - `Admin/GroupController@clearCache`（清 `image_{key}` 缓存）
  - `MakeThumbnails` 命令、`config/app.php` 的 `thumbnail_path`、`config/filesystems.php` 的 uploads disk / links
  - `composer.json` 全部 `league/flysystem-*`、`overtrue/flysystem-*`、`wispx/flysystem-upyun`、`zing/flysystem-oss` 依赖
  - 注意：**防盗链**并无独立实现；原图保护（`IsEnableOriginalProtection`）+ 随机 `key` 是唯一的“保护/防直链”手段，且它依赖本程序代理输出。

### 5.5 安装 / 升级流程
- **安装**：`GET/POST /install` → `Controller@install` 检测 PHP 扩展与 `readlink/symlink` 等函数；POST 时调用 `Artisan::call('lsky:install', ...)` → `app/Console/Commands/Install.php`：覆盖数据库连接配置 → `migrate:fresh` → `db:seed InstallSeeder` → 改写 `.env` → 写 `installed.lock`。随后 `Controller@install` 创建超级管理员（`is_adminer=true`），并把默认策略 URL 设为 `{当前域名}/i`。
- **InstallSeeder**（`database/seeders/InstallSeeder.php`）：写入 `config('convention.app')` 全部配置到 `configs` 表；创建“系统默认组&游客组”（`is_default=is_guest=true`）与默认本地策略。
- **升级**：`Admin/SettingController@checkUpdate/upgrade` + `app/Services/UpgradeService.php`（`ApiUrl=https://api.lsky.pro/v2`）：检查 `/versions`、拉取 `/diff/{version}` 补丁清单、下载并校验 md5、按 added/copied/deleted 应用补丁、更新 `AppVersion` 配置、`migrate --seed`、`optimize:clear`。CLI 入口 `lsky:upgrade`（`app/Console/Commands/Upgrade.php`）。进度存 Cache `upgrade_progress`。

---

## 6. 前端形态

- `resources/views/`：Blade 模板（67 个），组件化（`components/` 下 box/button/modal/table/fieldset/upload/switch 等），布局 `layouts/app.blade.php`、`layouts/guest.blade.php`。页面覆盖 install / auth / welcome / dashboard / upload / images / settings / gallery / api 文档 / admin(console,user,group,strategy,image,setting)。
- `resources/js/`：`app.js`（启动 Alpine.js + 两个 Alpine store：sidebar、modal；挂 `window.utils` 工具）、`bootstrap.js`、`context-js.js`。
- `resources/css/`：`app.css` + LESS（common/context-js/fontawesome/gallery），Tailwind（`tailwind.config.js`）。
- `public/`（构建产物 + 第三方库）：`js/` 含 jquery、blueimp-file-upload（上传）、blueimp-load-image、clipboard、dragselect（多选）、echarts（统计图）、justifiedGallery/masonry/imagesloaded（画廊）、viewer-js、context-js；`css/` 含 fontawesome、gallery、markdown、justified-gallery；`thumbnails/` 输出目录；`index.php` 入口。
- **形态总结**：传统 **多页 Blade 服务端渲染 + 少量 Alpine 状态 + jQuery 插件增强** 的后台系统（非 SPA），表单以 PUT/DELETE + JSON 响应（`{status,message,data}`）与后端交互。

---

## 7. 砍掉「后端存储 + 图片分发」后剩下的能力

假设：**所有存储与图片分发交给 PicGo**，lsky-pro 只保留“图床业务管理平台”（用户/组/权限/审核/相册/元数据）。

### 7.1 应删除
**表 / 字段**
- `strategies` 表整体删除（或仅保留轻量“存储后端”引用，见 7.2）。
- `group_strategy` 中间表删除。
- `images` 表：删除 `strategy_id`（外键）、`path`、`name`（若 PicGo 只回传 URL 可删）、`uploaded_ip` 视限频策略保留；`key`（用于本程序代理输出的短链）可删除或改为 PicGo 返回的标识。
- `configs`：删除与本地存储/输出相关的键（见 7.3）。
- `groups.configs` 中 `image_cache_ttl`、`image_save_format`、`image_save_quality`（如前端不再处理图片）、水印相关（见下）按需删除。

**枚举**
- `app/Enums/StrategyKey.php` 及整个 `app/Enums/Strategy/` 目录（Local/S3/Oss/Cos/Kodo/Uss/Sftp/Ftp/WebDav/Minio Option）。
- 水印 `app/Enums/Watermark/*`（若水印交给 PicGo）。

**控制器 / 服务 / 路由**
- `Controller@upload`（Web 上传）、`Controller@output`（原图代理输出）、`routes/image.php`。
- `Api/V1/StrategyController`、`Api/V1/ImageController@upload`（上传改由 PicGo 完成）。
- `ImageService::getAdapter`、`stickWatermark`、`makeThumbnail`、`replacePathname`、`ImageSaveFormat/Quality` 相关逻辑。
- `Strategy::booted()` 符号链接、`Admin/GroupController@clearCache`、`lsky:thumbnails` 命令。
- `composer.json` 中所有 Flysystem 存储适配器依赖；`config/filesystems.php` 的 `uploads` disk 与 `links`；`config/app.php` 的 `thumbnail_path`。

**中间件**
- 无存储/分发专用中间件可删；`CheckIsEnableApi` 等保留。

### 7.2 应改造
- **上传接口**：`POST /upload`、`POST /api/v1/upload` 改为“登记接口”——PicGo 完成上传后回调，提交 `url / md5 / sha1 / size / width / height / mimetype / extension / origin_name`，lsky-pro 仅落库并生成 `links`。或保留文件接收但转发给 PicGo。
- **`images` 表**：新增 `url`（直链，来自 PicGo）、`delete_url` / `delete_token`（删除回调凭据）；`key` 可保留作站内短链标识。
- **`Image` 模型**：`url()` accessor 改为返回存储的 `url`；移除 `filesystem()`、`thumbUrl()` 的本地文件判断、`deleting` 钩子中删物理文件逻辑改为调用 PicGo 删除接口。
- **`strategy` 概念**：可改造为“PicGo 存储配置/图床账号”选择，`group_strategy` 可保留为“组可用的 PicGo 图床配置”，或合并进 PicGo 侧配置。
- **限频、容量、审核、相册、命名规则**：全部保留，但容量/大小基于 PicGo 回传元数据统计。
- **`Admin/StrategyController` / `Admin/Strategy` 视图**：若保留“图床配置”概念，改造为 PicGo 图床/Token 管理。
- **审核 `ImageService::scan`**：可保留（基于回传文件或 URL 调用第三方审核），但不再依赖本地文件句柄。

### 7.3 建议删除的配置键（`ConfigKey` / `GroupConfigKey`）
- `GroupConfigKey`：`ImageCacheTtl`（本程序输出缓存）、`ImageSaveFormat`、`ImageSaveQuality`（若不在本地转码）、`IsEnableOriginalProtection`（原图保护=本程序代理，不再需要）、水印相关（若交给 PicGo）、`PathNamingRule`/`FileNamingRule`（若由 PicGo 决定命名）。
- `UserConfigKey`：`DefaultStrategy`（改为默认 PicGo 图床）。
- `ConfigKey`：无直接存储项需删，`AppVersion`/升级流程可保留或简化为纯前端版本。

### 7.4 明确保留（与存储/分发无关的核心能力）
用户与认证（注册/登录/找回/验证/Token）、用户组与权限、用户配额与用量、上传频率限制、图片元数据管理与检索、相册、公开权限（公开/私有）、违规审核与标记、画廊、统计控制台、站点设置、邮件、安装流程。这些均可原样保留（部分仅需去掉上传/存储耦合）。

---

## 附：关键文件索引
- 迁移：`database/migrations/*.php`
- 模型：`app/Models/{User,Group,Strategy,Album,Image,Config,Model}.php`
- 枚举：`app/Enums/**`
- 路由：`routes/{web,api,auth,image}.php`
- 控制器：`app/Http/Controllers/**`
- 中间件：`app/Http/Middleware/**`
- 服务：`app/Services/{ImageService,UserService,UpgradeService}.php`
- 命令：`app/Console/Commands/{Install,Upgrade,MakeThumbnails}.php`
- 默认配置：`config/convention.php`、`config/filesystems.php`、`config/auth.php`、`config/sanctum.php`
- 种子：`database/seeders/{DatabaseSeeder,InstallSeeder}.php`
- 请求校验：`app/Http/Requests/**`
- 前端：`resources/views/**`、`resources/js/**`、`public/**`
