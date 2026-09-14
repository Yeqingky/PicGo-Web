import { Badge, type BadgeProps } from '@/components/ui/badge'
import { t } from '@/i18n'
import type { JobStatus, LogStatus, UploadStatus } from '@/types/api'

/**
 * 状态徽章 / 色点（DESIGN.md §2.1 状态色用法、§9.3）。
 *
 * 语义色约定：
 *  - `success` 上传成功 / 任务成功
 *  - `warning` 部分失败 / 配额接近上限
 *  - `destructive` 失败 / 删除
 *  - `info` 进行中
 */

function statusVariant(status: string): BadgeProps['variant'] {
  switch (status) {
    case 'success':
    case 'succeeded':
      return 'success'
    case 'failed':
      return 'destructive'
    case 'running':
    case 'uploading':
    case 'pending':
      return 'info'
    case 'queued':
    case 'waiting':
      return 'secondary'
    default:
      return 'outline'
  }
}

function statusLabel(status: string): string {
  switch (status) {
    // 上传项 / 图片状态
    case 'waiting':
      return t('STATUS_WAITING')
    case 'uploading':
      return t('STATUS_UPLOADING')
    case 'success':
      return t('STATUS_SUCCESS')
    case 'failed':
      return t('STATUS_FAILED')
    // 任务状态（Job 只有 4 个，无 partial，D37）
    case 'queued':
      return t('STATUS_QUEUED')
    case 'running':
      return t('STATUS_RUNNING')
    case 'succeeded':
      return t('STATUS_SUCCEEDED')
    case 'pending':
      return t('STATUS_PENDING')
    default:
      return status
  }
}

/** 状态徽章。 */
export function StatusBadge({
  status,
  className,
}: {
  /** 上传项状态（`waiting`/`uploading`）也走这里 —— 它属于前端队列，不是服务端状态 */
  status: UploadStatus | JobStatus | 'waiting' | 'uploading' | string
  className?: string
}) {
  return (
    <Badge variant={statusVariant(status)} className={className}>
      {statusLabel(status)}
    </Badge>
  )
}

/** 状态色点（列表行首用，比徽章更轻量，DESIGN.md §5.6）。 */
export function StatusDot({ status, className }: { status: LogStatus | string; className?: string }) {
  const color =
    status === 'success'
      ? 'bg-success'
      : status === 'failed'
        ? 'bg-destructive'
        : 'bg-muted-foreground'

  return (
    <span
      className={`inline-block size-2 shrink-0 rounded-full ${color} ${className ?? ''}`}
      role="img"
      aria-label={statusLabel(status)}
    />
  )
}
