import { ChevronLeft, ChevronRight } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

/**
 * 分页控件。
 *
 * 只做「上一页 / 页码 / 下一页」，够用且不引入额外依赖。
 * 页码较多时用省略号折叠。
 */
export interface PaginationProps {
  page: number
  pageSize: number
  total: number
  onPageChange: (page: number) => void
  className?: string
}

/** 生成要展示的页码（含省略号用 `null` 表示）。 */
function buildPages(current: number, totalPages: number): (number | null)[] {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, i) => i + 1)
  }

  const pages: (number | null)[] = [1]
  const start = Math.max(2, current - 1)
  const end = Math.min(totalPages - 1, current + 1)

  if (start > 2) pages.push(null)
  for (let p = start; p <= end; p += 1) pages.push(p)
  if (end < totalPages - 1) pages.push(null)

  pages.push(totalPages)
  return pages
}

export function Pagination({ page, pageSize, total, onPageChange, className }: PaginationProps) {
  const totalPages = Math.max(1, Math.ceil(total / Math.max(1, pageSize)))
  const current = Math.min(Math.max(1, page), totalPages)
  const pages = buildPages(current, totalPages)

  return (
    <nav
      aria-label={t('PAGINATION_LABEL')}
      className={cn('flex items-center justify-between gap-3', className)}
    >
      <p className="text-xs text-muted-foreground">
        {t('PAGINATION_SUMMARY', { total, page: current, pages: totalPages })}
      </p>

      <div className="flex items-center gap-1">
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={current <= 1}
          onClick={() => onPageChange(current - 1)}
          aria-label={t('PAGINATION_PREV')}
        >
          <ChevronLeft aria-hidden />
        </Button>

        {pages.map((p, index) =>
          p === null ? (
            <span key={`gap-${index}`} className="px-1 text-xs text-muted-foreground">
              …
            </span>
          ) : (
            <Button
              key={p}
              type="button"
              variant={p === current ? 'default' : 'ghost'}
              size="sm"
              aria-current={p === current ? 'page' : undefined}
              onClick={() => onPageChange(p)}
              className="min-w-8"
            >
              {p}
            </Button>
          ),
        )}

        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={current >= totalPages}
          onClick={() => onPageChange(current + 1)}
          aria-label={t('PAGINATION_NEXT')}
        >
          <ChevronRight aria-hidden />
        </Button>
      </div>
    </nav>
  )
}
