import { useCallback, useState } from 'react'

import { useAsync } from '@/hooks/api/use-async'
import { usersApi, type UserListParams } from '@/lib/api'
import { type User } from '@/types/api'

/**
 * 用户列表（`GET /api/web/v1/users`，admin）。
 *
 * 形态：分页 + 关键词搜索。筛选条件由调用方（页面）持有，
 * 这里只负责「拿数据 + 暴露 refresh」（DESIGN.md §11：服务端状态不进 Zustand）。
 */
export interface UseUsersResult {
  items: User[]
  total: number
  page: number
  pageSize: number
  loading: boolean
  error: unknown
  setPage: (page: number) => void
  setPageSize: (size: number) => void
  setKeyword: (keyword: string) => void
  refresh: () => void
}

export function useUsers(initial: UserListParams = {}): UseUsersResult {
  const [page, setPage] = useState(initial.Page ?? 1)
  const [pageSize, setPageSize] = useState(initial.PageSize ?? 20)
  const [keyword, setKeywordState] = useState(initial.Keyword ?? '')

  const state = useAsync(
    () =>
      usersApi.list({
        Page: page,
        PageSize: pageSize,
        Keyword: keyword.trim() || undefined,
      }),
    [page, pageSize, keyword],
  )

  /** 改关键词时回到第 1 页（否则会停在一个可能不存在的页码上）。 */
  const setKeyword = useCallback((value: string) => {
    setKeywordState(value)
    setPage(1)
  }, [])

  return {
    items: state.data?.Items ?? [],
    total: state.data?.Total ?? 0,
    page: state.data?.Page ?? page,
    pageSize: state.data?.PageSize ?? pageSize,
    loading: state.loading,
    error: state.error,
    setPage,
    setPageSize,
    setKeyword,
    refresh: state.refresh,
  }
}
