import { ApiCode } from '@/types/api'

const BYTE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'] as const

/**
 * 格式化字节数为人类可读字符串。
 *
 * 例：`1536` → `"1.5 KB"`；`0` → `"0 B"`。
 */
export function formatBytes(bytes: number, fractionDigits = 1): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'

  const exponent = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), BYTE_UNITS.length - 1)
  const value = bytes / 1024 ** exponent
  const digits = exponent === 0 ? 0 : fractionDigits
  return `${value.toFixed(digits)} ${BYTE_UNITS[exponent]}`
}

/**
 * 格式化配额展示。
 *
 * `CapacityBytes = 0` 表示**不限额**（D20），此时返回 `"不限额"`。
 */
export function formatQuota(usedBytes: number, capacityBytes: number): string {
  if (capacityBytes <= 0) return '不限额'
  return `${formatBytes(usedBytes)} / ${formatBytes(capacityBytes)}`
}

/** 配额使用比例（0~1）；不限额时返回 0。 */
export function quotaRatio(usedBytes: number, capacityBytes: number): number {
  if (capacityBytes <= 0) return 0
  return Math.min(usedBytes / capacityBytes, 1)
}

/**
 * 格式化 Unix 秒时间为本地时间字符串。
 *
 * 后端所有时间字段都是 **Unix 秒**（DATA-MODEL §0.2）。
 * 注意：`0` 视为「从未发生」，返回 `"—"`。
 */
export function formatDateTime(unixSeconds: number): string {
  if (!unixSeconds) return '—'
  const d = new Date(unixSeconds * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  )
}

/** 只到日期。 */
export function formatDate(unixSeconds: number): string {
  if (!unixSeconds) return '—'
  const d = new Date(unixSeconds * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

/** 相对时间（如「3 分钟前」）。用于列表的次要时间列。 */
export function formatRelativeTime(unixSeconds: number): string {
  if (!unixSeconds) return '—'

  const diffSeconds = Math.floor(Date.now() / 1000) - unixSeconds
  if (diffSeconds < 0) return formatDateTime(unixSeconds)
  if (diffSeconds < 60) return '刚刚'

  const minutes = Math.floor(diffSeconds / 60)
  if (minutes < 60) return `${minutes} 分钟前`

  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`

  const days = Math.floor(hours / 24)
  if (days < 30) return `${days} 天前`

  return formatDate(unixSeconds)
}

/** 秒数 → 时长文案（用于「X 分钟后过期」之类）。 */
export function formatDuration(seconds: number): string {
  if (seconds <= 0) return '—'
  if (seconds < 60) return `${seconds} 秒`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时`
  const days = Math.floor(hours / 24)
  return `${days} 天`
}

/** 尺寸（宽×高）。 */
export function formatDimensions(width: number, height: number): string {
  if (!width || !height) return '—'
  return `${width} × ${height}`
}

/** 文件名截断（保留扩展名）。 */
export function truncateFileName(name: string, maxLength = 32): string {
  if (name.length <= maxLength) return name
  const dot = name.lastIndexOf('.')
  if (dot <= 0 || dot >= name.length - 1) return `${name.slice(0, maxLength - 1)}…`
  const ext = name.slice(dot)
  const baseLength = maxLength - ext.length - 1
  if (baseLength <= 0) return `${name.slice(0, maxLength - 1)}…`
  return `${name.slice(0, baseLength)}…${ext}`
}

/**
 * 把后端的错误码翻成一句给用户看的话。
 *
 * 后端 `Message` 已是中文，通常直接用；这里只在少数场景下做补充。
 */
export function describeApiCode(code: number): string {
  switch (code) {
    case ApiCode.InvalidParam:
      return '请求参数有误'
    case ApiCode.BadCredentials:
      return '邮箱或密码错误'
    case ApiCode.Unauthorized:
      return '登录已失效，请重新登录'
    case ApiCode.TokenExpired:
      return '登录已过期，请重新登录'
    case ApiCode.AccountDisabled:
      return '账号已被禁用'
    case ApiCode.Forbidden:
      return '权限不足'
    case ApiCode.QuotaExceeded:
      return '存储配额不足'
    case ApiCode.NotFound:
      return '资源不存在'
    case ApiCode.Conflict:
      return '操作冲突'
    case ApiCode.TooManyRequests:
      return '操作过于频繁，请稍后再试'
    case ApiCode.AgentUnavailable:
      return '内核（picgo-agent）不可用'
    default:
      return '服务器开小差了，请稍后再试'
  }
}
