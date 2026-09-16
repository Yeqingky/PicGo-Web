import { ArrowLeft, ExternalLink, Pencil, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'

import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { LinkCopyMenu } from '@/components/common/link-copy-menu'
import { PageHeader } from '@/components/common/page-header'
import { StatusBadge } from '@/components/common/status-badge'
import { RenameUploadDialog } from '@/components/gallery/upload-dialogs'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { EmptyState } from '@/components/ui/empty-state'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { toast } from '@/components/ui/toast'
import { useUploadDetail } from '@/hooks/api'
import { t } from '@/i18n'
import { uploadApi } from '@/lib/api'
import { formatBytes, formatDateTime, formatDimensions } from '@/lib/format'
import { toApiError } from '@/types/api'

/**
 * 图片详情（DESIGN.md §3.2 `/gallery/:UID`）。
 *
 * 内容：大图 + 元数据（文件名/尺寸/大小/存储/时间）+ 外链复制 + 重命名/删除。
 *
 * 注意 `AliasName` 与 `FileName` 的区别：前者是**站内展示名**，
 * 后者是**图床上的真实文件名**（含魔法文件名结果）。重命名只改前者（D42/D66 边界）。
 */
export function GalleryDetailPage() {
  const { uid = '' } = useParams()
  const navigate = useNavigate()

  const { upload, loading, error, refresh } = useUploadDetail(uid)

  const [renaming, setRenaming] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [deleteRemote, setDeleteRemote] = useState(false)

  const confirmDelete = async () => {
    await uploadApi.remove(uid, deleteRemote)
    toast.success(t('GALLERY_DELETE_SUCCESS', { count: 1 }))
    navigate('/gallery', { replace: true })
  }

  if (loading && !upload) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-80 w-full" />
      </div>
    )
  }

  if (error || !upload) {
    return (
      <EmptyState
        title={t('GALLERY_DETAIL_NOT_FOUND')}
        description={error ? toApiError(error).message : undefined}
        action={
          <Button asChild variant="outline">
            <Link to="/gallery">
              <ArrowLeft aria-hidden />
              {t('COMMON_BACK')}
            </Link>
          </Button>
        }
      />
    )
  }

  const displayName = upload.AliasName || upload.OriginalName || upload.FileName

  return (
    <>
      <PageHeader
        title={displayName}
        description={upload.FileName}
        actions={
          <>
            <Button asChild variant="ghost" size="sm">
              <Link to="/gallery">
                <ArrowLeft aria-hidden />
                {t('COMMON_BACK')}
              </Link>
            </Button>
            <LinkCopyMenu
              items={[{ URL: upload.URL, Name: displayName }]}
              variant="brand"
            />
            <Button variant="outline" size="sm" onClick={() => setRenaming(true)}>
              <Pencil aria-hidden />
              {t('GALLERY_RENAME_TITLE')}
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="text-destructive hover:text-destructive"
              onClick={() => setDeleting(true)}
            >
              <Trash2 aria-hidden />
              {t('COMMON_DELETE')}
            </Button>
          </>
        }
      />

      <div className="grid gap-5 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
        {/* 大图（不做缩略图，直接引图床 URL，D84） */}
        <Card className="overflow-hidden">
          <CardContent className="flex items-center justify-center bg-muted/30 p-4">
            <img
              src={upload.URL}
              alt={displayName}
              className="max-h-[70svh] w-auto max-w-full object-contain"
            />
          </CardContent>
        </Card>

        {/* 元数据 */}
        <Card>
          <CardHeader>
            <CardTitle className="text-base">{t('GALLERY_DETAIL_META')}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-0">
            <MetaRow label={t('GALLERY_COL_STATUS')}>
              <StatusBadge status={upload.Status} />
            </MetaRow>
            <MetaRow label={t('GALLERY_COL_NAME')}>{upload.FileName}</MetaRow>
            {upload.OriginalName ? (
              <MetaRow label={t('GALLERY_DETAIL_ORIGINAL')}>{upload.OriginalName}</MetaRow>
            ) : null}
            {upload.AliasName ? (
              <MetaRow label={t('GALLERY_RENAME_LABEL')}>{upload.AliasName}</MetaRow>
            ) : null}
            <MetaRow label={t('GALLERY_COL_SIZE')}>{formatBytes(upload.Size)}</MetaRow>
            <MetaRow label={t('GALLERY_COL_DIMENSIONS')}>
              {formatDimensions(upload.Width, upload.Height)}
            </MetaRow>
            <MetaRow label={t('GALLERY_DETAIL_MIME')}>{upload.MimeType || '—'}</MetaRow>
            <MetaRow label={t('GALLERY_COL_STORAGE')}>
              {upload.StorageName || upload.StorageUID || '—'}
            </MetaRow>
            <MetaRow label={t('GALLERY_DETAIL_SOURCE')}>{upload.Source}</MetaRow>
            <MetaRow label={t('GALLERY_COL_CREATED')}>{formatDateTime(upload.CreatedAt)}</MetaRow>
            {upload.Error ? (
              <MetaRow label={t('GALLERY_DETAIL_ERROR')}>
                <span className="text-destructive">{upload.Error}</span>
              </MetaRow>
            ) : null}

            <Separator className="my-3" />

            <div className="space-y-1">
              <Label className="text-xs text-muted-foreground">{t('GALLERY_DETAIL_URL')}</Label>
              <div className="flex items-start gap-2">
                <code className="min-w-0 flex-1 break-all rounded bg-muted px-2 py-1 text-xs">
                  {upload.URL}
                </code>
                <Button asChild variant="ghost" size="sm" className="shrink-0">
                  <a href={upload.URL} target="_blank" rel="noreferrer noopener">
                    <ExternalLink aria-hidden />
                  </a>
                </Button>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      <RenameUploadDialog
        open={renaming}
        onOpenChange={setRenaming}
        upload={upload}
        onRenamed={refresh}
      />

      <ConfirmDialog
        open={deleting}
        onOpenChange={(open) => {
          setDeleting(open)
          if (!open) setDeleteRemote(false)
        }}
        title={t('GALLERY_DELETE_TITLE', { count: 1 })}
        description={t('GALLERY_DELETE_DESC')}
        confirmLabel={t('COMMON_DELETE')}
        destructive
        onConfirm={confirmDelete}
      >
        <div className="flex items-start justify-between gap-3 rounded-md border border-border px-3 py-2.5">
          <div className="space-y-0.5">
            <Label htmlFor="detail-delete-remote">{t('GALLERY_DELETE_REMOTE')}</Label>
            <p className="text-xs text-muted-foreground">{t('GALLERY_DELETE_REMOTE_HINT')}</p>
          </div>
          <Switch
            id="detail-delete-remote"
            checked={deleteRemote}
            onCheckedChange={setDeleteRemote}
          />
        </div>
      </ConfirmDialog>
    </>
  )
}

function MetaRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 py-2">
      <span className="shrink-0 text-sm text-muted-foreground">{label}</span>
      <span className="min-w-0 break-all text-right text-sm text-foreground">{children}</span>
    </div>
  )
}
