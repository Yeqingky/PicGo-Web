import { Laptop, Moon, Sun } from 'lucide-react'

import { IconButton } from '@/components/ui/icon-button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { t } from '@/i18n'
import { useUIStore, type ThemeMode } from '@/store/ui-store'

/** 主题图标（三态）。 */
function themeIcon(theme: ThemeMode) {
  if (theme === 'light') return Sun
  if (theme === 'dark') return Moon
  return Laptop
}

/** 顶栏的主题切换（light / dark / system 三态，DESIGN.md §11）。 */
export function ThemeToggle() {
  const theme = useUIStore((state) => state.theme)
  const setTheme = useUIStore((state) => state.setTheme)

  const Icon = themeIcon(theme)
  const label = t('TOPBAR_TOGGLE_THEME')

  const options: { value: ThemeMode; labelKey: string; icon: typeof Sun }[] = [
    { value: 'light', labelKey: 'THEME_LIGHT', icon: Sun },
    { value: 'dark', labelKey: 'THEME_DARK', icon: Moon },
    { value: 'system', labelKey: 'THEME_SYSTEM', icon: Laptop },
  ]

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <IconButton label={label}>
          <Icon className="size-4" aria-hidden />
        </IconButton>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-40">
        <DropdownMenuLabel>{label}</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {options.map((option) => {
          const OptionIcon = option.icon
          return (
            <DropdownMenuItem
              key={option.value}
              onSelect={() => setTheme(option.value)}
              className="gap-2"
              aria-checked={theme === option.value}
              role="menuitemradio"
            >
              <OptionIcon className="size-4" aria-hidden />
              <span className="flex-1">{t(option.labelKey)}</span>
              {theme === option.value ? <span className="text-brand">·</span> : null}
            </DropdownMenuItem>
          )
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
