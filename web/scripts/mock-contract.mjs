// 用 Vite 的 ssrLoadModule 加载 TS 模块（免去额外构建工具）
import { createServer } from 'vite'

const server = await createServer({
  root: process.cwd(),
  server: { middlewareMode: true },
  appType: 'custom',
  logLevel: 'error',
})

try {
  const { handleW8 } = await server.ssrLoadModule('/src/mocks/handlers-w8.ts')
  const { ApiCode } = await server.ssrLoadModule('/src/types/api.ts')

  let pass = 0
  let fail = 0
  const check = (name, cond, detail = '') => {
    if (cond) { pass++; console.log('  ✓', name) }
    else { fail++; console.log('  ✗', name, detail) }
  }
  const call = (method, path, params = {}, data = {}) => {
    const res = handleW8(method, path, { url: path, method, params, data })
    if (!res) throw new Error(`未匹配：${method} ${path}`)
    return res.data
  }

  console.log('--- 存储 §3 ---')
  {
    const r = call('get', '/storage/drivers')
    const drivers = r.Data.Drivers
    check('drivers 返回数组（≥3）', Array.isArray(drivers) && drivers.length >= 3)
    check('driver 含 Capabilities', typeof drivers[0].Capabilities?.SupportsPathTemplate === 'boolean')
    check('driver.Config 字段名保持驱动原样（Name/Type，不是 PascalCase 转换后的业务名）',
      drivers[0].Config.every((f) => typeof f.Name === 'string'))
  }
  {
    const r = call('get', '/storage/configs', { Page: 1, PageSize: 10 })
    const d = r.Data
    check('分页结构 {Items,Total,Page,PageSize}', ['Items','Total','Page','PageSize'].every((k) => k in d))
    check('StorageConfig 用 PascalCase（UID/IsDefault/PathTemplate）',
      ['UID','IsDefault','PathTemplate','PicgoConfigName'].every((k) => k in d.Items[0]))
    check('响应不含密钥（只有 HasSecrets/SecretFields）',
      !('Config' in d.Items[0]) && 'HasSecrets' in d.Items[0])
  }
  {
    const r = call('post', '/storage/configs', {}, { Name: '新配置', Type: 'smms', Config: { token: 'x' } })
    check('新建返回单个 StorageConfig', typeof r.Data.UID === 'string')
    const dup = call('post', '/storage/configs', {}, { Name: '新配置', Type: 'smms' })
    check('重名 → 40901', dup.Code === ApiCode.Conflict, JSON.stringify(dup))
  }
  {
    const r = call('patch', '/storage/configs/st_mock_github_work', {}, { Name: '改名', PicgoConfigName: 'HACK' })
    check('PATCH 忽略 PicgoConfigName（创建后只读，D64）',
      r.Data.PicgoConfigName === 'work' && r.Data.Name === '改名')
  }
  {
    const r = call('post', '/storage/configs/st_mock_webdav_nut/test')
    check('连通性测试返回 {Ok,Message,LatencyMs}', 'Ok' in r.Data && 'LatencyMs' in r.Data)
  }

  console.log('--- 图库 §4 ---')
  {
    const r = call('get', '/uploads', { Page: 1, PageSize: 5 })
    const d = r.Data
    check('分页返回 5 条', d.Items.length === 5)
    check('Upload 字段 PascalCase（Width/Height/Status/SHA256）',
      ['Width','Height','Status','SHA256','URL'].every((k) => k in d.Items[0]))
    check('含 Scope=all 时的上传者字段 UserEmail', 'UserEmail' in d.Items[0])
    check('URL 不为空（D84：直接引图床 URL）', d.Items[0].URL.startsWith('http'))
  }
  {
    const r = call('get', '/uploads/stats', { Scope: 'mine' })
    check('stats 含 ByStorage/ByExtension', Array.isArray(r.Data.ByStorage) && Array.isArray(r.Data.ByExtension))
  }
  {
    const r = call('get', '/uploads/up_mock_001/link', { Format: 'markdown' })
    check('link 返回 markdown 文本（D68）', r.Data.Format === 'markdown' && r.Data.Text.startsWith('!['))
    const html = call('get', '/uploads/up_mock_001/link', { Format: 'html' })
    check('link 支持 html', html.Data.Text.startsWith('<img'))
    const url = call('get', '/uploads/up_mock_001/link', { Format: 'url' })
    check('link 支持 url', url.Data.Text === url.Data.URL)
  }
  {
    const r = call('post', '/uploads/links', {}, { UIDs: ['up_mock_001','up_mock_002'], Format: 'url' })
    check('批量外链多行拼接（D68）', r.Data.Text.split('\n').length === 2)
  }
  {
    const r = call('delete', '/uploads/up_mock_057', { DeleteRemote: 'true' })
    check('删除返回 RemoteDeleteSupported（D47 如实告知）',
      r.Data.Deleted === true && typeof r.Data.RemoteDeleteSupported === 'boolean')
  }
  {
    const r = call('patch', '/uploads/up_mock_002', {}, { AliasName: '新名字' })
    check('PATCH 改名走 AliasName（不改远端文件名）', r.Data.AliasName === '新名字' && r.Data.FileName === 'sample-2.jpg')
  }

  console.log('--- 任务 §8 ---')
  {
    const r = call('get', '/jobs', { Page: 1, PageSize: 10 })
    const jobs = r.Data.Items
    check('Job 含四项计数', ['TotalItems','SucceededItems','FailedItems','SkippedItems'].every((k) => k in jobs[0]))
    check('Job 状态只有 4 个（无 partial，D37）',
      jobs.every((j) => ['queued','running','succeeded','failed'].includes(j.Status)))
    const failedJob = jobs.find((j) => j.Status === 'failed')
    check('失败的 job 仍回传成功项 URL（D37）',
      failedJob.Result.Items.some((i) => i.URL !== ''), JSON.stringify(failedJob.Result))
    check('失败的 job 同时带失败项与原因', failedJob.Result.Items.some((i) => i.Error))
  }
  {
    const r = call('get', '/jobs/job_mock_3/logs', { AfterSeq: 0, Limit: 500 })
    check('job logs 增量结构 {Items,HasMore,LastSeq}',
      ['Items','HasMore','LastSeq'].every((k) => k in r.Data) && r.Data.Items.length > 0)
    const after = call('get', '/jobs/job_mock_3/logs', { AfterSeq: 2, Limit: 500 })
    check('AfterSeq 增量生效', after.Data.Items.every((l) => l.Seq > 2))
  }
  {
    const r = call('delete', '/jobs/job_mock_3')
    check('运行中的任务不能清理 → 40901', r.Code === ApiCode.Conflict)
  }

  console.log('--- 操作日志 §9 ---')
  {
    const r = call('get', '/logs/types')
    check('类型是小写点分且原样（D81 例外）',
      r.Data.Types.some((t) => t.Type === 'upload') && r.Data.Types.some((t) => t.Type === 'theme.install'))
    check('类型只返回稳定值（不带本地化 Label）',
      r.Data.Types.every((t) => typeof t.Type === 'string' && !('Label' in t)))
  }
  {
    const r = call('get', '/logs', { Page: 1, PageSize: 5, Status: 'failed' })
    check('状态过滤生效', r.Data.Items.every((i) => i.Status === 'failed') && r.Data.Items.length > 0)
    check('失败日志带 Error 原因', r.Data.Items.every((i) => i.Error !== ''))
  }
  {
    const r = call('get', '/logs/emails', { Page: 1, PageSize: 5 })
    check('邮件日志不含正文（D29）', !('Body' in r.Data.Items[0]) && 'ToAddress' in r.Data.Items[0])
  }

  console.log('--- 插件 §6 ---')
  {
    const r = call('get', '/plugins')
    check('插件含 GuiOnly 布尔', typeof r.Data.Items[0].GuiOnly === 'boolean')
    check('存在 GuiOnly=true 的示例（用于走查置灰）', r.Data.Items.some((p) => p.GuiOnly === true))
    check('存在已禁用插件', r.Data.Items.some((p) => p.Enabled === false))
  }
  {
    const r = call('get', '/plugins/search', { Q: 'picgo-plugin-' })
    check('搜索结果含 Installed 标记', r.Data.Items.every((i) => typeof i.Installed === 'boolean'))
  }
  {
    const r = call('post', '/plugins/install', {}, { Names: ['picgo-plugin-webp'] })
    check('安装返回 JobUID（异步，API.md §6.1）', typeof r.Data.JobUID === 'string')
  }

  console.log('--- 主题 §10 ---')
  {
    const r = call('get', '/themes')
    check('主题列表含 Active/Pages/CanUninstall/Valid',
      ['Active'].every((k) => k in r.Data) && ['Pages','CanUninstall','Valid','IsBuiltin'].every((k) => k in r.Data.Items[0]))
    const broken = r.Data.Items.find((t) => t.Valid === false)
    check('损坏主题带 Error 原因（含保留路径说明）', broken && broken.Error.includes('保留路径'))
  }
  {
    const r = call('put', '/themes/active', {}, { ThemeID: 'broken' })
    check('启用非法主题 → 40001 且提示 /login 是保留路径', r.Code === ApiCode.InvalidParam && r.Message.includes('/login'))
    const okRes = call('put', '/themes/active', {}, { ThemeID: 'minimal' })
    check('启用合法主题成功并返回 Previous', okRes.Code === 0 && okRes.Data.Previous === 'default')
  }
  {
    const r = call('get', '/themes/default/settings')
    check('主题 schema 用主题命名（Key/Type）', 'Key' in r.Data.Schema[0] && 'Type' in r.Data.Schema[0])
    check('Values 带 Source 徽章（db/default）', r.Data.Values.BackgroundURL.Source === 'db')
    check('json 类型带 ItemSchema（repeater 走查用）', JSON.stringify(r.Data.Schema).includes('ItemSchema'))
    check('Schema 含 switch 类型（走查开关渲染）', r.Data.Schema.some((s) => s.Type === 'switch'))
  }
  {
    const r = call('delete', '/themes/default')
    check('卸载内置主题 → 40901', r.Code === ApiCode.Conflict)
  }

  console.log('--- 系统设置 §11 ---')
  {
    const r = call('get', '/settings/system')
    const cats = r.Data.Groups.map((g) => g.Category)
    const need = ['site','user','mail','oauth','security','log','upload','picgo','integration']
    check('含全部 9 个分类', need.every((c) => cats.includes(c)), JSON.stringify(cats))
    const keys = r.Data.Groups.flatMap((g) => g.Keys)
    const secret = keys.find((k) => k.Type === 'secret')
    check('secret 类型 Value 是掩码 ******', secret.Value === '******')
    check('secret 类型带 HasValue（不泄露值）', 'HasValue' in secret)
    check('每项都带 Source（徽章用）', keys.every((k) => k.Source === 'db' || k.Source === 'default'))
  }
  {
    const r = call('put', '/settings/system', {}, { 'site.name': '新名字' })
    check('保存返回 Applied 列表', Array.isArray(r.Data.Applied) && r.Data.Applied.includes('site.name'))
  }
  {
    const r = call('post', '/settings/mail/test', {}, { To: '' })
    check('缺收件地址 → 40001', r.Code === ApiCode.InvalidParam)
  }

  console.log()
  console.log(`结果：${pass} 通过 / ${fail} 失败`)
  await server.close()
  process.exit(fail > 0 ? 1 : 0)
} catch (err) {
  console.error('测试执行失败：', err)
  await server.close()
  process.exit(1)
}
