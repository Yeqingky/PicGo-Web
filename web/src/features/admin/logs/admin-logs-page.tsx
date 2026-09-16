import { useState } from 'react'

import { PageHeader } from '@/components/common/page-header'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { EmailLogsPanel, OperationLogsPanel } from '@/features/logs/operation-logs-panel'
import { t } from '@/i18n'

/**
 * 全部日志（DESIGN.md §3.3 `/admin/logs`，admin）。
 *
 * 这里展示**全站**操作日志, 并提供一个 **「邮件日志」Tab**（`EmailLogs`, **不显示正文**, D29）。
 */
export function AdminLogsPage() {
  const [tab, setTab] = useState('operations')

  return (
    <>
      <PageHeader title={t('NAV_ADMIN_LOGS')} description={t('ADMIN_LOG_DESC')} />

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList className="mb-4">
          <TabsTrigger value="operations">{t('ADMIN_LOG_TAB_OPERATIONS')}</TabsTrigger>
          <TabsTrigger value="emails">{t('ADMIN_LOG_TAB_EMAILS')}</TabsTrigger>
        </TabsList>

        <TabsContent value="operations">
          <OperationLogsPanel />
        </TabsContent>

        <TabsContent value="emails">
          <EmailLogsPanel />
        </TabsContent>
      </Tabs>
    </>
  )
}
