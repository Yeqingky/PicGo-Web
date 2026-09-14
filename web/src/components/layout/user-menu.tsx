import { FileText, LogOut, Settings as SettingsIcon, ShieldCheck, User as UserIcon } from 'lucide-react'
import { useNavigate } from 'react-router'

import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { t } from '@/i18n'
import { toast } from '@/components/ui/toast'
import { useAuthStore } from '@/store/auth-store'

/** 取昵称的首字符作为头像占位。 */
function initial(user: { Nickname?: string; Email: string }): string {
  const name = user.Nickname?.trim() || user.Email
  return name.slice(0, 1).toUpperCase()
}

/**
 * 用户菜单（DESIGN.md §4.1）。
 *
 * 含「个人设置 / 操作日志 / 退出登录」。
 */
export function UserMenu() {
  const user = useAuthStore((state) => state.user)
  const logout = useAuthStore((state) => state.logout)
  const navigate = useNavigate()

  if (!user) return null

  const onLogout = async () => {
    await logout()
    toast.success(t('LOGOUT_SUCCESS'))
    navigate('/login', { replace: true })
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className="rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
        aria-label={t('TOPBAR_USER_MENU')}
      >
        <Avatar>
          {user.AvatarURL ? <AvatarImage src={user.AvatarURL} alt="" /> : null}
          <AvatarFallback>{initial(user)}</AvatarFallback>
        </Avatar>
      </DropdownMenuTrigger>

      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel className="flex flex-col gap-0.5 py-2">
          <span className="truncate text-sm font-medium text-foreground">
            {user.Nickname?.trim() || user.Email}
          </span>
          <span className="truncate text-xs font-normal text-muted-foreground">{user.Email}</span>
          <span className="mt-0.5 inline-flex items-center gap-1 text-xs font-normal text-muted-foreground">
            {user.Role === 'admin' ? (
              <ShieldCheck className="size-3" aria-hidden />
            ) : (
              <UserIcon className="size-3" aria-hidden />
            )}
            {user.Role === 'admin' ? t('USER_MENU_ROLE_ADMIN') : t('USER_MENU_ROLE_USER')}
          </span>
        </DropdownMenuLabel>

        <DropdownMenuSeparator />

        <DropdownMenuItem onSelect={() => navigate('/settings')}>
          <SettingsIcon aria-hidden />
          {t('USER_MENU_PROFILE')}
        </DropdownMenuItem>

        <DropdownMenuItem onSelect={() => navigate('/logs')}>
          <FileText aria-hidden />
          {t('USER_MENU_LOGS')}
        </DropdownMenuItem>

        <DropdownMenuSeparator />

        <DropdownMenuItem variant="destructive" onSelect={() => void onLogout()}>
          <LogOut aria-hidden />
          {t('USER_MENU_LOGOUT')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
