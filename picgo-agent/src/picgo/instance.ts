/**
 * 单例 `PicGo` 实例（D8）。
 *
 * 为什么必须是单例：picgo-core 是**全局单配置**的（`~/.picgo/config.json` 语义），
 * 插件加载、`helper.uploader` 注册表、`pluginHandler` 都挂在实例上。
 * 多实例会各自 `require` 一遍插件并各自维护注册表，既浪费内存也难以协调。
 *
 * 并发问题由我们的补丁解决（`UploadOptions.uploader` + per-context 配置覆盖），
 * 见 PicGo-Core 的 `FORK-NOTES.md`。
 */

import fs from 'node:fs'
import path from 'node:path'
import { PicGo } from '@yeqingky/picgo-core'
import type { IPicGo } from '@yeqingky/picgo-core'
import type { AgentEnv } from '../env.js'
import type { Logger } from '../logger.js'

let instance: IPicGo | null = null

/** 已初始化的配置路径（测试与诊断用）。 */
export function configPathOf(env: AgentEnv): string {
  return env.ConfigPath
}

/**
 * 确保 picgo 的 baseDir（config.json 所在目录）存在。
 *
 * 这一步很关键：`baseDir` 同时也是**插件安装目录**
 * （`PluginLoader.load()` 读 `<baseDir>/package.json` 的 dependencies，
 * 并从 `<baseDir>/node_modules/` 解析插件）。
 *
 * 我们刻意把它指向 `<dataDir>/picgo/`，**不污染用户主目录**。
 */
export function ensureBaseDir(env: AgentEnv): void {
  const baseDir = path.dirname(env.ConfigPath)
  fs.mkdirSync(baseDir, { recursive: true })

  // 插件清单文件：picgo 的 PluginLoader 会读它
  const pkgPath = path.join(baseDir, 'package.json')
  if (!fs.existsSync(pkgPath)) {
    fs.writeFileSync(
      pkgPath,
      JSON.stringify(
        {
          name: 'picgo-web-plugins',
          version: '1.0.0',
          description: 'Plugin manifest for PicGo-Web (managed by picgo-agent)',
          private: true,
          dependencies: {}
        },
        null,
        2
      )
    )
  }
}

/**
 * 取单例 PicGo。首次调用时构造。
 *
 * 构造时会读（或创建）`config.json`，因此调用前必须 `ensureBaseDir`。
 */
export function getPicgo(env: AgentEnv, log: Logger): IPicGo {
  if (instance) return instance

  ensureBaseDir(env)

  log.info('初始化 PicGo 实例', { configPath: env.ConfigPath })

  // 注意：依赖图里 picgo 是 CommonJS 包（main: dist/index.cjs.js）。
  // Node 的 ESM 互操作能把具名导出识别出来，所以这里可以直接 `import { PicGo }`。
  const picgo = new PicGo(env.ConfigPath) as unknown as IPicGo

  // 与 GUI 保持一致的运行时标记：插件会用它做环境判断（如跳过 Electron 专属路径）
  picgo.saveConfig({ PICGO_ENV: 'WEBUI' })

  // 若 Go 侧配置了上传代理，落成 picgo 的配置（插件普遍读 picBed.proxy）
  if (env.UploadProxy !== '') {
    picgo.saveConfig({ 'picBed.proxy': env.UploadProxy })
  }

  instance = picgo
  return picgo
}

/** 是否已初始化。 */
export function hasPicgo(): boolean {
  return instance !== null
}

/** 仅供测试使用：丢弃单例。 */
export function resetPicgoForTest(): void {
  instance = null
}

/** picgo 的 baseDir（config.json 的父目录，也是插件目录）。 */
export function baseDirOf(env: AgentEnv): string {
  return path.dirname(env.ConfigPath)
}
