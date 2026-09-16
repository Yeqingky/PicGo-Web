/**
 * 后端接口类型 —— 与 `docs/API.md` 手动保持同步。
 *
 * ⚠️ 命名规则（D81）：
 *  - **字段名一律 PascalCase**（与后端 JSON 完全一致，不做任何转换）
 *  - 缩写词全大写：`UID` / `URL` / `ID` / `API`
 *  - 例外：Lsky 兼容层（`/api/v1/**`）保持 snake_case，见文件末尾 `Lsky*` 类型
 *
 * ⚠️ 本文件是「文档 → 代码」的映射，**改动接口时必须同步改这里**（AGENTS.md §9）。
 */

// ---------------------------------------------------------------------------
// 信封与分页
// ---------------------------------------------------------------------------

/** 统一响应体（内部 API 专用）。 */
export interface Envelope<T> {
  Code: number
  Message: string
  Data: T
}

/** 分页响应的 Data。 */
export interface PageData<T> {
  Items: T[]
  Total: number
  Page: number
  PageSize: number
}

/** 分页请求参数（Query 一律 PascalCase，与响应字段一致）。 */
export interface PageQuery {
  Page?: number
  PageSize?: number
}

// ---------------------------------------------------------------------------
// 错误码（docs/API.md §0）
// ---------------------------------------------------------------------------

export const ApiCode = {
  OK: 0,

  InvalidParam: 40001,

  BadCredentials: 40101,
  Unauthorized: 40102,
  TokenExpired: 40103,
  AccountDisabled: 40104,

  /** 权限不足 */
  Forbidden: 40301,
  /** 配额不足（与 40301 严格区分，D20） */
  QuotaExceeded: 40302,

  NotFound: 40401,
  Conflict: 40901,
  TooManyRequests: 42901,

  Internal: 50001,
  AgentUnavailable: 50002,
  UploadFailed: 50003,
  PluginFailed: 50004,
  ThemeFailed: 50005,
} as const

export type ApiCodeValue = (typeof ApiCode)[keyof typeof ApiCode]

/** 需要触发「静默刷新」的错误码。 */
export const AUTH_RETRY_CODES: number[] = [ApiCode.Unauthorized, ApiCode.TokenExpired]

/**
 * 统一的服务端错误。
 *
 * 由 `lib/http.ts` 的响应拦截器抛出：
 *  - 业务失败（HTTP 4xx/5xx 且响应体是信封）→ 带上后端 `Code` 与 `Message`
 *  - 传输层失败（超时/断网）→ `Code = -1`
 */
export class ApiError extends Error {
  readonly Code: number
  readonly HttpStatus: number
  readonly Data: unknown

  constructor(code: number, message: string, httpStatus = 0, data: unknown = null) {
    super(message)
    this.name = 'ApiError'
    this.Code = code
    this.HttpStatus = httpStatus
    this.Data = data
  }

  /** 是否为「需要重新登录」的错误（40102 / 40103）。 */
  get isAuthError(): boolean {
    return AUTH_RETRY_CODES.includes(this.Code)
  }

  /** 是否为「权限不足」（40301）。 */
  get isForbidden(): boolean {
    return this.Code === ApiCode.Forbidden
  }

  /** 是否为「需要先改密码」（后端用 40301 + 特定 Message 表达，见 API.md §1）。 */
  get isPasswordChangeRequired(): boolean {
    return this.Code === ApiCode.Forbidden && this.message.includes('请先修改密码')
  }
}

/** 把任意异常归一化成 ApiError（供 UI 层统一处理）。 */
export function toApiError(err: unknown): ApiError {
  if (err instanceof ApiError) return err
  if (err instanceof Error) return new ApiError(-1, err.message)
  return new ApiError(-1, String(err))
}

// ---------------------------------------------------------------------------
// 认证（API.md §1）
// ---------------------------------------------------------------------------

export type UserRole = 'admin' | 'user'
export type UserStatus = 'active' | 'disabled'

