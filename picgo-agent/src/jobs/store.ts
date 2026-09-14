/**
 * 内存任务表 + 逐行日志环形缓冲。
 *
 * ⚠️ agent **不承担审计职责**。Go 侧的 `Jobs` / `JobItems` / `OperationLogs`
 * 才是持久化真相源；这里只维护**执行期**状态，进程重启即丢失。
 *
 * 这样设计的原因：agent 重启（插件装卸后必然发生）不应让 Go 侧的任务记录消失。
 */

import crypto from 'node:crypto'
import type { JobItem, JobStatus } from '../types.js'

/** 每个 job 保留的日志行数上限（环形，超出丢弃最旧的）。 */
const DEFAULT_LOG_CAPACITY = 500

/** 完成的 job 保留时长（毫秒），超过后清理，避免内存无限增长。 */
const DEFAULT_RETENTION_MS = 30 * 60 * 1000

/** 生成 job UID（`job_` 前缀 + 时间有序随机串，与 Go 侧 UID 风格一致）。 */
export function newJobUID(): string {
  const ts = Date.now().toString(36)
  const rand = crypto.randomBytes(6).toString('hex')
  return `job_${ts}${rand}`
}

interface JobRecord {
  item: JobItem
  logs: string[]
  /** 日志环形缓冲的起始序号（用于增量拉取）。 */
  logStart: number
  nextLogSeq: number
}

export class JobStore {
  private readonly jobs = new Map<string, JobRecord>()

  constructor(
    private readonly logCapacity = DEFAULT_LOG_CAPACITY,
    private readonly retentionMs = DEFAULT_RETENTION_MS
  ) {}

  /** 建一个 job 并返回其 UID。 */
  create(kind: string, payload: Record<string, unknown> = {}): string {
    this.gc()
    const uid = newJobUID()
    const now = Math.floor(Date.now() / 1000)
    this.jobs.set(uid, {
      item: {
        UID: uid,
        Kind: kind,
        Status: 'queued',
        Progress: 0,
        Payload: payload,
        Result: null,
        Error: '',
        CreatedAt: now,
        StartedAt: 0,
        FinishedAt: 0
      },
      logs: [],
      logStart: 0,
      nextLogSeq: 0
    })
    return uid
  }

  get(uid: string): JobItem | undefined {
    return this.jobs.get(uid)?.item
  }

  /** 全部 job（按创建时间倒序，最新的在前）。 */
  list(): JobItem[] {
    return [...this.jobs.values()]
      .map((r) => r.item)
      .sort((a, b) => b.CreatedAt - a.CreatedAt)
  }

  /** 标记为运行中。 */
  start(uid: string): void {
    const rec = this.jobs.get(uid)
    if (!rec) return
    rec.item.Status = 'running'
    rec.item.StartedAt = Math.floor(Date.now() / 1000)
  }

  /** 更新进度（0..100）。 */
  setProgress(uid: string, progress: number): void {
    const rec = this.jobs.get(uid)
    if (!rec) return
    rec.item.Progress = Math.max(0, Math.min(100, Math.round(progress)))
  }

  /** 追加一行日志（环形缓冲）。 */
  appendLog(uid: string, line: string): number | undefined {
    const rec = this.jobs.get(uid)
    if (!rec) return undefined

    const seq = rec.nextLogSeq
    rec.nextLogSeq += 1
    rec.logs.push(line)

    // 超出容量时丢弃最旧的（并推进 logStart 以便增量拉取对账）
    while (rec.logs.length > this.logCapacity) {
      rec.logs.shift()
      rec.logStart += 1
    }
    return seq
  }

  /**
   * 取日志。
   *
   * @param afterSeq 只返回序号 > afterSeq 的行（用于增量拉取）
   */
  logs(uid: string, afterSeq = -1): { lines: string[]; startSeq: number; nextSeq: number } | undefined {
    const rec = this.jobs.get(uid)
    if (!rec) return undefined

    const offset = Math.max(0, afterSeq + 1 - rec.logStart)
    return {
      lines: rec.logs.slice(offset),
      startSeq: rec.logStart,
      nextSeq: rec.nextLogSeq
    }
  }

  /** 标记成功。 */
  succeed(uid: string, result: Record<string, unknown> | null = null): void {
    const rec = this.jobs.get(uid)
    if (!rec) return
    rec.item.Status = 'succeeded'
    rec.item.Progress = 100
    rec.item.Result = result
    rec.item.FinishedAt = Math.floor(Date.now() / 1000)
  }

  /** 标记失败。 */
  fail(uid: string, error: string, result: Record<string, unknown> | null = null): void {
    const rec = this.jobs.get(uid)
    if (!rec) return
    rec.item.Status = 'failed'
    rec.item.Error = error
    rec.item.Result = result
    rec.item.FinishedAt = Math.floor(Date.now() / 1000)
  }

  /** 设置状态（内部用）。 */
  setStatus(uid: string, status: JobStatus): void {
    const rec = this.jobs.get(uid)
    if (!rec) return
    rec.item.Status = status
  }

  /** 删除一个已完成的 job。返回是否删掉了。 */
  remove(uid: string): boolean {
    const rec = this.jobs.get(uid)
    if (!rec) return false
    // 运行中的不允许删（避免调用方误清）
    if (rec.item.Status === 'queued' || rec.item.Status === 'running') return false
    return this.jobs.delete(uid)
  }

  /** 清理全部已完成的 job，返回删除数量。 */
  clearFinished(): number {
    let n = 0
    for (const [uid, rec] of [...this.jobs.entries()]) {
      if (rec.item.Status === 'succeeded' || rec.item.Status === 'failed') {
        this.jobs.delete(uid)
        n += 1
      }
    }
    return n
  }

  /** 清理超过保留期的已完成 job。 */
  private gc(): void {
    const cutoff = Date.now() - this.retentionMs
    for (const [uid, rec] of [...this.jobs.entries()]) {
      if (rec.item.Status !== 'succeeded' && rec.item.Status !== 'failed') continue
      const finishedMs = rec.item.FinishedAt * 1000
      if (finishedMs > 0 && finishedMs < cutoff) this.jobs.delete(uid)
    }
  }

  /** 统计。 */
  stats(): { total: number; running: number } {
    let running = 0
    for (const rec of this.jobs.values()) {
      if (rec.item.Status === 'queued' || rec.item.Status === 'running') running += 1
    }
    return { total: this.jobs.size, running }
  }
}
