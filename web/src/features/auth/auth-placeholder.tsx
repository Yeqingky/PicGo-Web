import { Construction, type LucideIcon } from 'lucide-react'

import { AuthLayout } from '@/features/auth/auth-layout'
import { EmptyState } from '@/components/ui/empty-state'

/**
 * 认证区未实现页面的占位（仍套用认证页布局，避免出现「裸页」）。
 */
export interface AuthPlaceholderProps {
  title: string
  description: string
  icon?: LucideIcon
}

export function AuthPlaceholder({ title, description, icon = Construction }: AuthPlaceholderProps) {
  return (
    <AuthLayout title={title}>
      <EmptyState icon={icon} title={title} description={description} />
    </AuthLayout>
  )
}
