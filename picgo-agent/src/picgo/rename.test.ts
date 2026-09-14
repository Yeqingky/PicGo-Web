/**
 * 魔法命名钩子的单测（D44）。
 *
 * 钩子读取 `ctx.contextData.magicPath`（由 `POST /api/upload` 注入），
 * 因此这里直接测 `applyMagicPath` 的行为 —— 它是钩子的核心。
 */

import { describe, expect, it } from 'vitest'
import type { IImgInfo, IPicGo } from '@yeqingky/picgo-core'
import { createLogger } from '../logger.js'
import { applyMagicPath, MAGIC_PATH_HOOK, readMagicPathContext, registerMagicPathHook } from './rename.js'

const log = createLogger('error')

function img(over: Partial<IImgInfo> = {}): IImgInfo {
  return { fileName: 'photo.png', extname: '.png', ...over }
}

const FIXED_NOW = new Date(2026, 1, 14, 9, 30, 15).getTime()

describe('applyMagicPath', () => {
  it('两个模板都为空时不改名（保持 picgo 默认行为）', () => {
    const output = [img()]
    applyMagicPath(output, {}, log)
    expect(output[0]?.fileName).toBe('photo.png')
  })

  it('应用文件名模板', () => {
    const output = [img()]
    applyMagicPath(output, { fileTemplate: 'x-{timestamp}{extname}' }, log, FIXED_NOW)
    expect(output[0]?.fileName).toBe(`x-${Math.floor(FIXED_NOW / 1000)}.png`)
  })

  it('应用路径模板（塞进 fileName 表达子目录）', () => {
    const output = [img()]
    applyMagicPath(
      output,
      { pathTemplate: 'img/{Y}/{m}', fileTemplate: '{filename}{extname}', supportsPathTemplate: true },
      log,
      FIXED_NOW
    )
    expect(output[0]?.fileName).toBe('img/2026/02/photo.png')
  })

  it('驱动不支持路径时降级为文件名前缀', () => {
    const output = [img()]
    applyMagicPath(
      output,
      { pathTemplate: 'img/{Y}', fileTemplate: '{filename}{extname}', supportsPathTemplate: false },
      log,
      FIXED_NOW
    )
    expect(output[0]?.fileName).toBe('img_2026_photo.png')
    expect(output[0]?.fileName).not.toContain('/')
  })

  it('多张图都改名（保持各自的扩展名）', () => {
    const output = [img({ fileName: 'a.png' }), img({ fileName: 'b.jpg', extname: '.jpg' })]
    applyMagicPath(output, { fileTemplate: '{filename}{extname}' }, log)
    expect(output[0]?.fileName).toBe('a.png')
    expect(output[1]?.fileName).toBe('b.jpg')
  })

  it('未知变量不阻断（原样保留）', () => {
    const output = [img()]
    applyMagicPath(output, { fileTemplate: 'x{unknown}{extname}' }, log)
    expect(output[0]?.fileName).toBe('x{unknown}.png')
  })

  it('fileName 缺失时用兜底名（不崩）', () => {
    const output = [img({ fileName: undefined })]
    applyMagicPath(output, { fileTemplate: '{filename}-{uniqid}{extname}' }, log, FIXED_NOW)
    expect(output[0]?.fileName).toMatch(/^image-[0-9a-f]{8}\.png$/)
  })

  it('空 output 不报错', () => {
    expect(() => applyMagicPath([], { fileTemplate: '{uniqid}' }, log)).not.toThrow()
  })

  it('非法字符被替换', () => {
    const output = [img()]
    applyMagicPath(output, { fileTemplate: 'my:file?.png' }, log)
    expect(output[0]?.fileName).toBe('my_file_.png')
  })
})

describe('readMagicPathContext', () => {
  it('从 contextData.magicPath 读取', () => {
    const ctx = {
      contextData: { magicPath: { fileTemplate: '{uniqid}', userUID: 'u1' } }
    } as unknown as IPicGo

    expect(readMagicPathContext(ctx)).toEqual({ fileTemplate: '{uniqid}', userUID: 'u1' })
  })

  it('没有 contextData 时返回 undefined', () => {
    expect(readMagicPathContext({} as unknown as IPicGo)).toBeUndefined()
  })

  it('contextData 存在但没有 magicPath 时返回 undefined', () => {
    const ctx = { contextData: { JobUID: 'j1' } } as unknown as IPicGo
    expect(readMagicPathContext(ctx)).toBeUndefined()
  })

  it('contextData 不是对象时安全返回 undefined', () => {
    expect(readMagicPathContext({ contextData: 'x' } as unknown as IPicGo)).toBeUndefined()
  })
})

describe('registerMagicPathHook', () => {
  it('注册钩子且能通过 ctx 生效（端到端的最小组件验证）', async () => {
    const registered = new Map<string, { handle: (ctx: IPicGo) => Promise<void> }>()

    const fake = {
      contextData: {},
      output: [] as IImgInfo[],
      helper: {
        beforeUploadPlugins: {
          register: (name: string, plugin: { handle: (ctx: IPicGo) => Promise<void> }) => {
            registered.set(name, plugin)
          },
          unregister: (name: string) => {
            registered.delete(name)
          }
        }
      }
    } as unknown as IPicGo

    registerMagicPathHook(fake, log)
    expect(registered.has(MAGIC_PATH_HOOK)).toBe(true)

    // 模拟一次上传：ctx 带上 magicPath + 已 transform 的 output
    const uploadCtx = {
      contextData: {
        magicPath: { pathTemplate: 'p/{Y}', fileTemplate: '{filename}{extname}', supportsPathTemplate: true }
      },
      output: [img()]
    } as unknown as IPicGo

    await registered.get(MAGIC_PATH_HOOK)!.handle(uploadCtx)
    expect((uploadCtx.output[0] as IImgInfo).fileName).toMatch(/^p\/\d{4}\/photo\.png$/)
  })

  it('重复注册时先反注册，避免同名钩子堆叠', () => {
    const calls: string[] = []
    const fake = {
      helper: {
        beforeUploadPlugins: {
          register: (name: string) => calls.push(`register:${name}`),
          unregister: (name: string) => calls.push(`unregister:${name}`)
        }
      }
    } as unknown as IPicGo

    registerMagicPathHook(fake, log)
    expect(calls).toEqual([`unregister:${MAGIC_PATH_HOOK}`, `register:${MAGIC_PATH_HOOK}`])
  })

  it('钩子在 ctx 没有 magicPath 时是 no-op（不误改别的上传）', async () => {
    const registered = new Map<string, { handle: (ctx: IPicGo) => Promise<void> }>()
    const fake = {
      helper: {
        beforeUploadPlugins: {
          register: (name: string, plugin: { handle: (ctx: IPicGo) => Promise<void> }) => {
            registered.set(name, plugin)
          },
          unregister: () => {}
        }
      }
    } as unknown as IPicGo

    registerMagicPathHook(fake, log)

    const otherCtx = { contextData: {}, output: [img()] } as unknown as IPicGo
    await registered.get(MAGIC_PATH_HOOK)!.handle(otherCtx)
    expect((otherCtx.output[0] as IImgInfo).fileName).toBe('photo.png') // 未改名
  })
})
