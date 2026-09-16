import { Info, Loader2, Mail, RotateCw, Save } from 'lucide-react'
import { useMemo, useState } from 'react'

import { PageHeader } from '@/components/common/page-header'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { toast } from '@/components/ui/toast'
import { useSystemInfo } from '@/hooks/api/use-system'
import { useSystemSettings } from '@/hooks/api'
import { settingsApi } from '@/lib/api'
import { t } from '@/i18n'
import { formatDateTime } from '@/lib/format'
import { toApiError, type SystemSettingItem } from '@/types/api'
import { cn } from '@/lib/utils'

/** 将 JSON 值递归排序，避免仅因对象键顺序不同而误判为已修改。 */
function sortJSON(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortJSON)
  if (value !== null && typeof value === 'object') {
    const object = value as Record<string, unknown>
    return Object.fromEntries(
      Object.keys(object)
        .sort()
        .map((key) => [key, sortJSON(object[key])]),
    )
  }
  return value
}

/** 把设置值规约成可比较的形式，兼容 API 返回值与输入控件值的类型差异。 */
function comparableSettingValue(item: SystemSettingItem | undefined, value: unknown): string {
  if (!item) return String(value ?? '')

  switch (item.Type) {
    case 'bool':
      return String(value === true || value === 'true')
    case 'int': {
      if (value === '' || value === null || value === undefined) return ''
      const numeric = typeof value === 'number' ? value : Number(value)
      return Number.isNaN(numeric) ? String(value) : String(numeric)
    }
    case 'json': {
      const parsed = typeof value === 'string' ? (() => {
        try {
          return JSON.parse(value) as unknown
        } catch {
          return value.trim()
        }
      })() : value ?? []
      return JSON.stringify(sortJSON(parsed)) ?? ''
    }
    default:
      return String(value ?? '')
  }
}

function sameSettingValue(item: SystemSettingItem | undefined, left: unknown, right: unknown): boolean {
  return comparableSettingValue(item, left) === comparableSettingValue(item, right)
}

/**
 * 站点设置（DESIGN.md §5.7，admin）。
 *
 * ⚠️ **没有「首页内容」与「背景图」两个 Tab**（D95 归类原则：
 *    「主题的画法 → 主题配置；站点属性 → `site.*`」）—— 它们已迁到
 *    **主题管理 → 主题设置**（`/admin/themes`）。
 *
 * 键名是 **`dot.lowerCamel` 原样**（D81.3 第 3 条）：它们是 KV 表的字符串 key。
 * 每项右侧显示 `Source` 徽章（**来自数据库 / 使用默认值**）。
 *
 * 关于 Tab 划分的一处**主动补充**：DESIGN.md §5.7 列了 6 个 Tab
 * （站点信息 / 邮件 / 登录方式 / 安全 / 日志 / 关于），但后端共有 9 个配置分类
 * （多出 `user` / `upload` / `picgo` / `integration`）。若不给出入口，管理员将无法修改
 * 上传限制、npm 源、Lsky 兼容层开关等**必需**项。因此这里把它们并入
 * 「站点信息」（`user`：新建用户默认配额属于站点策略）与新增的「高级」Tab。
 */
