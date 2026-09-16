import { AlertTriangle, CheckCircle2, ImageOff, Star } from 'lucide-react'
import { useState } from 'react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { themeApi } from '@/lib/api'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import type { ThemeListItem } from '@/types/api'

/**
 * 主题卡片（DESIGN.md §5.8）。
 *
 * 必须展示的要素：
 *  - 预览图（缺失时用占位图，**不能是空白**）
 *  - 名称 / 版本 / 作者
 *  - **`Pages`（接管范围）** —— 让管理员一眼看出该主题会影响哪些页面
 *  - 徽章 `IsActive`（当前启用）/ `IsBuiltin`（随镜像发布，不可卸载）
 *  - `Valid=false` → **红色错误卡 + 原因**，且**不允许启用**
 *
 * ⚠️ **认证页与 `/admin/**` 由系统内置、永久保留**，主题无法接管（D94.2）；
 *    这是安全设计，不是 bug。
 */
export interface ThemeCardProps {
  theme: ThemeListItem
  /** 启用（切换当前主题） */
  onActivate: (theme: ThemeListItem) => void
  /** 打开设置抽屉 */
  onConfigure: (theme: ThemeListItem) => void
  /** 卸载 */
  onUninstall: (theme: ThemeListItem) => void
  busy?: boolean
  className?: string
}

export function ThemeCard({
  theme,
  onActivate,
  onConfigure,
  onUninstall,
  busy = false,
  className,
}: ThemeCardProps) {
  const [previewBroken, setPreviewBroken] = useState(false)

  const invalid = !theme.Valid
  const canActivate = !invalid && !theme.IsActive && !busy
  const canUninstall = theme.CanUninstall && !busy

  return (
    <Card
      className={cn(
        invalid && 'border-destructive/50',
        theme.IsActive && 'ring-1 ring-brand',
        className,
      )}
    >
      <CardHeader className="space-y-3">
        {/* 预览图 */}
        <div className="flex aspect-[16/9] items-center justify-center overflow-hidden rounded-md border border-border bg-muted/40">
          {previewBroken || !theme.ScreenshotURL ? (
            <ImageOff className="size-6 text-muted-foreground" aria-hidden />
          ) : (
            <img
              src={themeApi.screenshotUrl(theme.ID)}
              alt={t('THEME_PREVIEW_ALT', { name: theme.Name })}
              onError={() => setPreviewBroken(true)}
              className="size-full object-cover"
              loading="lazy"
            />
          )}
        </div>

        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0 space-y-0.5">
            <CardTitle className="truncate text-base">{theme.Name}</CardTitle>
            <p className="text-xs text-muted-foreground">
              {theme.Author ? `${theme.Author} · ` : ''}
              <span className="font-mono">v{theme.Version || '—'}</span>
            </p>
          </div>

          <div className="flex shrink-0 flex-col items-end gap-1">
            {theme.IsActive ? (
              <Badge variant="brand" className="gap-1">
                <CheckCircle2 className="size-3" aria-hidden />
                {t('THEME_BADGE_ACTIVE')}
              </Badge>
            ) : null}
            {theme.IsBuiltin ? (
              <Badge variant="secondary" className="gap-1">
                <Star className="size-3" aria-hidden />
                {t('THEME_BADGE_BUILTIN')}
              </Badge>
            ) : null}
          </div>
        </div>
      </CardHeader>

      <CardContent className="space-y-3">
        {/* 接管范围 —— 必展示项 */}
        <div className="space-y-1">
          <p className="text-xs font-medium text-muted-foreground">{t('THEME_PAGES_LABEL')}</p>
          <div className="flex flex-wrap gap-1">
            {theme.Pages.length > 0 ? (
              theme.Pages.map((page) => (
                <code
                  key={page}
                  className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-foreground"
                >
                  {page}
                </code>
              ))
            ) : (
              <span className="text-xs text-muted-foreground">—</span>
            )}
          </div>
        </div>

        {theme.Description ? (
          <p className="line-clamp-2 text-xs text-muted-foreground">{theme.Description}</p>
        ) : null}

        {/* 无效主题：红色错误卡 + 原因 */}
        {invalid ? (
          <div className="flex items-start gap-2 rounded-md bg-destructive/10 px-2.5 py-2">
            <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-destructive" aria-hidden />
            <div className="min-w-0 space-y-0.5">
              <p className="text-xs font-medium text-destructive">{t('THEME_INVALID_TITLE')}</p>
              <p className="break-words text-[11px] text-destructive/90">
                {theme.Error || t('THEME_INVALID_UNKNOWN')}
              </p>
            </div>
          </div>
        ) : null}

        {/* 操作 */}
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            size="sm"
            variant={canActivate ? 'brand' : 'outline'}
            disabled={!canActivate}
            onClick={() => onActivate(theme)}
            title={theme.IsActive ? t('THEME_ACTION_ACTIVE_HINT') : undefined}
          >
            {t('THEME_ACTION_ACTIVATE')}
          </Button>

          <Button type="button" size="sm" variant="outline" disabled={busy} onClick={() => onConfigure(theme)}>
            {t('THEME_ACTION_CONFIGURE')}
          </Button>

          <Tooltip>
            <TooltipTrigger asChild>
              {/* 用 span 包一层，否则 disabled 的 button 不触发 Tooltip */}
              <span>
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  disabled={!canUninstall}
                  onClick={() => onUninstall(theme)}
                  className="text-destructive hover:text-destructive"
                >
                  {t('THEME_ACTION_UNINSTALL')}
                </Button>
              </span>
            </TooltipTrigger>
            {!canUninstall ? (
              <TooltipContent>
                {theme.IsActive ? t('THEME_UNINSTALL_DISABLED_ACTIVE') : t('THEME_UNINSTALL_DISABLED_BUILTIN')}
              </TooltipContent>
            ) : null}
          </Tooltip>
        </div>
      </CardContent>
    </Card>
  )
}
