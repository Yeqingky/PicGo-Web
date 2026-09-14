import { del, get } from '@/lib/http'
import type { Job, JobListQuery, JobLogsResponse, PageData } from '@/types/api'

/**
 * 任务与事件（API.md §8）。
 *
 * 两层「任务」要分清：
 *  - **Job**：后端的批次任务（一次上传 = 1 个 job；插件安装也是一个 job），可跨页面存活
 *  - **上传队列**：`/upload` 页的本地视图（见 `store/upload-store.ts`）
 *
 * 实时推送走 `lib/sse.ts`（`job.started` / `job.log` / `job.finished` /
 * `upload.progress` / `upload.finished` / `upload.failed` / `system.notice`）。
 */
export const jobApi = {
  list(query: JobListQuery = {}): Promise<PageData<Job>> {
    return get<PageData<Job>>('/jobs', { params: query })
  },

  /** 详情（含 `Items` 批内子项）。 */
  get(uid: string): Promise<Job> {
    return get<Job>(`/jobs/${encodeURIComponent(uid)}`)
  },

  /**
   * 增量拉取逐行日志（`AfterSeq` = 已知的最大 Seq）。
   *
   * SSE 不保证重放丢失事件，因此断线重连后要用它做一次状态对齐。
   */
  logs(uid: string, afterSeq = 0, limit = 500): Promise<JobLogsResponse> {
    return get<JobLogsResponse>(`/jobs/${encodeURIComponent(uid)}/logs`, {
      params: { AfterSeq: afterSeq, Limit: limit },
    })
  },

  /** 仅能清理**已结束**的任务（运行中 → 40901）。 */
  remove(uid: string): Promise<null> {
    return del<null>(`/jobs/${encodeURIComponent(uid)}`)
  },
}
