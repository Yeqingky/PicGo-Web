import { Album } from 'lucide-react'

import { PlaceholderPage } from '@/components/layout/placeholder-page'

export function AlbumsPage() {
  return (
    <PlaceholderPage
      icon={Album}
      title="相册"
      description="相册的增删改与「把图片移入相册」（DESIGN.md §3.2）。"
    />
  )
}
