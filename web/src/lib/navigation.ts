import {
  Album,
  ClipboardList,
  Images,
  LayoutDashboard,
  type LucideIcon,
  Puzzle,
  ScrollText,
  Server,
  Settings,
  SlidersHorizontal,
  Upload,
  Users,
} from 'lucide-react'

import { t } from '@/i18n'

/**
 * 侧边栏导航配置（DESIGN.md §3 / D77.2 扩展点）。
 *
 * ⚠️ **新增页面只需往这里加一项**，不要改 AppShell。
 *
 * 说明：
 *  - 路径与 DESIGN.md §3 的路由表严格一致
 *  - `adminOnly` 为真时仅 `Role === "admin"` 可见
 *  - 隐藏菜单**不是权限**：后端 API 必须独立鉴权（DESIGN.md §10）
 */

export interface NavItem {
  /** i18n key */
  labelKey: string
  to: string
  icon: LucideIcon
  adminOnly?: boolean
  /** 精确匹配（用于 `/` 这类会前缀匹配到所有路径的路由） */
  exact?: boolean
}

export interface NavGroup {
  /** 分组标题的 i18n key；`null` 表示不显示标题（第一组） */
  titleKey: string | null
  items: NavItem[]
}

export const navGroups: NavGroup[] = [
  {
    titleKey: null,
    items: [
      { labelKey: 'NAV_HOME', to: '/', icon: LayoutDashboard, exact: true },
      { labelKey: 'NAV_UPLOAD', to: '/upload', icon: Upload },
      { labelKey: 'NAV_GALLERY', to: '/gallery', icon: Images },
      { labelKey: 'NAV_ALBUMS', to: '/albums', icon: Album },
      { labelKey: 'NAV_JOBS', to: '/jobs', icon: ClipboardList },
      { labelKey: 'NAV_LOGS', to: '/logs', icon: ScrollText },
      { labelKey: 'NAV_SETTINGS', to: '/settings', icon: Settings },
    ],
  },
  {
    titleKey: 'NAV_GROUP_ADMIN',
    items: [
      { labelKey: 'NAV_ADMIN_USERS', to: '/admin/users', icon: Users, adminOnly: true },
      { labelKey: 'NAV_ADMIN_STORAGE', to: '/admin/storage', icon: Server, adminOnly: true },
      { labelKey: 'NAV_ADMIN_PLUGINS', to: '/admin/plugins', icon: Puzzle, adminOnly: true },
      { labelKey: 'NAV_ADMIN_THEMES', to: '/admin/themes', icon: SlidersHorizontal, adminOnly: true },
      { labelKey: 'NAV_ADMIN_SITE', to: '/admin/site', icon: Settings, adminOnly: true },
      { labelKey: 'NAV_ADMIN_LOGS', to: '/admin/logs', icon: ScrollText, adminOnly: true },
    ],
  },
]

/** 取导航项的展示文案（统一走 i18n，D75）。 */
export function navItemLabel(item: NavItem): string {
  return t(item.labelKey)
}

/** 分组标题（可能为 null）。 */
export function navGroupTitle(group: NavGroup): string | null {
  return group.titleKey ? t(group.titleKey) : null
}
