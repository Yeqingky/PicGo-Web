import { AlertTriangle, ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import type { SchemaField } from '@/components/schema-form/adapt'

/**
 * 单个 schema 字段的渲染（DESIGN.md §7.1）。
 *
 * 按**内部字段类型**分派；未知类型降级为单行输入 + 警告，**绝不崩溃**。
 */

export interface SchemaFieldProps {
  field: SchemaField
  value: unknown
  onChange: (value: unknown) => void
  error?: string
  disabled?: boolean
  /** 该字段的选项正在回源重求值（显示骨架 / 禁用） */
  loading?: boolean
  className?: string
}

/** 渲染字段控件（不含 label / help / error 这些外壳）。 */
function FieldControl({ field, value, onChange, disabled, loading }: SchemaFieldProps) {
  switch (field.type) {
    case 'text':
    case 'unknown':
      return (
        <Input
          value={
            typeof value === 'string'
              ? value
              : value === null || value === undefined
                ? ''
                : String(value)
          }
          onChange={(event) => onChange(event.target.value)}
          disabled={disabled || loading}
          autoComplete="off"
        />
      )

    case 'password':
      return (
        <Input
          type="password"
          value={typeof value === 'string' ? value : ''}
          onChange={(event) => onChange(event.target.value)}
          disabled={disabled || loading}
          autoComplete="new-password"
        />
      )

    case 'textarea':
      return (
        <Textarea
          value={typeof value === 'string' ? value : ''}
          onChange={(event) => onChange(event.target.value)}
          disabled={disabled || loading}
          rows={4}
        />
      )

    case 'number': {
      const numeric = typeof value === 'number' ? value : Number(value)
      return (
        <Input
          type="number"
          value={Number.isFinite(numeric) ? String(numeric) : ''}
          onChange={(event) => {
            const raw = event.target.value
            onChange(raw === '' ? '' : Number(raw))
          }}
          disabled={disabled || loading}
        />
      )
    }

    case 'switch':
      return (
        <Switch
          checked={value === true || value === 'true'}
          onCheckedChange={(checked) => onChange(checked)}
          disabled={disabled || loading}
        />
      )

    case 'select':
      return (
        <Select
          value={typeof value === 'string' && value !== '' ? value : undefined}
          onValueChange={(next) => onChange(next)}
          disabled={disabled || loading}
        >
          <SelectTrigger>
            <SelectValue placeholder={t('SCHEMA_SELECT_PLACEHOLDER')} />
          </SelectTrigger>
          <SelectContent>
            {(field.options ?? []).map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )

    case 'multiselect': {
      const selected = Array.isArray(value) ? (value as unknown[]).map(String) : []
      const options = field.options ?? []

      return (
        <div className="space-y-2 rounded-lg border border-input p-3">
          {options.length === 0 ? (
            <p className="text-xs text-muted-foreground">{t('SCHEMA_NO_OPTIONS')}</p>
          ) : (
            options.map((option) => {
              const checked = selected.includes(option.value)
              return (
                <div key={option.value} className="flex items-center gap-2">
                  <Checkbox
                    id={`field-${field.name}-${option.value}`}
                    checked={checked}
                    disabled={disabled || loading}
                    onCheckedChange={(next) => {
                      if (next === true) onChange([...selected, option.value])
                      else onChange(selected.filter((item) => item !== option.value))
                    }}
                  />
                  <Label
                    htmlFor={`field-${field.name}-${option.value}`}
                    className="text-sm font-normal"
                  >
                    {option.label}
                  </Label>
                </div>
              )
            })
          )}
        </div>
      )
    }

    case 'repeater':
      return <RepeaterField field={field} value={value} onChange={onChange} disabled={disabled} />

    default:
      return null
  }
}

/**
 * 可增删排序的列表编辑器（`repeater`）。
 *
 * 用于主题的 `json` 类型配置（如首页「核心能力 / 应用场景 / FAQ」）。
 *
 * 排序用**上下移动按钮**而非拖拽：DESIGN.md §9.2 提到用 `@dnd-kit`，
 * 但为一个排序引入拖拽库不划算，按钮排序同样可键盘操作（a11y 更友好）。
 * 若将来确有拖拽需求，再引入 `@dnd-kit` 替换这一处即可。
 */
function RepeaterField({
  field,
  value,
  onChange,
  disabled,
}: Pick<SchemaFieldProps, 'field' | 'value' | 'onChange' | 'disabled'>) {
  const items = Array.isArray(value) ? (value as Record<string, unknown>[]) : []
  const itemSchema = field.itemSchema ?? []
  const [expanded, setExpanded] = useState<number | null>(items.length > 0 ? 0 : null)

  const update = (next: Record<string, unknown>[]) => onChange(next)

  const addItem = () => {
    const blank: Record<string, unknown> = {}
    for (const sub of itemSchema) {
      blank[sub.name] = sub.type === 'multiselect' ? [] : (sub.defaultValue ?? '')
    }
    const next = [...items, blank]
    update(next)
    setExpanded(next.length - 1)
  }

  const removeItem = (index: number) => {
    update(items.filter((_, i) => i !== index))
    setExpanded(null)
  }

  const move = (index: number, delta: number) => {
    const target = index + delta
    if (target < 0 || target >= items.length) return
    const next = [...items]
    const [moved] = next.splice(index, 1)
    next.splice(target, 0, moved)
    update(next)
    setExpanded(target)
  }

  const patchItem = (index: number, key: string, nextValue: unknown) => {
    const next = items.map((item, i) => (i === index ? { ...item, [key]: nextValue } : item))
    update(next)
  }

  return (
    <div className="space-y-2">
      {items.length === 0 ? (
        <p className="rounded-lg border border-dashed border-border px-3 py-6 text-center text-xs text-muted-foreground">
          {t('SCHEMA_NO_ITEMS')}
        </p>
      ) : (
        <ul className="space-y-2">
          {items.map((item, index) => {
            const isOpen = expanded === index
            // 折叠时用第一个有值的字段做标题，便于辨认
            const summaryField = itemSchema.find((sub) => String(item[sub.name] ?? '').trim() !== '')
            const summary = summaryField
              ? String(item[summaryField.name])
              : t('SCHEMA_ITEM_FALLBACK', { index: index + 1 })

            return (
              <li key={index} className="rounded-lg border border-border">
                <div className="flex items-center gap-1 px-2 py-1.5">
                  <button
                    type="button"
                    onClick={() => setExpanded(isOpen ? null : index)}
                    className="min-w-0 flex-1 truncate rounded px-1 py-1 text-left text-sm hover:text-brand focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    aria-expanded={isOpen}
                  >
                    {summary}
                  </button>

                  <IconButton
                    type="button"
                    label={t('SCHEMA_MOVE_UP')}
                    disabled={disabled || index === 0}
                    onClick={() => move(index, -1)}
                  >
                    <ArrowUp className="size-3.5" aria-hidden />
                  </IconButton>
                  <IconButton
                    type="button"
                    label={t('SCHEMA_MOVE_DOWN')}
                    disabled={disabled || index === items.length - 1}
                    onClick={() => move(index, 1)}
                  >
                    <ArrowDown className="size-3.5" aria-hidden />
                  </IconButton>
                  <IconButton
                    type="button"
                    label={t('COMMON_DELETE')}
                    disabled={disabled}
                    onClick={() => removeItem(index)}
                    className="hover:text-destructive"
                  >
                    <Trash2 className="size-3.5" aria-hidden />
                  </IconButton>
                </div>

                {isOpen ? (
                  <div className="space-y-3 border-t border-border p-3">
                    {itemSchema.map((sub) => (
                      <SchemaFieldRow
                        key={sub.name}
                        field={sub}
                        value={item[sub.name]}
                        onChange={(nextValue) => patchItem(index, sub.name, nextValue)}
                        disabled={disabled}
                      />
                    ))}
                  </div>
                ) : null}
              </li>
            )
          })}
        </ul>
      )}

      <Button type="button" variant="outline" size="sm" onClick={addItem} disabled={disabled}>
        <Plus aria-hidden />
        {t('SCHEMA_ADD_ITEM')}
      </Button>
    </div>
  )
}

/** 带 label / help / error 外壳的字段行（供顶层与 repeater 内部共用）。 */
export function SchemaFieldRow(props: SchemaFieldProps) {
  const { field, error, className } = props
  const controlId = `schema-field-${field.name}`

  return (
    <div className={cn('space-y-1.5', className)}>
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor={controlId}>
          {field.label}
          {field.required ? (
            <span className="ml-0.5 text-destructive" aria-hidden>
              *
            </span>
          ) : null}
        </Label>
        {field.type === 'unknown' ? (
          <span className="inline-flex items-center gap-1 text-xs text-warning">
            <AlertTriangle className="size-3" aria-hidden />
            {t('SCHEMA_UNKNOWN_TYPE', { type: field.rawType })}
          </span>
        ) : null}
      </div>

      <FieldControl {...props} />

      {field.help ? <p className="text-xs text-muted-foreground">{field.help}</p> : null}
      {error ? <p className="text-xs text-destructive">{error}</p> : null}
    </div>
  )
}
