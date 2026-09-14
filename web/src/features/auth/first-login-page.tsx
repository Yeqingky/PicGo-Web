import { useState } from 'react'
import { useNavigate } from 'react-router'

import { AuthLayout } from '@/features/auth/auth-layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { toast } from '@/components/ui/toast'
import { t } from '@/i18n'
import { api } from '@/lib/api'
import { toApiError } from '@/types/api'
import { useAuthStore } from '@/store/auth-store'

/**
 * 首次登录强制改密（D32 / DESIGN.md §3.1）。
 *
 * 触发条件：`Users.MustChangePassword = true`（首启引导生成的管理员、
 * 或管理员重置了他人密码）。后端会对**除 `/auth/me`、`/auth/password`、
 * `/auth/logout` 之外**的接口返回 40301「请先修改密码」。
 */
export function FirstLoginPage() {
  const navigate = useNavigate()
  const setUser = useAuthStore((state) => state.setUser)
  const user = useAuthStore((state) => state.user)

  const [oldPassword, setOldPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [formError, setFormError] = useState<string | null>(null)

  const onSubmit = async (event: React.FormEvent) => {
    event.preventDefault()
    setFormError(null)

    const errors: Record<string, string> = {}
    if (!newPassword) errors.newPassword = t('AUTH_FIRST_LOGIN_NEW_REQUIRED')
    else if (newPassword.length < 8) errors.newPassword = t('AUTH_FIRST_LOGIN_NEW_TOO_SHORT')
    if (confirmPassword !== newPassword) errors.confirmPassword = t('AUTH_FIRST_LOGIN_CONFIRM_MISMATCH')

    setFieldErrors(errors)
    if (Object.keys(errors).length > 0) return

    setSubmitting(true)
    try {
      await api.auth.changePassword(oldPassword, newPassword)

      // 后端在改密后会**吊销全部 refresh token**（API.md §1），因此必须重新登录。
      // 这里同步清掉本地快照，避免 UI 停留在「已登录」而实际已失效。
      setUser(null)
      toast.success(t('AUTH_FIRST_LOGIN_SUCCESS'))
      navigate('/login', { replace: true })
    } catch (err) {
      const apiError = toApiError(err)
      setFormError(apiError.message || t('AUTH_FIRST_LOGIN_FAILED'))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <AuthLayout
      title={t('AUTH_FIRST_LOGIN_TITLE')}
      description={t('AUTH_FIRST_LOGIN_DESC')}
      footer={user?.Email ? <span className="text-xs">{user.Email}</span> : undefined}
    >
      <div className="space-y-4">
        {formError ? (
          <Alert variant="destructive">
            <AlertDescription>{formError}</AlertDescription>
          </Alert>
        ) : null}

        <form onSubmit={onSubmit} className="space-y-4" noValidate>
          <div className="space-y-1.5">
            <Label htmlFor="first-login-old">{t('AUTH_FIRST_LOGIN_OLD')}</Label>
            <Input
              id="first-login-old"
              type="password"
              autoComplete="current-password"
              value={oldPassword}
              onChange={(event) => setOldPassword(event.target.value)}
              disabled={submitting}
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="first-login-new">{t('AUTH_FIRST_LOGIN_NEW')}</Label>
            <Input
              id="first-login-new"
              type="password"
              autoComplete="new-password"
              value={newPassword}
              onChange={(event) => setNewPassword(event.target.value)}
              aria-invalid={fieldErrors.newPassword ? true : undefined}
              aria-describedby={fieldErrors.newPassword ? 'first-login-new-error' : undefined}
              disabled={submitting}
            />
            {fieldErrors.newPassword ? (
              <p id="first-login-new-error" className="text-xs text-destructive">
                {fieldErrors.newPassword}
              </p>
            ) : null}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="first-login-confirm">{t('AUTH_FIRST_LOGIN_CONFIRM')}</Label>
            <Input
              id="first-login-confirm"
              type="password"
              autoComplete="new-password"
              value={confirmPassword}
              onChange={(event) => setConfirmPassword(event.target.value)}
              aria-invalid={fieldErrors.confirmPassword ? true : undefined}
              aria-describedby={
                fieldErrors.confirmPassword ? 'first-login-confirm-error' : undefined
              }
              disabled={submitting}
            />
            {fieldErrors.confirmPassword ? (
              <p id="first-login-confirm-error" className="text-xs text-destructive">
                {fieldErrors.confirmPassword}
              </p>
            ) : null}
          </div>

          <Button type="submit" variant="brand" className="w-full" disabled={submitting}>
            {submitting ? t('AUTH_FIRST_LOGIN_SUBMITTING') : t('AUTH_FIRST_LOGIN_SUBMIT')}
          </Button>
        </form>
      </div>
    </AuthLayout>
  )
}
