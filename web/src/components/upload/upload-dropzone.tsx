import { ImagePlus, UploadCloud } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'

import { t } from '@/i18n'
import { cn } from '@/lib/utils'

/**
 * 上传接收区（DESIGN.md §5.1 / §9.2）。
 *
 * 三种输入方式：
 *  1. **点击**选择文件
 *  2. **拖拽**到区域内
 *  3. **`Ctrl/⌘+V` 粘贴** —— 支持剪贴板里的图片，也支持从文件管理器复制文件后粘贴
 *
 * ⚠️ 不做任何上传动作：只负责**把 File[] 交给调用方**（`store/upload-store.ts` 入队）。
 *    这样首页（主题）与 `/upload` 页可以复用同一个组件但各自决定后续行为。
 */

import { DEFAULT_ACCEPT, filesFromClipboard, filesFromDataTransfer } from '@/lib/dropzone'

export interface UploadDropzoneProps {
  onFiles: (files: File[]) => void
  /** 接受的文件类型（`<input accept>` 语法） */
  accept?: string
  multiple?: boolean
  disabled?: boolean
  /** 紧凑模式（用于首页 Hero 等空间受限处） */
  compact?: boolean
  className?: string
}

export function UploadDropzone({
  onFiles,
  accept = DEFAULT_ACCEPT,
  multiple = true,
  disabled = false,
  compact = false,
  className,
}: UploadDropzoneProps) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [dragging, setDragging] = useState(false)
  /** 拖拽计数：解决「dragenter/dragleave 在子元素间反复触发」导致的闪烁 */
  const dragDepth = useRef(0)

  const pick = useCallback(
    (files: File[]) => {
      if (disabled || files.length === 0) return
      onFiles(files)
    },
    [disabled, onFiles],
  )

  const handleDrop = (event: React.DragEvent) => {
    event.preventDefault()
    dragDepth.current = 0
    setDragging(false)
    pick(filesFromDataTransfer(event.dataTransfer))
  }

  return (
    <div
      onDragEnter={(event) => {
        event.preventDefault()
        dragDepth.current += 1
        if (!disabled) setDragging(true)
      }}
      onDragOver={(event) => {
        event.preventDefault()
      }}
      onDragLeave={(event) => {
        event.preventDefault()
        dragDepth.current = Math.max(0, dragDepth.current - 1)
        if (dragDepth.current === 0) setDragging(false)
      }}
      onDrop={handleDrop}
      className={cn(
        'relative flex flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed transition-colors',
        compact ? 'px-4 py-8' : 'px-6 py-14',
        dragging ? 'border-brand bg-brand/5' : 'border-border bg-muted/20',
        disabled && 'cursor-not-allowed opacity-60',
        className,
      )}
    >
      <input
        ref={inputRef}
        type="file"
        accept={accept}
        multiple={multiple}
        disabled={disabled}
        className="sr-only"
        onChange={(event) => {
          const files = event.target.files ? Array.from(event.target.files) : []
          pick(files)
          // 重置 input，保证「同一个文件再选一次」也能触发 change
          event.target.value = ''
        }}
      />

      <UploadCloud
        className={cn('text-muted-foreground', compact ? 'size-8' : 'size-10')}
        aria-hidden
      />

      <button
        type="button"
        disabled={disabled}
        onClick={() => inputRef.current?.click()}
        className="inline-flex items-center gap-1.5 text-sm font-medium text-foreground hover:text-brand focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none"
      >
        <ImagePlus className="size-4" aria-hidden />
        {t('UPLOAD_DROPZONE_CLICK')}
      </button>

      <p className="text-xs text-muted-foreground">{t('UPLOAD_DROPZONE_HINT')}</p>
      <p className="text-xs text-muted-foreground">{t('UPLOAD_DROPZONE_FORMATS')}</p>

      {dragging ? (
        <span className="pointer-events-none absolute inset-0 rounded-lg bg-brand/5" aria-hidden />
      ) : null}
    </div>
  )
}

