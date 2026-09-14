/**
 * 魔法路径 / 魔法文件名的模板引擎（D42–D44、D70）。
 *
 * ## 变量（常用集，D70）
 *
 * | 变量 | 含义 |
 * |---|---|
 * | `{Y}` `{m}` `{d}` | 年(4) / 月(2) / 日(2) |
 * | `{H}` `{i}` `{s}` | 时 / 分 / 秒（各 2 位） |
 * | `{timestamp}` | Unix 秒 |
 * | `{filename}` | 原始文件名（不含扩展名） |
 * | `{md5}` / `{md5-8}` | 文件 MD5 全量 / 前 8 位 |
 * | `{sha256-8}` | SHA-256 前 8 位（仅元数据参考；D66 已取消去重，它不是去重键） |
 * | `{uid}` | 上传者用户 ID |
 * | `{uniqid}` | 随机短串 |
 * | `{extname}` | 扩展名（含点，如 `.png`） |
 *
 * ## 规则
 *
 * - 魔法文件名与魔法路径**共用同一套变量**
 * - 模板为空 → 回退默认（文件名 = `{uniqid}{extname}`，路径 = 无）
 * - **未知变量原样保留**并记 warning，不报错、不阻断上传
 * - 非法字符（`\ : * ? " < > |` 与控制字符）在应用模板后**按路径段**替换为 `_`
 *   （`/` 是段分隔符，必须保留，否则 `img/{Y}/{m}` 这种模板会失效）
 * - `{md5*}` / `{sha256-8}` 需要读文件内容，因此**惰性计算**：
 *   只有模板里真的用到了才去读，避免无谓 I/O
 */

import fs from 'node:fs'
import crypto from 'node:crypto'
import path from 'node:path'

