/**
 * 统一响应信封（docs/API.md §13）。
 *
 *     { Code, Message, Data }
 *
 * `Code` 是**字符串**：`OK` / `ERR_PARAM` / `ERR_PICGO` / `ERR_NOT_FOUND` / `ERR_INTERNAL`。
 * 与 Go 侧的数字码不同 —— 两边刻意如此（agent 契约是内网专用，字符串更利于排查日志）。
 */

import type { Context } from 'hono'
import { describeError } from '../errors.js'
import { Code, type CodeValue, type Envelope } from '../types.js'

/** 各 Code 对应的 HTTP 状态码。 */
export function httpStatusOf(code: CodeValue): number {
  switch (code) {
    case Code.OK:
      return 200
    case Code.ERR_PARAM:
      return 400
    case Code.ERR_NOT_FOUND:
      return 404
    case Code.ERR_PICGO:
    case Code.ERR_INTERNAL:
      // 上传失败用 200 返回业务码（Go 需逐项记录失败原因，不应被 HTTP 层拦截）。
      // 由调用方通过 `ok200` 显式选择；默认给 500 便于区分「调用方式错误」。
      return 500
    default:
      return 500
  }
}

function send<T>(c: Context, status: number, body: Envelope<T>): Response {
  return c.json(body, status as never)
}

/** 成功。 */
export function ok<T>(c: Context, data: T, message = 'ok'): Response {
  return send(c, 200, { Code: Code.OK, Message: message, Data: data })
}

/**
 * 业务失败但仍返回 HTTP 200。
 *
 * 用于「上传失败」这类**已正确处理但业务未成功**的场景：
 * Go 侧需要读到 `ERR_PICGO` 才能把该 item 标为 failed 并记录原因（D39）。
 */
export function fail200<T>(c: Context, code: CodeValue, message: string, data: T | null = null): Response {
  return send(c, 200, { Code: code, Message: message, Data: data })
}

/** 参数错误（HTTP 400）。 */
export function errParam(c: Context, message: string, data: unknown = null): Response {
  return send(c, 400, { Code: Code.ERR_PARAM, Message: message, Data: data })
}

/** 资源不存在（HTTP 404）。 */
export function errNotFound(c: Context, message: string): Response {
  return send(c, 404, { Code: Code.ERR_NOT_FOUND, Message: message, Data: null })
}

/** picgo 侧错误（默认 HTTP 500）。 */
export function errPicgo(c: Context, message: string, data: unknown = null): Response {
  return send(c, 500, { Code: Code.ERR_PICGO, Message: message, Data: data })
}

/** 未预期错误（HTTP 500）。消息会脱敏，不泄露内部细节。 */
export function errInternal(c: Context, message = '内部错误'): Response {
  return send(c, 500, { Code: Code.ERR_INTERNAL, Message: message, Data: null })
}

/**
 * 把任意异常归一化成对外的 message。
 *
 * 内部走 {@link describeError}，能正确处理 picgo 驱动抛出的非 Error 对象
 * （否则会退化成 `"[object Object]"`，把失败原因丢掉）。
 *
 * **不泄露凭据**：只取 message 类字段与 body 摘要；栈信息只进日志，不进响应。
 */
export function messageOf(error: unknown): string {
  return describeError(error)
}
