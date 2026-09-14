import { Download, Loader2 } from 'lucide-react'
import { useEffect, useRef } from 'react'

import { JsonView } from '@/components/common/json-view'
import { StatusBadge } from '@/components/common/status-badge'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { useJobDetail, useJobLogs } from '@/hooks/api'
import { t } from '@/i18n'
import { formatDateTime } from '@/lib/format'
import { cn } from '@/lib/utils'

/**
 * 任务日志抽屉（DESIGN.md §5.4 / §5.5）。
 *
 * 用于：
 *  - 插件安装 / 卸载 / 更新（npm 输出逐行可见）
 *  - 上传批次（哪个文件失败、为什么）
 *
 * 日志来源有两路，**互为补充**：
 *  1. SSE `job.log` 到达即追加（`task-store` 累积）
 *  2. `GET /jobs/{Uid}/logs?AfterSeq=` 增量拉取（补齐断线期间丢失的事件）
 *
 * ⚠️ 「任务结束后 **agent 会重启自身**」是**预期行为**（API.md §6.1），
 *    不是错误 —— 插件页会额外显示「内核正在重启」提示。
 */
export interface JobLogDrawerProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  jobUID: string
}

export function JobLogDrawer({ open, onOpenChange, jobUID }: JobLogDrawerProps) {
  const { job, loading, error } = useJobDetail(jobUID, { skip: !open })
  const { logs, loading: logsLoading } = useJobLogs(jobUID, { skip: !open })

  const scrollRef = useRef<HTMLDivElement>(null)
  const shouldStick = useRef(true)

  // 自动滚底（DESIGN.md §5.5）—— 但用户手动上滚查看历史时不打断他
  useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    if (shouldStick.current) {
      el.scrollTop = el.scrollHeight
    }
  }, [logs])

  const onScroll = () => {
    const el = scrollRef.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40
    shouldStick.current = atBottom
  }

  const downloadLogs = () => {
    const text = logs.map((line) => line.Line).join('\n')
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${jobUID}.log`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-2xl">
        <SheetHeader className="border-b border-border px-5 py-4">
          <SheetTitle>{t('JOB_DRAWER_TITLE')}</SheetTitle>
          <SheetDescription>
            <span className="font-mono text-xs">{jobUID}</span>
          </SheetDescription>
        </SheetHeader>

        <div className="flex-1 space-y-4 overflow-y-auto px-5 py-4 scrollbar-thin">
          {/* 基本信息 */}
          {error ? (
            <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {t('JOB_LOAD_FAILED')}
            </p>
          ) : loading && !job ? (
            <p className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" aria-hidden />
              {t('COMMON_LOADING')}
            </p>
          ) : job ? (
            <section className="space-y-2 rounded-lg border border-border p-3">
              <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-sm">
                <StatusBadge status={job.Status} />
                <span className="text-muted-foreground">
                  {t('JOB_KIND')}: <span className="text-foreground">{job.Kind}</span>
                </span>
                <span className="text-muted-foreground">
                  {t('JOB_CREATED')}: {formatDateTime(job.CreatedAt)}
                </span>
              </div>

              <Progress
                value={job.Progress}
                variant={job.Status === 'failed' ? 'warning' : 'brand'}
              />

              <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
                <span>
                  {t('JOB_ITEMS_TOTAL')}: {job.TotalItems}
                </span>
                <span className="text-success">
                  {t('JOB_ITEMS_OK')}: {job.SucceededItems}
                </span>
                <span className={cn(job.FailedItems > 0 && 'text-destructive')}>
                  {t('JOB_ITEMS_FAILED')}: {job.FailedItems}
                </span>
                <span>
                  {t('JOB_ITEMS_SKIPPED')}: {job.SkippedItems}
                </span>
              </div>

              {job.Error ? (
                <p className="rounded bg-destructive/10 px-2 py-1.5 text-xs text-destructive">
                  {job.Error}
                </p>
              ) : null}
            </section>
          ) : null}

          {/* 批内子项（上传类任务尤其有用） */}
          {job?.Items && job.Items.length > 0 ? (
            <section className="space-y-2">
              <h3 className="text-sm font-medium text-foreground">{t('JOB_ITEMS_TITLE')}</h3>
              <ul className="divide-y divide-border overflow-hidden rounded-lg border border-border text-sm">
                {job.Items.map((item) => (
                  <li key={item.Seq} className="flex items-center gap-3 px-3 py-2">
                    <span className="w-6 shrink-0 text-xs text-muted-foreground">#{item.Seq}</span>
                    <span className="min-w-0 flex-1 truncate" title={item.FileName}>
                      {item.FileName}
                    </span>
                    <StatusBadge status={item.Status} />
                    {item.Error ? (
                      <span className="max-w-[40%] truncate text-xs text-destructive" title={item.Error}>
                        {item.Error}
                      </span>
                    ) : null}
                  </li>
                ))}
              </ul>
            </section>
          ) : null}

          {/* 逐行日志 */}
          <section className="space-y-2">
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-medium text-foreground">{t('JOB_LOGS_TITLE')}</h3>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={logs.length === 0}
                onClick={downloadLogs}
              >
                <Download aria-hidden />
                {t('JOB_LOGS_DOWNLOAD')}
              </Button>
            </div>

            <div
              ref={scrollRef}
              onScroll={onScroll}
              className="max-h-72 overflow-auto rounded-lg border border-border bg-muted/30 p-3 font-mono text-xs leading-relaxed scrollbar-thin"
            >
              {logsLoading && logs.length === 0 ? (
                <p className="text-muted-foreground">{t('COMMON_LOADING')}</p>
              ) : logs.length === 0 ? (
                <p className="text-muted-foreground">{t('JOB_LOGS_EMPTY')}</p>
              ) : (
                logs.map((line) => (
                  <div key={line.Seq} className="whitespace-pre-wrap break-all text-foreground">
                    {line.Line}
                  </div>
                ))
              )}
            </div>
          </section>

          {/* 结果 JSON（含成功项的 URL，D37：失败时成功项也不丢） */}
          {job?.Result ? (
            <section className="space-y-2">
              <h3 className="text-sm font-medium text-foreground">{t('JOB_RESULT_TITLE')}</h3>
              <JsonView value={job.Result} collapseLines={10} />
            </section>
          ) : null}
        </div>
      </SheetContent>
    </Sheet>
  )
}
