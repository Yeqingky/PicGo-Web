/**
 * 上传结果归一化的单测（D39 的**核心风险点**）。
 *
 * 这些用例复刻了实测的 picgo 行为：
 *
 * 1. 上传失败 → **既不 reject 也不返回 Error**，而是返回空数组 + `emit('failed')`
 * 2. 目标非法 → **抛出**（我们补丁的前置校验）
 * 3. 返回的数组里可能**没有 imgUrl**（picgo 把 output 原样返回）
 *
 * 三种都必须被正确归一化，否则 Go 侧会把失败当成功（或反之）。
 */

import { describe, expect, it } from 'vitest'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { createLogger } from '../logger.js'
import { performUpload, toUploadData, UploadGate } from './upload.js'
import { createFakePicgo } from '../testing/fake-picgo.js'

const log = createLogger('error') // 测试时静默

function tmpFile(name = 'a.png', content = 'fake-image'): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'up-'))
  const file = path.join(dir, name)
  fs.writeFileSync(file, content)
  return file
}

describe('performUpload —— 成功路径', () => {
  it('返回 URL / 尺寸 / Raw', async () => {
    const { picgo } = createFakePicgo({})
    const out = await performUpload(
      { picgo, log, gate: new UploadGate(1) },
      { Path: tmpFile(), Seq: 7, Uploader: { Type: 'github' } }
    )

    expect(out.ok).toBe(true)
    expect(out.kind).toBe('picgo')
    expect(out.data?.Seq).toBe(7)
    expect(out.data?.URL).toBe('https://cdn.test/ok.png')
    expect(out.data?.Width).toBe(10)
    expect(out.data?.Height).toBe(20)
    expect(out.data?.Size).toBe(123)
    expect(out.data?.ContentType).toBe('image/png')
    // ⚠️ Raw 必须是 picgo 的原生形态（小写字段名），供 Go 原样存 RawOutput
    expect(out.data?.Raw).toBeTruthy()
    expect(Object.keys(out.data?.Raw ?? {})).toContain('fileName')
    expect(out.data?.Raw?.fileName).toBe('ok.png')
  })

  it('把 Uploader 的 PascalCase 转成 picgo 的 camelCase', async () => {
    const { picgo, calls } = createFakePicgo({})
    await performUpload(
      { picgo, log, gate: new UploadGate(1) },
      { Path: tmpFile(), Uploader: { Type: 'github', ConfigName: 'Work' }, JobUID: 'job_1', Seq: 3 }
    )

    const opts = calls[0]?.options as {
      uploader?: { type?: string; configName?: string }
      contextData?: { JobUID?: string; Seq?: number }
    }
    // ⚠️ 必须是 camelCase，否则 picgo 匹配不到配置
    expect(opts.uploader).toEqual({ type: 'github', configName: 'Work' })
    // contextData 用于事件归属与魔法命名
    expect(opts.contextData?.JobUID).toBe('job_1')
    expect(opts.contextData?.Seq).toBe(3)
    expect(opts.contextData).toHaveProperty('magicPath')
  })

  it('省略 ConfigName 时不传 configName 字段', async () => {
    const { picgo, calls } = createFakePicgo({})
    await performUpload(
      { picgo, log, gate: new UploadGate(1) },
      { Path: tmpFile(), Uploader: { Type: 'github' } }
    )
    const opts = calls[0]?.options as { uploader?: unknown }
    expect(opts.uploader).toEqual({ type: 'github' })
  })

  it('不带 Uploader 时不传 uploader 选项（走当前激活配置）', async () => {
    const { picgo, calls } = createFakePicgo({})
    await performUpload({ picgo, log, gate: new UploadGate(1) }, { Path: tmpFile() })
    const opts = calls[0]?.options as { uploader?: unknown }
    expect(opts.uploader).toBeUndefined()
  })

  it('把魔法命名参数放进 contextData.magicPath', async () => {
    const { picgo, calls } = createFakePicgo({})
    await performUpload(
      { picgo, log, gate: new UploadGate(1) },
      {
        Path: tmpFile(),
        PathTemplate: 'img/{Y}',
        FileTemplate: '{uniqid}{extname}',
        UserUID: 'usr_9',
        SupportsPathTemplate: true
      }
    )
    const opts = calls[0]?.options as {
      contextData?: { magicPath?: Record<string, unknown> }
    }
    expect(opts.contextData?.magicPath).toEqual({
      pathTemplate: 'img/{Y}',
      fileTemplate: '{uniqid}{extname}',
      userUID: 'usr_9',
      supportsPathTemplate: true
    })
  })

  it('从多个返回值中挑出第一个带 imgUrl 的', async () => {
    const { picgo } = createFakePicgo({
      uploads: [
        {
          output: [
            { fileName: 'no-url.png' },
            { fileName: 'has-url.png', imgUrl: 'https://cdn.test/2.png', extname: '.png' }
          ]
        }
      ]
    })
    const out = await performUpload({ picgo, log, gate: new UploadGate(1) }, { Path: tmpFile() })
    expect(out.ok).toBe(true)
    expect(out.data?.URL).toBe('https://cdn.test/2.png')
  })
})

