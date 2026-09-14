import { create } from 'zustand'

import { getSharedSSEClient, SSEEvent } from '@/lib/sse'
import { uploadApi } from '@/lib/api'
import { isMockEnabled } from '@/mocks'

/**
 * 上传队列（DESIGN.md §5.1 / §11）。
 *
 * 边界（很重要）：
 *  - 这里只放**客户端的上传队列视图**（用户意图 + 实时进度），不是服务端状态
 *  - 服务端的任务事实在 `Jobs` 表里，通过 `jobApi` / `/jobs` 页查看；两者**不要**互相镜像
 *  - 进度来自 SSE `upload.progress`（picgo 只有 0/30/60/100 **四档**，见 OPERATIONS.md），
 *    因此在 store 内做**平滑插值**，避免进度条一跳一跳
 *
 * 一批次一驱动（D38）：每次「开始上传」创建一个批次 = 一个 job。
 * **切换目标存储只影响新加入队列的文件**，已在队列中的不变。
 */

export type UploadItemStatus = 'waiting' | 'uploading' | 'success' | 'failed'

export interface UploadItem {
  /** 本地 id（与 UploadUID 不同：后者由后端在入队后返回） */
  ID: string
  File: File
  FileName: string
  Size: number
  /** 本地预览 URL（`URL.createObjectURL`，**不是**后端缩略图，D84 不受影响） */
  PreviewURL: string
  Status: UploadItemStatus
  /** 展示用进度（平滑后的 0..100） */
  Progress: number
  /** SSE 给出的目标进度（四档：0 / 30 / 60 / 100） */
  TargetProgress: number
  /** 后端返回的图片 UID（上传成功后） */
  UploadUID: string
  /** 图床 URL（成功后） */
  URL: string
  Error: string
  Attempts: number
  /** 归属批次（job） */
  JobUID: string
    | ''
}

export interface UploadBatch {
  JobUID: string
  StorageUID: string
  StorageName: string
  AlbumUID: string
  CreatedAt: number
}

interface UploadState {
  items: UploadItem[]
  batches: UploadBatch[]
  /** 当前选中的目标存储（只影响**新加入**队列的文件，D38） */
  targetStorageUID: string
  /** 当前选中的相册（可选） */
  targetAlbumUID: string
  /** 是否正在提交（防重复点击） */
  submitting: boolean
}

interface UploadActions {
  setTargetStorage: (storageUID: string) => void
  setTargetAlbum: (albumUID: string) => void
  /** 加入待上传文件（会用当前的目标存储/相册创建新批次）。 */
  addFiles: (files: File[]) => void
  removeItem: (id: string) => void
  /** 重试单个失败项（按该项自己的批次驱动重传）。 */
  retryItem: (id: string) => void
  /** 重试全部失败项。 */
  retryAllFailed: () => void
  /** 清理已成功 / 已失败且被用户移除的项。 */
  clearFinished: () => void
  /** 清空队列（含预览 URL 释放）。 */
  clearAll: () => void
  /** 该批次是否全部结束（供「上传中离开页面」提示判断）。 */
  hasPending: () => boolean
}

// ---------------------------------------------------------------------------
// 内部工具
// ---------------------------------------------------------------------------

let seq = 0
function nextID(): string {
  seq += 1
  return `up_${Date.now().toString(36)}_${seq}`
}

/** 释放本地预览 URL（避免内存泄漏）。 */
function revokePreview(url: string): void {
  if (url && url.startsWith('blob:')) {
    try {
      URL.revokeObjectURL(url)
    } catch {
      // 忽略：某些环境下 revoke 会抛错（如已释放）
    }
  }
}

/** 从浏览器文件对象里取扩展名（小写，不含点）。 */
function extOf(name: string): string {
  const dot = name.lastIndexOf('.')
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : ''
}

// ---------------------------------------------------------------------------
// store
// ---------------------------------------------------------------------------

