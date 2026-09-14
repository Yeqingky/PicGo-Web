/**
 * 动态表单渲染器（DESIGN.md §7）。
 *
 * 两套 schema（插件 / 主题）经 `adapt.ts` 规约到同一组内部字段类型后，
 * 由 `SchemaForm` 统一渲染 —— 因此**不为每种图床或主题写一份表单**。
 */
export { SchemaForm, type SchemaFormProps } from '@/components/schema-form/schema-form'
export { SchemaFieldRow, type SchemaFieldProps } from '@/components/schema-form/schema-field'
export { useInitialSchemaValues, validateSchema } from '@/components/schema-form/helpers'
export {
  adaptPluginSchema,
  adaptThemeSchema,
  adaptThemeItem,
  initialValues,
  normalizePluginChoices,
  parseThemeOptions,
  stripUnchangedSecrets,
  validateRequired,
  type AdaptedSchema,
  type SchemaField,
  type SchemaFieldType,
  type SchemaOption,
} from '@/components/schema-form/adapt'
