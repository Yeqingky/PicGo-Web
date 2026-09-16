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
import { toast } from '@/components/ui/toast'
import { uploadApi } from '@/lib/api'
import { t } from '@/i18n'
import { toApiError } from '@/types/api'
import type { Upload } from '@/types/api'

/**
 * 图片重命名对话框（DESIGN.md §5.2 整理能力）。
 *
 * 改 `AliasName`，**不改远端文件名**（远端 URL 由图床决定，D42/D66 边界）。
 * 用**乐观更新**（DESIGN.md §9.3）：先改 UI 再请求，失败回滚 + Toast。
 */

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
