import { useEffect, useState } from 'react'

import { useAsync } from '@/hooks/api/use-async'
import { usePaged, type UsePagedResult } from '@/hooks/api/use-paged'
import { jobApi } from '@/lib/api'
import type { Job, JobListQuery, JobLogsResponse } from '@/types/api'
import { useTaskStore } from '@/store/task-store'

/**
 * 任务（API.md §8）。
 *
 * ⚠️ 任务状态是**服务端事实**（`Jobs` 表）；`task-store` 里只放
 * 「正在跟踪的 JobUID + SSE 推来的瞬态进度」。
 *
 * 列表在 SSE `job.finished` 到达时自动刷新（`task-store.jobsRevision`），
 * 因为「任务结束」正是列表最需要更新的时刻。
 */

export function useJobs(
  query: JobListQuery = {},
  options: { skip?: boolean; pageSize?: number } = {},
): UsePagedResult<Job> {
  const { skip = false, pageSize = 20 } = options

  // 订阅 task-store 的 revision：SSE 报告任务结束时触发一次重新拉取
  const jobsRevision = useTaskStore((state) => state.jobsRevision)

  return usePaged<Job>((page, size) => jobApi.list({ ...query, Page: page, PageSize: size }), {
    skip,
    initialPageSize: pageSize,
    deps: [query.Kind, query.Status, query.Scope, jobsRevision],
  })
}

/** 单个任务详情（含批内子项）。 */
export function useJobDetail(uid: string, options: { skip?: boolean } = {}) {
  const { skip = false } = options
  const jobsRevision = useTaskStore((state) => state.jobsRevision)

  const state = useAsync(() => jobApi.get(uid), [uid, jobsRevision], { skip: skip || !uid })

  return { job: state.data as Job | undefined, loading: state.loading, error: state.error, refresh: state.refresh }
}

/**
 * 逐行日志（增量拉取）。
 *
 * SSE 不保证重放丢失事件，因此这里用 `AfterSeq` **增量**拉取：
 *  - 首次拉全量（`AfterSeq=0`）
 *  - 之后每次只取新增行，追加到本地
 *  - `job.finished` 到达时做最后一次补齐
 */
export function useJobLogs(uid: string, options: { skip?: boolean } = {}) {
  const { skip = false } = options

  const [logs, setLogs] = useState<JobLogsResponse['Items']>([])
  const [lastSeq, setLastSeq] = useState(0)
  const [loading, setLoading] = useState(!skip)
  const [error, setError] = useState<unknown>(undefined)

  // task-store 里由 SSE 累积的日志（作为辅助：SSE 到达即追加，避免频繁轮询）
  const tracked = useTaskStore((state) => (uid ? state.jobs[uid]?.Logs : undefined))
  const trackedStatus = useTaskStore((state) => (uid ? state.jobs[uid]?.Status : undefined))

  // 首次 / 任务结束时做一次全量对齐
  useEffect(() => {
    if (skip || !uid) {
      setLoading(false)
      return
    }

    let cancelled = false
    setLoading(true)

    jobApi
      .logs(uid, 0, 2000)
      .then((res) => {
        if (cancelled) return
        setLogs(res.Items)
        setLastSeq(res.LastSeq)
        setError(undefined)
      })
      .catch((err: unknown) => {
        if (cancelled) return
        setError(err)
      })
      .finally(() => {
        if (cancelled) return
        setLoading(false)
      })

    return () => {
      cancelled = true
    }
  }, [uid, skip, trackedStatus])

  // SSE 到达新行时增量补齐（仅在任务仍在跑时；结束时上面的 effect 会全量对齐一次）
  useEffect(() => {
    if (skip || !uid || !tracked || tracked.length === 0) return
    if (trackedStatus !== 'running' && trackedStatus !== 'queued') return

    let cancelled = false
    jobApi
      .logs(uid, lastSeq, 500)
      .then((res) => {
        if (cancelled || res.Items.length === 0) return
        setLogs((prev) => [...prev, ...res.Items])
        setLastSeq(res.LastSeq)
      })
      .catch(() => {
        // 增量失败不影响已展示内容（下次 SSE 到达还会再试）
      })

    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [uid, skip, tracked?.length, trackedStatus])

  return { logs, loading, error }
}
