import { AlertTriangle, Loader2, Puzzle, RefreshCw, Search, Trash2, Upload } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'

import { PageHeader } from '@/components/common/page-header'
import { JobLogDrawer } from '@/components/jobs/job-log-drawer'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { EmptyState } from '@/components/ui/empty-state'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { toast } from '@/components/ui/toast'
import { usePluginSearch, usePlugins } from '@/hooks/api'
import { t } from '@/i18n'
import { pluginApi } from '@/lib/api'
import { toApiError, type Plugin, type PluginSearchItem } from '@/types/api'
import { cn } from '@/lib/utils'

/**
 * 插件（DESIGN.md §5.4，admin）。
 *
 * 三条必须实现的语义（API.md §6.1）：
 *  1. **`GuiOnly=true` 的插件含 Electron 专属能力**（`guiMenu` / `commands`）→
 *     Web 端无法执行，**置灰并提示「该能力仅在桌面端可用」**
 *  2. 安装 / 卸载 / 更新**一律异步** → 拿到 `JobUID` 后打开**任务日志抽屉**看 npm 输出
 *  3. **任务成功后 agent 会重启自身**（低频操作换取状态绝对干净）——
 *     期间上传类接口短暂 `503 / 50002`，前端展示「内核正在重启」并轮询 `/healthz`
 */
