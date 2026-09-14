import { useEffect } from 'react'

import { resolveIsDark, useUIStore } from '@/store/ui-store'

/**
 * 明暗主题（DESIGN.md §11）。
 *
 * 策略：`class` 策略 —— 在 `<html>` 上加/去 `dark`。
 *  - 首屏由 `index.html` 的内联脚本先设一次（防闪烁）
 *  - 这里负责「运行期切换」与「system 模式下跟随系统变化」
 *
 * ⚠️ 不在这里调用 `setTheme`（那会写 localStorage 并触发额外渲染），
 *    只同步 DOM class，避免与 index.html 的结论打架。
 */
export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const theme = useUIStore((state) => state.theme)

  useEffect(() => {
    const apply = () => {
      const isDark = resolveIsDark(theme)
      document.documentElement.classList.toggle('dark', isDark)
      document.documentElement.style.colorScheme = isDark ? 'dark' : 'light'
    }

    apply()

    // system 模式下跟随系统切换
    if (theme !== 'system' || !window.matchMedia) return

    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = () => apply()
    media.addEventListener('change', onChange)
    return () => media.removeEventListener('change', onChange)
  }, [theme])

  return children
}
