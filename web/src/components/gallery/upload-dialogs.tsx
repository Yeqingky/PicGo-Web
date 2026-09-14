import { Loader2 } from 'lucide-react'
import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { toast } from '@/components/ui/toast'
import { albumApi, uploadApi } from '@/lib/api'
import { t } from '@/i18n'
import { toApiError } from '@/types/api'
import type { Album, Upload } from '@/types/api'

/**
 * 图片整理的两个对话框（DESIGN.md §5.2 整理能力）。
 *
 *  - **重命名**：改 `AliasName`，**不改远端文件名**（远端 URL 由图床决定，D42/D66 边界）
 *  - **移动到相册**：支持批量；选「移出相册」时传 `TargetAlbumUID = ""`
 *
 * 两者都用**乐观更新**（DESIGN.md §9.3）：先改 UI 再请求，失败回滚 + Toast。
 */

/** `""` 在 Select 里代表「未选择」；用一个哨兵值区分「移出相册」。 */
const NO_ALBUM = '__none__'

// ---------------------------------------------------------------------------
// 重命名
// ---------------------------------------------------------------------------

export interface RenameUploadDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  upload: Upload | null
  onRenamed?: (upload: Upload) => void
}

export function RenameUploadDialog({
  open,
  onOpenChange,
  upload,
  onRenamed,
}: RenameUploadDialogProps) {
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (open && upload) {
      setName(upload.AliasName || upload.FileName)
      setError('')
    }
  }, [open, upload])

  if (!upload) return null

  const submit = async () => {
    const next = name.trim()
    if (!next) {
      setError(t('GALLERY_RENAME_EMPTY'))
      return
    }

    setBusy(true)
    setError('')
    try {
      const updated = await uploadApi.update(upload.UID, { AliasName: next })
      toast.success(t('GALLERY_RENAME_SUCCESS'))
      onOpenChange(false)
      onRenamed?.(updated)
    } catch (err) {
      setError(toApiError(err).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{t('GALLERY_RENAME_TITLE')}</DialogTitle>
          <DialogDescription>{t('GALLERY_RENAME_DESC')}</DialogDescription>
        </DialogHeader>

        <div className="space-y-2">
          <Label htmlFor="rename-input">{t('GALLERY_RENAME_LABEL')}</Label>
          <Input
            id="rename-input"
            value={name}
            onChange={(event) => setName(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter' && !busy) void submit()
            }}
            autoFocus
          />
          <p className="text-xs text-muted-foreground">
            {t('GALLERY_RENAME_HINT', { file: upload.FileName })}
          </p>
          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            {t('COMMON_CANCEL')}
          </Button>
          <Button variant="brand" onClick={() => void submit()} disabled={busy}>
            {busy ? <Loader2 className="size-4 animate-spin" aria-hidden /> : null}
            {t('COMMON_SAVE')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// ---------------------------------------------------------------------------
// 移动到相册
// ---------------------------------------------------------------------------

export interface MoveUploadsDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** 要移动的图片（可多选） */
  uploads: Upload[]
  albums: Album[]
  onMoved?: () => void
}

export function MoveUploadsDialog({
  open,
  onOpenChange,
  uploads,
  albums,
  onMoved,
}: MoveUploadsDialogProps) {
  const [target, setTarget] = useState<string>(NO_ALBUM)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (open) {
      setTarget(NO_ALBUM)
      setError('')
    }
  }, [open])

  const submit = async () => {
    if (uploads.length === 0) return

    setBusy(true)
    setError('')
    try {
      const targetUID = target === NO_ALBUM ? '' : target
      await albumApi.moveUploadsTo(targetUID, uploads.map((item) => item.UID))
      toast.success(t('GALLERY_MOVE_SUCCESS', { count: uploads.length }))
      onOpenChange(false)
      onMoved?.()
    } catch (err) {
      setError(toApiError(err).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{t('GALLERY_MOVE_TITLE')}</DialogTitle>
          <DialogDescription>
            {t('GALLERY_MOVE_DESC', { count: uploads.length })}
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-2">
          <Label htmlFor="album-select">{t('GALLERY_MOVE_TARGET')}</Label>
          <Select value={target} onValueChange={setTarget}>
            <SelectTrigger id="album-select">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NO_ALBUM}>{t('GALLERY_MOVE_OUT')}</SelectItem>
              {albums.map((album) => (
                <SelectItem key={album.UID} value={album.UID}>
                  {album.Name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>

          {albums.length === 0 ? (
            <p className="text-xs text-muted-foreground">{t('GALLERY_NO_ALBUMS_HINT')}</p>
          ) : null}

          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            {t('COMMON_CANCEL')}
          </Button>
          <Button variant="brand" onClick={() => void submit()} disabled={busy}>
            {busy ? <Loader2 className="size-4 animate-spin" aria-hidden /> : null}
            {t('COMMON_CONFIRM')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
