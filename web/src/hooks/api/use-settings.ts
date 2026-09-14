import { useMemo } from 'react'

import { useAsync } from '@/hooks/api/use-async'
import { settingsApi, systemApi } from '@/lib/api'
import type { SystemSettingItem, SystemSettingsResponse, SystemStats } from '@/types/api'

/**
 * 系统设置（API.md §11，admin）。
 *
 * ⚠️ 配置键名保持 `dot.lowerCamel` **原样**（D81.3 第 3 条）：
 * 它们是 KV 表的字符串 key，不是列名。只有**响应体的外层字段**用 PascalCase。
 */

export interface UseSystemSettingsResult {
  /** 按分类分组（供后台分 Tab 渲染） */
  groups: SystemSettingsResponse['Groups']
  meta: SystemSettingsResponse['Meta'] | undefined
  loading: boolean
  error: unknown
  refresh: () => void
  /** 按键名取一项（含元信息与 `Source`，供渲染「来自数据库 / 使用默认值」徽章） */
  getItem: (key: string) => SystemSettingItem | undefined
  /** 取某分类的全部项 */
  ofCategory: (category: string) => SystemSettingItem[]
}

export function useSystemSettings(options: { skip?: boolean } = {}): UseSystemSettingsResult {
  const { skip = false } = options
  const state = useAsync(() => settingsApi.getSystem(), [], { skip })

  const groups = useMemo(() => state.data?.Groups ?? [], [state.data])

  const index = useMemo(() => {
    const map = new Map<string, SystemSettingItem>()
    for (const group of groups) {
      for (const item of group.Keys) {
        map.set(item.Key, item)
      }
    }
    return map
  }, [groups])

  return {
    groups,
    meta: state.data?.Meta,
    loading: state.loading,
    error: state.error,
    refresh: state.refresh,
    getItem: (key: string) => index.get(key),
    ofCategory: (category: string) => index.size === 0
      ? []
      : groups.find((group) => group.Category === category)?.Keys ?? [],
  }
}

/** 统计面板数据（admin）。 */
export function useSystemStats(options: { skip?: boolean } = {}) {
  const { skip = false } = options
  const state = useAsync(() => systemApi.stats(), [], { skip })

  return {
    stats: state.data as SystemStats | undefined,
    loading: state.loading,
    error: state.error,
    refresh: state.refresh,
  }
}

/**
 * 取单个系统设置项（例如日志保留天数）。
 *
 * 找不到时返回 `undefined` —— 调用方应自行给文案兜底
 * （例如「日志保留 {{days}} 天」，取不到就整句不显示）。
 */
export function useSystemSettingItem(
  key: string,
  options: { skip?: boolean } = {},
): { item: SystemSettingItem | undefined; loading: boolean } {
  const { skip = false } = options
  const { getItem, loading } = useSystemSettings({ skip })
  return { item: getItem(key), loading }
}
