/**
 * `POST /api/shutdown` —— 优雅退出（docs/API.md §13.1）。
 *
 * 由 Go 在关闭子进程时调用（D7）。行为：
 * 1. 先返回响应（否则调用方会看到连接被重置）
 * 2. 延迟一小段，让在途请求收尾
 * 3. 关闭 SSE 连接 → `process.exit(0)`
 *
 * 不给「等待在跑任务结束」做长时间阻塞：任务状态由 Go 侧的队列负责，
 * agent 重启后在跑的上传会被 Go 标为失败并重试（`OPERATIONS.md` 的启动恢复）。
 */

import { Hono } from 'hono'
import type { AppContext } from '../context.js'
import { ok } from '../http/envelope.js'

export interface ShutdownOptions {
  /** 返回响应后延迟多久退出（毫秒）。 */
  delayMs: number
}

export function shutdownRoutes(ctx: AppContext, options: ShutdownOptions): Hono {
  const app = new Hono()

  app.post('/api/shutdown', (c) => {
    ctx.log.warn('收到关闭请求，准备退出')

    setTimeout(() => {
      ctx.onShutdownRequested('api/shutdown')
      ctx.sse.shutdown()
      // 给日志/连接一点收尾时间，然后退出
      setTimeout(() => process.exit(0), 100).unref?.()
    }, options.delayMs).unref?.()

    return ok(c, { ShuttingDown: true }, '正在关闭')
  })

  return app
}
