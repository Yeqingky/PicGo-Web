import { IdCard, ShieldCheck } from 'lucide-react'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Separator } from '@/components/ui/separator'
import { t } from '@/i18n'
import { formatBytes, formatDateTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/store/auth-store'

/**
 * 个人设置（DESIGN.md §3.2）。
 *
 * 本轮（W7）只展示**账号信息**（数据来自 `GET /auth/me` 的本地快照，
 * 由 `AppShell` 的 bootstrap 拉取）—— 这足以验证「登录 → 拿到真实用户数据」的链路。
 *
 * 改密码、API Token、GitHub 绑定留在 W8（见页面底部的说明）。
 */

interface InfoRowProps {
  label: string
  children: React.ReactNode
  className?: string
}

function InfoRow({ label, children, className }: InfoRowProps) {
  return (
    <div className={cn('flex items-start justify-between gap-4 py-2.5', className)}>
      <span className="shrink-0 text-sm text-muted-foreground">{label}</span>
      <span className="min-w-0 text-right text-sm text-foreground">{children}</span>
    </div>
  )
}

export function SettingsPage() {
  const user = useAuthStore((state) => state.user)

  if (!user) {
    // AppShell 的 RequireAuth 已保证已登录；这里只是类型收窄与极端情况兜底
    return null
  }

  const unlimited = user.CapacityBytes <= 0

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <h1 className="text-2xl font-semibold tracking-tight text-foreground">
        {t('SETTINGS_TITLE')}
      </h1>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <IdCard className="size-4 text-muted-foreground" aria-hidden />
            {t('SETTINGS_SECTION_ACCOUNT')}
          </CardTitle>
          <CardDescription>{t('SETTINGS_EMAIL')} · {t('SETTINGS_ROLE')} · {t('SETTINGS_QUOTA')}</CardDescription>
        </CardHeader>
        <CardContent className="divide-y divide-border">
          <InfoRow label={t('SETTINGS_EMAIL')}>{user.Email}</InfoRow>
          <InfoRow label={t('SETTINGS_NICKNAME')}>
            {user.Nickname?.trim() || <span className="text-muted-foreground">—</span>}
          </InfoRow>
          <InfoRow label={t('SETTINGS_ROLE')}>
            <Badge variant={user.Role === 'admin' ? 'brand' : 'secondary'}>
              {user.Role === 'admin' ? t('USER_MENU_ROLE_ADMIN') : t('USER_MENU_ROLE_USER')}
            </Badge>
          </InfoRow>
          <InfoRow label={t('SETTINGS_STATUS')}>
            <Badge variant={user.Status === 'active' ? 'success' : 'destructive'}>
              {user.Status === 'active' ? 'active' : 'disabled'}
            </Badge>
          </InfoRow>
          <InfoRow label={t('SETTINGS_QUOTA')}>
            {unlimited
              ? t('QUOTA_UNLIMITED')
              : `${formatBytes(user.UsedBytes)} / ${formatBytes(user.CapacityBytes)}`}
          </InfoRow>
          <InfoRow label={t('SETTINGS_LAST_LOGIN')}>{formatDateTime(user.LastLoginAt)}</InfoRow>
          <InfoRow label={t('SETTINGS_CREATED_AT')}>{formatDateTime(user.CreatedAt)}</InfoRow>
          <InfoRow label="UID">
            <span className="font-mono text-xs">{user.UID}</span>
          </InfoRow>
        </CardContent>
      </Card>

      <Separator />

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <ShieldCheck className="size-4 text-muted-foreground" aria-hidden />
            {t('SETTINGS_SECTION_SECURITY')}
          </CardTitle>
        </CardHeader>
        <CardContent>
          <Alert variant="info">
            <AlertDescription>{t('SETTINGS_W8_NOTE')}</AlertDescription>
          </Alert>
        </CardContent>
      </Card>
    </div>
  )
}
