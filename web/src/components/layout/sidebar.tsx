import { NavLink } from 'react-router'

import { SidebarFooter } from '@/components/layout/sidebar-footer'
import { navGroupTitle, navGroups, navItemLabel, type NavItem } from '@/lib/navigation'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/store/auth-store'

interface SidebarProps {
  /** 收起为图标条（桌面端 `md` 断点或用户手动折叠） */
  collapsed?: boolean
  /** 点击导航项后的回调（移动端抽屉用来关闭自己） */
  onNavigate?: () => void
  /** 当前 PicGo-Web 服务版本（来自公开站点配置）。 */
  serverVersion?: string
  /** GitHub 最新正式 Release 版本。 */
  latestVersion?: string
  /** GitHub 最新正式 Release 是否高于当前版本。 */
  updateAvailable?: boolean
  className?: string
}

/**
 * 侧边栏（DESIGN.md §4.1）。
 *
 * - 导航项由 `lib/navigation.ts` 的配置数组驱动，**新增页面只改那里**
 * - 当前项用强调色 `--brand`（DESIGN.md §2.1：强调色只用于主 CTA、当前导航项、聚焦环、进度条）
 * - 「管理」分组仅管理员渲染 —— 但**隐藏菜单不是权限**，后端仍独立鉴权
 */
export function Sidebar({
  collapsed = false,
  onNavigate,
  serverVersion,
  latestVersion,
  updateAvailable = false,
  className,
}: SidebarProps) {
  const isAdmin = useAuthStore((state) => state.user?.Role === 'admin')

  return (
    <div className={cn('flex h-full flex-col', className)}>
      <nav className="flex-1 overflow-y-auto scrollbar-thin px-2 py-3" aria-label="主导航">
        {navGroups.map((group, groupIndex) => {
          const items = group.items.filter((item: NavItem) => !item.adminOnly || isAdmin)
          if (items.length === 0) return null

          const title = navGroupTitle(group)

          return (
            <div key={group.titleKey ?? `group-${groupIndex}`} className="mb-3 last:mb-0">
              {title && !collapsed ? (
                <p className="px-3 pb-1.5 pt-2 text-xs font-medium uppercase tracking-wide text-muted-foreground">
                  {title}
                </p>
              ) : null}
              {title && collapsed ? (
                <div className="mx-3 mb-2 mt-2 border-t border-border" aria-hidden />
              ) : null}

              <ul className="space-y-0.5">
                {items.map((item) => {
                  const Icon = item.icon
                  const label = navItemLabel(item)

                  return (
                    <li key={item.to}>
                      <NavLink
                        to={item.to}
                        end={item.exact}
                        onClick={onNavigate}
                        title={collapsed ? label : undefined}
                        className={({ isActive }) =>
                          cn(
                            'flex items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium transition-colors duration-150',
                            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1',
                            collapsed && 'justify-center px-2',
                            isActive
                              ? 'bg-brand/10 text-brand'
                              : 'text-muted-foreground hover:bg-accent hover:text-foreground',
                          )
                        }
                      >
                        <Icon className="size-4 shrink-0" aria-hidden />
                        {collapsed ? <span className="sr-only">{label}</span> : <span>{label}</span>}
                      </NavLink>
                    </li>
                  )
                })}
              </ul>
            </div>
          )
        })}
      </nav>

      <div className="border-t border-border">
        <SidebarFooter
          collapsed={collapsed}
          serverVersion={serverVersion}
          latestVersion={latestVersion}
          updateAvailable={updateAvailable}
        />
      </div>
    </div>
  )
}
