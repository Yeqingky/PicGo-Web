import { FolderTree, ShieldOff, Trash2 } from 'lucide-react'

import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import type { StorageCapabilities } from '@/types/api'

/**
 * 驱动能力徽章（DESIGN.md §5.3）。
 *
 * 两个能力（来自 agent 探测 → 服务端缓存，D77：**不硬编码驱动名列表**）：
 *  - `SupportsPathTemplate` —— 支持自定义远端路径（魔法路径的前提）
 *  - `SupportsRemoteDelete` —— 插件实现了 `remove` 事件（远端删除的前提，D47）
 *
 * **不支持时必须给出原因**（否则管理员会以为是自己配错了）。
 */
export interface CapabilityBadgesProps {
  capabilities: StorageCapabilities | undefined
  className?: string
}

export function CapabilityBadges({ capabilities, className }: CapabilityBadgesProps) {
  if (!capabilities) return null

  return (
    <div className={cn('flex flex-wrap items-center gap-3 text-xs', className)}>
      <Capability
        ok={capabilities.SupportsPathTemplate}
        okLabel={t('STORAGE_CAP_PATH_OK')}
        failLabel={t('STORAGE_CAP_PATH_NO')}
        failReason={t('STORAGE_CAP_PATH_NO_REASON', {
          fields: capabilities.PathFieldNames.join(' / ') || '—',
        })}
      />

      <Capability
        ok={capabilities.SupportsRemoteDelete}
        okLabel={t('STORAGE_CAP_DELETE_OK')}
        failLabel={t('STORAGE_CAP_DELETE_NO')}
        failReason={t('STORAGE_CAP_DELETE_NO_REASON')}
        failIcon="trash"
      />
    </div>
  )
}

interface CapabilityProps {
  ok: boolean
  okLabel: string
  failLabel: string
  failReason: string
  failIcon?: 'tree' | 'trash'
}

function Capability({ ok, okLabel, failLabel, failReason, failIcon = 'tree' }: CapabilityProps) {
  const Icon = failIcon === 'trash' ? Trash2 : ShieldOff
  const label = ok ? okLabel : failLabel

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className={cn(
            'inline-flex cursor-help items-center gap-1',
            ok ? 'text-success' : 'text-muted-foreground',
          )}
        >
          {ok ? (
            <FolderTree className="size-3.5" aria-hidden />
          ) : (
            <Icon className="size-3.5" aria-hidden />
          )}
          {label}
        </span>
      </TooltipTrigger>
      <TooltipContent>{ok ? label : failReason}</TooltipContent>
    </Tooltip>
  )
}
