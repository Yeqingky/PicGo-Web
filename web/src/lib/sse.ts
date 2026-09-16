/**
 * SSE 客户端封装（DESIGN.md §9.4 / API.md §8）。
 *
 * 后端事件集（事件名保持小写点分，**事件体内字段用 PascalCase**）：
 *   - `job.started` / `job.log` / `job.finished`
 *   - `upload.progress` / `upload.finished` / `upload.failed`
 *   - `system.notice`（服务端提示，如优雅重启、agent 重启）
 *   - `ping`（每 25 秒保活）
 *
 * 职责：
 *  - 指数退避重连（1s → 2s → 4s → 8s，上限 30s）
 *  - 重连前用 `/auth/me` 校验会话，让过期的 `pcw_at` 通过 HTTP 客户端使用 `pcw_rt` 刷新
 *  - 按事件名订阅/退订
 *  - 连接状态回调（供侧栏底部显示服务器在线 / 离线状态）
 */

import { get } from '@/lib/http'
import { toApiError } from '@/types/api'

export const SSE_URL = '/api/web/v1/events'

/** 服务端事件名（与后端字符串一致，**不要改大小写**）。 */
export const SSEEvent = {
  JobStarted: 'job.started',
  JobLog: 'job.log',
  JobFinished: 'job.finished',

  UploadProgress: 'upload.progress',
  UploadFinished: 'upload.finished',
  UploadFailed: 'upload.failed',

  SystemNotice: 'system.notice',

  Ping: 'ping',
} as const

export type SSEEventName = (typeof SSEEvent)[keyof typeof SSEEvent]

/** 全部需要监听的事件名（含 ping，用于刷新「最后心跳」）。 */
const ALL_EVENTS: string[] = Object.values(SSEEvent)

export type SSEStatus = 'idle' | 'connecting' | 'open' | 'reconnecting' | 'closed'

export interface SSEStatusChange {
  status: SSEStatus
  /** 重连次数（status 为 reconnecting 时有效） */
  attempt?: number
  /** 下一次重连的等待毫秒数 */
  retryInMs?: number
}

type EventHandler = (payload: unknown, event: string) => void
type StatusHandler = (change: SSEStatusChange) => void

/** 退避上限 30s（DESIGN.md §9.4） */
const MAX_BACKOFF_MS = 30_000
/** 基础退避 1s，按 2^attempt 增长 */
const BASE_BACKOFF_MS = 1_000

/**
 * 一个可复用的 SSE 连接。
 *
 * 用法：
 *   const client = new SSEClient()
 *   client.onStatus(console.log)
 *   const off = client.on(SSEEvent.UploadProgress, (p) => { ... })
 *   client.connect()
 *   // 清理
 *   off(); client.close()
 */
export class SSEClient {
  private source: EventSource | null = null
  private readonly handlers = new Map<string, Set<EventHandler>>()
  private readonly statusHandlers = new Set<StatusHandler>()
  private attempt = 0
  private retryTimer: ReturnType<typeof setTimeout> | null = null
  private closedByUser = false
  private currentStatus: SSEStatus = 'idle'

  /** 当前连接状态。 */
  get status(): SSEStatus {
    return this.currentStatus
  }

  /** 订阅某个事件，返回退订函数。 */
  on(event: string, handler: EventHandler): () => void {
    let set = this.handlers.get(event)
    if (!set) {
      set = new Set()
      this.handlers.set(event, set)
    }
    set.add(handler)
    return () => {
      set?.delete(handler)
      if (set && set.size === 0) this.handlers.delete(event)
    }
  }

  /** 订阅连接状态变化，返回退订函数。 */
  onStatus(handler: StatusHandler): () => void {
    this.statusHandlers.add(handler)
    return () => {
      this.statusHandlers.delete(handler)
    }
  }

