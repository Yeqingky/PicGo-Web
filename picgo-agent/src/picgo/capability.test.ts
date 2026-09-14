/**
 * 驱动能力探测的单测（D44 / D77.2：**不硬编码驱动名列表**）。
 *
 * 关键回归：加一个未知驱动（不带 path 字段）时不应被误判为支持路径；
 * 带 `root` / `basePath` / `prefix` 等变体字段的驱动应被正确识别。
 */

import { describe, expect, it } from 'vitest'
import type { IPluginConfig } from 'picgo'
import {
  buildCapabilities,
  CapabilityCache,
  configFieldNames,
  detectRemoteDeleteSupport,
  isGuiOnly,
  looksLikePathField,
  pathFieldNames,
  pluginProvidedUploaders,
  PATH_FIELD_CANDIDATES
} from './capability.js'
import { createFakePicgo } from '../testing/fake-picgo.js'

function field(name: string): IPluginConfig {
  return { name, type: 'input', required: false }
}

describe('looksLikePathField', () => {
  it('识别常见路径字段名', () => {
    for (const name of ['path', 'root', 'basePath', 'prefix', 'dir', 'directory', 'folder']) {
      expect(looksLikePathField(name)).toBe(true)
    }
  })

  it('大小写与分隔符不敏感', () => {
    for (const name of ['PATH', 'Path', 'base_path', 'base-path', 'BASEDIR', 'PathPrefix']) {
      expect(looksLikePathField(name)).toBe(true)
    }
  })

  it('非路径字段返回 false', () => {
    for (const name of ['token', 'repo', 'bucket', 'secretKey', 'endpoint', 'url', 'accessKeyId']) {
      expect(looksLikePathField(name)).toBe(false)
    }
  })

  it('候选集里都是能被识别的名字（自洽性）', () => {
    for (const name of PATH_FIELD_CANDIDATES) {
      expect(looksLikePathField(name)).toBe(true)
    }
  })
})

describe('configFieldNames / pathFieldNames', () => {
  it('提取字段名并过滤出路径字段', () => {
    const schema: IPluginConfig[] = [
      field('repo'),
      field('branch'),
      field('token'),
      field('path'),
      field('customUrl')
    ]
    expect(configFieldNames(schema)).toEqual(['repo', 'branch', 'token', 'path', 'customUrl'])
    expect(pathFieldNames(configFieldNames(schema))).toEqual(['path'])
  })

  it('跳过没有 name 的字段', () => {
    const schema = [
      { type: 'input', required: false },
      field('root')
    ] as IPluginConfig[]
    expect(configFieldNames(schema)).toEqual(['root'])
  })

  it('没有任何路径字段时返回空数组', () => {
    expect(pathFieldNames(configFieldNames([field('token')]))).toEqual([])
  })
})

describe('buildCapabilities', () => {
  it('有 path 字段 → SupportsPathTemplate=true', () => {
    const caps = buildCapabilities({
      schema: [field('repo'), field('path')],
      builtin: true,
      guiOnly: false,
      supportsRemoteDelete: false,
      picgoVersion: '3.0.2',
      now: 1000
    })
    expect(caps.SupportsPathTemplate).toBe(true)
    expect(caps.PathFieldNames).toEqual(['path'])
    expect(caps.ConfigFields).toEqual(['repo', 'path'])
    expect(caps.PicgoVersion).toBe('3.0.2')
    expect(caps.DetectedAt).toBe(1000)
  })

  it('没有路径字段 → SupportsPathTemplate=false（smms / imgur 这类）', () => {
    const caps = buildCapabilities({
      schema: [field('token'), field('backupDomain')],
      builtin: true,
      guiOnly: false,
      supportsRemoteDelete: false,
      picgoVersion: '3.0.2',
      now: 1000
    })
    expect(caps.SupportsPathTemplate).toBe(false)
    expect(caps.PathFieldNames).toEqual([])
  })

  it('支持多个路径字段（root + prefix）', () => {
    const caps = buildCapabilities({
      schema: [field('root'), field('prefix')],
      builtin: false,
      guiOnly: false,
      supportsRemoteDelete: true,
      picgoVersion: '3.0.2',
      now: 1000
    })
    expect(caps.SupportsPathTemplate).toBe(true)
    expect(caps.PathFieldNames).toEqual(['root', 'prefix'])
  })

  it('无 schema 时不崩（返回 false）', () => {
    const caps = buildCapabilities({
      schema: [],
      builtin: true,
      guiOnly: false,
      supportsRemoteDelete: false,
      picgoVersion: '3.0.2',
      now: 0
    })
    expect(caps.SupportsPathTemplate).toBe(false)
    expect(caps.ConfigFields).toEqual([])
  })
})

