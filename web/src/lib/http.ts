import axios, {
  type AxiosError,
  type AxiosInstance,
  type AxiosRequestConfig,
  type AxiosResponse,
  type InternalAxiosRequestConfig,
} from 'axios'

import { ApiCode, ApiError, type Envelope } from '@/types/api'

/**
 * 与后端的 HTTP 客户端（DESIGN.md §9 / API.md §14）。
 *
 * 约定：
 *  - `baseURL = "/api/web/v1"`（内部 API；Lsky 兼容层 `/api/v1` 供第三方客户端使用，前端不用）
 *  - 鉴权走 **httpOnly Cookie**（`pcw_at` / `pcw_rt`），因此 `withCredentials: true`
 *  - 响应体是统一信封 `{Code, Message, Data}`：
 *      - `Code === 0` → **resolve `Data`**（调用方直接拿业务数据，不感知信封）
 *      - `Code !== 0` → reject `ApiError(Code, Message)`
 *  - `40102` / `40103` → **尝试一次静默刷新**并重放原请求；失败则触发登出回调
 */

export const API_BASE_URL = '/api/web/v1'

/** 刷新接口的路径（要与后端一致，不能带 baseURL 前缀重复拼接）。 */
const REFRESH_PATH = '/auth/refresh'

/**
 * 认证失效时的回调（由 auth store 注册）。
 *
 * 用回调而非直接 import store，避免 `http → store → http` 的循环依赖。
 */
type AuthFailureHandler = () => void
let authFailureHandler: AuthFailureHandler | null = null

/** 注册「刷新失败 / 需要重新登录」时的回调。 */
export function setAuthFailureHandler(handler: AuthFailureHandler | null): void {
  authFailureHandler = handler
}

/** 业务请求的配置：允许声明「此请求不需要静默刷新重试」。 */
export interface RequestOptions extends AxiosRequestConfig {
  /** 为真时不触发静默刷新（用于刷新接口本身、登录接口等） */
  skipAuthRetry?: boolean
}

interface RetriableConfig extends InternalAxiosRequestConfig {
  skipAuthRetry?: boolean
  /** 已重试过标记，防止无限循环 */
  _retried?: boolean
}

// ---------------------------------------------------------------------------
// 实例
// ---------------------------------------------------------------------------

export const http: AxiosInstance = axios.create({
  baseURL: API_BASE_URL,
  timeout: 30_000,
  withCredentials: true,
  headers: {
    // 后端按 Accept 判断返回 JSON；这里显式声明
    Accept: 'application/json',
  },
})

// ---------------------------------------------------------------------------
// 响应拦截：拆信封 + 抛 ApiError + 静默刷新
// ---------------------------------------------------------------------------

/** 判断响应体是否是我们约定的信封。 */
function isEnvelope(body: unknown): body is Envelope<unknown> {
  return (
    typeof body === 'object' &&
    body !== null &&
    'Code' in body &&
    'Message' in body &&
    typeof (body as Envelope<unknown>).Code === 'number'
  )
}

/** 把 axios 的错误归一化成 ApiError。 */
function normalizeError(error: AxiosError): ApiError {
  const response = error.response
  const body = response?.data

  if (isEnvelope(body)) {
    return new ApiError(body.Code, body.Message, response?.status ?? 0, body.Data)
  }

  if (response) {
    const status = response.status

    // 非信封响应（例如反向代理/开发代理直接返回的 502/500 纯文本）。
    // 这类响应体不是给用户看的（可能是 "Internal Server Error"），
    // 因此给一句可读的中文，并把 HTTP 状态附上供排查（消息会直接展示在 UI）。
    if (status >= 500) {
      return new ApiError(ApiCode.Internal, `服务暂时不可用（HTTP ${status}）`, status)
    }
    if (status === 401) {
      return new ApiError(ApiCode.Unauthorized, '登录已失效，请重新登录', status)
    }
    if (status === 404) {
      return new ApiError(ApiCode.NotFound, '接口不存在（前端与后端版本可能不一致）', status)
    }
    return new ApiError(
      ApiCode.InvalidParam,
      `请求失败（HTTP ${status}）`,
      status,
    )
  }

  if (error.code === 'ECONNABORTED' || error.code === 'ETIMEDOUT') {
    return new ApiError(-1, '请求超时，请检查网络或稍后重试')
  }

  return new ApiError(-1, '网络异常，请检查服务是否已启动')
}

