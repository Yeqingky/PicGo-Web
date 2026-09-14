import type { ComponentProps } from 'react'

import { cn } from '@/lib/utils'

/** 骨架屏（DESIGN.md §9.3：首屏用 Skeleton，表格用行内骨架）。 */
export function Skeleton({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('animate-pulse rounded-md bg-muted', className)} {...props} />
}