describe('detectRemoteDeleteSupport', () => {
  it('没有 remove 监听 → false', () => {
    const { picgo } = createFakePicgo({ removeListenerCount: 0 })
    expect(detectRemoteDeleteSupport(picgo)).toBe(false)
  })

  it('有 remove 监听 → true（插件实现了远端删除）', () => {
    const { picgo } = createFakePicgo({ removeListenerCount: 2 })
    expect(detectRemoteDeleteSupport(picgo)).toBe(true)
  })

  it('picgo 异常时保守返回 false（不抛错）', () => {
    const broken = {
      listenerCount: () => {
        throw new Error('boom')
      }
    } as never
    expect(detectRemoteDeleteSupport(broken)).toBe(false)
  })
})

describe('pluginProvidedUploaders —— 不硬编码内置驱动名', () => {
  it('插件声明的 uploader 被识别为插件提供（→ Builtin=false）', () => {
    const { picgo } = createFakePicgo({
      uploaders: ['github', 'smms', 'mockbed'],
      plugins: [{ name: 'picgo-plugin-mockbed', uploader: 'mockbed' }]
    })
    const provided = pluginProvidedUploaders(picgo)
    expect(provided.has('mockbed')).toBe(true)
    expect(provided.has('github')).toBe(false)
    expect(provided.has('smms')).toBe(false)
  })

  it('没有插件时为空集（全部视为内置）', () => {
    const { picgo } = createFakePicgo({ uploaders: ['github'] })
    expect(pluginProvidedUploaders(picgo).size).toBe(0)
  })

  it('插件系统异常时返回空集（不抛错）', () => {
    const broken = {
      pluginLoader: {
        getFullList: () => {
          throw new Error('boom')
        }
      }
    } as never
    expect(pluginProvidedUploaders(broken).size).toBe(0)
  })
})

describe('isGuiOnly', () => {
  it('含 guiMenu → true', () => {
    expect(isGuiOnly({ guiMenu: () => [] })).toBe(true)
  })

  it('含 commands → true', () => {
    expect(isGuiOnly({ commands: () => [] })).toBe(true)
  })

  it('都没有 → false', () => {
    expect(isGuiOnly({ uploader: 'x' })).toBe(false)
  })

  it('undefined → false', () => {
    expect(isGuiOnly(undefined)).toBe(false)
  })
})

describe('CapabilityCache', () => {
  const caps = (v: string) => ({
    SupportsPathTemplate: true,
    SupportsRemoteDelete: false,
    ConfigFields: [],
    PathFieldNames: [],
    DetectedAt: 0,
    PicgoVersion: v
  })

  it('按 type + 版本缓存', () => {
    const cache = new CapabilityCache()
    cache.set('github', '3.0.2', caps('3.0.2'))
    expect(cache.get('github', '3.0.2')).toBeTruthy()
    // 版本不同视为不同键（插件装卸后版本可能变）
    expect(cache.get('github', '3.0.1')).toBeUndefined()
    expect(cache.get('smms', '3.0.2')).toBeUndefined()
  })

  it('invalidateType 只清指定类型', () => {
    const cache = new CapabilityCache()
    cache.set('github', '3.0.2', caps('3.0.2'))
    cache.set('smms', '3.0.2', caps('3.0.2'))
    cache.invalidateType('github')
    expect(cache.get('github', '3.0.2')).toBeUndefined()
    expect(cache.get('smms', '3.0.2')).toBeTruthy()
  })

  it('invalidate 清空全部', () => {
    const cache = new CapabilityCache()
    cache.set('github', '3.0.2', caps('3.0.2'))
    cache.set('smms', '3.0.2', caps('3.0.2'))
    cache.invalidate()
    expect(cache.size()).toBe(0)
  })
})
