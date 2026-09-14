import { ChevronLeft, ChevronRight, ExternalLink, Info } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Link } from 'react-router'

import { LinkCopyMenu } from '@/components/common/link-copy-menu'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { IconButton } from '@/components/ui/icon-button'
import { t } from '@/i18n'
import { formatBytes, formatDateTime, formatDimensions } from '@/lib/format'
import type { Upload } from '@/types/api'

/**
 * 灯箱预览（DESIGN.md §5.2）。
 *
 * 交互：
 *  - 点击卡片 → 打开灯箱（**而非跳页**）
 *  - `←` / `→` 切换（边界循环）
 *  - `Esc` 关闭（Dialog 自带）
 *  - 详情入口在灯箱内（跳 `/gallery/:UID`）
 *
 * ⚠️ 大图直接用 `URL`（不做缩略图，D84）；`onError` 时给出「打不开原链接」的提示。
 */
export interface ImageLightboxProps {
  items: Upload[]
  /** `-1` = 关闭 */
  index: number
  onIndexChange: (index: number) => void
  onClose: () => void
  /** 是否显示上传者（管理员「全部图片」Tab） */
  showUploader?: boolean
}

export function ImageLightbox({
  items,
  index,
  onIndexChange,
  onClose,
  showUploader = false,
}: ImageLightboxProps) {
  const [broken, setBroken] = useState(false)
  const open = index >= 0 && index < items.length
  const current = open ? items[index] : undefined

  // 切换图片时重置「加载失败」标记
  useEffect(() => {
    setBroken(false)
  }, [index])

  // 方向键切换（DESIGN.md §9.1）
  useEffect(() => {
    if (!open) return

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'ArrowLeft') {
        event.preventDefault()
        onIndexChange((index - 1 + items.length) % items.length)
      } else if (event.key === 'ArrowRight') {
        event.preventDefault()
        onIndexChange((index + 1) % items.length)
      }
    }

    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [open, index, items.length, onIndexChange])

  if (!current) return null

  const displayName = current.AliasName || current.OriginalName || current.FileName

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent className="max-w-5xl gap-3 p-4" aria-describedby={undefined}>
        <DialogTitle className="sr-only">{displayName}</DialogTitle>

        {/* 图片区 */}
        <div className="relative flex max-h-[70svh] items-center justify-center overflow-hidden rounded-md bg-muted/40">
          {broken ? (
            <div className="flex flex-col items-center gap-2 p-10 text-center">
              <p className="text-sm text-muted-foreground">{t('LIGHTBOX_LOAD_FAILED')}</p>
              <Button asChild variant="outline" size="sm">
                <a href={current.URL} target="_blank" rel="noreferrer noopener">
                  <ExternalLink aria-hidden />
                  {t('GALLERY_OPEN_ORIGIN')}
                </a>
              </Button>
            </div>
          ) : (
            <img
              src={current.URL}
              alt={displayName}
              onError={() => setBroken(true)}
              className="max-h-[70svh] w-auto max-w-full object-contain"
            />
          )}

          {items.length > 1 ? (
            <>
              <IconButton
                label={t('LIGHTBOX_PREV')}
                className="absolute left-2 top-1/2 -translate-y-1/2 bg-background/80 shadow-sm"
                onClick={() => onIndexChange((index - 1 + items.length) % items.length)}
              >
                <ChevronLeft aria-hidden />
              </IconButton>
              <IconButton
                label={t('LIGHTBOX_NEXT')}
                className="absolute right-2 top-1/2 -translate-y-1/2 bg-background/80 shadow-sm"
                onClick={() => onIndexChange((index + 1) % items.length)}
              >
                <ChevronRight aria-hidden />
              </IconButton>
            </>
          ) : null}
        </div>

        {/* 信息条 */}
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5">
          <span className="min-w-0 flex-1 truncate text-sm font-medium text-foreground" title={displayName}>
            {displayName}
          </span>

          <span className="text-xs text-muted-foreground">
            {formatBytes(current.Size)} · {formatDimensions(current.Width, current.Height)}
          </span>

          <span className="text-xs text-muted-foreground">{formatDateTime(current.CreatedAt)}</span>

          {showUploader && current.UserEmail ? (
            <Badge variant="secondary">{current.UserEmail}</Badge>
          ) : null}

          <span className="text-xs text-muted-foreground">
            {index + 1} / {items.length}
          </span>

          <div className="ml-auto flex items-center gap-2">
            <LinkCopyMenu
              items={[{ URL: current.URL, Name: displayName }]}
              label={t('LINK_COPY_LABEL')}
            />
            <Button asChild variant="outline" size="sm">
              <Link to={`/gallery/${encodeURIComponent(current.UID)}`}>
                <Info aria-hidden />
                {t('GALLERY_DETAIL')}
              </Link>
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
