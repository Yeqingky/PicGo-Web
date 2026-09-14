/**
 * 配置端点（docs/API.md §13.2）。
 *
 * | 方法 | 路径 | 说明 |
 * |---|---|---|
 * | GET | `/api/config` | 完整 picgo config（**未脱敏**，仅内网） |
 * | PUT | `/api/config` | 整体替换（**实际只替换管辖键**，D22） |
 * | PATCH | `/api/config` | 点路径合并（只允许管辖键） |
 */

import { Hono } from 'hono'
import type { AppContext } from '../context.js'
import { readConfig, patchConfig, putConfig, pluginPrivateKeys } from '../picgo/config.js'
import type { ConfigGetData, ConfigPatchData, ConfigPatchRequest, ConfigPutRequest } from '../types.js'
import { errInternal, errParam, messageOf, ok } from '../http/envelope.js'

export function configRoutes(ctx: AppContext): Hono {
  const app = new Hono()

  app.get('/api/config', (c) => {
    const config = readConfig(ctx.picgo)
    const data: ConfigGetData = {
      Config: config,
      ConfigPath: ctx.env.ConfigPath
    }
    return ok(c, data)
  })

  app.put('/api/config', async (c) => {
    let body: ConfigPutRequest
    try {
      body = await c.req.json<ConfigPutRequest>()
    } catch (error) {
      return errParam(c, `请求体不是合法 JSON：${messageOf(error)}`)
    }

    if (typeof body?.Config !== 'object' || body.Config === null || Array.isArray(body.Config)) {
      return errParam(c, 'Config 必须是对象')
    }

    try {
      const result = putConfig(ctx.picgo, ctx.env, ctx.log, body.Config)
      const data: ConfigPatchData = result
      return ok(c, data, '配置已替换（仅管辖键）')
    } catch (error) {
      // 拒绝写入属于调用方错误
      const message = messageOf(error)
      ctx.log.warn('PUT /api/config 被拒绝', { err: message })
      return errParam(c, message)
    }
  })

  app.patch('/api/config', async (c) => {
    let body: ConfigPatchRequest
    try {
      body = await c.req.json<ConfigPatchRequest>()
    } catch (error) {
      return errParam(c, `请求体不是合法 JSON：${messageOf(error)}`)
    }

    if (typeof body?.Patch !== 'object' || body.Patch === null || Array.isArray(body.Patch)) {
      return errParam(c, 'Patch 必须是对象（点路径 → 值）')
    }

    try {
      const result = patchConfig(ctx.picgo, ctx.env, ctx.log, body.Patch)
      const data: ConfigPatchData = result
      return ok(c, data)
    } catch (error) {
      const message = messageOf(error)
      // 非管辖键 / 空 patch 等属于调用方错误
      if (message.includes('拒绝')) {
        ctx.log.warn('PATCH /api/config 被拒绝', { err: message })
        return errParam(c, message)
      }
      ctx.log.error('PATCH /api/config 失败', { err: error })
      return errInternal(c, message)
    }
  })

  return app
}

/** 供其它模块复用：当前插件的私有键列表（回归测试与诊断用）。 */
export function currentPluginPrivateKeys(ctx: AppContext): string[] {
  return pluginPrivateKeys(readConfig(ctx.picgo))
}