export function AdminPluginsPage() {
  const [tab, setTab] = useState('installed')
  const [openJob, setOpenJob] = useState('')
  const [kernelRestarting, setKernelRestarting] = useState(false)

  const { plugins, loading, error, refresh } = usePlugins()
  const { keyword, setKeyword, results, loading: searching, error: searchError, searched, search } =
    usePluginSearch()

  // 「内核重启中」时轮询 /healthz，恢复后自动收起提示（DESIGN §5.4）
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)

  useEffect(() => {
    if (!kernelRestarting) {
      if (pollRef.current) {
        clearInterval(pollRef.current)
        pollRef.current = null
      }
      return
    }

    pollRef.current = setInterval(() => {
      void fetch('/healthz')
        .then((res) => res.json())
        .then((data: { agent?: string }) => {
          if (data.agent === 'up') {
            setKernelRestarting(false)
            refresh()
            toast.success(t('PLUGIN_KERNEL_READY'))
          }
        })
        .catch(() => {
          // 服务本身不可达时继续等（可能也在重启）
        })
    }, 3000)

    return () => {
      if (pollRef.current) clearInterval(pollRef.current)
      pollRef.current = null
    }
  }, [kernelRestarting, refresh])

  /** 统一的异步插件操作：拿 job → 开抽屉 → 标记内核将重启。 */
  const runJob = async (action: () => Promise<{ JobUID: string }>, successText: string) => {
    try {
      const res = await action()
      setOpenJob(res.JobUID)
      toast.success(successText)
      // 安装 / 卸载成功后会重启 agent；更新不强制重启，但保持一致行为也无害
      setKernelRestarting(true)
    } catch (err) {
      toast.error(toApiError(err).message)
    }
  }

  const toggleEnabled = async (plugin: Plugin, enabled: boolean) => {
    try {
      await pluginApi.setEnabled(plugin.Name, enabled)
      refresh()
    } catch (err) {
      toast.error(toApiError(err).message)
    }
  }

  return (
    <>
      <PageHeader
        title={t('NAV_ADMIN_PLUGINS')}
        description={t('PLUGIN_DESC')}
        actions={
          <IconButton label={t('COMMON_REFRESH')} onClick={refresh}>
            <RefreshCw aria-hidden />
          </IconButton>
        }
      />

      {/* 内核重启提示：这是**预期行为**，不是错误（API.md §6.1） */}
      {kernelRestarting ? (
        <Alert variant="warning" className="mb-4">
          <Loader2 className="animate-spin" aria-hidden />
          <AlertDescription>{t('PLUGIN_KERNEL_RESTARTING')}</AlertDescription>
        </Alert>
      ) : null}

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList className="mb-4">
          <TabsTrigger value="installed">{t('PLUGIN_TAB_INSTALLED')}</TabsTrigger>
          <TabsTrigger value="browse">{t('PLUGIN_TAB_BROWSE')}</TabsTrigger>
        </TabsList>

        {/* 已安装 */}
        <TabsContent value="installed">
          {loading && plugins.length === 0 ? (
            <div className="space-y-3">
              {Array.from({ length: 3 }, (_, index) => (
                <Skeleton key={index} className="h-24 w-full" />
              ))}
            </div>
          ) : error ? (
            <EmptyState
              title={t('PLUGIN_LOAD_FAILED')}
              description={toApiError(error).message}
              action={
                <Button variant="outline" onClick={refresh}>
                  {t('COMMON_RETRY')}
                </Button>
              }
            />
          ) : plugins.length === 0 ? (
            <EmptyState
              icon={Puzzle}
              title={t('PLUGIN_EMPTY')}
              description={t('PLUGIN_EMPTY_DESC')}
              action={
                <Button variant="brand" onClick={() => setTab('browse')}>
                  {t('PLUGIN_TAB_BROWSE')}
                </Button>
              }
            />
          ) : (
            <div className="space-y-3">
              {plugins.map((plugin) => (
                <PluginRow
                  key={plugin.Name}
                  plugin={plugin}
                  onToggle={(enabled) => void toggleEnabled(plugin, enabled)}
                  onUpdate={() =>
                    void runJob(
                      () => pluginApi.update([plugin.Name]),
                      t('PLUGIN_UPDATE_STARTED', { name: plugin.Name }),
                    )
                  }
                  onUninstall={() =>
                    void runJob(
                      () => pluginApi.uninstall([plugin.Name]),
                      t('PLUGIN_UNINSTALL_STARTED', { name: plugin.Name }),
                    )
                  }
                />
              ))}

              <div className="pt-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() =>
                    void runJob(() => pluginApi.update([]), t('PLUGIN_UPDATE_ALL_STARTED'))
                  }
                  disabled={kernelRestarting}
                >
                  <RefreshCw aria-hidden />
                  {t('PLUGIN_UPDATE_ALL')}
                </Button>
              </div>
            </div>
          )}
        </TabsContent>

        {/* 浏览（npm 搜索） */}
        <TabsContent value="browse">
          <div className="mb-4 flex flex-wrap items-end gap-2">
            <div className="min-w-[16rem] flex-1 space-y-1">
              <label htmlFor="plugin-search" className="text-xs text-muted-foreground">
                {t('PLUGIN_SEARCH_LABEL')}
              </label>
              <Input
                id="plugin-search"
                value={keyword}
                onChange={(event) => setKeyword(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') void search()
                }}
                placeholder="picgo-plugin-"
              />
            </div>
            <Button variant="brand" onClick={() => void search()} disabled={searching}>
              {searching ? <Loader2 className="size-4 animate-spin" aria-hidden /> : <Search aria-hidden />}
              {t('COMMON_SEARCH')}
            </Button>
          </div>

          {searchError ? (
            <Alert variant="destructive">
              <AlertDescription>{searchError}</AlertDescription>
            </Alert>
          ) : null}

          {searching ? (
            <div className="space-y-3">
              {Array.from({ length: 3 }, (_, index) => (
                <Skeleton key={index} className="h-20 w-full" />
              ))}
            </div>
          ) : results.length > 0 ? (
            <div className="space-y-3">
              {results.map((item) => (
                <SearchResultRow
                  key={item.Name}
                  item={item}
                  disabled={kernelRestarting}
                  onInstall={() =>
                    void runJob(
                      () => pluginApi.install([item.Name]),
                      t('PLUGIN_INSTALL_STARTED', { name: item.Name }),
                    )
                  }
                />
              ))}
            </div>
          ) : searched ? (
            <EmptyState title={t('PLUGIN_SEARCH_EMPTY')} description={t('PLUGIN_SEARCH_EMPTY_DESC')} />
          ) : (
            <EmptyState
              icon={Search}
              title={t('PLUGIN_SEARCH_HINT_TITLE')}
              description={t('PLUGIN_SEARCH_HINT_DESC')}
            />
          )}
        </TabsContent>
      </Tabs>

      <JobLogDrawer open={openJob !== ''} onOpenChange={(open) => !open && setOpenJob('')} jobUID={openJob} />
    </>
  )
}