describe('performUpload —— 失败路径 1：上传失败（返回空数组 + emit failed）', () => {
  it('空数组 → ok=false / kind=picgo / 带上 failed 事件里的原因', async () => {
    const { picgo } = createFakePicgo({
      uploads: [{ emitFailed: { statusCode: 422, body: { message: '仓库不存在' } } }]
    })

    const out = await performUpload({ picgo, log, gate: new UploadGate(1) }, { Path: tmpFile(), Seq: 1 })

    expect(out.ok).toBe(false)
    expect(out.kind).toBe('picgo') // ← 上传失败（不是调用方错误）
    expect(out.data).toBeNull()
    // ⚠️ 必须提取出可读原因，不能是 "[object Object]"
    expect(out.error).toContain('仓库不存在')
    expect(out.error).not.toBe('[object Object]')
  })

  it('数组里的元素没有 imgUrl → 也算失败', async () => {
    const { picgo } = createFakePicgo({ uploads: [{ output: [{ fileName: 'x.png' }] }] })
    const out = await performUpload({ picgo, log, gate: new UploadGate(1) }, { Path: tmpFile() })
    expect(out.ok).toBe(false)
    expect(out.kind).toBe('picgo')
  })

  it('返回的数组为空且没有 failed 事件 → 仍判失败并给出兜底文案', async () => {
    const { picgo } = createFakePicgo({ uploads: [{ output: [] }] })
    const out = await performUpload({ picgo, log, gate: new UploadGate(1) }, { Path: tmpFile() })
    expect(out.ok).toBe(false)
    expect(out.error).not.toBe('')
    expect(out.error).toContain('图床未返回图片地址')
  })

  it('返回 Error 实例 → 判失败', async () => {
    const { picgo } = createFakePicgo({ uploads: [{ output: new Error('boom') }] })
    const out = await performUpload({ picgo, log, gate: new UploadGate(1) }, { Path: tmpFile() })
    expect(out.ok).toBe(false)
    expect(out.kind).toBe('picgo')
    expect(out.error).toContain('boom')
  })

  it('失败后必须解绑 failed 监听（不能泄漏）', async () => {
    const { picgo, emitter } = createFakePicgo({ uploads: [{ emitFailed: 'x' }] })
    const before = emitter.listenerCount('failed')
    await performUpload({ picgo, log, gate: new UploadGate(1) }, { Path: tmpFile() })
    expect(emitter.listenerCount('failed')).toBe(before)
  })
})

describe('performUpload —— 失败路径 2：目标非法（picgo 抛错）', () => {
  it('抛错 → ok=false / kind=param（映射为 HTTP 400）', async () => {
    const { picgo } = createFakePicgo({
      uploads: [{ throwError: new Error('Uploader type "nosuchtype" not found') }]
    })

    const out = await performUpload(
      { picgo, log, gate: new UploadGate(1) },
      { Path: tmpFile(), Uploader: { Type: 'nosuchtype' } }
    )

    expect(out.ok).toBe(false)
    expect(out.kind).toBe('param') // ← 调用方错误，应回 400
    expect(out.error).toContain('nosuchtype')
  })

  it('抛错路径与上传失败路径必须可区分（前者 param，后者 picgo）', async () => {
    const a = createFakePicgo({ uploads: [{ throwError: new Error('target bad') }] })
    const b = createFakePicgo({ uploads: [{ emitFailed: 'upload bad' }] })

    const outA = await performUpload({ picgo: a.picgo, log, gate: new UploadGate(1) }, { Path: tmpFile() })
    const outB = await performUpload({ picgo: b.picgo, log, gate: new UploadGate(1) }, { Path: tmpFile() })

    expect(outA.kind).toBe('param')
    expect(outB.kind).toBe('picgo')
  })
})

