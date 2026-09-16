import { useState } from 'react'

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { t } from '@/i18n'
import { formatBytes } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { SystemStats } from '@/types/api'

/**
 * 近 30 天上传趋势（DESIGN.md §4.2）。
 *
 * 参考 lsky-pro 控制台的「近 30 天内统计」（ECharts 折线 + 图例 + 数量/体积切换）
 * 与 skyImage 的 `TrendChart`。**不引入图表库**（DESIGN.md §1），用纯 SVG 实现。
 *
 * 实现取舍：
 *  - 柱状体画在 `viewBox="0 0 100 100"` + `preserveAspectRatio="none"` 的 SVG 里
 *    （只画 `fill`，不画描边，因此非等比缩放不会变形）
 *  - **网格线与文字全部用 HTML 覆盖层**：SVG 里的 `<text>` 会被非等比缩放拉变形，
 *    轴标签用绝对定位的 HTML 才能保持与正文一致的字号
 *  - 悬停用 HTML 的透明热区（每个日期一格），tooltip 也是 HTML，便于样式统一
 *
 * 数据来源：`GET /system/stats` 的 `Trend`，后端已按**本地日**分组（时区位移在 SQL 里做）。
 */

type Metric = 'count' | 'size'

const PLOT_HEIGHT = 176
/** 纵轴刻度数（含 0） */
const TICK_COUNT = 5

export interface TrendChartProps {
  data: SystemStats['Trend']
  className?: string
}

/** 把最大值向上取整到「好看」的刻度（1/2/2.5/5 × 10ⁿ），避免纵轴出现 137 这种刻度。 */
function niceCeil(value: number): number {
  if (value <= 0) return 1
  const exponent = Math.floor(Math.log10(value))
  const base = 10 ** exponent
  const normalized = value / base
  const step =
    normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 2.5 ? 2.5 : normalized <= 5 ? 5 : 10
  return step * base
}

/** 紧凑数字（1000 → 1K），用于纵轴与 tooltip。 */
function compactNumber(value: number): string {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  if (value >= 1000) return `${(value / 1000).toFixed(1)}K`
  return String(value)
}

