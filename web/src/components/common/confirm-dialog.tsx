import { Loader2 } from 'lucide-react'
import { useState, type ReactNode } from 'react'

import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { toApiError } from '@/types/api'

/**
 * 异步确认对话框（DESIGN.md §9.3「特殊」条款）。
 *
 * ⚠️ **不能用 `AlertDialogAction`**：它在点击后会立即关闭对话框，
 * 无论是否 `preventDefault()`。而破坏性操作通常需要「等 API 返回后再关」，
 * 否则失败时对话框已经消失、用户看不到原因。
 *
 * 因此这里用普通 `Button` + 手动控制 `open`，**成功后才关闭**。
 *
 * 用法：
 *   <ConfirmDialog
 *     open={open}
 *     onOpenChange={setOpen}
 *     title="删除图片"
 *     description="此操作不可撤销"
 *     confirmLabel="删除"
 *     destructive
 *     onConfirm={async () => { await api.remove(uid) }}
 *     onConfirmed={() => refresh()}
 *   />
 */
export interface ConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: ReactNode
  confirmLabel?: string
  cancelLabel?: string
  /** 用破坏性配色（删除类操作） */
  destructive?: boolean
  /** 异步动作；抛错时会展示错误且**不关闭**对话框 */
  onConfirm: () => Promise<void> | void
  /** 成功后调用（通常在关闭之后刷新列表） */
  onConfirmed?: () => void
  /** 额外的确认区内容（如「同时删除远端文件」开关） */
  children?: ReactNode
  /** 确认按钮禁用（例如用户未勾选必要项） */
  confirmDisabled?: boolean
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  cancelLabel,
  destructive = false,
  onConfirm,
  onConfirmed,
  children,
  confirmDisabled = false,
}: ConfirmDialogProps) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const handleConfirm = async () => {
    setBusy(true)
    setError('')
    try {
      await onConfirm()
      onOpenChange(false)
      onConfirmed?.()
    } catch (err) {
      // 失败时**保持对话框打开**，把原因展示出来
      setError(toApiError(err).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (busy) return // 提交中不允许关闭，避免动作半途而废
        if (!next) setError('')
        onOpenChange(next)
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          {description ? <AlertDialogDescription>{description}</AlertDialogDescription> : null}
        </AlertDialogHeader>

        {children ? <div className="space-y-3">{children}</div> : null}

        {error ? (
          <p className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>
        ) : null}

        <AlertDialogFooter>
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => onOpenChange(false)}
          >
            {cancelLabel ?? t('COMMON_CANCEL')}
          </Button>
          {/* ⚠️ 用普通 Button，不用 AlertDialogAction（见文件头说明） */}
          <Button
            type="button"
            variant={destructive ? 'destructive' : 'brand'}
            disabled={busy || confirmDisabled}
            onClick={() => void handleConfirm()}
          >
            {busy ? <Loader2 className="size-4 animate-spin" aria-hidden /> : null}
            {confirmLabel ?? t('COMMON_CONFIRM')}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
