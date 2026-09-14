import { Grid2X2, Images, List, RotateCw, Trash2, FolderInput } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router'

import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { LinkCopyMenu } from '@/components/common/link-copy-menu'
import { PageHeader } from '@/components/common/page-header'
import { ImageGrid } from '@/components/gallery/image-grid'
import { ImageLightbox } from '@/components/gallery/image-lightbox'
import { ImageList } from '@/components/gallery/image-list'
import { MoveUploadsDialog, RenameUploadDialog } from '@/components/gallery/upload-dialogs'
import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/ui/empty-state'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Pagination } from '@/components/ui/pagination'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toast } from '@/components/ui/toast'
import { useAlbums, useGalleryUploads, useStorageConfigs } from '@/hooks/api'
import { t } from '@/i18n'
import { uploadApi } from '@/lib/api'
import { albumNameMap } from '@/lib/album'
import { toApiError, type Upload } from '@/types/api'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/store/auth-store'
import { useGalleryStore } from '@/store/gallery-store'

/**
 * 图库（DESIGN.md §5.2）—— 本项目最核心的页面。
 *
 * 四条必须记住的设计：
 *  1. **管理员顶部 Tab「我的 / 全部」**（D71）：普通用户**不渲染切换器**；
 *     两个 Tab **各自保存筛选状态**（在 `gallery-store` 里按 scope 存了两份）
 *  2. **不做缩略图**（D84）：卡片直接引图床 URL，不依赖 `ThumbURL`
 *  3. **管理员默认落在 `mine`**：避免误操作他人图片
 *  4. 选的是**分页**而不是无限滚动（理由见 `use-paged.ts`）
 */
