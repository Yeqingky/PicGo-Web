import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { AlertCircle, HardDrive, Info, Loader2, MoreHorizontal, Plus, Star, Zap } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'

import { ConfirmDialog } from '@/components/common/confirm-dialog'
import { PageHeader } from '@/components/common/page-header'
import {
  SchemaForm,
  adaptPluginSchema,
  initialValues,
  stripUnchangedSecrets,
  validateRequired,
} from '@/components/schema-form'
import { CapabilityBadges } from '@/components/storage/capability-badges'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { EmptyState } from '@/components/ui/empty-state'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Sheet,
  SheetContent,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { toast } from '@/components/ui/toast'
import { useDriverSchema, useStorageConfigs, useStorageDrivers } from '@/hooks/api'
import { t } from '@/i18n'
import { storageApi } from '@/lib/api'
import { toApiError, type StorageConfig } from '@/types/api'
import { cn } from '@/lib/utils'

/**
 * 存储驱动（DESIGN.md §5.3，admin）。
 *
 * 三条关键语义：
 *  1. **同一驱动类型可有多条实例**（D64）：列表展示 `Name`，内部一律用 `UID`
 *  2. **密钥字段遮蔽显示，未改动则不提交**（后端保留原值，D78）
 *  3. `PicgoConfigName` **创建后只读**（界面上显示但不可编辑）
 *
 * 动态表单由 agent 求值后的 schema 渲染 —— **前端永不执行插件代码**（§7）。
 */