/** 当前用户 / 用户对象（`Users` + `UserProfiles` 两张表的合并视图）。 */
export interface User {
  UID: string
  Email: string
  Role: UserRole
  Status: UserStatus
  MustChangePassword: boolean
  Nickname: string
  AvatarURL: string
  Homepage: string
  /** 0 = 不限额（D20） */
  CapacityBytes: number
  UsedBytes: number
  ImageCount?: number
  /** 当前用户视图才有：是否设置了密码（纯 OAuth 用户为 false） */
  HasPassword?: boolean
  LastLoginAt: number
  CreatedAt: number
  UpdatedAt?: number
}

/** `POST /auth/login` 的 Data。 */
export interface LoginResponse {
  AccessToken: string
  /** 秒 */
  ExpiresIn: number
  User: User
}

/** `POST /auth/refresh` 的 Data。 */
export interface RefreshResponse {
  AccessToken: string
  ExpiresIn: number
}

/** `GET /auth/identities` 的 Data。 */
export interface IdentitiesResponse {
  HasPassword: boolean
  Identities: BoundIdentity[]
}

export interface BoundIdentity {
  Provider: string
  ProviderLogin: string
  ProviderEmail: string
  AvatarURL: string
  BoundAt: number
}

/** `GET /auth/oauth/providers` 的 Data。 */
export interface OAuthProvidersResponse {
  Providers: OAuthProvider[]
}

export interface OAuthProvider {
  Name: string
  DisplayName: string
}

/** `POST /auth/oauth/{provider}/bind` 的 Data。 */
export interface OAuthBindResponse {
  AuthorizeURL: string
  State: string
}

// ---------------------------------------------------------------------------
// 用户管理（API.md §2，admin）
// ---------------------------------------------------------------------------

export interface UserListQuery extends PageQuery {
  Keyword?: string
  Role?: UserRole
  Status?: UserStatus
  Sort?: string
  Order?: 'asc' | 'desc'
}

export interface CreateUserRequest {
  Email: string
  Password: string
  Role?: UserRole
  Nickname?: string
  /** 省略则由后端按 `user.defaultCapacityBytes` 决定（D21） */
  CapacityBytes?: number
  SendInviteEmail?: boolean
}

export interface UpdateUserRequest {
  Email?: string
  Nickname?: string
  Role?: UserRole
  Status?: UserStatus
  CapacityBytes?: number
  Homepage?: string
  NewPassword?: string
  MustChangePassword?: boolean
}

/** `DELETE /users/{UID}` 的 Data。 */
export interface DeleteUserResponse {
  DeletedUploads: number
  FreedBytes: number
  RemoteDeleteFailed: number
}

/** `POST /users/{UID}/reset-password` 的 Data（明文只返回一次）。 */
export interface ResetPasswordResponse {
  Password: string
}

// ---------------------------------------------------------------------------
// API Token（API.md §11）
// ---------------------------------------------------------------------------

export interface APIToken {
  UID: string
  Name: string
  /** 展示用前缀，如 `pcw_1a2b3c4d` */
  Prefix: string
  LastUsedAt: number
  /** 0 = 永不过期 */
  ExpiresAt: number
  CreatedAt: number
  Expired?: boolean
}

export interface CreateAPITokenRequest {
  Name: string
  /** 省略或 0 → 永不过期 */
  ExpiresInDays?: number
}

/** 创建响应：**明文 `Token` 只在此处返回一次**。 */
export interface CreateAPITokenResponse {
  Token: string
  TokenInfo: APIToken
}

// ---------------------------------------------------------------------------
// 系统（API.md §10）
// ---------------------------------------------------------------------------

export interface SystemInfo {
  Version: string
  SchemaVersion: number
  DatabaseDriver: string
  SiteName: string
  ThemeActive: string
  Uptime: number
}

/** `GET /healthz`（**无信封、字段小写**）。 */
export interface HealthzResponse {
  status: string
  version: string
  agent: 'up' | 'down' | 'restarting'
  uptime: number
}

