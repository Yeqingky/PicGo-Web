import { ChevronDown, ChevronRight } from 'lucide-react'
import { useState } from 'react'

import { t } from '@/i18n'
import { cn } from '@/lib/utils'

/**
 * JSON 展示（用于任务 `Result` / 日志 `Detail` / 插件输出等）。
 *
 * 要点（DESIGN.md §5.5 / §5.6）：
 *  - **高亮格式化**：`<pre>` + 等宽字体 + 横向滚动，不做语法着色（避免引入依赖）
 *  - 可折叠：长 JSON 默认折叠，点开看全文
 *  - 空值 / null 明确显示，不留空白让人以为是渲染失败
 */
export interface JsonViewProps {
  value: unknown
  /** 默认展开 */
  defaultOpen?: boolean
  /** 折叠时展示的行数阈值 */
  collapseLines?: number
  className?: string
  /** 无内容时的占位文案 */
  emptyText?: string
}

function stringify(value: unknown): string {
  if (value === null || value === undefined) return ''
  if (typeof value === 'string') {
    // 某些字段（如 Error）本身就是字符串；尝试解析成对象以便格式化
    const trimmed = value.trim()
    if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
      try {
        return JSON.stringify(JSON.parse(trimmed), null, 2)
      } catch {
        return value
      }
    }
    return value
  }
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

export function JsonView({
  value,
  defaultOpen = false,
  collapseLines = 12,
  className,
  emptyText = '—',
}: JsonViewProps) {
  const [open, setOpen] = useState(defaultOpen)

  const text = stringify(value)
  if (!text) {
    return <p className={cn('text-sm text-muted-foreground', className)}>{emptyText}</p>
  }

  const lines = text.split('\n')
  const collapsible = lines.length > collapseLines
  const shown = collapsible && !open ? lines.slice(0, collapseLines).join('\n') + '\n…' : text

  return (
    <div className={cn('space-y-1.5', className)}>
      <pre className="max-h-96 overflow-auto rounded-md border border-border bg-muted/40 p-3 font-mono text-xs leading-relaxed text-foreground scrollbar-thin">
        {shown}
      </pre>

      {collapsible ? (
        <button
          type="button"
          onClick={() => setOpen((prev) => !prev)}
          className="inline-flex items-center gap-1 text-xs text-brand hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1"
        >
          {open ? <ChevronDown className="size-3" aria-hidden /> : <ChevronRight className="size-3" aria-hidden />}
          {open ? t('JSON_COLLAPSE') : t('JSON_EXPAND_ALL', { lines: lines.length })}
        </button>
      ) : null}
    </div>
  )
}
