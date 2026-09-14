import { KeyRound } from 'lucide-react'

import { AuthPlaceholder } from '@/features/auth/auth-placeholder'

/**
 * 重置密码（路由占位，从邮件链接进入）。
 *
 * 与 `forgot-password-page.tsx` 同一个契约缺口：`docs/API.md` 未定义
 * `POST /auth/reset-password`。等 W6 补齐后再实现。
 */
export function ResetPasswordPage() {
  return (
    <AuthPlaceholder
      icon={KeyRound}
      title="重置密码"
      description="从邮件链接进入的重置流程，其后端端点尚未写入 docs/API.md。待 W6 补齐契约后实现。"
    />
  )
}
