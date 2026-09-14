/**
 * 上传器（驱动）能力与配置表单 schema 的封装。
 *
 * ## 两个关键点
 *
 * ### 1. schema 必须在**服务端**求值（D44 / PICGO-INTEGRATION §3）
 *
 * 插件的 `config(ctx)` 返回 `IPluginConfig[]`，其中 `default` / `choices`
 * **可能是函数**（依赖其他字段的当前值）。前端**永不执行插件代码**，
 * 因此必须在这里用 `evaluatePluginConfig` 把函数形态求值成静态结构后再下发。
 *
 * ### 2. 求值时必须用 **schema-only context**
 *
 * 插件普遍这么写：
 *
 *     const config = (ctx) => [{
 *       name: 'repo',
 *       default: ctx.getConfig('picBed.github')?.repo || ''   // ← 读自己的历史值
 *     }]
 *
 * 如果我们用真实 ctx 求值，`default` 会被**历史值**覆盖，
 * 前端表单就会把旧值当成「默认值」展示，新建配置时误导用户。
 * 因此求值时用一个 Proxy 隐藏该 uploader 自身的配置。
 *
 * （语义与 PicGo GUI 的 `schemaOnlyUploaderContext.ts` 一致，但这里独立实现 ——
 * GUI 的代码 import 了 electron，不能在纯 Node 里跑。）
 */

import { evaluatePluginConfig } from 'picgo'
import type { IPicGo, IPluginConfig, IUploaderConfigItem } from 'picgo'
import type { Logger } from '../logger.js'
import {
  buildCapabilities,
  detectRemoteDeleteSupport,
  isGuiOnly,
  pluginProvidedUploaders,
  type CapabilityCache
} from './capability.js'
import type { Capabilities, RawUploaderConfig, UploaderItem } from '../types.js'

type ConfigRecord = Record<string, unknown>

