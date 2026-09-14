import { MailQuestion } from 'lucide-react'

import { AuthPlaceholder } from '@/features/auth/auth-placeholder'

/**
 * 忘记密码（路由占位）。
 *
 * ⚠️ **本页暂为占位，原因是一个真实的契约缺口**（已在汇报中说明）：
 * `docs/API.md` §1 定义了 login / refresh / logout / me / password / identities / oauth，
 * 但**没有定义**「忘记密码」与「重置密码」的端点
 * （`docs/DESIGN.md` §3.1 与 `docs/OPERATIONS.md` §6 都描述了这个流程，D29 也要求写 `EmailLogs`）。
 *
 * 因此这里**不擅自发明接口路径**，等 W6（邮件与日志）把契约补进 `docs/API.md` 后再实现表单。
 * 该保留路由本身必须存在 —— D94.2 的「认证页保留列表」包含 `/forgot-password`。
 */
export function ForgotPasswordPage() {
  return (
    <AuthPlaceholder
      icon={MailQuestion}
      title="忘记密码"
      description="该流程依赖 SMTP 与「重置密码」接口，而 docs/API.md 尚未定义相关端点。待 W6 补齐契约后实现；当前如需重置密码，请联系管理员在「用户管理」中重置。"
    />
  )
}
