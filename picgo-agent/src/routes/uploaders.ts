/**
 * 上传器（驱动）与存储配置的端点（docs/API.md §13.3）。
 *
 * | 方法 | 路径 |
 * |---|---|
 * | GET | `/api/uploaders` |
 * | POST | `/api/uploaders/schema` |
 * | POST | `/api/uploaders/test` |
 * | GET | `/api/uploaders/configs?Type=` |
 * | POST | `/api/uploaders/configs` |
 * | DELETE | `/api/uploaders/configs?Type=&ConfigName=` |
 * | POST | `/api/uploader/use` |
 * | GET | `/api/transformers` |
 *
 * ⚠️ **配置里含明文凭据**（token / secret / password），因为这个接口只对
 * 本机的 Go 服务开放（`X-Agent-Token` + 127.0.0.1）。**脱敏是 Go 侧的职责**，
 * Go 绝不可把本接口的响应原样回传给前端。
 */

import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { Hono } from 'hono'
import type { AppContext } from '../context.js'
import {
  activeConfigItem,
  currentUploader,
  evaluateUploaderSchema,
  findConfigItem,
  getUploaderInfo,
  listConfigs,
  listUploaders,
  listUploaderTypes,
  removeConfig,
  upsertConfig,
  useUploader
} from '../picgo/uploaders.js'
import { performUpload } from '../picgo/upload.js'
import type {
  UploaderConfigUpsertData,
  UploaderConfigUpsertRequest,
  UploaderConfigsListData,
  UploaderSchemaData,
  UploaderSchemaRequest,
  UploaderTestData,
  UploaderTestRequest,
  UploaderUseRequest,
  UploadersListData,
  TransformersListData
} from '../types.js'
import { errInternal, errNotFound, errParam, messageOf, ok } from '../http/envelope.js'

/** 1x1 透明 PNG（用于连通性测试的最小合法图片）。 */
const TINY_PNG_BASE64 =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=='

/** 连通性测试文件的名字前缀（便于管理员在图床里辨认与清理）。 */
export const CONNECTIVITY_TEST_PREFIX = '_picgo-web-connectivity-test'

function readString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function nowSeconds(): number {
  return Math.floor(Date.now() / 1000)
}

/** 写一个临时 1x1 PNG，返回其路径（调用方负责删除目录）。 */
function writeTinyPng(dir: string): string {
  const file = path.join(dir, `${CONNECTIVITY_TEST_PREFIX}-${Date.now()}.png`)
  fs.writeFileSync(file, Buffer.from(TINY_PNG_BASE64, 'base64'))
  return file
}

