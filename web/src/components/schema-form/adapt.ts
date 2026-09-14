import type { IPluginConfig, IPluginConfigChoice, ThemeConfigItem, ThemeConfigItemSchema } from '@/types/schema'

/**
 * Schema 适配层（DESIGN.md §7.1 / §7.2）。
 *
 * 前端有**两套 schema** 需要渲染成表单：
 *  1. **插件 / 驱动**（`IPluginConfig[]`，agent 求值后返回）—— 类型名 `input`/`password`/`list`/…
 *  2. **主题**（`Configuration.Items[]`，来自主题 manifest）—— 类型名 `string`/`text`/`number`/…
 *
 * 两套命名**不能直接共用**，但**共用同一套渲染组件**。本文件是中间那层薄的适配层：
 * 把各自的 `Type` 与属性名规约到**内部字段类型**。
 *
 * ⚠️ 本文件必须是**纯数据变换**（无副作用、无 IO），便于单测与推理。
 */

// ---------------------------------------------------------------------------
// 内部字段类型（渲染器只认这一组）
// ---------------------------------------------------------------------------

export type SchemaFieldType =
  | 'text'
  | 'password'
  | 'textarea'
  | 'number'
  | 'switch'
  | 'select'
  | 'multiselect'
  | 'repeater'
  /** 未知类型：降级为单行输入 + 警告，**不崩溃** */
  | 'unknown'

export interface SchemaOption {
  label: string
  value: string
}

export interface SchemaField {
  /** 字段名（提交时的 key） */
  name: string
  label: string
  type: SchemaFieldType
  help?: string
  required: boolean
  defaultValue: unknown
  options?: SchemaOption[]
  /** 依赖的其它字段名（仅插件 schema 有；需要回源重求值） */
  dependsOn?: string[]
  /** 原始类型名（用于「未知类型」提示与排查） */
  rawType: string
  /** `repeater` 的子字段定义 */
  itemSchema?: SchemaField[]
  /** 值被遮蔽的字段（如 `password`）—— 未改动时不提交 */
  masked?: boolean
}

/** 一次适配的结果：字段列表 + 是否含需要回源的联动字段。 */
export interface AdaptedSchema {
  fields: SchemaField[]
  /** 为真表示存在 `dependsOn`，需要在依赖变化时回源 agent 重新求值（DESIGN §7.3） */
  hasDependencies: boolean
}

// ---------------------------------------------------------------------------
// 插件 schema → 内部类型
// ---------------------------------------------------------------------------

const PLUGIN_TYPE_MAP: Record<string, SchemaFieldType> = {
  input: 'text',
  password: 'password',
  editor: 'textarea',
  confirm: 'switch',
  list: 'select',
  checkbox: 'multiselect',
}

/** 归一化插件的 `Choices`（可能是字符串数组，也可能是 `{Name, Value}`）。 */
export function normalizePluginChoices(choices: IPluginConfigChoice[] | undefined): SchemaOption[] {
  if (!choices || choices.length === 0) return []

  return choices.map((choice) => {
    if (typeof choice === 'string') {
      return { label: choice, value: choice }
    }
    const value = choice.Value
    return {
      label: choice.Name ?? String(value),
      value: typeof value === 'string' ? value : String(value),
    }
  })
}

/** 适配插件 schema。 */
export function adaptPluginSchema(items: IPluginConfig[] | undefined): AdaptedSchema {
  if (!items || items.length === 0) return { fields: [], hasDependencies: false }

  const fields = items.map<SchemaField>((item) => {
    const rawType = item.Type
    const type = PLUGIN_TYPE_MAP[rawType] ?? 'unknown'

    return {
      name: item.Name,
      // 标签优先用 Alias（inquirer 风格），其次 Name
      label: item.Alias?.trim() || item.Name,
      type,
      help: item.Message,
      required: item.Required ?? false,
      defaultValue: item.Default,
      options: type === 'select' || type === 'multiselect' ? normalizePluginChoices(item.Choices) : undefined,
      dependsOn: item.DependsOn,
      rawType,
      masked: type === 'password',
    }
  })

  return {
    fields,
    hasDependencies: fields.some((field) => (field.dependsOn?.length ?? 0) > 0),
  }
}