function isConfigRecord(value: unknown): value is ConfigRecord {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function omitKey(value: unknown, key: string): unknown {
  if (!isConfigRecord(value)) return value
  const next = { ...value }
  delete next[key]
  return next
}

function sanitizeFullConfig(value: unknown, uploaderType: string): unknown {
  if (!isConfigRecord(value)) return value
  const next = { ...value }
  if ('picBed' in value) next.picBed = omitKey(value.picBed, uploaderType)
  if ('uploader' in value) next.uploader = omitKey(value.uploader, uploaderType)
  return next
}

/**
 * 构造「只读 schema」的上下文：隐藏该 uploader 自己的配置。
 *
 * 只拦截 `getConfig`，其余一律透传（Proxy 保持原型链与其它属性不变）。
 */
export function createSchemaOnlyContext<T extends { getConfig: (name?: string) => unknown }>(
  context: T,
  uploaderType: string
): T {
  const hiddenPaths = [`picBed.${uploaderType}`, `uploader.${uploaderType}`]

  const getConfig = <V>(name?: string): V => {
    if (
      name !== undefined &&
      hiddenPaths.some((p) => name === p || name.startsWith(`${p}.`))
    ) {
      return undefined as V
    }
    if (name === 'picBed' || name === 'uploader') {
      return omitKey(context.getConfig(name), uploaderType) as V
    }
    if (name === undefined) {
      return sanitizeFullConfig(context.getConfig(), uploaderType) as V
    }
    return context.getConfig(name) as V
  }

  return new Proxy(context, {
    get(target, property, receiver) {
      if (property === 'getConfig') return getConfig
      return Reflect.get(target, property, receiver)
    }
  })
}

/** 插件 `config(ctx)` 的**原始**返回值类型（函数形态，尚未求值）。 */
type RawConfigFn = (ctx: unknown) => unknown

export interface UploaderInfo {
  Type: string
  Name: string
  Builtin: boolean
  GuiOnly: boolean
  /** 已求值的 schema。 */
  Schema: IPluginConfig[]
  /** 未求值的原始 config 函数（供带 answers 重求值时使用）。 */
  rawConfigFn: RawConfigFn | undefined
}

/** 列出全部 uploader 类型。 */
export function listUploaderTypes(picgo: IPicGo): string[] {
  try {
    return picgo.helper.uploader.getIdList()
  } catch {
    return []
  }
}

/** 取单个 uploader 的元信息（含原始 config 函数，不在此处求值）。 */
export function getUploaderInfo(picgo: IPicGo, type: string): UploaderInfo | undefined {
  const helper = picgo.helper.uploader.get(type)
  if (!helper) return undefined

  const name = typeof helper.name === 'string' && helper.name !== '' ? helper.name : type
  const providerSet = pluginProvidedUploaders(picgo)
  const builtin = !providerSet.has(type)

  // 插件实例（用于 GuiOnly 判定）；内置 uploader 没有插件实例
  let guiOnly = false
  if (!builtin) {
    const pluginName = picgo.pluginLoader
      .getFullList()
      .find((n) => picgo.pluginLoader.getPlugin(n)?.uploader === type)
    if (pluginName) {
      guiOnly = isGuiOnly(picgo.pluginLoader.getPlugin(pluginName) as never)
    }
  }

  const rawConfigFn =
    typeof helper.config === 'function' ? (helper.config as unknown as RawConfigFn) : undefined

  return { Type: type, Name: name, Builtin: builtin, GuiOnly: guiOnly, Schema: [], rawConfigFn }
}

/**
 * 求值某 uploader 的 schema。
 *
 * @param answers 当前表单的值快照（`dependsOn` 联动时必需）
 * @param log     用于记录插件字段求值失败（`evaluatePluginConfig` 的 onError）
 */
export function evaluateUploaderSchema(
  picgo: IPicGo,
  type: string,
  log: Logger,
  answers: Record<string, unknown> = {}
): IPluginConfig[] {
  const info = getUploaderInfo(picgo, type)
  if (!info || !info.rawConfigFn) return []

  // ⚠️ 用 schema-only context：隐藏该 uploader 自身的已有配置
  const schemaCtx = createSchemaOnlyContext(
    picgo as unknown as { getConfig: (name?: string) => unknown },
    type
  )

  let raw: unknown
  try {
    raw = info.rawConfigFn(schemaCtx)
  } catch (error) {
    log.warn('插件 config(ctx) 调用失败', { uploader: type, err: error })
    return []
  }

  if (!Array.isArray(raw)) return []

  try {
    return evaluatePluginConfig(raw as IPluginConfig[], answers, {
      onError: (fieldName, kind, error) => {
        log.warn('插件配置字段求值失败', { uploader: type, field: fieldName, kind, err: error })
      }
    })
  } catch (error) {
    log.warn('evaluatePluginConfig 失败，返回原始 schema', { uploader: type, err: error })
    return raw as IPluginConfig[]
  }
}

/**
 * 列出全部 uploader（含求值后的 schema 与能力探测）。
 *
 * 结果**不做脱敏** —— agent 侧就是 picgo 的真实存储，脱敏是 Go 的职责。
 */
export function listUploaders(
  picgo: IPicGo,
  log: Logger,
  cache: CapabilityCache,
  picgoVersion: string,
  now = Math.floor(Date.now() / 1000)
): UploaderItem[] {
  const types = listUploaderTypes(picgo)
  const remoteDeleteSupported = detectRemoteDeleteSupport(picgo)
  const out: UploaderItem[] = []

  for (const type of types) {
    const info = getUploaderInfo(picgo, type)
    if (!info) continue

    const schema = evaluateUploaderSchema(picgo, type, log)

    let capabilities: Capabilities | undefined = cache.get(type, picgoVersion)
    if (!capabilities) {
      capabilities = buildCapabilities({
        schema,
        builtin: info.Builtin,
        guiOnly: info.GuiOnly,
        supportsRemoteDelete: remoteDeleteSupported,
        picgoVersion,
        now
      })
      cache.set(type, picgoVersion, capabilities)
    }

    out.push({
      Type: type,
      Name: info.Name,
      Builtin: info.Builtin,
      GuiOnly: info.GuiOnly,
      Config: schema,
      Capabilities: capabilities,
      ConfigNames: getConfigNames(picgo, type)
    })
  }

  return out
}

/** 取某类型的全部配置名（用于 Go 侧对账）。 */
export function getConfigNames(picgo: IPicGo, type: string): string[] {
  try {
    return picgo.uploaderConfig.getConfigList(type).map((c) => String(c._configName ?? ''))
  } catch {
    return []
  }
}

/** 取当前激活的 uploader 与配置名。 */
export function currentUploader(picgo: IPicGo): { Type: string; ConfigName: string } {
  const config = (picgo.getConfig() ?? {}) as ConfigRecord
  const picBed = isConfigRecord(config.picBed) ? config.picBed : {}

  const type =
    (typeof picBed.uploader === 'string' && picBed.uploader) ||
    (typeof picBed.current === 'string' && picBed.current) ||
    ''

  if (type === '') return { Type: '', ConfigName: '' }

  let configName = ''
  try {
    const active = picgo.uploaderConfig.getActiveConfig(type)
    if (active) configName = String(active._configName ?? '')
  } catch {
    configName = ''
  }

  return { Type: type, ConfigName: configName }
}

/**
 * 取某类型下某配置名的完整配置项（**含明文凭据**）。
 *
 * 供「连通性测试」用（需要真实凭据）。
 */
export function findConfigItem(
  picgo: IPicGo,
  type: string,
  configName: string
): IUploaderConfigItem | undefined {
  try {
    const list = picgo.uploaderConfig.getConfigList(type)
    const wanted = configName.trim().toLowerCase()
    return list.find((c) => String(c._configName ?? '').trim().toLowerCase() === wanted)
  } catch {
    return undefined
  }
}

/** 取某类型的当前激活配置项。 */
export function activeConfigItem(picgo: IPicGo, type: string): IUploaderConfigItem | undefined {
  try {
    return picgo.uploaderConfig.getActiveConfig(type)
  } catch {
    return undefined
  }
}

// ---------------------------------------------------------------------------
// 配置 CRUD（包一层错误归一化）
// ---------------------------------------------------------------------------

export function listConfigs(picgo: IPicGo, type: string): {
  configs: RawUploaderConfig[]
  defaultConfigName: string
} {
  const configs = picgo.uploaderConfig.getConfigList(type)
  const active = picgo.uploaderConfig.getActiveConfig(type)
  return {
    configs,
    defaultConfigName: active ? String(active._configName ?? '') : ''
  }
}

export function upsertConfig(
  picgo: IPicGo,
  type: string,
  configName: string,
  config: ConfigRecord,
  activate: boolean
): RawUploaderConfig {
  const created = picgo.uploaderConfig.createOrUpdate(type, configName, config)

  if (activate) {
    picgo.uploaderConfig.use(type, String(created._configName ?? configName))
  }

  return created
}

export function removeConfig(picgo: IPicGo, type: string, configName: string): void {
  picgo.uploaderConfig.remove(type, configName)
}

export function useUploader(picgo: IPicGo, type: string, configName?: string): RawUploaderConfig {
  if (configName !== undefined && configName !== '') {
    return picgo.uploaderConfig.use(type, configName)
  }
  return picgo.uploaderConfig.use(type)
}
