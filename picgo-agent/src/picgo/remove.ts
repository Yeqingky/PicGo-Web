/**
 * 远端删除（D47）。
 *
 * ## 为什么这么绕
 *
 * picgo-core **没有**删除 API。生态里的事实约定是：
 *
 *     picgo.emit('remove', files, guiApi)      // 调用方（GUI 侧）
 *     ctx.on('remove', onRemove)               // 插件侧（如 picgo-plugin-github-plus）
 *
 * 三个不可靠点：
 *
 * 1. **`emit` 没有返回值** —— 无法直接知道插件处理成功与否
 * 2. **插件的 handler 是 async 且无人 await** —— `emit` 立即返回
 * 3. **`guiApi` 是 Electron 专属** —— 插件会解构 `{ showNotification }`，纯 Node 里没有
 *
 * 因此策略是：
 * - 造一个 **guiApi shim**（提供 `showNotification` 等）
 * - `emit` 后**等一小段**（`RemoveTimeoutMs`），收集 shim 捕获到的通知文案
 * - 从文案**反推**成功/失败（例如 github-plus 会发「成功同步删除」或「删除失败N个」）
 * - 若事前就没有任何 `remove` 监听器 → 直接 `Supported = false`
 *
 * ## 可靠性说明（必须在 UI 上如实告知）
 *
 * 这是**尽力而为**的删除：文案反推可能误判，插件也可能压根不通知。
 * 因此 Go 侧只把它当作「远端删除的尝试结果」，本地记录无论如何都会删。
 */

import type { IPicGo } from 'picgo'
import { describeError } from '../errors.js'
import type { Logger } from '../logger.js'
import type { RawImgInfo, RemoveData } from '../types.js'
import type { CapabilityCache } from './capability.js'

/** 插件通过 `showNotification` 传给我们的通知。 */
interface CapturedNotice {
  Title: string
  Body: string
}

/**
 * `guiApi` shim。
 *
 * 插件对 guiApi 的使用方式是解构，常见字段：
 * - `showNotification({ title, body })` —— 我们捕获它
 * - 少数插件还会用 `copyToClipboard` / `showOpenDialog` 等，缺了会抛错；
 *   因此这里给出**全部为 no-op** 的宽松对象，并额外提供常见方法。
 */
export interface GuiApiShim {
  showNotification: (notice: { title?: string; body?: string; text?: string }) => void
  copyToClipboard: (text: string) => void
  showOpenDialog: () => Promise<{ canceled: boolean; filePaths: string[] }>
  showSaveDialog: () => Promise<{ canceled: boolean; filePath: string }>
  getClipboardFilePaths: () => string[]
  [key: string]: unknown
}

export interface RemoveResult extends RemoveData {
  /** 捕获到的通知（供日志与排查）。 */
  Notices: CapturedNotice[]
}

export interface RemoveDeps {
  picgo: IPicGo
  log: Logger
  cache: CapabilityCache
  timeoutMs: number
}

/**
 * 从通知文案反推成功/失败。
 *
 * 判定顺序很重要：**先看不成功的信号**，因为插件的失败文案里常常也包含「删除」二字。
 */
export function inferFromNotices(notices: readonly CapturedNotice[]): {
  verdict: 'success' | 'failure' | 'unknown'
  message: string
} {
  if (notices.length === 0) {
    return { verdict: 'unknown', message: '' }
  }

  const joined = notices.map((n) => `${n.Title} ${n.Body}`).join(' | ')

  const failureHints = ['失败', '错误', '异常', 'fail', 'error', '无法']
  const successHints = ['成功', 'success', '已删除', '删除完成']

  if (failureHints.some((hint) => joined.includes(hint))) {
    return { verdict: 'failure', message: joined }
  }
  if (successHints.some((hint) => joined.includes(hint))) {
    return { verdict: 'success', message: joined }
  }
  return { verdict: 'unknown', message: joined }
}