export function uploaderRoutes(ctx: AppContext): Hono {
  const app = new Hono()

  // -------------------------------------------------------------------------
  // GET /api/uploaders
  // -------------------------------------------------------------------------
  app.get('/api/uploaders', (c) => {
    try {
      const uploaders = listUploaders(
        ctx.picgo,
        ctx.log,
        ctx.capabilities,
        ctx.picgoVersion,
        nowSeconds()
      )

      let transformer = 'path'
      try {
        const cfg = ctx.picgo.getConfig<Record<string, unknown>>('picBed')
        if (cfg && typeof cfg.transformer === 'string' && cfg.transformer !== '') {
          transformer = cfg.transformer
        }
      } catch {
        transformer = 'path'
      }

      const data: UploadersListData = {
        Uploaders: uploaders,
        Current: currentUploader(ctx.picgo),
        Transformer: transformer
      }
      return ok(c, data)
    } catch (error) {
      ctx.log.error('列出 uploader 失败', { err: error })
      return errInternal(c, messageOf(error))
    }
  })

  // -------------------------------------------------------------------------
  // POST /api/uploaders/schema  （dependsOn 联动重求值）
  // -------------------------------------------------------------------------
  app.post('/api/uploaders/schema', async (c) => {
    let body: UploaderSchemaRequest
    try {
      body = await c.req.json<UploaderSchemaRequest>()
    } catch (error) {
      return errParam(c, `请求体不是合法 JSON：${messageOf(error)}`)
    }

    const type = readString(body?.Type)
    if (type === '') return errParam(c, 'Type 必填')

    const info = getUploaderInfo(ctx.picgo, type)
    if (!info) return errNotFound(c, `未找到 uploader：${type}`)

    const answers =
      body.Answers && typeof body.Answers === 'object' && !Array.isArray(body.Answers)
        ? body.Answers
        : {}

    try {
      const schema = evaluateUploaderSchema(ctx.picgo, type, ctx.log, answers)
      const data: UploaderSchemaData = {
        Type: type,
        Name: info.Name,
        Config: schema,
        Capabilities:
          ctx.capabilities.get(type, ctx.picgoVersion) ??
          listUploaders(ctx.picgo, ctx.log, ctx.capabilities, ctx.picgoVersion, nowSeconds()).find(
            (u) => u.Type === type
          )?.Capabilities ??
          {
            SupportsPathTemplate: false,
            SupportsRemoteDelete: false,
            ServerRenames: false,
            ConfigFields: [],
            PathFieldNames: [],
            DetectedAt: nowSeconds(),
            PicgoVersion: ctx.picgoVersion
          }
      }
      return ok(c, data)
    } catch (error) {
      ctx.log.error('求值 uploader schema 失败', { type, err: error })
      return errInternal(c, messageOf(error))
    }
  })

  // -------------------------------------------------------------------------
  // POST /api/uploaders/test  （连通性测试）
  //
  // picgo 没有「测试驱动」的 API，因此采用**真上传一张 1x1 PNG** 的方式探测，
  // 结束后尽力清理（若驱动支持远端删除）。
  // 无法清理时会留一个 `_picgo-web-connectivity-test-*.png` 小文件 —— UI 需说明。
  // -------------------------------------------------------------------------
  app.post('/api/uploaders/test', async (c) => {
    let body: UploaderTestRequest
    try {
      body = await c.req.json<UploaderTestRequest>()
    } catch (error) {
      return errParam(c, `请求体不是合法 JSON：${messageOf(error)}`)
    }

    const type = readString(body?.Type)
    if (type === '') return errParam(c, 'Type 必填')

    const info = getUploaderInfo(ctx.picgo, type)
    if (!info) return errNotFound(c, `未找到 uploader：${type}`)

    const configName = readString(body.ConfigName)
    const inline = body.Config
    const hasInline = typeof inline === 'object' && inline !== null && !Array.isArray(inline)

    if (configName === '' && !hasInline) {
      return errParam(c, '必须提供 ConfigName（测已保存配置）或 Config（测未落盘的配置）')
    }

    // 解析出「用哪份配置来测」
    let testConfig: Record<string, unknown>
    if (hasInline) {
      testConfig = inline as Record<string, unknown>
    } else {
      const item = findConfigItem(ctx.picgo, type, configName) ?? activeConfigItem(ctx.picgo, type)
      if (!item) return errNotFound(c, `未找到配置：${type} / ${configName}`)
      testConfig = item as unknown as Record<string, unknown>
    }

    const started = Date.now()
    const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'picgo-web-test-'))
    let tmpFile = ''
    let uploadedURL = ''
    let rawItem: unknown = null

    // 记录原状，测完恢复（inline 模式会临时改内存配置）
    let prevBed: unknown
    let prevUploader = ''
    let prevCurrent = ''
    try {
      prevBed = ctx.picgo.getConfig(`picBed.${type}`)
      prevUploader = ctx.picgo.getConfig<string>('picBed.uploader') ?? ''
      prevCurrent = ctx.picgo.getConfig<string>('picBed.current') ?? ''
    } catch {
      prevBed = undefined
    }

    try {
      tmpFile = writeTinyPng(tmpDir)

      const uploaderTarget = hasInline
        ? undefined
        : { Type: type, ...(configName !== '' ? { ConfigName: configName } : {}) }

      if (hasInline) {
        // 只改内存（不落盘），并依赖 uploadGate 串行化保证不被并发干扰
        ctx.picgo.setConfig({
          [`picBed.${type}`]: testConfig,
          'picBed.uploader': type,
          'picBed.current': type
        })
      }

      const outcome = await performUpload(
        { picgo: ctx.picgo, log: ctx.log, gate: ctx.uploadGate },
        {
          Path: tmpFile,
          ...(uploaderTarget ? { Uploader: uploaderTarget } : {}),
          ...(hasInline ? { SupportsPathTemplate: true } : {}),
          Seq: 0,
          JobUID: 'connectivity-test',
          FileName: path.basename(tmpFile)
        }
      )

      const latency = Date.now() - started

      if (!outcome.ok) {
        const data: UploaderTestData = {
          Ok: false,
          Message: outcome.error,
          LatencyMs: latency
        }
        return ok(c, data, '连通性测试失败')
      }

      uploadedURL = outcome.data?.URL ?? ''
      rawItem = outcome.data?.Raw ?? null

      const data: UploaderTestData = {
        Ok: true,
        Message: `连通正常（测试文件已上传${uploadedURL !== '' ? `：${uploadedURL}` : ''}）`,
        LatencyMs: latency,
        Detail: uploadedURL,
        FileName: outcome.data?.FileName ?? path.basename(tmpFile)
      }
      return ok(c, data)
    } catch (error) {
      const latency = Date.now() - started
      const data: UploaderTestData = {
        Ok: false,
        Message: messageOf(error),
        LatencyMs: latency
      }
      return ok(c, data, '连通性测试失败')
    } finally {
      // 恢复内存配置
      if (hasInline) {
        try {
          ctx.picgo.setConfig({
            [`picBed.${type}`]: prevBed,
            'picBed.uploader': prevUploader,
            'picBed.current': prevCurrent
          })
        } catch (error) {
          ctx.log.warn('恢复测试前的配置失败', { err: error })
        }
      }

      // 清理临时文件
      try {
        fs.rmSync(tmpDir, { recursive: true, force: true })
      } catch {
        // 忽略
      }

      // 尽力清理已上传的测试文件（走 remove 事件约定）
      if (uploadedURL !== '' && rawItem !== null) {
        try {
          const { performRemove } = await import('../picgo/remove.js')
          await performRemove(
            {
              picgo: ctx.picgo,
              log: ctx.log,
              cache: ctx.capabilities,
              timeoutMs: ctx.env.RemoveTimeoutMs
            },
            [rawItem as never],
            type
          )
        } catch (error) {
          ctx.log.warn('清理连通性测试文件失败（可忽略）', { err: error })
        }
      }
    }
  })

  // -------------------------------------------------------------------------
  // GET /api/uploaders/configs?Type=
  // -------------------------------------------------------------------------
  app.get('/api/uploaders/configs', (c) => {
    const type = readString(c.req.query('Type') ?? c.req.query('type'))
    if (type === '') return errParam(c, 'Type 必填')

    if (!listUploaderTypes(ctx.picgo).includes(type)) {
      return errNotFound(c, `未找到 uploader 类型：${type}`)
    }

    try {
      const { configs, defaultConfigName } = listConfigs(ctx.picgo, type)
      const data: UploaderConfigsListData = {
        Type: type,
        DefaultConfigName: defaultConfigName,
        // ⚠️ 原样返回（含 _id / _configName 与驱动字段），不做命名转换
        Configs: configs
      }
      return ok(c, data)
    } catch (error) {
      ctx.log.error('读取 uploader 配置列表失败', { type, err: error })
      return errInternal(c, messageOf(error))
    }
  })

  // -------------------------------------------------------------------------
  // POST /api/uploaders/configs  （createOrUpdate）
  // -------------------------------------------------------------------------
  app.post('/api/uploaders/configs', async (c) => {
    let body: UploaderConfigUpsertRequest
    try {
      body = await c.req.json<UploaderConfigUpsertRequest>()
    } catch (error) {
      return errParam(c, `请求体不是合法 JSON：${messageOf(error)}`)
    }

    const type = readString(body?.Type)
    if (type === '') return errParam(c, 'Type 必填')

    if (!listUploaderTypes(ctx.picgo).includes(type)) {
      return errNotFound(c, `未找到 uploader 类型：${type}`)
    }

    const configName = readString(body.ConfigName)
    if (configName === '') return errParam(c, 'ConfigName 必填')

    const config =
      body.Config && typeof body.Config === 'object' && !Array.isArray(body.Config)
        ? (body.Config as Record<string, unknown>)
        : {}

    try {
      const created = upsertConfig(ctx.picgo, type, configName, config, body.Activate === true)
      // 配置变了 → 能力（路径字段）可能变化 → 让缓存失效
      ctx.capabilities.invalidateType(type)

      const data: UploaderConfigUpsertData = {
        Type: type,
        Config: created
      }
      return ok(c, data)
    } catch (error) {
      ctx.log.error('保存 uploader 配置失败', { type, configName, err: error })
      return errInternal(c, messageOf(error))
    }
  })

  // -------------------------------------------------------------------------
  // DELETE /api/uploaders/configs?Type=&ConfigName=
  // -------------------------------------------------------------------------
  app.delete('/api/uploaders/configs', (c) => {
    const type = readString(c.req.query('Type') ?? c.req.query('type'))
    const configName = readString(c.req.query('ConfigName') ?? c.req.query('configName'))

    if (type === '') return errParam(c, 'Type 必填')
    if (configName === '') return errParam(c, 'ConfigName 必填')

    try {
      removeConfig(ctx.picgo, type, configName)
      ctx.capabilities.invalidateType(type)
      return ok(c, { Type: type, ConfigName: configName }, '已删除')
    } catch (error) {
      ctx.log.error('删除 uploader 配置失败', { type, configName, err: error })
      return errInternal(c, messageOf(error))
    }
  })

  // -------------------------------------------------------------------------
  // POST /api/uploader/use  （切换当前上传器）
  // -------------------------------------------------------------------------
  app.post('/api/uploader/use', async (c) => {
    let body: UploaderUseRequest
    try {
      body = await c.req.json<UploaderUseRequest>()
    } catch (error) {
      return errParam(c, `请求体不是合法 JSON：${messageOf(error)}`)
    }

    const type = readString(body?.Type)
    if (type === '') return errParam(c, 'Type 必填')

    if (!listUploaderTypes(ctx.picgo).includes(type)) {
      return errNotFound(c, `未找到 uploader 类型：${type}`)
    }

    try {
      const configName = readString(body.ConfigName)
      const active = useUploader(ctx.picgo, type, configName !== '' ? configName : undefined)
      return ok(c, { Type: type, Config: active })
    } catch (error) {
      ctx.log.error('切换 uploader 失败', { type, err: error })
      return errInternal(c, messageOf(error))
    }
  })

  // -------------------------------------------------------------------------
  // GET /api/transformers
  // -------------------------------------------------------------------------
  app.get('/api/transformers', (c) => {
    try {
      const types = ctx.picgo.helper.transformer.getIdList()
      let current = 'path'
      try {
        const cfg = ctx.picgo.getConfig<Record<string, unknown>>('picBed')
        if (cfg && typeof cfg.transformer === 'string' && cfg.transformer !== '') {
          current = cfg.transformer
        }
      } catch {
        current = 'path'
      }

      const data: TransformersListData = {
        Current: current,
        Transformers: types.map((t) => {
          const helper = ctx.picgo.helper.transformer.get(t)
          const name = typeof helper?.name === 'string' && helper.name !== '' ? helper.name : t
          return { Type: t, Name: name }
        })
      }
      return ok(c, data)
    } catch (error) {
      ctx.log.error('列出 transformer 失败', { err: error })
      return errInternal(c, messageOf(error))
    }
  })

  return app
}
