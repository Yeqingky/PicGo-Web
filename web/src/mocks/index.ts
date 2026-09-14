import type { AxiosAdapter, AxiosRequestConfig, AxiosResponse, InternalAxiosRequestConfig } from 'axios'

import type { Envelope, PageData, User } from '@/types/api'
import { ApiCode } from '@/types/api'

/**
 * Mock 适配器（`VITE_USE_MOCK=true` 时启用）。
 *
 * 目的：让 W8（前端页面）在后端接口尚未全部就绪时也能独立开发。
 *
 * 实现方式：**替换 axios 的 adapter**，而不是起一个 mock server ——
 * 这样拦截器（拆信封、静默刷新）走真实代码路径，mock 与真实后端的行为一致。
 *
 * ⚠️ 只用于开发；`import.meta.env.PROD` 下 `installMockAdapter` 不会做任何事。
 */

/** 是否启用 mock。 */
export function isMockEnabled(): boolean {
  return import.meta.env.DEV && import.meta.env.VITE_USE_MOCK === 'true'
}

// ---------------------------------------------------------------------------
// 假数据
// ---------------------------------------------------------------------------

const ADMIN: User = {
  UID: 'usr_mock_admin',
  Email: 'admin@localhost',
  Role: 'admin',
  Status: 'active',
  MustChangePassword: false,
  Nickname: '管理员',
  AvatarURL: '',
  Homepage: '',
  CapacityBytes: 5 * 1024 ** 3,
  UsedBytes: 1_288_490_188,
  ImageCount: 128,
  AlbumCount: 4,
  HasPassword: true,
  LastLoginAt: Math.floor(Date.now() / 1000) - 3600,
  CreatedAt: Math.floor(Date.now() / 1000) - 86400 * 30,
}

const MEMBER: User = {
  UID: 'usr_mock_member',
  Email: 'user@example.com',
  Role: 'user',
  Status: 'active',
  MustChangePassword: false,
  Nickname: '小明',
  AvatarURL: '',
  Homepage: '',
  // 0 = 不限额（D20）
  CapacityBytes: 0,
  UsedBytes: 20_971_520,
  ImageCount: 12,
  AlbumCount: 1,
  HasPassword: true,
  LastLoginAt: Math.floor(Date.now() / 1000) - 7200,
  CreatedAt: Math.floor(Date.now() / 1000) - 86400 * 7,
}

const USERS: User[] = [
  ADMIN,
  MEMBER,
  {
    UID: 'usr_mock_disabled',
    Email: 'disabled@example.com',
    Role: 'user',
    Status: 'disabled',
    MustChangePassword: true,
    Nickname: '',
    AvatarURL: '',
    Homepage: '',
    CapacityBytes: 1024 ** 3,
    UsedBytes: 0,
    ImageCount: 0,
    AlbumCount: 0,
    HasPassword: true,
    LastLoginAt: 0,
    CreatedAt: Math.floor(Date.now() / 1000) - 86400 * 3,
  },
]

/**
 * mock 的登录态。
 *
 * ⚠️ 用 `sessionStorage` 持久化：真实后端用 httpOnly Cookie，**整页刷新后仍保持登录**；
 *    如果 mock 只放在内存里，开发时每刷新一次就被踢回登录页，体验与真实环境不一致
 *    （已由浏览器验证发现）。
 */
const MOCK_SESSION_KEY = 'picgo-web.mock.session'

function readSession(): User | null {
  try {
    const raw = sessionStorage.getItem(MOCK_SESSION_KEY)
    if (!raw) return null
    return JSON.parse(raw) as User
  } catch {
    return null
  }
}

function writeSession(user: User | null): void {
  try {
    if (user) sessionStorage.setItem(MOCK_SESSION_KEY, JSON.stringify(user))
    else sessionStorage.removeItem(MOCK_SESSION_KEY)
  } catch {
    /* sessionStorage 不可用时退化为内存态 */
  }
}

let currentUser: User | null = readSession()

// ---------------------------------------------------------------------------
// 响应助手
// ---------------------------------------------------------------------------

function envelope<T>(code: number, message: string, data: T): Envelope<T> {
  return { Code: code, Message: message, Data: data }
}

function respond<T>(
  config: AxiosRequestConfig,
  status: number,
  body: Envelope<T>,
): AxiosResponse<Envelope<T>> {
  return {
    data: body,
    status,
    statusText: String(status),
    headers: {},
    config: config as InternalAxiosRequestConfig,
  }
}

