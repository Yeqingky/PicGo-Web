import { Menu, PanelLeftClose, PanelLeftOpen, Search } from 'lucide-react'
import { useEffect, useState } from 'react'

import { CommandPalette } from '@/components/layout/command-palette'

import { IconButton } from '@/components/ui/icon-button'
import { ThemeToggle } from '@/components/layout/theme-toggle'
import { UserMenu } from '@/components/layout/user-menu'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/store/auth-store'
import { useUIStore } from '@/store/ui-store'

interface TopbarProps {
  /** 站点名（来自公开的 site config；缺失时回退到内置名称） */
  siteName: string
  className?: string
}

/**
 * 顶栏（DESIGN.md §4.1，h-14 吸顶）。
 *
 * 左侧：移动端汉堡 + 桌面端折叠按钮 + 站点名
 * 右侧：命令面板（⌘K，暂为占位）、主题切换、用户菜单
 */
export function Topbar({ siteName, className }: TopbarProps) {
  const user = useAuthStore((state) => state.user)
  const [paletteOpen, setPaletteOpen] = useState(false)

  // `⌘/Ctrl + K` 打开命令面板（DESIGN.md §9.1）—— 全局快捷键，挂在 window 上
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setPaletteOpen((prev) => !prev)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])
  const collapsed = useUIStore((state) => state.sidebarCollapsed)
  const toggleSidebar = useUIStore((state) => state.toggleSidebar)
  const setMobileNavOpen = useUIStore((state) => state.setMobileNavOpen)

  return (
    <header
      className={cn(
        'sticky top-0 z-10 flex h-14 shrink-0 items-center gap-2 border-b border-border bg-background/95 px-3 backdrop-blur',
        className,
      )}
    >
      {/* 移动端：打开抽屉 */}
      <IconButton
        label={t('TOPBAR_OPEN_MENU')}
        className="md:hidden"
        onClick={() => setMobileNavOpen(true)}
      >
        <Menu className="size-4" aria-hidden />
      </IconButton>

      {/* 桌面端：折叠/展开侧栏 */}
      <IconButton
        label={collapsed ? t('COMMON_EXPAND') : t('COMMON_COLLAPSE')}
        className="hidden md:inline-flex"
        onClick={toggleSidebar}
      >
        {collapsed ? (
          <PanelLeftOpen className="size-4" aria-hidden />
        ) : (
          <PanelLeftClose className="size-4" aria-hidden />
        )}
      </IconButton>

      <span className="truncate text-sm font-semibold text-foreground">{siteName}</span>

      <div className="ml-auto flex items-center gap-1">
        <IconButton
          label={t('TOPBAR_COMMAND_PALETTE')}
          onClick={() => setPaletteOpen(true)}
        >
          <Search className="size-4" aria-hidden />
        </IconButton>

        <ThemeToggle />

        {user ? <UserMenu /> : null}
      </div>

      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
    </header>
  )
}
