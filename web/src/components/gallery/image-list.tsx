import { MoreHorizontal } from 'lucide-react'

import { LinkCopyMenu } from '@/components/common/link-copy-menu'
import { StatusBadge } from '@/components/common/status-badge'
import { Checkbox } from '@/components/ui/checkbox'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { IconButton } from '@/components/ui/icon-button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { t } from '@/i18n'
import { formatBytes, formatDateTime, formatDimensions } from '@/lib/format'
import type { Upload } from '@/types/api'

/**
 * 图库列表视图（DESIGN.md §5.2）。
 *
 * 表格列 = 文件名 / 大小 / 尺寸 / 存储 / 状态 / 时间 / 操作。
 * 管理员在「全部图片」Tab 下额外有「上传者」列（DESIGN.md §10）。
 */
export interface ImageListProps {
  items: Upload[]
  selectedUIDs: string[]
  showUploader?: boolean
  storageNames?: Record<string, string>
  onToggleSelect: (uid: string) => void
  onSelect: (uid: string, shiftKey: boolean) => void
  onOpen: (uid: string) => void
  onEdit: (upload: Upload) => void
  onDelete: (upload: Upload) => void
}

export function ImageList({
  items,
  selectedUIDs,
  showUploader = false,
  storageNames = {},
  onToggleSelect,
  onSelect,
  onOpen,
  onEdit,
  onDelete,
}: ImageListProps) {
  return (
    <div className="overflow-x-auto rounded-lg border border-border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-10" />
            <TableHead>{t('GALLERY_COL_NAME')}</TableHead>
            {showUploader ? <TableHead>{t('GALLERY_COL_UPLOADER')}</TableHead> : null}
            <TableHead>{t('GALLERY_COL_SIZE')}</TableHead>
            <TableHead>{t('GALLERY_COL_DIMENSIONS')}</TableHead>
            <TableHead>{t('GALLERY_COL_STORAGE')}</TableHead>
            <TableHead>{t('GALLERY_COL_STATUS')}</TableHead>
            <TableHead>{t('GALLERY_COL_CREATED')}</TableHead>
            <TableHead className="w-12" />
          </TableRow>
        </TableHeader>

        <TableBody>
          {items.map((upload) => {
            const selected = selectedUIDs.includes(upload.UID)
            const displayName = upload.AliasName || upload.FileName

            return (
              <TableRow
                key={upload.UID}
                data-selected={selected || undefined}
                className={selected ? 'bg-brand/5' : undefined}
              >
                <TableCell>
                  <Checkbox
                    checked={selected}
                    onCheckedChange={() => onToggleSelect(upload.UID)}
                    aria-label={t('GALLERY_TOGGLE_SELECT')}
                  />
                </TableCell>

                <TableCell className="max-w-[16rem]">
                  <button
                    type="button"
                    onClick={(event) => {
                      if (event.shiftKey) onSelect(upload.UID, true)
                      else onOpen(upload.UID)
                    }}
                    className="block max-w-full truncate text-left text-sm text-foreground hover:text-brand hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    title={upload.AliasName ? `${upload.AliasName}（${upload.FileName}）` : upload.FileName}
                  >
                    {displayName}
                  </button>
                </TableCell>

                {showUploader ? (
                  <TableCell className="text-xs text-muted-foreground">
                    {upload.UserEmail || '—'}
                  </TableCell>
                ) : null}

                <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                  {formatBytes(upload.Size)}
                </TableCell>
                <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                  {formatDimensions(upload.Width, upload.Height)}
                </TableCell>
                <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                  {upload.StorageName || storageNames[upload.StorageUID] || upload.StorageUID || '—'}
                </TableCell>
                <TableCell>
                  <StatusBadge status={upload.Status} />
                </TableCell>
                <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                  {formatDateTime(upload.CreatedAt)}
                </TableCell>

                <TableCell>
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <IconButton label={t('COMMON_MORE')} className="size-8">
                        <MoreHorizontal className="size-4" aria-hidden />
                      </IconButton>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem onSelect={() => onEdit(upload)}>
                        {t('GALLERY_RENAME_TITLE')}
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        className="text-destructive focus:text-destructive"
                        onSelect={() => onDelete(upload)}
                      >
                        {t('COMMON_DELETE')}
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}

/** 列表视图里的复制按钮（复用统一的三种格式菜单）。 */
export function ListCopyAction({ upload }: { upload: Upload }) {
  return (
    <LinkCopyMenu
      items={[{ URL: upload.URL, Name: upload.AliasName || upload.OriginalName || upload.FileName }]}
      size="sm"
      variant="ghost"
    />
  )
}