/** 路径段内的非法字符（`/` 单独处理，它是分隔符）。 */
const ILLEGAL_IN_SEGMENT = /[\\:*?"<>|\u0000-\u001f]/g

export interface TemplateVars {
  /** 原始文件名（含扩展名）。 */
  fileName: string
  /** 扩展名（含点，如 `.png`）；未知时为空串。 */
  extname: string
  /** 上传者 UID（可选）。 */
  userUID?: string
  /** 文件绝对路径（用于惰性计算 md5 / sha256）。 */
  filePath?: string
  /** 时间戳（毫秒）；默认 Date.now()。测试可注入。 */
  now?: number
}

export interface ExpandResult {
  /** 展开后的字符串（未做段内清洗）。 */
  value: string
  /** 模板中出现但未被识别的变量名（供上层记 warning）。 */
  unknownVars: string[]
}

/** 生成短随机串（8 位，base36）。 */
export function uniqid(): string {
  return crypto.randomBytes(6).toString('hex').slice(0, 8)
}

function pad2(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

function baseNameWithoutExt(fileName: string): string {
  const ext = path.extname(fileName)
  return ext === '' ? fileName : fileName.slice(0, -ext.length)
}

/** 惰性哈希：只算被模板用到的。 */
class LazyHashes {
  private md5?: string
  private sha256?: string

  constructor(private readonly filePath: string | undefined) {}

  private compute(algo: 'md5' | 'sha256'): string | undefined {
    if (this.filePath === undefined) return undefined
    try {
      // 图片通常几 MB，一次性读入可接受；避免流式带来的复杂度
      const buf = fs.readFileSync(this.filePath)
      return crypto.createHash(algo).update(buf).digest('hex')
    } catch {
      return undefined
    }
  }

  getMd5(): string | undefined {
    if (this.md5 === undefined) this.md5 = this.compute('md5')
    return this.md5
  }

  getSha256(): string | undefined {
    if (this.sha256 === undefined) this.sha256 = this.compute('sha256')
    return this.sha256
  }
}

/**
 * 展开模板。
 *
 * **不做**路径清洗（由 {@link sanitizeRelativePath} 负责），因为未知变量的
 * 原样保留需要与清洗分开处理。
 */
export function expandTemplate(template: string, vars: TemplateVars): ExpandResult {
  if (template === '') return { value: '', unknownVars: [] }

  const nowMs = vars.now ?? Date.now()
  const date = new Date(nowMs)
  const hashes = new LazyHashes(vars.filePath)
  const unknownVars: string[] = []

  const value = template.replace(/\{([A-Za-z0-9_-]+)\}/g, (whole, rawName: string): string => {
    switch (rawName) {
      case 'Y':
        return String(date.getFullYear())
      case 'm':
        return pad2(date.getMonth() + 1)
      case 'd':
        return pad2(date.getDate())
      case 'H':
        return pad2(date.getHours())
      case 'i':
        return pad2(date.getMinutes())
      case 's':
        return pad2(date.getSeconds())
      case 'timestamp':
        return String(Math.floor(nowMs / 1000))
      case 'filename':
        return baseNameWithoutExt(vars.fileName)
      case 'md5': {
        const v = hashes.getMd5()
        return v ?? ''
      }
      case 'md5-8': {
        const v = hashes.getMd5()
        return v === undefined ? '' : v.slice(0, 8)
      }
      case 'sha256-8': {
        const v = hashes.getSha256()
        return v === undefined ? '' : v.slice(0, 8)
      }
      case 'uid':
        return vars.userUID ?? ''
      case 'uniqid':
        return uniqid()
      case 'extname':
        return vars.extname
      default:
        // 未知变量：原样保留 + 记录（D70：不报错、不阻断）
        unknownVars.push(rawName)
        return whole
    }
  })

  return { value, unknownVars }
}

/**
 * 清洗相对路径：按 `/` 分段，逐段替换非法字符，丢弃空段与「纯点段」。
 *
 * 这样 `img/{Y}/{m}` → `img/2026/02` 保留分隔符，
 * 而 `a:b.png` → `a_b.png` 去掉冒号（Windows/对象存储都不接受）。
 *
 * 「纯点段」（`.` / `..` / `...` / `....`）一律丢弃：
 * 它们没有作为图片文件名的正当用途，且是路径归一化最容易出问题的形态。
 */
export function sanitizeRelativePath(input: string): string {
  const segments = input
    .split('/')
    .map((seg) => seg.replace(ILLEGAL_IN_SEGMENT, '_').trim())
    .filter((seg) => seg !== '' && !/^\.+$/.test(seg))

  const joined = segments.join('/')
  // 去掉可能残留的开头斜杠，保证是「相对」路径
  return joined.replace(/^\/+/, '')
}

export interface BuildNameInput {
  /** 原始文件名（含扩展名）。 */
  fileName: string
  /**
   * 扩展名覆盖（含点）。
   *
   * picgo 的 transformer 会在 `ctx.output[i].extname` 里给出权威扩展名，
   * 而 `fileName` 在某些驱动/输入下可能没有扩展名（例如通过 buffer 传入）。
   * 此时以本字段为准；未提供时才从 `fileName` 推导。
   */
  extname?: string | undefined
  /** 魔法路径模板（可为空）。 */
  pathTemplate?: string | undefined
  /** 魔法文件名模板（可为空）。 */
  fileTemplate?: string | undefined
  /** 上传者 UID。 */
  userUID?: string | undefined
  /** 文件绝对路径（惰性哈希用）。 */
  filePath?: string | undefined
  /**
   * 驱动是否支持自定义路径。
   *
   * 为 false 时**路径降级为文件名前缀**（D44：不支持的驱动只把路径当名字的一部分）。
   */
  supportsPathTemplate?: boolean | undefined
  /** 测试注入时间。 */
  now?: number | undefined
}

export interface BuildNameResult {
  /** 最终要写入 `ctx.output[i].fileName` 的值（可能含 `/` 以表达子目录）。 */
  fileName: string
  /** 模板中未识别的变量名（供上层记 warning）。 */
  unknownVars: string[]
  /** 路径是否被降级为文件名前缀。 */
  pathDowngraded: boolean
}

/**
 * 由模板构造最终文件名。
 *
 * 这是 D44 的核心：**把路径塞进 fileName**。
 * 多数驱动的实现是 `config.path + fileName`，因此 fileName 里的 `/`
 * 会被驱动原样当作子目录，从而在没有「按次指定路径」API 的前提下实现魔法路径。
 */
export function buildFileName(input: BuildNameInput): BuildNameResult {
  // 优先用调用方给的 extname（picgo 的权威值），否则从文件名推导
  const providedExt = (input.extname ?? '').trim()
  const extname = providedExt !== '' ? providedExt : path.extname(input.fileName)
  const supportsPath = input.supportsPathTemplate !== false

  const pathTemplate = (input.pathTemplate ?? '').trim()
  const fileTemplate = (input.fileTemplate ?? '').trim()

  const unknownVars: string[] = []

  // ---- 文件名部分 ----
  const fileTpl = fileTemplate === '' ? '{uniqid}{extname}' : fileTemplate
  const fileExpanded = expandTemplate(fileTpl, {
    fileName: input.fileName,
    extname,
    ...(input.userUID !== undefined ? { userUID: input.userUID } : {}),
    ...(input.filePath !== undefined ? { filePath: input.filePath } : {}),
    ...(input.now !== undefined ? { now: input.now } : {})
  })
  unknownVars.push(...fileExpanded.unknownVars)
  let namePart = fileExpanded.value

  // 模板没带扩展名时补上（否则图床上的文件没有后缀，很多场景会出问题）
  if (path.extname(namePart) === '' && extname !== '') {
    namePart = `${namePart}${extname}`
  }

  // ---- 路径部分 ----
  let pathPart = ''
  let pathDowngraded = false
  if (pathTemplate !== '') {
    const pathExpanded = expandTemplate(pathTemplate, {
      fileName: input.fileName,
      extname,
      ...(input.userUID !== undefined ? { userUID: input.userUID } : {}),
      ...(input.filePath !== undefined ? { filePath: input.filePath } : {}),
      ...(input.now !== undefined ? { now: input.now } : {})
    })
    unknownVars.push(...pathExpanded.unknownVars)
    pathPart = pathExpanded.value

    if (!supportsPath) {
      // 驱动不支持路径（或已按 posix 拼好但可能被服务端忽略）→ 降级为文件名前缀
      pathDowngraded = true
      const flat = sanitizeRelativePath(pathPart).replace(/\//g, '_')
      if (flat !== '') {
        namePart = `${flat}_${namePart}`
      }
      pathPart = ''
    }
  }

  const combined = pathPart === '' ? sanitizeRelativePath(namePart) : sanitizeRelativePath(`${pathPart}/${namePart}`)

  // 极端兜底：清洗后为空（例如模板全是非法字符）时给一个随机名
  if (combined === '') {
    return {
      fileName: `${uniqid()}${extname}`,
      unknownVars,
      pathDowngraded
    }
  }

  return { fileName: combined, unknownVars, pathDowngraded }
}
