import { Images } from 'lucide-react'

import { PlaceholderPage } from '@/components/layout/placeholder-page'

export function GalleryPage() {
  return (
    <PlaceholderPage
      icon={Images}
      title="图库"
      description="瀑布流/网格、筛选、批量操作、灯箱、外链复制；管理员可用顶部 Tab 切「我的 / 全部」（D71）。不做缩略图，直接引图床 URL（D84）。"
    />
  )
}
