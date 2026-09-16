import { Toaster as SonnerToaster, toast } from 'sonner'

import { useUIStore } from '@/store/ui-store'

/**
 * Toast 封装（sonner）。
 *
 * - `toast.success` 用于「操作成功」的轻提示（DESIGN.md §9.3：2s）
 * - 重要结果（如上传拿到外链）用带操作的 Toast（`action`）
 * - 与主题联动：跟随当前亮/暗色
 */
export { toast }

export function Toaster() {
  const theme = useUIStore((state) => state.theme)

  return (
    <SonnerToaster
      // sonner 的 theme 只接受这三个字面量，与我们的 ThemeMode 同名，可直接传
      theme={theme}
      position="bottom-right"
      // 最多同时显示 3 条，超出的叠放在第 3 条位置（sonner 默认层级叠放）
      visibleToasts={3}
      closeButton
      richColors={false}
      toastOptions={{
        classNames: {
          toast:
            'rounded-lg border border-border bg-popover text-popover-foreground shadow-lg text-sm',
          description: 'text-muted-foreground',
          actionButton: 'bg-brand text-brand-foreground',
          cancelButton: 'bg-muted text-muted-foreground',
        },
      }}
    />
  )
}

