import { useCallback, useState } from 'react'

import { useAsync, type AsyncState } from '@/hooks/api/use-async'
import type { PageData } from '@/types/api'

/**
 * 通用分页列表 hook（AGENTS.md / DESIGN.md §11：服务端状态用自研 hooks，不进 Zustand）。
 *
 * 为什么用**分页**而不是无限滚动（DESIGN.md §5.2 给了二选一）：
 *  - 图库的核心操作是**批量选择 + 批量操作**，分页能给出稳定的「共 N 条 / 第 x 页」，
 *    选中项不会随列表增长而「失联」
 *  - 本项目**不做缩略图**（D84），大图懒加载本身就有滚动性能压力；
 *    一次性渲染 50 条可控，无限滚动会把压力堆到几百条
 *
 * 契约：`fetcher(page, pageSize)` 返回 `PageData<T>`。
 */
export interface UsePagedResult<T> {
  items: T[]
  total: number
  page: number
  pageSize: number
  loading: boolean
  error: AsyncState<PageData<T>>['error']
  setPage: (page: number) => void
  setPageSize: (size: number) => void
  /** 回到第 1 页（筛选条件变化时调用） */
  resetPage: () => void
  refresh: () => void
  /** 本地写入（乐观更新用；不触发请求） */
  mutate: (updater: (prev: T[]) => T[]) => void
}

export interface UsePagedOptions {
  initialPage?: number
  initialPageSize?: number
  /** 为真时跳过请求（例如缺少必要参数） */
  skip?: boolean
  /** 额外的请求依赖（变化时重新拉取） */
  deps?: readonly unknown[]
}

export function usePaged<T>(
  fetcher: (page: number, pageSize: number) => Promise<PageData<T>>,
  options: UsePagedOptions = {},
): UsePagedResult<T> {
  const { initialPage = 1, initialPageSize = 20, skip = false, deps = [] } = options

  const [page, setPage] = useState(initialPage)
  const [pageSize, setPageSize] = useState(initialPageSize)

  const state = useAsync(() => fetcher(page, pageSize), [page, pageSize, ...deps], { skip })

  const resetPage = useCallback(() => setPage(1), [])

  const mutate = useCallback(
    (updater: (prev: T[]) => T[]) => {
      state.mutate((prev) =>
        prev ? { ...prev, Items: updater(prev.Items) } : prev,
      )
    },
    [state],
  )

  return {
    items: state.data?.Items ?? [],
    total: state.data?.Total ?? 0,
    page: state.data?.Page ?? page,
    pageSize: state.data?.PageSize ?? pageSize,
    loading: state.loading,
    error: state.error,
    setPage,
    setPageSize,
    resetPage,
    refresh: state.refresh,
    mutate,
  }
}