export function AdminStoragePage() {
  const { items, loading, error, refresh, defaultConfig } = useStorageConfigs({}, { pageSize: 100 })
  const { drivers } = useStorageDrivers()

  const [editing, setEditing] = useState<StorageConfig | null>(null)
  const [creating, setCreating] = useState(false)
  const [deleting, setDeleting] = useState<StorageConfig | null>(null)
  const [testingUID, setTestingUID] = useState('')

  /** 连通性测试（结果用 Toast 展示，含延迟） */
  const runTest = async (config: StorageConfig) => {
    setTestingUID(config.UID)
    try {
      const result = await storageApi.test(config.UID)
      if (result.Ok) {
        toast.success(`${result.Message}（${result.LatencyMs}ms）`)
      } else {
        toast.error(result.Message || t('STORAGE_TEST_FAILED'))
      }
    } catch (err) {
      toast.error(toApiError(err).message)
    } finally {
      setTestingUID('')
    }
  }

  const activate = async (config: StorageConfig) => {
    try {
      await storageApi.activate(config.UID)
      toast.success(t('STORAGE_ACTIVATED', { name: config.Name }))
      refresh()
    } catch (err) {
      toast.error(toApiError(err).message)
    }
  }

  const toggleEnabled = async (config: StorageConfig, enabled: boolean) => {
    try {
      await storageApi.update(config.UID, { Enabled: enabled })
      refresh()
    } catch (err) {
      toast.error(toApiError(err).message)
    }
  }

  const confirmDelete = async () => {
    if (!deleting) return
    await storageApi.remove(deleting.UID, false)
    toast.success(t('STORAGE_DELETED'))
    refresh()
  }

  return (
    <>
      <PageHeader
        title={t('NAV_ADMIN_STORAGE')}
        description={t('STORAGE_DESC')}
        actions={
          <Button variant="brand" size="sm" onClick={() => setCreating(true)}>
            <Plus aria-hidden />
            {t('STORAGE_CREATE')}
          </Button>
        }
      />

      {loading && items.length === 0 ? (
        <div className="space-y-3">
          {Array.from({ length: 3 }, (_, index) => (
            <Skeleton key={index} className="h-28 w-full" />
          ))}
        </div>
      ) : error ? (
        <EmptyState
          title={t('STORAGE_LOAD_FAILED')}
          description={toApiError(error).message}
          action={
            <Button variant="outline" onClick={refresh}>
              {t('COMMON_RETRY')}
            </Button>
          }
        />
      ) : items.length === 0 ? (
        <EmptyState
          icon={HardDrive}
          title={t('STORAGE_EMPTY')}
          description={t('STORAGE_EMPTY_DESC')}
          action={
            <Button variant="brand" onClick={() => setCreating(true)}>
              {t('STORAGE_CREATE')}
            </Button>
          }
        />
      ) : (
        <div className="space-y-3">
          {items.map((config) => (
            <Card key={config.UID} className={cn(config.IsDefault && 'ring-1 ring-brand')}>
              <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
                <div className="min-w-0 space-y-1">
                  <CardTitle className="flex flex-wrap items-center gap-2 text-base">
                    <span className="truncate">{config.Name}</span>
                    {config.IsDefault ? (
                      <Badge variant="brand" className="gap-1">
                        <Star className="size-3" aria-hidden />
                        {t('STORAGE_DEFAULT_BADGE')}
                      </Badge>
                    ) : null}
                    {!config.Enabled ? (
                      <Badge variant="secondary">{t('STORAGE_DISABLED_BADGE')}</Badge>
                    ) : null}
                    {config.HasSecrets ? null : (
                      <Badge variant="warning">{t('STORAGE_NO_SECRETS_BADGE')}</Badge>
                    )}
                  </CardTitle>

                  <p className="text-xs text-muted-foreground">
                    <span className="font-mono">{config.Type}</span>
                    {' · '}
                    {t('STORAGE_PICGO_CONFIG_NAME')}: <span className="font-mono">{config.PicgoConfigName}</span>
                    {' · '}
                    {t('STORAGE_UPLOAD_COUNT', { count: config.UploadCount })}
                  </p>

                  <p className="text-xs text-muted-foreground">
                    {/* 驱动不支持自定义路径时魔法路径没有意义（会降级为文件名前缀），不展示；
                        ServerRenames 时魔法文件名也被驱动无视，同样不展示 */}
                    {config.Capabilities?.SupportsPathTemplate ? (
                      <>
                        {t('STORAGE_PATH_TEMPLATE')}:{' '}
                        {config.PathTemplate ? (
                          <code className="rounded bg-muted px-1 py-0.5">{config.PathTemplate}</code>
                        ) : (
                          t('STORAGE_TEMPLATE_UNSET')
                        )}
                        {config.FileTemplate && !config.Capabilities?.ServerRenames ? ' · ' : null}
                      </>
                    ) : null}
                    {config.FileTemplate && !config.Capabilities?.ServerRenames ? (
                      <>
                        {t('STORAGE_FILE_TEMPLATE')}:{' '}
                        <code className="rounded bg-muted px-1 py-0.5">{config.FileTemplate}</code>
                      </>
                    ) : null}
                  </p>

                  <CapabilityBadges capabilities={config.Capabilities} className="pt-1" />
                </div>

                <div className="flex shrink-0 items-center gap-1">
                  <Switch
                    checked={config.Enabled}
                    onCheckedChange={(checked) => void toggleEnabled(config, checked)}
                    aria-label={t('STORAGE_ENABLED_TOGGLE')}
                  />
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <IconButton label={t('COMMON_MORE')} className="size-8">
                        <MoreHorizontal aria-hidden />
                      </IconButton>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem
                        disabled={testingUID === config.UID}
                        onSelect={() => void runTest(config)}
                      >
                        <Zap aria-hidden />
                        {t('STORAGE_TEST')}
                      </DropdownMenuItem>
                      <DropdownMenuItem onSelect={() => setEditing(config)}>
                        {t('COMMON_EDIT')}
                      </DropdownMenuItem>
                      {!config.IsDefault ? (
                        <DropdownMenuItem onSelect={() => void activate(config)}>
                          <Star aria-hidden />
                          {t('STORAGE_SET_DEFAULT')}
                        </DropdownMenuItem>
                      ) : null}
                      <DropdownMenuItem
                        className="text-destructive focus:text-destructive"
                        onSelect={() => setDeleting(config)}
                      >
                        {t('COMMON_DELETE')}
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>
              </CardHeader>

              <CardContent className="pt-0">
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={testingUID === config.UID}
                    onClick={() => void runTest(config)}
                  >
                    {testingUID === config.UID ? (
                      <Loader2 className="size-4 animate-spin" aria-hidden />
                    ) : (
                      <Zap aria-hidden />
                    )}
                    {t('STORAGE_TEST')}
                  </Button>
                  <Button variant="outline" size="sm" onClick={() => setEditing(config)}>
                    {t('COMMON_EDIT')}
                  </Button>
                  {!config.IsDefault ? (
                    <Button variant="ghost" size="sm" onClick={() => void activate(config)}>
                      {t('STORAGE_SET_DEFAULT')}
                    </Button>
                  ) : null}
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {/* 新建 / 编辑抽屉 */}
      <StorageConfigDrawer
        open={creating || editing !== null}
        config={editing}
        drivers={drivers}
        defaultConfig={defaultConfig}
        onOpenChange={(open) => {
          if (!open) {
            setCreating(false)
            setEditing(null)
          }
        }}
        onSaved={() => {
          refresh()
          setCreating(false)
          setEditing(null)
        }}
      />

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open) setDeleting(null)
        }}
        title={t('STORAGE_DELETE_TITLE', { name: deleting?.Name ?? '' })}
        description={
          deleting && deleting.UploadCount > 0
            ? t('STORAGE_DELETE_HAS_UPLOADS', { count: deleting.UploadCount })
            : t('STORAGE_DELETE_DESC')
        }
        confirmLabel={t('COMMON_DELETE')}
        destructive
        onConfirm={confirmDelete}
      />
    </>
  )
}

// ---------------------------------------------------------------------------
// 新建 / 编辑抽屉
// ---------------------------------------------------------------------------

/** 魔法路径/文件名的可用变量（D70 常用集）。 */
const TEMPLATE_VARS = [
  '{Y}', '{m}', '{d}', '{H}', '{i}', '{s}',
  '{timestamp}', '{filename}', '{md5}', '{md5-8}', '{sha256-8}',
  '{uid}', '{uniqid}', '{extname}',
]

interface DrawerProps {
  open: boolean
  /** 非空 = 编辑模式 */
  config: StorageConfig | null
  drivers: { Type: string; Name: string; ConfigCount: number }[]
  defaultConfig: StorageConfig | undefined
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}

function StorageConfigDrawer({
  open,
  config,
  drivers,
  defaultConfig,
  onOpenChange,
  onSaved,
}: DrawerProps) {
  const editing = config !== null

  const [type, setType] = useState('')
  const [name, setName] = useState('')
  const [picgoConfigName, setPicgoConfigName] = useState('Default')
  const [enabled, setEnabled] = useState(true)
  const [isDefault, setIsDefault] = useState(false)
  const [pathTemplate, setPathTemplate] = useState('')
  const [fileTemplate, setFileTemplate] = useState('')
  const [values, setValues] = useState<Record<string, unknown>>({})
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [testAfterSave, setTestAfterSave] = useState(false)

  // 驱动 schema（按 `type` 求值；编辑时用该配置的类型）
  const { schema, loading: schemaLoading, reevaluate } = useDriverSchema(type, { skip: !open || !type })
  const adapted = useMemo(() => adaptPluginSchema(schema), [schema])

  // 初始化表单
  useEffect(() => {
    if (!open) return

    if (config) {
      setType(config.Type)
      setName(config.Name)
      setPicgoConfigName(config.PicgoConfigName)
      setEnabled(config.Enabled)
      setIsDefault(config.IsDefault)
      setPathTemplate(config.PathTemplate)
      setFileTemplate(config.FileTemplate)
      // 编辑时不回填密钥（前端拿不到值）；用户不填 = 不修改
      setValues(initialValues(adaptPluginSchema([]).fields))
    } else {
      setType(drivers[0]?.Type ?? '')
      setName('')
      setPicgoConfigName('Default')
      setEnabled(true)
      // 第二、第三条配置不再自动设为「默认」，避免误切换
      setIsDefault(!defaultConfig)
      setPathTemplate('')
      setFileTemplate('')
      setValues({})
    }
    setErrors({})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, config])

  // schema 变化时补齐初始值（保留用户已填的部分）
  useEffect(() => {
    if (!open || adapted.fields.length === 0) return
    setValues((prev) => {
      const next = initialValues(adapted.fields, prev)
      return next
    })
  }, [open, adapted.fields])

  const insertVar = (variable: string, target: 'path' | 'file') => {
    if (target === 'path') setPathTemplate((prev) => prev + variable)
    else setFileTemplate((prev) => prev + variable)
  }

  const submit = async () => {
    const trimmedName = name.trim()
    if (!trimmedName) {
      toast.error(t('STORAGE_NAME_REQUIRED'))
      return
    }
    if (!type) {
      toast.error(t('STORAGE_TYPE_REQUIRED'))
      return
    }

    const requiredErrors = validateRequired(adapted.fields, values, t('COMMON_REQUIRED'))
    if (Object.keys(requiredErrors).length > 0) {
      setErrors(requiredErrors)
      toast.error(t('SETTINGS_VALIDATE_FAILED'))
      return
    }

    setBusy(true)
    try {
      let saved: StorageConfig

      if (editing && config) {
        saved = await storageApi.update(config.UID, {
          Name: trimmedName,
          Enabled: enabled,
          IsDefault: isDefault,
          PathTemplate: pathTemplate,
          FileTemplate: fileTemplate,
        })

        // 只提交**用户实际填了**的凭据字段。
        //
        // 为什么不回填已有密钥：后端只返回「是否已配置」（`HasSecrets`），不返回值（D78）。
        // 因此前端无法区分「未改动」与「想清空」—— 这里采取保守策略：
        // **空 = 不改**（后端 merge 语义会保留原值）。需要清空时属于少数场景，
        // 后续可加专门的「清除凭据」操作，而不是让用户在一个看不见值的框里猜。
        const secretPayload = stripUnchangedSecrets(adapted.fields, values, '******')
        const patch: Record<string, unknown> = {}
        for (const [key, value] of Object.entries(secretPayload)) {
          if (value !== undefined && value !== '') patch[key] = value
        }
        if (Object.keys(patch).length > 0) {
          await storageApi.updateSecrets(config.UID, { Config: patch })
        }
      } else {
        saved = await storageApi.create({
          Name: trimmedName,
          Type: type,
          PicgoConfigName: picgoConfigName.trim() || 'Default',
          Enabled: enabled,
          IsDefault: isDefault,
          PathTemplate: pathTemplate,
          FileTemplate: fileTemplate,
          Config: values,
        })
      }

      toast.success(editing ? t('STORAGE_UPDATED') : t('STORAGE_CREATED'))

      // 「保存后立即测试」：给管理员即时反馈，避免先保存再手动点测试
      if (testAfterSave) {
        try {
          const result = await storageApi.test(saved.UID)
          if (result.Ok) {
            toast.success(`${result.Message}（${result.LatencyMs}ms）`)
          } else {
            toast.error(result.Message || t('STORAGE_TEST_FAILED'))
          }
        } catch (err) {
          toast.error(toApiError(err).message)
        }
      }

      onSaved()
    } catch (err) {
      toast.error(toApiError(err).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-2xl">
        <SheetHeader className="border-b border-border px-5 py-4">
          <SheetTitle>{editing ? t('STORAGE_EDIT_TITLE') : t('STORAGE_CREATE_TITLE')}</SheetTitle>
        </SheetHeader>

        <div className="flex-1 space-y-5 overflow-y-auto px-5 py-4 scrollbar-thin">
          {/* 基本信息 */}
          <section className="space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="storage-name">{t('STORAGE_NAME')}</Label>
              <Input
                id="storage-name"
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder={t('STORAGE_NAME_PLACEHOLDER')}
              />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="storage-type">{t('STORAGE_TYPE')}</Label>
              {editing ? (
                // 驱动类型创建后不可改（改了等于换驱动，应该新建一条）
                <Input id="storage-type" value={type} readOnly disabled />
              ) : (
                <Select
                  value={type}
                  onValueChange={(value) => {
                    setType(value)
                    setValues({})
                  }}
                >
                  <SelectTrigger id="storage-type">
                    <SelectValue placeholder={t('STORAGE_TYPE_PLACEHOLDER')} />
                  </SelectTrigger>
                  <SelectContent>
                    {drivers.map((driver) => (
                      <SelectItem key={driver.Type} value={driver.Type}>
                        {driver.Name}（{driver.Type}）
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="storage-config-name">{t('STORAGE_PICGO_CONFIG_NAME')}</Label>
              <Input
                id="storage-config-name"
                value={picgoConfigName}
                readOnly
                disabled
                aria-describedby="storage-config-name-hint"
              />
              <p id="storage-config-name-hint" className="text-xs text-muted-foreground">
                {t('STORAGE_PICGO_CONFIG_NAME_HINT')}
              </p>
            </div>

            <div className="flex flex-wrap items-center gap-6">
              <div className="flex items-center gap-2">
                <Switch id="storage-enabled" checked={enabled} onCheckedChange={setEnabled} />
                <Label htmlFor="storage-enabled">{t('STORAGE_ENABLED')}</Label>
              </div>
              <div className="flex items-center gap-2">
                <Switch id="storage-default" checked={isDefault} onCheckedChange={setIsDefault} />
                <Label htmlFor="storage-default">{t('STORAGE_SET_DEFAULT')}</Label>
              </div>
            </div>
          </section>

          {/* 驱动配置（动态表单，§7） */}
          <section className="space-y-3">
            <h3 className="text-sm font-medium text-foreground">{t('STORAGE_SECTION_CREDENTIALS')}</h3>

            {editing ? (
              <Alert variant="info">
                <AlertDescription>{t('STORAGE_SECRETS_KEEP_HINT')}</AlertDescription>
              </Alert>
            ) : null}

            {!type ? (
              <p className="text-sm text-muted-foreground">{t('STORAGE_PICK_TYPE_FIRST')}</p>
            ) : schemaLoading ? (
              <Skeleton className="h-24 w-full" />
            ) : adapted.fields.length === 0 ? (
              <Alert variant="info">
                <AlertDescription>{t('STORAGE_NO_CONFIG_FIELDS')}</AlertDescription>
              </Alert>
            ) : (
              <SchemaForm
                schema={adapted}
                values={values}
                onChange={(fieldName, value) => {
                  const next = { ...values, [fieldName]: value }
                  setValues(next)
                  // `DependsOn` 联动：回源 agent 重求值（**前端不执行插件代码**）
                  if (adapted.hasDependencies) {
                    void reevaluate(next).catch(() => {
                      // 失败保留旧选项即可，不打断用户
                    })
                  }
                }}
                errors={errors}
                disabled={busy}
              />
            )}
          </section>

          {/* 魔法路径 / 文件名（D43 / D70）
              降级链（PicList 同款能力探测）：
                · 驱动不支持自定义路径 → 不显示魔法路径（会降级为文件名前缀）
                · 实测图床无视传入文件名（ServerRenames）→ 魔法文件名也没有意义，
                  用说明替代输入框；探测由「测试连通性」或首次真实上传触发 */}
          <section className="space-y-3">
            {config?.Capabilities?.ServerRenames ? (
              <Alert variant="info">
                <AlertCircle aria-hidden />
                <AlertDescription>{t('STORAGE_CAP_SERVER_RENAMES_REASON')}</AlertDescription>
              </Alert>
            ) : config && !config.Capabilities.SupportsPathTemplate ? (
              <div className="space-y-1.5">
                <Label htmlFor="file-template">{t('STORAGE_FILE_TEMPLATE')}</Label>
                <Input
                  id="file-template"
                  value={fileTemplate}
                  onChange={(event) => setFileTemplate(event.target.value)}
                  placeholder="{uniqid}{extname}"
                />
              </div>
            ) : (
              <>
                {/* 魔法文件名语义提示（AlertTitle 与图标同行） */}
                <Alert variant="info">
                  <Info aria-hidden />
                  <AlertTitle>{t('STORAGE_FILE_TEMPLATE_NOTICE_TITLE')}</AlertTitle>
                  <AlertDescription>{t('STORAGE_FILE_TEMPLATE_NOTICE_DESC')}</AlertDescription>
                </Alert>

                <div className="space-y-1.5">
                  <Label htmlFor="path-template">{t('STORAGE_PATH_TEMPLATE')}</Label>
                  <Input
                    id="path-template"
                    value={pathTemplate}
                    onChange={(event) => setPathTemplate(event.target.value)}
                    placeholder="{Y}/{m}/{d}"
                  />
                </div>

                <div className="space-y-1.5">
                  <Label htmlFor="file-template">{t('STORAGE_FILE_TEMPLATE')}</Label>
                  <Input
                    id="file-template"
                    value={fileTemplate}
                    onChange={(event) => setFileTemplate(event.target.value)}
                    placeholder="{uniqid}{extname}"
                  />
                </div>

                {/* 变量：点击即插入（避免用户手打错） */}
                <div className="space-y-1.5">
                  <Label className="text-xs text-muted-foreground">{t('STORAGE_TEMPLATE_VARS_TIP')}</Label>
                  <div className="flex flex-wrap gap-1">
                    {TEMPLATE_VARS.map((variable) => (
                      <button
                        key={variable}
                        type="button"
                        onClick={() => insertVar(variable, 'file')}
                        className="rounded border border-border bg-muted/40 px-1.5 py-0.5 font-mono text-[11px] text-foreground transition-colors hover:border-brand hover:text-brand focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                        title={t('STORAGE_TEMPLATE_INSERT_FILE')}
                      >
                        {variable}
                      </button>
                    ))}
                  </div>
                  <p className="text-xs text-muted-foreground">{t('STORAGE_TEMPLATE_VARS_HINT')}</p>
                </div>
              </>
            )}
          </section>
        </div>

        <SheetFooter className="flex-row items-center justify-between gap-3 border-t border-border px-5 py-3">
          <div className="flex items-center gap-2">
            <Switch
              id="test-after-save"
              checked={testAfterSave}
              onCheckedChange={setTestAfterSave}
            />
            <Label htmlFor="test-after-save" className="text-xs text-muted-foreground">
              {t('STORAGE_TEST_AFTER_SAVE')}
            </Label>
          </div>

          <div className="flex items-center gap-2">
            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
              {t('COMMON_CANCEL')}
            </Button>
            <Button variant="brand" onClick={() => void submit()} disabled={busy}>
              {busy ? <Loader2 className="size-4 animate-spin" aria-hidden /> : null}
              {t('COMMON_SAVE')}
            </Button>
          </div>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
