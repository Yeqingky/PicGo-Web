import { useCallback, useMemo } from 'react'

import { useAsync } from '@/hooks/api/use-async'
import { usePaged, type UsePagedResult } from '@/hooks/api/use-paged'
import { albumApi, uploadApi } from '@/lib/api'
import type { Album, Upload, UploadListQuery, UploadStats } from '@/types/api'

/**
 * 图库与相册（API.md §4 / §5）。
 *
 * 三条必须记住的规则：
 *  - **不做缩略图**（D84）→ 列表直接用 `Upload.URL`，`ThumbURL` 不使用
 *  - `Scope=all` **仅管理员**有效；普通用户传了会被后端静默降级为 `mine`（D71）
 *  - 一次上传 = 一个 job = 一个驱动（D38）；上传走 `store/upload-store.ts`
 */

/** 图库列表（分页 + 筛选）。筛选变化时调用方应 `resetPage()`。 */
export function useGalleryUploads(
  query: UploadListQuery,
  options: { skip?: boolean; pageSize?: number } = {},
): UsePagedResult<Upload> {
  const { skip = false, pageSize = 50 } = options

  return usePaged<Upload>((page, size) => uploadApi.list({ ...query, Page: page, PageSize: size }), {
    skip,
    initialPageSize: pageSize,
    deps: [
      query.Scope,
      query.Keyword,
      query.StorageUID,
      query.AlbumUID,
      query.Status,
      query.Sort,
      query.Order,
    ],
  })
}

/** 单张图片详情（`/gallery/:UID`）。 */
export function useUploadDetail(uid: string, options: { skip?: boolean } = {}) {
  const { skip = false } = options
  const state = useAsync(() => uploadApi.get(uid), [uid], { skip: skip || !uid })

  return { upload: state.data, loading: state.loading, error: state.error, refresh: state.refresh }
}

/** 图库统计（按当前 Tab 的 Scope）。 */
export function useUploadStats(scope: 'mine' | 'all', options: { skip?: boolean } = {}) {
  const { skip = false } = options
  const state = useAsync(() => uploadApi.stats(scope), [scope], { skip })

  return { stats: state.data as UploadStats | undefined, loading: state.loading, refresh: state.refresh }
}

/** 相册列表（**不分页**：相册数量天然有限）。 */
export function useAlbums(options: { keyword?: string; skip?: boolean } = {}) {
  const { keyword = '', skip = false } = options
  const state = useAsync(() => albumApi.list(keyword ? { Keyword: keyword } : {}), [keyword], { skip })

  const albums: Album[] = useMemo(() => state.data?.Items ?? [], [state.data])

  /** 便捷：给选择器用的 `{ value, label }` 列表。 */
  const options_ = useCallback(
    () => albums.map((album) => ({ value: album.UID, label: album.Name })),
    [albums],
  )

  return {
    albums,
    asOptions: options_,
    loading: state.loading,
    error: state.error,
    refresh: state.refresh,
  }
}
