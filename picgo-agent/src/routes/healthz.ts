/**
 * `GET /healthz` —— **免 token、不使用信封**（docs/API.md §13.1）。
 *
 * 这是 agent 侧**唯一**不用信封的端点：
 * 供 Go 侧的子进程健康探测与容器探针直接解析，因此返回扁平的 PascalCase 对象。
 *
 *     { "Ok": true, "PicgoVersion": "3.0.2", "ConfigPath": "/data/picgo/config.json",
 *       "Uptime": 3600, "PID": 12345, "PluginsLoaded": 3 }
 *
 * 与 Go 侧自己的 `/healthz` 不同（那个用字段小写、面向 K8s）：
 * 这里是我们自己的内网契约，遵循 D81 的 PascalCase 规则。
 */

import { Hono } from 'hono'
import type { AppContext } from '../context.js'
import { uptimeSeconds } from '../context.js'
import { patchStatus } from '../picgo/patch.js'
import type { HealthzData } from '../types.js'

export function healthzRoutes(ctx: AppContext): Hono {
  const app = new Hono()

  app.get('/healthz', (c) => {
    let pluginsLoaded = 0
    try {
      pluginsLoaded = ctx.picgo.pluginLoader.getFullList().length
    } catch {
      pluginsLoaded = 0
    }

    const data: HealthzData = {
      Ok: true,
      PicgoVersion: ctx.picgoVersion,
      ConfigPath: ctx.env.ConfigPath,
      Uptime: uptimeSeconds(ctx),
      PID: process.pid,
      PluginsLoaded: pluginsLoaded,
      // 补丁状态：Go 侧据此决定并发度上限（见 picgo/patch.ts 的说明）
      Patches: patchStatus()
    }

    return c.json(data)
  })

  return app
}