export function AdminSitePage() {
  const { groups, getItem, loading, error, refresh } = useSystemSettings()
  const { info } = useSystemInfo()

  const [tab, setTab] = useState('site')

  /** 待提交的改动（键 → 新值）。只提交改动过的键，避免无意义的整表写入。 */
  const [dirty, setDirty] = useState<Record<string, unknown>>({})
  const [saving, setSaving] = useState(false)
  const [mailTo, setMailTo] = useState('')
  const [testingMail, setTestingMail] = useState(false)

  const oauthCallback = useMemo(() => {
    const base = getItem('site.baseUrl')?.Value
    const origin = typeof base === 'string' && base.trim() ? base.trim().replace(/\/$/, '') : window.location.origin
    return `${origin}/api/web/v1/auth/oauth/github/callback`
  }, [getItem])

  const valueOf = (key: string): unknown => {
    if (key in dirty) return dirty[key]
    const item = getItem(key)
    if (!item) return undefined
    // secret 类型：不在表单里回填掩码（用户看不到原值，留空即不修改）
    return item.Type === 'secret' ? '' : item.Value
  }

  const setValue = (key: string, value: unknown) => {
    setDirty((prev) => {
      const item = getItem(key)
      // dirty 只记录「当前值与初始值不同」的字段。
      // 用户修改后还原原值时，必须移除该字段，否则会一直显示「未保存」。
      const original = item?.Type === 'secret' ? '' : item?.Value
      if (sameSettingValue(item, value, original)) {
        if (!(key in prev)) return prev
        const next = { ...prev }
        delete next[key]
        return next
      }
      return { ...prev, [key]: value }
    })
  }

  const save = async () => {
    const keys = Object.keys(dirty)
    if (keys.length === 0) {
      toast.info(t('SETTINGS_NO_CHANGES'))
      return
    }

    // secret 字段：留空表示不修改，直接剔除（后端也支持掩码，但这里更直白）
    const payload: Record<string, unknown> = {}
    for (const key of keys) {
      const item = getItem(key)
      const value = dirty[key]
      if (item?.Type === 'secret' && (value === '' || value === undefined)) continue
      payload[key] = value
    }

    if (Object.keys(payload).length === 0) {
      toast.info(t('SETTINGS_NO_CHANGES'))
      return
    }

    setSaving(true)
    try {
      const result = await settingsApi.updateSystem(payload)
      toast.success(t('SETTINGS_SAVED', { count: result.Applied.length }))
      setDirty({})
      refresh()
    } catch (err) {
      toast.error(toApiError(err).message)
    } finally {
      setSaving(false)
    }
  }

  const testMail = async () => {
    const to = mailTo.trim()
    if (!to) {
      toast.error(t('MAIL_TEST_NEEDS_ADDRESS'))
      return
    }

    setTestingMail(true)
    try {
      const result = await settingsApi.testMail({ To: to })
      if (result.Ok) toast.success(result.Message || t('MAIL_TEST_OK'))
      else toast.error(result.Message || t('MAIL_TEST_FAILED'))
    } catch (err) {
      toast.error(toApiError(err).message)
    } finally {
      setTestingMail(false)
    }
  }

  const renderGroup = (keys: string[], emptyHint?: string) => {
    if (keys.length === 0) {
      return <p className="text-sm text-muted-foreground">{emptyHint ?? t('SETTINGS_GROUP_EMPTY')}</p>
    }

    return (
      <div className="space-y-4">
        {keys.map((key) => (
          <SettingField
            key={key}
            item={getItem(key)}
            keyName={key}
            value={valueOf(key)}
            dirty={key in dirty}
            onChange={(value) => setValue(key, value)}
            disabled={saving}
          />
        ))}
      </div>
    )
  }

  /**
   * 按分类取键名（保持后端返回的顺序）。
   *
   * 用 hook 返回的 `groups` 而不是自己再查一遍：**单一真相源**，
   * 否则一旦后端顺序或分组变化，Tab 内容会与「来源徽章」显示不一致。
   */
  const keysOf = (...categories: string[]): string[] => {
    const out: string[] = []
    for (const category of categories) {
      const group = groups.find((entry) => entry.Category === category)
      if (group) out.push(...group.Keys.map((item) => item.Key))
    }
    return out
  }

  return (
    <>
      <PageHeader
        title={t('NAV_ADMIN_SITE')}
        description={t('SITE_DESC')}
        actions={
          <>
            <Button variant="outline" size="sm" onClick={refresh}>
              <RotateCw aria-hidden />
              {t('COMMON_REFRESH')}
            </Button>
            <Button variant="brand" size="sm" disabled={saving} onClick={() => void save()}>
              {saving ? <Loader2 className="size-4 animate-spin" aria-hidden /> : <Save aria-hidden />}
              {t('COMMON_SAVE')}
            </Button>
          </>
        }
      />

      {Object.keys(dirty).length > 0 ? (
        <Alert variant="info" className="mb-4">
          <Info aria-hidden />
          <AlertDescription>
            {t('SETTINGS_PENDING_CHANGES', { count: Object.keys(dirty).length })}
          </AlertDescription>
        </Alert>
      ) : null}

      {error ? (
        <Alert variant="destructive" className="mb-4">
          <AlertDescription>{t('SETTINGS_LOAD_FAILED')}</AlertDescription>
        </Alert>
      ) : null}

      {loading ? (
        <div className="space-y-3">
          {Array.from({ length: 5 }, (_, index) => (
            <Skeleton key={index} className="h-16 w-full" />
          ))}
        </div>
      ) : (
        <Tabs value={tab} onValueChange={setTab}>
          <TabsList className="mb-4 flex-wrap">
            <TabsTrigger value="site">{t('SITE_TAB_SITE')}</TabsTrigger>
            <TabsTrigger value="mail">{t('SITE_TAB_MAIL')}</TabsTrigger>
            <TabsTrigger value="oauth">{t('SITE_TAB_OAUTH')}</TabsTrigger>
            <TabsTrigger value="security">{t('SITE_TAB_SECURITY')}</TabsTrigger>
            <TabsTrigger value="log">{t('SITE_TAB_LOG')}</TabsTrigger>
            <TabsTrigger value="advanced">{t('SITE_TAB_ADVANCED')}</TabsTrigger>
            <TabsTrigger value="about">{t('SITE_TAB_ABOUT')}</TabsTrigger>
          </TabsList>

          {/* 站点信息（site + user：新建用户默认配额属于站点策略，D21） */}
          <TabsContent value="site">
            <Card>
              <CardHeader>
                <CardTitle className="text-base">{t('SITE_SECTION_SITE')}</CardTitle>
              </CardHeader>
              <CardContent className="space-y-5">
                {renderGroup(keysOf('site'))}

                <div className="space-y-1.5 rounded-md border border-border px-3 py-3">
                  <Label className="text-xs text-muted-foreground">
                    {t('SITE_OAUTH_CALLBACK')}
                  </Label>
                  <div className="flex items-center gap-2">
                    <code className="min-w-0 flex-1 break-all rounded bg-muted px-2 py-1 text-xs">
                      {oauthCallback}
                    </code>
                    <Button
                      variant="outline"
                      size="sm"
                      className="shrink-0"
                      onClick={() => {
                        void navigator.clipboard?.writeText(oauthCallback)
                        toast.success(t('COMMON_COPIED'))
                      }}
                    >
                      {t('COMMON_COPY')}
                    </Button>
                  </div>
                  <p className="text-xs text-muted-foreground">{t('SITE_OAUTH_CALLBACK_HINT')}</p>
                </div>

                <div className="space-y-3 border-t border-border pt-4">
                  <h3 className="text-sm font-medium text-foreground">{t('SITE_SECTION_NEW_USER')}</h3>
                  {renderGroup(keysOf('user'))}
                </div>
              </CardContent>
            </Card>
          </TabsContent>

          {/* 邮件 */}
          <TabsContent value="mail">
            <Card>
              <CardHeader>
                <CardTitle className="text-base">{t('SITE_SECTION_MAIL')}</CardTitle>
              </CardHeader>
              <CardContent className="space-y-5">
                {renderGroup(keysOf('mail'))}

                <div className="space-y-2 border-t border-border pt-4">
                  <Label htmlFor="mail-test-to">{t('MAIL_TEST_LABEL')}</Label>
                  <div className="flex items-center gap-2">
                    <Input
                      id="mail-test-to"
                      value={mailTo}
                      onChange={(event) => setMailTo(event.target.value)}
                      placeholder="name@example.com"
                    />
                    <Button
                      variant="outline"
                      disabled={testingMail}
                      onClick={() => void testMail()}
                      className="shrink-0"
                    >
                      {testingMail ? (
                        <Loader2 className="size-4 animate-spin" aria-hidden />
                      ) : (
                        <Mail aria-hidden />
                      )}
                      {t('MAIL_TEST_SEND')}
                    </Button>
                  </div>
                  <p className="text-xs text-muted-foreground">{t('MAIL_TEST_HINT')}</p>
                </div>
              </CardContent>
            </Card>
          </TabsContent>

          {/* 登录方式 */}
          <TabsContent value="oauth">
            <Card>
              <CardHeader>
                <CardTitle className="text-base">{t('SITE_SECTION_OAUTH')}</CardTitle>
              </CardHeader>
              <CardContent className="space-y-5">
                <Alert variant="info">
                  <AlertDescription>{t('SITE_OAUTH_BIND_ONLY')}</AlertDescription>
                </Alert>
                {renderGroup(keysOf('oauth'))}
              </CardContent>
            </Card>
          </TabsContent>

          {/* 安全 */}
          <TabsContent value="security">
            <Card>
              <CardHeader>
                <CardTitle className="text-base">{t('SITE_SECTION_SECURITY')}</CardTitle>
              </CardHeader>
              <CardContent>{renderGroup(keysOf('security'))}</CardContent>
            </Card>
          </TabsContent>

          {/* 日志 */}
          <TabsContent value="log">
            <Card>
              <CardHeader>
                <CardTitle className="text-base">{t('SITE_SECTION_LOG')}</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3">
                {renderGroup(keysOf('log'))}
                <p className="text-xs text-muted-foreground">{t('SITE_LOG_RETENTION_HINT')}</p>
              </CardContent>
            </Card>
          </TabsContent>

          {/* 高级（upload / picgo / integration） */}
          <TabsContent value="advanced">
            <div className="space-y-5">
              <Card>
                <CardHeader>
                  <CardTitle className="text-base">{t('SITE_SECTION_UPLOAD')}</CardTitle>
                </CardHeader>
                <CardContent>{renderGroup(keysOf('upload'))}</CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle className="text-base">{t('SITE_SECTION_PICGO')}</CardTitle>
                </CardHeader>
                <CardContent>{renderGroup(keysOf('picgo'))}</CardContent>
              </Card>

              <Card>
                <CardHeader>
                  <CardTitle className="text-base">{t('SITE_SECTION_INTEGRATION')}</CardTitle>
                </CardHeader>
                <CardContent className="space-y-3">
                  {renderGroup(keysOf('integration'))}
                  <p className="text-xs text-muted-foreground">{t('SITE_LSKY_HINT')}</p>
                </CardContent>
              </Card>
            </div>
          </TabsContent>

          {/* 关于 */}
          <TabsContent value="about">
            <Card>
              <CardHeader>
                <CardTitle className="text-base">{t('SITE_SECTION_ABOUT')}</CardTitle>
              </CardHeader>
              <CardContent className="space-y-2 text-sm">
                <div className="flex justify-between gap-4 py-1.5">
                  <span className="text-muted-foreground">{t('ABOUT_VERSION')}</span>
                  <span className="font-mono">{info?.Version ?? '—'}</span>
                </div>
                <div className="flex justify-between gap-4 py-1.5">
                  <span className="text-muted-foreground">{t('ABOUT_SCHEMA_VERSION')}</span>
                  <span className="font-mono">{info?.SchemaVersion ?? '—'}</span>
                </div>
                <div className="flex justify-between gap-4 py-1.5">
                  <span className="text-muted-foreground">{t('ABOUT_DB_DRIVER')}</span>
                  <span className="font-mono">{info?.DatabaseDriver ?? '—'}</span>
                </div>
                <div className="flex justify-between gap-4 py-1.5">
                  <span className="text-muted-foreground">{t('ABOUT_THEME')}</span>
                  <span className="font-mono">{info?.ThemeActive || '—'}</span>
                </div>
                <div className="flex justify-between gap-4 py-1.5">
                  <span className="text-muted-foreground">{t('ABOUT_UPTIME')}</span>
                  <span>{formatDateTime(Math.floor(Date.now() / 1000) - (info?.Uptime ?? 0))}</span>
                </div>
              </CardContent>
            </Card>
          </TabsContent>
        </Tabs>
      )}
    </>
  )
}

