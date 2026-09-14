/**
 * 插件管理：列表 / README / 安装 / 卸载 / 更新 / 启停。
 *
 * ## 真相源
 *
 * npm 包的真实状态在 `<picgo.baseDir>/node_modules/`。agent 是真相源，
 * Go 侧的 `Plugins` 表只是**展示缓存**（避免每次读盘）。
 *
 * ## 元信息从哪来
 *
 * picgo 的 `pluginLoader` 只给名字与是否启用。版本/描述/作者/首页要读
 * `<picgo.baseDir>/node_modules/<name>/package.json`（GUI 也是这么做的）。
 *
 * ## 安装是异步的
 *
 * npm install 可能几十秒，因此：
 * - 立即返回 `JobUID`，把 npm 输出作为 job 日志通过 SSE 推给前端
 * - 完成后**重启自身进程**换取状态绝对干净（低频操作，可接受）
 *   —— 热 `require` 一个刚装上的插件在 Node 里非常不可靠（模块缓存、依赖树变化）
 */

import fs from 'node:fs'
import path from 'node:path'
import type { IPicGo } from '@yeqingky/picgo-core'
import type { Logger } from '../logger.js'
import type { PluginItem, PluginsListData } from '../types.js'
import { isGuiOnly } from './capability.js'

/** 插件 package.json 里我们关心的字段。 */
interface PluginPkg {
  name?: string
  version?: string
  description?: string
  author?: string | { name?: string }
  homepage?: string
  repository?: string | { url?: string }
}

function readPluginPkg(baseDir: string, name: string): PluginPkg | undefined {
  const pkgPath = path.join(baseDir, 'node_modules', name, 'package.json')
  try {
    const raw = fs.readFileSync(pkgPath, 'utf8')
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed !== 'object' || parsed === null) return undefined
    return parsed as PluginPkg
  } catch {
    return undefined
  }
}

function authorOf(pkg: PluginPkg | undefined): string {
  if (!pkg?.author) return ''
  if (typeof pkg.author === 'string') return pkg.author
  return typeof pkg.author.name === 'string' ? pkg.author.name : ''
}

function homepageOf(pkg: PluginPkg | undefined): string {
  if (typeof pkg?.homepage === 'string' && pkg.homepage !== '') return pkg.homepage
  const repo = pkg?.repository
  if (typeof repo === 'string') return repo
  if (repo && typeof repo.url === 'string') return repo.url
  return ''
}

/** 取插件实例（用于 GuiOnly / uploader / transformer 判定）。 */
function pluginInterface(picgo: IPicGo, name: string) {
  try {
    return picgo.pluginLoader.getPlugin(name)
  } catch {
    return undefined
  }
}

/**
 * 列出全部插件。
 *
 * - `getFullList()` 给全部（含被禁用）
 * - `getList()` 给仅启用
 * - 启用状态以 `picgoPlugins` 配置为准（与 picgo 自身一致）
 */
export function listPlugins(picgo: IPicGo, baseDir: string, log: Logger): PluginsListData {
  let all: string[] = []
  let enabled: string[] = []

  try {
    all = picgo.pluginLoader.getFullList()
  } catch (error) {
    log.warn('读取插件列表失败', { err: error })
    return { Plugins: [], Disabled: [] }
  }

  try {
    enabled = picgo.pluginLoader.getList()
  } catch {
    enabled = []
  }

  const enabledSet = new Set(enabled)
  const plugins: PluginItem[] = []
  const disabled: string[] = []

  for (const name of all) {
    const pkg = readPluginPkg(baseDir, name)
    const iface = pluginInterface(picgo, name)
    const isEnabled = enabledSet.has(name)

    if (!isEnabled) disabled.push(name)

    plugins.push({
      Name: name,
      Version: typeof pkg?.version === 'string' ? pkg.version : '',
      Enabled: isEnabled,
      GuiOnly: isGuiOnly(iface as never),
      Uploader: typeof iface?.uploader === 'string' ? iface.uploader : '',
      Transformer: typeof iface?.transformer === 'string' ? iface.transformer : '',
      Description: typeof pkg?.description === 'string' ? pkg.description : '',
      Author: authorOf(pkg),
      Homepage: homepageOf(pkg)
    })
  }

  return { Plugins: plugins, Disabled: disabled }
}

/** 读插件 README（尝试常见文件名）。 */
export function readPluginReadme(
  baseDir: string,
  name: string
): { Content: string; Path: string } | undefined {
  const candidates = ['README.md', 'readme.md', 'Readme.md', 'README.MD', 'README.markdown', 'README']
  const dir = path.join(baseDir, 'node_modules', name)

  for (const file of candidates) {
    const full = path.join(dir, file)
    try {
      if (fs.existsSync(full)) {
        return { Content: fs.readFileSync(full, 'utf8'), Path: full }
      }
    } catch {
      // 继续试下一个
    }
  }
  return undefined
}

export interface PluginHandlerResult {
  success: boolean
  body: string
}

/**
 * 把 picgo 的 `IPluginHandlerResult` 归一化成 `{ success, body }`。
 *
 * picgo 的返回是 `{ success: boolean, body: string[] | string }`：
 * 成功时 body 是插件名数组，失败时 body 是错误信息字符串。
 */
function normalizeHandlerResult(raw: unknown): PluginHandlerResult {
  if (typeof raw !== 'object' || raw === null) {
    return { success: false, body: '插件操作未返回结果' }
  }
  const obj = raw as { success?: unknown; body?: unknown }
  const success = obj.success === true

  if (Array.isArray(obj.body)) {
    return { success, body: obj.body.join(', ') }
  }
  if (typeof obj.body === 'string') {
    return { success, body: obj.body }
  }
  return { success, body: success ? '操作成功' : '操作失败' }
}

export interface NpmOptions {
  npmRegistry: string
  npmProxy: string
}

/** 构造传给 picgo 的 npm 选项（空值不传，避免覆盖 picgo 自己的默认）。 */
function npmOptions(opts: NpmOptions): { npmRegistry?: string; npmProxy?: string } {
  const out: { npmRegistry?: string; npmProxy?: string } = {}
  if (opts.npmRegistry !== '') out.npmRegistry = opts.npmRegistry
  if (opts.npmProxy !== '') out.npmProxy = opts.npmProxy
  return out
}

/** 安装插件。 */
export async function installPlugins(
  picgo: IPicGo,
  names: string[],
  opts: NpmOptions
): Promise<PluginHandlerResult> {
  const raw = await picgo.pluginHandler.install(names, npmOptions(opts) as never)
  return normalizeHandlerResult(raw)
}

/** 卸载插件。 */
export async function uninstallPlugins(
  picgo: IPicGo,
  names: string[]
): Promise<PluginHandlerResult> {
  const raw = await picgo.pluginHandler.uninstall(names)
  return normalizeHandlerResult(raw)
}

/** 更新插件（`names` 为空数组时 picgo 会更新全部）。 */
export async function updatePlugins(
  picgo: IPicGo,
  names: string[],
  opts: NpmOptions
): Promise<PluginHandlerResult> {
  const raw = await picgo.pluginHandler.update(names, npmOptions(opts) as never)
  return normalizeHandlerResult(raw)
}

/**
 * 启用 / 禁用插件。
 *
 * 通过写 `picgoPlugins[<name>] = enabled` 实现（与 picgo 自身一致）。
 * 使其实时生效需要重启进程 —— 由 Go 侧决定何时重启。
 */
export function setPluginEnabled(picgo: IPicGo, name: string, enabled: boolean): void {
  picgo.saveConfig({ [`picgoPlugins.${name}`]: enabled })
}
