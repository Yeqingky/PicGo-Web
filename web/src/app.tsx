import { useEffect } from 'react'
import { RouterProvider } from 'react-router'

import { ThemeProvider } from '@/components/theme-provider'
import { Toaster } from '@/components/ui/toast'
import { TooltipProvider } from '@/components/ui/tooltip'
import { router } from '@/router'
import { useAuthStore } from '@/store/auth-store'

/**
 * 内置 SPA 的根组件。
 *
 * 全局 Provider（顺序有讲究）：
 *  1. `ThemeProvider`：只同步 `<html class="dark">`，不产生 DOM 包裹
 *  2. `TooltipProvider`：Radix Tooltip 的共享上下文（避免每个 Tooltip 各自计时）
 *  3. `RouterProvider`：路由
 *  4. `Toaster`：Toast 容器（不受路由影响，放在最外层）
 */
export function App() {
  const bootstrap = useAuthStore((state) => state.bootstrap)

  // 登录态探测放在**应用级**，而不是 AppShell 里：
  // 未登录访问 `/login` 时 AppShell 不会挂载（被 RequireAnonymous 拦住），
  // 若探测只在 AppShell 里做，`status` 会永远是 `unknown`，
  // guard 就会停在加载态（这是个真实的 bug，已由浏览器验证抓到）。
  //
  // 无条件调用一次：即使 localStorage 里有上次的用户快照，也要用
  // `GET /auth/me` 校验它是否仍然有效（cookie 可能已过期/被吊销）。
  useEffect(() => {
    void bootstrap()
  }, [bootstrap])

  return (
    <ThemeProvider>
      <TooltipProvider delayDuration={300}>
        <RouterProvider router={router} />
        <Toaster />
      </TooltipProvider>
    </ThemeProvider>
  )
}
