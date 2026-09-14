import { useCallback, useEffect, useRef, useState } from 'react'

import { toApiError, type ApiError } from '@/types/api'

/**
 * 极简的服务端状态 hook（DESIGN.md §11）。
 *
 * 为什么不用 TanStack Query：内置 SPA 的数据需求以「列表 + 详情 + 手动刷新」为主，
 * 且**服务端数据不镜像进 Zustand**，一个 60 行的 hook 已够用，少一个依赖。
 *
 * 特性：
 *  - `data` / `loading` / `error` / `refresh`
 *  - 卸载后不再 setState（避免 React 警告与内存泄漏）
 *  - `deps` 变化时自动重新请求
 *  - `refresh()` 手动重新请求（用于「重试」按钮与操作后刷新）
 */
export interface AsyncState<T> {
  data: T | undefined
  loading: boolean
  error: ApiError | undefined
  refresh: () => void
  /** 本地写入（乐观更新用；不会触发请求） */
  mutate: (updater: (prev: T | undefined) => T | undefined) => void
}

export interface UseAsyncOptions {
  /** 为真时跳过请求（例如缺少必要参数）。默认 `false`。 */
  skip?: boolean
  /** 是否在挂载时立即请求。默认 `true`。 */
  immediate?: boolean
}

export function useAsync<T>(
  fetcher: () => Promise<T>,
  deps: readonly unknown[],
  options: UseAsyncOptions = {},
): AsyncState<T> {
  const { skip = false, immediate = true } = options

  const [data, setData] = useState<T | undefined>(undefined)
  const [loading, setLoading] = useState<boolean>(!skip && immediate)
  const [error, setError] = useState<ApiError | undefined>(undefined)
  const [revision, setRevision] = useState(0)

  // 卸载标记：避免请求返回后对已卸载组件 setState
  const mountedRef = useRef(true)
  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  // fetcher 每次渲染都是新函数，用 ref 持有以免把它放进依赖数组（会导致无限循环）
  const fetcherRef = useRef(fetcher)
  fetcherRef.current = fetcher

  useEffect(() => {
    if (skip || !immediate) {
      setLoading(false)
      return
    }

    let cancelled = false
    setLoading(true)
    setError(undefined)

    fetcherRef
      .current()
      .then((result) => {
        if (cancelled || !mountedRef.current) return
        setData(result)
      })
      .catch((err: unknown) => {
        if (cancelled || !mountedRef.current) return
        setError(toApiError(err))
      })
      .finally(() => {
        if (cancelled || !mountedRef.current) return
        setLoading(false)
      })

    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [skip, immediate, revision, ...deps])

  const refresh = useCallback(() => {
    setRevision((value) => value + 1)
  }, [])

  const mutate = useCallback((updater: (prev: T | undefined) => T | undefined) => {
    setData((prev) => updater(prev))
  }, [])

  return { data, loading, error, refresh, mutate }
}
