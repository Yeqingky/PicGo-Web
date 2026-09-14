/**
 * 远端删除的单测（D47）。
 *
 * 这条链路的**本质是不可靠**的（`emit` 无返回值、插件 async 无人 await），
 * 因此测试重点是「各种不可靠情形下的行为边界」：
 * - 无 remove 监听器 → Supported=false（Go 只删本地记录）
 * - 插件通知成功 → RemoteDeleted=true
 * - 插件通知失败 → Supported=true / RemoteDeleted=false
 * - 插件沉默 → 结果未知（也返回 false + 明确说明）
 * - 插件抛错 → 不把异常抛给调用方
 */

import { describe, expect, it } from 'vitest'
import type { IPicGo } from 'picgo'
import { createLogger } from '../logger.js'
import { CapabilityCache } from './capability.js'
import { createGuiApiShim, inferFromNotices, performRemove } from './remove.js'
import { createFakePicgo } from '../testing/fake-picgo.js'

const log = createLogger('error')

/** 立即返回的 sleeper（测试不等真实超时）。 */
const noSleep = async (): Promise<void> => {}

describe('inferFromNotices —— 从插件通知文案反推结果', () => {
  it('无通知 → unknown', () => {
    expect(inferFromNotices([]).verdict).toBe('unknown')
  })

  it('含「成功」→ success', () => {
    expect(inferFromNotices([{ Title: '删除提示', Body: '成功同步删除' }]).verdict).toBe('success')
  })

  it('含「失败」→ failure', () => {
    expect(inferFromNotices([{ Title: '删除提示', Body: '删除失败2个' }]).verdict).toBe('failure')
  })

  it('**优先级**：同时含成功与失败时判失败（插件的失败文案常含「删除」）', () => {
    expect(
      inferFromNotices([
        { Title: '删除提示', Body: '删除失败1个' },
        { Title: '成功', Body: '已删除 2 个' }
      ]).verdict
    ).toBe('failure')
  })

  it('英文文案也能识别', () => {
    expect(inferFromNotices([{ Title: '', Body: 'Delete success' }]).verdict).toBe('success')
    expect(inferFromNotices([{ Title: '', Body: 'delete error' }]).verdict).toBe('failure')
  })

  it('无法判断的中性文案 → unknown', () => {
    expect(inferFromNotices([{ Title: '提示', Body: '已处理' }]).verdict).toBe('unknown')
  })

  it('返回拼接后的原始文案供日志使用', () => {
    const r = inferFromNotices([{ Title: 'A', Body: 'B' }])
    expect(r.message).toContain('A')
    expect(r.message).toContain('B')
  })

  it('「无法」也算失败信号', () => {
    expect(inferFromNotices([{ Title: '', Body: '无法删除该文件' }]).verdict).toBe('failure')
  })
})

describe('createGuiApiShim', () => {
  it('捕获 showNotification 的 title / body', () => {
    const sink: Array<{ Title: string; Body: string }> = []
    const shim = createGuiApiShim(sink)
    shim.showNotification({ title: 'T', body: 'B' })
    expect(sink).toEqual([{ Title: 'T', Body: 'B' }])
  })

  it('缺失字段降级为空串（不崩）', () => {
    const sink: Array<{ Title: string; Body: string }> = []
    const shim = createGuiApiShim(sink)
    shim.showNotification({})
    expect(sink).toEqual([{ Title: '', Body: '' }])
  })

  it('提供插件可能解构的其它方法（避免插件解构 undefined 抛错）', async () => {
    const shim = createGuiApiShim([])
    expect(typeof shim.copyToClipboard).toBe('function')
    expect(typeof shim.showOpenDialog).toBe('function')
    expect(typeof shim.showSaveDialog).toBe('function')
    expect(typeof shim.getClipboardFilePaths).toBe('function')
    await expect(shim.showOpenDialog()).resolves.toEqual({ canceled: true, filePaths: [] })
  })
})

function removeDeps(picgo: IPicGo, cache = new CapabilityCache()) {
  return { picgo, log, cache, timeoutMs: 50 }
}