describe('performUpload —— 前置校验', () => {
  it('路径为空 → param', async () => {
    const { picgo, calls } = createFakePicgo({})
    const out = await performUpload({ picgo, log, gate: new UploadGate(1) }, { Path: '' })
    expect(out.kind).toBe('param')
    expect(out.error).toContain('缺少文件路径')
    expect(calls).toHaveLength(0) // 不应调用 picgo
  })

  it('文件不存在 → param，且不调用 picgo', async () => {
    const { picgo, calls } = createFakePicgo({})
    const out = await performUpload({ picgo, log, gate: new UploadGate(1) }, { Path: '/no/such/file.png' })
    expect(out.kind).toBe('param')
    expect(out.error).toContain('文件不存在')
    expect(calls).toHaveLength(0)
  })
})

describe('UploadGate —— 串行化（保证事件归属正确）', () => {
  it('并发度 1 时严格串行', async () => {
    const gate = new UploadGate(1)
    const order: string[] = []

    const task = async (name: string): Promise<void> => {
      await gate.acquire()
      try {
        order.push(`${name}:in`)
        await new Promise((r) => setTimeout(r, 10))
        order.push(`${name}:out`)
      } finally {
        gate.release()
      }
    }

    await Promise.all([task('a'), task('b'), task('c')])

    // 严格交替，说明没有重叠
    expect(order).toEqual(['a:in', 'a:out', 'b:in', 'b:out', 'c:in', 'c:out'])
  })

  it('并发度 2 时允许两路并行', async () => {
    const gate = new UploadGate(2)
    let peak = 0
    let running = 0

    const task = async (): Promise<void> => {
      await gate.acquire()
      running += 1
      peak = Math.max(peak, running)
      await new Promise((r) => setTimeout(r, 10))
      running -= 1
      gate.release()
    }

    await Promise.all([task(), task(), task(), task()])
    expect(peak).toBe(2)
  })

  it('并发上传到不同配置时互不阻塞（走补丁路径）', async () => {
    const { picgo, calls } = createFakePicgo({
      uploads: [{ delayMs: 20 }, { delayMs: 20 }]
    })
    const gate = new UploadGate(2)

    const started = Date.now()
    await Promise.all([
      performUpload({ picgo, log, gate }, { Path: tmpFile('a.png'), Uploader: { Type: 'github', ConfigName: 'A' } }),
      performUpload({ picgo, log, gate }, { Path: tmpFile('b.png'), Uploader: { Type: 'github', ConfigName: 'B' } })
    ])
    const elapsed = Date.now() - started

    // 并行时约 20ms，串行时约 40ms
    expect(elapsed).toBeLessThan(38)
    expect(calls).toHaveLength(2)
  })
})

describe('toUploadData', () => {
  it('把 IImgInfo 映射为 PascalCase 响应，并保留 Raw 原样', () => {
    const raw = {
      fileName: 'a.png',
      extname: '.png',
      imgUrl: 'https://cdn/a.png',
      width: 100,
      height: 200,
      size: 999,
      contentType: 'image/png',
      type: 'github',
      sha: 'abc123'
    }
    const data = toUploadData(raw, 5, 'fallback')

    expect(data.Seq).toBe(5)
    expect(data.URL).toBe('https://cdn/a.png')
    expect(data.FileName).toBe('a.png')
    expect(data.Extname).toBe('.png')
    expect(data.Width).toBe(100)
    expect(data.Height).toBe(200)
    expect(data.Size).toBe(999)
    expect(data.ContentType).toBe('image/png')
    expect(data.UploaderType).toBe('github')
    // Raw 原样（含插件回写字段 sha）
    expect(data.Raw).toBe(raw)
    expect(data.Raw?.sha).toBe('abc123')
  })

  it('缺失字段降级为 0 / 空串', () => {
    const data = toUploadData({}, 0, 'github')
    expect(data.URL).toBe('')
    expect(data.Width).toBe(0)
    expect(data.Size).toBe(0)
    expect(data.UploaderType).toBe('github') // 回退到调用方给的类型
  })

  it('字符串型宽高能被解析（picgo 某些驱动返回字符串）', () => {
    const data = toUploadData(
      { imgUrl: 'u', width: '800' as unknown as number, height: '600' as unknown as number },
      0,
      'x'
    )
    expect(data.Width).toBe(800)
    expect(data.Height).toBe(600)
  })
})
