/**
 * config 键级合并的单测（D22 —— **最容易造成插件账本损坏的地方**）。
 *
 * 核心不变量：
 * 1. 只允许写 `picBed.*` / `uploader.*` / `picgoPlugins.*` / `settings.*`
 * 2. 其余键（插件私有状态，如 `uploaded`）**必须原样保留、不得删除**
 * 3. `PUT` 也走同一套过滤（不是真的整体替换），并拒绝不含管辖键的请求体
 * 4. 写前备份（轮转保留 N 份）
 */

import { describe, expect, it } from 'vitest'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { createLogger } from '../logger.js'
import type { AgentEnv } from '../env.js'
import {
  backupConfig,
  isManagedKey,
  MANAGED_ROOTS,
  patchConfig,
  pluginPrivateKeys,
  putConfig,
  readConfig,
  rootOf
} from './config.js'
import { createFakePicgo } from '../testing/fake-picgo.js'

const log = createLogger('error')

function makeEnv(dir: string): AgentEnv {
  return {
    Port: 36678,
    Host: '127.0.0.1',
    Token: 't',
    ConfigPath: path.join(dir, 'config.json'),
    AllowNoToken: false,
    NpmRegistry: '',
    NpmProxy: '',
    UploadProxy: '',
    ConfigBackupCount: 3,
    RemoveTimeoutMs: 100,
    LogLevel: 'error'
  }
}

function tmpEnv(): AgentEnv {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'cfg-'))
  const env = makeEnv(dir)
  fs.writeFileSync(env.ConfigPath, JSON.stringify({ picBed: {} }, null, 2))
  return env
}

const REAL_CONFIG = {
  picBed: { uploader: 'github', current: 'github' },
  picgoPlugins: { 'picgo-plugin-github-plus': true },
  uploader: { github: { configList: [] } },
  settings: { logLevel: ['info'] },
  // ⚠️ 插件私有键（必须被保留）
  uploaded: [{ type: 'github', fileName: 'a.png' }],
  'picgo-plugin-github-plus': { lastSync: 12345 }
}

describe('isManagedKey / rootOf', () => {
  it('管辖根键的任意点路径都算管辖键', () => {
    expect(isManagedKey('picBed')).toBe(true)
    expect(isManagedKey('picBed.uploader')).toBe(true)
    expect(isManagedKey('picBed.github.token')).toBe(true)
    expect(isManagedKey('uploader.github.configList')).toBe(true)
    expect(isManagedKey('picgoPlugins.picgo-plugin-x')).toBe(true)
    expect(isManagedKey('settings.logLevel')).toBe(true)
  })

  it('非管辖键返回 false', () => {
    expect(isManagedKey('uploaded')).toBe(false)
    expect(isManagedKey('picgo-plugin-github-plus')).toBe(false)
    expect(isManagedKey('anything.else')).toBe(false)
  })

  it('rootOf 取点路径的根', () => {
    expect(rootOf('picBed.github.token')).toBe('picBed')
    expect(rootOf('picBed')).toBe('picBed')
  })

  it('MANAGED_ROOTS 正好是四个（回归：改动需同步文档）', () => {
    expect([...MANAGED_ROOTS]).toEqual(['picBed', 'uploader', 'picgoPlugins', 'settings'])
  })
})

describe('pluginPrivateKeys', () => {
  it('挑出非管辖键', () => {
    const keys = pluginPrivateKeys(REAL_CONFIG)
    expect(keys).toContain('uploaded')
    expect(keys).toContain('picgo-plugin-github-plus')
    expect(keys).not.toContain('picBed')
    expect(keys).not.toContain('uploader')
  })
})

