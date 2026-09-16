export { useAsync, type AsyncState, type UseAsyncOptions } from '@/hooks/api/use-async'
export { usePaged, type UsePagedOptions, type UsePagedResult } from '@/hooks/api/use-paged'
export { useUsers, type UseUsersResult } from '@/hooks/api/use-users'
export {
  useSiteConfig,
  siteDisplayName,
  siteIconUrl,
  invalidateSiteConfig,
  type SiteConfigState,
} from '@/hooks/api/use-site-config'

// ---- 存储（API.md §3，admin）----
export {
  useStorageConfigs,
  useStorageDrivers,
  useDriverSchema,
  fetchDefaultStorageConfig,
  type UseStorageConfigsResult,
} from '@/hooks/api/use-storage'

// ---- 图库与相册（API.md §4 / §5）----
export { useGalleryUploads, useUploadDetail, useUploadStats } from '@/hooks/api/use-gallery'

// ---- 任务（API.md §8）----
export { useJobs, useJobDetail, useJobLogs } from '@/hooks/api/use-jobs'

// ---- 操作日志（API.md §9，admin）----
export {
  useOperationLogs,
  useLogTypes,
  useEmailLogs,
  useLogRetention,
} from '@/hooks/api/use-logs'

// ---- 插件（API.md §6，admin）----
export { usePlugins, usePluginSearch } from '@/hooks/api/use-plugins'

// ---- 主题（API.md §10，admin）----
export { useThemes, useThemeSettings, useThemeInstall } from '@/hooks/api/use-themes'

// ---- 系统信息（API.md §10）----
export { useSystemInfo } from '@/hooks/api/use-system'

// ---- 设置与统计（API.md §11）----
export {
  useSystemSettings,
  useSystemStats,
  useSystemSettingItem,
  type UseSystemSettingsResult,
} from '@/hooks/api/use-settings'
