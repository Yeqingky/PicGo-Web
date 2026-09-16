import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { t } from '@/i18n'
import { Progress } from '@/components/ui/progress'
import type { SystemStats } from '@/types/api'

/**
 * 按存储分布（仅管理员，DESIGN.md §4.2）。
 *
 * 参考 lsky-pro 控制台的「占用储存」维度：这里按**存储配置**拆分图片数，
 * 让管理员一眼看出流量集中在哪个图床。
 *
 * ⚠️ 存储可能已被删除 → 后端返回的 `Name` 为空串，此时显示「已删除的存储」。
 */
export interface StorageBreakdownProps {
  items: SystemStats['ByStorage']
  className?: string
}

export function StorageBreakdown({ items, className }: StorageBreakdownProps) {
  const list = items ?? []
  const total = list.reduce((sum, item) => sum + item.Count, 0)

  return (
    <Card className={className}>
      <CardHeader className="pb-4">
        <CardTitle className="text-base">{t('OV_STORAGE_DIST_TITLE')}</CardTitle>
        <CardDescription>
          {total > 0 ? t('OV_STORAGE_DIST_DESC', { total }) : t('OV_STORAGE_DIST_DESC_EMPTY')}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {list.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted-foreground">
            {t('OV_STORAGE_DIST_EMPTY')}
          </p>
        ) : (
          <ul className="space-y-3.5">
            {list.map((item) => {
              const ratio = total > 0 ? item.Count / total : 0
              return (
                <li key={item.StorageUID} className="space-y-1.5">
                  <div className="flex items-baseline justify-between gap-3 text-sm">
                    <span className="truncate font-medium text-foreground">
                      {item.Name?.trim() || t('OV_STORAGE_DIST_UNKNOWN')}
                    </span>
                    <span className="shrink-0 tabular-nums text-muted-foreground">
                      {item.Count}
                      <span className="ml-1.5 text-xs">{(ratio * 100).toFixed(1)}%</span>
                    </span>
                  </div>
                  <Progress value={Math.round(ratio * 100)} variant="brand" />
                </li>
              )
            })}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}
