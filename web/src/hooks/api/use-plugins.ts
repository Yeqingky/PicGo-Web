import { useCallback, useState } from 'react'

import { useAsync } from '@/hooks/api/use-async'
import { pluginApi } from '@/lib/api'
import type { Job, Plugin, PluginSearchItem } from '@/types/api'

/**
 * 插件（API.md §6，admin）。
 *
 * 记忆点：
 *  - `GuiOnly=true` → 含 Electron 专属能力，**Web 端无法执行**，前端置灰并提示
 *  - 安装 / 卸载 / 更新**一律异步**（返回 `JobUID`），之后靠 SSE `job.log` 展示 npm 输出
 *  - 任务成功后 **agent 会重启自身**，期间上传类接口短暂 503/50002 —— **预期行为**
 */

/**
 * 归一化插件列表响应。
 *
 * ⚠️ 这里对**后端当前实现与 `docs/API.md` 的偏离**做了容错：
 *  - 契约（§6）是 `{ Items, PendingJobs }`
 *  - 后端当前返回 `{ Plugins, Disabled }`
 *
 * 两种形状都能读，**契约仍是 `API.md`**（已在交付说明中列为后端待对齐项）。
 */
function normalizePluginList(data: unknown): { plugins: Plugin[]; pendingJobs: Job[] } {
  if (typeof data !== 'object' || data === null) return { plugins: [], pendingJobs: [] }

  const raw = data as Record<string, unknown>
  const plugins = (raw.Items ?? raw.Plugins ?? []) as Plugin[]
  const pendingJobs = (raw.PendingJobs ?? []) as Job[]

  return { plugins, pendingJobs }
}

export function usePlugins(options: { refresh?: boolean } = {}) {
  const { refresh = false } = options
  const state = useAsync(() => pluginApi.list(refresh), [refresh])

  const { plugins, pendingJobs } = normalizePluginList(state.data)

  return { plugins, pendingJobs, loading: state.loading, error: state.error, refresh: state.refresh }
}

/** npm 搜索（默认关键词 `picgo-plugin-`，与 PicGo 生态惯例一致）。 */
export function usePluginSearch() {
  const [keyword, setKeyword] = useState('picgo-plugin-')
  const [results, setResults] = useState<PluginSearchItem[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string>('')
  const [searched, setSearched] = useState(false)

  const search = useCallback(async (value?: string) => {
    const q = (value ?? keyword).trim()
    if (!q) return

    setLoading(true)
    setError('')
    try {
      const page = await pluginApi.search(q)
      setResults(page.Items)
      setSearched(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setResults([])
    } finally {
      setLoading(false)
    }
  }, [keyword])

  return { keyword, setKeyword, results, loading, error, searched, search }
}