// ---------------------------------------------------------------------------

interface SettingFieldProps {
  item: SystemSettingItem | undefined
  keyName: string
  value: unknown
  dirty: boolean
  onChange: (value: unknown) => void
  disabled: boolean
}

/** 单个设置项（按 `Type` 分派控件 + `Source` 徽章）。 */
function SettingField({ item, keyName, value, dirty, onChange, disabled }: SettingFieldProps) {
  const inputId = `setting-${keyName.replace(/\./g, '-')}`

  if (!item) {
    // 后端未返回该键（例如 Schema 有但 DB 无）：显示为不可编辑，避免前端自作主张
    return (
      <div className="space-y-1">
        <Label className="text-xs text-muted-foreground">{keyName}</Label>
        <p className="text-xs text-muted-foreground">{t('SETTINGS_KEY_UNAVAILABLE')}</p>
      </div>
    )
  }

  const label = item.Label || keyName
  const isSecret = item.Type === 'secret'

  return (
    <div className="space-y-1.5">
      <div className="flex flex-wrap items-center gap-2">
        <Label htmlFor={inputId}>{label}</Label>
        <Badge variant={item.Source === 'db' ? 'brand' : 'secondary'}>
          {item.Source === 'db' ? t('SETTINGS_SOURCE_DB') : t('SETTINGS_SOURCE_DEFAULT')}
        </Badge>
        {dirty ? <Badge variant="warning">{t('SETTINGS_UNSAVED')}</Badge> : null}
        {item.RequiresRestart ? <Badge variant="outline">{t('SETTINGS_REQUIRES_RESTART')}</Badge> : null}
      </div>

      {item.Type === 'bool' ? (
        <div className="flex items-center gap-2">
          <Switch
            id={inputId}
            checked={value === true || value === 'true'}
            onCheckedChange={onChange}
            disabled={disabled}
          />
          <span className="text-xs text-muted-foreground">
            {value === true || value === 'true' ? t('COMMON_OK') : t('COMMON_CLOSE')}
          </span>
        </div>
      ) : item.Type === 'json' ? (
        <Textarea
          id={inputId}
          value={typeof value === 'string' ? value : JSON.stringify(value ?? [], null, 2)}
          onChange={(event) => onChange(event.target.value)}
          rows={4}
          className="font-mono text-xs"
          disabled={disabled}
        />
      ) : (
        <Input
          id={inputId}
          type={isSecret ? 'password' : item.Type === 'int' ? 'number' : 'text'}
          value={value === undefined || value === null ? '' : String(value)}
          onChange={(event) =>
            onChange(item.Type === 'int' ? Number(event.target.value) : event.target.value)
          }
          placeholder={isSecret && item.HasValue ? t('SETTINGS_SECRET_KEEP_HINT') : undefined}
          className={cn(isSecret && 'font-mono')}
          disabled={disabled}
        />
      )}

      {item.Description ? (
        <p className="text-xs text-muted-foreground">{item.Description}</p>
      ) : null}
      {isSecret && item.HasValue ? (
        <p className="text-xs text-success">{t('SETTINGS_SECRET_SET')}</p>
      ) : null}
    </div>
  )
}
