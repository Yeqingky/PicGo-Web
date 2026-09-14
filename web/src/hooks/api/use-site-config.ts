import { useEffect, useState } from 'react'

import { siteApi } from '@/lib/api'
import type { SiteConfigResponse } from '@/types/api'

/**
 * 站点公开信息 + 当前主题配置（`GET /api/web/v1/site/config`，**无需登录**）。
 *
 * 用法与边界（DESIGN.md §9.5）：
 *  - 内置 SPA 只用它取「站点名 / 图标 / 是否启用 GitHub 登录」这类少量信息
 *  - **首页（主题）不依赖本 hook**：主题是独立前端，自己调同一个接口
 *  - **不返回任何敏感信息**（后端保证）
 *
 * 缓存策略：模块级 promise 缓存 —— 站点信息在一次会话内基本不变，
 * 多个组件同时调用只会产生**一次**请求（与 `useAsync` 的组合正好互补）。
 */

let cached: Promise<SiteConfigResponse> | null = null

/** 主动失效缓存（例如管理员改了站点设置后）。 */
export function invalidateSiteConfig(): void {
  cached = null
}

function loadSiteConfig(): Promise<SiteConfigResponse> {
  if (!cached) {
    cached = siteApi.config().catch((err: unknown) => {
      // 失败不缓存：否则一次网络抖动会让整个会话都拿不到站点信息
      cached = null
      throw err
    })
  }
  return cached
}

export interface SiteConfigState {
  config: SiteConfigResponse | undefined
  loading: boolean
  error: unknown
}

export function useSiteConfig(): SiteConfigState {
  const [config, setConfig] = useState<SiteConfigResponse | undefined>(undefined)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(undefined)

  useEffect(() => {
    let cancelled = false

    loadSiteConfig()
      .then((result) => {
        if (cancelled) return
        setConfig(result)
      })
      .catch((err: unknown) => {
        if (cancelled) return
        setError(err)
      })
      .finally(() => {
        if (cancelled) return
        setLoading(false)
      })

    return () => {
      cancelled = true
    }
  }, [])

  return { config, loading, error }
}

/**
 * 站点展示名（含回退）。
 *
 * 优先级：`SiteConfig.Site.Name` → 构建期默认名 `PicGo Web`。
 */
export function siteDisplayName(config: SiteConfigResponse | undefined): string {
  return config?.Site?.Name?.trim() || 'PicGo Web'
}

/** 站点图标 URL（可能为空）。 */
export function siteIconUrl(config: SiteConfigResponse | undefined): string {
  return config?.Site?.IconURL?.trim() || ''
}
