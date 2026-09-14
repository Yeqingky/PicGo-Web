import { useMemo, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'

import { GithubMark } from '@/components/icons/github-mark'
import { NoSignupHint, AuthLayout } from '@/features/auth/auth-layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { useAsync, useSiteConfig } from '@/hooks/api'
import { t } from '@/i18n'
import { api } from '@/lib/api'
import { toApiError, ApiCode } from '@/types/api'
import { useAuthStore } from '@/store/auth-store'

/**
 * 邮箱格式校验（前端只挡明显的输入错误，真正的校验在后端）。
 *
 * ⚠️ **必须接受无点域名**（如 `admin@localhost`）：
 *    首启引导创建的管理员就是 `admin@localhost`（`server/internal/auth/bootstrap.go`
 *    的 `BootstrapAdminEmail`）。早期版本硬要求 `@` 后面有点，
 *    导致默认管理员**根本无法登录**（已由真实后端联调发现）。
 *    RFC 5321 允许无点域名，因此这里只要求「一个 @、两侧非空、无空白字符」。
 */
function isValidEmail(value: string): boolean {
  return /^[^\s@]+@[^\s@]+$/.test(value)
}

/** 把 URL 上的 `?error=` 映射成一句可读的提示（来自 OAuth 回调）。 */
function loginErrorFromQuery(reason: string | null): string | null {
  if (!reason) return null
  switch (reason) {
    case 'not_bound':
      return t('AUTH_LOGIN_ERROR_NOT_BOUND')
    case 'disabled':
      return t('AUTH_LOGIN_ERROR_DISABLED')
    case 'state_invalid':
      return t('AUTH_LOGIN_ERROR_STATE_INVALID')
    case 'exchange_failed':
      return t('AUTH_LOGIN_ERROR_EXCHANGE_FAILED')
    default:
      return t('AUTH_LOGIN_ERROR_UNKNOWN')
  }
}

/**
 * 登录页（DESIGN.md §4.3 / D23 / D25 / D26 / D27）。
 *
 * 要点：
 *  - **只认邮箱**（D23），无用户名
 *  - **无注册入口**（D25）
 *  - GitHub 按钮：仅当站点已启用 OAuth 时展示；且**必须先绑定才能用**（D27），
 *    因此按钮旁给出说明
 *  - 登录失败**不区分**「邮箱不存在 / 密码错误」（防枚举）
 *  - 背景用中性装饰背景，**不消费主题配置**（DESIGN.md §4.3 的安全理由，见 auth-layout.tsx）
 */
export function LoginPage() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const { config } = useSiteConfig()

  const login = useAuthStore((state) => state.login)

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [remember, setRemember] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [fieldErrors, setFieldErrors] = useState<{ email?: string; password?: string }>({})
  const [formError, setFormError] = useState<string | null>(null)

  // OAuth provider 列表（未配置则空数组 → 不渲染 GitHub 按钮，D26）
  const providers = useAsync(() => api.auth.oauthProviders(), [])
  const githubEnabled = useMemo(
    () => (providers.data?.Providers ?? []).some((p) => p.Name === 'github'),
    [providers.data],
  )

  /** 登录成功后的跳转目标（只接受站内相对路径，防开放重定向）。 */
  const redirectTo = useMemo(() => {
    const raw = searchParams.get('redirect')
    if (!raw || !raw.startsWith('/') || raw.startsWith('//')) return '/'
    return raw
  }, [searchParams])

  const oauthError = loginErrorFromQuery(searchParams.get('error'))

  const onSubmit = async (event: React.FormEvent) => {
    event.preventDefault()
    setFormError(null)

    const errors: { email?: string; password?: string } = {}
    if (!email.trim()) errors.email = t('AUTH_LOGIN_EMAIL_REQUIRED')
    else if (!isValidEmail(email.trim())) errors.email = t('AUTH_LOGIN_EMAIL_INVALID')
    if (!password) errors.password = t('AUTH_LOGIN_PASSWORD_REQUIRED')

    setFieldErrors(errors)
    if (Object.keys(errors).length > 0) return

    setSubmitting(true)
    try {
      const user = await login(email.trim(), password, remember)
      // 需要强制改密 → 先去改密页（D32）
      if (user.MustChangePassword) {
        navigate('/first-login', { replace: true })
        return
      }
      navigate(redirectTo, { replace: true })
    } catch (err) {
      const apiError = toApiError(err)
      // 统一文案，**不区分**邮箱不存在与密码错误（防枚举）
      if (apiError.Code === ApiCode.BadCredentials) {
        setFormError(t('AUTH_LOGIN_FAILED'))
      } else {
        setFormError(apiError.message)
      }
    } finally {
      setSubmitting(false)
    }
  }

  const onGithubLogin = () => {
    // OAuth 是「已绑定用户的便捷登录」，未绑定会由后端 302 回 /login?error=not_bound（D27）
    const start = `/api/web/v1/auth/oauth/github/start?Redirect=${encodeURIComponent(redirectTo)}`
    window.location.assign(start)
  }

  return (
    <AuthLayout title={t('AUTH_LOGIN_TITLE')} description={t('AUTH_LOGIN_SUBTITLE', { site: config?.Site.Name ?? 'PicGo Web' })}>
      <div className="space-y-4">
        {oauthError ? (
          <Alert variant="destructive">
            <AlertDescription>{oauthError}</AlertDescription>
          </Alert>
        ) : null}

        {formError ? (
          <Alert variant="destructive">
            <AlertDescription>{formError}</AlertDescription>
          </Alert>
        ) : null}

        <form onSubmit={onSubmit} className="space-y-4" noValidate>
          <div className="space-y-1.5">
            <Label htmlFor="login-email">{t('AUTH_LOGIN_EMAIL')}</Label>
            <Input
              id="login-email"
              name="email"
              type="email"
              autoComplete="email"
              inputMode="email"
              autoFocus
              placeholder={t('AUTH_LOGIN_EMAIL_PLACEHOLDER')}
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              aria-invalid={fieldErrors.email ? true : undefined}
              aria-describedby={fieldErrors.email ? 'login-email-error' : undefined}
              disabled={submitting}
            />
            {fieldErrors.email ? (
              <p id="login-email-error" className="text-xs text-destructive">
                {fieldErrors.email}
              </p>
            ) : null}
          </div>

          <div className="space-y-1.5">
            <div className="flex items-center justify-between gap-2">
              <Label htmlFor="login-password">{t('AUTH_LOGIN_PASSWORD')}</Label>
              <Link
                to="/forgot-password"
                className="text-xs text-muted-foreground transition-colors hover:text-brand"
              >
                {t('AUTH_LOGIN_FORGOT')}
              </Link>
            </div>
            <Input
              id="login-password"
              name="password"
              type="password"
              autoComplete="current-password"
              placeholder={t('AUTH_LOGIN_PASSWORD_PLACEHOLDER')}
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              aria-invalid={fieldErrors.password ? true : undefined}
              aria-describedby={fieldErrors.password ? 'login-password-error' : undefined}
              disabled={submitting}
            />
            {fieldErrors.password ? (
              <p id="login-password-error" className="text-xs text-destructive">
                {fieldErrors.password}
              </p>
            ) : null}
          </div>

          <div className="flex items-center gap-2">
            <Checkbox
              id="login-remember"
              checked={remember}
              onCheckedChange={(checked) => setRemember(checked === true)}
              disabled={submitting}
            />
            <Label htmlFor="login-remember" className="text-sm font-normal text-muted-foreground">
              {t('AUTH_LOGIN_REMEMBER')}
            </Label>
          </div>

          <Button type="submit" variant="brand" className="w-full" disabled={submitting}>
            {submitting ? t('AUTH_LOGIN_SUBMITTING') : t('AUTH_LOGIN_SUBMIT')}
          </Button>
        </form>

        {/* GitHub 登录：未配置 provider 时完全不渲染（D26） */}
        {providers.loading ? <Skeleton className="h-10 w-full" /> : null}
        {!providers.loading && githubEnabled ? (
          <TooltipProvider delayDuration={200}>
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  type="button"
                  variant="outline"
                  className="w-full"
                  onClick={onGithubLogin}
                  disabled={submitting}
                >
                  <GithubMark />
                  {t('AUTH_LOGIN_WITH_GITHUB')}
                </Button>
              </TooltipTrigger>
              <TooltipContent>{t('AUTH_LOGIN_GITHUB_HINT')}</TooltipContent>
            </Tooltip>
          </TooltipProvider>
        ) : null}

        <NoSignupHint />
      </div>
    </AuthLayout>
  )
}
