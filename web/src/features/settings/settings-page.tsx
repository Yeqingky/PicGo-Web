import { Check, KeyRound, Loader2, Plus, ShieldCheck, Trash2, User as UserIcon } from 'lucide-react'
import { useState } from 'react'

import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { GithubMark } from '@/components/icons/github-mark'
import { PageHeader } from '@/components/common/page-header'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Separator } from '@/components/ui/separator'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { toast } from '@/components/ui/toast'
import { useAsync } from '@/hooks/api'
import { t } from '@/i18n'
import { apiTokensApi, authApi } from '@/lib/api'
import { formatBytes, formatDateTime } from '@/lib/format'
import { toApiError, type APIToken } from '@/types/api'
import { useAuthStore } from '@/store/auth-store'
import { useUIStore, type ThemeMode } from '@/store/ui-store'

/**
 * 个人设置（DESIGN.md §3.2 `/settings`）。
 *
 * 四块：
 *  1. **账号信息**（只读）
 *  2. **修改密码**
 *  3. **第三方登录**（GitHub 绑定 / 解绑）
 *  4. **API Token**（明文只显示一次）
 *
 * ⚠️ **资料（昵称/头像/主页）在后端只有 admin 的 `PATCH /users/{Uid}`**，
 *    没有「用户自助修改资料」的端点；且 `UserProfiles` 是独立表，
 *    若走 `PUT /settings` 会把同一语义分散到两处（`UserProfiles` 与 `UserSettings`）。
 *    因此这里**把资料做成只读展示**并注明由管理员维护，而不是伪造一个后端不存在的契约。
 *    （已在交付说明中列为「与文档不一致 / 待补接口」。）
 */
export function SettingsPage() {
  const user = useAuthStore((state) => state.user)
  const theme = useUIStore((state) => state.theme)
  const setTheme = useUIStore((state) => state.setTheme)

  if (!user) return null

  const unlimited = user.CapacityBytes <= 0

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <PageHeader title={t('SETTINGS_TITLE')} description={t('SETTINGS_DESC')} />

      {/* 1. 账号信息（只读） */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <UserIcon className="size-4 text-muted-foreground" aria-hidden />
            {t('SETTINGS_SECTION_ACCOUNT')}
          </CardTitle>
          <CardDescription>{t('SETTINGS_ACCOUNT_DESC')}</CardDescription>
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
              {user.Status === 'active' ? t('STATUS_ACTIVE') : t('STATUS_DISABLED')}
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

      {/* 偏好：主题（落本地 + 可同步到服务端） */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <ShieldCheck className="size-4 text-muted-foreground" aria-hidden />
            {t('SETTINGS_SECTION_PREFERENCE')}
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex items-center justify-between gap-4">
            <div className="space-y-0.5">
              <Label htmlFor="pref-theme">{t('THEME_LABEL')}</Label>
              <p className="text-xs text-muted-foreground">{t('SETTINGS_THEME_HINT')}</p>
            </div>
            <Select value={theme} onValueChange={(value) => setTheme(value as ThemeMode)}>
              <SelectTrigger id="pref-theme" className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="light">{t('THEME_LIGHT')}</SelectItem>
                <SelectItem value="dark">{t('THEME_DARK')}</SelectItem>
                <SelectItem value="system">{t('THEME_SYSTEM')}</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </CardContent>
      </Card>

      {/* 2. 修改密码 */}
      <ChangePasswordCard hasPassword={user.HasPassword !== false} />

      {/* 3. 第三方登录 */}
      <IdentitiesCard />

      {/* 4. API Token */}
      <APITokensCard />
    </div>
  )
}

// ---------------------------------------------------------------------------

function InfoRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 py-2.5">
      <span className="shrink-0 text-sm text-muted-foreground">{label}</span>
      <span className="min-w-0 break-all text-right text-sm text-foreground">{children}</span>
    </div>
  )
}

