import { useEffect } from 'react'
import { Outlet } from 'react-router'

import { Sidebar } from '@/components/layout/sidebar'
import { Topbar } from '@/components/layout/topbar'
import { useSiteConfig, siteDisplayName } from '@/hooks/api'
import { useLatestVersion } from '@/hooks/use-latest-version'
import { Sheet, SheetContent, SheetTitle } from '@/components/ui/sheet'
import { cn } from '@/lib/utils'
import { useTaskStore } from '@/store/task-store'
import { bindUploadEvents } from '@/store/upload-store'
import { useUIStore } from '@/store/ui-store'

/**
 * 后台外壳（DESIGN.md §4.1）。
 *
 * 结构（应用式布局：整页锁定在视口内，只有内容区滚动）：
 *   ┌──────────────────────────────────────┐
 *   │ 顶栏 h-14（固定）                     │
 *   ├────────┬─────────────────────────────┤
 *   │ 侧栏   │ 内容区 max-w-[1400px]        │
 *   │ w-60   │ （唯一滚动容器）              │
 *   └────────┴─────────────────────────────┘
 *
 *   注意：根容器必须是 h-svh + overflow-hidden，内层行需要 min-h-0，
 *   否则 flex 子项会撑开整页、退化为 body 滚动，侧栏就会跟着滚走。
 *
 * 响应式：`lg+` 侧栏展开；`md` 收为图标条；移动端变 Sheet 抽屉。
 */
export function AppShell() {
  const { config } = useSiteConfig()
  const { latestVersion, updateAvailable } = useLatestVersion(config?.Site.Version)
  const collapsed = useUIStore((state) => state.sidebarCollapsed)
  const mobileNavOpen = useUIStore((state) => state.mobileNavOpen)
  const setMobileNavOpen = useUIStore((state) => state.setMobileNavOpen)

  const connectSSE = useTaskStore((state) => state.connectSSE)
  const disconnectSSE = useTaskStore((state) => state.disconnectSSE)

  // 登录态探测在 `App` 里统一做（见那里的说明），这里不再重复。
  // 进入外壳即建立 SSE（DESIGN.md §9.4）；离开时断开
  useEffect(() => {
    // 上传队列也依赖同一条共享 SSE 连接接收 finished / failed 事件。
    // 必须在连接建立时绑定, 否则队列会永久停留在 uploading.
    bindUploadEvents()
    connectSSE()
    return () => disconnectSSE()
  }, [connectSSE, disconnectSSE])

  return (
    <div className="flex h-svh flex-col overflow-hidden bg-background">
      <Topbar siteName={siteDisplayName(config)} />

      <div className="flex min-h-0 flex-1 overflow-hidden">
        {/* 桌面端侧栏（固定，导航区由 Sidebar 内部自行滚动） */}
        <aside
          className={cn(
            'hidden shrink-0 border-r border-border bg-background md:block',
            'overflow-hidden',
            collapsed ? 'md:w-14' : 'md:w-40 lg:w-60',
          )}
        >
          <Sidebar
            collapsed={collapsed}
            serverVersion={config?.Site.Version}
            latestVersion={latestVersion}
            updateAvailable={updateAvailable}
          />
        </aside>

        {/* 移动端抽屉 */}
        <Sheet open={mobileNavOpen} onOpenChange={setMobileNavOpen}>
          <SheetContent side="left" className="w-64 p-0" aria-describedby={undefined}>
            <SheetTitle className="sr-only">{siteDisplayName(config)}</SheetTitle>
            <Sidebar
              onNavigate={() => setMobileNavOpen(false)}
              serverVersion={config?.Site.Version}
              latestVersion={latestVersion}
              updateAvailable={updateAvailable}
            />
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
