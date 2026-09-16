import { RotateCw, ScrollText } from 'lucide-react'
import { useState } from 'react'

import { JsonView } from '@/components/common/json-view'
import { StatusDot } from '@/components/common/status-badge'
import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/ui/empty-state'
import { IconButton } from '@/components/ui/icon-button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Pagination } from '@/components/ui/pagination'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useEmailLogs, useLogRetention, useLogTypes, useOperationLogs } from '@/hooks/api'
import { t } from '@/i18n'
import { logTypeLabel } from '@/i18n/log-types'
import { formatDateTime } from '@/lib/format'
import { ApiCode, toApiError, type EmailLog, type LogListQuery, type OperationLog } from '@/types/api'
import { cn } from '@/lib/utils'

/**
 * 操作日志表（DESIGN.md §5.6）。
 *
 * 仅供 `/admin/logs` 使用：管理员查看全站操作日志与邮件日志。
 *
 * ⚠️ **可见范围完全由后端决定**：管理员不传 `UserUID` 就会拿到全站数据，
 *    普通用户则只会拿到自己的。前端**不传范围参数**，也不假设权限 ——
 *    拿到 `40301` 就展示一句权限提示，而不是报「加载失败」。
 */
export function OperationLogsPanel() {
  const { types } = useLogTypes()
  const { days } = useLogRetention()

  const [type, setType] = useState('')
  const [status, setStatus] = useState('')
  const [keywordInput, setKeywordInput] = useState('')
  const [keyword, setKeyword] = useState('')
  const [detail, setDetail] = useState<OperationLog | null>(null)

  const query: LogListQuery = {
    Type: type || undefined,
    Status: (status || undefined) as never,
    Keyword: keyword || undefined,
    Sort: 'CreatedAt',
    Order: 'desc',
  }

  const { items, total, page, pageSize, loading, error, setPage, refresh } = useOperationLogs(query)

  const forbidden = error instanceof Error && 'Code' in error && error.Code === ApiCode.Forbidden

  return (
    <div className="space-y-4">
      {/* 保留期提示（D74） */}
      {days !== undefined ? (
        <p className="text-xs text-muted-foreground">
          {days === 0 ? t('LOG_RETENTION_FOREVER') : t('LOG_RETENTION_DAYS', { days })}
        </p>
      ) : null}

      {/* 筛选 */}
      <div className="flex flex-wrap items-end gap-3">
        <div className="space-y-1">
          <Label className="text-xs text-muted-foreground">{t('LOG_FILTER_TYPE')}</Label>
          <Select
            value={type || '__all__'}
            onValueChange={(value) => {
              setType(value === '__all__' ? '' : value)
              setPage(1)
            }}
          >
            <SelectTrigger className="min-w-[11rem]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__all__">{t('GALLERY_FILTER_ALL')}</SelectItem>
              {types.map((item) => (
                <SelectItem key={item.Type} value={item.Type}>
                  {logTypeLabel(item.Type)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="space-y-1">
          <Label className="text-xs text-muted-foreground">{t('GALLERY_FILTER_STATUS')}</Label>
          <Select
            value={status || '__all__'}
            onValueChange={(value) => {
              setStatus(value === '__all__' ? '' : value)
              setPage(1)
            }}
          >
            <SelectTrigger className="min-w-[8rem]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__all__">{t('GALLERY_FILTER_ALL')}</SelectItem>
              <SelectItem value="success">{t('STATUS_SUCCESS')}</SelectItem>
              <SelectItem value="failed">{t('STATUS_FAILED')}</SelectItem>
            </SelectContent>
          </Select>
        </div>

        <div className="min-w-[12rem] flex-1 space-y-1">
          <Label className="text-xs text-muted-foreground">{t('LOG_FILTER_KEYWORD')}</Label>
          <Input
            value={keywordInput}
            onChange={(event) => setKeywordInput(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                setKeyword(keywordInput.trim())
                setPage(1)
              }
            }}
            placeholder={t('LOG_FILTER_KEYWORD_PLACEHOLDER')}
          />
        </div>

        <Button
          variant="outline"
          onClick={() => {
            setKeyword(keywordInput.trim())
            setPage(1)
          }}
        >
          {t('COMMON_SEARCH')}
        </Button>

        <IconButton label={t('COMMON_REFRESH')} onClick={refresh}>
          <RotateCw aria-hidden />
        </IconButton>
      </div>

      {/* 表格 */}
      {loading && items.length === 0 ? (
        <div className="space-y-2">
          {Array.from({ length: 6 }, (_, index) => (
            <Skeleton key={index} className="h-11 w-full" />
          ))}
        </div>
      ) : forbidden ? (
        <EmptyState title={t('LOG_FORBIDDEN')} description={t('LOG_FORBIDDEN_DESC')} />
      ) : error ? (
        <EmptyState
          title={t('LOG_LOAD_FAILED')}
          description={toApiError(error).message}
          action={
            <Button variant="outline" onClick={refresh}>
              {t('COMMON_RETRY')}
            </Button>
          }
        />
      ) : items.length === 0 ? (
        <EmptyState icon={ScrollText} title={t('LOG_EMPTY')} description={t('LOG_EMPTY_DESC')} />
      ) : (
        <>
          <div className="overflow-x-auto rounded-lg border border-border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-8" />
                  <TableHead>{t('LOG_COL_TIME')}</TableHead>
                  <TableHead>{t('LOG_COL_TYPE')}</TableHead>
                  <TableHead>{t('LOG_COL_STATUS')}</TableHead>
                  <TableHead>{t('LOG_COL_OPERATOR')}</TableHead>
                  <TableHead>{t('LOG_COL_TARGET')}</TableHead>
                  <TableHead>{t('LOG_COL_SUMMARY')}</TableHead>
                </TableRow>
              </TableHeader>

              <TableBody>
                {items.map((log) => {
                  const label = logTypeLabel(log.Type)
                  return (
                    <TableRow
                      key={log.UID}
                      className="cursor-pointer"
                      onClick={() => setDetail(log)}
                    >
                      <TableCell>
                        <StatusDot status={log.Status} />
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                        {formatDateTime(log.CreatedAt)}
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-sm">{label}</TableCell>
                      <TableCell>
                        <span
                          className={cn(
                            'text-xs',
                            log.Status === 'failed' ? 'text-destructive' : 'text-success',
                          )}
                        >
                          {log.Status === 'failed' ? t('STATUS_FAILED') : t('STATUS_SUCCESS')}
                        </span>
                      </TableCell>
                      <TableCell className="truncate text-xs text-muted-foreground">
                        {log.Username || t('LOG_SYSTEM')}
                      </TableCell>
                      <TableCell className="max-w-[14rem] truncate font-mono text-xs text-muted-foreground">
                        {log.TargetUID || '—'}
                      </TableCell>
                      <TableCell className="max-w-[18rem] truncate text-xs">
                        {log.Error ? (
                          <span className="text-destructive">{log.Error}</span>
                        ) : (
                          <span className="text-muted-foreground">{t('LOG_CLICK_DETAIL')}</span>
                        )}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>

          <Pagination page={page} pageSize={pageSize} total={total} onPageChange={setPage} />
        </>
      )}

      {/* 详情抽屉（完整 JSON，DESIGN.md §5.6） */}
      <Sheet open={detail !== null} onOpenChange={(open) => !open && setDetail(null)}>
        <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-xl">
          <SheetHeader className="border-b border-border px-5 py-4">
            <SheetTitle>{t('LOG_DETAIL_TITLE')}</SheetTitle>
            <SheetDescription className="font-mono text-xs">{detail?.UID}</SheetDescription>
          </SheetHeader>

          {detail ? (
            <div className="flex-1 space-y-4 overflow-y-auto px-5 py-4 scrollbar-thin">
              <dl className="space-y-2 text-sm">
                <DetailRow label={t('LOG_COL_TYPE')}>{logTypeLabel(detail.Type)}</DetailRow>
                <DetailRow label={t('LOG_COL_STATUS')}>{detail.Status}</DetailRow>
                <DetailRow label={t('LOG_COL_OPERATOR')}>
                  {detail.Username || t('LOG_SYSTEM')}
                </DetailRow>
                <DetailRow label={t('LOG_COL_TARGET')}>
                  <span className="font-mono text-xs">
                    {detail.TargetType || '—'} / {detail.TargetUID || '—'}
                  </span>
                </DetailRow>
                <DetailRow label={t('LOG_COL_TIME')}>{formatDateTime(detail.CreatedAt)}</DetailRow>
                <DetailRow label={t('LOG_FIELD_IP')}>{detail.ClientIP || '—'}</DetailRow>
                <DetailRow label={t('LOG_FIELD_UA')}>{detail.UserAgent || '—'}</DetailRow>
              </dl>

              {detail.Error ? (
                <div className="space-y-1.5">
                  <Label className="text-xs text-destructive">{t('LOG_FIELD_ERROR')}</Label>
                  <p className="whitespace-pre-wrap rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
                    {detail.Error}
                  </p>
                </div>
              ) : null}

              <div className="space-y-1.5">
                <Label className="text-xs text-muted-foreground">{t('LOG_FIELD_DETAIL')}</Label>
                <JsonView value={detail.Detail} defaultOpen />
              </div>
            </div>
          ) : null}
        </SheetContent>
      </Sheet>
    </div>
  )
}

function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4">
      <dt className="shrink-0 text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-all text-right text-foreground">{children}</dd>
    </div>
  )
}

/**
 * 邮件日志面板（DESIGN.md §5.6，**仅管理员**）。
 *
 * ⚠️ **不显示邮件正文**（D29：后端本来也没存）。只有「发给谁 / 主题 / 模板 / 结果」。
 */
export function EmailLogsPanel() {
  const [to, setTo] = useState('')
  const [status, setStatus] = useState('')
  const [detail, setDetail] = useState<EmailLog | null>(null)

  const { items, total, page, pageSize, loading, error, setPage, refresh } = useEmailLogs({
    ToAddress: to || undefined,
    Status: (status || undefined) as never,
  })

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-[14rem] flex-1 space-y-1">
          <Label className="text-xs text-muted-foreground">{t('MAILLOG_FILTER_TO')}</Label>
          <Input
            value={to}
            onChange={(event) => setTo(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                setPage(1)
                refresh()
              }
            }}
            placeholder="name@example.com"
          />
        </div>

        <div className="space-y-1">
          <Label className="text-xs text-muted-foreground">{t('GALLERY_FILTER_STATUS')}</Label>
          <Select
            value={status || '__all__'}
            onValueChange={(value) => {
              setStatus(value === '__all__' ? '' : value)
              setPage(1)
            }}
          >
            <SelectTrigger className="min-w-[8rem]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__all__">{t('GALLERY_FILTER_ALL')}</SelectItem>
              <SelectItem value="success">{t('STATUS_SUCCESS')}</SelectItem>
              <SelectItem value="failed">{t('STATUS_FAILED')}</SelectItem>
            </SelectContent>
          </Select>
        </div>

        <Button variant="outline" onClick={refresh}>
          {t('COMMON_REFRESH')}
        </Button>
      </div>

      {loading && items.length === 0 ? (
        <div className="space-y-2">
          {Array.from({ length: 5 }, (_, index) => (
            <Skeleton key={index} className="h-11 w-full" />
          ))}
        </div>
      ) : error ? (
        <EmptyState
          title={t('MAILLOG_LOAD_FAILED')}
          description={toApiError(error).message}
          action={
            <Button variant="outline" onClick={refresh}>
              {t('COMMON_RETRY')}
            </Button>
          }
        />
      ) : items.length === 0 ? (
        <EmptyState title={t('MAILLOG_EMPTY')} description={t('MAILLOG_EMPTY_DESC')} />
      ) : (
        <>
          <p className="text-xs text-muted-foreground">{t('MAILLOG_NO_BODY_NOTE')}</p>

          <div className="overflow-x-auto rounded-lg border border-border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-8" />
                  <TableHead>{t('LOG_COL_TIME')}</TableHead>
                  <TableHead>{t('MAILLOG_COL_TO')}</TableHead>
                  <TableHead>{t('MAILLOG_COL_SUBJECT')}</TableHead>
                  <TableHead>{t('MAILLOG_COL_TEMPLATE')}</TableHead>
                  <TableHead>{t('LOG_COL_STATUS')}</TableHead>
                </TableRow>
              </TableHeader>

              <TableBody>
                {items.map((log) => (
                  <TableRow key={log.UID} className="cursor-pointer" onClick={() => setDetail(log)}>
                    <TableCell>
                      <StatusDot status={log.Status} />
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-muted-foreground">
                      {formatDateTime(log.CreatedAt)}
                    </TableCell>
                    <TableCell className="text-sm">{log.ToAddress}</TableCell>
                    <TableCell className="max-w-[16rem] truncate text-sm">{log.Subject || '—'}</TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground">
                      {log.Template || '—'}
                    </TableCell>
                    <TableCell>
                      <span
                        className={cn(
                          'text-xs',
                          log.Status === 'failed' ? 'text-destructive' : 'text-success',
                        )}
                      >
                        {log.Status === 'failed' ? t('STATUS_FAILED') : t('STATUS_SUCCESS')}
                      </span>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <Pagination page={page} pageSize={pageSize} total={total} onPageChange={setPage} />
        </>
      )}

      <Sheet open={detail !== null} onOpenChange={(open) => !open && setDetail(null)}>
        <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-lg">
          <SheetHeader className="border-b border-border px-5 py-4">
            <SheetTitle>{t('MAILLOG_DETAIL_TITLE')}</SheetTitle>
            <SheetDescription className="font-mono text-xs">{detail?.UID}</SheetDescription>
          </SheetHeader>

          {detail ? (
            <dl className="space-y-2 px-5 py-4 text-sm">
              <DetailRow label={t('MAILLOG_COL_TO')}>{detail.ToAddress}</DetailRow>
              <DetailRow label={t('MAILLOG_COL_SUBJECT')}>{detail.Subject || '—'}</DetailRow>
              <DetailRow label={t('MAILLOG_COL_TEMPLATE')}>{detail.Template || '—'}</DetailRow>
              <DetailRow label={t('LOG_COL_STATUS')}>{detail.Status}</DetailRow>
              <DetailRow label={t('LOG_COL_TIME')}>{formatDateTime(detail.CreatedAt)}</DetailRow>
              <DetailRow label={t('MAILLOG_COL_RELATED_USER')}>{detail.RelatedUserUID || '—'}</DetailRow>
              {detail.Error ? (
                <DetailRow label={t('LOG_FIELD_ERROR')}>
                  <span className="text-destructive">{detail.Error}</span>
                </DetailRow>
              ) : null}
            </dl>
          ) : null}
        </SheetContent>
      </Sheet>
    </div>
  )
}
