import { Navigate, Outlet, useLocation } from 'react-router'

import { ForbiddenPage } from '@/components/layout/error-pages'
import { Skeleton } from '@/components/ui/skeleton'
import { useAuthStore } from '@/store/auth-store'

/**
 * 路由守卫（DESIGN.md §3 / §8）。
 *
 * 三条规则：
 *  1. **登录态未知**（首屏探测中）→ 渲染骨架，避免「先闪登录页再跳回来」
 *  2. **未登录** → `/login?redirect=<当前地址>`（登录后能回到原处）
 *  3. **需要强制改密** → `/first-login`（D32；后端也会拦截其它接口）
 *
 * ⚠️ 前端守卫只是 UX；**真正的鉴权始终在后端 API**（DESIGN.md §10）。
 */

/** 首屏探测中的骨架（不显眼但足够占位）。 */
function AuthLoading() {
  return (
    <div className="flex min-h-svh items-center justify-center p-6">
      <div className="w-full max-w-sm space-y-3">
        <Skeleton className="h-8 w-40" />
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-10 w-full" />
        <Skeleton className="h-10 w-2/3" />
      </div>
    </div>
  )
}

/** 需登录。 */
export function RequireAuth() {
  const status = useAuthStore((state) => state.status)
  const mustChangePassword = useAuthStore((state) => state.user?.MustChangePassword === true)
  const location = useLocation()

  if (status === 'unknown') return <AuthLoading />

  if (status === 'anonymous') {
    const redirect = `${location.pathname}${location.search}`
    const target = redirect && redirect !== '/' ? `/login?redirect=${encodeURIComponent(redirect)}` : '/login'
    return <Navigate to={target} replace />
  }

  // 强制改密：其它页面一律让位（后端也会返回 40301「请先修改密码」）
  if (mustChangePassword && location.pathname !== '/first-login') {
    return <Navigate to="/first-login" replace />
  }

  return <Outlet />
}

/** 需管理员。非 admin 渲染 403（不跳转）。 */
export function RequireAdmin() {
  const role = useAuthStore((state) => state.user?.Role)
  if (role !== 'admin') return <ForbiddenPage />
  return <Outlet />
}

/**
 * 禁止已登录用户访问的页面（`/login` 等）。
 *
 * 已登录访问登录页 → 直接送回控制台（`/overview`）。
 * 注意：`MustChangePassword` 时先送 `/first-login`。
 */
export function RequireAnonymous() {
  const status = useAuthStore((state) => state.status)
  const mustChangePassword = useAuthStore((state) => state.user?.MustChangePassword === true)

  if (status === 'unknown') return <AuthLoading />
  if (status === 'authenticated') {
    // ⚠️ 用 `/overview` 而不是 `/`：`/` 在生产环境由主题渲染（D94）
    return <Navigate to={mustChangePassword ? '/first-login' : '/overview'} replace />
  }

  return <Outlet />
}
