/**
 * 应用上下文：把到处都要用的依赖打成一包，避免层层传参。
 *
 * 这是一个**显式容器**（不是全局单例），因此单元测试可以构造独立的实例。
 */

import type { IPicGo } from 'picgo'
import type { AgentEnv } from './env.js'
import type { Logger } from './logger.js'
import type { CapabilityCache } from './picgo/capability.js'
import { CapabilityCache as CapabilityCacheImpl } from './picgo/capability.js'
import type { UploadGate } from './picgo/upload.js'
import { UploadGate as UploadGateImpl } from './picgo/upload.js'
import { JobStore } from './jobs/store.js'
import { SSEBroadcaster } from './jobs/sse.js'
import { baseDirOf, getPicgo } from './picgo/instance.js'

export interface AppContext {
  env: AgentEnv
  log: Logger
  picgo: IPicGo
  /** picgo 的 baseDir（= config.json 的父目录，也是插件目录）。 */
  baseDir: string
  jobs: JobStore
  sse: SSEBroadcaster
  capabilities: CapabilityCache
  uploadGate: UploadGate
  /** 进程启动时间（毫秒），用于 uptime。 */
  startedAt: number
  /** picgo-core 版本。 */
  picgoVersion: string
  /** 请求关闭的信号（由 shutdown 路由触发）。 */
  onShutdownRequested: (reason: string) => void
}

export interface CreateContextOptions {
  env: AgentEnv
  log: Logger
  /** 上传并发度（默认 1：严格串行，保证 failed 事件归属准确）。 */
  uploadConcurrency?: number
  onShutdownRequested?: (reason: string) => void
}

export function createContext(options: CreateContextOptions): AppContext {
  const { env, log } = options

  const picgo = getPicgo(env, log)

  const picgoVersion =
    typeof picgo.VERSION === 'string' && picgo.VERSION !== '' ? picgo.VERSION : 'unknown'

  return {
    env,
    log,
    picgo,
    baseDir: baseDirOf(env),
    jobs: new JobStore(),
    sse: new SSEBroadcaster({ logger: log }),
    capabilities: new CapabilityCacheImpl(),
    uploadGate: new UploadGateImpl(options.uploadConcurrency ?? 1),
    startedAt: Date.now(),
    picgoVersion,
    onShutdownRequested: options.onShutdownRequested ?? (() => {})
  }
}

/** 进程已运行的秒数。 */
export function uptimeSeconds(ctx: AppContext): number {
  return Math.floor((Date.now() - ctx.startedAt) / 1000)
}
