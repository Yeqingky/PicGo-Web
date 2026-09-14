import { Info, Palette } from 'lucide-react'
import { Link } from 'react-router'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useSiteConfig } from '@/hooks/api'
import { t } from '@/i18n'

/**
 * 内置 SPA 的 `/` 路由 —— **开发期占位，生产环境不可达**。
 *
 * 为什么会有这个页面（D94 / DESIGN.md §3.1 / §9.5）：
 *  - 生产环境 `/` 由**当前主题**渲染，Go 的分发逻辑在第 3 步就把 `/` 交给主题，
 *    内置 SPA 的这段路由永远不会被命中
 *  - 但 `vite dev server` 只服务 SPA，不经过 Go 的主题分发，
 *    所以本地开发直接访问 `/` 会落到本页
 *
 * ⚠️ **不要在这里实现落地页**：落地页属于默认主题（`themes/default/`，W10），
 *    而主题是**独立构建单元**（资源前缀 `/theme-assets/**`），不属于内置 SPA。
 */
export function HomePage() {
  const { config } = useSiteConfig()

  const themeName = config?.Theme?.Name
  const themePages = config?.Theme?.Pages ?? []

  return (
    <div className="mx-auto max-w-2xl space-y-6 py-6">
      <div className="space-y-1.5">
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">
          {t('HOME_DEV_PLACEHOLDER_TITLE')}
        </h1>
        <p className="text-sm text-muted-foreground">{t('HOME_DEV_PLACEHOLDER_DESC')}</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <Palette className="size-4 text-muted-foreground" aria-hidden />
            当前主题
          </CardTitle>
          <CardDescription>
            {themeName
              ? `ID：${config?.Theme?.ID} · 版本 ${config?.Theme?.Version}`
              : '主题信息不可用（后端未返回，或 /site/config 尚未实现）'}
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <div>
            <p className="mb-1 text-xs font-medium text-muted-foreground">接管的路由（Pages）</p>
            {themePages.length > 0 ? (
              <ul className="space-y-0.5 font-mono text-xs text-foreground">
                {themePages.map((page) => (
                  <li key={page}>{page}</li>
                ))}
              </ul>
            ) : (
              <p className="text-xs text-muted-foreground">—</p>
            )}
          </div>
          <p className="text-xs text-muted-foreground">
            主题在 `manifest.json` 的 `Pages` 里自行注册要接管的页面；未注册的路径一律走内置 SPA。
            认证页与 `/admin/**` 永久保留，主题无法接管。
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <Info className="size-4 text-muted-foreground" aria-hidden />
            开发提示
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3 text-sm text-muted-foreground">
          <p>
            本地开发时 `/` 会落到本页，这是预期行为。想验证主题渲染，请用
            `make theme` 产出 `themes/default/` 后由 Go 托管访问。
          </p>
          <Button asChild variant="outline" size="sm">
            <Link to="/gallery">前往图库</Link>
          </Button>
        </CardContent>
      </Card>
    </div>
  )
}