// ---------------------------------------------------------------------------
// 整页拖拽遮罩 + 全局粘贴
// ---------------------------------------------------------------------------

/**
 * 全局拖拽遮罩与粘贴上传（DESIGN.md §9.2）。
 *
 * 「拖入窗口任意位置显示整页虚线遮罩：松开即可上传」——
 * 因此遮罩挂在 `window` 上，而不是某个容器里。
 *
 * ⚠️ 粘贴只在**没有输入焦点**时生效：否则用户往搜索框粘贴文字会被当作上传。
 *    （图片粘贴时 `clipboardData` 里只有文件，不影响文本粘贴；这里保守处理，
 *     仅当剪贴板里确实含文件时才拦截。）
 */
export interface GlobalUploadListenerProps {
  onFiles: (files: File[]) => void
  /** 为真时启用（未登录时可以传 false 让用户先去登录） */
  enabled?: boolean
  /** 拖拽遮罩里的文案（首页与上传页可不同） */
  overlayText?: string
}

export function GlobalUploadListener({
  onFiles,
  enabled = true,
  overlayText,
}: GlobalUploadListenerProps) {
  const [dragging, setDragging] = useState(false)
  const dragDepth = useRef(0)

  useEffect(() => {
    if (!enabled) return

    const onDragEnter = (event: DragEvent) => {
      if (!event.dataTransfer) return
      // 只在拖入「文件」时接管，避免干扰页面内的文字拖拽
      if (!Array.from(event.dataTransfer.types).includes('Files')) return
      event.preventDefault()
      dragDepth.current += 1
      setDragging(true)
    }

    const onDragOver = (event: DragEvent) => {
      if (!event.dataTransfer) return
      if (!Array.from(event.dataTransfer.types).includes('Files')) return
      // 必须 preventDefault，否则浏览器会直接打开文件
      event.preventDefault()
      event.dataTransfer.dropEffect = 'copy'
    }

    const onDragLeave = (event: DragEvent) => {
      event.preventDefault()
      dragDepth.current = Math.max(0, dragDepth.current - 1)
      if (dragDepth.current === 0) setDragging(false)
    }

    const onDrop = (event: DragEvent) => {
      if (!event.dataTransfer) return
      if (!Array.from(event.dataTransfer.types).includes('Files')) return
      event.preventDefault()
      dragDepth.current = 0
      setDragging(false)
      onFiles(filesFromDataTransfer(event.dataTransfer))
    }

    const onPaste = (event: ClipboardEvent) => {
      const files = filesFromClipboard(event)
      if (files.length === 0) return
      // 剪贴板里含文件 → 这是「粘贴上传」，拦截默认行为
      event.preventDefault()
      onFiles(files)
    }

    window.addEventListener('dragenter', onDragEnter)
    window.addEventListener('dragover', onDragOver)
    window.addEventListener('dragleave', onDragLeave)
    window.addEventListener('drop', onDrop)
    window.addEventListener('paste', onPaste)

    return () => {
      window.removeEventListener('dragenter', onDragEnter)
      window.removeEventListener('dragover', onDragOver)
      window.removeEventListener('dragleave', onDragLeave)
      window.removeEventListener('drop', onDrop)
      window.removeEventListener('paste', onPaste)
    }
  }, [enabled, onFiles])

  if (!dragging) return null

  return (
    <div
      className="fixed inset-0 z-40 flex items-center justify-center bg-background/70 backdrop-blur-sm"
      aria-hidden
    >
      <div className="flex flex-col items-center gap-3 rounded-xl border-2 border-dashed border-brand bg-background/90 px-10 py-8 shadow-lg">
        <UploadCloud className="size-10 text-brand" aria-hidden />
        <p className="text-sm font-medium text-foreground">
          {overlayText ?? t('UPLOAD_DROP_ANYWHERE')}
        </p>
      </div>
    </div>
  )
}
