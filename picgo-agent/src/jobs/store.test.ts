/**
 * 内存任务表与日志环形缓冲的单测。
 *
 * ⚠️ 记住 agent 的任务**只是执行期状态**（进程重启即丢），
 * 持久化的 `Jobs` 在 Go 侧。因此这里的测试关注：
 * - 状态流转（queued → running → succeeded/failed）
 * - 日志的环形缓冲与增量拉取语义
 * - 已完成任务的清理与保留期 GC
 */

import { describe, expect, it, vi } from 'vitest'
import { JobStore, newJobUID } from './store.js'

describe('newJobUID', () => {
  it('带 job_ 前缀且唯一', () => {
    const ids = new Set<string>()
    for (let i = 0; i < 2000; i += 1) {
      const id = newJobUID()
      expect(id.startsWith('job_')).toBe(true)
      ids.add(id)
    }
    expect(ids.size).toBe(2000)
  })
})

describe('JobStore 状态流转', () => {
  it('create → queued', () => {
    const store = new JobStore()
    const uid = store.create('plugin.install', { Names: ['x'] })
    const job = store.get(uid)!
    expect(job.Status).toBe('queued')
    expect(job.Kind).toBe('plugin.install')
    expect(job.Progress).toBe(0)
    expect(job.Payload).toEqual({ Names: ['x'] })
    expect(job.StartedAt).toBe(0)
    expect(job.FinishedAt).toBe(0)
    expect(job.Result).toBeNull()
  })

  it('start → running 并记录 StartedAt', () => {
    const store = new JobStore()
    const uid = store.create('upload')
    store.start(uid)
    const job = store.get(uid)!
    expect(job.Status).toBe('running')
    expect(job.StartedAt).toBeGreaterThan(0)
  })

  it('succeed → succeeded / Progress=100 / Result', () => {
    const store = new JobStore()
    const uid = store.create('upload')
    store.succeed(uid, { Ok: true })
    const job = store.get(uid)!
    expect(job.Status).toBe('succeeded')
    expect(job.Progress).toBe(100)
    expect(job.Result).toEqual({ Ok: true })
    expect(job.FinishedAt).toBeGreaterThan(0)
  })

  it('fail → failed / Error / Result', () => {
    const store = new JobStore()
    const uid = store.create('upload')
    store.fail(uid, '网络错误', { Partial: true })
    const job = store.get(uid)!
    expect(job.Status).toBe('failed')
    expect(job.Error).toBe('网络错误')
    expect(job.Result).toEqual({ Partial: true })
  })

  it('setProgress 会被限制在 0..100 并取整', () => {
    const store = new JobStore()
    const uid = store.create('upload')
    store.setProgress(uid, 55.6)
    expect(store.get(uid)!.Progress).toBe(56)
    store.setProgress(uid, -10)
    expect(store.get(uid)!.Progress).toBe(0)
    store.setProgress(uid, 999)
    expect(store.get(uid)!.Progress).toBe(100)
  })

  it('操作不存在的 job 不抛错', () => {
    const store = new JobStore()
    expect(() => {
      store.start('nope')
      store.setProgress('nope', 50)
      store.succeed('nope')
      store.fail('nope', 'x')
      store.appendLog('nope', 'line')
    }).not.toThrow()
    expect(store.get('nope')).toBeUndefined()
  })

  it('list 按创建时间倒序（新的在前）', () => {
    vi.useFakeTimers()
    try {
      const store = new JobStore()
      vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
      const a = store.create('a')
      vi.setSystemTime(new Date('2026-01-01T00:00:10Z'))
      const b = store.create('b')

      const list = store.list()
      expect(list[0]?.UID).toBe(b)
      expect(list[1]?.UID).toBe(a)
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('JobStore 日志与环形缓冲', () => {
  it('追加日志返回递增序号', () => {
    const store = new JobStore()
    const uid = store.create('x')
    expect(store.appendLog(uid, 'l0')).toBe(0)
    expect(store.appendLog(uid, 'l1')).toBe(1)
    expect(store.appendLog(uid, 'l2')).toBe(2)
  })

  it('logs() 返回全部', () => {
    const store = new JobStore()
    const uid = store.create('x')
    store.appendLog(uid, 'a')
    store.appendLog(uid, 'b')
    const r = store.logs(uid)!
    expect(r.lines).toEqual(['a', 'b'])
    expect(r.startSeq).toBe(0)
    expect(r.nextSeq).toBe(2)
  })

  it('logs(afterSeq) 只返回增量（供长任务轮询）', () => {
    const store = new JobStore()
    const uid = store.create('x')
    store.appendLog(uid, 'a')
    store.appendLog(uid, 'b')
    store.appendLog(uid, 'c')

    expect(store.logs(uid, 0)!.lines).toEqual(['b', 'c'])
    expect(store.logs(uid, 2)!.lines).toEqual([])
  })

  it('超出容量时丢弃最旧的，并推进 startSeq（增量拉取仍可对账）', () => {
    const store = new JobStore(3) // 只保留 3 行
    const uid = store.create('x')
    for (let i = 0; i < 6; i += 1) store.appendLog(uid, `l${i}`)

    const r = store.logs(uid)!
    expect(r.lines).toEqual(['l3', 'l4', 'l5'])
    expect(r.startSeq).toBe(3) // 前 3 行已被丢弃
    expect(r.nextSeq).toBe(6)

    // 客户端拿着旧的 afterSeq 拉取，仍然拿到「还在缓冲里的」部分（不报错）
    expect(store.logs(uid, 0)!.lines).toEqual(['l3', 'l4', 'l5'])
  })

  it('不存在的 job 返回 undefined', () => {
    const store = new JobStore()
    expect(store.logs('nope')).toBeUndefined()
    expect(store.appendLog('nope', 'x')).toBeUndefined()
  })
})

describe('JobStore 清理', () => {
  it('remove 只能删已完成的（避免误清在跑的任务）', () => {
    const store = new JobStore()
    const uid = store.create('x')

    expect(store.remove(uid)).toBe(false) // queued
    store.start(uid)
    expect(store.remove(uid)).toBe(false) // running

    store.succeed(uid)
    expect(store.remove(uid)).toBe(true)
    expect(store.get(uid)).toBeUndefined()
  })

  it('clearFinished 只清已完成，返回数量', () => {
    const store = new JobStore()
    const running = store.create('a')
    store.start(running)
    const ok = store.create('b')
    store.succeed(ok)
    const bad = store.create('c')
    store.fail(bad, 'x')

    expect(store.clearFinished()).toBe(2)
    expect(store.get(running)).toBeTruthy()
    expect(store.get(ok)).toBeUndefined()
    expect(store.get(bad)).toBeUndefined()
  })

  it('create 时按保留期 GC 掉过老的已完成任务', () => {
    vi.useFakeTimers()
    try {
      const store = new JobStore(500, 60_000) // 保留 60 秒
      vi.setSystemTime(new Date('2026-01-01T00:00:00Z'))
      const old = store.create('x')
      store.succeed(old)

      // 70 秒后创建新任务 → 触发 GC
      vi.setSystemTime(new Date('2026-01-01T00:01:10Z'))
      const fresh = store.create('y')

      expect(store.get(old)).toBeUndefined()
      expect(store.get(fresh)).toBeTruthy()
    } finally {
      vi.useRealTimers()
    }
  })

  it('stats 统计在跑与总数', () => {
    const store = new JobStore()
    const a = store.create('a')
    store.start(a)
    const b = store.create('b')
    store.succeed(b)

    expect(store.stats()).toEqual({ total: 2, running: 1 })
  })
})
