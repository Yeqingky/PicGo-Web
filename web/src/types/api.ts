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
  AlbumCount?: number
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
