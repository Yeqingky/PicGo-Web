import type { LucideIcon } from 'lucide-react'
import type { ReactNode } from 'react'

import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'

/**
 * 概览页的统计卡片（DESIGN.md §4.2）。
 *
 * 参考 lsky-pro 仪表盘的「图片数量 / 可用储存 / 使用储存 / 总储存」四卡，
 * 与 skyImage 的 `StatCard`（标题 + 图标 + 大数字）合并为同一个组件：
 *  - `value` 是**已经格式化好的字符串**（数字靠 `tabular-nums` 对齐）
 *  - `hint` 是可选的次要行（占比、拆分说明），不传则不占位
 */

/** 图标底色/前景组合；与语义色对应，不引入新的 token。 */
const TONES = {
  brand: 'bg-brand/10 text-brand',
  success: 'bg-success/10 text-success',
  warning: 'bg-warning/10 text-warning',
  destructive: 'bg-destructive/10 text-destructive',
  info: 'bg-info/10 text-info',
  muted: 'bg-muted text-muted-foreground',
} as const

export type StatCardTone = keyof typeof TONES

export interface StatCardProps {
  title: string
  value: ReactNode
  /** 次要说明行（占比、拆分等） */
  hint?: ReactNode
  icon: LucideIcon
  tone?: StatCardTone
  className?: string
}

export function StatCard({
  title,
  value,
  hint,
  icon: Icon,
  tone = 'muted',
  className,
}: StatCardProps) {
  return (
    <Card className={className}>
      <CardHeader className="flex flex-row items-center justify-between gap-2 space-y-0 pb-3">
        <CardTitle className="text-sm font-medium text-muted-foreground">{title}</CardTitle>
        <span className={cn('flex size-8 items-center justify-center rounded-md', TONES[tone])}>
          <Icon className="size-4" aria-hidden />
        </span>
      </CardHeader>
      <CardContent className="space-y-1">
        <p className="truncate text-2xl font-semibold tracking-tight tabular-nums text-foreground">
          {value}
        </p>
        {hint ? <p className="text-xs leading-relaxed text-muted-foreground">{hint}</p> : null}
      </CardContent>
    </Card>
  )
}
