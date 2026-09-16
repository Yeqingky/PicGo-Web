import { t } from '@/i18n'
import { formatBytes } from '@/lib/format'
import { cn } from '@/lib/utils'

/**
 * 配额环形图（DESIGN.md §4.2）。
 *
 * 参考 skyImage 仪表盘的 SVG 圆环：`stroke-dasharray` 画进度弧，中心放百分比。
 * 不引入图表库（DESIGN.md §1：不引入第三方组件库），纯 SVG 即可。
 *
 * `capacityBytes = 0` 表示**不限额**（D20）：此时不画弧，中心显示「不限额」。
 */
export interface QuotaDonutProps {
  usedBytes: number
  capacityBytes: number
  /** 直径（px） */
  size?: number
  className?: string
}

const STROKE = 10

export function QuotaDonut({ usedBytes, capacityBytes, size = 132, className }: QuotaDonutProps) {
  const unlimited = capacityBytes <= 0
  const ratio = unlimited ? 0 : Math.min(Math.max(usedBytes / capacityBytes, 0), 1)
  const percent = ratio * 100

  // 半径与周长：用 viewBox 坐标系（120×120）计算，与 size 解耦
  const r = 48
  const circumference = 2 * Math.PI * r
  const dash = (ratio * circumference).toFixed(2)

  // ≥ 90% 用 warning 提示（与 QuotaBar / Progress 的语义保持一致）
  const nearLimit = !unlimited && ratio >= 0.9

  return (
    <div
      className={cn('relative shrink-0', className)}
      style={{ width: size, height: size }}
      role="img"
      aria-label={
        unlimited
          ? t('OV_QUOTA_ARIA_UNLIMITED', { used: formatBytes(usedBytes) })
          : t('OV_QUOTA_ARIA', { percent: percent.toFixed(1) })
      }
    >
      <svg viewBox="0 0 120 120" className="h-full w-full">
        <circle
          cx="60"
          cy="60"
          r={r}
          fill="none"
          strokeWidth={STROKE}
          className="stroke-muted"
        />
        {unlimited ? null : (
          <circle
            cx="60"
            cy="60"
            r={r}
            fill="none"
            strokeWidth={STROKE}
            strokeLinecap="round"
            strokeDasharray={`${dash} ${circumference.toFixed(2)}`}
            transform="rotate(-90 60 60)"
            className={cn(
              'transition-[stroke-dasharray] duration-300',
              nearLimit ? 'stroke-warning' : 'stroke-brand',
            )}
          />
        )}
      </svg>

      <div className="absolute inset-0 flex flex-col items-center justify-center">
        {unlimited ? (
          <span className="text-sm font-medium text-muted-foreground">{t('OV_QUOTA_UNLIMITED')}</span>
        ) : (
          <>
            <span
              className={cn(
                'text-xl font-semibold tabular-nums',
                nearLimit ? 'text-warning' : 'text-foreground',
              )}
            >
              {percent.toFixed(1)}%
            </span>
            <span className="text-xs text-muted-foreground">{t('OV_QUOTA_USAGE')}</span>
          </>
        )}
      </div>
    </div>
  )
}
