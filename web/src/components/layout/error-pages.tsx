import { ShieldAlert } from 'lucide-react'
import { Link } from 'react-router'

import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/ui/empty-state'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

/** 403 页（非 admin 访问 `/admin/**`）。
 *
 * ⚠️ 刻意**不跳转**（DESIGN.md §8）：跳走会让用户困惑「为什么地址变了」，
 *    留在原地址渲染 403 更清楚。
 */
export function ForbiddenPage({ className }: { className?: string }) {
  return (
    <div className={cn('mx-auto max-w-2xl py-16', className)}>
      <EmptyState
        icon={ShieldAlert}
        title={t('FORBIDDEN_TITLE')}
        description={t('FORBIDDEN_DESC')}
        action={
          <Button asChild variant="outline">
            <Link to="/">{t('FORBIDDEN_BACK')}</Link>
          </Button>
        }
      />
    </div>
  )
}

/** 404 页。 */
export function NotFoundPage({ className }: { className?: string }) {
  return (
    <div className={cn('mx-auto max-w-2xl py-16', className)}>
      <EmptyState
        title={t('NOT_FOUND_TITLE')}
        description={t('NOT_FOUND_DESC')}
        action={
          <Button asChild variant="outline">
            <Link to="/">{t('NOT_FOUND_BACK')}</Link>
          </Button>
        }
      />
    </div>
  )
}
