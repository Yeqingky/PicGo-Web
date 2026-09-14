import { WifiOff } from 'lucide-react'
import { useEffect } from 'react'
import { Outlet } from 'react-router'

import { Sidebar } from '@/components/layout/sidebar'
import { Topbar } from '@/components/layout/topbar'
import { useSiteConfig, siteDisplayName } from '@/hooks/api'
import { t } from '@/i18n'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Sheet, SheetContent, SheetTitle } from '@/components/ui/sheet'
import { cn } from '@/lib/utils'
import { useTaskStore } from '@/store/task-store'
import { useUIStore } from '@/store/ui-store'

/**
 * 后台外壳（DESIGN.md §4.1）。
 *
 * 结构：
 *   ┌──────────────────────────────────────┐
 *   │ 顶栏 h-14 吸顶                        │
 *   ├────────┬─────────────────────────────┤
 *   │ 侧栏   │ 内容区 max-w-[1400px]        │
 *   │ w-60   │                             │
 *   └────────┴─────────────────────────────┘
 *
 * 响应式：`lg+` 侧栏展开；`md` 收为图标条；移动端变 Sheet 抽屉。
 */
export function AppShell() {
  const { config } = useSiteConfig()
  const collapsed = useUIStore((state) => state.sidebarCollapsed)
  const mobileNavOpen = useUIStore((state) => state.mobileNavOpen)
  const setMobileNavOpen = useUIStore((state) => state.setMobileNavOpen)

  const connectSSE = useTaskStore((state) => state.connectSSE)
  const disconnectSSE = useTaskStore((state) => state.disconnectSSE)
  const sseStatus = useTaskStore((state) => state.sseStatus)
  const sseEverConnected = useTaskStore((state) => state.sseEverConnected)

  // 登录态探测在 `App` 里统一做（见那里的说明），这里不再重复。
  // 进入外壳即建立 SSE（DESIGN.md §9.4）；离开时断开
  useEffect(() => {
    connectSSE()
    return () => disconnectSSE()
  }, [connectSSE, disconnectSSE])

  const showSSEBanner = sseStatus === 'reconnecting' || (sseEverConnected && sseStatus === 'closed')

  return (
    <div className="flex min-h-svh flex-col bg-background">
      <Topbar siteName={siteDisplayName(config)} />

      {showSSEBanner ? (
        <Alert
          variant="warning"
          className="flex items-center gap-2 rounded-none border-x-0 border-t-0 py-2"
        >
          <WifiOff aria-hidden />
          <AlertDescription className="text-warning">{t('SSE_DISCONNECTED')}</AlertDescription>
        </Alert>
      ) : null}

      <div className="flex flex-1 overflow-hidden">
        {/* 桌面端侧栏（固定，内容区独立滚动） */}
        <aside
          className={cn(
            'hidden shrink-0 border-r border-border bg-background md:block',
            'sticky top-14 h-[calc(100svh-3.5rem)] overflow-hidden',
            collapsed ? 'md:w-14' : 'md:w-40 lg:w-60',
          )}
        >
          <Sidebar collapsed={collapsed} />
        </aside>

        {/* 移动端抽屉 */}
        <Sheet open={mobileNavOpen} onOpenChange={setMobileNavOpen}>
          <SheetContent side="left" className="w-64 p-0" aria-describedby={undefined}>
            <SheetTitle className="sr-only">{siteDisplayName(config)}</SheetTitle>
            <Sidebar onNavigate={() => setMobileNavOpen(false)} />
          </SheetContent>
        </Sheet>

        {/* 内容区 */}
        <main className="min-w-0 flex-1 overflow-y-auto scrollbar-thin">
          <div className="mx-auto w-full max-w-[1400px] px-4 py-6 md:px-6">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  )
}
