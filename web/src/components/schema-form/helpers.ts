import { useMemo } from 'react'

import { initialValues, validateRequired, type AdaptedSchema } from '@/components/schema-form/adapt'
import { t } from '@/i18n'

/**
 * Schema 表单的辅助函数。
 *
 * 单独成文件（而非放在 `schema-form.tsx` 里）：
 *  - 让 `schema-form.tsx` 只导出组件，满足 React Fast Refresh 的约束
 *  - 这些是无状态工具（构造初始值 / 校验），便于单测与在非组件环境复用
 */

/** 用 schema 的默认值构造一份初始值（供页面的 `useState`）。 */
export function useInitialSchemaValues(
  schema: AdaptedSchema,
  current?: Record<string, unknown>,
): Record<string, unknown> {
  // schema 在一次表单生命周期内不变；current 由调用方决定是否稳定
  return useMemo(() => initialValues(schema.fields, current), [schema, current])
}

/** 校验必填项（提交前调用）；返回「字段名 → 错误文案」。 */
export function validateSchema(
  schema: AdaptedSchema,
  values: Record<string, unknown>,
): Record<string, string> {
  return validateRequired(schema.fields, values, t('COMMON_REQUIRED'))
}
