import { Link } from 'react-router'

import { useSiteConfig, siteDisplayName } from '@/hooks/api'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

/**
 * 认证页共用布局（登录 / 首启改密 / 忘记密码 / 重置密码）。
 *
 * 布局（DESIGN.md §4.3）：
 *  - 桌面：左右分栏 —— 左侧表单卡，右侧**中性装饰背景**
 *  - 移动：单列，中性背景作整页底图 + 蒙层
 *
 * ⚠️ **刻意不使用主题的 `BackgroundURL`**（DESIGN.md §4.3）：
 *    认证页是「永久内置」的（D94.2），而主题是第三方前端代码；
 *    主题的配置不应影响承载凭据输入的页面。这里用的是设计 token 里的
 *    中性渐变 + 细微网格，与主题无关。
 */

/** 中性装饰背景：品牌色柔光 + 细网格（纯 CSS，可跟随亮暗色）。 */
function NeutralBackground({ className }: { className?: string }) {
  return (
    <div
      aria-hidden
      className={cn('relative overflow-hidden bg-muted', className)}
      style={{
        // 用 token 派生的柔和渐变，不硬编码颜色值（DESIGN.md §2.1）
        backgroundImage: [
          'radial-gradient(60rem 60rem at 20% 10%, hsl(var(--brand) / 0.18), transparent 60%)',
          'radial-gradient(50rem 50rem at 85% 85%, hsl(var(--info) / 0.14), transparent 55%)',
          'linear-gradient(180deg, hsl(var(--muted)) 0%, hsl(var(--background)) 100%)',
        ].join(','),
      }}
    >
      {/* 细微网格纹理 */}
      <div
        className="absolute inset-0 opacity-[0.05]"
        style={{
          backgroundImage: [
            'linear-gradient(hsl(var(--foreground)) 1px, transparent 1px)',
            'linear-gradient(90deg, hsl(var(--foreground)) 1px, transparent 1px)',
          ].join(','),
          backgroundSize: '32px 32px',
        }}
      />
    </div>
  )
}

export interface AuthLayoutProps {
  title: string
  description?: string
  children: React.ReactNode
  /** 表单下方的附加内容（如「返回登录」链接） */
  footer?: React.ReactNode
}

export function AuthLayout({ title, description, children, footer }: AuthLayoutProps) {
  const { config } = useSiteConfig()
  const siteName = siteDisplayName(config)

  return (
    <div className="relative min-h-svh">
      {/* 移动端整页底图 */}
      <NeutralBackground className="absolute inset-0 lg:hidden" />

      <div className="relative grid min-h-svh lg:grid-cols-2">
        {/* 左：表单 */}
        <div className="flex items-center justify-center px-4 py-10 sm:px-8">
          <div className="w-full max-w-sm animate-fade-up rounded-lg border border-border bg-background/95 p-6 shadow-sm backdrop-blur lg:border-0 lg:bg-transparent lg:p-0 lg:shadow-none lg:backdrop-blur-none">
            <header className="mb-6 space-y-1.5">
              <Link
                to="/login"
                className="inline-block text-lg font-semibold text-foreground transition-colors hover:text-brand"
              >
                {siteName}
              </Link>
              <h1 className="text-2xl font-semibold tracking-tight text-foreground">{title}</h1>
              {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
            </header>

            {children}

            {footer ? <div className="mt-6 text-sm text-muted-foreground">{footer}</div> : null}
          </div>
        </div>

        {/* 右：中性装饰背景（桌面） */}
        <NeutralBackground className="hidden lg:block" />
      </div>
    </div>
  )
}

/** 「不开放自助注册」的说明（D25）。 */
export function NoSignupHint() {
  return (
    <p className="rounded-md border border-border bg-muted/50 px-3 py-2 text-xs text-muted-foreground">
      {t('AUTH_LOGIN_NO_SIGNUP')}
    </p>
  )
}
