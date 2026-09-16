import { AlertTriangle, Info, Loader2, RefreshCw, Upload } from 'lucide-react'
import { useState } from 'react'

import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { PageHeader } from '@/components/common/page-header'
import { ThemeCard } from '@/components/themes/theme-card'
import { ThemeSettingsDrawer } from '@/components/themes/theme-settings-drawer'
import { ThemeUploadDialog } from '@/components/themes/theme-upload-dialog'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/ui/empty-state'
import { Skeleton } from '@/components/ui/skeleton'
import { toast } from '@/components/ui/toast'
import { useThemes } from '@/hooks/api'
import { themeApi } from '@/lib/api'
import { t } from '@/i18n'
import { toApiError, type ThemeListItem } from '@/types/api'

/**
 * 主题管理（DESIGN.md §5.8，admin）。
 *
 * 页面固定包含三块说明（§5.8.4，**不是可配置项**）：
 *  1. 主题 = 服务器上的任意前端代码 → 只装可信主题
 *  2. **认证页与 `/admin/**` 永久内置，主题无法接管**（安全默认值，防钓鱼）
 *  3. 主题缺失 / 损坏时首页回退内置默认主题（**永不白屏**）
 */
export function AdminThemesPage() {
  const { themes, loading, error, refresh, rescan } = useThemes()

  const [uploadOpen, setUploadOpen] = useState(false)
  const [configureTarget, setConfigureTarget] = useState<ThemeListItem | null>(null)
  const [uninstallTarget, setUninstallTarget] = useState<ThemeListItem | null>(null)
  const [busyUID, setBusyUID] = useState('')
  const [rescanning, setRescanning] = useState(false)

  const activate = async (theme: ThemeListItem) => {
    setBusyUID(theme.ID)
    try {
      await themeApi.activate(theme.ID)
      toast.success(t('THEME_ACTIVATED', { name: theme.Name }))
      refresh()
    } catch (err) {
      toast.error(toApiError(err).message)
    } finally {
      setBusyUID('')
    }
  }

  const confirmUninstall = async () => {
    if (!uninstallTarget) return
    await themeApi.remove(uninstallTarget.ID)
    toast.success(t('THEME_UNINSTALLED', { name: uninstallTarget.Name }))
    refresh()
  }

  const handleRescan = async () => {
    setRescanning(true)
    try {
      const result = await rescan()
      toast.success(t('THEME_RESCAN_DONE', { count: result.Items.length }))
    } catch (err) {
      toast.error(toApiError(err).message)
    } finally {
      setRescanning(false)
    }
  }

  return (
    <>
      <PageHeader
        title={t('NAV_ADMIN_THEMES')}
        description={t('THEME_DESC')}
        actions={
          <>
            <Button variant="outline" size="sm" disabled={rescanning} onClick={() => void handleRescan()}>
              {rescanning ? (
                <Loader2 className="size-4 animate-spin" aria-hidden />
              ) : (
                <RefreshCw aria-hidden />
              )}
              {t('THEME_RESCAN')}
            </Button>
            <Button variant="brand" size="sm" onClick={() => setUploadOpen(true)}>
              <Upload aria-hidden />
              {t('THEME_UPLOAD_BUTTON')}
            </Button>
          </>
        }
      />

      {/* 安全提示（固定，不是可配置项） */}
      <Alert variant="warning" className="mb-5">
        <AlertTriangle aria-hidden />
        <AlertTitle>{t('THEME_SECURITY_TITLE')}</AlertTitle>
        <AlertDescription>{t('THEME_SECURITY_DESC')}</AlertDescription>
      </Alert>

      {loading && themes.length === 0 ? (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 3 }, (_, index) => (
            <Skeleton key={index} className="h-72 w-full" />
          ))}
        </div>
      ) : error ? (
        <EmptyState
          title={t('THEME_LOAD_FAILED')}
          description={toApiError(error).message}
          action={
            <Button variant="outline" onClick={refresh}>
              {t('COMMON_RETRY')}
            </Button>
          }
        />
      ) : themes.length === 0 ? (
        <EmptyState
          title={t('THEME_EMPTY')}
          description={t('THEME_EMPTY_DESC')}
          action={
            <Button variant="brand" onClick={() => setUploadOpen(true)}>
              {t('THEME_UPLOAD_BUTTON')}
            </Button>
          }
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {themes.map((theme) => (
            <ThemeCard
              key={theme.ID}
              theme={theme}
              busy={busyUID === theme.ID}
              onActivate={(item) => void activate(item)}
              onConfigure={setConfigureTarget}
              onUninstall={setUninstallTarget}
            />
          ))}
        </div>
      )}

      {/* 边界说明（固定文案，帮助管理员理解「为什么主题改不了登录页」） */}
      <Alert variant="info" className="mt-6">
        <Info aria-hidden />
        <AlertTitle>{t('THEME_BOUNDARY_TITLE')}</AlertTitle>
        <AlertDescription>
          <ul className="list-disc space-y-1 pl-4">
            <li>{t('THEME_BOUNDARY_AUTH')}</li>
            <li>{t('THEME_BOUNDARY_ASSETS')}</li>
            <li>{t('THEME_BOUNDARY_FALLBACK')}</li>
          </ul>
        </AlertDescription>
      </Alert>

      <ThemeUploadDialog
        open={uploadOpen}
        onOpenChange={setUploadOpen}
        onInstalled={() => refresh()}
      />

      <ThemeSettingsDrawer
        open={configureTarget !== null}
        onOpenChange={(open) => !open && setConfigureTarget(null)}
        theme={configureTarget}
        onCleared={refresh}
      />

      <ConfirmDialog
        open={uninstallTarget !== null}
        onOpenChange={(open) => !open && setUninstallTarget(null)}
        title={t('THEME_UNINSTALL_TITLE', { name: uninstallTarget?.Name ?? '' })}
        description={t('THEME_UNINSTALL_DESC')}
        confirmLabel={t('THEME_ACTION_UNINSTALL')}
        destructive
        onConfirm={confirmUninstall}
      />
    </>
  )
}