export const useUploadStore = create<UploadState & UploadActions>()((set, get) => ({
  items: [],
  batches: [],
  targetStorageUID: '',
  targetAlbumUID: '',
  submitting: false,

  setTargetStorage(storageUID) {
    set({ targetStorageUID: storageUID })
  },

  setTargetAlbum(albumUID) {
    set({ targetAlbumUID: albumUID })
  },

  addFiles(files) {
    if (files.length === 0) return

    const storageUID = get().targetStorageUID
    const newItems: UploadItem[] = files.map((file) => ({
      ID: nextID(),
      File: file,
      FileName: file.name || `image.${extOf(file.type) || 'png'}`,
      Size: file.size,
      // 本地预览：让用户在上传前就能看到图（与后端缩略图无关，D84）
      PreviewURL: file.type.startsWith('image/') ? URL.createObjectURL(file) : '',
      Status: 'waiting',
      Progress: 0,
      TargetProgress: 0,
      UploadUID: '',
      URL: '',
      Error: '',
      Attempts: 0,
      JobUID: '',
    }))

    set((state) => ({ items: [...state.items, ...newItems] }))

    // 有目标存储时立即开传；没有则留在队列里等用户选驱动
    if (storageUID) {
      void submitPending()
    }
  },

  removeItem(id) {
    set((state) => {
      const target = state.items.find((item) => item.ID === id)
      if (target) revokePreview(target.PreviewURL)
      return { items: state.items.filter((item) => item.ID !== id) }
    })
  },

  retryItem(id) {
    const item = get().items.find((entry) => entry.ID === id)
    if (!item || item.Status !== 'failed') return

    set((state) => ({
      items: state.items.map((entry) =>
        entry.ID === id
          ? { ...entry, Status: 'waiting', Progress: 0, TargetProgress: 0, Error: '' }
          : entry,
      ),
    }))
    void submitPending()
  },

  retryAllFailed() {
    set((state) => ({
      items: state.items.map((entry) =>
        entry.Status === 'failed'
          ? { ...entry, Status: 'waiting', Progress: 0, TargetProgress: 0, Error: '' }
          : entry,
      ),
    }))
    void submitPending()
  },

  clearFinished() {
    set((state) => {
      const keep: UploadItem[] = []
      for (const item of state.items) {
        if (item.Status === 'success' || item.Status === 'failed') {
          revokePreview(item.PreviewURL)
          continue
        }
        keep.push(item)
      }
      return { items: keep }
    })
  },

  clearAll() {
    for (const item of get().items) revokePreview(item.PreviewURL)
    set({ items: [], batches: [] })
  },

  hasPending() {
    return get().items.some((item) => item.Status === 'waiting' || item.Status === 'uploading')
  },
}))

// ---------------------------------------------------------------------------
// 提交（按批次分组后逐批调用上传接口）
// ---------------------------------------------------------------------------

let submitting = false

/**
 * 把队列里所有 `waiting` 项按「当前目标存储」分批提交。
 *
 * 为什么需要分批：**一次上传请求只能有一个驱动**（D38）。
 * 同一批次内的文件共享一个 `JobUID`，进度事件靠它归属。
 */
async function submitPending(): Promise<void> {
  if (submitting) return
  submitting = true
  useUploadStore.setState({ submitting: true })

  try {
    // 每轮只取「当前仍处于 waiting 且尚未分配 job」的项
    for (;;) {
      const state = useUploadStore.getState()
      const pending = state.items.filter((item) => item.Status === 'waiting' && item.JobUID === '')

      if (pending.length === 0) break

      const storageUID = state.targetStorageUID
      if (!storageUID) {
        // 没有目标存储：留在队列里等用户选（不报错，UI 会提示去选）
        break
      }

      const albumUID = state.targetAlbumUID
      const ids = pending.map((item) => item.ID)
      const files = pending.map((item) => item.File)

      // 先乐观地标记为 uploading，让 UI 立即有反应
      useUploadStore.setState((prev) => ({
        items: prev.items.map((item) =>
          ids.includes(item.ID) ? { ...item, Status: 'uploading' as const } : item,
        ),
      }))

      try {
        const res = await uploadApi.create({ Files: files, StorageUID: storageUID, AlbumUID: albumUID })

        // 记录批次 + 把后端返回的 UploadUID / jobUID 绑到队列项
        useUploadStore.setState((prev) => ({
          batches: [
            ...prev.batches,
            {
              JobUID: res.JobUID,
              StorageUID: res.StorageUID,
              StorageName: storageUID,
              AlbumUID: albumUID,
              CreatedAt: Date.now(),
            },
          ],
          items: prev.items.map((item) => {
            const ref = res.Items.find((entry) => entry.FileName === item.FileName)
            if (!ids.includes(item.ID)) return item
            return {
              ...item,
              JobUID: res.JobUID,
              UploadUID: ref?.UploadUID ?? '',
            }
          }),
        }))

        // 加入任务面板的跟踪列表（供顶栏「任务」提示与 /jobs 页）
        bindJobTracking(res.JobUID)

        // mock 模式没有真实 SSE，进度不会推进 —— 这里模拟一次完成，
        // 让 `VITE_USE_MOCK=true` 的走查能看到「进度条走完 + 出现复制按钮」
        if (isMockEnabled()) {
          simulateMockCompletion(res.JobUID, ids)
        }
      } catch (err) {
        const message = err instanceof Error ? err.message : String(err)
        // 整批提交失败（参数错误 / 配额不足 / 限流）→ 全部标记失败
        useUploadStore.setState((prev) => ({
          items: prev.items.map((item) =>
            ids.includes(item.ID)
              ? { ...item, Status: 'failed' as const, Error: message }
              : item,
          ),
        }))
      }
    }
  } finally {
    submitting = false
    useUploadStore.setState({ submitting: false })
  }
}

