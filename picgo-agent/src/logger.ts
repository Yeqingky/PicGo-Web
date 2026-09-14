/**
 * 极简 JSON 行日志。
 *
 * 与 Go 侧的 log/slog JSON 输出对齐，便于容器里统一采集。
 * 不引入额外依赖（pino/winston），因为 agent 的日志量很小且格式要求简单。
 */

export type LogLevel = 'debug' | 'info' | 'warn' | 'error'

const LEVEL_ORDER: Record<LogLevel, number> = { debug: 10, info: 20, warn: 30, error: 40 }

export interface Logger {
  debug: (msg: string, fields?: Record<string, unknown>) => void
  info: (msg: string, fields?: Record<string, unknown>) => void
  warn: (msg: string, fields?: Record<string, unknown>) => void
  error: (msg: string, fields?: Record<string, unknown>) => void
  child: (fields: Record<string, unknown>) => Logger
}

function serializeError(value: unknown): unknown {
  if (value instanceof Error) {
    return { name: value.name, message: value.message, stack: value.stack }
  }
  return value
}

/** 把 fields 里的 Error 展开成可序列化对象（JSON.stringify 默认会丢掉 message）。 */
function normalizeFields(fields: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(fields)) {
    out[key] = serializeError(value)
  }
  return out
}

export function createLogger(level: LogLevel, base: Record<string, unknown> = {}): Logger {
  const threshold = LEVEL_ORDER[level]

  const emit = (lvl: LogLevel, msg: string, fields?: Record<string, unknown>): void => {
    if (LEVEL_ORDER[lvl] < threshold) return
    const payload = {
      time: new Date().toISOString(),
      level: lvl.toUpperCase(),
      component: 'picgo-agent',
      msg,
      ...normalizeFields(base),
      ...(fields ? normalizeFields(fields) : {})
    }
    // 单行 JSON，写 stdout（error 也写 stdout，保持顺序；由容器决定分流）
    process.stdout.write(`${JSON.stringify(payload)}\n`)
  }

  return {
    debug: (msg, fields) => emit('debug', msg, fields),
    info: (msg, fields) => emit('info', msg, fields),
    warn: (msg, fields) => emit('warn', msg, fields),
    error: (msg, fields) => emit('error', msg, fields),
    child: (fields) => createLogger(level, { ...base, ...fields })
  }
}
