import { del, get, patch, post, put } from '@/lib/http'
import type {
  APIToken,
  CreateAPITokenRequest,
  CreateAPITokenResponse,
  IdentitiesResponse,
  LoginResponse,
  OAuthBindResponse,
  OAuthProvidersResponse,
  RefreshResponse,
  SettingsResponse,
  SiteConfigResponse,
  SystemInfo,
  UpdateUserSettingsResponse,
  User,
} from '@/types/api'

/**
 * 按域封装的接口调用（DESIGN.md §14.1）。
 *
 * ⚠️ 请求函数一律写在这里，**不要散落在组件里**。
 */

// ---------------------------------------------------------------------------
// auth（API.md §1）
// ---------------------------------------------------------------------------

export const authApi = {
  /**
   * 邮箱 + 密码登录（D23：只认邮箱）。成功后 Cookie 由后端下发。
   *
   * `Remember` 为登录页「记住我」的意图：
   * 会话时长由 `settings:security.sessionTtlHours` 决定，后端目前**不区分**该字段；
   * 仍显式传递，避免 UI 开关变成静默 no-op（将来后端支持时无需改前端）。
   */
  login(email: string, password: string, remember = true): Promise<LoginResponse> {
    return post<LoginResponse>(
      '/auth/login',
      { Email: email, Password: password, Remember: remember },
      // 登录失败不应触发静默刷新
      { skipAuthRetry: true },
    )
  },

  /** 主动刷新登录态（Cookie `pcw_rt`）。 */
  refresh(): Promise<RefreshResponse> {
    return post<RefreshResponse>('/auth/refresh', undefined, { skipAuthRetry: true })
  },

  logout(): Promise<null> {
    return post<null>('/auth/logout', undefined, { skipAuthRetry: true })
  },

  /** 当前用户（未登录返回 40102）。 */
  me(): Promise<User> {
    return get<User>('/auth/me')
  },

  /** 修改密码。首次强制改密时 oldPassword 可省略。 */
  changePassword(oldPassword: string, newPassword: string): Promise<null> {
    return patch<null>('/auth/password', { OldPassword: oldPassword, NewPassword: newPassword })
  },

  /** 已绑定的第三方身份。 */
  identities(): Promise<IdentitiesResponse> {
    return get<IdentitiesResponse>('/auth/identities')
  },

  /** 已启用的 OAuth provider（未配置则返回空数组，D26）。 */
  oauthProviders(): Promise<OAuthProvidersResponse> {
    return get<OAuthProvidersResponse>('/auth/oauth/providers', { skipAuthRetry: true })
  },

  /**
   * 取 GitHub 绑定的授权 URL（需登录）。
   * 前端拿到后自行 `window.location.assign(AuthorizeURL)`。
   */
  bindOAuth(provider: string, redirect?: string): Promise<OAuthBindResponse> {
    const query = redirect ? `?Redirect=${encodeURIComponent(redirect)}` : ''
    return post<OAuthBindResponse>(`/auth/oauth/${provider}/bind${query}`)
  },

  /** 解绑（若用户无密码且无其他身份，后端会拒绝，防锁死账号）。 */
  unbindOAuth(provider: string): Promise<null> {
    return del<null>(`/auth/oauth/${provider}`)
  },
}

// ---------------------------------------------------------------------------
// 用户管理（API.md §2，全部需要 admin）
// ---------------------------------------------------------------------------

export interface UserListParams {
  Page?: number
  PageSize?: number
  Keyword?: string
  Role?: string
  Status?: string
  Sort?: string
  Order?: 'asc' | 'desc'
}

export const usersApi = {
  list(params: UserListParams = {}) {
    return get<{ Items: User[]; Total: number; Page: number; PageSize: number }>('/users', {
      params,
    })
  },

  get(uid: string): Promise<User> {
    return get<User>(`/users/${encodeURIComponent(uid)}`)
  },

  create(payload: {
    Email: string
    Password: string
    Role?: string
    Nickname?: string
    CapacityBytes?: number
    SendInviteEmail?: boolean
  }): Promise<User> {
    return post<User>('/users', payload)
  },

  update(uid: string, payload: Record<string, unknown>): Promise<User> {
    return patch<User>(`/users/${encodeURIComponent(uid)}`, payload)
  },

  remove(uid: string) {
    return del<{ DeletedUploads: number; FreedBytes: number; RemoteDeleteFailed: number }>(
      `/users/${encodeURIComponent(uid)}`,
    )
  },

  /** 管理员重置他人密码：明文只返回一次，并置 MustChangePassword。 */
  resetPassword(uid: string) {
    return post<{ Password: string }>(`/users/${encodeURIComponent(uid)}/reset-password`)
  },
}

// ---------------------------------------------------------------------------
// API Token（API.md §11，需登录）
// ---------------------------------------------------------------------------

export const apiTokensApi = {
  list(): Promise<APIToken[]> {
    return get<APIToken[]>('/settings/api-tokens')
  },

  /** 明文 `Token` 只在创建响应里返回一次（D31）。 */
  create(payload: CreateAPITokenRequest): Promise<CreateAPITokenResponse> {
    return post<CreateAPITokenResponse>('/settings/api-tokens', payload)
  },

  revoke(uid: string): Promise<null> {
    return del<null>(`/settings/api-tokens/${encodeURIComponent(uid)}`)
  },
}

// ---------------------------------------------------------------------------
// 设置（API.md §11）
// ---------------------------------------------------------------------------

export const settingsApi = {
  /** 站点公开信息 + 用户级设置。 */
  get(): Promise<SettingsResponse> {
    return get<SettingsResponse>('/settings')
  },

  /** 更新用户级设置（落 UserSettings 表）。 */
  updateUser(payload: Record<string, unknown>): Promise<UpdateUserSettingsResponse> {
    return put<UpdateUserSettingsResponse>('/settings', payload)
  },

  /** 全部系统键位 + 当前生效值（admin）。 */
  getSystem(): Promise<Record<string, unknown>> {
    return get<Record<string, unknown>>('/settings/system')
  },

  updateSystem(payload: Record<string, unknown>): Promise<{ Updated: number }> {
    return put<{ Updated: number }>('/settings/system', payload)
  },
}

// ---------------------------------------------------------------------------
// 站点与主题公开信息（API.md §10，**无需登录**）
// ---------------------------------------------------------------------------

export const siteApi = {
  /**
   * 首屏调用一次：站点信息 + 当前主题的元数据与设置（D83/D94/D95）。
   *
   * 首页（主题）与内置 SPA 都可能用到；**不返回任何敏感信息**。
   */
  config(): Promise<SiteConfigResponse> {
    return get<SiteConfigResponse>('/site/config', { skipAuthRetry: true })
  },
}

// ---------------------------------------------------------------------------
// 系统（API.md §10）
// ---------------------------------------------------------------------------

export const systemApi = {
  info(): Promise<SystemInfo> {
    return get<SystemInfo>('/system/info')
  },
}

// ---------------------------------------------------------------------------
// 聚合导出，便于按域引用
// ---------------------------------------------------------------------------

export const api = {
  auth: authApi,
  users: usersApi,
  apiTokens: apiTokensApi,
  settings: settingsApi,
  site: siteApi,
  system: systemApi,
}
