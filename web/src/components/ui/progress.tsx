import * as ProgressPrimitive from '@radix-ui/react-progress'
import type { ComponentProps } from 'react'

import { cn } from '@/lib/utils'

/**
 * 进度条。
 *
 * `variant` 与语义色对应：进行中用 `brand`（强调色），失败用 `destructive`。
 */
export function Progress({
  className,
  value,
  variant = 'brand',
  ...props
}: Omit<ComponentProps<typeof ProgressPrimitive.Root>, 'value'> & {
  /** 0..100；省略或 null 时按 0 处理 */
  value?: number | null
  variant?: 'brand' | 'success' | 'warning' | 'destructive'
}) {
  const clamped = Math.max(0, Math.min(100, value ?? 0))
  return (
    <ProgressPrimitive.Root
      value={clamped}
      className={cn('relative h-2 w-full overflow-hidden rounded-full bg-muted', className)}
      {...props}
    >
      <ProgressPrimitive.Indicator
        className={cn(
          'h-full w-full flex-1 transition-transform duration-200',
          variant === 'brand' && 'bg-brand',
          variant === 'success' && 'bg-success',
          variant === 'warning' && 'bg-warning',
          variant === 'destructive' && 'bg-destructive',
        )}
        style={{ transform: `translateX(-${100 - clamped}%)` }}
      />
    </ProgressPrimitive.Root>
  )
}
