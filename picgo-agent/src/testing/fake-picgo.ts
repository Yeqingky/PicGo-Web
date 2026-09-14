/**
 * 测试用的假 PicGo 实例。
 *
 * 不引入真实的 picgo-core：agent 的单元测试关心的是**我们自己的逻辑**
 * （失败语义归一化、键级合并、能力探测），这些都可以用可控的假对象精确验证。
 * 真实的端到端验证由 `README.md` 里记录的 curl 脚本承担。
 */

import { EventEmitter } from 'node:events'
import type { IPicGo } from 'picgo'

export interface FakeUploadBehavior {
  /** 返回的 IImgInfo[]（默认返回一张成功的图）。 */
  output?: unknown
  /** 是否抛出（模拟「目标非法」路径）。 */
  throwError?: Error
  /** 在返回前 emit 的错误（模拟「上传失败但返回空数组」路径）。 */
  emitFailed?: unknown
  /** 模拟耗时（毫秒）。 */
  delayMs?: number
}

export interface FakePicgoOptions {
  config?: Record<string, unknown>
  uploaders?: string[]
  transformers?: string[]
  /** 上传行为；按调用序号取值，用完后沿用最后一个。 */
  uploads?: FakeUploadBehavior[]
  plugins?: Array<{ name: string; uploader?: string; transformer?: string; guiMenu?: boolean }>
  removeListenerCount?: number
  uploaderConfigs?: Record<string, unknown[]>
}

export interface FakePicgo {
  picgo: IPicGo
  /** 收到的 upload 调用参数。 */
  calls: Array<{ paths: string[]; options: unknown }>
  /** 收到的 saveConfig 调用。 */
  saved: Array<Record<string, unknown>>;
  /** 收到的 setConfig 调用。 */
  set: Array<Record<string, unknown>>
  emitter: EventEmitter
}

function clone<T>(v: T): T {
  return JSON.parse(JSON.stringify(v)) as T
}

