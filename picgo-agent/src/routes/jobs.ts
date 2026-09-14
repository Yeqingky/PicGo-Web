/**
 * 任务端点（docs/API.md §13.6）。
 *
 * | 方法 | 路径 |
 * |---|---|
 * | GET | `/api/jobs` |
 * | GET | `/api/jobs/{uid}` |
 * | DELETE | `/api/jobs/{uid}` |
 *
 * ⚠️ agent 的任务只是**执行期状态**（内存），进程重启即丢。
 * 持久化的 `Jobs` / `JobItems` / `OperationLogs` 在 Go 侧。
 */

import { Hono } from 'hono'
import type { AppContext } from '../context.js'
import type { JobListData } from '../types.js'
import { errNotFound, errParam, ok } from '../http/envelope.js'

function readString(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

export function jobRoutes(ctx: AppContext): Hono {
  const app = new Hono()

  app.get('/api/jobs', (c) => {
    const data: JobListData = { Jobs: ctx.jobs.list() }
    return ok(c, data)
  })

  app.get('/api/jobs/:uid', (c) => {
    const uid = readString(c.req.param('uid'))
    if (uid === '') return errParam(c, 'JobUID 必填')

    const job = ctx.jobs.get(uid)
    if (!job) return errNotFound(c, `未找到任务：${uid}`)

    const logInfo = ctx.jobs.logs(uid)
    return ok(c, {
      ...job,
      Logs: logInfo?.lines ?? []
    })
  })

  app.delete('/api/jobs/:uid', (c) => {
    const uid = readString(c.req.param('uid'))
    if (uid === '') return errParam(c, 'JobUID 必填')

    const job = ctx.jobs.get(uid)
    if (!job) return errNotFound(c, `未找到任务：${uid}`)

    if (!ctx.jobs.remove(uid)) {
      return errParam(c, '任务仍在排队或执行中，无法清理')
    }
    return ok(c, { Deleted: 1 }, '已清理')
  })

  return app
}