/**
 * mock 模式下模拟上传完成（仅 `VITE_USE_MOCK=true` 时调用）。
 *
 * 真实环境靠 SSE `upload.finished` 驱动；mock 没有 SSE，
 * 若不模拟，队列会永远停在「上传中」，无法走查后续交互。
 */
function simulateMockCompletion(jobUID: string, ids: string[]): void {
  // 先走一次中间进度，模拟 picgo 的 0/30/60/100 四档
  useUploadStore.setState((prev) => ({
    items: prev.items.map((item) =>
      ids.includes(item.ID) ? { ...item, TargetProgress: 60 } : item,
    ),
  }))

  setTimeout(() => {
    useUploadStore.setState((prev) => ({
      items: prev.items.map((item) =>
        ids.includes(item.ID)
          ? {
              ...item,
              Status: 'success' as const,
              Progress: 100,
              TargetProgress: 100,
              URL: `https://picsum.photos/seed/${encodeURIComponent(jobUID)}-${item.ID}/1200/800`,
            }
          : item,
      ),
    }))
  }, 1200)
}

/** 触发出队（供外部在设置目标存储后调用）。 */
export function flushUploadQueue(): void {
  void submitPending()
}

// ---------------------------------------------------------------------------
// SSE → 队列（进度与结果）
// ---------------------------------------------------------------------------

interface UploadEventPayload {
  JobUID?: string
  UploadUID?: string
  Seq?: number
  FileName?: string
  Progress?: number
  URL?: string
  Error?: string
  Attempts?: number
}

let eventsBound = false

function toPayload(raw: unknown): UploadEventPayload {
  if (typeof raw !== 'object' || raw === null) return {}
  const obj = raw as Record<string, unknown>
  const out: UploadEventPayload = {}
  if (typeof obj.JobUID === 'string') out.JobUID = obj.JobUID
  if (typeof obj.UploadUID === 'string') out.UploadUID = obj.UploadUID
  if (typeof obj.Seq === 'number') out.Seq = obj.Seq
  if (typeof obj.FileName === 'string') out.FileName = obj.FileName
  if (typeof obj.Progress === 'number') out.Progress = obj.Progress
  if (typeof obj.URL === 'string') out.URL = obj.URL
  if (typeof obj.Error === 'string') out.Error = obj.Error
  if (typeof obj.Attempts === 'number') out.Attempts = obj.Attempts
  return out
}

/** 在一个批次内定位文件（优先 UploadUID，其次 FileName）。 */
function findItem(items: UploadItem[], jobUID: string, payload: UploadEventPayload): UploadItem | undefined {
  const inBatch = items.filter((item) => item.JobUID === jobUID)

  if (payload.UploadUID) {
    const byUID = inBatch.find((item) => item.UploadUID === payload.UploadUID)
    if (byUID) return byUID
  }
  if (payload.FileName) {
    const byName = inBatch.find(
      (item) => item.FileName === payload.FileName && item.Status !== 'success',
    )
    if (byName) return byName
  }
  return undefined
}

