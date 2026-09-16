import {
  AlertCircle,
  ArrowRight,
  HardDrive,
  Images,
  ListChecks,
  RefreshCw,
  Upload,
} from 'lucide-react'
import { Link } from 'react-router'
import type { ReactNode } from 'react'

import { PageHeader } from '@/components/common/page-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { EmptyState } from '@/components/ui/empty-state'
import { Skeleton } from '@/components/ui/skeleton'
import { QuotaDonut } from '@/features/overview/components/quota-donut'
import { StatCard } from '@/features/overview/components/stat-card'
import { StorageBreakdown } from '@/features/overview/components/storage-breakdown'
import { TrendChart } from '@/features/overview/components/trend-chart'
import { siteDisplayName, useSiteConfig, useSystemStats } from '@/hooks/api'
import { t } from '@/i18n'
import { formatBytes, formatDateTime, formatRelativeTime, quotaRatio } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/store/auth-store'

/**
 * 概览页（`/overview`）—— 内置 SPA 的登录后落地页。
 *
 * ⚠️ **`/` 不属于内置 SPA**（D94 / DESIGN.md §3.1）：
 *  - 生产环境 `/` 由**当前主题**渲染（默认主题 `Pages = ["/"]`），Go 分发在第 3 步就把它交给主题，
 *    内置 SPA 的路由不会被命中
 *  - 因此侧边栏「概览」指向 `/overview`，SPA 内的 `/` 只做一次重定向
 *    —— 见 D102 与 `router.tsx`
 *
 * 内容参考两个同类项目的仪表盘实现：
 *  - **lsky-pro** 用户仪表盘（图片数量 / 可用储存 / 使用储存 / 总储存 四卡 + 我的信息 + 角色组信息）
 *    与**管理员控制台**（今日 / 昨日 / 本周 / 本月上传 + 近 30 天趋势图）
 *  - **skyImage** 用户 Dashboard（容量上限 / 已用 / 剩余 + SVG 配额圆环 + 趋势图）
 *    与 AdminConsole（用户数 / 文件数 / 存储用量）
 *
 * 数据全部来自 `GET /api/web/v1/system/stats`（后端按角色决定 scope）：
 * 普通用户只看到自己的上传，管理员额外拿到 `Users` 与 `ByStorage`。
 * 趋势由**后端**按本地日分组（时区位移在 SQL 里做），前端只负责画。
 */
