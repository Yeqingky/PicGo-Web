import { create } from 'zustand'

import { getSharedSSEClient, SSEEvent, type SSEEventName, type SSEStatus } from '@/lib/sse'

/**
 * 任务与实时推送状态（DESIGN.md §9.4 / §11）。
 *
 * 归属：
 *  - **SSE 连接状态**（供侧栏底部展示服务器在线 / 离线）
 *  - **正在跟踪的 JobUID 集合**（上传 / 插件安装 / 主题安装都会产生 job）
 *
 * 注意区分两层「任务」：
 *  - `Jobs` 表 / `job.*` 事件 —— 后端的批次任务（长任务，可跨页面存活）
 *  - `upload.progress` 等 —— 上传进度；上传队列本身的本地视图属于 `upload-store`（W8 建）
 */

export interface TrackedJob {
  JobUID: string
  /** 任务类型（`upload` / `plugin.install` / `theme.install` …） */
  Kind: string
  /** 0..100 */
  Progress: number
  Status: 'queued' | 'running' | 'succeeded' | 'failed'
  /** 最近若干行日志（供抽屉展示） */
  Logs: string[]
}

/** 每次任务最多保留的日志行数（防止长任务把内存吃满）。 */
const MAX_LOGS_PER_JOB = 500

interface TaskState {
  /** SSE 连接状态 */
  sseStatus: SSEStatus
  /** 重连尝试次数 */
  sseAttempt: number
  /** 正在跟踪的任务 */
  jobs: Record<string, TrackedJob>
  /** 轮询式任务列表的刷新信号（W8 的 hooks 监听它来重新拉取） */
  jobsRevision: number
}

interface TaskActions {
  /** 建立 SSE 连接（幂等）。AppShell 挂载时调用一次。 */
  connectSSE: () => void
  /** 断开 SSE 连接。 */
  disconnectSSE: () => void
  /** 开始跟踪一个 job。 */
  trackJob: (jobUID: string, kind: string) => void
  /** 停止跟踪（任务完成且用户已查看时清理）。 */
  untrackJob: (jobUID: string) => void
  /** 清空所有已终结的任务。 */
  clearFinished: () => void
}

export const useTaskStore = create<TaskState & TaskActions>()((set) => ({
  sseStatus: 'idle',
  sseAttempt: 0,
  jobs: {},
  jobsRevision: 0,

  connectSSE() {
    const client = getSharedSSEClient()

    // 状态回调只注册一次（用 Symbol 风格标记避免重复注册）
    if (!statusBound) {
      statusBound = true
      client.onStatus((change) => {
        set({
          sseStatus: change.status,
          sseAttempt: change.attempt ?? 0,
        })
      })
    }

    if (!eventsBound) {
      eventsBound = true
      bindJobEvents()
    }

    client.connect()
  },

  disconnectSSE() {
    getSharedSSEClient().close()
  },

  trackJob(jobUID, kind) {
    set((state) => ({
      jobs: {
        ...state.jobs,
        [jobUID]: state.jobs[jobUID] ?? {
          JobUID: jobUID,
          Kind: kind,
          Progress: 0,
          Status: 'queued',
          Logs: [],
        },
      },
    }))
  },

  untrackJob(jobUID) {
    set((state) => {
      const next = { ...state.jobs }
      delete next[jobUID]
      return { jobs: next }
    })
  },

  clearFinished() {
    set((state) => {
      const next: Record<string, TrackedJob> = {}
      for (const [uid, job] of Object.entries(state.jobs)) {
        if (job.Status === 'running' || job.Status === 'queued') {
          next[uid] = job
        }
      }
      return { jobs: next }
    })
  },
}))

// --- SSE 事件 → store 的桥接（模块级只绑定一次） ---

let statusBound = false
let eventsBound = false

/** SSE 事件载荷（事件体内字段用 PascalCase，见 docs/API.md §8）。 */
interface JobEventPayload {
  JobUID?: string
  Kind?: string
  Progress?: number
  Line?: string
  Status?: string
}

function parseJobPayload(payload: unknown): JobEventPayload {
  if (typeof payload !== 'object' || payload === null) return {}

  const raw = payload as Record<string, unknown>
  const out: JobEventPayload = {}

  if (typeof raw.JobUID === 'string') out.JobUID = raw.JobUID
  if (typeof raw.Kind === 'string') out.Kind = raw.Kind
  if (typeof raw.Progress === 'number') out.Progress = raw.Progress
  if (typeof raw.Line === 'string') out.Line = raw.Line
  if (typeof raw.Status === 'string') out.Status = raw.Status

  return out
}

function bindJobEvents(): void {
  const client = getSharedSSEClient()

  const patchJob = (jobUID: string | undefined, patch: Partial<TrackedJob>) => {
    if (!jobUID) return
    const state = useTaskStore.getState()
    const existing = state.jobs[jobUID]
    // 未跟踪的任务不主动创建（避免把历史任务灌进内存）；
    // 需要跟踪时由业务调用 trackJob。
    if (!existing) return

    useTaskStore.setState({
      jobs: {
        ...state.jobs,
        [jobUID]: { ...existing, ...patch },
      },
    })
  }

  const onStarted = (payload: unknown) => {
    const data = parseJobPayload(payload)
    patchJob(data.JobUID, { Status: 'running', Kind: data.Kind })
  }

  const onFinished = (payload: unknown) => {
    const data = parseJobPayload(payload)
    const failed = data.Status === 'failed'
    patchJob(data.JobUID, {
      Status: failed ? 'failed' : 'succeeded',
      Progress: failed ? undefined : 100,
    })
    // 任务终结后让列表重新拉取（W8 的 hooks 监听 jobsRevision）
    useTaskStore.setState((state) => ({ jobsRevision: state.jobsRevision + 1 }))
  }

  const onLog = (payload: unknown) => {
    const data = parseJobPayload(payload)
    if (!data.JobUID || typeof data.Line !== 'string') return

    const state = useTaskStore.getState()
    const existing = state.jobs[data.JobUID]
    if (!existing) return

    const logs = [...existing.Logs, data.Line]
    if (logs.length > MAX_LOGS_PER_JOB) {
      logs.splice(0, logs.length - MAX_LOGS_PER_JOB)
    }
    useTaskStore.setState({
      jobs: { ...state.jobs, [data.JobUID]: { ...existing, Logs: logs } },
    })
  }

  const onProgress = (payload: unknown) => {
    const data = parseJobPayload(payload)
    if (!data.JobUID) return
    if (typeof data.Progress !== 'number') return
    patchJob(data.JobUID, { Progress: Math.max(0, Math.min(100, data.Progress)) })
  }

  client.on(SSEEvent.JobStarted satisfies SSEEventName, onStarted)
  client.on(SSEEvent.JobFinished satisfies SSEEventName, onFinished)
  client.on(SSEEvent.JobLog satisfies SSEEventName, onLog)
  client.on(SSEEvent.UploadProgress satisfies SSEEventName, onProgress)
}
