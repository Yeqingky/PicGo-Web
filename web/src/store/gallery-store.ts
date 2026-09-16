import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'

/**
 * 图库的客户端 UI 状态（DESIGN.md §5.2 / §11 / D71）。
 *
 * 只放**用户意图**：当前 Tab、筛选条件、视图模式、多选集合、灯箱位置。
 * **服务端数据不进这里**（列表由 `useGallery` hook 拉取）。
 *
 * 两条关键语义（D71）：
 *  - 「我的图片 / 全部图片」是**两个独立视图**，**各自保存筛选状态**（切 Tab 不丢条件）
 *  - 切换 Tab 时**清空选择**（两个列表里是不同对象，保留选择会误操作他人图片）
 */

export type GalleryScope = 'mine' | 'all'
export type GalleryViewMode = 'grid' | 'list'
export type GalleryStatusFilter = '' | 'pending' | 'success' | 'failed'

export interface GalleryFilter {
  Keyword: string
  /** `''` = 全部存储 */
  StorageUID: string
  Status: GalleryStatusFilter
  Sort: 'CreatedAt' | 'Size' | 'FileName'
  Order: 'asc' | 'desc'
}

/** 一份空筛选（新用户 / 重置时用）。 */
export const EMPTY_GALLERY_FILTER: GalleryFilter = {
  Keyword: '',
  StorageUID: '',
  Status: '',
  Sort: 'CreatedAt',
  Order: 'desc',
}

interface GalleryState {
  /** 当前 Tab。**管理员默认落在 `mine`**（D71：避免误操作他人图片） */
  scope: GalleryScope
  /** 两个 Tab **各自**的筛选条件 */
  filters: Record<GalleryScope, GalleryFilter>
  viewMode: GalleryViewMode
  /** 多选集合（只存 UID） */
  selectedUIDs: string[]
  /** 上一次点选的项（`Shift` 连选的锚点） */
  anchorUID: string
  /** 灯箱当前展示的索引（`-1` = 关闭） */
  lightboxIndex: number
}

interface GalleryActions {
  setScope: (scope: GalleryScope) => void
  /** 改当前 Tab 的筛选；**关键词变化会重置到第 1 页**（由调用方处理分页）。 */
  patchFilter: (patch: Partial<GalleryFilter>) => void
  resetFilter: () => void
  setViewMode: (mode: GalleryViewMode) => void

  /** 设置选择（替换）。 */
  setSelected: (uids: string[]) => void
  toggleSelected: (uid: string) => void
  /** `Shift` 连选：选中 `anchorUID` 到 `uid` 之间的全部项。 */
  selectRange: (uid: string, orderedUIDs: string[]) => void
  /** `Ctrl/⌘+A`：全选当前已加载项。 */
  selectAll: (uids: string[]) => void
  clearSelected: () => void
  isSelected: (uid: string) => boolean

  openLightbox: (index: number) => void
  closeLightbox: () => void
  /** 灯箱里左右切换（会做边界循环）。 */
  stepLightbox: (delta: number, total: number) => void
}

export const useGalleryStore = create<GalleryState & GalleryActions>()(
  persist(
    (set, get) => ({
      scope: 'mine',
      filters: {
        mine: { ...EMPTY_GALLERY_FILTER },
        all: { ...EMPTY_GALLERY_FILTER },
      },
      viewMode: 'grid',
      selectedUIDs: [],
      anchorUID: '',
      lightboxIndex: -1,

      setScope(scope) {
        // 切 Tab 时清空选择与灯箱：两个列表是不同对象集合，保留选择会误操作他人图片
        set({ scope, selectedUIDs: [], anchorUID: '', lightboxIndex: -1 })
      },

      patchFilter(patch) {
        const { scope } = get()
        set((state) => ({
          filters: {
            ...state.filters,
            [scope]: { ...state.filters[scope], ...patch },
          },
          // 筛选变化后原选择可能已不在列表中，清掉避免误操作
          selectedUIDs: [],
          anchorUID: '',
        }))
      },

      resetFilter() {
        const { scope } = get()
        set((state) => ({
          filters: { ...state.filters, [scope]: { ...EMPTY_GALLERY_FILTER } },
          selectedUIDs: [],
          anchorUID: '',
        }))
      },

      setViewMode(mode) {
        set({ viewMode: mode })
      },

      setSelected(uids) {
        set({ selectedUIDs: uids })
      },

      toggleSelected(uid) {
        set((state) => {
          const exists = state.selectedUIDs.includes(uid)
          return {
            selectedUIDs: exists
              ? state.selectedUIDs.filter((item) => item !== uid)
              : [...state.selectedUIDs, uid],
            anchorUID: uid,
          }
        })
      },

      selectRange(uid, orderedUIDs) {
        const anchor = get().anchorUID || uid
        const from = orderedUIDs.indexOf(anchor)
        const to = orderedUIDs.indexOf(uid)

        if (from < 0 || to < 0) {
          set({ selectedUIDs: [uid], anchorUID: uid })
          return
        }

        const [start, end] = from <= to ? [from, to] : [to, from]
        set({ selectedUIDs: orderedUIDs.slice(start, end + 1) })
      },

      selectAll(uids) {
        set({ selectedUIDs: [...uids] })
      },

      clearSelected() {
        set({ selectedUIDs: [], anchorUID: '' })
      },

      isSelected(uid) {
        return get().selectedUIDs.includes(uid)
      },

      openLightbox(index) {
        set({ lightboxIndex: index })
      },

      closeLightbox() {
        set({ lightboxIndex: -1 })
      },

      stepLightbox(delta, total) {
        if (total <= 0) return
        const current = get().lightboxIndex
        if (current < 0) return
        // 边界循环：最后一张按 → 回到第一张
        const next = (current + delta + total) % total
        set({ lightboxIndex: next })
      },
    }),
    {
      name: 'picgo-web.gallery',
      storage: createJSONStorage(() => localStorage),
      // 只持久化「用户偏好」；选择与灯箱是瞬态，不持久化
      partialize: (state) => ({
        scope: state.scope,
        filters: state.filters,
        viewMode: state.viewMode,
      }),
    },
  ),
)
