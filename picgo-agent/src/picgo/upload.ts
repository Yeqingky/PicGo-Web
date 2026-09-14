/**
 * 单文件同步上传（D39）。
 *
 * ## 失败语义（**最容易写错的地方**）
 *
 * picgo 的 `upload()` 传路径时：
 *
 * | 情形 | 行为 |
 * |---|---|
 * | 上传失败（网络 / 图床报错） | **既不 reject 也不返回 `Error`** —— 吞掉异常、返回**可能为空的数组**，同时 `emit('failed', err)` |
 * | `options.uploader` 指向不存在的 type/configName | **抛出异常**（我们补丁的前置校验） |
 *
 * 因此必须**两条路都覆盖**：
 * 1. `catch` → 判是否为目标非法（抛错路径）→ `ERR_PARAM`
 * 2. 返回值长度 0 / 是 Error → `ERR_PICGO`，并把 `failed` 事件捕获到的原因带上
 *
 * ## 事件归属
 *
 * picgo 的事件是**全局**的（emit 绑定到根实例）。我们通过：
 * - `contextData` 携带 `JobUID` / `Seq` / `magicPath`
 * - `finished` / `afterUpload` 事件会带 `ctx`，可从中读 `contextData` 精确归属
 * - `failed` 事件**只带 error**（不带 ctx），因此靠**上传串行化**保证归属正确
 *
 * 串行化默认开启（`PICGO_AGENT_UPLOAD_CONCURRENCY=1`），与 Go 侧默认并发度一致（D35）。
 * 调大并发时，成功/失败判定仍然可靠（看返回值长度），只有**错误文案**可能串到别的请求上。
 */

import fs from 'node:fs'
import type { IImgInfo, IPicGo } from 'picgo'
import { describeError } from '../errors.js'
import type { Logger } from '../logger.js'
import type { UploadData, UploadRequest } from '../types.js'
import type { MagicPathContext } from './rename.js'

/** 目标非法的错误标记（映射为 HTTP 400 / ERR_PARAM）。 */
export class InvalidUploadTargetError extends Error {
  override readonly name = 'InvalidUploadTargetError'
}

export interface UploadOutcome {
  ok: boolean
  data: UploadData | null
  error: string
  /** 失败时区分原因：目标非法（调用方错误）还是上传失败（图床/网络）。 */
  kind: 'param' | 'picgo'
}

/**
 * 上传串行化闸门。
 *
 * `limit` 为 1 时严格串行（默认），此时 `failed` 事件与本次上传一一对应，
 * 错误文案绝对准确。
 */
export class UploadGate {
  private running = 0
  private readonly waiting: Array<() => void> = []

  constructor(private readonly limit = 1) {}

  async acquire(): Promise<void> {
    if (this.running < this.limit) {
      this.running += 1
      return
    }
    await new Promise<void>((resolve) => this.waiting.push(resolve))
    this.running += 1
  }

  release(): void {
    this.running -= 1
    const next = this.waiting.shift()
    if (next) next()
  }

  /** 仅供测试/诊断。 */
  stats(): { running: number; waiting: number } {
    return { running: this.running, waiting: this.waiting.length }
  }
}

/** 把 picgo 侧的 `uploader` 选项从我们的 PascalCase 转成 picgo 的 camelCase。 */
function toPicgoUploader(
  uploader: UploadRequest['Uploader']
): { type: string; configName?: string } | undefined {
  if (!uploader || typeof uploader.Type !== 'string' || uploader.Type === '') return undefined
  const name = uploader.ConfigName
  return typeof name === 'string' && name !== ''
    ? { type: uploader.Type, configName: name }
    : { type: uploader.Type }
}

function toNumber(value: unknown, fallback = 0): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string') {
    const n = Number.parseInt(value, 10)
    if (Number.isFinite(n)) return n
  }
  return fallback
}