export function createFakePicgo(options: FakePicgoOptions = {}): FakePicgo {
  const emitter = new EventEmitter()
  const calls: Array<{ paths: string[]; options: unknown }> = []
  const saved: Array<Record<string, unknown>> = []
  const setCalls: Array<Record<string, unknown>> = []

  // 浅层 config（够用于我们现在测的点路径）
  const config: Record<string, unknown> = clone(options.config ?? { picBed: {}, picgoPlugins: {} })

  const getConfig = (path?: string): unknown => {
    if (path === undefined) return config
    return path.split('.').reduce<unknown>((acc, key) => {
      if (acc === null || acc === undefined || typeof acc !== 'object') return undefined
      return (acc as Record<string, unknown>)[key]
    }, config)
  }

  const setPath = (path: string, value: unknown): void => {
    const parts = path.split('.')
    let cursor: Record<string, unknown> = config
    for (let i = 0; i < parts.length - 1; i += 1) {
      const key = parts[i] as string
      const next = cursor[key]
      if (next === null || next === undefined || typeof next !== 'object') {
        cursor[key] = {}
      }
      cursor = cursor[key] as Record<string, unknown>
    }
    cursor[parts[parts.length - 1] as string] = value
  }

  const uploads = options.uploads ?? [{}]

  const uploaderTypes = options.uploaders ?? ['github', 'smms']
  const transformerTypes = options.transformers ?? ['path', 'base64']

  const picgo = {
    VERSION: '3.0.2',
    configPath: '/tmp/fake/config.json',
    baseDir: '/tmp/fake',
    output: [],
    input: [],

    // ---- 配置（含内存版 setConfig：只改内存、不落盘）----
    getConfig,
    saveConfig: (patch: Record<string, unknown>) => {
      saved.push(clone(patch))
      for (const [k, v] of Object.entries(patch)) setPath(k, v)
    },
    setConfig: (patch: Record<string, unknown>) => {
      setCalls.push(clone(patch))
      for (const [k, v] of Object.entries(patch)) setPath(k, v)
    },

    // ---- 事件（透传 EventEmitter，便于测 failed / remove 监听）----
    on: emitter.on.bind(emitter),
    off: emitter.off.bind(emitter),
    once: emitter.once.bind(emitter),
    emit: emitter.emit.bind(emitter),
    listenerCount: emitter.listenerCount.bind(emitter),
    removeAllListeners: emitter.removeAllListeners.bind(emitter),

    // ---- helper（uploader / transformer 注册表）----
    helper: {
      uploader: {
        getIdList: () => [...uploaderTypes],
        get: (type: string) =>
          uploaderTypes.includes(type)
            ? {
                name: type.toUpperCase(),
                handle: async () => {},
                config: () => [
                  { name: 'repo', type: 'input', required: true, default: '' },
                  { name: 'path', type: 'input', required: false, default: '' }
                ]
              }
            : undefined
      },
      transformer: {
        getIdList: () => [...transformerTypes],
        get: (type: string) => (transformerTypes.includes(type) ? { name: type } : undefined)
      },
      beforeUploadPlugins: {
        register: () => {},
        unregister: () => {},
        getList: () => [],
        getIdList: () => [],
        getName: () => 'beforeUploadPlugins'
      },
      beforeTransformPlugins: {
        register: () => {},
        unregister: () => {},
        getList: () => [],
        getIdList: () => [],
        getName: () => 'beforeTransformPlugins'
      },
      afterUploadPlugins: {
        register: () => {},
        unregister: () => {},
        getList: () => [],
        getIdList: () => [],
        getName: () => 'afterUploadPlugins'
      },
      afterFinishPlugins: {
        register: () => {},
        unregister: () => {},
        getList: () => [],
        getIdList: () => [],
        getName: () => 'afterFinishPlugins'
      }
    },

    // ---- 上传：**精确复刻 picgo 的失败语义** ----
    upload: async (paths: string[], opts?: unknown) => {
      calls.push({ paths, options: opts })

      const behavior = uploads[Math.min(calls.length - 1, uploads.length - 1)] ?? {}

      if (behavior.delayMs) {
        await new Promise((r) => setTimeout(r, behavior.delayMs))
      }

      // 路径 1：目标非法 → 抛出（picgo 补丁的前置校验行为）
      if (behavior.throwError) {
        throw behavior.throwError
      }

      // 路径 2：上传失败 → emit failed 并返回**可能为空的数组**（不抛错、不返回 Error）
      if (behavior.emitFailed !== undefined) {
        emitter.emit('failed', behavior.emitFailed)
        return []
      }

      if (behavior.output !== undefined) {
        return behavior.output
      }

      // 默认：成功
      return [
        {
          fileName: 'ok.png',
          extname: '.png',
          imgUrl: 'https://cdn.test/ok.png',
          width: 10,
          height: 20,
          size: 123,
          contentType: 'image/png',
          type: 'github'
        }
      ]
    },

    // ---- 插件 ----
    pluginLoader: {
      getFullList: () => (options.plugins ?? []).map((p) => p.name),
      getList: () => (options.plugins ?? []).map((p) => p.name),
      getPlugin: (name: string) => {
        const found = (options.plugins ?? []).find((p) => p.name === name)
        if (!found) return undefined
        return {
          uploader: found.uploader ?? '',
          transformer: found.transformer ?? '',
          ...(found.guiMenu ? { guiMenu: () => [] } : {})
        }
      },
      hasPlugin: () => false,
      registerPlugin: () => {},
      unregisterPlugin: () => {}
    },
    pluginHandler: {
      install: async () => ({ success: true, body: ['picgo-plugin-x'] }),
      update: async () => ({ success: true, body: ['picgo-plugin-x'] }),
      uninstall: async () => ({ success: true, body: ['picgo-plugin-x'] })
    },

    // ---- uploaderConfig ----
    uploaderConfig: {
      listUploaderTypes: () => [...uploaderTypes],
      getConfigList: (type: string) => options.uploaderConfigs?.[type] ?? [],
      getActiveConfig: (type: string) => (options.uploaderConfigs?.[type] ?? [])[0],
      use: (type: string, configName?: string) => ({
        _id: 'st_fake',
        _configName: configName ?? 'Default',
        _createdAt: 0,
        _updatedAt: 0,
        type
      }),
      createOrUpdate: (type: string, configName?: string, patch?: Record<string, unknown>) => ({
        ...(patch ?? {}),
        _id: `st_${type}`,
        _configName: configName ?? 'Default',
        _createdAt: 0,
        _updatedAt: 0
      }),
      copy: () => ({ _id: 'x', _configName: 'x', _createdAt: 0, _updatedAt: 0 }),
      rename: () => ({ _id: 'x', _configName: 'x', _createdAt: 0, _updatedAt: 0 }),
      remove: () => {}
    },

    log: {
      info: () => {},
      warn: () => {},
      error: () => {},
      success: () => {},
      debug: () => {},
      createLogger: () => ({
        info: () => {},
        warn: () => {},
        error: () => {},
        success: () => {},
        debug: () => {}
      })
    }
  } as unknown as IPicGo

  // 预置 remove 监听器（模拟有插件实现了远端删除）
  const removeCount = options.removeListenerCount ?? 0
  for (let i = 0; i < removeCount; i += 1) {
    emitter.on('remove', () => {})
  }

  return { picgo, calls, saved, set: setCalls, emitter }
}
