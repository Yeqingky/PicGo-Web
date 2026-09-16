import { AlertTriangle, Loader2, Save, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'

import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { SchemaForm, adaptThemeSchema, initialValues, validateRequired } from '@/components/schema-form'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { toast } from '@/components/ui/toast'
import { useThemeSettings } from '@/hooks/api'
import { themeApi } from '@/lib/api'
import { t } from '@/i18n'
import { toApiError, type ThemeListItem } from '@/types/api'

/**
 * 主题设置抽屉（DESIGN.md §5.8.2 / §7）。
 *
 * 用**同一套渲染器**（§7）渲染主题 schema（`string`/`text`/`number`/`switch`/`select`/`json`）。
 * 与插件 schema 的差别：**主题 schema 是静态的**（无 `dependsOn`、不回源）。
 *
 * 每项右侧显示 `Source` 徽章：**来自数据库 / 使用默认值**（D95 三级兜底）。
 */
export interface ThemeSettingsDrawerProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  theme: ThemeListItem | null
  /** 清理配置成功后回调（刷新列表） */
  onCleared?: () => void
}

export function ThemeSettingsDrawer({
  open,
  onOpenChange,
  theme,
  onCleared,
}: ThemeSettingsDrawerProps) {
  const themeID = theme?.ID ?? ''
  const { settings, values, loading, error, refresh } = useThemeSettings(themeID, { skip: !open })

  const [form, setForm] = useState<Record<string, unknown>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  const [confirmClear, setConfirmClear] = useState(false)

  // schema 适配（纯数据变换，可缓存）
  const adapted = useMemo(() => adaptThemeSchema(settings?.Schema), [settings?.Schema])

  // 打开或数据变化时初始化表单值（已存值优先）
  useEffect(() => {
    if (!open || !settings) return
    setForm(initialValues(adapted.fields, values))
    setErrors({})
    // values 每次渲染都是新对象，但它由 settings 派生；用 settings 做依赖即可
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, settings, adapted.fields])

  const sourceOf = (key: string): 'db' | 'default' =>
    settings?.Values?.[key]?.Source ?? 'default'

  const save = async () => {
    const requiredErrors = validateRequired(
      adapted.fields,
      form,
      t('COMMON_REQUIRED'),
    )
    if (Object.keys(requiredErrors).length > 0) {
      setErrors(requiredErrors)
      toast.error(t('SETTINGS_VALIDATE_FAILED'))
      return
    }

    setSaving(true)
    try {
      await themeApi.updateSettings(themeID, { Values: form })
      toast.success(t('THEME_SETTINGS_SAVED'))
      refresh()
    } catch (err) {
      toast.error(toApiError(err).message)
    } finally {
      setSaving(false)
    }
  }

  const clear = async () => {
    await themeApi.clearSettings(themeID)
    toast.success(t('THEME_SETTINGS_CLEARED'))
    refresh()
    onCleared?.()
  }

  return (
    <>
      <Sheet open={open} onOpenChange={onOpenChange}>
        <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-xl">
          <SheetHeader className="border-b border-border px-5 py-4">
            <SheetTitle>{t('THEME_SETTINGS_TITLE', { name: theme?.Name ?? '' })}</SheetTitle>
            <SheetDescription>
              <span className="font-mono text-xs">{themeID}</span>
            </SheetDescription>
          </SheetHeader>

          <div className="flex-1 space-y-4 overflow-y-auto px-5 py-4 scrollbar-thin">
            {/* 影响面提示：该主题会接管哪些页面 */}
            {theme?.Pages && theme.Pages.length > 0 ? (
              <div className="space-y-1.5 rounded-md border border-border px-3 py-2.5">
                <p className="text-xs font-medium text-muted-foreground">
                  {t('THEME_SETTINGS_PAGES_LABEL')}
                </p>
                <div className="flex flex-wrap gap-1">
                  {theme.Pages.map((page) => (
                    <code key={page} className="rounded bg-muted px-1.5 py-0.5 text-[11px]">
                      {page}
                    </code>
                  ))}
                </div>
              </div>
            ) : null}

            {error ? (
              <Alert variant="destructive">
                <AlertDescription>{t('THEME_SETTINGS_LOAD_FAILED')}</AlertDescription>
              </Alert>
            ) : null}

            {loading && !settings ? (
              <p className="flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" aria-hidden />
                {t('COMMON_LOADING')}
              </p>
            ) : adapted.fields.length === 0 ? (
              <Alert variant="info">
                <AlertDescription>{t('THEME_SETTINGS_EMPTY')}</AlertDescription>
              </Alert>
            ) : (
              <div className="space-y-4">
                <SchemaForm
                  schema={adapted}
                  values={form}
                  onChange={(name, value) => setForm((prev) => ({ ...prev, [name]: value }))}
                  errors={errors}
                  disabled={saving}
                />

                {/* 来源徽章（帮助管理员判断「这个值是我设的还是默认的」） */}
                <div className="space-y-1.5 rounded-md border border-border px-3 py-2.5">
                  <Label className="text-xs text-muted-foreground">
                    {t('SETTINGS_SOURCE_TITLE')}
                  </Label>
                  <ul className="space-y-1">
                    {adapted.fields.map((field) => (
                      <li key={field.name} className="flex items-center justify-between gap-2 text-xs">
                        <span className="truncate text-muted-foreground">{field.label}</span>
                        <Badge variant={sourceOf(field.name) === 'db' ? 'brand' : 'secondary'}>
                          {sourceOf(field.name) === 'db'
                            ? t('SETTINGS_SOURCE_DB')
                            : t('SETTINGS_SOURCE_DEFAULT')}
                        </Badge>
                      </li>
                    ))}
                  </ul>
                </div>
              </div>
            )}

            {/* 危险区 */}
            <div className="space-y-2 rounded-md border border-destructive/30 px-3 py-3">
              <div className="flex items-start gap-2">
                <AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" aria-hidden />
                <div className="space-y-0.5">
                  <p className="text-sm font-medium text-destructive">{t('THEME_DANGER_TITLE')}</p>
                  <p className="text-xs text-muted-foreground">{t('THEME_DANGER_CLEAR_DESC')}</p>
                </div>
              </div>
              <Button
                type="button"
                variant="destructive"
                size="sm"
                disabled={theme?.IsActive || saving}
                onClick={() => setConfirmClear(true)}
                title={theme?.IsActive ? t('THEME_DANGER_CLEAR_ACTIVE_HINT') : undefined}
              >
                <Trash2 aria-hidden />
                {t('THEME_DANGER_CLEAR')}
              </Button>
            </div>
          </div>

          <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3">
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
              {t('COMMON_CLOSE')}
            </Button>
            <Button
              type="button"
              variant="brand"
              onClick={() => void save()}
              disabled={saving || adapted.fields.length === 0}
            >
              {saving ? <Loader2 className="size-4 animate-spin" aria-hidden /> : <Save aria-hidden />}
              {t('COMMON_SAVE')}
            </Button>
          </div>
        </SheetContent>
      </Sheet>

      {/* 清理确认（用 ConfirmDialog：异步确认按钮不能是 AlertDialogAction） */}
      <ConfirmDialog
        open={confirmClear}
        onOpenChange={setConfirmClear}
        title={t('THEME_DANGER_CLEAR')}
        description={t('THEME_DANGER_CLEAR_CONFIRM', { name: theme?.Name ?? themeID })}
        confirmLabel={t('COMMON_DELETE')}
        destructive
        onConfirm={clear}
        onConfirmed={onCleared}
      />
    </>
  )
}