function ChangePasswordCard({ hasPassword }: { hasPassword: boolean }) {
  const [oldPassword, setOldPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const submit = async () => {
    if (newPassword.length < 8) {
      setError(t('AUTH_FIRST_LOGIN_NEW_TOO_SHORT'))
      return
    }
    if (newPassword !== confirm) {
      setError(t('AUTH_FIRST_LOGIN_CONFIRM_MISMATCH'))
      return
    }

    setBusy(true)
    setError('')
    try {
      await authApi.changePassword(oldPassword, newPassword)
      toast.success(t('SETTINGS_PASSWORD_CHANGED'))
      setOldPassword('')
      setNewPassword('')
      setConfirm('')
    } catch (err) {
      setError(toApiError(err).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <KeyRound className="size-4 text-muted-foreground" aria-hidden />
          {t('SETTINGS_SECTION_SECURITY')}
        </CardTitle>
        <CardDescription>{t('SETTINGS_PASSWORD_DESC')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {hasPassword ? (
          <div className="space-y-1.5">
            <Label htmlFor="old-password">{t('AUTH_FIRST_LOGIN_OLD')}</Label>
            <Input
              id="old-password"
              type="password"
              autoComplete="current-password"
              value={oldPassword}
              onChange={(event) => setOldPassword(event.target.value)}
            />
          </div>
        ) : null}

        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1.5">
            <Label htmlFor="new-password">{t('AUTH_FIRST_LOGIN_NEW')}</Label>
            <Input
              id="new-password"
              type="password"
              autoComplete="new-password"
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="confirm-password">{t('AUTH_FIRST_LOGIN_CONFIRM')}</Label>
            <Input
              id="confirm-password"
              type="password"
              autoComplete="new-password"
              value={confirm}
              onChange={(event) => setConfirm(event.target.value)}
            />
          </div>
        </div>

        {error ? <p className="text-xs text-destructive">{error}</p> : null}

        <Button variant="brand" disabled={busy} onClick={() => void submit()}>
          {busy ? <Loader2 className="size-4 animate-spin" aria-hidden /> : null}
          {t('SETTINGS_PASSWORD_SUBMIT')}
        </Button>
      </CardContent>
    </Card>
  )
}

function IdentitiesCard() {
  const reload = useAuthStore((state) => state.bootstrap)
  const state = useAsync(() => authApi.identities(), [])
  const providers = useAsync(() => authApi.oauthProviders(), [])

  const [busy, setBusy] = useState(false)

  const githubBound = state.data?.Identities?.some((item) => item.Provider === 'github') ?? false

  const bind = async () => {
    setBusy(true)
    try {
      const res = await authApi.bindOAuth('github', window.location.pathname)
      window.location.assign(res.AuthorizeURL)
    } catch (err) {
      toast.error(toApiError(err).message)
      setBusy(false)
    }
  }

  const unbind = async () => {
    setBusy(true)
    try {
      await authApi.unbindOAuth('github')
      toast.success(t('SETTINGS_IDENTITY_UNBOUND'))
      state.refresh()
      void reload()
    } catch (err) {
      // 后端在「无密码且无其他身份」时会拒绝，防锁死账号 —— 原因会带在 Message 里
      toast.error(toApiError(err).message)
    } finally {
      setBusy(false)
    }
  }

  const githubEnabled = (providers.data?.Providers ?? []).some((item) => item.Name === 'github')

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <GithubMark className="size-4 text-muted-foreground" aria-hidden />
          {t('SETTINGS_SECTION_IDENTITIES')}
        </CardTitle>
        <CardDescription>{t('SETTINGS_IDENTITIES_DESC')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {state.loading ? (
          <Skeleton className="h-10 w-full" />
        ) : !githubEnabled ? (
          <Alert variant="info">
            <AlertDescription>{t('SETTINGS_IDENTITY_DISABLED')}</AlertDescription>
          </Alert>
        ) : (
          <div className="flex items-center justify-between gap-4 rounded-md border border-border px-3 py-2.5">
            <div className="flex items-center gap-2">
              <GithubMark className="size-4" aria-hidden />
              <span className="text-sm">GitHub</span>
              {githubBound ? (
                <Badge variant="success" className="gap-1">
                  <Check className="size-3" aria-hidden />
                  {t('SETTINGS_IDENTITY_BOUND')}
                </Badge>
              ) : (
                <Badge variant="secondary">{t('SETTINGS_IDENTITY_UNBOUND_BADGE')}</Badge>
              )}
            </div>

            {githubBound ? (
              <Button variant="outline" size="sm" disabled={busy} onClick={() => void unbind()}>
                {t('SETTINGS_IDENTITY_UNBIND')}
              </Button>
            ) : (
              <Button variant="brand" size="sm" disabled={busy} onClick={() => void bind()}>
                {t('SETTINGS_IDENTITY_BIND')}
              </Button>
            )}
          </div>
        )}

        {githubBound ? (
          <p className="text-xs text-muted-foreground">{t('SETTINGS_IDENTITY_UNBIND_HINT')}</p>
        ) : (
          <p className="text-xs text-muted-foreground">{t('SETTINGS_IDENTITY_BIND_HINT')}</p>
        )}
      </CardContent>
    </Card>
  )
}

function APITokensCard() {
  const state = useAsync(() => apiTokensApi.list(), [])

  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const [expiresInDays, setExpiresInDays] = useState('365')
  const [busy, setBusy] = useState(false)
  /** 新建后**只显示一次**的明文（D31） */
  const [plainToken, setPlainToken] = useState('')
  const [revoking, setRevoking] = useState<APIToken | null>(null)

  const create = async () => {
    const trimmed = name.trim()
    if (!trimmed) {
      toast.error(t('TOKEN_NAME_REQUIRED'))
      return
    }

    setBusy(true)
    try {
      const days = Number(expiresInDays)
      const res = await apiTokensApi.create({
        Name: trimmed,
        ExpiresInDays: Number.isFinite(days) && days > 0 ? days : 0,
      })
      setPlainToken(res.Token)
      setName('')
      setCreating(false)
      state.refresh()
    } catch (err) {
      toast.error(toApiError(err).message)
    } finally {
      setBusy(false)
    }
  }

  const confirmRevoke = async () => {
    if (!revoking) return
    await apiTokensApi.revoke(revoking.UID)
    toast.success(t('TOKEN_REVOKED'))
    state.refresh()
  }

  const tokens: APIToken[] = state.data ?? []

  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
        <div className="space-y-1">
          <CardTitle className="flex items-center gap-2 text-base">
            <ShieldCheck className="size-4 text-muted-foreground" aria-hidden />
            {t('SETTINGS_SECTION_TOKENS')}
          </CardTitle>
          <CardDescription>{t('SETTINGS_TOKENS_DESC')}</CardDescription>
        </div>
        <Button variant="outline" size="sm" onClick={() => setCreating(true)}>
          <Plus aria-hidden />
          {t('TOKEN_CREATE')}
        </Button>
      </CardHeader>

      <CardContent className="space-y-3">
        {/* 明文只显示一次（D31） */}
        {plainToken ? (
          <Alert variant="warning">
            <AlertDescription className="space-y-2">
              <p className="font-medium">{t('TOKEN_PLAIN_ONCE')}</p>
              <div className="flex items-center gap-2">
                <code className="min-w-0 flex-1 break-all rounded bg-muted px-2 py-1 font-mono text-xs">
                  {plainToken}
                </code>
                <Button
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  onClick={() => {
                    void navigator.clipboard?.writeText(plainToken)
                    toast.success(t('COMMON_COPIED'))
                  }}
                >
                  {t('COMMON_COPY')}
                </Button>
              </div>
              <Button variant="ghost" size="sm" onClick={() => setPlainToken('')}>
                {t('TOKEN_PLAIN_DISMISS')}
              </Button>
            </AlertDescription>
          </Alert>
        ) : null}

        {creating ? (
          <div className="space-y-3 rounded-md border border-border p-3">
            <div className="space-y-1.5">
              <Label htmlFor="token-name">{t('TOKEN_NAME')}</Label>
              <Input
                id="token-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder={t('TOKEN_NAME_PLACEHOLDER')}
                autoFocus
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="token-expires">{t('TOKEN_EXPIRES')}</Label>
              <Select value={expiresInDays} onValueChange={setExpiresInDays}>
                <SelectTrigger id="token-expires">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="30">{t('TOKEN_EXPIRES_30')}</SelectItem>
                  <SelectItem value="90">{t('TOKEN_EXPIRES_90')}</SelectItem>
                  <SelectItem value="365">{t('TOKEN_EXPIRES_365')}</SelectItem>
                  <SelectItem value="0">{t('TOKEN_EXPIRES_NEVER')}</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="flex items-center gap-2">
              <Button variant="brand" size="sm" disabled={busy} onClick={() => void create()}>
                {busy ? <Loader2 className="size-4 animate-spin" aria-hidden /> : null}
                {t('TOKEN_CREATE_SUBMIT')}
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setCreating(false)}>
                {t('COMMON_CANCEL')}
              </Button>
            </div>
          </div>
        ) : null}

        {state.loading ? (
          <Skeleton className="h-12 w-full" />
        ) : tokens.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t('TOKEN_EMPTY')}</p>
        ) : (
          <ul className="divide-y divide-border overflow-hidden rounded-md border border-border">
            {tokens.map((token) => (
              <li key={token.UID} className="flex items-center gap-3 px-3 py-2.5">
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm text-foreground">{token.Name}</p>
                  <p className="text-xs text-muted-foreground">
                    <span className="font-mono">{token.Prefix}…</span>
                    {' · '}
                    {token.ExpiresAt === 0
                      ? t('TOKEN_EXPIRES_NEVER')
                      : t('TOKEN_EXPIRES_AT', { time: formatDateTime(token.ExpiresAt) })}
                    {token.LastUsedAt > 0
                      ? ` · ${t('TOKEN_LAST_USED', { time: formatDateTime(token.LastUsedAt) })}`
                      : ` · ${t('TOKEN_NEVER_USED')}`}
                  </p>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  className="shrink-0 text-destructive hover:text-destructive"
                  onClick={() => setRevoking(token)}
                >
                  <Trash2 aria-hidden />
                  {t('TOKEN_REVOKE')}
                </Button>
              </li>
            ))}
          </ul>
        )}

        <Separator />
        <p className="text-xs text-muted-foreground">
          {t('SETTINGS_TOKENS_USAGE_HINT')}
        </p>
      </CardContent>

      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(open) => !open && setRevoking(null)}
        title={t('TOKEN_REVOKE_TITLE', { name: revoking?.Name ?? '' })}
        description={t('TOKEN_REVOKE_DESC')}
        confirmLabel={t('TOKEN_REVOKE')}
        destructive
        onConfirm={confirmRevoke}
      />
    </Card>
  )
}
