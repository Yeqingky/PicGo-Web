/**
 * SSE 广播（`GET /api/events`）。
 *
 * ## 事件集（与 Go 侧自己的 SSE 同名同结构，docs/API.md §8.1）
 *
 * | 事件名 | 说明 |
 * |---|---|
 * | `upload.progress` | 单文件进度（picgo 只有 0/30/60/100 四档） |
 * | `upload.finished` | 单文件成功 |
 * | `upload.failed` | 单文件失败 |
 * | `job.started` | job 转 running |
 * | `job.log` | job 逐行日志 |
 * | `job.finished` | job 结束 |
 * | `system.notice` | 系统提示（重启、插件装卸等） |
 * | `ping` | 每 25 秒保活 |
 *
 * ## 命名（D81）
 *
 * **事件名保持小写点分**（它是协议层标识符，不是 JSON 字段）；
 * **事件体内的字段用 PascalCase**。
 *
 * ## 连接管理
 *
 * 客户端断开时必须清掉监听器，否则长时间运行会泄漏（每个连接一份 writer）。
 */

import type { Logger } from '../logger.js'

/** ping 间隔（毫秒）。SSE 保活的常规值是 15~30s；Go 侧同样用 25s。 */
export const PING_INTERVAL_MS = 25_000

/** 一个已连接的订阅者。 */
export interface SSEWriter {
  /** 写一条事件。返回 false 表示写入失败（连接已断）。 */
  write: (event: string, data: unknown) => boolean
  /** 关闭连接。 */
  close: () => void
}

export interface BroadcasterOptions {
  pingIntervalMs?: number
  logger?: Logger
}

/**
 * SSE 广播中心。
 *
 * 极简实现：维护一组 writer，`broadcast` 逐个写。
 * 不做背压控制 —— agent 的事件频率极低（每个上传 4~5 条），无需队列。
 */
export class SSEBroadcaster {
  private readonly writers = new Set<SSEWriter>()
  private pingTimer: NodeJS.Timeout | null = null
  private readonly pingIntervalMs: number
  private readonly log: Logger | undefined

  constructor(options: BroadcasterOptions = {}) {
    this.pingIntervalMs = options.pingIntervalMs ?? PING_INTERVAL_MS
    this.log = options.logger
  }

  /** 加入一个连接，返回撤销函数（务必在连接关闭时调用）。 */
  add(writer: SSEWriter): () => void {
    this.writers.add(writer)
    this.ensurePingTimer()
    this.log?.debug('SSE 客户端已连接', { total: this.writers.size })

    return () => {
      this.writers.delete(writer)
      this.log?.debug('SSE 客户端已断开', { total: this.writers.size })
      if (this.writers.size === 0) this.stopPingTimer()
    }
  }

  /** 广播一条事件（无订阅者时是 no-op）。 */
  broadcast(event: string, data: unknown): void {
    if (this.writers.size === 0) return

    for (const writer of [...this.writers]) {
      try {
        const ok = writer.write(event, data)
        if (!ok) {
          this.writers.delete(writer)
          writer.close()
        }
      } catch (error) {
        this.log?.warn('SSE 写入失败，移除该连接', { event, err: error })
        this.writers.delete(writer)
      }
    }

    if (this.writers.size === 0) this.stopPingTimer()
  }

  /** 当前连接数。 */
  size(): number {
    return this.writers.size
  }

  /** 停止全部连接并释放定时器（优雅退出时调用）。 */
  shutdown(): void {
    this.stopPingTimer()
    for (const writer of [...this.writers]) {
      try {
        writer.close()
      } catch {
        // 关闭失败无所谓，进程要退了
      }
    }
    this.writers.clear()
  }

  private ensurePingTimer(): void {
    if (this.pingTimer) return
    this.pingTimer = setInterval(() => {
      this.broadcast('ping', { Ts: Math.floor(Date.now() / 1000) })
    }, this.pingIntervalMs)
    // 不要因为这个定时器阻止进程退出
    this.pingTimer.unref?.()
  }

  private stopPingTimer(): void {
    if (!this.pingTimer) return
    clearInterval(this.pingTimer)
    this.pingTimer = null
  }
}

// ---------------------------------------------------------------------------
// 事件负载类型（事件体字段 PascalCase，D81）
// ---------------------------------------------------------------------------

export interface UploadProgressPayload {
  JobUID: string
  Seq: number
  FileName: string
  /** 0..100。picgo 只有 0/30/60/100 四档。 */
  Progress: number
}

export interface UploadFinishedPayload {
  JobUID: string
  Seq: number
  FileName: string
  URL: string
  Width: number
  Height: number
  Size: number
  UploaderType: string
}

export interface UploadFailedPayload {
  JobUID: string
  Seq: number
  FileName: string
  Error: string
}

export interface JobLogPayload {
  JobUID: string
  Seq: number
  Line: string
}

export interface JobFinishedPayload {
  JobUID: string
  Status: 'succeeded' | 'failed'
  Result: Record<string, unknown> | null
  Error: string
}

export interface SystemNoticePayload {
  Level: 'info' | 'warn' | 'error'
  Message: string
}

/** 事件名常量（避免拼写错误）。 */
export const EventName = {
  UploadProgress: 'upload.progress',
  UploadFinished: 'upload.finished',
  UploadFailed: 'upload.failed',
  JobStarted: 'job.started',
  JobLog: 'job.log',
  JobFinished: 'job.finished',
  SystemNotice: 'system.notice',
  Ping: 'ping'
} as const