export function GalleryPage() {
  const isAdmin = useAuthStore((state) => state.user?.Role === 'admin')

  const scope = useGalleryStore((state) => state.scope)
  const filter = useGalleryStore((state) => state.filters[state.scope])
  const viewMode = useGalleryStore((state) => state.viewMode)
  const selectedUIDs = useGalleryStore((state) => state.selectedUIDs)
  const lightboxIndex = useGalleryStore((state) => state.lightboxIndex)

  const setScope = useGalleryStore((state) => state.setScope)
  const patchFilter = useGalleryStore((state) => state.patchFilter)
  const resetFilter = useGalleryStore((state) => state.resetFilter)
  const setViewMode = useGalleryStore((state) => state.setViewMode)
  const toggleSelected = useGalleryStore((state) => state.toggleSelected)
  const selectRange = useGalleryStore((state) => state.selectRange)
  const selectAll = useGalleryStore((state) => state.selectAll)
  const clearSelected = useGalleryStore((state) => state.clearSelected)
  const openLightbox = useGalleryStore((state) => state.openLightbox)
  const closeLightbox = useGalleryStore((state) => state.closeLightbox)
  const setLightboxIndex = useGalleryStore((state) => state.openLightbox)

  const [keywordInput, setKeywordInput] = useState(filter.Keyword)
  const [renaming, setRenaming] = useState<Upload | null>(null)
  const [moving, setMoving] = useState<Upload[] | null>(null)
  const [deleting, setDeleting] = useState<Upload[] | null>(null)
  const [deleteRemote, setDeleteRemote] = useState(false)

  const { albums } = useAlbums()
  const { items: storageConfigs } = useStorageConfigs()

  const {
    items,
    total,
    page,
    pageSize,
    loading,
    error,
    setPage,
    resetPage,
    refresh,
    mutate,
  } = useGalleryUploads({
    Scope: scope,
    Keyword: filter.Keyword || undefined,
    StorageUID: filter.StorageUID || undefined,
    AlbumUID: filter.AlbumUID || undefined,
    Status: filter.Status || undefined,
    Sort: filter.Sort,
    Order: filter.Order,
  })

  const albumNames = useMemo(() => albumNameMap(albums), [albums])

  // 关键词输入防抖（300ms）：避免每敲一个字都打一次接口
  useEffect(() => {
    const timer = setTimeout(() => {
      if (keywordInput !== filter.Keyword) {
        patchFilter({ Keyword: keywordInput })
        resetPage()
      }
    }, 300)
    return () => clearTimeout(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [keywordInput])

  // 切 Tab 时同步关键词输入框（两个 Tab 独立保存筛选）
  useEffect(() => {
    setKeywordInput(filter.Keyword)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scope])

  const orderedUIDs = useMemo(() => items.map((item) => item.UID), [items])
  const selectedUploads = useMemo(
    () => items.filter((item) => selectedUIDs.includes(item.UID)),
    [items, selectedUIDs],
  )

  const handleSelect = useCallback(
    (uid: string, shiftKey: boolean) => {
      if (shiftKey) selectRange(uid, orderedUIDs)
      else toggleSelected(uid)
    },
    [selectRange, toggleSelected, orderedUIDs],
  )

  const handleOpen = useCallback(
    (uid: string) => {
      const index = items.findIndex((item) => item.UID === uid)
      if (index >= 0) openLightbox(index)
    },
    [items, openLightbox],
  )

  // 键盘：Ctrl/⌘+A 全选已加载项；Esc 清除选择（DESIGN.md §9.1）
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null
      const inInput =
        target?.tagName === 'INPUT' || target?.tagName === 'TEXTAREA' || target?.isContentEditable
      if (inInput) return

      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'a') {
        event.preventDefault()
        selectAll(orderedUIDs)
      } else if (event.key === 'Escape') {
        clearSelected()
      }
    }

    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [orderedUIDs, selectAll, clearSelected])

  const confirmDelete = async () => {
    if (!deleting || deleting.length === 0) return
    const uids = deleting.map((item) => item.UID)

    if (uids.length === 1) {
      await uploadApi.remove(uids[0], deleteRemote)
    } else {
      await uploadApi.batchRemove({ UIDs: uids, DeleteRemote: deleteRemote })
    }

    // 乐观地把它从列表移除（refresh 会与服务端对齐）
    mutate((prev) => prev.filter((item) => !uids.includes(item.UID)))
    clearSelected()
    toast.success(t('GALLERY_DELETE_SUCCESS', { count: uids.length }))
    refresh()
  }

  const hasFilter =
    filter.Keyword !== '' ||
    filter.StorageUID !== '' ||
    filter.AlbumUID !== '' ||
    filter.Status !== ''

  return (
    <>
      <PageHeader
        title={t('NAV_GALLERY')}
        description={t('GALLERY_DESC')}
        actions={
          <>
            <IconButton
              label={t('GALLERY_VIEW_GRID')}
              onClick={() => setViewMode('grid')}
              className={cn(viewMode === 'grid' && 'bg-accent text-foreground')}
            >
              <Grid2X2 aria-hidden />
            </IconButton>
            <IconButton
              label={t('GALLERY_VIEW_LIST')}
              onClick={() => setViewMode('list')}
              className={cn(viewMode === 'list' && 'bg-accent text-foreground')}
            >
              <List aria-hidden />
            </IconButton>
            <IconButton label={t('COMMON_REFRESH')} onClick={refresh}>
              <RotateCw aria-hidden />
            </IconButton>
          </>
        }
      >
        {/* 管理员 Tab（普通用户不渲染切换器，D71） */}
        {isAdmin ? (
          <Tabs
            value={scope}
            onValueChange={(value) => {
              setScope(value as 'mine' | 'all')
              resetPage()
            }}
          >
            <TabsList>
              <TabsTrigger value="mine">{t('GALLERY_TAB_MINE')}</TabsTrigger>
              <TabsTrigger value="all">{t('GALLERY_TAB_ALL')}</TabsTrigger>
            </TabsList>
          </Tabs>
        ) : null}

        {/* 筛选栏 */}
        <div className="flex flex-wrap items-end gap-2">
          <div className="min-w-[12rem] flex-1 space-y-1">
            <Label htmlFor="gallery-keyword" className="text-xs text-muted-foreground">
              {t('GALLERY_FILTER_KEYWORD')}
            </Label>
            <Input
              id="gallery-keyword"
              value={keywordInput}
              onChange={(event) => setKeywordInput(event.target.value)}
              placeholder={t('GALLERY_FILTER_KEYWORD_PLACEHOLDER')}
            />
          </div>

          <FilterSelect
            id="gallery-storage"
            label={t('GALLERY_FILTER_STORAGE')}
            value={filter.StorageUID}
            onChange={(value) => {
              patchFilter({ StorageUID: value })
              resetPage()
            }}
            allLabel={t('GALLERY_FILTER_ALL')}
            options={storageConfigs.map((config) => ({ value: config.UID, label: config.Name }))}
          />

          <FilterSelect
            id="gallery-album"
            label={t('GALLERY_FILTER_ALBUM')}
            value={filter.AlbumUID}
            onChange={(value) => {
              patchFilter({ AlbumUID: value })
              resetPage()
            }}
            allLabel={t('GALLERY_FILTER_ALL')}
            extraOptions={[{ value: 'none', label: t('GALLERY_NO_ALBUM') }]}
            options={albums.map((album) => ({ value: album.UID, label: album.Name }))}
          />

          <FilterSelect
            id="gallery-status"
            label={t('GALLERY_FILTER_STATUS')}
            value={filter.Status}
            onChange={(value) => {
              patchFilter({ Status: value as typeof filter.Status })
              resetPage()
            }}
            allLabel={t('GALLERY_FILTER_ALL')}
            options={[
              { value: 'success', label: t('STATUS_SUCCESS') },
              { value: 'pending', label: t('STATUS_PENDING') },
              { value: 'failed', label: t('STATUS_FAILED') },
            ]}
          />

          <FilterSelect
            id="gallery-sort"
            label={t('GALLERY_FILTER_SORT')}
            value={`${filter.Sort}:${filter.Order}`}
            onChange={(value) => {
              const [sort, order] = value.split(':')
              patchFilter({
                Sort: sort as typeof filter.Sort,
                Order: order as typeof filter.Order,
              })
              resetPage()
            }}
            allLabel={t('GALLERY_SORT_CREATED_DESC')}
            optionsLabel={t('GALLERY_SORT_LABEL')}
            options={[
              { value: 'CreatedAt:desc', label: t('GALLERY_SORT_CREATED_DESC') },
              { value: 'CreatedAt:asc', label: t('GALLERY_SORT_CREATED_ASC') },
              { value: 'Size:desc', label: t('GALLERY_SORT_SIZE_DESC') },
              { value: 'Size:asc', label: t('GALLERY_SORT_SIZE_ASC') },
              { value: 'FileName:asc', label: t('GALLERY_SORT_NAME_ASC') },
              { value: 'FileName:desc', label: t('GALLERY_SORT_NAME_DESC') },
            ]}
            noAll
          />

          {hasFilter ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => {
                resetFilter()
                setKeywordInput('')
                resetPage()
              }}
            >
              {t('GALLERY_FILTER_RESET')}
            </Button>
          ) : null}
        </div>

        {/* 批量操作条 */}
        {selectedUIDs.length > 0 ? (
          <div className="flex flex-wrap items-center gap-2 rounded-lg border border-brand/30 bg-brand/5 px-3 py-2">
            <span className="text-sm text-foreground">
              {t('GALLERY_SELECTED', { count: selectedUIDs.length })}
            </span>
            <div className="ml-auto flex flex-wrap items-center gap-2">
              <LinkCopyMenu
                items={selectedUploads.map((item) => ({
                  URL: item.URL,
                  Name: item.AliasName || item.OriginalName || item.FileName,
                }))}
              />
              <Button type="button" variant="outline" size="sm" onClick={() => setMoving(selectedUploads)}>
                <FolderInput aria-hidden />
                {t('GALLERY_MOVE_TO_ALBUM')}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="text-destructive hover:text-destructive"
                onClick={() => setDeleting(selectedUploads)}
              >
                <Trash2 aria-hidden />
                {t('COMMON_DELETE')}
              </Button>
              <Button type="button" variant="ghost" size="sm" onClick={clearSelected}>
                {t('GALLERY_CLEAR_SELECTION')}
              </Button>
            </div>
          </div>
        ) : null}
      </PageHeader>

      {/* 列表主体 */}
      {loading && items.length === 0 ? (
        <GallerySkeleton viewMode={viewMode} />
      ) : error ? (
        <EmptyState
          title={t('GALLERY_LOAD_FAILED')}
          description={toApiError(error).message}
          action={
            <Button type="button" variant="outline" onClick={refresh}>
              {t('COMMON_RETRY')}
            </Button>
          }
        />
      ) : items.length === 0 ? (
        <EmptyState
          icon={Images}
          title={hasFilter ? t('GALLERY_EMPTY_FILTERED') : t('GALLERY_EMPTY')}
          description={hasFilter ? t('GALLERY_EMPTY_FILTERED_DESC') : t('GALLERY_EMPTY_DESC')}
          action={
            hasFilter ? (
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  resetFilter()
                  setKeywordInput('')
                }}
              >
                {t('GALLERY_FILTER_RESET')}
              </Button>
            ) : (
              <Button asChild variant="brand">
                <Link to="/upload">{t('GALLERY_GO_UPLOAD')}</Link>
              </Button>
            )
          }
        />
      ) : viewMode === 'grid' ? (
        <ImageGrid
          items={items}
          selectedUIDs={selectedUIDs}
          showUploader={scope === 'all'}
          onToggleSelect={toggleSelected}
          onSelect={handleSelect}
          onOpen={handleOpen}
        />
      ) : (
        <ImageList
          items={items}
          selectedUIDs={selectedUIDs}
          showUploader={scope === 'all'}
          albumNames={albumNames}
          onToggleSelect={toggleSelected}
          onSelect={handleSelect}
          onOpen={handleOpen}
          onEdit={setRenaming}
          onDelete={(upload) => setDeleting([upload])}
          onMove={(upload) => setMoving([upload])}
        />
      )}

      {/* 分页 */}
      {items.length > 0 ? (
        <Pagination
          className="mt-5"
          page={page}
          pageSize={pageSize}
          total={total}
          onPageChange={setPage}
        />
      ) : null}

      {/* 灯箱 */}
      <ImageLightbox
        items={items}
        index={lightboxIndex}
        onIndexChange={setLightboxIndex}
        onClose={closeLightbox}
        showUploader={scope === 'all'}
      />

      {/* 重命名 */}
      <RenameUploadDialog
        open={renaming !== null}
        onOpenChange={(open) => {
          if (!open) setRenaming(null)
        }}
        upload={renaming}
        onRenamed={refresh}
      />

      {/* 移动到相册 */}
      <MoveUploadsDialog
        open={moving !== null}
        onOpenChange={(open) => {
          if (!open) setMoving(null)
        }}
        uploads={moving ?? []}
        albums={albums}
        onMoved={() => {
          clearSelected()
          refresh()
        }}
      />

      {/* 删除确认（用 ConfirmDialog：异步确认按钮不能是 AlertDialogAction） */}
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open) {
            setDeleting(null)
            setDeleteRemote(false)
          }
        }}
        title={t('GALLERY_DELETE_TITLE', { count: deleting?.length ?? 0 })}
        description={t('GALLERY_DELETE_DESC')}
        confirmLabel={t('COMMON_DELETE')}
        destructive
        onConfirm={confirmDelete}
      >
        <div className="flex items-start justify-between gap-3 rounded-md border border-border px-3 py-2.5">
          <div className="space-y-0.5">
            <Label htmlFor="delete-remote">{t('GALLERY_DELETE_REMOTE')}</Label>
            <p className="text-xs text-muted-foreground">{t('GALLERY_DELETE_REMOTE_HINT')}</p>
          </div>
          <Switch id="delete-remote" checked={deleteRemote} onCheckedChange={setDeleteRemote} />
        </div>
      </ConfirmDialog>
    </>
  )
}

