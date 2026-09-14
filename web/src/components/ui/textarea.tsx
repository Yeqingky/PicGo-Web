import type { ComponentProps } from 'react'

import { cn } from '@/lib/utils'

/**
 * 多行输入。默认 `resize-y`（可拖拽调高，DESIGN.md §7 的 textarea 要求）。
 */
export function Textarea({ className, ...props }: ComponentProps<'textarea'>) {
  return (
    <textarea
      className={cn(
        'flex min-h-20 w-full resize-y rounded-lg border border-input bg-background px-3 py-2 text-base',
        'placeholder:text-muted-foreground',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:ring-offset-background',
        'disabled:cursor-not-allowed disabled:opacity-50',
        'aria-[invalid=true]:border-destructive',
        'transition-colors duration-150',
        className,
      )}
      {...props}
    />
  )
}
