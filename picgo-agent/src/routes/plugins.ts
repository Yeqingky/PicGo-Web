/**
 * 插件端点（docs/API.md §13.4）。
 *
 * 安装 / 卸载 / 更新都是**长任务**（npm 可能要几十秒），因此：
 * 1. 立即建 job 并返回 `JobUID`
 * 2. 后台执行，npm 的每行输出都进 job 日志并通过 SSE 推给前端
 * 3. 成功后**重启自身进程**（`process.exit(0)`），由 Go 重新拉起
 *
 * 为什么必须重启：Node 的 `require` 缓存 + 模块解析在 `node_modules` 变化后极不可靠。
 * 插件的启用状态、`helper.uploader` 注册表、`resolvePlugin` 的路径解析
 * 都建立在「进程启动时的模块图」上。重启是最干净、最可预测的做法，
 * 而且插件装卸是低频操作，代价可以接受。
 */

import { Hono } from 'hono'
import type { AppContext } from '../context.js'
import {
  installPlugins,
  listPlugins,
  readPluginReadme,
  setPluginEnabled,
  uninstallPlugins,
  updatePlugins,
  type PluginHandlerResult
} from '../picgo/plugins.js'
import type { PluginOpData, PluginOpRequest, PluginsListData, PluginToggleRequest } from '../types.js'
import { errInternal, errNotFound, errParam, messageOf, ok } from '../http/envelope.js'
import { EventName } from '../jobs/sse.js'
import type { JobFinishedPayload, JobLogPayload, SystemNoticePayload } from '../jobs/sse.js'

