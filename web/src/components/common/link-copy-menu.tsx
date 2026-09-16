import { Copy, Loader2 } from 'lucide-react'
import { useState } from 'react'

import { Button, type ButtonProps } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { toast } from '@/components/ui/toast'
import { t } from '@/i18n'
import {
  copyText,
  formatLink,
  formatLinks,
  linkFormatLabel,
  LINK_FORMATS,
  type LinkFormat,
  type LinkFormatInput,
} from '@/lib/clipboard'

/**
 * 外链复制（D68 / DESIGN.md §8.2）。
 *
 * 三种格式：**Markdown / 直链 / HTML**（D68）。
 * 单张图用点按（默认格式直接复制）；多张图或需要选格式时用下拉菜单。
 *
 * 复制优先用 `navigator.clipboard`，非安全上下文回退 `execCommand`
 * （内网 HTTP 部署很常见，见 `lib/clipboard.ts`）。
 */

export interface LinkCopyMenuProps {
  /** 单个或多个图片（多张会按格式拼成多行） */
  items: LinkFormatInput[]
  /** 按钮文案（缺省「复制链接」） */
  label?: string
  /** 仅显示复制图标，仍保留 aria-label 与 title。 */
  iconOnly?: boolean
  variant?: ButtonProps['variant']
  size?: ButtonProps['size']
  className?: string
  disabled?: boolean
}

export function LinkCopyMenu({
  items,
  label,
  iconOnly = false,
  variant = 'outline',
  size = 'sm',
  className,
  disabled,
}: LinkCopyMenuProps) {
  const [busy, setBusy] = useState(false)

  const copy = async (format: LinkFormat) => {
    if (items.length === 0) return
    setBusy(true)
    try {
      const text = items.length === 1 ? formatLink(items[0], format) : formatLinks(items, format)
      const ok = await copyText(text)
      if (ok) {
        toast.success(t('COMMON_COPIED'))
      } else {
        toast.error(t('COMMON_COPY_FAILED'))
      }
    } finally {
      setBusy(false)
    }
  }

  const disabledNow = disabled || items.length === 0
  const buttonLabel = label ?? t('LINK_COPY_LABEL')

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          variant={variant}
          size={size}
          className={className}
          disabled={disabledNow || busy}
          aria-label={buttonLabel}
          title={iconOnly ? buttonLabel : undefined}
        >
          {busy ? (
            <Loader2 className="size-4 animate-spin" aria-hidden />
          ) : (
            <Copy aria-hidden />
          )}
          {iconOnly ? null : buttonLabel}
        </Button>
      </DropdownMenuTrigger>

      <DropdownMenuContent align="end">
        {LINK_FORMATS.map((format) => (
          <DropdownMenuItem key={format} onSelect={() => void copy(format)}>
            {linkFormatLabel(format)}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
