import { useAsync } from '@/hooks/api/use-async'
import { systemApi } from '@/lib/api'
import type { SystemInfo } from '@/types/api'

/**
 * 系统信息（`GET /system/info`，需登录）。
 *
 * 用于「站点设置 → 关于」展示版本 / schema 版本 / 数据库方言 / 当前主题。
 * 数据在一次会话内基本不变，但仍走 `useAsync` 以统一 loading/error 处理。
 */
export function useSystemInfo(): { info: SystemInfo | undefined; loading: boolean; refresh: () => void } {
  const state = useAsync(() => systemApi.info(), [])
  return { info: state.data, loading: state.loading, refresh: state.refresh }
}