  /** 建立连接（重复调用无副作用）。 */
  connect(): void {
    if (this.source || typeof window === 'undefined' || !('EventSource' in window)) return

    this.closedByUser = false
    this.setStatus(this.attempt > 0 ? 'reconnecting' : 'connecting')

    const source = new EventSource(SSE_URL, { withCredentials: true })
    this.source = source

    source.onopen = () => {
      this.attempt = 0
      this.setStatus('open')
    }

    // 逐个事件注册监听（SSE 的 message 事件只覆盖无名事件，必须显式注册具名事件）
    for (const name of ALL_EVENTS) {
      source.addEventListener(name, (evt) => {
        this.dispatch(name, (evt as MessageEvent).data)
      })
    }

    // 兜底：未具名的 message（理论上后端不会发）也走一次
    source.onmessage = (evt) => {
      this.dispatch('message', (evt as MessageEvent).data)
    }

    source.onerror = () => {
      // EventSource 在断线时会自动重连；我们主动接管以获得可控的退避
      source.close()
      this.source = null
      if (this.closedByUser) {
        this.setStatus('closed')
        return
      }
      this.scheduleReconnect()
    }
  }

  /** 主动关闭（不再自动重连）。 */
  close(): void {
    this.closedByUser = true
    if (this.retryTimer) {
      clearTimeout(this.retryTimer)
      this.retryTimer = null
    }
    this.source?.close()
    this.source = null
    this.setStatus('closed')
  }

  // -------------------------------------------------------------------------

  private dispatch(event: string, raw: string): void {
    let payload: unknown = raw
    if (raw) {
      try {
        payload = JSON.parse(raw)
      } catch {
        // 非 JSON 载荷：原样透传，避免丢掉信息
        payload = raw
      }
    }

    const set = this.handlers.get(event)
    if (set) {
      for (const handler of set) {
        try {
          handler(payload, event)
        } catch (err) {
          // 单个订阅者抛错不应影响其它订阅者
          console.error('[sse] 订阅者处理事件失败', event, err)
        }
      }
    }
  }

  private scheduleReconnect(): void {
    const delay = Math.min(BASE_BACKOFF_MS * 2 ** this.attempt, MAX_BACKOFF_MS)
    this.attempt += 1

    this.setStatus('reconnecting', { attempt: this.attempt, retryInMs: delay })

    this.retryTimer = setTimeout(() => {
      this.retryTimer = null
      void this.reconnect()
    }, delay)
  }

  /**
   * EventSource 的 error 事件不会暴露 HTTP 状态码。
   * 重连前先请求一个普通鉴权接口：若 access token 过期，Axios 拦截器会
   * 用 refresh token 静默刷新；网络不可用时则继续让 EventSource 自己重连。
   */
  private async reconnect(): Promise<void> {
    if (this.closedByUser) return

    try {
      await get<unknown>('/auth/me', { timeout: 5_000 })
    } catch (error) {
      if (this.closedByUser) return

      const apiError = toApiError(error)
      if (apiError.isAuthError || apiError.HttpStatus === 401) {
        // HTTP 客户端已经完成刷新尝试并通知登录态；继续重连只会制造请求风暴。
        this.close()
        return
      }
      // 断网或服务重启：不要把一次探测失败误判为登出，继续建立 SSE。
    }

    if (!this.closedByUser) this.connect()
  }

  private setStatus(status: SSEStatus, extra?: Omit<SSEStatusChange, 'status'>): void {
    this.currentStatus = status
    const change: SSEStatusChange = { status, ...extra }
    for (const handler of this.statusHandlers) {
      try {
        handler(change)
      } catch (err) {
        console.error('[sse] 状态订阅者处理失败', err)
      }
    }
  }
}

/** 模块级共享连接（内置 SPA 全局一个即可，DESIGN.md §9.4）。 */
let sharedClient: SSEClient | null = null

/** 获取共享的 SSE 客户端（懒创建，不自动连接）。 */
export function getSharedSSEClient(): SSEClient {
  if (!sharedClient) {
    sharedClient = new SSEClient()
  }
  return sharedClient
}
