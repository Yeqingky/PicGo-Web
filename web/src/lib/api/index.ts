import { del, get, patch, post } from '@/lib/http'
import type {
  APIToken,
  CreateAPITokenRequest,
  CreateAPITokenResponse,
  IdentitiesResponse,
  LoginResponse,
  OAuthBindResponse,
  OAuthProvidersResponse,
  RefreshResponse,
  SiteConfigResponse,
  User,
} from '@/types/api'

/**
 * 按域拆分的接口调用（DESIGN.md §14.1）。
 *
 * ⚠️ 请求函数一律写在各域文件里，**不要散落在组件里**。
 *
 * | 域 | 文件 | 说明 |
 * |---|---|---|
 * | 认证 / 用户 / API Token | `index.ts`（本文件） | 基础且与其它域无耦合，留在入口 |
 * | 存储 | `storage.ts` | admin |
 * | 图库 / 相册 | `gallery.ts` | |
 * | 任务 | `job.ts` | |
 * | 日志 | `log.ts` | admin |
 * | 插件 | `plugin.ts` | admin |
 * | 主题 | `theme.ts` | admin |
 * | 设置 / 系统 | `setting.ts` | |
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
  /**
   * 列出自己的 API Token。
   *
   * ⚠️ 对**后端当前实现与 `docs/API.md` 的偏离**做容错：
   * 契约未细化该响应的形状，后端当前返回 `{ Items: [...] }`。
   * 这里两种都接受，统一返回数组（归一化集中在这一处）。
   */
  async list(): Promise<APIToken[]> {
    const data = await get<APIToken[] | { Items: APIToken[] }>('/settings/api-tokens')
    if (Array.isArray(data)) return data
    return data?.Items ?? []
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
// 按域重导出（DESIGN.md §14.1）
// ---------------------------------------------------------------------------

export { uploadApi, type UploadFilesInput } from '@/lib/api/gallery'
export { jobApi } from '@/lib/api/job'
export { logApi } from '@/lib/api/log'
export { pluginApi } from '@/lib/api/plugin'
export { settingsApi, systemApi } from '@/lib/api/setting'
export { storageApi, fetchDefaultStorageConfig, type StorageConfigListParams } from '@/lib/api/storage'
export { themeApi } from '@/lib/api/theme'

import { uploadApi } from '@/lib/api/gallery'
import { jobApi } from '@/lib/api/job'
import { logApi } from '@/lib/api/log'
import { pluginApi } from '@/lib/api/plugin'
import { settingsApi, systemApi } from '@/lib/api/setting'
import { storageApi } from '@/lib/api/storage'
import { themeApi } from '@/lib/api/theme'

/** 聚合导出，便于按域引用（也便于 mock 层整体替换）。 */
export const api = {
  auth: authApi,
  users: usersApi,
  apiTokens: apiTokensApi,
  site: siteApi,
  settings: settingsApi,
  system: systemApi,
  storage: storageApi,
  upload: uploadApi,
  job: jobApi,
  log: logApi,
  plugin: pluginApi,
  theme: themeApi,
}