// ---------------------------------------------------------------------------
// 辅助组件
// ---------------------------------------------------------------------------

interface FilterSelectProps {
  id: string
  label: string
  value: string
  onChange: (value: string) => void
  options: { value: string; label: string }[]
  extraOptions?: { value: string; label: string }[]
  /** 「全部」选项的文案（传 `noAll` 则不加） */
  allLabel: string
  noAll?: boolean
  /** 有分组语义时用（排序） */
  optionsLabel?: string
}

/** 统一形态的筛选下拉（含「全部」项）。 */
function FilterSelect({
  id,
  label,
  value,
  onChange,
  options,
  extraOptions = [],
  allLabel,
  noAll = false,
  optionsLabel,
}: FilterSelectProps) {
  const all = noAll ? [] : [{ value: '__all__', label: allLabel }]

  return (
    <div className="space-y-1">
      <Label htmlFor={id} className="text-xs text-muted-foreground">
        {label}
      </Label>
      <Select
        value={value === '' ? '__all__' : value}
        onValueChange={(next) => onChange(next === '__all__' ? '' : next)}
      >
        <SelectTrigger id={id} className="min-w-[9rem]">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {optionsLabel ? (
            <SelectItem value="__label__" disabled>
              {optionsLabel}
            </SelectItem>
          ) : null}
          {[...all, ...extraOptions, ...options].map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}

/** 加载态骨架（DESIGN.md §9.3：首屏用 Skeleton）。 */
function GallerySkeleton({ viewMode }: { viewMode: 'grid' | 'list' }) {
  if (viewMode === 'list') {
    return (
      <div className="space-y-2">
        {Array.from({ length: 6 }, (_, index) => (
          <Skeleton key={index} className="h-12 w-full" />
        ))}
      </div>
    )
  }

  return (
    <div className="columns-2 gap-3 sm:columns-3 lg:columns-4 xl:columns-5 2xl:columns-6">
      {[160, 220, 190, 240, 170, 210, 200, 180].map((height, index) => (
        <Skeleton key={index} className="mb-3 break-inside-avoid" style={{ height }} />
      ))}
    </div>
  )
}
