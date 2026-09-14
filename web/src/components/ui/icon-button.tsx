import type { ComponentProps } from 'react'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

/**
 * 图标按钮。
 *
 * ⚠️ `label` **必填**：图标按钮必须有可读名称（DESIGN.md §12 a11y）。
 * 它同时作为 `aria-label` 与 `title`（悬浮提示）。
 */
export interface IconButtonProps extends Omit<ComponentProps<'button'>, 'aria-label' | 'title'> {
  /** 无障碍名称（必填） */
  label: string
  /** 是否显示悬浮提示（默认显示） */
  tooltip?: boolean
}

export function IconButton({ label, tooltip = true, className, ...props }: IconButtonProps) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      aria-label={label}
      title={tooltip ? label : undefined}
      className={cn('text-muted-foreground hover:text-foreground', className)}
      {...props}
    />
  )
}
