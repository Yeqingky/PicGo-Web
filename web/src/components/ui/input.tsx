import type { ComponentProps } from 'react'

import { cn } from '@/lib/utils'

/**
 * 单行输入（DESIGN.md §2.2：输入框字号 16px，避免移动端聚焦缩放）。
 *
 * 错误态：传 `aria-invalid` 即可变红（配合 Form 使用）。
 */
export function Input({ className, type, ...props }: ComponentProps<'input'>) {
  return (
    <input
      type={type}
      className={cn(
        'flex h-10 w-full rounded-lg border border-input bg-background px-3 py-2 text-base',
        'placeholder:text-muted-foreground',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:ring-offset-background',
        'disabled:cursor-not-allowed disabled:opacity-50',
        'aria-[invalid=true]:border-destructive aria-[invalid=true]:focus-visible:ring-destructive',
        'transition-colors duration-150',
        className,
      )}
      {...props}
    />
  )
}