/** 把路径归一化：去掉 baseURL 前缀与查询串。 */
function normalizePath(config: AxiosRequestConfig): string {
  const raw = config.url ?? ''
  const base = config.baseURL ?? ''
  let path = raw.startsWith(base) ? raw.slice(base.length) : raw
  const queryIndex = path.indexOf('?')
  if (queryIndex >= 0) path = path.slice(0, queryIndex)
  if (!path.startsWith('/')) path = `/${path}`
  return path
}

function parseBody<T>(config: AxiosRequestConfig): T | undefined {
  if (typeof config.data !== 'string') {
    return config.data as T | undefined
  }
  try {
    return JSON.parse(config.data) as T
  } catch {
    return undefined
  }
}

function parseQuery(config: AxiosRequestConfig): URLSearchParams {
  const raw = config.url ?? ''
  const queryIndex = raw.indexOf('?')
  return new URLSearchParams(queryIndex >= 0 ? raw.slice(queryIndex + 1) : '')
}

// ---------------------------------------------------------------------------
// 路由表
// ---------------------------------------------------------------------------

const now = () => Math.floor(Date.now() / 1000)

/** 处理一次 mock 请求；返回 null 表示「未匹配」（交由真实 adapter 处理）。 */
function handle(config: AxiosRequestConfig): AxiosResponse<Envelope<unknown>> | null {
  const method = (config.method ?? 'get').toLowerCase()
  const path = normalizePath(config)

  // ---- auth ----
  if (method === 'post' && path === '/auth/login') {
    const body = parseBody<{ Email?: string; Password?: string }>(config)
    const email = body?.Email?.trim() ?? ''
    const password = body?.Password ?? ''

    if (password !== 'adminpassword' && password !== 'userpassword') {
      return respond(config, 401, envelope(ApiCode.BadCredentials, '邮箱或密码错误', null))
    }

    currentUser = password === 'adminpassword' ? ADMIN : MEMBER
    if (email && currentUser.Email !== email) {
      // 允许任意邮箱登录（mock 的便利性），但保留一个已登录用户
      currentUser = { ...currentUser, Email: email }
    }
    writeSession(currentUser)

    return respond(
      config,
      200,
      envelope(ApiCode.OK, 'ok', {
        AccessToken: 'mock-access-token',
        ExpiresIn: 900,
        User: currentUser,
      }),
    )
  }

  if (method === 'post' && path === '/auth/refresh') {
    if (!currentUser) {
      return respond(config, 401, envelope(ApiCode.Unauthorized, '未登录或令牌无效', null))
    }
    return respond(
      config,
      200,
      envelope(ApiCode.OK, 'ok', { AccessToken: 'mock-access-token', ExpiresIn: 900 }),
    )
  }

  if (method === 'post' && path === '/auth/logout') {
    currentUser = null
    writeSession(null)
    return respond(config, 200, envelope(ApiCode.OK, 'ok', null))
  }

  if (method === 'get' && path === '/auth/me') {
    if (!currentUser) {
      return respond(config, 401, envelope(ApiCode.Unauthorized, '未登录或令牌无效', null))
    }
    return respond(config, 200, envelope(ApiCode.OK, 'ok', currentUser))
  }

  if (method === 'patch' && path === '/auth/password') {
    if (!currentUser) {
      return respond(config, 401, envelope(ApiCode.Unauthorized, '未登录或令牌无效', null))
    }
    currentUser = { ...currentUser, MustChangePassword: false }
    writeSession(currentUser)
    return respond(config, 200, envelope(ApiCode.OK, 'ok', null))
  }

  if (method === 'get' && path === '/auth/identities') {
    return respond(
      config,
      200,
      envelope(ApiCode.OK, 'ok', {
        HasPassword: true,
        Identities: [
          {
            Provider: 'github',
            ProviderLogin: 'octocat',
            ProviderEmail: 'octocat@example.com',
            AvatarURL: '',
            BoundAt: now() - 86400,
          },
        ],
      }),
    )
  }

  if (method === 'get' && path === '/auth/oauth/providers') {
    // mock 默认「未启用 OAuth」→ 登录页不显示 GitHub 按钮（便于验证 D26 的分支）
    return respond(config, 200, envelope(ApiCode.OK, 'ok', { Providers: [] }))
  }

  // ---- users（admin）----
  if (method === 'get' && path === '/users') {
    const query = parseQuery(config)
    const page = Number(query.get('Page') ?? '1') || 1
    const pageSize = Number(query.get('PageSize') ?? '20') || 20
    const keyword = (query.get('Keyword') ?? '').trim().toLowerCase()

    const filtered = keyword
      ? USERS.filter(
          (u) =>
            u.Email.toLowerCase().includes(keyword) ||
            (u.Nickname ?? '').toLowerCase().includes(keyword),
        )
      : USERS

    const start = (page - 1) * pageSize
    const pageData: PageData<User> = {
      Items: filtered.slice(start, start + pageSize),
      Total: filtered.length,
      Page: page,
      PageSize: pageSize,
    }
    return respond(config, 200, envelope(ApiCode.OK, 'ok', pageData))
  }

  // ---- settings ----
  if (method === 'get' && path === '/settings') {
    return respond(
      config,
      200,
      envelope(ApiCode.OK, 'ok', {
        Site: {
          name: 'PicGo Web（mock）',
          subtitle: '自建图床控制台',
          description: '这是 mock 模式下的站点描述',
          notice: '',
          icp: '',
          baseUrl: 'http://127.0.0.1:5173',
          iconUrl: '',
          allowSelfRegistration: false,
        },
        User: { 'ui.theme': 'system' },
        Upload: {
          maxSizeBytes: 20 * 1024 ** 2,
          allowedExts: ['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'svg', 'ico', 'avif'],
          blockSvg: false,
        },
        Features: { oauthGithubEnabled: false, mailEnabled: false },
      }),
    )
  }

  if (method === 'put' && path === '/settings') {
    const body = parseBody<Record<string, unknown>>(config) ?? {}
    return respond(
      config,
      200,
      envelope(ApiCode.OK, 'ok', { Applied: Object.keys(body), Ignored: [] }),
    )
  }

  // ---- settings: api tokens ----
  if (method === 'get' && path === '/settings/api-tokens') {
    return respond(
      config,
      200,
      envelope(ApiCode.OK, 'ok', [
        {
          UID: 'apt_mock_1',
          Name: 'CLI',
          Prefix: 'pcw_1a2b3c4d',
          LastUsedAt: now() - 600,
          ExpiresAt: 0,
          CreatedAt: now() - 86400 * 10,
          Expired: false,
        },
      ]),
    )
  }

  // ---- site config（公开）----
  if (method === 'get' && path === '/site/config') {
    return respond(
      config,
      200,
      envelope(ApiCode.OK, 'ok', {
        Site: {
          Name: 'PicGo Web（mock）',
          Subtitle: '自建图床控制台',
          Description: '这是 mock 模式下的站点描述',
          Keywords: '',
          IconURL: '',
          Notice: '',
          Icp: '',
          BaseURL: 'http://127.0.0.1:5173',
          Version: '0.1.0',
        },
        Theme: {
          ID: 'default',
          Name: '默认主题（mock）',
          Version: '1.0.0',
          Pages: ['/'],
          AssetBase: '/theme-assets',
          Settings: { BackgroundURL: '' },
        },
        Features: { oauthGithubEnabled: false, mailEnabled: false },
      }),
    )
  }

  // ---- system ----
  if (method === 'get' && path === '/system/info') {
    return respond(
      config,
      200,
      envelope(ApiCode.OK, 'ok', {
        Version: '0.1.0-mock',
        SchemaVersion: 1,
        DatabaseDriver: 'sqlite',
        SiteName: 'PicGo Web（mock）',
        ThemeActive: 'default',
        Uptime: 42,
      }),
    )
  }

  return null
}

// ---------------------------------------------------------------------------
// 安装
// ---------------------------------------------------------------------------

/** 构造一个 mock adapter；未匹配的请求**回落到 fallback**（通常是 axios 的原生 adapter）。 */
export function createMockAdapter(fallback?: AxiosAdapter | string): AxiosAdapter {
  return async (requestConfig: InternalAxiosRequestConfig) => {
    const mocked = handle(requestConfig)
    if (mocked) {
      // 模拟一点网络延迟，便于观察 loading 态
      await new Promise((resolve) => setTimeout(resolve, 180))
      return mocked
    }

    if (fallback && typeof fallback !== 'string') {
      return fallback(requestConfig)
    }

    // 没有 fallback（极端情况）：返回一个明确的 501，避免静默挂住
    return respond(
      requestConfig,
      501,
      envelope(
        ApiCode.Internal,
        `[mock] 未覆盖的请求：${requestConfig.method?.toUpperCase()} ${requestConfig.url}`,
        null,
      ),
    )
  }
}

/**
 * 把 mock adapter 装到 axios 实例上（只在 `VITE_USE_MOCK=true` 且 DEV 时生效）。
 *
 * 未匹配的请求会回落到原本的 adapter，因此可以「后端就绪一部分、mock 补一部分」。
 */
export function installMockAdapter(instance: {
  defaults: { adapter?: AxiosAdapter | string | Array<AxiosAdapter | string> }
}): void {
  if (!isMockEnabled()) return

  const current = instance.defaults.adapter
  const fallback = Array.isArray(current) ? current[0] : current
  instance.defaults.adapter = createMockAdapter(fallback)
}
