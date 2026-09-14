import { SchemaFieldRow } from '@/components/schema-form/schema-field'
import { type AdaptedSchema, type SchemaField } from '@/components/schema-form/adapt'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { cn } from '@/lib/utils'

/**
 * 动态表单渲染器（DESIGN.md §7）。
 *
 * 输入是**适配后的内部 schema**（见 `adapt.ts`），因此同一套组件同时服务：
 *  - 插件 / 驱动配置（`adaptPluginSchema`，`/admin/storage`）
 *  - 主题设置（`adaptThemeSchema`，`/admin/themes` 的设置抽屉）
 *
 * 表单状态由**调用方持有**（受控）：`values` + `onChange`。
 * 这样提交、校验、回源重求值都能由页面统一编排（例如 `dependsOn` 触发时先取消旧请求）。
 *
 * ⚠️ 前端**永不执行插件代码**（PICGO-INTEGRATION §3）：
 *    `dependsOn` 联动是把当前 `values` 回传给后端重新求值，而不是在浏览器里跑插件函数。
 */
export interface SchemaFormProps {
  schema: AdaptedSchema
  values: Record<string, unknown>
  onChange: (name: string, value: unknown) => void
  /** 字段名 → 错误文案（由调用方在提交时用 `validateSchema` 生成） */
  errors?: Record<string, string>
  disabled?: boolean
  /** 正在回源重求值的字段名集合（显示禁用态） */
  loadingFields?: string[]
  className?: string
}

export function SchemaForm({
  schema,
  values,
  onChange,
  errors,
  disabled,
  loadingFields,
  className,
}: SchemaFormProps) {
  const { fields } = schema

  if (fields.length === 0) {
    return (
      <Alert variant="info">
        <AlertDescription>该配置没有可填写的字段。</AlertDescription>
      </Alert>
    )
  }

  return (
    <div className={cn('space-y-4', className)}>
      {fields.map((field: SchemaField) => (
        <SchemaFieldRow
          key={field.name}
          field={field}
          value={values[field.name]}
          onChange={(value) => onChange(field.name, value)}
          error={errors?.[field.name]}
          disabled={disabled}
          loading={loadingFields?.includes(field.name)}
        />
      ))}
    </div>
  )
}