export function OverviewPage() {
  const user = useAuthStore((state) => state.user)
  const { config } = useSiteConfig()
  const { stats, loading, error, refresh } = useSystemStats()

  const isAdmin = user?.Role === 'admin'

  const capacityBytes = user?.CapacityBytes ?? 0
  const usedBytes = user?.UsedBytes ?? 0
  const unlimited = capacityBytes <= 0
  const remainingBytes = unlimited ? 0 : Math.max(capacityBytes - usedBytes, 0)
  const usedPercent = quotaRatio(usedBytes, capacityBytes) * 100

  const displayName = user?.Nickname?.trim() || user?.Email || ''

  return (
    <div className="space-y-5">
      <PageHeader
        title={t('OV_TITLE')}
        description={
          displayName
            ? t('OV_SUBTITLE', { name: displayName, site: siteDisplayName(config) })
            : t('OV_SUBTITLE_ANON', { site: siteDisplayName(config) })
        }
        actions={
          <>
            <Button variant="outline" size="sm" onClick={refresh} disabled={loading}>
              <RefreshCw aria-hidden className={loading ? 'animate-spin' : undefined} />
              {t('OV_REFRESH')}
            </Button>
            <Button asChild variant="brand" size="sm">
              <Link to="/upload">
                <Upload aria-hidden />
                {t('OV_GO_UPLOAD')}
              </Link>
            </Button>
          </>
        }
      />

      {error && !stats ? (
        <EmptyState
          icon={AlertCircle}
          title={t('OV_ERROR_TITLE')}
          description={error.message}
          action={
            <Button variant="outline" size="sm" onClick={refresh}>
              <RefreshCw aria-hidden />
              {t('OV_RETRY')}
            </Button>
          }
        />
      ) : (
        <>
          {/* ---- 资源卡片（lsky-pro 的四卡 + skyImage 的容量三栏）---- */}
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
            {loading && !stats ? (
              <>
                <Skeleton className="h-[132px] w-full" />
                <Skeleton className="h-[132px] w-full" />
                <Skeleton className="h-[132px] w-full" />
                <Skeleton className="h-[132px] w-full" />
              </>
            ) : (
              <>
                <StatCard
                  title={t('OV_STAT_STORAGE')}
                  value={formatBytes(usedBytes)}
                  hint={
                    unlimited
                      ? t('OV_STAT_STORAGE_UNLIMITED')
                      : t('OV_STAT_STORAGE_HINT', {
                          percent: usedPercent.toFixed(1),
                          capacity: formatBytes(capacityBytes),
                        })
                  }
                  icon={HardDrive}
                  tone="brand"
                />
                <StatCard
                  title={t('OV_STAT_IMAGES')}
                  value={stats?.Uploads.Total ?? 0}
                  hint={t('OV_STAT_IMAGES_HINT', { failed: stats?.Uploads.FailedCount ?? 0 })}
                  icon={Images}
                  tone="info"
                />
                <StatCard
                  title={t('OV_STAT_TODAY')}
                  value={stats?.Uploads.TodayCount ?? 0}
                  hint={t('OV_STAT_TODAY_HINT', { pending: stats?.Uploads.PendingCount ?? 0 })}
                  icon={Upload}
                  tone="success"
                />
                <StatCard
                  title={t('OV_STAT_JOBS')}
                  value={stats?.Jobs.Running ?? 0}
                  hint={t('OV_STAT_JOBS_HINT', { queued: stats?.Jobs.Queued ?? 0 })}
                  icon={ListChecks}
                  tone={(stats?.Jobs.Running ?? 0) > 0 ? 'warning' : 'muted'}
                />
              </>
            )}
          </div>

          {/* ---- 配额明细 + 上传状态 ---- */}
          <div className="grid gap-4 lg:grid-cols-3">
            <Card className="lg:col-span-2">
              <CardHeader className="pb-4">
                <CardTitle className="text-base">{t('OV_QUOTA_TITLE')}</CardTitle>
                <CardDescription>{t('OV_QUOTA_DESC')}</CardDescription>
              </CardHeader>
              <CardContent>
                <div className="flex flex-col items-center gap-6 sm:flex-row sm:justify-center sm:gap-10">
                  <QuotaDonut usedBytes={usedBytes} capacityBytes={capacityBytes} />

                  <dl className="grid w-full gap-4 sm:w-auto sm:grid-cols-3">
                    <div className="space-y-1 text-center">
                      <dt className="text-xs text-muted-foreground">{t('OV_QUOTA_USED')}</dt>
                      <dd className="text-lg font-semibold tabular-nums text-foreground">
                        {formatBytes(usedBytes)}
                      </dd>
                    </div>
                    <div className="space-y-1 text-center">
                      <dt className="text-xs text-muted-foreground">{t('OV_QUOTA_REMAINING')}</dt>
                      <dd className="text-lg font-semibold tabular-nums text-foreground">
                        {unlimited ? '—' : formatBytes(remainingBytes)}
                      </dd>
                    </div>
                    <div className="space-y-1 text-center">
                      <dt className="text-xs text-muted-foreground">{t('OV_QUOTA_TOTAL')}</dt>
                      <dd className="text-lg font-semibold tabular-nums text-foreground">
                        {unlimited ? t('OV_QUOTA_UNLIMITED') : formatBytes(capacityBytes)}
                      </dd>
                    </div>
                  </dl>
                </div>

                {unlimited ? (
                  <p className="mt-5 text-xs text-muted-foreground">{t('OV_QUOTA_UNLIMITED_HINT')}</p>
                ) : null}
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="pb-4">
                <CardTitle className="text-base">{t('OV_UPLOAD_STATUS_TITLE')}</CardTitle>
                <CardDescription>{t('OV_UPLOAD_STATUS_DESC')}</CardDescription>
              </CardHeader>
              <CardContent>
                <dl className="space-y-3">
                  <StatusRow
                    label={t('OV_UPLOAD_STATUS_PENDING')}
                    value={stats?.Uploads.PendingCount ?? 0}
                    tone="info"
                  />
                  <StatusRow
                    label={t('OV_UPLOAD_STATUS_FAILED')}
                    value={stats?.Uploads.FailedCount ?? 0}
                    tone="destructive"
                  />
                  <StatusRow
                    label={t('OV_UPLOAD_STATUS_TOTAL')}
                    value={stats?.Uploads.Total ?? 0}
                    tone="muted"
                  />
                  <StatusRow
                    label={t('OV_UPLOAD_STATUS_SIZE')}
                    value={formatBytes(stats?.Uploads.TotalSize ?? 0)}
                    tone="muted"
                  />
                </dl>
              </CardContent>
            </Card>
          </div>

          {/* ---- 近 30 天趋势 ---- */}
          {loading && !stats ? (
            <Skeleton className="h-72 w-full" />
          ) : (
            <TrendChart data={stats?.Trend ?? []} />
          )}

          {/* ---- 管理员专属：站点概况 + 按存储分布 ---- */}
          {isAdmin ? (
            <div className="grid gap-4 lg:grid-cols-2">
              <Card>
                <CardHeader className="pb-4">
                  <CardTitle className="text-base">{t('OV_ADMIN_TITLE')}</CardTitle>
                  <CardDescription>{t('OV_ADMIN_DESC')}</CardDescription>
                </CardHeader>
                <CardContent>
                  <dl className="grid grid-cols-2 gap-4">
                    <MetricCell
                      label={t('OV_ADMIN_USERS_TOTAL')}
                      value={stats?.Users?.Total ?? 0}
                    />
                    <MetricCell
                      label={t('OV_ADMIN_USERS_ACTIVE')}
                      value={stats?.Users?.Active ?? 0}
                    />
                    <MetricCell
                      label={t('OV_ADMIN_USERS_DISABLED')}
                      value={stats?.Users?.Disabled ?? 0}
                    />
                    <MetricCell
                      label={t('OV_ADMIN_USERS_ADMINS')}
                      value={stats?.Users?.Admins ?? 0}
                    />
                  </dl>
                  <Button asChild variant="outline" size="sm" className="mt-5">
                    <Link to="/admin/users">
                      {t('OV_ADMIN_MANAGE_USERS')}
                      <ArrowRight aria-hidden />
                    </Link>
                  </Button>
                </CardContent>
              </Card>

              <StorageBreakdown items={stats?.ByStorage ?? []} />
            </div>
          ) : null}

          {/* ---- 我的信息（参考 lsky-pro 的「我的信息」卡片）---- */}
          <Card>
            <CardHeader className="pb-4">
              <CardTitle className="text-base">{t('OV_PROFILE_TITLE')}</CardTitle>
              <CardDescription>{t('OV_PROFILE_DESC')}</CardDescription>
            </CardHeader>
            <CardContent>
              <dl className="grid gap-x-8 gap-y-3 sm:grid-cols-2 lg:grid-cols-3">
                <InfoRow label={t('OV_PROFILE_NICKNAME')} value={user?.Nickname?.trim() || '—'} />
                <InfoRow label={t('OV_PROFILE_EMAIL')} value={user?.Email ?? '—'} />
                <InfoRow
                  label={t('OV_PROFILE_ROLE')}
                  value={
                    isAdmin ? (
                      <Badge variant="brand">{t('OV_PROFILE_ROLE_ADMIN')}</Badge>
                    ) : (
                      <Badge variant="secondary">{t('OV_PROFILE_ROLE_USER')}</Badge>
                    )
                  }
                />
                <InfoRow
                  label={t('OV_PROFILE_CREATED')}
                  value={user?.CreatedAt ? formatDateTime(user.CreatedAt) : '—'}
                />
                <InfoRow
                  label={t('OV_PROFILE_LAST_LOGIN')}
                  value={
                    user?.LastLoginAt
                      ? `${formatRelativeTime(user.LastLoginAt)}（${formatDateTime(user.LastLoginAt)}）`
                      : '—'
                  }
                />
                <InfoRow
                  label={t('OV_PROFILE_SETTINGS')}
                  value={
                    <Link className="text-brand underline-offset-4 hover:underline" to="/settings">
                      {t('OV_PROFILE_SETTINGS_LINK')}
                    </Link>
                  }
                />
              </dl>
            </CardContent>
          </Card>
        </>
      )}
    </div>
  )
}

/** 上传状态的一行：名称 + 数量 + 语义色点。 */
function StatusRow({
  label,
  value,
  tone,
}: {
  label: string
  value: number | string
  tone: 'info' | 'destructive' | 'muted'
}) {
  return (
    <div className="flex items-center justify-between gap-3">
      <dt className="flex items-center gap-2 text-sm text-muted-foreground">
        <span
          className={cn(
            'size-2 shrink-0 rounded-full',
            tone === 'info' && 'bg-info',
            tone === 'destructive' && 'bg-destructive',
            tone === 'muted' && 'bg-muted-foreground',
          )}
          aria-hidden
        />
        {label}
      </dt>
      <dd className="text-sm font-medium tabular-nums text-foreground">{value}</dd>
    </div>
  )
}

/** 指标格（管理员区块用）。 */
function MetricCell({ label, value }: { label: string; value: number }) {
  return (
    <div className="space-y-1 rounded-lg bg-muted/50 px-3 py-2.5">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-xl font-semibold tabular-nums text-foreground">{value}</dd>
    </div>
  )
}

/** 「我的信息」的一行。 */
function InfoRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="space-y-1">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="truncate text-sm text-foreground">{value}</dd>
    </div>
  )
}
