import { Construction, type LucideIcon } from 'lucide-react'

import { EmptyState } from '@/components/ui/empty-state'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

/**
 * 未实现页面的统一占位（W7 只交付「基座」，业务页面在 W8 补齐）。
 *
 * 刻意做成显眼但友好的形态：用户知道这不是坏掉了，而是还没做；
 * 开发者也一眼能看出还剩哪些页面。
 */
export interface PlaceholderPageProps {
  /** 页面标题（显示在卡片上方，也便于在浏览器里确认路由正确） */
  title: string
  /** 覆盖默认说明（例如说明这条路有特殊的前置依赖） */
  description?: string
  /** 自定义图标 */
  icon?: LucideIcon
  className?: string
}

export function PlaceholderPage({
  title,
  description,
  icon = Construction,
  className,
}: PlaceholderPageProps) {
  return (
    <div className={cn('space-y-4', className)}>
      <h1 className="text-2xl font-semibold tracking-tight text-foreground">{title}</h1>
      <EmptyState
        icon={icon}
        title={t('PLACEHOLDER_TODO_TITLE')}
        description={description ?? t('PLACEHOLDER_TODO_DESC')}
      />
    </div>
  )
}
