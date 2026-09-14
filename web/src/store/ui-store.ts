import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'

/**
 * 全局 UI 状态（DESIGN.md §11）。
 *
 * 只放「用户意图 + 本地 UI 状态」：
 *  - 主题（light / dark / system）
 *  - 侧栏折叠 / 移动端抽屉
 *  - 全局 loading 遮罩
 *  - SSE 连接状态（由 task store 写入，这里只读展示）
 *
 * ⚠️ 服务端数据**不**进这里。
 */

export type ThemeMode = 'light' | 'dark' | 'system'

/**
 * UI store 在 localStorage 里的键名。
 *
 * ⚠️ 必须与 `persist({ name })` **以及 `index.html` 的首屏防闪烁脚本**三处一致：
 *  - 键名：`picgo-web.ui`
 *  - 值：JSON，形如 `{"state":{"theme":"dark","sidebarCollapsed":false},"version":0}`
 *    （**不是**裸字符串 `"dark"`）
 *
 * 任一处不一致都会导致「暗色用户每次加载闪一下亮色」（FOUC），
 * 且不会报错、很难发现 —— 曾由真实浏览器验证抓到过这个 bug。
 */
export const UI_STORE_STORAGE_KEY = 'picgo-web.ui'

interface UIState {
  theme: ThemeMode
  /** 桌面端侧栏是否收起为图标条 */
  sidebarCollapsed: boolean
  /** 移动端抽屉是否打开 */
  mobileNavOpen: boolean
  /** 全局 loading 计数（并发请求时用计数而非布尔，避免提前关掉） */
  loadingCount: number

  isLoading: () => boolean
}

interface UIActions {
  setTheme: (theme: ThemeMode) => void
  /** 在 light → dark → system 之间循环（供顶栏按钮用）。 */
  cycleTheme: () => void
  toggleSidebar: () => void
  setSidebarCollapsed: (collapsed: boolean) => void
  setMobileNavOpen: (open: boolean) => void
  pushLoading: () => void
  popLoading: () => void
}

/** 计算「当前是否应处于暗色」（供 ThemeProvider 解析 system）。 */
export function resolveIsDark(theme: ThemeMode): boolean {
  if (theme === 'dark') return true
  if (theme === 'light') return false
  if (typeof window === 'undefined' || !window.matchMedia) return false
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

/** 把主题写到 <html>（class 策略，DESIGN.md §11）。 */
export function applyThemeToDocument(theme: ThemeMode): void {
  if (typeof document === 'undefined') return
  const isDark = resolveIsDark(theme)
  document.documentElement.classList.toggle('dark', isDark)
  document.documentElement.style.colorScheme = isDark ? 'dark' : 'light'
}

export const useUIStore = create<UIState & UIActions>()(
  persist(
    (set, get) => ({
      theme: 'system',
      sidebarCollapsed: false,
      mobileNavOpen: false,
      loadingCount: 0,

      isLoading: () => get().loadingCount > 0,

      setTheme(theme) {
        applyThemeToDocument(theme)
        set({ theme })
      },

      cycleTheme() {
        const order: ThemeMode[] = ['light', 'dark', 'system']
        const next = order[(order.indexOf(get().theme) + 1) % order.length]
        applyThemeToDocument(next)
        set({ theme: next })
      },

      toggleSidebar() {
        set((state) => ({ sidebarCollapsed: !state.sidebarCollapsed }))
      },

      setSidebarCollapsed(collapsed) {
        set({ sidebarCollapsed: collapsed })
      },

      setMobileNavOpen(open) {
        set({ mobileNavOpen: open })
      },

      pushLoading() {
        set((state) => ({ loadingCount: state.loadingCount + 1 }))
      },

      popLoading() {
        set((state) => ({ loadingCount: Math.max(0, state.loadingCount - 1) }))
      },
    }),
    {
      name: UI_STORE_STORAGE_KEY,
      storage: createJSONStorage(() => localStorage),
      // 只持久化「用户偏好」，不持久化抽屉开合与 loading 计数
      partialize: (state) => ({
        theme: state.theme,
        sidebarCollapsed: state.sidebarCollapsed,
      }),
      onRehydrateStorage: () => (state) => {
        // 恢复到已有主题后立即应用，避免与 index.html 的内联脚本结论不一致
        if (state?.theme) applyThemeToDocument(state.theme)
      },
    },
  ),
)
