import { Server } from 'lucide-react'

import { PlaceholderPage } from '@/components/layout/placeholder-page'

export function AdminStoragePage() {
  return (
    <PlaceholderPage
      icon={Server}
      title="存储驱动"
      description="同类型多实例配置、激活、连通性测试、魔法路径模板（D64 / DESIGN.md §5.3）。"
    />
  )
}