/** 绑定 SSE 事件（模块级只绑一次）。 */
export function bindUploadEvents(): void {
  if (eventsBound) return
  eventsBound = true

  const client = getSharedSSEClient()

  client.on(SSEEvent.UploadProgress, (raw) => {
    const data = toPayload(raw)
    if (!data.JobUID) return

    useUploadStore.setState((state) => ({
      items: state.items.map((item) => {
        if (item.ID !== findItem(state.items, data.JobUID as string, data)?.ID) return item
        return { ...item, TargetProgress: Math.max(0, Math.min(100, data.Progress ?? 0)) }
      }),
    }))
  })

  client.on(SSEEvent.UploadFinished, (raw) => {
    const data = toPayload(raw)
    if (!data.JobUID) return

    useUploadStore.setState((state) => {
      const target = findItem(state.items, data.JobUID as string, data)
      if (!target) return state
      return {
        items: state.items.map((item) =>
          item.ID === target.ID
            ? {
                ...item,
                Status: 'success' as const,
                Progress: 100,
                TargetProgress: 100,
                URL: data.URL ?? item.URL,
              }
            : item,
        ),
      }
    })
  })

  client.on(SSEEvent.UploadFailed, (raw) => {
    const data = toPayload(raw)
    if (!data.JobUID) return

    useUploadStore.setState((state) => {
      const target = findItem(state.items, data.JobUID as string, data)
      if (!target) return state
      return {
        items: state.items.map((item) =>
          item.ID === target.ID
            ? {
                ...item,
                Status: 'failed' as const,
                Error: data.Error ?? '上传失败',
                Attempts: data.Attempts ?? item.Attempts,
              }
            : item,
        ),
      }
    })
  })

  startProgressTicker()
}

/** 延迟绑定任务面板（避免 store 之间循环 import）。 */
let jobTracker: ((jobUID: string) => void) | null = null

/** 由 `task-store` 在初始化时注入（打破循环依赖）。 */
export function setJobTracker(tracker: (jobUID: string) => void): void {
  jobTracker = tracker
}

function bindJobTracking(jobUID: string): void {
  jobTracker?.(jobUID)
}

// ---------------------------------------------------------------------------
// 进度平滑插值
// ---------------------------------------------------------------------------

/**
 * picgo 只上报 0 / 30 / 60 / 100 **四档**（OPERATIONS.md）。
 *
 * 直接把四档画成进度条会一跳一跳，因此在 store 内做**匀速逼近**：
 * 每 120ms 把展示进度朝目标推进一点，到 30/60 附近放缓，营造连续感。
 *
 * ⚠️ 这是**纯展示层**的平滑，不改变任何业务语义（真实进度仍以 SSE 为准）。
 */
const TICK_MS = 120

let ticker: ReturnType<typeof setInterval> | null = null

function startProgressTicker(): void {
  if (ticker) return

  ticker = setInterval(() => {
    const state = useUploadStore.getState()

    let changed = false
    const items = state.items.map((item) => {
      if (item.Status !== 'uploading') return item
      if (item.Progress >= item.TargetProgress) return item

      changed = true
      // 距离目标越远走得越快，越近越慢（避免长时间停在 99% 或突然跳）
      const gap = item.TargetProgress - item.Progress
      const step = Math.max(0.6, Math.min(gap * 0.18, 8))
      const next = Math.min(item.TargetProgress, item.Progress + step)
      return { ...item, Progress: Number(next.toFixed(2)) }
    })

    if (changed) {
      useUploadStore.setState({ items })
    } else {
      // 没有在传的任务就停掉定时器（省电，且避免空转）
      const active = state.items.some((item) => item.Status === 'uploading' || item.Status === 'waiting')
      if (!active && ticker) {
        clearInterval(ticker)
        ticker = null
      }
    }
  }, TICK_MS)
}

/** 整批进度（已完成项 / 总项数）—— DESIGN.md §5.1。 */
export function batchProgress(items: UploadItem[]): number {
  if (items.length === 0) return 0
  const done = items.filter((item) => item.Status === 'success').length
  const partial = items
    .filter((item) => item.Status === 'uploading')
    .reduce((acc, item) => acc + item.Progress / 100, 0)
  return Math.min(100, Math.round(((done + partial) / items.length) * 100))
}
