import { useAsync } from '@/hooks/api/use-async'
import { useSystemSettingItem } from '@/hooks/api/use-settings'
import { usePaged, type UsePagedResult } from '@/hooks/api/use-paged'
import { logApi } from '@/lib/api'
import type { EmailLog, EmailLogListQuery, LogListQuery, LogTypeItem, OperationLog } from '@/types/api'

/**
 * 操作日志（API.md §9，admin）。
 *
 * 两套日志要分清（D78 生命周期不同）：
 *  - **OperationLogs**：审计级结果记录，保留 **180 天**（D74）
 *  - **EmailLogs**：邮件发送记录（**不保存正文**）
 */

export function useOperationLogs(
  query: LogListQuery,
  options: { skip?: boolean; pageSize?: number } = {},
): UsePagedResult<OperationLog> {
  const { skip = false, pageSize = 50 } = options

  return usePaged<OperationLog>(
    (page, size) => logApi.list({ ...query, Page: page, PageSize: size }),
    {
      skip,
      initialPageSize: pageSize,
      deps: [
        query.Type,
        query.Status,
        query.Keyword,
        query.UserUID,
        query.TargetType,
        query.TargetUID,
        query.From,
        query.To,
      ],
    },
  )
}

/** 可过滤的类型清单（静态声明，供筛选下拉）。 */
export function useLogTypes() {
  const state = useAsync(() => logApi.types(), [])
  const types: LogTypeItem[] = state.data?.Types ?? []
  return { types, loading: state.loading, error: state.error }
}

export function useEmailLogs(
  query: EmailLogListQuery = {},
  options: { skip?: boolean; pageSize?: number } = {},
): UsePagedResult<EmailLog> {
  const { skip = false, pageSize = 50 } = options

  return usePaged<EmailLog>(
    (page, size) => logApi.emails({ ...query, Page: page, PageSize: size }),
    { skip, initialPageSize: pageSize, deps: [query.ToAddress, query.Template, query.Status] },
  )
}

/**
 * 日志保留天数（用于「日志保留 180 天」提示，D74）。
 *
 * 从系统设置 `log.retentionDays` 读（admin 才能拿；普通用户拿不到时返回 undefined，
 * UI 退化为不显示该提示）。`0` 表示**永久保留**。
 */
export function useLogRetention(): { days: number | undefined; loading: boolean } {
  const { item, loading } = useSystemSettingItem('log.retentionDays')

  const raw = item?.Value
  const days =
    typeof raw === 'number'
      ? raw
      : typeof raw === 'string' && raw.trim() !== ''
        ? Number(raw)
        : item?.Default !== undefined
          ? Number(item.Default)
          : undefined

  return { days: Number.isFinite(days) ? days : undefined, loading }
}