// ---------------------------------------------------------------------------
// 设置（API.md §11）
// ---------------------------------------------------------------------------

/** 用户级 + 站点公开信息（`GET /settings`）。 */
export interface SettingsResponse {
  Site: SiteSettings
  User: Record<string, unknown>
  Upload: UploadSettings
  Features: SiteFeatures
}

export interface SiteSettings {
  name: string
  subtitle?: string
  description: string
  notice: string
  icp: string
  baseUrl: string
  iconUrl?: string
  allowSelfRegistration: boolean
}

export interface UploadSettings {
  maxSizeBytes: number
  allowedExts: string[]
  blockSvg: boolean
}

export interface SiteFeatures {
  oauthGithubEnabled: boolean
  mailEnabled: boolean
}

/** 用户级设置（落 `UserSettings` 表，键名保持 `dot.lowerCamel`）。 */
export type UserSettingsPayload = Record<string, unknown>

export interface UpdateUserSettingsResponse {
  Applied: string[]
  Ignored?: string[]
}

// ---------------------------------------------------------------------------
// 主题（API.md §10，`GET /site/config`）
// ---------------------------------------------------------------------------

export interface SiteConfigResponse {
  Site: SiteConfigSite
  Theme: SiteConfigTheme | null
  /** 主题缺失/损坏时的原因（`Theme` 为 null 时给出） */
  ThemeError?: string
  Features: SiteFeatures
}

export interface SiteConfigSite {
  Name: string
  Subtitle: string
  Description: string
  Keywords: string
  IconURL: string
  Notice: string
  Icp: string
  BaseURL: string
  Version: string
}

export interface SiteConfigTheme {
  ID: string
  Name: string
  Version: string
  /** 该主题接管的路由前缀（D94.2） */
  Pages: string[]
  /** 主题静态资源前缀，固定 `/theme-assets` */
  AssetBase: string
  /** 已按「DB → manifest Default → 零值」合并后的最终值 */
  Settings: Record<string, unknown>
}

import type { IPluginConfig } from '@/types/schema'

// ---------------------------------------------------------------------------
// 存储 Storage（API.md §3，全部 admin）
// ---------------------------------------------------------------------------

/**
 * 驱动配置字段（**已由服务端 `evaluatePluginConfig` 求值**）。
 *
 * 与 `types/schema.ts` 的 `IPluginConfig` 是**同一个东西**（agent 返回的
 * `Config` schema），因此这里直接复用，避免两份定义漂移。
 *
 * ⚠️ `Name` / `Type` / `Alias` 等是 **picgo 与插件定义的**，原样保留（D81.3 第 5 条）：
 * `repo` / `token` / `path` / `bucket` 这些字段名不适用 D81，前端不得改写。
 */
export type DriverConfigField = IPluginConfig

/** 运行时探测的驱动能力（agent 探测 → 服务端缓存，D77：不硬编码驱动名列表） */
export interface StorageCapabilities {
  SupportsPathTemplate: boolean
  SupportsRemoteDelete: boolean
  /** 该图床是否无视传入文件名（服务端自行命名，如 NodeImage）——上传结果运行时探测回写 */
  ServerRenames: boolean
  /** 该驱动声明的配置字段名（原样） */
  ConfigFields: string[]
  /** 推断 `SupportsPathTemplate` 的依据字段 */
  PathFieldNames: string[]
  DetectedAt: number
  PicgoVersion: string
}

/** `GET /storage/drivers` 的单个驱动 */
export interface StorageDriver {
  Type: string
  Name: string
  Builtin: boolean
  GuiOnly: boolean
  Config: DriverConfigField[]
  Capabilities: StorageCapabilities
  /** 已配置的实例数 */
  ConfigCount: number
}

/**
 * 存储配置（**响应中绝不含任何密钥**，D78）。
 *
 * 同一驱动类型可有多条实例（D64）；对外一律用 `UID`。
 */
