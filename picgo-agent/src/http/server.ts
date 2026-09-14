/**
 * hono 应用装配。
 *
 * 中间件顺序：
 * 1. 请求日志（最外层，记录耗时与结果）
 * 2. `X-Agent-Token` 校验（`/healthz` 免）
 * 3. 路由
 * 4. 兜底错误处理（不泄露栈信息）
 */

import { Hono } from 'hono'
import { logger as honoLogger } from 'hono/logger'
import type { AppContext } from '../context.js'
import { agentAuth } from './auth.js'
import { messageOf } from './envelope.js'
import { healthzRoutes } from '../routes/healthz.js'
import { configRoutes } from '../routes/config.js'
import { uploaderRoutes } from '../routes/uploaders.js'
import { pluginRoutes } from '../routes/plugins.js'
import { uploadRoutes } from '../routes/upload.js'
import { removeRoutes } from '../routes/remove.js'
import { jobRoutes } from '../routes/jobs.js'
import { eventRoutes } from '../routes/events.js'
import { logRoutes } from '../routes/logs.js'
import { shutdownRoutes } from '../routes/shutdown.js'

export interface BuildAppOptions {
  /** 请求日志开关（默认关；hono 的 logger 打的是 stdout 文本，与我们 JSON 日志风格不一致）。 */
  requestLogging?: boolean
  /** shutdown 返回后的延迟。 */
  shutdownDelayMs?: number
}

export function buildApp(ctx: AppContext, options: BuildAppOptions = {}): Hono {
  const app = new Hono()

  // ---- 1. 请求日志（默认关闭） ----
  if (options.requestLogging === true) {
    app.use(
      '*',
      honoLogger((message: string, ...rest: string[]) => {
        ctx.log.debug('http', { line: `${message} ${rest.join(' ')}`.trim() })
      })
    )
  }

  // ---- 2. 鉴权 ----
  app.use(
    '*',
    agentAuth({
      token: ctx.env.Token,
      allowNoToken: ctx.env.AllowNoToken,
      onReject: (c, reason) => {
        ctx.log.warn('拒绝未授权请求', {
          path: c.req.path,
          method: c.req.method,
          reason,
          ip: c.req.header('x-forwarded-for') ?? ''
        })
      }
    })
  )

  // ---- 3. 路由 ----
  // 健康检查（免 token、无信封）
  app.route('/', healthzRoutes(ctx))

  // 业务端点（统一 {Code, Message, Data} 信封）
  app.route('/', configRoutes(ctx))
  app.route('/', uploaderRoutes(ctx))
  app.route('/', pluginRoutes(ctx))
  app.route('/', uploadRoutes(ctx))
  app.route('/', removeRoutes(ctx))
  app.route('/', jobRoutes(ctx))
  app.route('/', eventRoutes(ctx))
  app.route('/', logRoutes(ctx))
  app.route('/', shutdownRoutes(ctx, { delayMs: options.shutdownDelayMs ?? 200 }))

  // ---- 4. 404 ----
  app.notFound((c) => {
    return c.json(
      { Code: 'ERR_NOT_FOUND', Message: `未实现的端点：${c.req.method} ${c.req.path}`, Data: null },
      404
    )
  })

  // ---- 5. 兜底错误处理 ----
  app.onError((error, c) => {
    ctx.log.error('未捕获的请求异常', {
      path: c.req.path,
      method: c.req.method,
      err: error
    })
    // ⚠️ 不把栈信息返回给调用方
    return c.json({ Code: 'ERR_INTERNAL', Message: messageOf(error), Data: null }, 500)
  })

  return app
}