describe('patchConfig', () => {
  it('写入管辖键成功', () => {
    const env = tmpEnv()
    const { picgo } = createFakePicgo({ config: structuredClone(REAL_CONFIG) })

    const result = patchConfig(picgo, env, log, { 'picBed.uploader': 'smms' })

    expect(result.Applied).toEqual(['picBed.uploader'])
    expect(readConfig(picgo).picBed).toEqual({ uploader: 'smms', current: 'github' })
  })

  it('拒绝写入非管辖键（防误毁插件状态）', () => {
    const env = tmpEnv()
    const { picgo } = createFakePicgo({ config: structuredClone(REAL_CONFIG) })

    expect(() => patchConfig(picgo, env, log, { uploaded: [] })).toThrow(/拒绝写入非管辖键/)
    // 未被改动
    expect(readConfig(picgo).uploaded).toBeTruthy()
  })

  it('混合请求：只要有一个非管辖键就整体拒绝（不写一半）', () => {
    const env = tmpEnv()
    const { picgo } = createFakePicgo({ config: structuredClone(REAL_CONFIG) })
    const before = structuredClone(readConfig(picgo))

    expect(() =>
      patchConfig(picgo, env, log, { 'picBed.uploader': 'smms', uploaded: [] })
    ).toThrow()

    expect(readConfig(picgo)).toEqual(before)
  })

  it('allowUnmanaged 时允许写非管辖键（内部/测试用）', () => {
    const env = tmpEnv()
    const { picgo } = createFakePicgo({ config: structuredClone(REAL_CONFIG) })

    const result = patchConfig(picgo, env, log, { custom: 1 }, { allowUnmanaged: true })
    expect(result.Applied).toEqual(['custom'])
  })

  it('空 patch 是 no-op（不写盘、不备份）', () => {
    const env = tmpEnv()
    const { picgo, saved } = createFakePicgo({ config: structuredClone(REAL_CONFIG) })

    const result = patchConfig(picgo, env, log, {})
    expect(result.Applied).toEqual([])
    expect(saved).toHaveLength(0)
  })

  it('**关键回归**：写入管辖键后，插件私有键仍在（模拟 github-plus 的 uploaded）', () => {
    const env = tmpEnv()
    fs.writeFileSync(env.ConfigPath, JSON.stringify(REAL_CONFIG, null, 2))
    const { picgo } = createFakePicgo({ config: structuredClone(REAL_CONFIG) })

    patchConfig(picgo, env, log, { 'picBed.uploader': 'smms' })
    patchConfig(picgo, env, log, { 'picBed.transformer': 'path' })

    const after = readConfig(picgo)
    expect(after.uploaded).toEqual(REAL_CONFIG.uploaded)
    expect(after['picgo-plugin-github-plus']).toEqual(REAL_CONFIG['picgo-plugin-github-plus'])
  })

  it('响应里报告被保留的插件私有键', () => {
    const env = tmpEnv()
    const { picgo } = createFakePicgo({ config: structuredClone(REAL_CONFIG) })
    const result = patchConfig(picgo, env, log, { 'picBed.uploader': 'x' })
    expect(result.PreservedPluginKeys).toContain('uploaded')
  })
})

describe('putConfig', () => {
  it('只替换管辖键，插件私有键保留（不是真的整体替换）', () => {
    const env = tmpEnv()
    const { picgo } = createFakePicgo({ config: structuredClone(REAL_CONFIG) })

    const result = putConfig(picgo, env, log, {
      picBed: { uploader: 'smms', current: 'smms' },
      picgoPlugins: {},
      uploader: {},
      settings: {},
      // 即便调用方传了这些，也不应被采纳
      uploaded: []
    })

    expect(result.Applied.sort()).toEqual(['picBed', 'picgoPlugins', 'settings', 'uploader'])
    const after = readConfig(picgo)
    expect(after.picBed).toEqual({ uploader: 'smms', current: 'smms' })
    // ⚠️ uploaded 必须保持原值，不能被请求体里的 [] 覆盖
    expect(after.uploaded).toEqual(REAL_CONFIG.uploaded)
  })

  it('缺少全部管辖键 → 拒绝（防误毁）', () => {
    const env = tmpEnv()
    const { picgo } = createFakePicgo({ config: structuredClone(REAL_CONFIG) })

    expect(() => putConfig(picgo, env, log, { uploaded: [], custom: 1 })).toThrow(/缺少全部管辖键/)
  })

  it('只提供部分管辖键也可接受', () => {
    const env = tmpEnv()
    const { picgo } = createFakePicgo({ config: structuredClone(REAL_CONFIG) })

    const result = putConfig(picgo, env, log, { picBed: { uploader: 'qiniu' } })
    expect(result.Applied).toEqual(['picBed'])
    expect(readConfig(picgo).uploaded).toEqual(REAL_CONFIG.uploaded)
  })
})

