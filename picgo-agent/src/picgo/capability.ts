/**
 * 驱动能力探测（D44 / D77.2：**不硬编码驱动名列表**）。
 *
 * Go 侧把结果缓存进 `StorageConfigs.Capabilities`，前端据此提示管理员
 * 「这个驱动支不支持自定义路径 / 远端删除」。加新驱动**不需要改 Go 代码**。
 *
 * 三项能力：
 * - `SupportsPathTemplate` —— 驱动 schema 里是否存在路径类字段
 * - `SupportsRemoteDelete` —— 是否有插件注册了 `remove` 监听（走 `ctx.on('remove', ...)`）
 * - `GuiOnly` —— 插件含 `guiMenu` / `commands`（Electron 专属，Web 端不可用）
 */

import type { IPicGo, IPluginConfig } from 'picgo'
import type { Capabilities } from '../types.js'

/**
 * 路径类字段的候选名。
 *
 * 这些是「驱动允许你指定远端目录」的常见命名，来自对 picgo 生态的观察：
 * - 内置：github/aliyun/tcyun/qiniu/upyun 都用 `path`
 * - 插件：webdav / minio / s3 类插件常用 `root` / `basePath` / `prefix` / `dir`
 *
 * ⚠️ 这是**候选集**而不是白名单：任何命中即视为支持，未命中则视为不支持。
 * 不需要为每个新驱动维护映射（D77）。
 */
export const PATH_FIELD_CANDIDATES: readonly string[] = [
  'path',
  'root',
  'basePath',
  'basepath',
  'baseDir',
  'basedir',
  'prefix',
  'dir',
  'directory',
  'folder',
  'pathPrefix',
  'pathprefix'
]

/** 归一化字段名用于比较（去掉分隔符、转小写）。 */
function normalizeFieldName(name: string): string {
  return name.replace(/[_-]/g, '').toLowerCase()
}

const NORMALIZED_CANDIDATES = new Set(PATH_FIELD_CANDIDATES.map(normalizeFieldName))

/** 该字段名是否像「路径」。 */
export function looksLikePathField(fieldName: string): boolean {
  return NORMALIZED_CANDIDATES.has(normalizeFieldName(fieldName))
}

/** 从已求值的 schema 里挑出字段名。 */
export function configFieldNames(schema: readonly IPluginConfig[]): string[] {
  return schema.map((f) => f.name).filter((n): n is string => typeof n === 'string' && n !== '')
}

/** 从字段名列表里挑出路径类字段。 */
export function pathFieldNames(fieldNames: readonly string[]): string[] {
  return fieldNames.filter(looksLikePathField)
}

/**
 * 探测 `SupportsRemoteDelete`。
 *
 * picgo 的事件是**全局**的（挂在根实例上），插件通过 `ctx.on('remove', handler)`
 * 注册。因此「有 remove 监听器」即意味着「至少有一个插件实现了远端删除」。
 *
 * ⚠️ 这是**保守**判定：
 * - 有监听器 ≠ 所有驱动都支持（监听器内部会按 `each.type === UploaderName` 过滤）
 * - 无监听器 ⇒ 一定不支持
 *
 * 因此默认给 `true`（若有监听器），并在 `/api/delete` 实际调用后
 * 根据「是否有插件响应」把结果修正为 `false`（见 remove.ts 的反馈机制）。
 */
export function detectRemoteDeleteSupport(picgo: IPicGo): boolean {
  try {
    // EventEmitter.listenerCount
    return picgo.listenerCount('remove') > 0
  } catch {
    return false
  }
}

/**
 * 收集插件提供的 uploader / transformer 名，用于判定某个 uploader 是否内置。
 *
 * 内置驱动由 picgo-core 自己注册（smms / tcyun / upyun / aliyun / qiniu / imgur /
 * github / picgo-cloud）；插件声明的 `uploader` 字段则是插件提供。
 * **不硬编码内置驱动名**，而是反过来用「插件声明了谁」来排除。
 */
export function pluginProvidedUploaders(picgo: IPicGo): Set<string> {
  const out = new Set<string>()
  try {
    const names = picgo.pluginLoader.getFullList()
    for (const name of names) {
      const plugin = picgo.pluginLoader.getPlugin(name)
      if (plugin?.uploader && typeof plugin.uploader === 'string') {
        out.add(plugin.uploader)
      }
    }
  } catch {
    // 插件系统异常时退化为「全部视为内置」，不影响主流程
  }
  return out
}

/**
 * 插件是否为 GuiOnly（含 `guiMenu` / `commands`）。
 *
 * 参数故意用 `unknown`：我们只知道插件对象上**可能**有这两个字段，
 * 不应把它约束成某个具体接口（插件的形状由第三方决定）。
 */
export function isGuiOnly(plugin: unknown): boolean {
  if (typeof plugin !== 'object' || plugin === null) return false
  const rec = plugin as Record<string, unknown>
  return typeof rec.guiMenu === 'function' || typeof rec.commands === 'function'
}

export interface CapabilityInput {
  schema: readonly IPluginConfig[]
  /** 该 uploader 是否内置（由调用方判定）。 */
  builtin: boolean
  guiOnly: boolean
  supportsRemoteDelete: boolean
  picgoVersion: string
  now: number
}

/** 组装能力对象。 */
export function buildCapabilities(input: CapabilityInput): Capabilities {
  const fields = configFieldNames(input.schema)
  const pathFields = pathFieldNames(fields)

  return {
    SupportsPathTemplate: pathFields.length > 0,
    SupportsRemoteDelete: input.supportsRemoteDelete,
    ConfigFields: fields,
    PathFieldNames: pathFields,
    DetectedAt: input.now,
    PicgoVersion: input.picgoVersion
  }
}

/**
 * 进程内能力缓存。
 *
 * 缓存键为 `<picgoVersion>|<uploaderType>`，因为插件装卸后能力可能变化
 * （插件装卸会重启 agent，所以版本变化已经隐含了「注册表变了」）。
 * 另外提供 `invalidate()` 供插件启停后手动失效。
 */
export class CapabilityCache {
  private readonly map = new Map<string, Capabilities>()

  private static key(type: string, picgoVersion: string): string {
    return `${picgoVersion}|${type}`
  }

  get(type: string, picgoVersion: string): Capabilities | undefined {
    return this.map.get(CapabilityCache.key(type, picgoVersion))
  }

  set(type: string, picgoVersion: string, value: Capabilities): void {
    this.map.set(CapabilityCache.key(type, picgoVersion), value)
  }

  /** 清空全部（插件装卸后调用）。 */
  invalidate(): void {
    this.map.clear()
  }

  /** 清空某个（`SupportsRemoteDelete` 被实测修正时调用）。 */
  invalidateType(type: string): void {
    for (const key of [...this.map.keys()]) {
      if (key.endsWith(`|${type}`)) this.map.delete(key)
    }
  }

  size(): number {
    return this.map.size
  }
}
