/**
 * 魔法路径 / 魔法文件名的落地钩子（D44）。
 *
 * ## 为什么必须走 `beforeUploadPlugins`
 *
 * `ctx.output` 是在 **transform 阶段**（`transformer.handle(ctx)`）才被填充的
 * （`path` transformer 把文件路径读成 buffer + fileName + 尺寸）。
 * 因此改名只能发生在 transform **之后**、upload **之前** —— 那正是
 * `beforeUploadPlugins` 的位置。
 *
 * 参考实现：PicGo GUI 的 `src/main/apis/app/uploader/index.ts` 里的 `renameFn`。
 *
 * ## 为什么只注册一个钩子
 *
 * 模板是**按次上传**变化的（每个存储配置有各自的模板），
 * 但钩子是**进程级**注册的。因此：
 *
 * - 启动时注册**一个**固定名字的钩子（`picgo-web-magic-path`）
 * - 每次上传时通过 `UploadOptions.contextData` 把本次的模板与变量带进来
 * - 钩子读 `ctx.contextData.magicPath` 决定怎么改名
 *
 * 这样既满足「必须在 upload() 之前注册」，又天然支持并发（每个 ctx 有独立的 contextData）。
 */

import type { IPicGo, IImgInfo } from 'picgo'
import type { Logger } from '../logger.js'
import { buildFileName } from './template.js'

/** 注册的钩子名字（固定，便于排查）。 */
export const MAGIC_PATH_HOOK = 'picgo-web-magic-path'

/**
 * 每次上传随 `contextData` 携带的魔法命名参数。
 *
 * ⚠️ 这些是我们自己的字段，用 camelCase 还是 PascalCase 都行（不对外暴露），
 * 但为了与 `contextData` 的使用方（upload.ts）保持一致，这里用 camelCase，
 * 因为 `contextData` 会与 picgo 内部（camelCase 世界）打交道。
 */
export interface MagicPathContext {
  /** 魔法路径模板（D43）。 */
  pathTemplate?: string
  /** 魔法文件名模板（D43）。 */
  fileTemplate?: string
  /** 上传者 UID（供 `{uid}` 变量）。 */
  userUID?: string
  /** 驱动是否支持自定义路径（来自 Capabilities）。 */
  supportsPathTemplate?: boolean
}

/** 从 ctx 里安全取出我们的 magicPath 参数。 */
export function readMagicPathContext(ctx: IPicGo): MagicPathContext | undefined {
  const data = ctx.contextData
  if (!data || typeof data !== 'object') return undefined
  const magic = (data as Record<string, unknown>).magicPath
  if (!magic || typeof magic !== 'object') return undefined
  return magic as MagicPathContext
}

/**
 * 应用魔法命名到 `ctx.output`。
 *
 * 导出为独立函数是为了**可单测**（不必真的跑一遍完整生命周期）。
 */
export function applyMagicPath(
  output: IImgInfo[],
  magic: MagicPathContext,
  log: Logger,
  now?: number
): void {
  const pathTemplate = magic.pathTemplate ?? ''
  const fileTemplate = magic.fileTemplate ?? ''

  // 两个模板都为空 → 不改名（保持 picgo 的默认行为，避免无谓改动）
  if (pathTemplate === '' && fileTemplate === '') return

  for (const item of output) {
    const originalFileName = typeof item.fileName === 'string' && item.fileName !== ''
      ? item.fileName
      : 'image'

    const result = buildFileName({
      fileName: originalFileName,
      // picgo 的 transformer 会给出权威扩展名；fileName 可能没有（如 buffer 输入）
      extname: typeof item.extname === 'string' ? item.extname : undefined,
      pathTemplate,
      fileTemplate,
      userUID: magic.userUID,
      filePath: typeof item.filePath === 'string' ? item.filePath : undefined,
      supportsPathTemplate: magic.supportsPathTemplate,
      ...(now !== undefined ? { now } : {})
    })

    if (result.unknownVars.length > 0) {
      // D70：未知变量原样保留，只记 warning，不阻断上传
      log.warn('魔法命名模板含未知变量（已原样保留）', {
        template: pathTemplate !== '' ? pathTemplate : fileTemplate,
        unknown: [...new Set(result.unknownVars)]
      })
    }

    if (result.pathDowngraded) {
      log.info('驱动不支持自定义路径，已降级为文件名前缀', {
        original: originalFileName,
        final: result.fileName
      })
    }

    item.fileName = result.fileName
  }
}

/**
 * 注册魔法命名钩子。
 *
 * ⚠️ **必须在任何 `upload()` 之前调用**（picgo 的钩子注册是即时生效的，
 * 上传过程中注册不会影响进行中的上传）。
 */
export function registerMagicPathHook(picgo: IPicGo, log: Logger): void {
  // 先清掉可能存在的同名钩子（热重载/测试场景，避免重复注册）
  try {
    picgo.helper.beforeUploadPlugins.unregister(MAGIC_PATH_HOOK)
  } catch {
    // 不存在时会抛错，忽略
  }

  picgo.helper.beforeUploadPlugins.register(MAGIC_PATH_HOOK, {
    handle: async (ctx: IPicGo): Promise<void> => {
      const magic = readMagicPathContext(ctx)
      if (!magic) return
      applyMagicPath(ctx.output ?? [], magic, log)
    }
  })

  log.debug('已注册魔法命名钩子', { hook: MAGIC_PATH_HOOK })
}
