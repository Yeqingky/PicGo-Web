import { Upload } from 'lucide-react'

import { PlaceholderPage } from '@/components/layout/placeholder-page'

export function UploadPage() {
  return (
    <PlaceholderPage
      icon={Upload}
      title="上传"
      description="拖拽/点击/粘贴上传、目标存储选择、上传队列与 SSE 进度（DESIGN.md §5.1）。"
    />
  )
}
