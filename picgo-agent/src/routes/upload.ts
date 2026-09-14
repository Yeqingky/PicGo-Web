/**
 * `POST /api/upload` —— 单文件同步上传（D39，docs/API.md §13.5）。
 *
 * ## 为什么单文件
 *
 * picgo 的 `uploadProgress` 只有四档（0/30/60/100），且是**该次 `upload()` 调用**的进度。
 * 一次传一个文件时，这四档就精确等于**这个文件**的进度，Go 侧聚合后进度条很顺滑。
 *
 * ## 失败不返回 5xx
 *
 * 上传失败时返回 **HTTP 200 + `Code = "ERR_PICGO"`**，让 Go 侧能逐项记录失败原因
 * （D39：`FailedItems` 与每项的 `Error`）。**只有**「目标非法」这类调用方错误才 400。
 */

import { Hono } from 'hono'
import type { AppContext } from '../context.js'
import { performUpload, InvalidUploadTargetError } from '../picgo/upload.js'
import type { UploadData, UploadErrorData, UploadRequest } from '../types.js'
import { fail200, errParam, errInternal, messageOf, ok } from '../http/envelope.js'
import { EventName } from '../jobs/sse.js'
import type { UploadFailedPayload, UploadFinishedPayload, UploadProgressPayload } from '../jobs/sse.js'

function readString(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

export function uploadRoutes(ctx: AppContext): Hono {
  const app = new Hono()

  app.post('/api/upload', async (c) => {
    let body: UploadRequest
    try {
      body = await c.req.json<UploadRequest>()
    } catch (error) {
      return errParam(c, `请求体不是合法 JSON：${messageOf(error)}`)
    }

    const seq = typeof body?.Seq === 'number' ? body.Seq : 0
    const jobUID = readString(body?.JobUID)
    const reqPath = readString(body?.Path)
    const fileName = reqPath === '' ? '' : (reqPath.split('/').pop() ?? reqPath)

    try {
      const outcome = await performUpload(
        { picgo: ctx.picgo, log: ctx.log, gate: ctx.uploadGate },
        body,
        {
          onProgress: (progress) => {
            const payload: UploadProgressPayload = {
              JobUID: jobUID,
              Seq: seq,
              FileName: fileName,
              Progress: progress
            }
            ctx.sse.broadcast(EventName.UploadProgress, payload)
          }
        }
      )

      if (outcome.ok && outcome.data) {
        const data: UploadData = outcome.data
        const payload: UploadFinishedPayload = {
          JobUID: jobUID,
          Seq: seq,
          FileName: data.FileName,
          URL: data.URL,
          Width: data.Width,
          Height: data.Height,
          Size: data.Size,
          UploaderType: data.UploaderType
        }
        ctx.sse.broadcast(EventName.UploadFinished, payload)
        return ok(c, data)
      }

      // ---- 失败 ----
      const errorData: UploadErrorData = { Seq: seq, Error: outcome.error, Raw: null }
      const failedPayload: UploadFailedPayload = {
        JobUID: jobUID,
        Seq: seq,
        FileName: fileName,
        Error: outcome.error
      }
      ctx.sse.broadcast(EventName.UploadFailed, failedPayload)

      if (outcome.kind === 'param') {
        // 目标非法 / 文件不存在 → 调用方错误
        return errParam(c, outcome.error, errorData)
      }

      // 上传失败 → HTTP 200 + ERR_PICGO（便于 Go 逐项记录）
      return fail200(c, 'ERR_PICGO', outcome.error, errorData)
    } catch (error) {
      // 兜底：不应到达（performUpload 内部已收敛），但保证不会有未捕获异常
      if (error instanceof InvalidUploadTargetError) {
        return errParam(c, error.message)
      }
      ctx.log.error('上传处理异常', { err: error })
      return errInternal(c, messageOf(error))
    }
  })

  return app
}
