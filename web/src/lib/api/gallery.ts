import { del, get, patch, post } from '@/lib/http'
import type {
  Album,
  AlbumListQuery,
  BatchDeleteUploadsRequest,
  BatchDeleteUploadsResponse,
  CreateAlbumRequest,
  CreateUploadsResponse,
  DeleteAlbumResponse,
  DeleteUploadResponse,
  LinkFormatName,
  MoveUploadsResponse,
  PageData,
  UpdateAlbumRequest,
  UpdateUploadRequest,
  Upload,
  UploadLinkResponse,
  UploadLinksRequest,
  UploadLinksResponse,
  UploadListQuery,
  UploadStats,
} from '@/types/api'

/**
 * 图库与相册（API.md §4 / §5）。
 *
 * 三条必须记住的规则：
 *  - **一次上传请求 = 1 个 job = 1 个驱动**（D38）→ 上传接口是 `multipart/form-data`
 *  - **不做缩略图**（D84）→ 列表直接用 `Upload.URL`，`ThumbURL` 不使用
 *  - `Scope=all` **仅管理员**有效；普通用户传了会被后端静默降级为 `mine`（D71）
 */

/** 一次上传的入参。`Files` 可多值，但**一次只能选一个驱动**（D38）。 */
export interface UploadFilesInput {
  Files: File[]
  StorageUID: string
  AlbumUID?: string
  KeepLocal?: boolean
}

export const uploadApi = {
  /**
   * 上传文件（`multipart/form-data`）。
   *
   * 立即返回 `JobUID`，真正的上传在后台队列中推进 —— 进度通过 SSE 获取
   * （`upload.progress` / `upload.finished` / `upload.failed`）。
   */
  create(
    input: UploadFilesInput,
    options: { onUploadProgress?: (percent: number) => void } = {},
  ): Promise<CreateUploadsResponse> {
    const form = new FormData()
    for (const file of input.Files) {
      form.append('Files', file)
    }
    form.append('StorageUID', input.StorageUID)
    if (input.AlbumUID) form.append('AlbumUID', input.AlbumUID)
    if (input.KeepLocal !== undefined) form.append('KeepLocal', String(input.KeepLocal))

    return post<CreateUploadsResponse>('/uploads', form, {
      headers: { 'Content-Type': 'multipart/form-data' },
      // 上传大文件可能较久，覆盖默认 30s
      timeout: 120_000,
      onUploadProgress: (event) => {
        if (!options.onUploadProgress) return
        const total = event.total ?? 0
        if (total <= 0) return
        options.onUploadProgress(Math.round((event.loaded / total) * 100))
      },
    })
  },

  /** 服务端拉取远程图片再交给队列（后端做 SSRF 防护）。 */
  createFromUrl(payload: {
    URLs: string[]
    StorageUID: string
    AlbumUID?: string
  }): Promise<CreateUploadsResponse> {
    return post<CreateUploadsResponse>('/uploads/from-url', payload)
  },

  list(query: UploadListQuery = {}): Promise<PageData<Upload>> {
    return get<PageData<Upload>>('/uploads', { params: query })
  },

  get(uid: string): Promise<Upload> {
    return get<Upload>(`/uploads/${encodeURIComponent(uid)}`)
  },

  /** 重命名（`AliasName`，**不改远端文件名**）/ 移动相册。 */
  update(uid: string, payload: UpdateUploadRequest): Promise<Upload> {
    return patch<Upload>(`/uploads/${encodeURIComponent(uid)}`, payload)
  },

  /**
   * 硬删除（D46）。`deleteRemote` 为真时尝试同步删除图床上的文件（D47）——
   * **仅当该驱动实现了 `remove` 事件时才生效**，否则只删本地记录。
   */
  remove(uid: string, deleteRemote = false): Promise<DeleteUploadResponse> {
    return del<DeleteUploadResponse>(`/uploads/${encodeURIComponent(uid)}`, {
      params: { DeleteRemote: deleteRemote },
    })
  },

  /** 批量删除（单次上限 200；逐条独立处理，部分失败不整体回滚）。 */
  batchRemove(payload: BatchDeleteUploadsRequest): Promise<BatchDeleteUploadsResponse> {
    return post<BatchDeleteUploadsResponse>('/uploads/batch-delete', payload)
  },

  stats(scope: 'mine' | 'all' = 'mine'): Promise<UploadStats> {
    return get<UploadStats>('/uploads/stats', { params: { Scope: scope } })
  },

  /** 单张外链（三种格式，D68）。 */
  link(uid: string, format: LinkFormatName = 'markdown'): Promise<UploadLinkResponse> {
    return get<UploadLinkResponse>(`/uploads/${encodeURIComponent(uid)}/link`, {
      params: { Format: format },
    })
  },

  /** 批量外链（多行拼接，供一次性复制，D68）。 */
  links(payload: UploadLinksRequest): Promise<UploadLinksResponse> {
    return post<UploadLinksResponse>('/uploads/links', payload)
  },
}

export const albumApi = {
  list(query: AlbumListQuery = {}): Promise<{ Items: Album[] }> {
    return get<{ Items: Album[] }>('/albums', { params: query })
  },

  get(uid: string): Promise<Album> {
    return get<Album>(`/albums/${encodeURIComponent(uid)}`)
  },

  create(payload: CreateAlbumRequest): Promise<Album> {
    return post<Album>('/albums', payload)
  },

  update(uid: string, payload: UpdateAlbumRequest): Promise<Album> {
    return patch<Album>(`/albums/${encodeURIComponent(uid)}`, payload)
  },

  /**
   * 删除相册。
   *
   * `withUploads=false` 且相册内有图片 → 后端返 `40901`（提示先移出）；
   * `withUploads=true` 时相册内图片**仅脱离相册**，**不删除图片**。
   */
  remove(uid: string, withUploads = false): Promise<DeleteAlbumResponse> {
    return del<DeleteAlbumResponse>(`/albums/${encodeURIComponent(uid)}`, {
      params: { WithUploads: withUploads },
    })
  },

  /** 把图片移入该相册。 */
  moveUploads(uid: string, uploadUIDs: string[]): Promise<MoveUploadsResponse> {
    return post<MoveUploadsResponse>(`/albums/${encodeURIComponent(uid)}/move-uploads`, {
      UploadUIDs: uploadUIDs,
    })
  },

  /**
   * 按请求体指定目标相册（`targetAlbumUID` 传 `""` 表示**移出相册**）。
   */
  moveUploadsTo(targetAlbumUID: string, uploadUIDs: string[]): Promise<MoveUploadsResponse> {
    return post<MoveUploadsResponse>('/albums/move-uploads', {
      UploadUIDs: uploadUIDs,
      TargetAlbumUID: targetAlbumUID,
    })
  },
}