export interface StorageConfig {
  UID: string
  Name: string
  Type: string
  /** 映射 picgo 的 `_configName`；**创建后只读** */
  PicgoConfigName: string
  Enabled: boolean
  /** 全局同时只有一条为 true */
  IsDefault: boolean
  PathTemplate: string
  FileTemplate: string
  Capabilities: StorageCapabilities
  /** 是否已配置凭据（**只给布尔**，不给值） */
  HasSecrets: boolean
  /** 已填写的密钥字段名列表（只给字段名） */
  SecretFields: string[]
  Metadata: Record<string, unknown>
  /** 引用该配置的图片数（删除前提示用） */
  UploadCount: number
  CreatedAt: number
  UpdatedAt: number
}

/** `POST /storage/configs` 请求体 */
export interface CreateStorageConfigRequest {
  Name: string
  Type: string
  PicgoConfigName?: string
  Enabled?: boolean
  IsDefault?: boolean
  PathTemplate?: string
  FileTemplate?: string
  /** 明文提交，服务端加密后写 `StorageSecrets` */
  Config?: Record<string, unknown>
}

/** `PATCH /storage/configs/{Uid}` 请求体（**不含密钥、不含 PicgoConfigName**） */
export interface UpdateStorageConfigRequest {
  Name?: string
  Enabled?: boolean
  IsDefault?: boolean
  PathTemplate?: string
  FileTemplate?: string
}

/** `PUT /storage/configs/{Uid}/secrets` 请求体 */
export interface UpdateStorageSecretsRequest {
  /** 只提交需变更的字段；传 `{}` = 不修改；传 `""` = 清空该字段 */
  Config: Record<string, unknown>
}

/** `PUT /storage/configs/{Uid}/secrets` 响应 */
export interface UpdateStorageSecretsResponse {
  UID: string
  HasSecrets: boolean
  SecretFields: string[]
}

/** `DELETE /storage/configs/{Uid}` 响应 */
export interface DeleteStorageConfigResponse {
  Deleted: boolean
  AffectedUploads: number
  /** 删的是默认项时，自动切换到的另一条（可能为空） */
  DefaultSwitchedTo: string
}

/** `POST /storage/configs/{Uid}/test` 响应 */
export interface TestStorageConfigResponse {
  Ok: boolean
  Message: string
  LatencyMs: number
  DriverVersion?: string
}

/** `POST /storage/drivers/schema` 请求体（`DependsOn` 联动回源求值） */
export interface DriverSchemaRequest {
  Type: string
  Answers: Record<string, unknown>
}

/** `POST /storage/drivers/schema` 响应 */
export interface DriverSchemaResponse {
  Type: string
  Name: string
  Config: DriverConfigField[]
}

// ---------------------------------------------------------------------------
// 图库 Gallery（API.md §4）
// ---------------------------------------------------------------------------

export type UploadStatus = 'pending' | 'success' | 'failed'
export type UploadSource = 'web' | 'api' | 'lsky'

/**
 * 图片元数据（与 `Uploads` 表一一对应）。
 *
 * ⚠️ **不做缩略图**（D84）：卡片直接用 `URL`，`ThumbURL` **保留但不使用**。
 */
export interface Upload {
  UID: string
  UserUID: string
  StorageUID: string
  /** 最终文件名（含扩展名，含魔法文件名结果） */
  FileName: string
  /** 原始上传文件名 */
  OriginalName: string
  /** 用户重命名（展示优先） */
  AliasName: string
  Size: number
  MimeType: string
  /** 不含点，小写 */
  Extension: string
  Width: number
  Height: number
  /** 仅记录，不做去重（D66） */
  SHA256: string
  URL: string
  /** 预留：本项目不做缩略图（D84），**前端不得依赖** */
  ThumbURL: string
  Status: UploadStatus
  Error: string
  Source: UploadSource
  JobUID: string
  Metadata: Record<string, unknown>
  CreatedAt: number
  UpdatedAt: number

