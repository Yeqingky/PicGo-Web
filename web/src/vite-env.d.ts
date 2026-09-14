/// <reference types="vite/client" />

/**
 * 本项目用到的 Vite 环境变量（`.env` / `.env.local`）。
 *
 * ⚠️ 只放**构建期**开关。运行期配置一律走后端（数据库 settings 表，D18）。
 */
interface ImportMetaEnv {
  /** 启用 mock adapter（仅 DEV 生效），让前端可在后端未就绪时开发 */
  readonly VITE_USE_MOCK?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
