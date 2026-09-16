import { Check, Languages } from 'lucide-react'

import { IconButton } from '@/components/ui/icon-button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { availableLocales, setLocale, t, useLocale } from '@/i18n'

/** 顶栏语言切换（当前支持简体中文与 English）。 */
export function LanguageToggle() {
  const locale = useLocale()
  const label = t('TOPBAR_LANGUAGE')
  const options = availableLocales()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <IconButton label={label}>
          <Languages aria-hidden />
        </IconButton>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-48">
        <DropdownMenuLabel>{label}</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {options.map((option) => (
          <DropdownMenuItem
            key={option.value}
            onSelect={() => setLocale(option.value)}
            aria-checked={locale === option.value}
            role="menuitemradio"
            className="gap-2"
          >
            <span className="flex-1">{option.label}</span>
            {locale === option.value ? <Check className="size-4" aria-hidden /> : null}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
