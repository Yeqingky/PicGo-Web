import { useNavigate } from 'react-router'
import { useEffect, useMemo, useState } from 'react'

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { navGroupTitle, navGroups, navItemLabel } from '@/lib/navigation'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/store/auth-store'

/**
 * 命令面板（DESIGN.md §9.1，`⌘/Ctrl + K`）。
 *
 * 范围（按需扩展，**不做过度设计**）：
 *  - **跳页**：侧栏导航项（按权限过滤）
 *  - 上下键选择、`Enter` 执行、`Esc` 关闭
 *
 * ⚠️ 「搜图 / 执行操作」暂不包含：它们需要额外的查询接口与确认流程，
 *    先把「跳页」这条最高频路径做扎实（DESIGN §9.1 也只列了「跳页、搜图、执行操作」三类）。
 */
export interface CommandPaletteProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

interface Command {
  id: string
  label: string
  group: string
  to: string
}

export function CommandPalette({ open, onOpenChange }: CommandPaletteProps) {
  const navigate = useNavigate()
  const isAdmin = useAuthStore((state) => state.user?.Role === 'admin')

  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)

  const commands = useMemo<Command[]>(() => {
    const out: Command[] = []
    for (const group of navGroups) {
      const title = navGroupTitle(group) ?? ''
      for (const item of group.items) {
        if (item.adminOnly && !isAdmin) continue
        out.push({
          id: item.to,
          label: navItemLabel(item),
          group: title,
          to: item.to,
        })
      }
    }
    return out
  }, [isAdmin])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return commands
    return commands.filter(
      (command) =>
        command.label.toLowerCase().includes(q) ||
        command.to.toLowerCase().includes(q) ||
        command.group.toLowerCase().includes(q),
    )
  }, [commands, query])

  // 打开时重置
  useEffect(() => {
    if (open) {
      setQuery('')
      setActiveIndex(0)
    }
  }, [open])

  // 过滤结果变化后修正选中项，避免越界
  useEffect(() => {
    setActiveIndex((prev) => (prev >= filtered.length ? 0 : prev))
  }, [filtered.length])

  const run = (command: Command | undefined) => {
    if (!command) return
    onOpenChange(false)
    navigate(command.to)
  }

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      setActiveIndex((prev) => (filtered.length === 0 ? 0 : (prev + 1) % filtered.length))
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      setActiveIndex((prev) =>
        filtered.length === 0 ? 0 : (prev - 1 + filtered.length) % filtered.length,
      )
    } else if (event.key === 'Enter') {
      event.preventDefault()
      run(filtered[activeIndex])
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="top-[15%] max-w-lg translate-y-0 gap-0 p-0" aria-describedby={undefined}>
        <DialogTitle className="sr-only">{t('TOPBAR_COMMAND_PALETTE')}</DialogTitle>

        <div className="border-b border-border px-3 py-2">
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={onKeyDown}
            placeholder={t('COMMAND_PALETTE_PLACEHOLDER')}
            autoFocus
            className="border-0 px-0 shadow-none focus-visible:ring-0"
            aria-label={t('COMMAND_PALETTE_PLACEHOLDER')}
          />
        </div>

        <ul role="listbox" className="max-h-80 overflow-y-auto scrollbar-thin py-1">
          {filtered.length === 0 ? (
            <li className="px-3 py-6 text-center text-sm text-muted-foreground">
              {t('COMMAND_PALETTE_EMPTY')}
            </li>
          ) : (
            filtered.map((command, index) => (
              <li key={command.id}>
                <button
                  type="button"
                  role="option"
                  aria-selected={index === activeIndex}
                  onMouseEnter={() => setActiveIndex(index)}
                  onClick={() => run(command)}
                  className={cn(
                    'flex w-full items-center justify-between gap-3 px-3 py-2 text-left text-sm transition-colors',
                    index === activeIndex ? 'bg-accent text-foreground' : 'text-muted-foreground',
                  )}
                >
                  <span className="truncate">{command.label}</span>
                  <span className="shrink-0 font-mono text-[11px] text-muted-foreground">
                    {command.to}
                  </span>
                </button>
              </li>
            ))
          )}
        </ul>

        <div className="flex items-center justify-between border-t border-border px-3 py-2 text-[11px] text-muted-foreground">
          <span>{t('COMMAND_PALETTE_HINT_NAV')}</span>
          <span>{t('COMMAND_PALETTE_HINT_CLOSE')}</span>
        </div>
      </DialogContent>
    </Dialog>
  )
}
