/**
 * 错误信息提取。
 *
 * 为什么需要它：picgo 的驱动（以及底层 axios）**并不总是抛 `Error` 实例**。
 * 实测常见形态：
 *
 * - `Error` → 取 `.message`
 * - `{ message: '...' }` → 取 `.message`
 * - `{ statusCode: 422, body: {...} }`（tcyun / github 驱动的 throw 形态）
 * - `{ response: { data: {...}, status } }`（axios 原始错误）
 * - 纯字符串
 *
 * 直接用 `String(err)` 会得到 `"[object Object]"` —— 这正是 agent 曾经把
 * 「上传失败」的错误原因丢掉的原因。因此统一走这里提取。
 *
 * **安全**：结果会返回给 Go 并最终可能展示给用户，因此要**截断长度**，
 * 但不做内容改写（错误原因本身是有用的排障信息）。
 */

/** 单条错误信息的最大长度。 */
const MAX_LEN = 500

function truncate(text: string): string {
  const oneLine = text.replace(/\s+/g, ' ').trim()
  return oneLine.length > MAX_LEN ? `${oneLine.slice(0, MAX_LEN)}…` : oneLine
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : undefined
}

/**
 * 安全 JSON 序列化（处理循环引用）。
 *
 * 末层兜底**绝不能**回到 `String(value)` —— 那会再次得到 `"[object Object]"`，
 * 正是我们要消灭的东西。因此这里用 seen 集合替换循环节点，
 * 真序列化不了时退化为「类型 + 顶层键名」。
 */
function safeStringify(value: unknown): string {
  const seen = new WeakSet<object>()
  try {
    const json = JSON.stringify(value, (_key, val: unknown) => {
      if (typeof val === 'object' && val !== null) {
        if (seen.has(val)) return '[Circular]'
        seen.add(val)
      }
      return val
    })
    if (typeof json === 'string' && json !== '{}' && json !== '') return json
  } catch {
    // 落到下面
  }

  // 极致兜底：给出类型与顶层键名，至少能看出「是什么东西出错了」
  const rec = asRecord(value)
  if (rec) {
    const keys = Object.keys(rec)
    return keys.length > 0 ? `[object keys=${keys.join(',')}]` : '[object]'
  }
  return String(value)
}

function pickString(source: Record<string, unknown>, keys: readonly string[]): string {
  for (const key of keys) {
    const value = source[key]
    if (typeof value === 'string' && value.trim() !== '') return value
  }
  return ''
}

/**
 * 把任意异常/拒因归一化成**人可读**的错误信息。
 *
 * 优先顺序：常见 message 字段 → body/response.data → JSON → String()。
 */
export function describeError(error: unknown): string {
  if (error === undefined || error === null) return ''
  if (error instanceof Error) return truncate(error.message || error.name || 'Error')
  if (typeof error === 'string') return truncate(error)
  if (typeof error === 'number' || typeof error === 'boolean') return String(error)

  const rec = asRecord(error)
  if (!rec) return truncate(String(error))

  // 1) 最常见的 message 类字段
  const direct = pickString(rec, ['message', 'msg', 'error', 'errorMessage', 'detail', 'reason'])
  if (direct !== '') return truncate(direct)

  // 2) axios 形态：response.data 里通常有真正的后端报错
  const response = asRecord(rec.response)
  if (response) {
    const data = asRecord(response.data)
    const fromData = data ? pickString(data, ['message', 'msg', 'error', 'detail']) : ''
    if (fromData !== '') {
      const status = typeof response.status === 'number' ? `HTTP ${response.status}: ` : ''
      return truncate(`${status}${fromData}`)
    }
    const statusText = pickString(response, ['statusText'])
    if (statusText !== '') {
      const status = typeof response.status === 'number' ? `${response.status} ` : ''
      return truncate(`${status}${statusText}`)
    }
  }

  // 3) picgo 驱动常见形态：{ statusCode, body }
  const body = asRecord(rec.body)
  if (body) {
    const fromBody = pickString(body, ['message', 'msg', 'error', 'err', 'detail'])
    const statusCode = typeof rec.statusCode === 'number' ? `HTTP ${rec.statusCode}: ` : ''
    if (fromBody !== '') return truncate(`${statusCode}${fromBody}`)

    const json = safeStringify(body)
    if (json !== '{}') return truncate(`${statusCode}${json}`)
  }

  // 4) 兜底：安全序列化整个对象（比 "[object Object]" 有用得多，且不怕循环引用）
  return truncate(safeStringify(rec))
}
