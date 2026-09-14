import { ClipboardList, RotateCw, Trash2 } from 'lucide-react'
import { useState } from 'react'

import { StatusBadge } from '@/components/common/status-badge'
import { JobLogDrawer } from '@/components/jobs/job-log-drawer'
import { PageHeader } from '@/components/common/page-header'
import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/ui/empty-state'
import { IconButton } from '@/components/ui/icon-button'
import { Label } from '@/components/ui/label'
import { Pagination } from '@/components/ui/pagination'
import { Progress } from '@/components/ui/progress'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { toast } from '@/components/ui/toast'
import { useJobs } from '@/hooks/api'
import { t } from '@/i18n'
import { jobApi } from '@/lib/api'
import { formatDateTime } from '@/lib/format'
import { toApiError } from '@/types/api'
import { useAuthStore } from '@/store/auth-store'

/**
 * 任务（DESIGN.md §5.5）。
 *
 * 语义要点：
 *  - job 状态**只有 4 个**（D37）：有 item 失败即 `failed`，但成功项结果照样在 `Result` 里
 *  - 进度**实时更新**：SSE `job.finished` 到达时 `task-store.jobsRevision` 变化 → 列表自动重拉
 *  - 只能清理**已结束**的任务（运行中由后端拒绝）
 */
export function JobsPage() {
  const isAdmin = useAuthStore((state) => state.user?.Role === 'admin')

  const [kind, setKind] = useState('')
  const [status, setStatus] = useState('')
  const [scope, setScope] = useState<'mine' | 'all'>('mine')
  const [openJob, setOpenJob] = useState('')

  const { items, total, page, pageSize, loading, error, setPage, refresh } = useJobs({
    Kind: kind || undefined,
    Status: (status || undefined) as never,
    Scope: scope,
  })

  const cleanupFinished = async () => {
    const finished = items.filter((job) => job.Status === 'succeeded' || job.Status === 'failed')
    if (finished.length === 0) return

    let removed = 0
    for (const job of finished) {
      try {
        await jobApi.remove(job.UID)
        removed += 1
      } catch {
        // 逐条独立：某条失败（例如其实还在跑）不影响其它
      }
    }

    toast.success(t('JOB_CLEANUP_DONE', { count: removed }))
    refresh()
  }

  return (
    <>
      <PageHeader
        title={t('NAV_JOBS')}
        description={t('JOB_DESC')}
        actions={
          <>
            <Button variant="outline" size="sm" onClick={cleanupFinished}>
              <Trash2 aria-hidden />
              {t('JOB_CLEANUP')}
            </Button>
            <IconButton label={t('COMMON_REFRESH')} onClick={refresh}>
              <RotateCw aria-hidden />
            </IconButton>
          </>
        }
      />

      <div className="mb-4 flex flex-wrap items-end gap-3">
        {isAdmin ? (
          <div className="space-y-1">
            <Label className="text-xs text-muted-foreground">{t('JOB_SCOPE')}</Label>
            <Select value={scope} onValueChange={(value) => setScope(value as 'mine' | 'all')}>
              <SelectTrigger className="min-w-[9rem]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="mine">{t('JOB_SCOPE_MINE')}</SelectItem>
                <SelectItem value="all">{t('JOB_SCOPE_ALL')}</SelectItem>
              </SelectContent>
            </Select>
          </div>
        ) : null}

        <div className="space-y-1">
          <Label className="text-xs text-muted-foreground">{t('JOB_KIND')}</Label>
          <Select value={kind || '__all__'} onValueChange={(value) => setKind(value === '__all__' ? '' : value)}>
            <SelectTrigger className="min-w-[11rem]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__all__">{t('GALLERY_FILTER_ALL')}</SelectItem>
              <SelectItem value="upload">{t('JOB_KIND_UPLOAD')}</SelectItem>
              <SelectItem value="plugin.install">{t('JOB_KIND_PLUGIN_INSTALL')}</SelectItem>
              <SelectItem value="plugin.uninstall">{t('JOB_KIND_PLUGIN_UNINSTALL')}</SelectItem>
              <SelectItem value="plugin.update">{t('JOB_KIND_PLUGIN_UPDATE')}</SelectItem>
              <SelectItem value="theme.install">{t('JOB_KIND_THEME_INSTALL')}</SelectItem>
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-1">
          <Label className="text-xs text-muted-foreground">{t('GALLERY_FILTER_STATUS')}</Label>
          <Select
            value={status || '__all__'}
            onValueChange={(value) => setStatus(value === '__all__' ? '' : value)}
          >
            <SelectTrigger className="min-w-[9rem]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__all__">{t('GALLERY_FILTER_ALL')}</SelectItem>
              <SelectItem value="queued">{t('STATUS_QUEUED')}</SelectItem>
              <SelectItem value="running">{t('STATUS_RUNNING')}</SelectItem>
              <SelectItem value="succeeded">{t('STATUS_SUCCEEDED')}</SelectItem>
              <SelectItem value="failed">{t('STATUS_FAILED')}</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>

      {loading && items.length === 0 ? (
        <div className="space-y-2">
          {Array.from({ length: 5 }, (_, index) => (
            <Skeleton key={index} className="h-12 w-full" />
          ))}
        </div>
      ) : error ? (
        <EmptyState
          title={t('JOB_LOAD_FAILED')}
          description={toApiError(error).message}
          action={
            <Button variant="outline" onClick={refresh}>
              {t('COMMON_RETRY')}
            </Button>
          }
        />
      ) : items.length === 0 ? (
        <EmptyState icon={ClipboardList} title={t('JOB_EMPTY')} description={t('JOB_EMPTY_DESC')} />
      ) : (
        <>
          <div className="overflow-x-auto rounded-lg border border-border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('JOB_COL_ID')}</TableHead>
                  <TableHead>{t('JOB_COL_KIND')}</TableHead>
                  <TableHead>{t('JOB_COL_STATUS')}</TableHead>
                  <TableHead className="min-w-[10rem]">{t('JOB_COL_PROGRESS')}</TableHead>
                  <TableHead>{t('JOB_COL_ITEMS')}</TableHead>
                  <TableHead>{t('JOB_COL_CREATED')}</TableHead>
                </TableRow>
              </TableHeader>

              <TableBody>
                {items.map((job) => (
                  <TableRow
                    key={job.UID}
                    className="cursor-pointer"
                    onClick={() => setOpenJob(job.UID)}
                  >
                    <TableCell className="font-mono text-xs">{job.UID}</TableCell>
                    <TableCell className="text-sm">{job.Kind}</TableCell>
                    <TableCell>
                      <StatusBadge status={job.Status} />
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center gap-2">
                        <Progress
                          value={job.Progress}
                          variant={job.Status === 'failed' ? 'warning' : 'brand'}
                          className="h-1.5 min-w-[5rem] flex-1"
                        />
                        <span className="w-9 shrink-0 text-right text-xs text-muted-foreground">
                          {job.Progress}%
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                      <span className="text-success">{job.SucceededItems}</span>
                      {' / '}
                      <span className={job.FailedItems > 0 ? 'text-destructive' : undefined}>
                        {job.FailedItems}
                      </span>
                      {' / '}
                      {job.TotalItems}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                      {formatDateTime(job.CreatedAt)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <Pagination
            className="mt-4"
            page={page}
            pageSize={pageSize}
            total={total}
            onPageChange={setPage}
          />
        </>
      )}

      <JobLogDrawer open={openJob !== ''} onOpenChange={(open) => !open && setOpenJob('')} jobUID={openJob} />
    </>
  )
}
