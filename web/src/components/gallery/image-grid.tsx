import { ImageCard } from '@/components/gallery/image-card'
import type { Upload } from '@/types/api'
import { formatRelativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'

/**
 * 图库网格 / 瀑布流（DESIGN.md §5.2）。
 *
 * 为什么用 **CSS columns（瀑布流）** 而不是等高网格：
 *  - 本项目**不做缩略图**（D84），加载的是原图，宽高比差异很大；
 *    等高网格会把竖图裁得很难看，CSS columns 能保留原始比例
 *  - 代价是**阅读顺序是「先满一列再下一列」**（列优先），不是行优先。
 *    分页场景下这一点可接受（用户按「共 N 条 / 第 x 页」理解列表，
 *    而不是靠视觉扫描顺序）；无限滚动才会把这个缺点放大 —— 这也是选分页的原因之一
 *
 * 列数按 `DESIGN.md §2.4` 的断点：默认 2 / sm 3 / lg 4 / xl 5 / 2xl 6。
 */
export interface ImageGridProps {
  items: Upload[]
  selectedUIDs: string[]
  /** 显示上传者（仅管理员「全部图片」Tab） */
  showUploader?: boolean
  onToggleSelect: (uid: string) => void
  /** `shiftKey` 为真时做连选（由页面提供已加载列表的顺序） */
  onSelect: (uid: string, shiftKey: boolean) => void
  onOpen: (uid: string) => void
  className?: string
}

export function ImageGrid({
  items,
  selectedUIDs,
  showUploader = false,
  onToggleSelect,
  onSelect,
  onOpen,
  className,
}: ImageGridProps) {
  return (
    <div
      className={cn(
        'columns-2 gap-3 sm:columns-3 lg:columns-4 xl:columns-5 2xl:columns-6',
        className,
      )}
      role="list"
      aria-label="图片列表"
    >
      {items.map((upload) => (
        <div key={upload.UID} className="mb-3 break-inside-avoid" role="listitem">
          <ImageCard
            url={upload.URL}
            fileName={upload.FileName}
            alt={upload.AliasName || upload.OriginalName || upload.FileName}
            width={upload.Width}
            height={upload.Height}
            selected={selectedUIDs.includes(upload.UID)}
            uploaderLabel={showUploader ? upload.UserEmail || undefined : undefined}
            footer={formatRelativeTime(upload.CreatedAt)}
            onToggleSelect={() => onToggleSelect(upload.UID)}
            onOpen={(shiftKey) => {
              if (shiftKey) onSelect(upload.UID, true)
              else onOpen(upload.UID)
            }}
          />
        </div>
      ))}
    </div>
  )
}

/**
 * 点击语义（DESIGN.md §5.2）：
 *  - 普通点击 → **打开灯箱**（主要动作，而不是跳页）
 *  - `Shift + 点击` → 连选（与勾选框互补：勾选框用于零散多选，Shift 用于区间）
 *
 * 分派逻辑内联在 `onOpen` 回调里，不再单独导出函数 ——
 * 避免「有一个函数但没人用」的死代码。
 */