/**
 * 单飞（single-flight）刷新：并发的多个 401 只触发一次 `/auth/refresh`。
 */
let refreshPromise: Promise<void> | null = null

async function refreshOnce(): Promise<void> {
  if (!refreshPromise) {
    refreshPromise = (async () => {
      // 用独立实例，避免被下面的拦截器再次拦截而递归
      const res = await axios.post<Envelope<unknown>>(
        `${API_BASE_URL}${REFRESH_PATH}`,
        {},
        { withCredentials: true, timeout: 15_000 },
      )
      const body = res.data
      if (!isEnvelope(body) || body.Code !== ApiCode.OK) {
        throw new ApiError(body?.Code ?? ApiCode.Unauthorized, body?.Message ?? '刷新登录态失败')
      }
    })().finally(() => {
      refreshPromise = null
    })
  }
  return refreshPromise
}

/** 触发认证失效处理（清登录态 + 跳登录页）。 */
function notifyAuthFailure(): void {
  authFailureHandler?.()
}

/** 处理统一信封中的业务错误，兼容 HTTP 200 与 HTTP 4xx/5xx 响应。 */
function handleApiError(
  config: RetriableConfig | undefined,
  error: ApiError,
): Promise<AxiosResponse> {
  if (config && !config.skipAuthRetry && !config._retried && error.isAuthError) {
    config._retried = true
    return refreshOnce()
      .then(() => http.request(config))
      .catch((refreshError: unknown) => {
        notifyAuthFailure()
        return Promise.reject(
          refreshError instanceof ApiError ? refreshError : error,
        )
      })
  }

  if (error.isAuthError || error.Code === ApiCode.AccountDisabled) {
    notifyAuthFailure()
  }
  return Promise.reject(error)
}

http.interceptors.response.use(
  (response: AxiosResponse) => {
    const config = response.config as RetriableConfig
    const body: unknown = response.data

    if (!isEnvelope(body)) {
      // 非信封响应：
      //  - 某些端点（如二进制下载）本就不返回信封，直接透传
      //  - 其它情况视为后端异常，避免调用方拿到奇怪结构
      return response
    }

    if (body.Code === ApiCode.OK) {
      // 拆信封：调用方直接拿到 Data
      response.data = body.Data
      return response
    }

    // 业务失败
    return handleApiError(
      config,
      new ApiError(body.Code, body.Message, response.status, body.Data),
    )
  },
  (error: AxiosError) => {
    const config = error.config as RetriableConfig | undefined
    const response = error.response
    const body = response?.data

    // Axios 默认会把 HTTP 401/4xx 交给 rejected handler；后端仍返回统一信封，
    // 因此这里必须走与 HTTP 200 + Code 非 0 相同的刷新/重放逻辑。
    if (isEnvelope(body)) {
      return handleApiError(
        config,
        new ApiError(body.Code, body.Message, response?.status ?? 0, body.Data),
      )
    }

    const normalized = normalizeError(error)
    if (normalized.isAuthError || normalized.Code === ApiCode.AccountDisabled) {
      return handleApiError(config, normalized)
    }
    return Promise.reject(normalized)
  },
)

// ---------------------------------------------------------------------------
// 便捷方法：直接返回业务数据（已拆信封）
// ---------------------------------------------------------------------------

export async function get<T>(url: string, config?: RequestOptions): Promise<T> {
  const res = await http.get<T>(url, config)
  return res.data
}

export async function post<T>(url: string, body?: unknown, config?: RequestOptions): Promise<T> {
  const res = await http.post<T>(url, body, config)
  return res.data
}

export async function put<T>(url: string, body?: unknown, config?: RequestOptions): Promise<T> {
  const res = await http.put<T>(url, body, config)
  return res.data
}

export async function patch<T>(url: string, body?: unknown, config?: RequestOptions): Promise<T> {
  const res = await http.patch<T>(url, body, config)
  return res.data
}

export async function del<T>(url: string, config?: RequestOptions): Promise<T> {
  const res = await http.delete<T>(url, config)
  return res.data
}

/** 上传（multipart）时用的进度回调类型。 */
export type UploadProgressHandler = (percent: number) => void

/**
 * 主动刷新登录态（供需要「进入页面前确认登录」的场景显式调用）。
 * 失败会抛 ApiError。
 */
export async function refreshSession(): Promise<void> {
  await refreshOnce()
}