  // ---- 列表响应中的附加只读字段 ----
  /** 便于前端直接展示存储名，免二次查询 */
  StorageName?: string
  /** **仅当 `Scope=all`** 时返回（管理员视图） */
  UserEmail?: string
}

/** `GET /uploads` 的查询参数 */
export interface UploadListQuery extends PageQuery {
  /** 匹配 FileName / OriginalName / AliasName */
  Keyword?: string
  StorageUID?: string
  Status?: UploadStatus
  /** `mine`（默认）/ `all`（仅管理员；普通用户传 all 会被静默降级） */
  Scope?: 'mine' | 'all'
  Sort?: 'CreatedAt' | 'Size' | 'FileName'
  Order?: 'asc' | 'desc'
}

/** `POST /uploads` 响应（立即返回，上传在后台队列推进） */
export interface CreateUploadsResponse {
  JobUID: string
  StorageUID: string
  Items: UploadQueueItemRef[]
}

export interface UploadQueueItemRef {
  Seq: number
  FileName: string
  UploadUID: string
  Status: UploadStatus
}

/** `POST /uploads/from-url` 请求体 */
export interface UploadFromUrlRequest {
  URLs: string[]
  StorageUID: string
}

/** `PATCH /uploads/{Uid}` 请求体 */
export interface UpdateUploadRequest {
  AliasName?: string
}

/** `DELETE /uploads/{Uid}` 响应 */
export interface DeleteUploadResponse {
  Deleted: boolean
  RemoteDeleted: boolean
  /** 该驱动的 capability */
  RemoteDeleteSupported: boolean
  /** 不支持或失败时给原因 */
  RemoteDeleteError: string
  FreedBytes: number
}

/** `POST /uploads/batch-delete` 请求体 */
export interface BatchDeleteUploadsRequest {
  UIDs: string[]
  DeleteRemote?: boolean
}

/** `POST /uploads/batch-delete` 响应 */
export interface BatchDeleteUploadsResponse {
  Total: number
  Deleted: number
  Skipped: number
  FailedRemoteDelete: number
  FreedBytes: number
  Items: BatchDeleteUploadResultItem[]
}

export interface BatchDeleteUploadResultItem {
  UID: string
  Deleted: boolean
  RemoteDeleted: boolean
  Error: string
}

/** `GET /uploads/stats` 响应 */
export interface UploadStats {
  Scope: 'mine' | 'all'
  Total: number
  TotalSize: number
  SuccessCount: number
  FailedCount: number
  PendingCount: number
  TodayCount: number
  WeekCount: number
  ByStorage: { StorageUID: string; Name: string; Count: number }[]
  ByExtension: { Extension: string; Count: number }[]
}

/** 外链格式（D68：Markdown / 直链 / HTML） */
export type LinkFormatName = 'markdown' | 'url' | 'html'

/** `GET /uploads/{Uid}/link` 响应 */
export interface UploadLinkResponse {
  UID: string
  Format: LinkFormatName
  Text: string
  URL: string
}

/** `POST /uploads/links`（批量）请求体 */
export interface UploadLinksRequest {
  UIDs: string[]
  Format: LinkFormatName
}

/** `POST /uploads/links` 响应（多行拼接，供一次性复制） */
export interface UploadLinksResponse {
  Format: LinkFormatName
  Text: string
  Items: { UID: string; URL: string }[]
}

// ---------------------------------------------------------------------------
// 插件 Plugins（API.md §6，全部 admin）
// ---------------------------------------------------------------------------

export interface Plugin {
  Name: string
  Version: string
  Description: string
  Author: string
  Homepage: string
  /** 插件注册的 uploader 名（如 `githubPlus`）—— **picgo 侧标识，原样** */
  Uploader: string
  Transformer: string
  Enabled: boolean
  /** 含 `guiMenu` / `commands`（Electron 专属），Web 端**无法执行** */
  GuiOnly: boolean
  InstalledAt: number
}

/** `GET /plugins` 响应 */
export interface PluginListResponse {
  Items: Plugin[]
  PendingJobs: Job[]
}