describe('performRemove', () => {
  it('无 remove 监听器 → Supported=false（Go 据此只删本地记录）', async () => {
    const { picgo } = createFakePicgo({ removeListenerCount: 0 })
    const r = await performRemove(removeDeps(picgo), [{ fileName: 'a.png' }], 'smms', noSleep)

    expect(r.Supported).toBe(false)
    expect(r.RemoteDeleted).toBe(false)
    expect(r.Message).toContain('不支持远端删除')
  })

  it('无监听器时让能力缓存失效（下次探测会更新）', async () => {
    const cache = new CapabilityCache()
    cache.set('smms', '3.0.2', {
      SupportsPathTemplate: false,
      SupportsRemoteDelete: true, // 之前误判为支持
      ConfigFields: [],
      PathFieldNames: [],
      DetectedAt: 0,
      PicgoVersion: '3.0.2'
    })

    const { picgo } = createFakePicgo({ removeListenerCount: 0 })
    await performRemove(removeDeps(picgo, cache), [{ fileName: 'a.png' }], 'smms', noSleep)

    expect(cache.get('smms', '3.0.2')).toBeUndefined()
  })

  it('插件通知成功 → RemoteDeleted=true / Supported=true', async () => {
    const { picgo, emitter } = createFakePicgo({ removeListenerCount: 1 })
    emitter.removeAllListeners('remove')
    emitter.on('remove', (_files: unknown, guiApi: { showNotification: (n: unknown) => void }) => {
      guiApi.showNotification({ title: '删除提示', body: '成功同步删除' })
    })

    const r = await performRemove(removeDeps(picgo), [{ fileName: 'a.png' }], 'mockbed', noSleep)
    expect(r.Supported).toBe(true)
    expect(r.RemoteDeleted).toBe(true)
    expect(r.Message).toContain('成功')
  })

  it('插件通知失败 → Supported=true / RemoteDeleted=false', async () => {
    const { picgo, emitter } = createFakePicgo({ removeListenerCount: 1 })
    emitter.removeAllListeners('remove')
    emitter.on('remove', (_files: unknown, guiApi: { showNotification: (n: unknown) => void }) => {
      guiApi.showNotification({ title: '删除提示', body: '删除失败2个' })
    })

    const r = await performRemove(removeDeps(picgo), [{ fileName: 'a.png' }], 'mockbed', noSleep)
    expect(r.Supported).toBe(true)
    expect(r.RemoteDeleted).toBe(false)
    expect(r.Message).toContain('失败')
  })

  it('插件沉默（无通知）→ 结果未知，但 Supported=true 且说明清楚', async () => {
    const { picgo } = createFakePicgo({ removeListenerCount: 1 }) // 有监听器但不发通知
    const r = await performRemove(removeDeps(picgo), [{ fileName: 'a.png' }], 'mockbed', noSleep)

    expect(r.Supported).toBe(true)
    expect(r.RemoteDeleted).toBe(false)
    expect(r.Message).toContain('未知')
  })

  it('插件是 async 且稍后通知 → 能等到（因为我们会 sleep）', async () => {
    const { picgo, emitter } = createFakePicgo({ removeListenerCount: 1 })
    emitter.removeAllListeners('remove')
    emitter.on('remove', (_files: unknown, guiApi: { showNotification: (n: unknown) => void }) => {
      setTimeout(() => guiApi.showNotification({ title: '删除提示', body: '成功同步删除' }), 5)
    })

    const r = await performRemove(
      { picgo, log, cache: new CapabilityCache(), timeoutMs: 200 },
      [{ fileName: 'a.png' }],
      'mockbed',
      (ms) => new Promise((res) => setTimeout(res, ms))
    )
    expect(r.RemoteDeleted).toBe(true)
  })

  it('插件抛错 → 不把异常抛给调用方，转为可读消息', async () => {
    const { picgo, emitter } = createFakePicgo({ removeListenerCount: 1 })
    emitter.removeAllListeners('remove')
    emitter.on('remove', () => {
      throw new Error('插件内部炸了')
    })

    const r = await performRemove(removeDeps(picgo), [{ fileName: 'a.png' }], 'mockbed', noSleep)
    expect(r.Supported).toBe(true)
    expect(r.RemoteDeleted).toBe(false)
    expect(r.Message).toContain('插件内部炸了')
  })

  it('空 items → 不改远端、Supported=true（无事可做）', async () => {
    const { picgo } = createFakePicgo({ removeListenerCount: 1 })
    const r = await performRemove(removeDeps(picgo), [], 'mockbed', noSleep)
    expect(r.RemoteDeleted).toBe(false)
    expect(r.Supported).toBe(true)
  })

  it('把原样的 IImgInfo 交给插件（含插件回写的 key，删远端必需）', async () => {
    const { picgo, emitter } = createFakePicgo({ removeListenerCount: 1 })
    emitter.removeAllListeners('remove')

    let received: unknown
    emitter.on('remove', (files: unknown) => {
      received = files
    })

    const items = [{ fileName: 'img/a.png', mockKey: 'abc123', imgUrl: 'https://x/a.png' }]
    await performRemove(removeDeps(picgo), items, 'mockbed', noSleep)

    expect(received).toEqual(items)
    // 字段名保持 picgo 的原生形态（小写），未被转成 PascalCase
    expect((received as Array<Record<string, unknown>>)[0]).toHaveProperty('fileName')
    expect((received as Array<Record<string, unknown>>)[0]?.mockKey).toBe('abc123')
  })

  it('多张图一次 emit（插件自行遍历）', async () => {
    const { picgo, emitter } = createFakePicgo({ removeListenerCount: 1 })
    emitter.removeAllListeners('remove')

    let count = 0
    emitter.on('remove', (files: unknown[]) => {
      count = files.length
    })

    await performRemove(
      removeDeps(picgo),
      [{ fileName: 'a.png' }, { fileName: 'b.png' }],
      'mockbed',
      noSleep
    )
    expect(count).toBe(2)
  })
})
