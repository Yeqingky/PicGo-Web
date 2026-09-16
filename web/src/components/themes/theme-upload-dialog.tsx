import { AlertTriangle, Loader2, UploadCloud } from 'lucide-react'
import { useRef, useState } from 'react'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Switch } from '@/components/ui/switch'
import { Label } from '@/components/ui/label'
import { toast } from '@/components/ui/toast'
import { useThemeInstall } from '@/hooks/api'
import { t } from '@/i18n'
import { formatBytes } from '@/lib/format'
import type { ThemeInstallResponse } from '@/types/api'

/**
 * 主题上传对话框（DESIGN.md §5.8.1 / D96）。
 *
 * 后端会做**全套安全校验**（防 Zip Slip / 拒绝符号链接 / 体积与文件数上限 /
 * manifest 校验 / 冲突检测 / 原子性），失败时返回具体原因。
 * 本对话框把后端 `Message` **原样展示**（它已逐条说明是哪个校验没过），
 * 而不是笼统地说「上传失败」—— 这样管理员才知道该改什么。
 */
export interface ThemeUploadDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onInstalled?: (result: ThemeInstallResponse) => void
}

/** 默认包体上限提示（与后端 `theme.maxPackageBytes` 默认值一致）。 */
const DEFAULT_MAX_BYTES = 64 * 1024 * 1024

export function ThemeUploadDialog({ open, onOpenChange, onInstalled }: ThemeUploadDialogProps) {
  const { file, setFile, overwrite, setOverwrite, submitting, error, submit, reset } =
    useThemeInstall()
  const inputRef = useRef<HTMLInputElement>(null)
  const [localError, setLocalError] = useState('')

  const handleFile = (files: FileList | null) => {
    const picked = files?.[0]
    if (!picked) return

    if (!picked.name.toLowerCase().endsWith('.zip')) {
      setLocalError(t('THEME_UPLOAD_ONLY_ZIP'))
      return
    }
    if (picked.size > DEFAULT_MAX_BYTES) {
      setLocalError(t('THEME_UPLOAD_TOO_LARGE', { max: formatBytes(DEFAULT_MAX_BYTES) }))
      return
    }

    setLocalError('')
    setFile(picked)
  }

  const handleSubmit = async () => {
    const result = await submit()
    if (!result) return

    toast.success(t('THEME_UPLOAD_SUCCESS', { name: result.Name || result.ID }))
    reset()
    onOpenChange(false)
    onInstalled?.(result)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          reset()
          setLocalError('')
        }
        onOpenChange(next)
      }}
    >
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('THEME_UPLOAD_TITLE')}</DialogTitle>
          <DialogDescription>{t('THEME_UPLOAD_DESC')}</DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          {/* 安全提示（D96：主题 = 服务器上的任意前端代码） */}
          <Alert variant="warning">
            <AlertTriangle aria-hidden />
            <AlertDescription>{t('THEME_UPLOAD_SECURITY_NOTE')}</AlertDescription>
          </Alert>

          {/* 文件选择 */}
          <div className="space-y-2">
            <Label htmlFor="theme-zip">{t('THEME_UPLOAD_FILE_LABEL')}</Label>
            <input
              ref={inputRef}
              id="theme-zip"
              type="file"
              accept=".zip,application/zip"
              className="sr-only"
              onChange={(event) => handleFile(event.target.files)}
            />
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="outline"
                onClick={() => inputRef.current?.click()}
                disabled={submitting}
              >
                <UploadCloud aria-hidden />
                {t('THEME_UPLOAD_PICK')}
              </Button>
              <span className="min-w-0 flex-1 truncate text-sm text-muted-foreground">
                {file ? `${file.name}（${formatBytes(file.size)}）` : t('THEME_UPLOAD_NONE')}
              </span>
            </div>
            <p className="text-xs text-muted-foreground">
              {t('THEME_UPLOAD_SIZE_HINT', { max: formatBytes(DEFAULT_MAX_BYTES) })}
            </p>
          </div>

          {/* 覆盖开关 */}
          <div className="flex items-center justify-between gap-3 rounded-md border border-border px-3 py-2.5">
            <div className="space-y-0.5">
              <Label htmlFor="theme-overwrite">{t('THEME_UPLOAD_OVERWRITE')}</Label>
              <p className="text-xs text-muted-foreground">{t('THEME_UPLOAD_OVERWRITE_HINT')}</p>
            </div>
            <Switch
              id="theme-overwrite"
              checked={overwrite}
              onCheckedChange={setOverwrite}
              disabled={submitting}
            />
          </div>

          {/* 错误：后端会给出逐条校验原因，原样展示 */}
          {localError || error ? (
            <Alert variant="destructive">
              <AlertDescription className="whitespace-pre-wrap">
                {localError || error}
              </AlertDescription>
            </Alert>
          ) : null}
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            disabled={submitting}
            onClick={() => onOpenChange(false)}
          >
            {t('COMMON_CANCEL')}
          </Button>
          <Button
            type="button"
            variant="brand"
            disabled={submitting || !file}
            onClick={() => void handleSubmit()}
          >
            {submitting ? <Loader2 className="size-4 animate-spin" aria-hidden /> : null}
            {t('THEME_UPLOAD_SUBMIT')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