/** `GET /plugins/search` 的单项 */
export interface PluginSearchItem {
  Name: string
  Version: string
  Description: string
  Author: string
  Homepage: string
  Installed: boolean
}

/** `GET /plugins/{Name}/readme` 响应（Markdown 原文，**前端必须净化后渲染**） */
export interface PluginReadmeResponse {
  Name: string
  Content: string
  Truncated: boolean
}

/** 插件异步操作响应（安装/卸载/更新一律异步） */
export interface PluginJobResponse {
  JobUID: string
}

// ---------------------------------------------------------------------------
// 任务与事件 Jobs / Events（API.md §8）
// ---------------------------------------------------------------------------

export type JobStatus = 'queued' | 'running' | 'succeeded' | 'failed'

/**
 * 批次任务。**状态只有 4 个，没有 `partial`**（D37）：
 * 只要有 item 失败 → `Status = "failed"`，但成功项的结果照样在 `Result` 中回传。
 */
export interface Job {
  UID: string
  Kind: 'upload' | 'plugin.install' | 'plugin.uninstall' | 'plugin.update' | 'config.sync' | string
  Status: JobStatus
  /** 0..100 */
  Progress: number
  UserUID: string
  /** 上传类任务（D38 一批一驱动） */
  StorageUID: string
  TotalItems: number
  SucceededItems: number
  FailedItems: number
  /** D66 取消去重后恒为 0（保留字段，D77） */
  SkippedItems: number
  /** 请求上下文（密钥已脱敏） */
  Payload: Record<string, unknown>
  Result: Record<string, unknown> | null
  Error: string
  CreatedAt: number
  StartedAt: number
  FinishedAt: number

  // ---- 详情响应附带 ----
  Items?: JobItem[]
}

/** 批次内子项。身份由「父 job 的 UID + Seq」构成，**不单独设 UID** */
export interface JobItem {
  Seq: number
  UploadUID: string
  FileName: string
  Status: JobStatus
  Attempts: number
  Error: string
  StartedAt: number
  FinishedAt: number
}

export interface JobListQuery extends PageQuery {
  Kind?: string
  Status?: JobStatus
  Scope?: 'mine' | 'all'
}

/** `GET /jobs/{Uid}/logs` 响应（增量拉取） */
export interface JobLogsResponse {
  Items: JobLog[]
  HasMore: boolean
  LastSeq: number
}

export interface JobLog {
  Seq: number
  Line: string
  CreatedAt: number
}

/** SSE `upload.progress` / `upload.finished` / `upload.failed` 的事件体 */
export interface UploadProgressEvent {
  JobUID: string
  UploadUID?: string
  Seq?: number
  FileName?: string
  Progress?: number
}

export interface UploadFinishedEvent {
  JobUID: string
  UploadUID?: string
  Seq?: number
  FileName?: string
  URL?: string
  ThumbURL?: string
}

export interface UploadFailedEvent {
  JobUID: string
  UploadUID?: string
  Seq?: number
  FileName?: string
  Error?: string
  Attempts?: number
}

/** SSE `job.finished` 的事件体 */
export interface JobFinishedEvent {
  JobUID: string
  Kind?: string
  Status?: JobStatus
  Progress?: number
  TotalItems?: number
  SucceededItems?: number
  FailedItems?: number
  SkippedItems?: number
}

/** SSE `system.notice` 的事件体 */
export interface SystemNoticeEvent {
  Level?: 'info' | 'warn' | 'error'
  Message?: string
}

// ---------------------------------------------------------------------------
// 操作日志 Operation Logs（API.md §9，全部 admin）
// ---------------------------------------------------------------------------

export type LogStatus = 'success' | 'failed'

/**
 * 统一操作日志（D45）。
 *
 * ⚠️ `Type` 是**小写点分字符串枚举，原样保留**（D81 例外）——
 * 它们是历史数据与日志检索的键，改了会让旧记录搜不到。
 */
