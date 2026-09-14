import { Users } from 'lucide-react'

import { PlaceholderPage } from '@/components/layout/placeholder-page'

export function AdminUsersPage() {
  return (
    <PlaceholderPage
      icon={Users}
      title="用户管理"
      description="用户 CRUD、配额调整、状态、重置密码、注销（API.md §2）。"
    />
  )
}
