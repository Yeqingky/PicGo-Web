import { Album as AlbumIcon, Images, MoreHorizontal, Pencil, Plus, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router'

import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { PageHeader } from '@/components/common/page-header'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { EmptyState } from '@/components/ui/empty-state'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import { toast } from '@/components/ui/toast'
import { useAlbums } from '@/hooks/api'
import { t } from '@/i18n'
import { albumApi } from '@/lib/api'
import { toApiError, type Album } from '@/types/api'
import { useGalleryStore } from '@/store/gallery-store'

/**
 * 相册（DESIGN.md §3.2 `/albums`）。
 *
 * 设计取舍：**「进入相册查看图片」跳转到图库并带上 `AlbumUID` 筛选**，
 * 而不是在这里再实现一套图片列表 + 灯箱。
 *  - 理由：图库已经具备完整的筛选/多选/灯箱/批量能力，重复实现会带来两份行为不一致
 *  - 代价：用户会经历一次路由跳转；但筛选条件可见、可清除，路径清晰
 */
export function AlbumsPage() {
  const navigate = useNavigate()
  const { albums, loading, error, refresh } = useAlbums()

  const patchFilter = useGalleryStore((state) => state.patchFilter)
  const setScope = useGalleryStore((state) => state.setScope)

  const [creating, setCreating] = useState(false)
  const [editing, setEditing] = useState<Album | null>(null)
  const [deleting, setDeleting] = useState<Album | null>(null)

  const openAlbum = (album: Album) => {
    // 相册属于「我的」（D33 图片各自私有），因此切回 mine
    setScope('mine')
    patchFilter({ AlbumUID: album.UID })
    navigate('/gallery')
  }

  const confirmDelete = async () => {
    if (!deleting) return
    await albumApi.remove(deleting.UID, false)
    toast.success(t('ALBUM_DELETE_SUCCESS'))
    refresh()
  }

  return (
    <>
      <PageHeader
        title={t('NAV_ALBUMS')}
        description={t('ALBUM_DESC')}
        actions={
          <Button variant="brand" size="sm" onClick={() => setCreating(true)}>
            <Plus aria-hidden />
            {t('ALBUM_CREATE')}
          </Button>
        }
      />

      {loading && albums.length === 0 ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {Array.from({ length: 4 }, (_, index) => (
            <Skeleton key={index} className="h-40 w-full" />
          ))}
        </div>
      ) : error ? (
        <EmptyState
          title={t('ALBUM_LOAD_FAILED')}
          description={toApiError(error).message}
          action={
            <Button variant="outline" onClick={refresh}>
              {t('COMMON_RETRY')}
            </Button>
          }
        />
      ) : albums.length === 0 ? (
        <EmptyState
          icon={AlbumIcon}
          title={t('ALBUM_EMPTY')}
          description={t('ALBUM_EMPTY_DESC')}
          action={
            <Button variant="brand" onClick={() => setCreating(true)}>
              {t('ALBUM_CREATE')}
            </Button>
          }
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {albums.map((album) => (
            <Card key={album.UID} className="overflow-hidden">
              {/* 封面：用相册内某张图（`CoverURL`）；没有则占位 */}
              <button
                type="button"
                onClick={() => openAlbum(album)}
                className="flex aspect-[16/9] w-full items-center justify-center overflow-hidden bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                aria-label={t('ALBUM_OPEN', { name: album.Name })}
              >
                {album.CoverURL ? (
                  <img
                    src={album.CoverURL}
                    alt=""
                    loading="lazy"
                    className="size-full object-cover"
                  />
                ) : (
                  <Images className="size-7 text-muted-foreground" aria-hidden />
                )}
              </button>

              <CardContent className="flex items-start justify-between gap-2 p-3">
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-foreground" title={album.Name}>
                    {album.Name}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {t('ALBUM_IMAGE_COUNT', { count: album.ImageCount })}
                  </p>
                </div>

                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <IconButton label={t('COMMON_MORE')} className="size-8 shrink-0">
                      <MoreHorizontal aria-hidden />
                    </IconButton>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    <DropdownMenuItem onSelect={() => setEditing(album)}>
                      <Pencil aria-hidden />
                      {t('COMMON_EDIT')}
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      className="text-destructive focus:text-destructive"
                      onSelect={() => setDeleting(album)}
                    >
                      <Trash2 aria-hidden />
                      {t('COMMON_DELETE')}
                    </DropdownMenuItem>
                  </DropdownMenuContent>
                </DropdownMenu>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {/* 新建 / 编辑（同一个对话框） */}
      <AlbumFormDialog
        open={creating || editing !== null}
        album={editing}
        onOpenChange={(open) => {
          if (!open) {
            setCreating(false)
            setEditing(null)
          }
        }}
        onSaved={() => {
          refresh()
          setCreating(false)
          setEditing(null)
        }}
      />

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open) setDeleting(null)
        }}
        title={t('ALBUM_DELETE_TITLE', { name: deleting?.Name ?? '' })}
        description={
          deleting && deleting.ImageCount > 0
            ? t('ALBUM_DELETE_HAS_IMAGES', { count: deleting.ImageCount })
            : t('ALBUM_DELETE_DESC')
        }
        confirmLabel={t('COMMON_DELETE')}
        destructive
        onConfirm={confirmDelete}
      />
    </>
  )
}

interface AlbumFormDialogProps {
  open: boolean
  album: Album | null
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}

function AlbumFormDialog({ open, album, onOpenChange, onSaved }: AlbumFormDialogProps) {
  const [name, setName] = useState('')
  const [intro, setIntro] = useState('')
  const [busy, setBusy] = useState(false)

  // 打开时用相册现值初始化（新建则清空）。
  // 用 useEffect 而不是「渲染期判断 + 直接 mutate state」，后者在 StrictMode 下行为不可预期。
  useEffect(() => {
    if (!open) return
    setName(album?.Name ?? '')
    setIntro(album?.Intro ?? '')
  }, [open, album])

  const submit = async () => {
    const trimmed = name.trim()
    if (!trimmed) {
      toast.error(t('ALBUM_NAME_REQUIRED'))
      return
    }

    setBusy(true)
    try {
      if (album) {
        await albumApi.update(album.UID, { Name: trimmed, Intro: intro.trim() })
        toast.success(t('ALBUM_UPDATED'))
      } else {
        await albumApi.create({ Name: trimmed, Intro: intro.trim() })
        toast.success(t('ALBUM_CREATED'))
      }
      onSaved()
    } catch (err) {
      toast.error(toApiError(err).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{album ? t('ALBUM_EDIT_TITLE') : t('ALBUM_CREATE_TITLE')}</DialogTitle>
        </DialogHeader>

        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="album-name">{t('ALBUM_NAME')}</Label>
            <Input
              id="album-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              autoFocus
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="album-intro">{t('ALBUM_INTRO')}</Label>
            <Textarea
              id="album-intro"
              value={intro}
              onChange={(event) => setIntro(event.target.value)}
              rows={3}
            />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            {t('COMMON_CANCEL')}
          </Button>
          <Button variant="brand" onClick={() => void submit()} disabled={busy}>
            {t('COMMON_SAVE')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