function readString(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

/** 把多行文本拆成日志行（npm 输出是流式的，按行推更自然）。 */
function splitLines(text: string): string[] {
  return text
    .split(/\r?\n/)
    .map((l) => l.trimEnd())
    .filter((l) => l !== '')
}

export interface PluginJobOptions {
  /** job 完成后是否重启进程（安装/卸载需要；更新也需要）。 */
  restartAfter: boolean
  /** 重启延迟（毫秒），让响应先发出去。 */
  restartDelayMs?: number
}

export function pluginRoutes(ctx: AppContext): Hono {
  const app = new Hono()

  /** 把一次插件操作包成 job 并异步执行。 */
  function runPluginJob(
    kind: string,
    names: string[],
    payload: Record<string, unknown>,
    run: () => Promise<PluginHandlerResult>,
    options: PluginJobOptions
  ): PluginOpData {
    const jobUID = ctx.jobs.create(kind, { ...payload, Names: names })
    ctx.jobs.start(jobUID)
    ctx.sse.broadcast(EventName.JobStarted, { JobUID: jobUID, Kind: kind })

    const logLine = (line: string): void => {
      const seq = ctx.jobs.appendLog(jobUID, line)
      const p: JobLogPayload = { JobUID: jobUID, Seq: seq ?? 0, Line: line }
      ctx.sse.broadcast(EventName.JobLog, p)
    }

    logLine(`[agent] 开始 ${kind}：${names.length > 0 ? names.join(', ') : '(全部)'}`)

    // 后台执行（不 await）
    run()
      .then((result) => {
        for (const line of splitLines(result.body)) logLine(line)

        if (result.success) {
          ctx.jobs.succeed(jobUID, { Success: true, Body: result.body })
          logLine(`[agent] ${kind} 成功`)

          const fin: JobFinishedPayload = {
            JobUID: jobUID,
            Status: 'succeeded',
            Result: { Success: true },
            Error: ''
          }
          ctx.sse.broadcast(EventName.JobFinished, fin)

          if (options.restartAfter) {
            const notice: SystemNoticePayload = {
              Level: 'warn',
              Message: '插件已变更，内核正在重启以使其生效（期间上传不可用）'
            }
            ctx.sse.broadcast(EventName.SystemNotice, notice)
            logLine('[agent] 内核即将重启以加载插件变更…')

            const delay = options.restartDelayMs ?? 1000
            setTimeout(() => {
              ctx.log.warn('插件变更后主动退出，等待 Go 重新拉起', { jobUID })
              ctx.sse.shutdown()
              process.exit(0)
            }, delay).unref?.()
          }
        } else {
          ctx.jobs.fail(jobUID, result.body)
          const fin: JobFinishedPayload = {
            JobUID: jobUID,
            Status: 'failed',
            Result: { Success: false },
            Error: result.body
          }
          ctx.sse.broadcast(EventName.JobFinished, fin)
          logLine(`[agent] ${kind} 失败：${result.body}`)
        }
      })
      .catch((error: unknown) => {
        const message = messageOf(error)
        ctx.jobs.fail(jobUID, message)
        logLine(`[agent] ${kind} 异常：${message}`)
        const fin: JobFinishedPayload = {
          JobUID: jobUID,
          Status: 'failed',
          Result: null,
          Error: message
        }
        ctx.sse.broadcast(EventName.JobFinished, fin)
        ctx.log.error('插件任务异常', { kind, jobUID, err: error })
      })

    return {
      JobUID: jobUID,
      RestartPending: options.restartAfter,
      Message: options.restartAfter
        ? '任务已排队；完成后内核会重启一次'
        : '任务已排队'
    }
  }

  // -------------------------------------------------------------------------
  // GET /api/plugins
  // -------------------------------------------------------------------------
  app.get('/api/plugins', (c) => {
    try {
      const data: PluginsListData = listPlugins(ctx.picgo, ctx.baseDir, ctx.log)
      return ok(c, data)
    } catch (error) {
      ctx.log.error('列出插件失败', { err: error })
      return errInternal(c, messageOf(error))
    }
  })

  // -------------------------------------------------------------------------
  // GET /api/plugins/{name}/readme
  // -------------------------------------------------------------------------
  app.get('/api/plugins/:name/readme', (c) => {
    const name = readString(c.req.param('name'))
    if (name === '') return errParam(c, '插件名必填')

    const readme = readPluginReadme(ctx.baseDir, name)
    if (!readme) return errNotFound(c, `未找到 ${name} 的 README`)

    return ok(c, readme)
  })

  // -------------------------------------------------------------------------
  // POST /api/plugins/install
  // -------------------------------------------------------------------------
  app.post('/api/plugins/install', async (c) => {
    let body: PluginOpRequest
    try {
      body = await c.req.json<PluginOpRequest>()
    } catch (error) {
      return errParam(c, `请求体不是合法 JSON：${messageOf(error)}`)
    }

    if (!Array.isArray(body?.Names) || body.Names.length === 0) {
      return errParam(c, 'Names 必须是非空数组')
    }

    const names = body.Names.map((n) => readString(n)).filter((n) => n !== '')
    if (names.length === 0) return errParam(c, 'Names 里没有有效插件名')

    const data = runPluginJob(
      'plugin.install',
      names,
      { Operation: 'install' },
      () =>
        installPlugins(ctx.picgo, names, {
          npmRegistry: ctx.env.NpmRegistry,
          npmProxy: ctx.env.NpmProxy
        }),
      { restartAfter: true }
    )
    return ok(c, data)
  })

  // -------------------------------------------------------------------------
  // POST /api/plugins/uninstall
  // -------------------------------------------------------------------------
  app.post('/api/plugins/uninstall', async (c) => {
    let body: PluginOpRequest
    try {
      body = await c.req.json<PluginOpRequest>()
    } catch (error) {
      return errParam(c, `请求体不是合法 JSON：${messageOf(error)}`)
    }

    if (!Array.isArray(body?.Names) || body.Names.length === 0) {
      return errParam(c, 'Names 必须是非空数组')
    }

    const names = body.Names.map((n) => readString(n)).filter((n) => n !== '')
    if (names.length === 0) return errParam(c, 'Names 里没有有效插件名')

    const data = runPluginJob(
      'plugin.uninstall',
      names,
      { Operation: 'uninstall' },
      () => uninstallPlugins(ctx.picgo, names),
      { restartAfter: true }
    )
    return ok(c, data)
  })

  // -------------------------------------------------------------------------
  // POST /api/plugins/update
  // -------------------------------------------------------------------------
  app.post('/api/plugins/update', async (c) => {
    let body: PluginOpRequest
    try {
      body = await c.req.json<PluginOpRequest>()
    } catch (error) {
      return errParam(c, `请求体不是合法 JSON：${messageOf(error)}`)
    }

    // 空数组 = 更新全部（picgo 语义）
    const names = Array.isArray(body?.Names)
      ? body.Names.map((n) => readString(n)).filter((n) => n !== '')
      : []

    const data = runPluginJob(
      'plugin.update',
      names,
      { Operation: 'update' },
      () =>
        updatePlugins(ctx.picgo, names, {
          npmRegistry: ctx.env.NpmRegistry,
          npmProxy: ctx.env.NpmProxy
        }),
      { restartAfter: true }
    )
    return ok(c, data)
  })

  // -------------------------------------------------------------------------
  // PATCH /api/plugins/{name}  （启用 / 禁用）
  // -------------------------------------------------------------------------
  app.patch('/api/plugins/:name', async (c) => {
    const name = readString(c.req.param('name'))
    if (name === '') return errParam(c, '插件名必填')

    let body: PluginToggleRequest
    try {
      body = await c.req.json<PluginToggleRequest>()
    } catch (error) {
      return errParam(c, `请求体不是合法 JSON：${messageOf(error)}`)
    }

    if (typeof body?.Enabled !== 'boolean') {
      return errParam(c, 'Enabled 必须是布尔值')
    }

    try {
      setPluginEnabled(ctx.picgo, name, body.Enabled)
      // 启用状态变化会影响 getList()（是否加载插件），能力缓存应失效
      ctx.capabilities.invalidate()

      return ok(
        c,
        { Name: name, Enabled: body.Enabled, RestartPending: true },
        '已更新启用状态；内核需重启一次才能生效'
      )
    } catch (error) {
      ctx.log.error('切换插件启用状态失败', { name, err: error })
      return errInternal(c, messageOf(error))
    }
  })

  return app
}
