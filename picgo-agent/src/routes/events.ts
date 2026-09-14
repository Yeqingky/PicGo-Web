/**
 * `GET /api/events` —— SSE（docs/API.md §13.6）。
 *
 * 事件名与 Go 侧自己的 SSE 同名同结构（`docs/API.md` §8.1），
 * 由 Go 订阅后转发并入它自己的 Hub。
 *
 * 保活：每 25 秒一条 `ping`。
 * 断开：`c.req.raw.signal` 的 abort 事件里清掉 writer（**必须**，否则连接泄漏）。
 */

import { Hono } from 'hono'
import { streamSSE } from 'hono/streaming'
import type { AppContext } from '../context.js'

export function eventRoutes(ctx: AppContext): Hono {
  const app = new Hono()

  app.get('/api/events', (c) => {
    c.header('Cache-Control', 'no-cache, no-transform')
    c.header('X-Accel-Buffering', 'no')

    return streamSSE(c, async (stream) => {
      let closed = false

      const writer = {
        write: (event: string, data: unknown): boolean => {
          if (closed) return false
          // streamSSE 的 writeSSE 是异步的；这里不 await（广播不应被慢客户端阻塞），
          // 失败时由 catch 捕获并标记断开。
          void stream
            .writeSSE({ event, data: JSON.stringify(data) })
            .catch(() => {
              closed = true
            })
          return !closed
        },
        close: (): void => {
          if (closed) return
          closed = true
          try {
            stream.close()
          } catch {
            // 已断开
          }
        }
      }

      const unsubscribe = ctx.sse.add(writer)

      // 客户端断开时清理（SSE 最容易泄漏的地方）
      const onAbort = (): void => {
        unsubscribe()
        writer.close()
      }
      try {
        c.req.raw.signal.addEventListener('abort', onAbort, { once: true })
      } catch {
        // 某些运行时可能不支持 signal，忽略（依赖 ping 写入失败来回收）
      }

      // 建连即发一条 hello，便于前端确认连接可用
      stream.writeSSE({
        event: 'system.notice',
        data: JSON.stringify({ Level: 'info', Message: '已连接到 PicGo 内核事件流' })
      }).catch(() => {
        closed = true
      })

      // 保持连接直到客户端断开：等到 abort 或 stream 结束
      await new Promise<void>((resolve) => {
        const done = (): void => resolve()
        try {
          c.req.raw.signal.addEventListener('abort', done, { once: true })
        } catch {
          // 若拿不到 signal，就用一个长定时器兜底（避免立即返回导致流被关闭）
          setTimeout(done, 24 * 60 * 60 * 1000)
        }
      }).finally(() => {
        unsubscribe()
      })
    })
  })

  return app
}
