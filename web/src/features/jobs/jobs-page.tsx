import { ClipboardList } from 'lucide-react'

import { PlaceholderPage } from '@/components/layout/placeholder-page'

export function JobsPage() {
  return (
    <PlaceholderPage
      icon={ClipboardList}
      title="任务"
      description="任务列表 + 逐行日志抽屉（SSE 实时）—— 上传批次与插件/主题安装任务（DESIGN.md §5.5）。"
    />
  )
}
