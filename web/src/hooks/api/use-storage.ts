import { useCallback } from 'react'

import { useAsync } from '@/hooks/api/use-async'
import { usePaged, type UsePagedResult } from '@/hooks/api/use-paged'
import { fetchDefaultStorageConfig, storageApi, type StorageConfigListParams } from '@/lib/api'
import type { StorageConfig, StorageDriver } from '@/types/api'

/**
 * 存储驱动（API.md §3，admin）。
 *
 * 同一驱动类型可有多条实例（D64）；列表展示 `Name`，内部一律用 `UID`。
 */

/** 存储配置分页列表 + 便捷的「默认那条」。 */
export interface UseStorageConfigsResult extends UsePagedResult<StorageConfig> {
  /** 当前默认（`IsDefault`）那条；没有则回退第一条启用的 */
  defaultConfig: StorageConfig | undefined
}

export function useStorageConfigs(
  params: Omit<StorageConfigListParams, 'Page' | 'PageSize'> = {},
  options: { skip?: boolean; pageSize?: number } = {},
): UseStorageConfigsResult {
  const { skip = false, pageSize = 50 } = options

  const paged = usePaged<StorageConfig>(
    (page, size) => storageApi.list({ ...params, Page: page, PageSize: size }),
    { skip, initialPageSize: pageSize, deps: [params.Type, params.Enabled, params.Keyword] },
  )

  const defaultConfig = paged.items.find((item) => item.IsDefault) ?? paged.items[0]

  return { ...paged, defaultConfig }
}

/**
 * 驱动类型列表（含**服务端求值后的**配置 schema）。
 *
 * 供 `/admin/storage` 的「新建配置」选择驱动，以及渲染动态表单。
 */
export function useStorageDrivers(): {
  drivers: StorageDriver[]
  loading: boolean
  error: unknown
  refresh: () => void
} {
  const state = useAsync(() => storageApi.drivers(), [])

  return {
    drivers: state.data?.Drivers ?? [],
    loading: state.loading,
    error: state.error,
    refresh: state.refresh,
  }
}

/** 按驱动类型取「一次求值后的 schema」（用于打开新建抽屉时初始化表单）。 */
export function useDriverSchema(type: string, options: { skip?: boolean } = {}) {
  const { skip = false } = options
  const state = useAsync(() => storageApi.driverSchema({ Type: type, Answers: {} }), [type], {
    skip: skip || !type,
  })

  /** `DependsOn` 联动：带着当前表单值回源重求值（**前端永不执行插件代码**）。 */
  const reevaluate = useCallback(
    (answers: Record<string, unknown>) => storageApi.driverSchema({ Type: type, Answers: answers }),
    [type],
  )

  return {
    schema: state.data?.Config ?? [],
    driverName: state.data?.Name ?? type,
    loading: state.loading,
    error: state.error,
    refresh: state.refresh,
    reevaluate,
  }
}

export { fetchDefaultStorageConfig }
