import { Settings } from 'lucide-react'

import { PlaceholderPage } from '@/components/layout/placeholder-page'

export function AdminSitePage() {
  return (
    <PlaceholderPage
      icon={Settings}
      title="站点设置"
      description="站点信息 / 邮件 / 登录方式 / 安全 / 日志 / 关于。⚠️ 首页内容与背景图在「主题设置」里，不在此页（D95 / DESIGN.md §5.7）。"
    />
  )
}