export interface OperationLog {
  UID: string
  /** `upload` | `mail.send` | `user.create` | …（原样） */
  Type: string
  Status: LogStatus
  /** 操作者；系统操作为 `""` */
  UserUID: string
  /** 冗余，便于展示与搜索 */
  Username: string
  /** `upload` | `user` | `storage` | `plugin` | `setting` | `""` */
  TargetType: string
  TargetUID: string
  /** JSON 上下文（密钥已脱敏）；可能为 null */
  Detail: Record<string, unknown> | null
  /** 失败原因（失败时必填） */
  Error: string
  ClientIP: string
  UserAgent: string
  CreatedAt: number
}

/** `GET /logs` 的查询参数 */
export interface LogListQuery extends PageQuery {
  /** 可重复传多值 = OR */
  Type?: string | string[]
  Status?: LogStatus
  /** 匹配 Username / TargetUID / Detail / Error */
  Keyword?: string
  UserUID?: string
  TargetType?: string
  TargetUID?: string
  /** Unix 秒 */
  From?: number
  To?: number
  Sort?: string
  Order?: 'asc' | 'desc'
}

/** `GET /logs/types` 的单项 */
export interface LogTypeItem {
  /** Stable backend/database identifier; the UI translates it through i18n. */
  Type: string
  TargetType: string
}

/**
 * 邮件发送日志（**不保存邮件正文**，D29）。
 * 日志只记录「发给谁 / 主题 / 模板 / 结果」。
 */
export interface EmailLog {
  UID: string
  ToAddress: string
  Subject: string
  /** `invite` | `reset_password` | …（小写，原样） */
  Template: string
  Status: LogStatus
  Error: string
  RelatedUserUID: string
  CreatedAt: number
}

export interface EmailLogListQuery extends PageQuery {
  ToAddress?: string
  Template?: string
  Status?: LogStatus
  From?: number
  To?: number
}

// ---------------------------------------------------------------------------
// 主题 Themes（API.md §10，admin）
// ---------------------------------------------------------------------------

/** `GET /themes` 的单项（**扫描文件系统**得到，D94：不建表） */
export interface ThemeListItem {
  ID: string
  Name: string
  Version: string
  Description: string
  Author: string
  Tags: string[]
  Repo: string
  MinAppVersion: string
  /** ★ 该主题接管的路由前缀（D94.2） */
  Pages: string[]
  IsActive: boolean
  /** 随镜像发布（default）；不可卸载 */
  IsBuiltin: boolean
  /** 当前启用中或内置 → false */
  CanUninstall: boolean
  /** manifest 的配置项数量 */
  SettingCount: number
  ScreenshotURL: string
  Valid: boolean
  /** `Valid=false` 时说明原因 */
  Error: string
}

export interface ThemeListResponse {
  Active: string
  Items: ThemeListItem[]
  ScannedAt: number
}

/** `POST /themes/install` 响应 */
export interface ThemeInstallResponse {
  ID: string
  Name: string
  Version: string
  Installed: boolean
}

/** `PUT /themes/active` 响应 */
export interface ThemeActivateResponse {
  Active: string
  Previous: string
}

/** 主题配置项 schema（来自 manifest 的 `Configuration.Items`，D98） */
export interface ThemeConfigItem {
  /** ⚠️ 配置键名，**原样**（如 `BackgroundURL`） */
  Key: string
  /** 展示名（多语言已由服务端按 `Accept-Language` 解析成单串，前端直接展示） */
  Name: string
  Type: string
  Required?: boolean
  Default?: unknown
  /** `select` 类型：**逗号分隔的字符串** */
  Options?: string
  Help?: string
  /** `json` 类型且有 `ItemSchema` 时用 repeater 渲染 */
  ItemSchema?: ThemeConfigItem[]
}

/** 单个主题设置项的「值 + 默认值 + 来源」 */
export interface ThemeSettingValue {
  Value: unknown
  Default: unknown
  /** `db`（来自数据库）/ `default`（用 manifest 默认值） */
  Source: 'db' | 'default'
  /** 仅 `password` 类型：是否已设置（不泄露值） */
  HasValue?: boolean
}

