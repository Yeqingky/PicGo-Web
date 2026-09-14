/**
 * 探测 picgo-core 是否带本项目所需的**补丁**。
 *
 * ## 为什么需要运行时探测
 *
 * 侧车依赖 `@yeqingky/picgo-core`（PicGo-Core 的 `PicGo-Web` fork），
 * 它新增了：
 *   · `UploadOptions.uploader`      —— 按次指定图床（并发投递的基础）
 *   · `UploadOptions.contextData`   —— 全局事件归属到具体任务
 *
 * 若依赖被换成**上游原版** `picgo`（例如手工改 package.json、
 * 或用了别人构建的镜像），服务**照样起得来、上传照样"成功"** ——
 * 但并发时两个批次会互相覆盖 `picBed.uploader`，**静默传错图床**。
 * 这类问题在运行时极难定位（不报错、链接可用，只是落到了错误的桶）。
 *
 * 因此：
 *   · **构建期**：生产 Dockerfile 已断言（缺 `contextData` 即构建失败）
 *   · **运行时**：这里再探测一次，通过 `/healthz` 暴露给 Go 侧，
 *     由 Go 决定是否**强制降级为单并发**（安全但慢）
 *
 * 双层保险的原因：构建期断言只覆盖「用我们的 Dockerfile」这条路径。
 */
import { createRequire } from 'node:module'
import { readFileSync } from 'node:fs'

const require = createRequire(import.meta.url)

/** 补丁探测结果。 */
export interface PatchStatus {
  /** 是否支持 `UploadOptions.uploader`（按次指定图床）。 */
  UploaderTarget: boolean
  /** 是否支持 `UploadOptions.contextData`（事件归属）。 */
  ContextData: boolean
  /** 探测到的包名与版本，便于排查「装错包」。 */
  PackageName: string
  PackageVersion: string
  /** 探测失败时的原因（不影响启动，只影响并发能力）。 */
  Error?: string
}

/**
 * 探测标志串。
 *
 * 用**源码文本匹配**而不是尝试调用：调用需要真实上传才能验证，
 * 成本高且可能产生副作用；而这两个标志串在打包产物里必然出现
 * （`contextData` 是拼进对象字面量的属性名，不会被压缩掉）。
 */
const MARKERS: ReadonlyArray<{ key: keyof Pick<PatchStatus, 'UploaderTarget' | 'ContextData'>, pattern: string }> = [
  // createContext 会把 options.contextData 挂到 ctx 上，属性名保留
  { key: 'ContextData', pattern: 'contextData' },
  // 补丁的前置校验会抛出含 Uploader type 的错误信息
  { key: 'UploaderTarget', pattern: 'Uploader type' }
]

/**
 * 读取 picgo-core 的打包产物源码。
 *
 * 优先 `require.resolve` 拿到入口文件（`dist/index.cjs.js`），
 * 失败时退化为直接读 `package.json` 所在目录的 `dist/index.cjs.js`。
 */
function readCoreSource(): { source: string, pkgName: string, pkgVersion: string } {
  let entry: string
  let pkgPath: string
  try {
    entry = require.resolve('@yeqingky/picgo-core')
    pkgPath = require.resolve('@yeqingky/picgo-core/package.json')
  } catch (err) {
    // 退化：也许装的是上游原版 picgo
    entry = require.resolve('picgo')
    pkgPath = require.resolve('picgo/package.json')
    void err
  }

  const pkg = JSON.parse(readFileSync(pkgPath, 'utf8')) as { name?: string, version?: string }
  return {
    source: readFileSync(entry, 'utf8'),
    pkgName: pkg.name ?? '(unknown)',
    pkgVersion: pkg.version ?? '(unknown)'
  }
}

/** 探测一次（结果由调用方缓存）。 */
export function detectPatches(): PatchStatus {
  const base: PatchStatus = {
    UploaderTarget: false,
    ContextData: false,
    PackageName: '(unknown)',
    PackageVersion: '(unknown)'
  }

  try {
    const { source, pkgName, pkgVersion } = readCoreSource()
    base.PackageName = pkgName
    base.PackageVersion = pkgVersion

    for (const { key, pattern } of MARKERS) {
      base[key] = source.includes(pattern)
    }

    if (!base.UploaderTarget || !base.ContextData) {
      base.Error =
        `picgo-core (${pkgName}@${pkgVersion}) 缺少本项目所需补丁：` +
        [
          base.UploaderTarget ? '' : 'UploadOptions.uploader',
          base.ContextData ? '' : 'UploadOptions.contextData'
        ].filter(Boolean).join(', ') +
        '。并发上传将不安全（Go 侧会强制降级为单并发）。' +
        '请确认依赖是 @yeqingky/picgo-core（而非上游 picgo）。'
    }
    return base
  } catch (err) {
    base.Error = `无法探测 picgo-core 补丁：${err instanceof Error ? err.message : String(err)}`
    return base
  }
}

/** 进程内缓存：探测结果不会在运行期变化。 */
let cached: PatchStatus | null = null

/** 取探测结果（首次调用时探测）。 */
export function patchStatus(): PatchStatus {
  if (!cached) cached = detectPatches()
  return cached
}

/** 是否**全部**补丁就位（Go 侧据此决定是否允许并发 > 1）。 */
export function patchesComplete(): boolean {
  const s = patchStatus()
  return s.UploaderTarget && s.ContextData
}
