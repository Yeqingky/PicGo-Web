/**
 * `POST /api/delete` —— 远端删除（D47，docs/API.md §13.5）。
 *
 * 语义见 `src/picgo/remove.ts` 的说明。要点：
 * - `Supported = false` → 该驱动不支持远端删除，Go 只删本地记录并记日志
 * - `Supported = true, RemoteDeleted = false` → 插件实现了但失败/结果未知
 * - `Supported = true, RemoteDeleted = true` → 远端已删除
 */

import { Hono } from 'hono'
import type { AppContext } from '../context.js'
import { performRemove } from '../picgo/remove.js'
import type { RawImgInfo, RemoveData, RemoveRequest } from '../types.js'
import { errParam, errInternal, messageOf, ok } from '../http/envelope.js'

function readString(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

export function removeRoutes(ctx: AppContext): Hono {
  const app = new Hono()

  app.post('/api/delete', async (c) => {
    let body: RemoveRequest
    try {
      body = await c.req.json<RemoveRequest>()
    } catch (error) {
      return errParam(c, `请求体不是合法 JSON：${messageOf(error)}`)
    }

    if (!Array.isArray(body?.Items)) {
      return errParam(c, 'Items 必须是数组（元素为上传时的原始 IImgInfo）')
    }

    const items = body.Items as RawImgInfo[]
    const uploaderType = readString(body.UploaderType)

    try {
      const result = await performRemove(
        {
          picgo: ctx.picgo,
          log: ctx.log,
          cache: ctx.capabilities,
          timeoutMs: ctx.env.RemoveTimeoutMs
        },
        items,
        uploaderType
      )

      const data: RemoveData = {
        RemoteDeleted: result.RemoteDeleted,
        Supported: result.Supported,
        Message: result.Message
      }
      return ok(c, data)
    } catch (error) {
      ctx.log.error('远端删除异常', { err: error, uploader: uploaderType })
      return errInternal(c, messageOf(error))
    }
  })

  return app
}
