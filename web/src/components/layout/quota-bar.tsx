import { Progress } from '@/components/ui/progress'
import { t } from '@/i18n'
import { formatBytes, quotaRatio } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/store/auth-store'

/**
 * 配额条（DESIGN.md §4.1 底部）。
 *
 * `CapacityBytes = 0` 表示**不限额**（D20）→ 显示「不限额」而不是 0/0。
 * 使用率 ≥ 90% 时改用 `warning` 语义色提示。
 */
export function QuotaBar({ className }: { className?: string }) {
  const user = useAuthStore((state) => state.user)
  if (!user) return null

  const unlimited = user.CapacityBytes <= 0
  const ratio = quotaRatio(user.UsedBytes, user.CapacityBytes)
  const nearLimit = !unlimited && ratio >= 0.9

  return (
    <div className={cn('space-y-1.5 px-3 py-3', className)}>
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="text-muted-foreground">{t('QUOTA_LABEL')}</span>
        <span className={cn('font-medium', nearLimit ? 'text-warning' : 'text-foreground')}>
          {unlimited
            ? t('QUOTA_UNLIMITED')
            : `${formatBytes(user.UsedBytes)} / ${formatBytes(user.CapacityBytes)}`}
        </span>
      </div>

      {unlimited ? null : (
        <Progress
          value={Math.round(ratio * 100)}
          variant={nearLimit ? 'warning' : 'brand'}
          aria-label={t('QUOTA_LABEL')}
        />
      )}
    </div>
  )
}