// ---------------------------------------------------------------------------

function PluginRow({
  plugin,
  onToggle,
  onUpdate,
  onUninstall,
}: {
  plugin: Plugin
  onToggle: (enabled: boolean) => void
  onUpdate: () => void
  onUninstall: () => void
}) {
  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between gap-3 space-y-0">
        <div className="min-w-0 space-y-1">
          <CardTitle className="flex flex-wrap items-center gap-2 text-base">
            <span className="truncate font-mono text-sm">{plugin.Name}</span>
            <Badge variant="secondary">v{plugin.Version || '—'}</Badge>
            {/* `GuiOnly`：Electron 专属能力，Web 端无法执行 */}
            {plugin.GuiOnly ? (
              <Badge variant="warning" className="gap-1">
                <AlertTriangle className="size-3" aria-hidden />
                {t('PLUGIN_GUI_ONLY_BADGE')}
              </Badge>
            ) : null}
            {plugin.Uploader ? <Badge variant="info">{plugin.Uploader}</Badge> : null}
          </CardTitle>

          {plugin.Description ? (
            <p className="line-clamp-2 text-xs text-muted-foreground">{plugin.Description}</p>
          ) : null}

          <p className="text-xs text-muted-foreground">
            {plugin.Author || '—'}
            {plugin.Homepage ? (
              <>
                {' · '}
                <a
                  href={plugin.Homepage}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="text-brand underline"
                >
                  {t('PLUGIN_HOMEPAGE')}
                </a>
              </>
            ) : null}
          </p>

          {plugin.GuiOnly ? (
            <p className="text-xs text-warning">{t('PLUGIN_GUI_ONLY_HINT')}</p>
          ) : null}
        </div>

        <div className="flex shrink-0 items-center gap-2">
          <Switch
            checked={plugin.Enabled}
            onCheckedChange={onToggle}
            aria-label={t('PLUGIN_ENABLED_TOGGLE')}
          />
        </div>
      </CardHeader>

      <CardContent className="pt-0">
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" onClick={onUpdate}>
            <RefreshCw aria-hidden />
            {t('PLUGIN_UPDATE')}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className={cn('text-destructive hover:text-destructive')}
            onClick={onUninstall}
          >
            <Trash2 aria-hidden />
            {t('PLUGIN_UNINSTALL')}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}

function SearchResultRow({
  item,
  disabled,
  onInstall,
}: {
  item: PluginSearchItem
  disabled: boolean
  onInstall: () => void
}) {
  return (
    <Card>
      <CardContent className="flex items-start justify-between gap-3 p-4">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="truncate font-mono text-sm text-foreground">{item.Name}</span>
            <Badge variant="secondary">v{item.Version}</Badge>
          </div>
          {item.Description ? (
            <p className="line-clamp-2 text-xs text-muted-foreground">{item.Description}</p>
          ) : null}
          <p className="text-xs text-muted-foreground">{item.Author || '—'}</p>
        </div>

        <Button
          variant={item.Installed ? 'outline' : 'brand'}
          size="sm"
          disabled={item.Installed || disabled}
          onClick={onInstall}
          className="shrink-0"
        >
          <Upload aria-hidden />
          {item.Installed ? t('PLUGIN_INSTALLED') : t('PLUGIN_INSTALL')}
        </Button>
      </CardContent>
    </Card>
  )
}