function toStringOrEmpty(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

/** 由 IImgInfo 组装对外响应。 */
export function toUploadData(img: IImgInfo, seq: number, fallbackType: string): UploadData {
  return {
    Seq: seq,
    URL: toStringOrEmpty(img.imgUrl),
    ThumbURL: toStringOrEmpty((img as Record<string, unknown>).thumbnailUrl),
    FileName: toStringOrEmpty(img.fileName),
    Extname: toStringOrEmpty(img.extname),
    Width: toNumber(img.width),
    Height: toNumber(img.height),
    Size: toNumber(img.size),
    ContentType: toStringOrEmpty(img.contentType),
    UploaderType: toStringOrEmpty(img.type) || fallbackType,
    // ⚠️ 原样回传（字段名是 picgo 的 IImgInfo 定义），Go 侧必须原样存进 UploadResults.RawOutput
    Raw: img
  }
}

export interface UploadDeps {
  picgo: IPicGo
  log: Logger
  gate: UploadGate
}

/**
 * 上传过程的观察钩子。
 *
 * 进度事件（`uploadProgress`）**只带一个数字、不带 ctx**，因此无法按
 * `contextData` 归属。钩子在**取得闸门之后**才被挂上，配合串行化保证
 * 「此刻收到的进度一定属于本次上传」。
 */
export interface UploadHooks {
  /** picgo 的四档进度：0 / 30 / 60 / 100（-1 表示失败）。 */
  onProgress?: (progress: number) => void
}

/**
 * 执行一次单文件上传。
 *
 * @returns 归一化结果（**不抛异常**；调用方按 `kind` 决定 HTTP 状态）
 */
export async function performUpload(
  deps: UploadDeps,
  req: UploadRequest,
  hooks: UploadHooks = {}
): Promise<UploadOutcome> {
  const { picgo, log, gate } = deps
  const seq = typeof req.Seq === 'number' ? req.Seq : 0
  const uploaderType = req.Uploader?.Type ?? ''

  // ---- 1. 前置校验：文件必须存在 ----
  if (typeof req.Path !== 'string' || req.Path === '') {
    return { ok: false, data: null, error: '缺少文件路径', kind: 'param' }
  }
  if (!fs.existsSync(req.Path)) {
    return { ok: false, data: null, error: `文件不存在：${req.Path}`, kind: 'param' }
  }

  await gate.acquire()
  try {
    // ---- 2. 组装 contextData（魔法命名 + 事件归属） ----
    const magicPath: MagicPathContext = {
      ...(req.PathTemplate !== undefined ? { pathTemplate: req.PathTemplate } : {}),
      ...(req.FileTemplate !== undefined ? { fileTemplate: req.FileTemplate } : {}),
      ...(req.UserUID !== undefined ? { userUID: req.UserUID } : {}),
      ...(req.SupportsPathTemplate !== undefined
        ? { supportsPathTemplate: req.SupportsPathTemplate }
        : {})
    }

    const contextData: Record<string, unknown> = {
      JobUID: req.JobUID ?? '',
      Seq: seq,
      magicPath
    }

    // ---- 3. 捕获 failed / uploadProgress 事件 ----
    //
    // ⚠️ 两个事件都只带 payload、不带 ctx（`failed` 带 error，`uploadProgress` 带数字），
    // 因此**无法**用 contextData 过滤。归属正确性完全依赖 `gate` 的串行化
    // （默认并发度 1）。这也是把「上传串行」作为默认行为的重要原因（D35）。
    // ⚠️ 必须用 describeError：picgo 驱动常抛**非 Error 对象**
    // （如 `{statusCode, body}`），直接 String() 会得到 "[object Object]"
    let capturedError: string | null = null
    const onFailed = (err: unknown): void => {
      if (capturedError !== null) return
      capturedError = describeError(err)
    }
    picgo.on('failed', onFailed)

    const onProgress = (progress: unknown): void => {
      if (typeof progress !== 'number') return
      if (progress < 0) return // -1 表示失败，由 failed 事件负责
      hooks.onProgress?.(progress)
    }
    if (hooks.onProgress) picgo.on('uploadProgress', onProgress)

    // ---- 4. 调用 upload ----
    const picgoUploader = toPicgoUploader(req.Uploader)

    let output: unknown
    try {
      output = await picgo.upload(
        [req.Path],
        {
          ...(picgoUploader ? { uploader: picgoUploader } : {}),
          contextData
        } as never
      )
    } catch (error) {
      // ⚠️ 抛错路径：只可能是「目标非法」（我们补丁的前置校验），属调用方错误
      const message = describeError(error)
      log.warn('上传目标非法或前置校验失败', {
        seq,
        uploader: uploaderType,
        configName: req.Uploader?.ConfigName ?? '',
        err: message
      })
      const wrapped = new InvalidUploadTargetError(message)
      return { ok: false, data: null, error: wrapped.message, kind: 'param' }
    } finally {
      picgo.off('failed', onFailed)
      if (hooks.onProgress) picgo.off('uploadProgress', onProgress)
    }

    // ---- 5. 判定结果 ----
    // ⚠️ 失败时 upload() 返回的是「可能为空的数组」，不是 Error（实测结论）
    if (output instanceof Error) {
      const message = capturedError ?? output.message
      log.warn('上传失败（返回值是 Error）', { seq, err: message })
      return { ok: false, data: null, error: message, kind: 'picgo' }
    }

    const list = Array.isArray(output) ? (output as IImgInfo[]) : []
    const first = list.find((item) => typeof item?.imgUrl === 'string' && item.imgUrl !== '')

    if (!first) {
      const message = capturedError ?? '上传失败：图床未返回图片地址'
      log.warn('上传失败（无有效输出）', { seq, captured: capturedError, count: list.length })
      return { ok: false, data: null, error: message, kind: 'picgo' }
    }

    const data = toUploadData(first, seq, uploaderType)
    log.info('上传成功', {
      seq,
      url: data.URL,
      fileName: data.FileName,
      width: data.Width,
      height: data.Height,
      size: data.Size
    })
    return { ok: true, data, error: '', kind: 'picgo' }
  } finally {
    gate.release()
  }
}
