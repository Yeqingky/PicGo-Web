/**
 * picgo `config.json` 的读写。
 *
 * ## 硬约束（D22）
 *
 * **键级合并，绝不整体重建。**
 *
 * 插件会往 config 里写自己的状态（例如 `picgo-plugin-github-plus` 写
 * `uploaded: [...]` 图片账本与 `[PluginName].lastSync`）。若我们整体重写 config，
 * 这些私有键会被抹掉，插件的账本随之损坏。
 *
 * 因此：
 * - 我们**只允许写** `picBed.*` / `uploader.*` / `picgoPlugins.*` / `settings.*`
 * - 其余键**原样保留且不得删除**
 * - `PUT` 也走同一套过滤（不是真的「整体替换」），并**拒绝**不含任何管辖键的请求体
 *
 * ## 写前备份
 *
 * 由**我们主动**写 config 之前，先把当前 `config.json` 复制为 `config.json.bak.N`
 * （轮转保留最近 N 份）。注意 picgo 自身的 `saveConfig` 我们拦不住，
 * 所以备份只能覆盖「我们发起的写入」，这已足够覆盖风险最高的批量同步场景。
 */

import fs from 'node:fs'
import path from 'node:path'
import type { IPicGo } from '@yeqingky/picgo-core'
import type { AgentEnv } from '../env.js'
import type { Logger } from '../logger.js'
import type { RawPicgoConfig } from '../types.js'

/** 我们**被允许**修改的配置根键（D22）。 */
export const MANAGED_ROOTS = ['picBed', 'uploader', 'picgoPlugins', 'settings'] as const
export type ManagedRoot = (typeof MANAGED_ROOTS)[number]

const MANAGED_ROOT_SET = new Set<string>(MANAGED_ROOTS)

/** 顶级键是否归我们管辖。 */
export function isManagedKey(key: string): boolean {
  const root = rootOf(key)
  return MANAGED_ROOT_SET.has(root)
}

/** 从点路径取根键。 */
export function rootOf(dotPath: string): string {
  return dotPath.split('.')[0] ?? ''
}

/**
 * 读当前 config（**原样**，含插件私有键）。
 *
 * 直接读 picgo 实例的内存视图（它就是磁盘内容的镜像）。
 */
export function readConfig(picgo: IPicGo): RawPicgoConfig {
  return (picgo.getConfig() ?? {}) as RawPicgoConfig
}

/**
 * 找出配置中**非我们管辖**的顶级键（即插件私有状态）。
 *
 * 用于 `PATCH` 的响应里告知调用方「这些键被保留了」，也用于回归测试。
 */
export function pluginPrivateKeys(config: RawPicgoConfig): string[] {
  return Object.keys(config).filter((key) => !isManagedKey(key))
}

/**
 * 写前备份：把当前 config.json 复制成 `config.json.bak.N`。
 *
 * 轮转规则：`bak.0` 是最新备份，写入前先把已有的 N-1…0 依次后移，
 * 最老的被丢弃。备份失败**不阻断**写入（记 warn）。
 */
export function backupConfig(env: AgentEnv, log: Logger, keep: number): void {
  if (keep <= 0) return

  const file = env.ConfigPath
  if (!fs.existsSync(file)) return

  try {
    const oldest = `${file}.bak.${keep - 1}`
    if (fs.existsSync(oldest)) fs.rmSync(oldest, { force: true })

    // bak.i-1 → bak.i（从大到小，避免覆盖）
    for (let i = keep - 2; i >= 0; i -= 1) {
      const from = `${file}.bak.${i}`
      const to = `${file}.bak.${i + 1}`
      if (fs.existsSync(from)) fs.renameSync(from, to)
    }

    fs.copyFileSync(file, `${file}.bak.0`)
  } catch (error) {
    log.warn('配置备份失败（不阻断写入）', { err: error })
  }
}

/** 写前确认 baseDir 与文件存在（picgo 用 write-file-atomic 落盘，目录不存在会失败）。 */
function ensureWritable(env: AgentEnv): void {
  fs.mkdirSync(path.dirname(env.ConfigPath), { recursive: true })
}

export interface PatchResult {
  Applied: string[]
  PreservedPluginKeys: string[]
}

export interface PatchOptions {
  /** 是否允许写入非管辖键。默认 false（拒绝）。 */
  allowUnmanaged?: boolean
}

/**
 * 点路径合并写入（`PATCH /api/config`）。
 *
 * - `patch` 的键是 picgo 的结构路径（`picBed.uploader`），**原样**
 * - 非管辖键默认**拒绝**（抛出异常，由路由层转成 `ERR_PARAM`）
 * - 写入前先备份
 *
 * 落盘走 `picgo.saveConfig`，它本身就是 lodash `set` 的点路径语义。
 */
export function patchConfig(
  picgo: IPicGo,
  env: AgentEnv,
  log: Logger,
  patch: Record<string, unknown>,
  options: PatchOptions = {}
): PatchResult {
  const entries = Object.entries(patch)
  if (entries.length === 0) {
    return { Applied: [], PreservedPluginKeys: pluginPrivateKeys(readConfig(picgo)) }
  }

  const rejected: string[] = []
  const applied: string[] = []
  const toWrite: Record<string, unknown> = {}

  for (const [key, value] of entries) {
    if (!isManagedKey(key) && options.allowUnmanaged !== true) {
      rejected.push(key)
      continue
    }
    toWrite[key] = value
    applied.push(key)
  }

  if (rejected.length > 0) {
    throw new Error(
      `拒绝写入非管辖键（只允许 ${MANAGED_ROOTS.join(' / ')}）：${rejected.join(', ')}`
    )
  }

  backupConfig(env, log, env.ConfigBackupCount)
  ensureWritable(env)

  picgo.saveConfig(toWrite)

  log.debug('已写入配置', { applied })
  return { Applied: applied, PreservedPluginKeys: pluginPrivateKeys(readConfig(picgo)) }
}

/**
 * 「整体替换」（`PUT /api/config`）。
 *
 * ⚠️ 为了不毁掉插件状态，**实际只替换管辖根键**，其余键原样保留（D22）。
 * 若请求体**缺少全部**管辖键 → 拒绝（说明调用方八成传错了东西）。
 */
export function putConfig(
  picgo: IPicGo,
  env: AgentEnv,
  log: Logger,
  incoming: RawPicgoConfig
): PatchResult {
  const managedEntries = Object.entries(incoming).filter(([key]) => isManagedKey(key))
  if (managedEntries.length === 0) {
    throw new Error(
      `拒绝整体替换：请求体缺少全部管辖键（${MANAGED_ROOTS.join(' / ')}）。` +
        '若确实要替换，请至少提供其中一个。'
    )
  }

  backupConfig(env, log, env.ConfigBackupCount)
  ensureWritable(env)

  const toWrite: Record<string, unknown> = {}
  for (const [key, value] of managedEntries) {
    toWrite[key] = value
  }

  picgo.saveConfig(toWrite)
  log.warn('已按管辖键替换配置（插件私有键保持不变）', { replaced: Object.keys(toWrite) })

  return {
    Applied: Object.keys(toWrite),
    PreservedPluginKeys: pluginPrivateKeys(readConfig(picgo))
  }
}
