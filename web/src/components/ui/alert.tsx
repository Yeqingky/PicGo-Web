import { cva, type VariantProps } from 'class-variance-authority'
import type { ComponentProps } from 'react'

import { cn } from '@/lib/utils'

const alertVariants = cva(
  // 图标与首行文字同行：有 svg 时启用 grid 两列，显式把**所有直接子 div**（标题/说明）
  // 放进第 2 列（自动跨行堆叠），图标在第 1 行第 1 列；
  // 第 2 行起第 1 列自动为空（行高 0），图标无需跨行 ——
  // ⚠️ 不能用 row-span 跨大行号：会强制生成大量空行，配合行间距撑出巨高空白（踩过）。
  // 无图标的纯文案 Alert 不启用 grid，退回单列块布局不受影响。
  'relative w-full rounded-lg border px-4 py-3 text-sm [&>svg]:size-4 [&>svg]:shrink-0 '
    + 'has-[>svg]:grid has-[>svg]:grid-cols-[auto_minmax(0,1fr)] has-[>svg]:items-start has-[>svg]:gap-x-3 has-[>svg]:gap-y-1 '
    + '[&>svg]:mt-0.5 [&>div]:col-start-2 [&>div]:min-w-0',
  {
    variants: {
      variant: {
        default: 'border-border bg-background text-foreground',
        info: 'border-info/30 bg-info/5 text-foreground',
        success: 'border-success/30 bg-success/5 text-foreground',
        warning: 'border-warning/30 bg-warning/5 text-foreground',
        destructive: 'border-destructive/40 bg-destructive/5 text-foreground',
      },
    },
    defaultVariants: { variant: 'default' },
  },
)

export interface AlertProps extends ComponentProps<'div'>, VariantProps<typeof alertVariants> {}

export function Alert({ className, variant, ...props }: AlertProps) {
  return <div role="alert" className={cn(alertVariants({ variant }), className)} {...props} />
}

export function AlertTitle({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('mb-1 font-medium leading-none tracking-tight', className)} {...props} />
}

export function AlertDescription({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('text-sm text-muted-foreground', className)} {...props} />
}
