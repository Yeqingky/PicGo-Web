import { create } from 'zustand'
import { persist, createJSONStorage } from 'zustand/middleware'

import { authApi } from '@/lib/api'
import { setAuthFailureHandler } from '@/lib/http'
import { toApiError, type User } from '@/types/api'

/**
 * 登录态（DESIGN.md §11）。
 *
 * 边界（很重要）：
 *  - **只存用户快照**，**绝不存令牌** —— 令牌在 httpOnly Cookie 里（D30），JS 读不到也不该读
 *  - 服务端数据不镜像进 store；这里存的是「当前登录者是谁」这一 UI 事实
 *  - `status === 'unknown'` 表示尚未探测（首屏 loading），用于避免登录页闪一下
 */

export type AuthStatus = 'unknown' | 'authenticated' | 'anonymous'

interface AuthState {
  status: AuthStatus
  user: User | null
  /** 最近一次探测/登录失败的提示（供 UI 展示） */
  error: string | null

  /** 是否管理员（跳过后端判断的便捷派生，不可作为权限依据） */
  isAdmin: () => boolean
  /** 是否需要强制改密 */
  mustChangePassword: () => boolean
}

interface AuthActions {
  /** 首屏探测：调 `/auth/me` 判断是否已登录。 */
  bootstrap: () => Promise<void>
  /** 邮箱 + 密码登录（D23）。`remember` 为「记住我」意图（见 `api.auth.login`）。 */
  login: (email: string, password: string, remember?: boolean) => Promise<User>
  /** 退出登录：通知后端吊销 refresh token 并清本地状态。 */
  logout: () => Promise<void>
  /** 直接写入用户快照（登录成功后或 `/auth/me` 返回后使用）。 */
  setUser: (user: User | null) => void
  /** 清空登录态（刷新失败 / 主动登出时使用）。 */
  clear: () => void
  /** 设置错误提示（供登录页展示）。 */
  setError: (message: string | null) => void
}

const initialState: AuthState = {
  status: 'unknown',
  user: null,
  error: null,
  isAdmin: () => false,
  mustChangePassword: () => false,
}

/** 从用户快照派生便捷判断（集中在一处，避免组件里反复写 `user?.Role === 'admin'`）。 */
function deriveFlags(user: User | null) {
  return {
    isAdmin: () => user?.Role === 'admin',
    mustChangePassword: () => user?.MustChangePassword === true,
  }
}

export const useAuthStore = create<AuthState & AuthActions>()(
  persist(
    (set) => ({
      ...initialState,
      ...deriveFlags(null),

      async bootstrap() {
        try {
          const user = await authApi.me()
          set({ status: 'authenticated', user, error: null, ...deriveFlags(user) })
        } catch (err) {
          const apiError = toApiError(err)
          // 40102/40103 = 未登录；其它错误（含网络失败）也按未登录处理，
          // 但不覆盖 error（避免首屏弹出「服务不可用」干扰登录页）
          set({
            status: 'anonymous',
            user: null,
            error: apiError.isAuthError ? null : null,
            ...deriveFlags(null),
          })
        }
      },

      async login(email, password, remember = true) {
        const res = await authApi.login(email, password, remember)
        set({
          status: 'authenticated',
          user: res.User,
          error: null,
          ...deriveFlags(res.User),
        })
        return res.User
      },

      async logout() {
        try {
          await authApi.logout()
        } catch {
          // 即使后端吊销失败，本地也必须清干净（否则 UI 卡在已登录）
        }
        set({ status: 'anonymous', user: null, error: null, ...deriveFlags(null) })
      },

      setUser(user) {
        set({
          status: user ? 'authenticated' : 'anonymous',
          user,
          ...deriveFlags(user),
        })
      },

      clear() {
        set({ status: 'anonymous', user: null, ...deriveFlags(null) })
      },

      setError(message) {
        set({ error: message })
      },
    }),
    {
      name: 'picgo-web.auth',
      storage: createJSONStorage(() => localStorage),
      // ⚠️ 只持久化用户快照与探测结果；**不含令牌**（令牌在 httpOnly Cookie 里）
      partialize: (state) => ({
        status: state.status === 'authenticated' ? state.status : 'unknown',
        user: state.user,
      }),
      // 恢复时补上派生的便捷判断（函数不参与持久化）
      onRehydrateStorage: () => (state) => {
        if (state) {
          const flags = deriveFlags(state.user)
          state.isAdmin = flags.isAdmin
          state.mustChangePassword = flags.mustChangePassword
          // 恢复出来的 user 只是「上次看到的快照」，仍需 bootstrap 校验
          if (state.user) {
            state.status = 'authenticated'
          }
        }
      },
    },
  ),
)

/**
 * 注册「刷新失败 / 需要重新登录」时的处理（`lib/http.ts` 的回调）。
 *
 * 采用**声明式**处理：只清状态，跳转由 `RequireAuth` 观察到
 * `status === 'anonymous'` 后完成（比命令式 `window.location` 更可控，
 * 也避免整页 reload）。
 */
setAuthFailureHandler(() => {
  const state = useAuthStore.getState()
  if (state.status !== 'anonymous') {
    state.clear()
  }
})