// ---------------------------------------------------------------------------
// 主题 schema → 内部类型
// ---------------------------------------------------------------------------

/** 把主题的 `Options`（**逗号分隔字符串**）拆成选项。 */
export function parseThemeOptions(options: string | undefined): SchemaOption[] {
  if (!options) return []
  return options
    .split(',')
    .map((part) => part.trim())
    .filter((part) => part.length > 0)
    .map((value) => ({ label: value, value }))
}

function adaptThemeItemSchema(items: ThemeConfigItemSchema[] | undefined): SchemaField[] | undefined {
  if (!items || items.length === 0) return undefined
  return items.map((item) => adaptThemeItem(item))
}

/** 适配单个主题配置项。 */
export function adaptThemeItem(item: ThemeConfigItem): SchemaField {
  const rawType = (item.Type ?? 'string').toLowerCase()

  let type: SchemaFieldType
  switch (rawType) {
    case 'string':
      type = 'text'
      break
    case 'text':
      type = 'textarea'
      break
    case 'number':
      type = 'number'
      break
    case 'switch':
    case 'confirm':
      type = 'switch'
      break
    case 'select':
      type = 'select'
      break
    case 'json':
      // 有 ItemSchema → 结构化列表编辑器；否则退化为 JSON 文本编辑（DESIGN §7.2）
      type = item.ItemSchema && item.ItemSchema.length > 0 ? 'repeater' : 'textarea'
      break
    default:
      type = 'unknown'
      break
  }

  return {
    name: item.Key,
    // 主题 schema 的 Name 已由服务端按 Accept-Language 解析成单串（D98），直接展示
    label: item.Name?.trim() || item.Key,
    type,
    help: item.Help,
    required: item.Required ?? false,
    defaultValue: item.Default,
    options: type === 'select' ? parseThemeOptions(item.Options) : undefined,
    rawType,
    itemSchema: type === 'repeater' ? adaptThemeItemSchema(item.ItemSchema) : undefined,
  }
}

/** 适配主题 schema（`Configuration.Items[]`）。 */
export function adaptThemeSchema(items: ThemeConfigItem[] | undefined): AdaptedSchema {
  if (!items || items.length === 0) return { fields: [], hasDependencies: false }

  const fields = items.map((item) => adaptThemeItem(item))
  // 主题 schema 是静态的：没有 dependsOn，也不回源（DESIGN §7.3）
  return { fields, hasDependencies: false }
}

// ---------------------------------------------------------------------------
// 值工具
// ---------------------------------------------------------------------------

/** 用默认值初始化一份表单值（已有值优先）。 */
export function initialValues(fields: SchemaField[], current?: Record<string, unknown>): Record<string, unknown> {
  const values: Record<string, unknown> = {}

  for (const field of fields) {
    if (current && Object.prototype.hasOwnProperty.call(current, field.name)) {
      values[field.name] = current[field.name]
      continue
    }
    values[field.name] = field.type === 'multiselect' ? [] : (field.defaultValue ?? '')
  }

  return values
}

/** 校验必填项；返回「字段名 → 错误文案」。 */
export function validateRequired(
  fields: SchemaField[],
  values: Record<string, unknown>,
  requiredMessage: string,
): Record<string, string> {
  const errors: Record<string, string> = {}

  for (const field of fields) {
    if (!field.required) continue

    const value = values[field.name]
    const empty =
      value === undefined ||
      value === null ||
      value === '' ||
      (Array.isArray(value) && value.length === 0)

    if (empty) errors[field.name] = requiredMessage
  }

  return errors
}

/**
 * 提交前清理：去掉被遮蔽且未改动的字段。
 *
 * 语义与后端约定一致（API.md §3 / D95）：**掩码表示「不修改」**。
 */
export function stripUnchangedSecrets(
  fields: SchemaField[],
  values: Record<string, unknown>,
  mask: string,
): Record<string, unknown> {
  const out: Record<string, unknown> = { ...values }

  for (const field of fields) {
    if (!field.masked) continue
    if (out[field.name] === mask) {
      delete out[field.name]
    }
  }

  return out
}
