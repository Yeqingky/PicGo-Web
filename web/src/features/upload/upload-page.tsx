import { AlertCircle, ExternalLink, HardDrive } from 'lucide-react'
import { useEffect } from 'react'
import { Link } from 'react-router'

import { PageHeader } from '@/components/common/page-header'
import { GlobalUploadListener, UploadDropzone } from '@/components/upload/upload-dropzone'
import { UploadQueue } from '@/components/upload/upload-queue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { useStorageConfigs } from '@/hooks/api'
import { t } from '@/i18n'
import { flushUploadQueue, useUploadStore } from '@/store/upload-store'

/**
 * 上传页（DESIGN.md §5.1）。
 *
 * 三条关键语义：
 *  - **目标存储必须显式选择**（一批一个驱动，D38）；**切换只影响新加入队列的文件**，
 *    已在队列中的不变（避免把同一个 job 串到两个驱动上）
 *  - 上传是**异步**的：`POST /uploads` 立即返回 `JobUID`，进度来自 SSE
 *  - **上传中离开页面要提示**（`beforeunload` + 路由切换）
 */
export function UploadPage() {
  const {
    items: configs,
    loading: storageLoading,
    defaultConfig,
    error: storageError,
  } = useStorageConfigs()

  const items = useUploadStore((state) => state.items)
  const targetStorageUID = useUploadStore((state) => state.targetStorageUID)
  const setTargetStorage = useUploadStore((state) => state.setTargetStorage)
  const addFiles = useUploadStore((state) => state.addFiles)
  const removeItem = useUploadStore((state) => state.removeItem)
  const retryItem = useUploadStore((state) => state.retryItem)
  const retryAllFailed = useUploadStore((state) => state.retryAllFailed)
  const clearFinished = useUploadStore((state) => state.clearFinished)

  // 首次进入时把默认存储填上（用户仍可改；改了只影响后续新加入的文件）
  useEffect(() => {
    if (!targetStorageUID && defaultConfig) {
      setTargetStorage(defaultConfig.UID)
    }
  }, [targetStorageUID, defaultConfig, setTargetStorage])

  const pending = items.some((item) => item.Status === 'waiting' || item.Status === 'uploading')

  // 上传中离开页面提示（DESIGN.md §5.1）。任务在后端继续，但用户需要知道这一点
  useEffect(() => {
    if (!pending) return

    const handler = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      // 现代浏览器忽略自定义文案，但必须设置 returnValue 才会弹确认
      event.returnValue = ''
    }

    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  }, [pending])

  const storageOptions = configs.filter((config) => config.Enabled)
  const hasStorage = storageOptions.length > 0

  return (
    <>
      {/* 整页拖拽 + Ctrl+V 粘贴上传（DESIGN.md §9.2） */}
      <GlobalUploadListener onFiles={addFiles} enabled={hasStorage} />

      <PageHeader
        title={t('UPLOAD_TITLE')}
        description={t('UPLOAD_DESC')}
        actions={
          <Button asChild variant="outline" size="sm">
            <Link to="/admin/storage">
              <HardDrive aria-hidden />
              {t('UPLOAD_MANAGE_STORAGE')}
            </Link>
          </Button>
        }
      />

      <div className="space-y-5">
        {/* 没有可用存储时的引导（而不是让用户对着报错猜） */}
        {storageLoading ? (
          <Skeleton className="h-40 w-full" />
        ) : !hasStorage ? (
          <Alert variant="warning">
            <AlertCircle aria-hidden />
            <AlertDescription className="space-y-2">
              <p>{storageError ? t('UPLOAD_STORAGE_LOAD_FAILED') : t('UPLOAD_NO_STORAGE')}</p>
              <p className="text-xs">
                {t('UPLOAD_NO_STORAGE_HINT')}{' '}
                <Link to="/admin/storage" className="text-brand underline">
                  {t('UPLOAD_GO_STORAGE')}
                </Link>
              </p>
            </AlertDescription>
          </Alert>
        ) : (
          <UploadDropzone onFiles={addFiles} />
        )}

        {/* 目标存储 */}
        <div className="max-w-md">
          <div className="space-y-1.5">
            <Label htmlFor="target-storage">{t('UPLOAD_TARGET_STORAGE')}</Label>
            <Select
              value={targetStorageUID}
              onValueChange={(value) => {
                setTargetStorage(value)
                // 切换后把「等待中且尚未分配 job」的文件交给新驱动
                flushUploadQueue()
              }}
              disabled={!hasStorage}
            >
              <SelectTrigger id="target-storage">
                <SelectValue placeholder={t('UPLOAD_SELECT_STORAGE')} />
              </SelectTrigger>
              <SelectContent>
                {storageOptions.map((config) => (
                  <SelectItem key={config.UID} value={config.UID}>
                    {config.Name}
                    {config.IsDefault ? ` · ${t('STORAGE_DEFAULT_BADGE')}` : ''}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-xs text-muted-foreground">{t('UPLOAD_STORAGE_SWITCH_HINT')}</p>
          </div>
        </div>

        {/* 队列 */}
        <UploadQueue
          items={items}
          onRetry={retryItem}
          onRemove={removeItem}
          onRetryAll={retryAllFailed}
          onClearFinished={clearFinished}
        />

        {pending ? (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <ExternalLink className="size-4" aria-hidden />
            {t('UPLOAD_IN_PROGRESS_HINT')}{' '}
            <Link to="/jobs" className="text-brand underline">
              {t('NAV_JOBS')}
            </Link>
          </p>
        ) : null}
      </div>
    </>
  )
}