/** 构造 guiApi shim，并把捕获到的通知写进 `sink`。 */
export function createGuiApiShim(sink: CapturedNotice[]): GuiApiShim {
  return {
    showNotification: (notice) => {
      sink.push({
        Title: typeof notice?.title === 'string' ? notice.title : '',
        Body: typeof notice?.body === 'string' ? notice.body : ''
      })
    },
    copyToClipboard: () => {
      /* no-op */
    },
    showOpenDialog: async () => ({ canceled: true, filePaths: [] }),
    showSaveDialog: async () => ({ canceled: true, filePath: '' }),
    getClipboardFilePaths: () => []
  }
}

/** 可注入的等待器（测试用）。 */
export type Sleeper = (ms: number) => Promise<void>

const realSleep: Sleeper = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

/**
 * 执行远端删除。
 *
 * @param items 上传时的完整 `IImgInfo` 数组（**字段名原样**，插件靠 `fileName` / `sha` 等定位文件）
 */
export async function performRemove(
  deps: RemoveDeps,
  items: readonly RawImgInfo[],
  uploaderType: string,
  sleep: Sleeper = realSleep
): Promise<RemoveResult> {
  const { picgo, log, cache, timeoutMs } = deps

  if (items.length === 0) {
    return {
      RemoteDeleted: false,
      Supported: true,
      Message: '没有可删除的远端条目',
      Notices: []
    }
  }

  // ---- 1. 事前判定：有没有插件实现了 remove ----
  const listenerCount = safelyCountRemoveListeners(picgo)
  if (listenerCount === 0) {
    // 缓存修正：该驱动不支持远端删除（Go 侧据此只删本地记录）
    if (uploaderType !== '') cache.invalidateType(uploaderType)

    log.info('驱动不支持远端删除（无 remove 监听器）', {
      uploader: uploaderType,
      count: items.length
    })
    return {
      RemoteDeleted: false,
      Supported: false,
      Message: '该驱动不支持远端删除（没有插件实现 remove 事件）',
      Notices: []
    }
  }

  // ---- 2. emit ----
  const notices: CapturedNotice[] = []
  const shim = createGuiApiShim(notices)

  log.debug('触发 remove 事件', {
    uploader: uploaderType,
    count: items.length,
    listeners: listenerCount
  })

  try {
    picgo.emit('remove', items as never, shim as never)
  } catch (error) {
    log.warn('emit(remove) 抛出异常', { uploader: uploaderType, err: error })
    return {
      RemoteDeleted: false,
      Supported: true,
      Message: `触发删除时插件抛错：${describeError(error)}`,
      Notices: notices
    }
  }

  // ---- 3. 等一小段，回收异步插件的通知 ----
  await sleep(timeoutMs)

  // ---- 4. 从文案反推 ----
  const { verdict, message } = inferFromNotices(notices)

  if (verdict === 'success') {
    log.info('远端删除成功', { uploader: uploaderType, count: items.length, notices: notices.length })
    return {
      RemoteDeleted: true,
      Supported: true,
      Message: message || '成功同步删除',
      Notices: notices
    }
  }

  if (verdict === 'failure') {
    log.warn('远端删除失败', { uploader: uploaderType, count: items.length, message })
    return {
      RemoteDeleted: false,
      Supported: true,
      Message: message,
      Notices: notices
    }
  }

  // unknown：没有通知。可能是插件成功但静默，也可能是插件没实现该驱动的删除分支。
  log.warn('远端删除结果未知（插件未发通知）', {
    uploader: uploaderType,
    count: items.length,
    timeoutMs
  })
  return {
    RemoteDeleted: false,
    Supported: true,
    Message: `远端删除结果未知：等待 ${timeoutMs}ms 未收到插件通知（插件可能未实现该驱动的删除）`,
    Notices: notices
  }
}

function safelyCountRemoveListeners(picgo: IPicGo): number {
  try {
    return picgo.listenerCount('remove')
  } catch {
    return 0
  }
}
