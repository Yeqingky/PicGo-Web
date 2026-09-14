import { CheckCircle2, Copy, Loader2, RotateCw, Trash2, XCircle } from 'lucide-react'
import { useState } from 'react'

import { StatusBadge } from '@/components/common/status-badge'
import { Button } from '@/components/ui/button'
import { IconButton } from '@/components/ui/icon-button'
import { Progress } from '@/components/ui/progress'
import { toast } from '@/components/ui/toast'
import { t } from '@/i18n'
import { copyText, formatLink, type LinkFormat } from '@/lib/clipboard'
import { formatBytes, truncateFileName } from '@/lib/format'
import { cn } from '@/lib/utils'
import { batchProgress, type UploadItem } from '@/store/upload-store'

/**
 * 上传队列（DESIGN.md §5.1）。
 *
 * 两条语义：
 *  - **整批进度 = 已完成项 / 总项数**（DESIGN 明确写了这个口径）；
 *    单文件进度来自 SSE `upload.progress`（picgo 只有 0/30/60/100 四档，
 *    在 `upload-store` 里做了平滑插值）
 *  - 成功项直接给「复制链接」（三种格式，D68），省一次跳转
 */
export interface UploadQueueProps {
  items: UploadItem[]
  onRetry: (id: string) => void
  onRemove: (id: string) => void
  onRetryAll: () => void
  onClearFinished: () => void
  className?: string
}

export function UploadQueue({
  items,
  onRetry,
  onRemove,
  onRetryAll,
  onClearFinished,
  className,
}: UploadQueueProps) {
  const failedCount = items.filter((item) => item.Status === 'failed').length
  const finishedCount = items.filter(
    (item) => item.Status === 'success' || item.Status === 'failed',
  ).length

  if (items.length === 0) return null

  const progress = batchProgress(items)

  return (
    <section className={cn('space-y-3', className)} aria-label={t('UPLOAD_QUEUE_TITLE')}>
      <header className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <h2 className="text-base font-medium text-foreground">{t('UPLOAD_QUEUE_TITLE')}</h2>
          <span className="text-xs text-muted-foreground">
            {t('UPLOAD_QUEUE_COUNT', { total: items.length, done: finishedCount })}
          </span>
        </div>

        <div className="flex items-center gap-2">
          {failedCount > 0 ? (
            <Button type="button" variant="outline" size="sm" onClick={onRetryAll}>
              <RotateCw aria-hidden />
              {t('UPLOAD_RETRY_ALL', { count: failedCount })}
            </Button>
          ) : null}
          {finishedCount > 0 ? (
            <Button type="button" variant="ghost" size="sm" onClick={onClearFinished}>
              {t('UPLOAD_CLEAR_FINISHED')}
            </Button>
          ) : null}
        </div>
      </header>

      <Progress value={progress} variant={failedCount > 0 ? 'warning' : 'brand'} />

      <ul className="divide-y divide-border overflow-hidden rounded-lg border border-border">
        {items.map((item) => (
          <UploadQueueRow
            key={item.ID}
            item={item}
            onRetry={() => onRetry(item.ID)}
            onRemove={() => onRemove(item.ID)}
          />
        ))}
      </ul>
    </section>
  )
}

interface RowProps {
  item: UploadItem
  onRetry: () => void
  onRemove: () => void
}

function UploadQueueRow({ item, onRetry, onRemove }: RowProps) {
  const [copied, setCopied] = useState(false)
  const [showError, setShowError] = useState(false)

  const copy = async (format: LinkFormat) => {
    if (!item.URL) return
    const ok = await copyText(formatLink({ URL: item.URL, Name: item.FileName }, format))
    if (ok) {
      setCopied(true)
      toast.success(t('COMMON_COPIED'))
      setTimeout(() => setCopied(false), 2000)
    } else {
      toast.error(t('COMMON_COPY_FAILED'))
    }
  }

  return (
    <li className="flex items-center gap-3 bg-background px-3 py-2.5">
      {/* 本地预览（objectURL，与后端缩略图无关，D84 不受影响） */}
      <div className="flex size-10 shrink-0 items-center justify-center overflow-hidden rounded border border-border bg-muted">
        {item.PreviewURL ? (
          <img src={item.PreviewURL} alt="" className="size-full object-cover" />
        ) : (
          <span className="text-[10px] text-muted-foreground">IMG</span>
        )}
      </div>

      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm text-foreground" title={item.FileName}>
            {truncateFileName(item.FileName, 48)}
          </span>
          <span className="shrink-0 text-xs text-muted-foreground">{formatBytes(item.Size)}</span>
        </div>

        {item.Status === 'uploading' ? (
          <Progress value={item.Progress} variant="brand" className="h-1.5" />
        ) : (
          <div className="flex items-center gap-2">
            <StatusBadge status={item.Status} />
            {item.Status === 'failed' && item.Error ? (
              <button
                type="button"
                onClick={() => setShowError((prev) => !prev)}
                className="text-xs text-destructive underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                {t('UPLOAD_SHOW_ERROR')}
              </button>
            ) : null}
            {item.Attempts > 0 ? (
              <span className="text-xs text-muted-foreground">
                {t('UPLOAD_ATTEMPTS', { count: item.Attempts })}
              </span>
            ) : null}
          </div>
        )}

        {showError && item.Error ? (
          <p className="rounded bg-destructive/10 px-2 py-1 text-xs text-destructive">
            {item.Error}
          </p>
        ) : null}
      </div>

      {/* 右侧操作 */}
      <div className="flex shrink-0 items-center gap-1">
        {item.Status === 'success' && item.URL ? (
          <>
            <IconButton
              label={t('LINK_FORMAT_URL')}
              onClick={() => void copy('url')}
              className={cn(copied && 'text-success')}
            >
              {copied ? <CheckCircle2 aria-hidden /> : <Copy aria-hidden />}
            </IconButton>
            <IconButton
              label={t('LINK_FORMAT_MARKDOWN')}
              onClick={() => void copy('markdown')}
            >
              <span className="text-[10px] font-semibold" aria-hidden>
                MD
              </span>
            </IconButton>
          </>
        ) : null}

        {item.Status === 'failed' ? (
          <IconButton label={t('COMMON_RETRY')} onClick={onRetry}>
            <RotateCw aria-hidden />
          </IconButton>
        ) : null}

        {item.Status === 'uploading' ? (
          <span className="px-1 text-muted-foreground" aria-hidden>
            <Loader2 className="size-4 animate-spin" />
          </span>
        ) : null}

        <IconButton
          label={t('COMMON_DELETE')}
          onClick={onRemove}
          className="text-muted-foreground hover:text-destructive"
        >
          <Trash2 aria-hidden />
        </IconButton>
      </div>
    </li>
  )
}

/** 空队列时的提示（供页面区分「刚进来」与「传完了」）。 */
export function EmptyQueueHint({ failed }: { failed: number }) {
  if (failed > 0) {
    return (
      <p className="flex items-center gap-2 text-sm text-destructive">
        <XCircle className="size-4" aria-hidden />
        {t('UPLOAD_ALL_FAILED')}
      </p>
    )
  }
  return null
}
