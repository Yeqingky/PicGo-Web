import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

/**
 * 页面标题栏（DESIGN.md §2.2 / §4.1）。
 *
 * 统一「标题 + 描述 + 右侧操作」的排布，避免每个页面各写一套间距。
 */
export interface PageHeaderProps {
  title: string
  description?: string
  /** 右侧操作区（按钮组 / 视图切换等） */
  actions?: ReactNode
  /** 标题下方紧邻的一行（如 Tab 切换器） */
  children?: ReactNode
  className?: string
}

export function PageHeader({ title, description, actions, children, className }: PageHeaderProps) {
  return (
    <div className={cn('mb-5 space-y-3', className)}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">{title}</h1>
          {description ? (
            <p className="max-w-prose text-sm text-muted-foreground">{description}</p>
          ) : null}
        </div>
        {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </div>
      {children}
    </div>
  )
}
