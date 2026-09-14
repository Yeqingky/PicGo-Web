/**
 * picgo-agent 入口。
 *
 * 启动顺序：
 *
 *     env → logger → 校验令牌 → PicGo 单例 → 注册魔法命名钩子 → 建上下文 → 起 HTTP
 *
 * 退出：
 * - `POST /api/shutdown`（Go 关闭子进程时调用）
 * - `SIGTERM` / `SIGINT`
 * - 插件装卸成功后**主动** `process.exit(0)`（由 Go 重新拉起）
 */

import { serve } from '@hono/node-server'
import { loadEnv } from './env.js'
import { createLogger } from './logger.js'
import { createContext } from './context.js'
import { buildApp } from './http/server.js'
import { registerMagicPathHook } from './picgo/rename.js'
import { getPicgo } from './picgo/instance.js'
import { patchStatus } from './picgo/patch.js'

function readInt(key: string, fallback: number): number {
  const raw = process.env[key]
  if (raw === undefined) return fallback
  const n = Number.parseInt(raw.trim(), 10)
  return Number.isFinite(n) && n > 0 ? n : fallback
}

async function main(): Promise<void> {
  const env = loadEnv()
  const log = createLogger(env.LogLevel)

  log.info('picgo-agent 启动中', {
    listen: `${env.Host}:${env.Port}`,
    configPath: env.ConfigPath,
    logLevel: env.LogLevel
  })

  // ---- 令牌校验（fail fast，不要等第一个请求才发现没配） ----
  if (env.Token === '' && !env.AllowNoToken) {
    log.error(
      '未配置 PICGO_AGENT_TOKEN 且未开启 PICGO_AGENT_ALLOW_NO_TOKEN：拒绝启动。' +
        '（由 Go 拉起时会自动注入令牌；独立部署请自行设置）'
    )
    process.exit(1)
  }
  if (env.AllowNoToken) {
    log.warn('已开启 PICGO_AGENT_ALLOW_NO_TOKEN：任何本机进程都可调用 agent，仅限本地调试！')
  }
  if (env.Host !== '127.0.0.1' && env.Host !== 'localhost' && env.Host !== '::1') {
    log.warn('agent 监听在非回环地址上，存在被外部访问的风险', { host: env.Host })
  }

  // ---- 初始化 picgo 并注册钩子（必须在任何 upload 之前） ----
  const picgo = getPicgo(env, log)
  registerMagicPathHook(picgo, log)

  let shuttingDown = false
  const beginShutdown = (reason: string): void => {
    if (shuttingDown) return
    shuttingDown = true
    log.warn('开始关闭', { reason })
  }

  const ctx = createContext({
    env,
    log,
    uploadConcurrency: readInt('PICGO_AGENT_UPLOAD_CONCURRENCY', 1),
    onShutdownRequested: beginShutdown
  })

  const app = buildApp(ctx, {
    requestLogging: env.LogLevel === 'debug',
    shutdownDelayMs: 200
  })

  const server = serve(
    {
      fetch: app.fetch,
      hostname: env.Host,
      port: env.Port
    },
    (info) => {
      log.info('picgo-agent 已就绪', {
        address: `http://${env.Host}:${info.port}`,
        picgoVersion: ctx.picgoVersion,
        node: process.version
      })

      // 补丁探测：缺失时**立即告警**（并发上传不安全），并把结果暴露给 Go
      // （Go 侧会据此把 upload.concurrency 强制降到 1，见 internal/agent）
      const patches = patchStatus()
      if (patches.UploaderTarget && patches.ContextData) {
        log.info('picgo-core 补丁已就位（支持按次指定图床与事件归属）', {
          pkg: `${patches.PackageName}@${patches.PackageVersion}`
        })
      } else {
        log.warn('picgo-core 缺少本项目所需补丁 —— 并发上传不安全，Go 侧将强制降级为单并发', {
          pkg: `${patches.PackageName}@${patches.PackageVersion}`,
          uploaderTarget: patches.UploaderTarget,
          contextData: patches.ContextData,
          reason: patches.Error ?? '(未提供原因)'
        })
      }
    }
  )

  // ---- 优雅退出 ----
  const shutdown = (signal: string): void => {
    beginShutdown(signal)
    ctx.sse.shutdown()
    server.close(() => {
      log.info('HTTP 服务已关闭，进程退出')
      process.exit(0)
    })
    // 兜底：3 秒还没关干净就强退（避免 Go 侧等待超时）
    setTimeout(() => {
      log.warn('关闭超时，强制退出')
      process.exit(0)
    }, 3000).unref?.()
  }

  process.on('SIGTERM', () => shutdown('SIGTERM'))
  process.on('SIGINT', () => shutdown('SIGINT'))

  // 未捕获异常：记录后退出，让 Go 重新拉起（比带病运行更安全）
  process.on('uncaughtException', (error) => {
    log.error('未捕获异常，进程退出', { err: error })
    process.exit(1)
  })
  process.on('unhandledRejection', (reason) => {
    log.error('未处理的 Promise 拒绝（不退出）', { err: reason })
  })
}

main().catch((error: unknown) => {
  // 启动阶段失败：slog 可能还没建好，直接写 stderr
  process.stderr.write(
    `picgo-agent 启动失败: ${error instanceof Error ? (error.stack ?? error.message) : String(error)}\n`
  )
  process.exit(1)
})
