import { CheckSquare, Square } from 'lucide-react'
import { useState } from 'react'

import { t } from '@/i18n'
import { cn } from '@/lib/utils'

/**
 * 图片卡片（DESIGN.md §5.2）。
 *
 * ⚠️ **不做缩略图**（D84）：
 *  - 直接 `<img src={Upload.URL}>`，**不依赖 `ThumbURL`**（后者保留但不使用，D77）
 *  - 用 `aspect-ratio` + 数据里的 `Width`/`Height` **预留占位**，避免懒加载导致布局跳动
 *  - 图床慢 / 不可达时 `onError` 显示占位 + 「打开原链接」，**不是空白**
 *
 * `alt` 用 `AliasName || OriginalName`（DESIGN.md §12 a11y）。
 */
export interface ImageCardProps {
  url: string
  fileName: string
  /** 原始文件名（`alt` 的回退来源） */
  alt?: string
  width?: number
  height?: number
  /** 勾选态 */
  selected?: boolean
  /** 是否显示勾选框（多选模式） */
  selectable?: boolean
  /** 管理员「全部图片」Tab 下展示上传者 */
  uploaderLabel?: string
  onToggleSelect?: () => void
  /** `shiftKey` 为真时表示「连选」而不是打开（DESIGN.md §5.2） */
  onOpen?: (shiftKey: boolean) => void
  /** 卡片右下角的附加内容（如时间） */
  footer?: string
  className?: string
}

export function ImageCard({
  url,
  fileName,
  alt,
  width = 0,
  height = 0,
  selected = false,
  selectable = true,
  uploaderLabel,
  onToggleSelect,
  onOpen,
  footer,
  className,
}: ImageCardProps) {
  const [broken, setBroken] = useState(false)

  // 用真实宽高算比例；缺失时退化为 4:3（常见截图比例），保证网格稳定
  const ratio = width > 0 && height > 0 ? `${width} / ${height}` : '4 / 3'

  return (
    <figure
      className={cn(
        'group relative overflow-hidden rounded-lg border border-border bg-muted/30',
        'transition-shadow duration-150 hover:shadow-md',
        selected && 'ring-2 ring-brand',
        className,
      )}
    >
      <button
        type="button"
        onClick={(event) => onOpen?.(event.shiftKey)}
        className="block w-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
        aria-label={t('GALLERY_OPEN_IMAGE', { name: alt || fileName })}
      >
        {broken ? (
          // 图床不可达时的降级：给出明确入口而不是空白
          <span
            className="flex w-full flex-col items-center justify-center gap-1 bg-muted px-2 text-center"
            style={{ aspectRatio: ratio }}
          >
            <span className="line-clamp-2 break-all text-xs text-muted-foreground">{fileName}</span>
            <span className="text-xs text-brand underline" role="link">
              {t('GALLERY_OPEN_ORIGIN')}
            </span>
          </span>
        ) : (
          <img
            src={url}
            alt={alt || fileName}
            loading="lazy"
            decoding="async"
            onError={() => setBroken(true)}
            className="w-full bg-muted object-cover"
            style={{ aspectRatio: ratio }}
          />
        )}
      </button>

      {/* 勾选框（左上） */}
      {selectable ? (
        <button
          type="button"
          onClick={(event) => {
            event.stopPropagation()
            onToggleSelect?.()
          }}
          aria-label={t('GALLERY_TOGGLE_SELECT')}
          aria-pressed={selected}
          className={cn(
            'absolute left-1.5 top-1.5 rounded bg-background/85 p-0.5 shadow-sm transition-opacity',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
            selected ? 'opacity-100' : 'opacity-0 group-hover:opacity-100 focus-visible:opacity-100',
          )}
        >
          {selected ? (
            <CheckSquare className="size-4 text-brand" aria-hidden />
          ) : (
            <Square className="size-4 text-muted-foreground" aria-hidden />
          )}
        </button>
      ) : null}

      {/* 上传者（管理员「全部图片」Tab） */}
      {uploaderLabel ? (
        <span className="absolute right-1.5 top-1.5 max-w-[70%] truncate rounded bg-background/85 px-1.5 py-0.5 text-[11px] text-foreground shadow-sm">
          {uploaderLabel}
        </span>
      ) : null}

      {/* 底部信息条 */}
      <figcaption className="flex items-center justify-between gap-1 px-2 py-1.5">
        <span className="truncate text-xs text-foreground" title={fileName}>
          {fileName}
        </span>
        {footer ? <span className="shrink-0 text-[11px] text-muted-foreground">{footer}</span> : null}
      </figcaption>
    </figure>
  )
}