/** `GET /themes/{ThemeID}/settings` 响应 */
export interface ThemeSettingsResponse {
  ThemeID: string
  Name: string
  /** 该主题的 `Pages`（影响面提示用） */
  Pages?: string[]
  Schema: ThemeConfigItem[]
  Values: Record<string, ThemeSettingValue>
}

/** `PUT /themes/{ThemeID}/settings` 请求体 */
export interface UpdateThemeSettingsRequest {
  Values: Record<string, unknown>
}

// ---------------------------------------------------------------------------
// 站点公开信息（API.md §10，`GET /site/config`，**无需登录**）
// ---------------------------------------------------------------------------

/** 站点公开信息与当前主题（`SiteConfigResponse` 已在上面定义） */
export type SiteConfig = SiteConfigResponse

// ---------------------------------------------------------------------------
// 系统统计（API.md §10）
// ---------------------------------------------------------------------------

export interface SystemStats {
  /** `mine`（普通用户）/ `all`（管理员）—— 后端按角色决定统计范围 */
  Scope?: 'mine' | 'all'
  /** ⚠️ **仅管理员**返回（普通用户的响应里没有这个字段） */
  Users?: { Total: number; Active: number; Disabled: number; Admins: number }
  Uploads: {
    Total: number
    TotalSize: number
    TodayCount: number
    PendingCount: number
    FailedCount: number
  }
  Jobs: { Running: number; Queued: number }
  /** 最近 30 天，后端已按**本地日**分组并补齐空缺日期 */
  Trend: { Date: string; Count: number; Size: number }[]
  /** ⚠️ **仅管理员**返回 */
  ByStorage?: { StorageUID: string; Name: string; Count: number }[]
}

// ---------------------------------------------------------------------------
// 系统设置（API.md §11，admin）
// ---------------------------------------------------------------------------

/**
 * 系统设置项的元信息 + 当前值。
 *
 * ⚠️ `Key` 是**配置键名，`dot.lowerCamel`，原样**（D81.3 第 3 条）——
 * 它们是 KV 表的字符串 key，不是列名。
 */
export interface SystemSettingItem {
  Key: string
  Category: string
  Type: 'string' | 'int' | 'bool' | 'json' | 'secret'
  /** `secret` 类型**恒为掩码** */
  Value: unknown
  /** 仅 `secret` 类：是否已设置（不泄露值） */
  HasValue?: boolean
  Default: unknown
  /** `db`（来自数据库）/ `default`（用默认值） */
  Source: 'db' | 'default'
  Secret: boolean
  Label: string
  Description: string
  RequiresRestart: boolean
}

export interface SystemSettingGroup {
  Category: string
  Label: string
  Keys: SystemSettingItem[]
}

/** `GET /settings/system` 响应 */
export interface SystemSettingsResponse {
  Groups: SystemSettingGroup[]
  Meta: { SchemaKeyCount: number; DBKeyCount: number; UpdatedAt: number }
}

/** `PUT /settings/system` 响应 */
export interface UpdateSystemSettingsResponse {
  Applied: string[]
  Effects: string[]
}

/** `POST /settings/mail/test` 请求体（admin，站点设置 → 邮件） */
export interface MailTestRequest {
  To: string
}

/** `POST /settings/mail/test` 响应 */
export interface MailTestResponse {
  Ok: boolean
  Message: string
  MessageID?: string
}

// ---------------------------------------------------------------------------
// Lsky 兼容层（API.md §12）—— **保持 snake_case**，不受 D81 影响
// ---------------------------------------------------------------------------

export interface LskyEnvelope<T> {
  status: boolean | string
  message: string
  data: T
}

export interface LskyToken {
  token: string
}

export interface LskyUploadResult {
  key: string
  name: string
  pathname: string
  url: string
  links: {
    url: string
    html: string
    bbcode: string
    markdown: string
    markdown_with_link: string
    thumbnail_url: string
  }
}
