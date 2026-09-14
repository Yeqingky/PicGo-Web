import { get } from '@/lib/http'
import type {
  EmailLog,
  EmailLogListQuery,
  LogListQuery,
  LogTypeItem,
  OperationLog,
  PageData,
} from '@/types/api'

/**
 * 操作日志（API.md §9）。**全部端点需要 admin**。
 *
 * 两套日志要分清（D78 生命周期不同）：
 *  - **OperationLogs**：审计级结果记录（一次操作一条），保留 **180 天**（D74）
 *  - **EmailLogs**：邮件发送记录（**不保存正文**），独立列表
 */
export const logApi = {
  list(query: LogListQuery = {}): Promise<PageData<OperationLog>> {
    return get<PageData<OperationLog>>('/logs', {
      params: query,
      // `Type` 支持多值（OR），axios 需要 repeat 形式 `Type=a&Type=b`
      paramsSerializer: { indexes: null },
    })
  },

  get(uid: string): Promise<OperationLog> {
    return get<OperationLog>(`/logs/${encodeURIComponent(uid)}`)
  },

  /** 可过滤的类型清单（静态声明，便于渲染筛选项）。 */
  types(): Promise<{ Types: LogTypeItem[] }> {
    return get<{ Types: LogTypeItem[] }>('/logs/types')
  },

  emails(query: EmailLogListQuery = {}): Promise<PageData<EmailLog>> {
    return get<PageData<EmailLog>>('/logs/emails', { params: query })
  },

  email(uid: string): Promise<EmailLog> {
    return get<EmailLog>(`/logs/emails/${encodeURIComponent(uid)}`)
  },
}