describe('backupConfig', () => {
  it('生成 bak.0 并轮转', () => {
    const env = tmpEnv()
    fs.writeFileSync(env.ConfigPath, '{"v":1}')
    backupConfig(env, log, 3)
    expect(fs.existsSync(`${env.ConfigPath}.bak.0`)).toBe(true)
    expect(fs.readFileSync(`${env.ConfigPath}.bak.0`, 'utf8')).toBe('{"v":1}')

    fs.writeFileSync(env.ConfigPath, '{"v":2}')
    backupConfig(env, log, 3)
    expect(fs.readFileSync(`${env.ConfigPath}.bak.0`, 'utf8')).toBe('{"v":2}')
    expect(fs.readFileSync(`${env.ConfigPath}.bak.1`, 'utf8')).toBe('{"v":1}')
  })

  it('保留份数受限（超出丢弃最老的）', () => {
    const env = tmpEnv()
    for (let i = 1; i <= 6; i += 1) {
      fs.writeFileSync(env.ConfigPath, `{"v":${i}}`)
      backupConfig(env, log, 3)
    }
    expect(fs.existsSync(`${env.ConfigPath}.bak.0`)).toBe(true)
    expect(fs.existsSync(`${env.ConfigPath}.bak.2`)).toBe(true)
    // keep=3 只保留 bak.0/1/2
    expect(fs.existsSync(`${env.ConfigPath}.bak.3`)).toBe(false)
    expect(fs.readFileSync(`${env.ConfigPath}.bak.0`, 'utf8')).toBe('{"v":6}')
  })

  it('配置文件不存在时不报错', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'cfg-'))
    const env = makeEnv(dir) // ConfigPath 指向不存在的文件
    expect(() => backupConfig(env, log, 3)).not.toThrow()
  })

  it('keep <= 0 时不做备份', () => {
    const env = tmpEnv()
    backupConfig(env, log, 0)
    expect(fs.existsSync(`${env.ConfigPath}.bak.0`)).toBe(false)
  })

  it('patchConfig 会先备份（每次写都留一份）', () => {
    const env = tmpEnv()
    fs.writeFileSync(env.ConfigPath, '{"picBed":{}}')
    const { picgo } = createFakePicgo({ config: { picBed: {} } })

    patchConfig(picgo, env, log, { 'picBed.uploader': 'github' })
    expect(fs.existsSync(`${env.ConfigPath}.bak.0`)).toBe(true)

    patchConfig(picgo, env, log, { 'picBed.uploader': 'smms' })
    expect(fs.existsSync(`${env.ConfigPath}.bak.1`)).toBe(true)
  })
})

describe('readConfig', () => {
  it('返回原样的 picgo 配置', () => {
    const { picgo } = createFakePicgo({ config: structuredClone(REAL_CONFIG) })
    expect(readConfig(picgo)).toEqual(REAL_CONFIG)
  })

  it('getConfig 返回空时降级为空对象（不崩）', () => {
    const { picgo } = createFakePicgo({})
    // 清空
    ;(picgo as unknown as { getConfig: () => unknown }).getConfig = () => undefined
    expect(readConfig(picgo)).toEqual({})
  })
})
