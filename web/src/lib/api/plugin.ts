import { get, patch, post } from '@/lib/http'
import type {
  PageData,
  Plugin,
  PluginJobResponse,
  PluginListResponse,
  PluginReadmeResponse,
  PluginSearchItem,
} from '@/types/api'

/**
 * 插件（API.md §6）。**全部端点需要 admin**。
 *
 * 三条必须记住的规则：
 *  1. `GuiOnly=true` 的插件含 `guiMenu` / `commands`（Electron 专属），**Web 端无法执行** →
 *     前端置灰并提示「该能力仅在桌面端可用」
 *  2. 安装 / 卸载 / 更新**一律异步**：返回 `JobUID`，之后靠 SSE `job.log` 展示 npm 输出
 *  3. **任务成功后 agent 会重启自身进程**；重启期间上传类接口短暂 `503 / 50002`。
 *     这是**预期行为**，不是错误（DESIGN.md §5.4 / API.md §6.1）
 */
export const pluginApi = {
  /**
   * `refresh=true` 强制重新探测并刷新后端缓存表。
   *
   * ⚠️ 返回类型放宽为「契约形状 **或** 后端当前形状」：
   * 契约（`API.md` §6）是 `{ Items, PendingJobs }`，后端当前是 `{ Plugins, Disabled }`。
   * 归一化在 `usePlugins` 里做（见那里的说明）。
   */
  list(refresh = false): Promise<PluginListResponse | { Plugins: Plugin[]; Disabled: Plugin[] }> {
    return get<PluginListResponse | { Plugins: Plugin[]; Disabled: Plugin[] }>('/plugins', {
      params: { Refresh: refresh },
    })
  },

  /** npm registry 搜索（源取自 `picgo.npmRegistry` 设置）；单次最多 20 条。 */
  search(keyword: string): Promise<PageData<PluginSearchItem>> {
    return get<PageData<PluginSearchItem>>('/plugins/search', { params: { Q: keyword } })
  },

  /** README 原文（Markdown；**前端必须净化后渲染**，防 XSS）。 */
  readme(name: string): Promise<PluginReadmeResponse> {
    return get<PluginReadmeResponse>(`/plugins/${encodeURIComponent(name)}/readme`)
  },

  /** 安装。插件名支持完整名 / 短名 / scope / 本地路径四种写法。 */
  install(names: string[]): Promise<PluginJobResponse> {
    return post<PluginJobResponse>('/plugins/install', { Names: names })
  },

  uninstall(names: string[]): Promise<PluginJobResponse> {
    return post<PluginJobResponse>('/plugins/uninstall', { Names: names })
  },

  /** 更新；传空数组 = 更新全部已装插件。 */
  update(names: string[] = []): Promise<PluginJobResponse> {
    return post<PluginJobResponse>('/plugins/update', { Names: names })
  },

  /** 启用 / 禁用（写回 picgo 的 `picgoPlugins[name]`）。 */
  setEnabled(name: string, enabled: boolean): Promise<{ Name: string; Enabled: boolean }> {
    return patch<{ Name: string; Enabled: boolean }>(`/plugins/${encodeURIComponent(name)}`, {
      Enabled: enabled,
    })
  },
}
