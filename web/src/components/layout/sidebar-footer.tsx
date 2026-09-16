import { HardDrive } from 'lucide-react'

import { QuotaBar } from '@/components/layout/quota-bar'
import { t } from '@/i18n'
import { PICGO_WEB_RELEASE_URL } from '@/hooks/use-latest-version'
import { cn } from '@/lib/utils'
import { useTaskStore } from '@/store/task-store'

interface SidebarFooterProps {
  /** 侧栏收起为图标条时只显示紧凑状态。 */
  collapsed?: boolean
  /** 当前 PicGo-Web 服务版本（来自公开站点配置）。 */
  serverVersion?: string
  /** GitHub 最新正式 Release 版本。 */
  latestVersion?: string
  /** GitHub 最新正式 Release 是否高于当前版本。 */
  updateAvailable?: boolean
}

/**
 * 侧栏底部信息（参考 Immich 的底部状态区）。
 *
 * SSE 是后台外壳与服务端之间的实时通道，因此这里用它的状态作为
 * 「服务器在线 / 离线」的即时反馈；断连时不占用内容区顶部空间。
 */
export function SidebarFooter({
  collapsed = false,
  serverVersion,
  latestVersion,
  updateAvailable = false,
}: SidebarFooterProps) {
  const sseStatus = useTaskStore((state) => state.sseStatus)
  const online = sseStatus === 'open'
  const connecting = sseStatus === 'connecting'
  const version = serverVersion?.trim() ? `v${serverVersion.trim()}` : t('SIDEBAR_VERSION_UNKNOWN')
  const statusLabel = online
    ? t('SIDEBAR_SERVER_ONLINE')
    : connecting
      ? t('SIDEBAR_SERVER_CONNECTING')
      : t('SIDEBAR_SERVER_OFFLINE')
  const dotClass = online
    ? 'bg-success'
    : connecting
      ? 'bg-warning'
      : 'bg-destructive'

  if (collapsed) {
    return (
      <div className="flex flex-col items-center gap-3 px-2 py-3">
        <span title={t('SIDEBAR_STORAGE_SPACE')}>
          <HardDrive className="size-4 text-muted-foreground" aria-hidden />
        </span>
        <span
          className={cn('size-2 rounded-full', dotClass)}
          aria-label={statusLabel}
          title={statusLabel}
        />
      </div>
    )
  }

  return (
    <div className="space-y-3 px-3 py-3">
      <QuotaBar className="rounded-lg bg-muted/60" />

      <div
        className="flex items-center gap-2 rounded-lg border border-border px-2.5 py-2 text-sm"
        role="status"
        aria-label={`${statusLabel}: ${version}`}
      >
        <span
          className={cn('size-2 shrink-0 rounded-full', dotClass)}
          aria-label={statusLabel}
          title={statusLabel}
        />
        <span className="font-light text-foreground">{version}</span>
        {updateAvailable ? (
          <a
            href={PICGO_WEB_RELEASE_URL}
            target="_blank"
            rel="noreferrer noopener"
            className="ml-auto shrink-0 text-xs font-light text-brand hover:underline"
            aria-label={`${t('SIDEBAR_NEW_VERSION')}${latestVersion ? ` (${latestVersion})` : ''}`}
          >
            {t('SIDEBAR_NEW_VERSION')}
          </a>
        ) : null}
      </div>
    </div>
  )
}
