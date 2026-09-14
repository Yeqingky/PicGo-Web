import type { Album } from '@/types/api'

/**
 * 相册 UID → 名称映射（列表视图展示用）。
 *
 * 纯函数，单独放 `lib` 而不是组件文件里：
 *  - 组件文件只导出组件（React Fast Refresh 约定）
 *  - 列表视图与详情页都要用，集中一处避免各写一遍
 */
export function albumNameMap(albums: Album[]): Record<string, string> {
  const map: Record<string, string> = {}
  for (const album of albums) map[album.UID] = album.Name
  return map
}
