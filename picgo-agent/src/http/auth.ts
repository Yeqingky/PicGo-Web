/**
 * `X-Agent-Token` 校验中间件。
 *
 * 安全约束（见 docs/ARCHITECTURE.md 安全清单）：
 * - agent 只监听 127.0.0.1，但**仍然**要求令牌（防同机其它进程误调）
 * - 令牌比较用**恒定时间**算法，避免计时侧信道
 * - 失败一律 401，且**不回显**任何与令牌有关的信息
 */

import { createMiddleware } from 'hono/factory'
import type { Context, Next } from 'hono'
import { timingSafeEqual } from 'node:crypto'

export const AGENT_TOKEN_HEADER = 'X-Agent-Token'

/** 健康检查与 shutdown 之外的所有端点都要令牌。 */
const PUBLIC_PATHS = new Set(['/healthz'])

function safeEqual(a: string, b: string): boolean {
  const bufA = Buffer.from(a, 'utf8')
  const bufB = Buffer.from(b, 'utf8')
  // 长度不同时先比一次自身再返回 false，保持「比较耗时与长度无关」的近似性质
  if (bufA.length !== bufB.length) {
    timingSafeEqual(bufA, bufA)
    return false
  }
  return timingSafeEqual(bufA, bufB)
}

export interface AgentAuthOptions {
  /** 期望令牌。 */
  token: string
  /** 允许无令牌（仅本地调试）。 */
  allowNoToken: boolean
  /** 日志记录器（记录被拒请求）。 */
  onReject?: (c: Context, reason: string) => void
}

export function agentAuth(options: AgentAuthOptions) {
  return createMiddleware(async (c: Context, next: Next) => {
    const path = c.req.path
    if (PUBLIC_PATHS.has(path)) {
      await next()
      return
    }

    if (options.allowNoToken) {
      await next()
      return
    }

    if (options.token === '') {
      options.onReject?.(c, 'agent 未配置令牌且未开启 allow-no-token，拒绝所有请求')
      return c.json(
        { Code: 'ERR_INTERNAL', Message: 'agent 未配置 X-Agent-Token', Data: null },
        500
      )
    }

    const provided = c.req.header(AGENT_TOKEN_HEADER) ?? ''
    if (provided === '' || !safeEqual(provided, options.token)) {
      options.onReject?.(c, provided === '' ? '缺少令牌' : '令牌不匹配')
      return c.json({ Code: 'ERR_PARAM', Message: '未授权', Data: null }, 401)
    }

    await next()
  })
}