export function TrendChart({ data, className }: TrendChartProps) {
  const [metric, setMetric] = useState<Metric>('count')
  const [hover, setHover] = useState<number | null>(null)

  const points = data ?? []
  const valueOf = (point: SystemStats['Trend'][number]): number =>
    metric === 'count' ? point.Count : point.Size
  const formatValue = (value: number): string =>
    metric === 'count' ? `${value}` : formatBytes(value, 2)

  const max = niceCeil(Math.max(0, ...points.map(valueOf)))
  const total = points.reduce((sum, point) => sum + valueOf(point), 0)
  const peak = points.reduce(
    (best, point) => (valueOf(point) > valueOf(best) ? point : best),
    points[0],
  )

  const slot = points.length > 0 ? 100 / points.length : 100
  const barWidth = slot * 0.62

  const ticks = Array.from({ length: TICK_COUNT }, (_, index) => {
    const ratio = index / (TICK_COUNT - 1)
    return { ratio, value: max * (1 - ratio) }
  })

  /** x 轴只标 首 / 中 / 尾 三个日期，避免 30 个标签糊成一片 */
  const labelIndexes =
    points.length === 0
      ? []
      : points.length === 1
        ? [0]
        : [0, Math.floor((points.length - 1) / 2), points.length - 1]

  const hovered = hover !== null ? points[hover] : undefined

  return (
    <Card className={className}>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3 space-y-0 pb-4">
        <div className="space-y-1.5">
          <CardTitle className="text-base">{t('OV_TREND_TITLE')}</CardTitle>
          <CardDescription>{t('OV_TREND_DESC')}</CardDescription>
        </div>

        {/* 指标切换（lsky-pro 的「数量 / 体积」两个系列在本项目里拆成切换，避免双纵轴误读） */}
        <div
          className="flex items-center gap-1 rounded-lg border border-input p-1"
          role="group"
          aria-label={t('OV_TREND_METRIC_LABEL')}
        >
          {(['count', 'size'] as const).map((value) => (
            <button
              key={value}
              type="button"
              onClick={() => setMetric(value)}
              aria-pressed={metric === value}
              className={cn(
                'rounded-md px-2.5 py-1 text-xs font-medium transition-colors duration-150',
                'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
                metric === value
                  ? 'bg-brand/10 text-brand'
                  : 'text-muted-foreground hover:bg-accent hover:text-foreground',
              )}
            >
              {value === 'count' ? t('OV_TREND_METRIC_COUNT') : t('OV_TREND_METRIC_SIZE')}
            </button>
          ))}
        </div>
      </CardHeader>

      <CardContent className="space-y-4">
        {points.length === 0 ? (
          <p className="py-10 text-center text-sm text-muted-foreground">{t('OV_TREND_EMPTY')}</p>
        ) : (
          <>
            <div className="flex gap-2">
              {/* 纵轴刻度 */}
              <div className="relative w-12 shrink-0" style={{ height: PLOT_HEIGHT }}>
                {ticks.map((tick) => (
                  <span
                    key={tick.ratio}
                    className="absolute right-0 -translate-y-1/2 text-xs tabular-nums text-muted-foreground"
                    style={{ top: `${tick.ratio * 100}%` }}
                  >
                    {metric === 'count' ? compactNumber(Math.round(tick.value)) : formatBytes(tick.value, 0)}
                  </span>
                ))}
              </div>

              <div className="relative flex-1" style={{ height: PLOT_HEIGHT }}>
                {/* 网格线（HTML，避免 SVG 非等比缩放导致线宽不均） */}
                {ticks.map((tick) => (
                  <div
                    key={tick.ratio}
                    className={cn(
                      'absolute inset-x-0 h-px',
                      tick.ratio === 1 ? 'bg-border' : 'bg-border/60',
                    )}
                    style={{ top: `${tick.ratio * 100}%` }}
                    aria-hidden
                  />
                ))}

                {/* 柱体 */}
                <svg
                  className="absolute inset-0 h-full w-full"
                  viewBox="0 0 100 100"
                  preserveAspectRatio="none"
                  aria-hidden
                >
                  {points.map((point, index) => {
                    const height = max > 0 ? (valueOf(point) / max) * 100 : 0
                    if (height <= 0) return null
                    return (
                      <rect
                        key={point.Date}
                        x={slot * index + (slot - barWidth) / 2}
                        y={100 - height}
                        width={barWidth}
                        height={height}
                        className={cn(
                          hover === index ? 'fill-brand' : 'fill-brand/55',
                          'transition-[fill] duration-150',
                        )}
                      />
                    )
                  })}
                </svg>

                {/* 悬停热区（每格一个，命中面积比柱体大） */}
                {points.map((point, index) => (
                  <div
                    key={point.Date}
                    className="absolute inset-y-0 cursor-default"
                    style={{ left: `${slot * index}%`, width: `${slot}%` }}
                    onMouseEnter={() => setHover(index)}
                    onMouseLeave={() => setHover((current) => (current === index ? null : current))}
                  />
                ))}

                {/* tooltip */}
                {hovered ? (
                  <div
                    className="pointer-events-none absolute -top-1 z-10 -translate-x-1/2 whitespace-nowrap rounded-md border border-border bg-popover px-2.5 py-1.5 text-xs shadow-md"
                    style={{
                      left: `${Math.min(Math.max(slot * (hover ?? 0) + slot / 2, 8), 92)}%`,
                    }}
                    role="status"
                  >
                    <span className="text-muted-foreground">{hovered.Date}</span>
                    <span className="ml-2 font-medium tabular-nums text-popover-foreground">
                      {formatValue(valueOf(hovered))}
                    </span>
                  </div>
                ) : null}
              </div>
            </div>

            {/* x 轴标签 */}
            <div className="flex gap-2">
              <div className="w-12 shrink-0" aria-hidden />
              <div className="relative h-4 flex-1">
                {labelIndexes.map((index) => (
                  <span
                    key={index}
                    className="absolute -translate-x-1/2 text-xs tabular-nums text-muted-foreground"
                    style={{ left: `${Math.min(Math.max(slot * index + slot / 2, 6), 94)}%` }}
                  >
                    {points[index].Date.slice(5)}
                  </span>
                ))}
              </div>
            </div>

            {/* 汇总：合计 + 峰值（参考 lsky-pro 的「今日/昨日/本周/本月」把总量摆在明处） */}
            <div className="flex flex-wrap gap-x-8 gap-y-2 border-t border-border pt-4 text-sm">
              <div className="space-y-0.5">
                <p className="text-xs text-muted-foreground">{t('OV_TREND_TOTAL')}</p>
                <p className="font-medium tabular-nums text-foreground">
                  {formatValue(total)}
                </p>
              </div>
              <div className="space-y-0.5">
                <p className="text-xs text-muted-foreground">{t('OV_TREND_PEAK')}</p>
                <p className="font-medium tabular-nums text-foreground">
                  {peak ? `${formatValue(valueOf(peak))}` : '—'}
                  {peak ? (
                    <span className="ml-2 text-xs font-normal text-muted-foreground">
                      {peak.Date}
                    </span>
                  ) : null}
                </p>
              </div>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}
