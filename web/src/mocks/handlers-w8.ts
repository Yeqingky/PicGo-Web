import type { AxiosRequestConfig, AxiosResponse, InternalAxiosRequestConfig } from 'axios'

import type { Envelope } from '@/types/api'
import { ApiCode } from '@/types/api'

/**
 * W8 各域的 mock handler（`VITE_USE_MOCK=true` 时生效）。
 *
 * 目的：后端（W5/W6/W9/W10）尚未全部就绪时，前端页面仍能独立开发与走查。
 *
 * ⚠️ 字段名与信封**严格按 `docs/API.md`**（PascalCase），
 *    这样切到真后端时前端**零改动**。
 */

// ---------------------------------------------------------------------------
// 工具
// ---------------------------------------------------------------------------

function now(): number {
  return Math.floor(Date.now() / 1000)
}

function respond<T>(config: AxiosRequestConfig, body: Envelope<T>, status = 200): AxiosResponse<Envelope<T>> {
  return {
    data: body,
    status,
    statusText: String(status),
    headers: {},
    config: config as InternalAxiosRequestConfig,
  }
}

function ok<T>(config: AxiosRequestConfig, data: T): AxiosResponse<Envelope<T>> {
  return respond(config, { Code: ApiCode.OK, Message: 'ok', Data: data })
}

function fail(config: AxiosRequestConfig, code: number, message: string, status = 400) {
  return respond(config, { Code: code, Message: message, Data: null as never }, status)
}

/** 从 path 里抽取 `{uid}` 这类路径参数。 */
function match(path: string, pattern: string): Record<string, string> | null {
  const p = pattern.split('/')
  const s = path.split('/')
  if (p.length !== s.length) return null

  const out: Record<string, string> = {}
  for (let i = 0; i < p.length; i += 1) {
    const seg = p[i]
    if (seg.startsWith('{') && seg.endsWith('}')) {
      out[seg.slice(1, -1)] = decodeURIComponent(s[i])
    } else if (seg !== s[i]) {
      return null
    }
  }
  return out
}

// ---------------------------------------------------------------------------
// 假数据
// ---------------------------------------------------------------------------

const DRIVERS = [
  {
    Type: 'github',
    Name: 'GitHub',
    Builtin: true,
    GuiOnly: false,
    ConfigCount: 2,
    Config: [
      { Name: 'repo', Type: 'input', Required: true, Alias: '仓库名', Message: 'username/repo', Default: '' },
      { Name: 'branch', Type: 'input', Required: true, Alias: '分支', Default: 'main' },
      { Name: 'token', Type: 'password', Required: true, Alias: 'Token', Default: '' },
      { Name: 'path', Type: 'input', Required: false, Alias: '存储路径', Default: '' },
    ],
    Capabilities: {
      SupportsPathTemplate: true,
      SupportsRemoteDelete: true,
      ConfigFields: ['repo', 'branch', 'token', 'path'],
      PathFieldNames: ['path'],
      DetectedAt: now(),
      PicgoVersion: '3.0.2',
    },
  },
  {
    Type: 'webdav',
    Name: 'WebDAV',
    Builtin: true,
    GuiOnly: false,
    ConfigCount: 1,
    Config: [
      { Name: 'url', Type: 'input', Required: true, Alias: '服务地址', Default: '' },
      { Name: 'username', Type: 'input', Required: true, Alias: '用户名', Default: '' },
      { Name: 'password', Type: 'password', Required: true, Alias: '密码', Default: '' },
      { Name: 'path', Type: 'input', Required: false, Alias: '存储路径', Default: '' },
    ],
    Capabilities: {
      SupportsPathTemplate: true,
      SupportsRemoteDelete: false,
      ConfigFields: ['url', 'username', 'password', 'path'],
      PathFieldNames: ['path'],
      DetectedAt: now(),
      PicgoVersion: '3.0.2',
    },
  },
  {
    Type: 'smms',
    Name: 'SM.MS',
    Builtin: true,
    GuiOnly: false,
    ConfigCount: 0,
    Config: [
      { Name: 'token', Type: 'password', Required: true, Alias: 'API Token', Default: '' },
      {
        Name: 'region',
        Type: 'list',
        Required: false,
        Alias: '区域',
        Default: 'cn',
        Choices: ['cn', 'global'],
      },
    ],
    Capabilities: {
      SupportsPathTemplate: false,
      SupportsRemoteDelete: false,
      ConfigFields: ['token', 'region'],
      PathFieldNames: [],
      DetectedAt: now(),
      PicgoVersion: '3.0.2',
    },
  },
]

let storageConfigs = [
  {
    UID: 'st_mock_github_work',
    Name: 'GitHub · work',
    Type: 'github',
    PicgoConfigName: 'work',
    Enabled: true,
    IsDefault: true,
    PathTemplate: '{Y}/{m}/{d}',
    FileTemplate: '{md5-8}{extname}',
    Capabilities: DRIVERS[0].Capabilities,
    HasSecrets: true,
    SecretFields: ['repo', 'branch', 'token'],
    Metadata: {},
    UploadCount: 42,
    CreatedAt: now() - 86400 * 10,
    UpdatedAt: now() - 3600,
  },
  {
    UID: 'st_mock_webdav_nut',
    Name: '坚果云 WebDAV',
    Type: 'webdav',
    PicgoConfigName: 'Default',
    Enabled: true,
    IsDefault: false,
    PathTemplate: 'photos/{Y}/{m}',
    FileTemplate: '{uniqid}{extname}',
    Capabilities: DRIVERS[1].Capabilities,
    HasSecrets: true,
    SecretFields: ['url', 'username', 'password'],
    Metadata: {},
    UploadCount: 12,
    CreatedAt: now() - 86400 * 3,
    UpdatedAt: now() - 7200,
  },
]

const EXT_STYLE: Record<string, { w: number; h: number; size: number }> = {
  png: { w: 1920, h: 1080, size: 1_248_576 },
  jpg: { w: 1200, h: 1600, size: 862_144 },
  gif: { w: 480, h: 480, size: 2_048_000 },
  webp: { w: 1600, h: 900, size: 512_000 },
}

let uploads = Array.from({ length: 57 }, (_, index) => {
  const exts = Object.keys(EXT_STYLE)
  const ext = exts[index % exts.length]
  const style = EXT_STYLE[ext]
  const status = index % 13 === 0 ? 'failed' : index % 17 === 0 ? 'pending' : 'success'
  const storage = storageConfigs[index % 2]

  return {
    UID: `up_mock_${String(index + 1).padStart(3, '0')}`,
    UserUID: 'usr_mock_admin',
    StorageUID: storage.UID,
    AlbumUID: index % 3 === 0 ? 'al_mock_wallpaper' : '',
    FileName: `sample-${index + 1}.${ext}`,
    OriginalName: `IMG_${1000 + index}.${ext}`,
    AliasName: index % 7 === 0 ? `封面图 ${index + 1}` : '',
    Size: style.size + index * 1024,
    MimeType: `image/${ext === 'jpg' ? 'jpeg' : ext}`,
    Extension: ext,
    Width: style.w,
    Height: style.h,
    SHA256: `mock${index}`.padEnd(64, '0'),
    // 用 picsum 的稳定地址，避免 mock 下大量 404（D84：不做缩略图，直接引 URL）
    URL: `https://picsum.photos/seed/picgo-web-${index}/1200/800`,
    ThumbURL: '',
    Status: status,
    Error: status === 'failed' ? '图床返回 422：文件名已存在' : '',
    Source: 'web',
    JobUID: `job_mock_${Math.floor(index / 6) + 1}`,
    Metadata: {},
    StorageName: storage.Name,
    UserEmail: index % 5 === 0 ? 'user@example.com' : 'admin@localhost',
    CreatedAt: now() - index * 3600,
    UpdatedAt: now() - index * 3600,
  }
})

let albums = [
  {
    UID: 'al_mock_wallpaper',
    UserUID: 'usr_mock_admin',
    ParentUID: '',
    Name: '壁纸',
    Intro: '桌面壁纸合集',
    CoverUploadUID: 'up_mock_001',
    CoverURL: 'https://picsum.photos/seed/picgo-web-0/1200/800',
    ImageCount: 19,
    SortOrder: 0,
    Metadata: {},
    CreatedAt: now() - 86400 * 5,
    UpdatedAt: now() - 3600,
  },
  {
    UID: 'al_mock_docs',
    UserUID: 'usr_mock_admin',
    ParentUID: '',
    Name: '文档配图',
    Intro: '',
    CoverUploadUID: '',
    CoverURL: '',
    ImageCount: 0,
    SortOrder: 1,
    Metadata: {},
    CreatedAt: now() - 86400 * 2,
    UpdatedAt: now() - 86400 * 2,
  },
]

const JOBS = [
  {
    UID: 'job_mock_1',
    Kind: 'upload',
    Status: 'succeeded',
    Progress: 100,
    UserUID: 'usr_mock_admin',
    StorageUID: 'st_mock_github_work',
    TotalItems: 6,
    SucceededItems: 6,
    FailedItems: 0,
    SkippedItems: 0,
    Payload: {},
    Result: {
      Total: 6,
      Succeeded: 6,
      Failed: 0,
      Skipped: 0,
      Items: [{ Seq: 1, URL: 'https://picsum.photos/seed/a/800/600', FileName: 'a.png' }],
    },
    Error: '',
    CreatedAt: now() - 1800,
    StartedAt: now() - 1795,
    FinishedAt: now() - 1780,
  },
  {
    UID: 'job_mock_2',
    Kind: 'upload',
    Status: 'failed',
    Progress: 100,
    UserUID: 'usr_mock_admin',
    StorageUID: 'st_mock_webdav_nut',
    TotalItems: 3,
    SucceededItems: 2,
    FailedItems: 1,
    SkippedItems: 0,
    Payload: {},
    // D37：有 item 失败即 failed，但**成功项的结果照样在 Result 里**，不丢数据
    Result: {
      Total: 3,
      Succeeded: 2,
      Failed: 1,
      Skipped: 0,
      Items: [
        { Seq: 1, URL: 'https://picsum.photos/seed/b/800/600', FileName: 'b.png' },
        { Seq: 2, URL: 'https://picsum.photos/seed/c/800/600', FileName: 'c.png' },
        { Seq: 3, URL: '', FileName: 'broken.gif', Error: '图床返回 422' },
      ],
    },
    Error: '1 个文件上传失败',
    CreatedAt: now() - 7200,
    StartedAt: now() - 7195,
    FinishedAt: now() - 7100,
  },
  {
    UID: 'job_mock_3',
    Kind: 'plugin.install',
    Status: 'running',
    Progress: 45,
    UserUID: 'usr_mock_admin',
    StorageUID: '',
    TotalItems: 0,
    SucceededItems: 0,
    FailedItems: 0,
    SkippedItems: 0,
    Payload: { Names: ['picgo-plugin-webp'] },
    Result: null,
    Error: '',
    CreatedAt: now() - 60,
    StartedAt: now() - 58,
    FinishedAt: 0,
  },
]

const JOB_LOGS: Record<string, { Seq: number; Line: string; CreatedAt: number }[]> = {
  job_mock_3: [
    { Seq: 1, Line: '[npm] installing picgo-plugin-webp@1.0.2', CreatedAt: now() - 58 },
    { Seq: 2, Line: '[npm] added 12 packages in 4s', CreatedAt: now() - 55 },
    { Seq: 3, Line: '[picgo] reloading plugin registry…', CreatedAt: now() - 50 },
    { Seq: 4, Line: '[picgo] plugin loaded: picgo-plugin-webp', CreatedAt: now() - 45 },
  ],
  job_mock_1: [
    { Seq: 1, Line: '[upload] a.png → https://example.com/a.png', CreatedAt: now() - 1795 },
    { Seq: 2, Line: '[upload] 队列完成：6/6', CreatedAt: now() - 1780 },
  ],
}

const LOG_TYPES = [
  { Type: 'upload', Label: '上传', TargetType: 'upload' },
  { Type: 'image.delete', Label: '删除图片', TargetType: 'upload' },
  { Type: 'image.update', Label: '整理图片', TargetType: 'upload' },
  { Type: 'mail.send', Label: '邮件发送', TargetType: 'email' },
  { Type: 'user.create', Label: '账号创建', TargetType: 'user' },
  { Type: 'user.delete', Label: '账号注销', TargetType: 'user' },
  { Type: 'user.update', Label: '账号修改', TargetType: 'user' },
  { Type: 'storage.create', Label: '新建存储', TargetType: 'storage' },
  { Type: 'storage.update', Label: '修改存储', TargetType: 'storage' },
  { Type: 'storage.delete', Label: '删除存储', TargetType: 'storage' },
  { Type: 'plugin.install', Label: '安装插件', TargetType: 'plugin' },
  { Type: 'plugin.uninstall', Label: '卸载插件', TargetType: 'plugin' },
  { Type: 'plugin.update', Label: '更新插件', TargetType: 'plugin' },
  { Type: 'auth.login', Label: '登录', TargetType: 'user' },
  { Type: 'auth.failed', Label: '登录失败', TargetType: 'user' },
  { Type: 'auth.logout', Label: '登出', TargetType: 'user' },
  { Type: 'setting.update', Label: '修改设置', TargetType: 'setting' },
  { Type: 'theme.install', Label: '安装主题', TargetType: 'theme' },
  { Type: 'theme.activate', Label: '启用主题', TargetType: 'theme' },
  { Type: 'system.log.cleanup', Label: '日志清理', TargetType: '' },
]

const operationLogs = Array.from({ length: 73 }, (_, index) => {
  const type = LOG_TYPES[index % LOG_TYPES.length]
  const failed = index % 9 === 0

  return {
    UID: `log_mock_${String(index + 1).padStart(3, '0')}`,
    Type: type.Type,
    Status: failed ? 'failed' : 'success',
    UserUID: index % 6 === 0 ? '' : 'usr_mock_admin',
    Username: index % 6 === 0 ? '' : 'admin@localhost',
    TargetType: type.TargetType,
    TargetUID: `up_mock_${String(index + 1).padStart(3, '0')}`,
    Detail: { action: type.Type, fileName: `sample-${index + 1}.png` },
    Error: failed ? '图床返回 422：文件名已存在' : '',
    ClientIP: '127.0.0.1',
    UserAgent: 'Mozilla/5.0 (mock)',
    CreatedAt: now() - index * 900,
  }
})

const emailLogs = Array.from({ length: 18 }, (_, index) => ({
  UID: `mlog_mock_${index + 1}`,
  ToAddress: index % 3 === 0 ? 'user@example.com' : 'admin@localhost',
  Subject: index % 2 === 0 ? '账号邀请' : '重置密码',
  Template: index % 2 === 0 ? 'invite' : 'reset_password',
  Status: index % 7 === 0 ? 'failed' : 'success',
  Error: index % 7 === 0 ? '连接 SMTP 服务器超时' : '',
  RelatedUserUID: 'usr_mock_admin',
  CreatedAt: now() - index * 3600,
}))

const PLUGINS = [
  {
    Name: 'picgo-plugin-github-plus',
    Version: '1.2.3',
    Description: 'GitHub 图床增强：支持同步删除、按仓库管理图片',
    Author: 'zwing',
    Homepage: 'https://github.com/zWing-org/picgo-plugin-github-plus',
    Uploader: 'githubPlus',
    Transformer: '',
    Enabled: true,
    GuiOnly: false,
    InstalledAt: now() - 86400 * 20,
  },
  {
    Name: 'picgo-plugin-webp',
    Version: '1.0.2',
    Description: '上传前把图片转成 WebP',
    Author: 'picgo',
    Homepage: '',
    Uploader: '',
    Transformer: 'webp',
    Enabled: false,
    GuiOnly: false,
    InstalledAt: now() - 86400 * 5,
  },
  {
    Name: 'picgo-plugin-desktop-only',
    Version: '0.3.0',
    Description: '含桌面端菜单的示例插件（Web 端不可执行其菜单能力）',
    Author: 'someone',
    Homepage: '',
    Uploader: 'desktopOnly',
    Transformer: '',
    Enabled: true,
    GuiOnly: true,
    InstalledAt: now() - 86400,
  },
]

const THEMES = [
  {
    ID: 'default',
    Name: '默认主题',
    Version: '1.0.0',
    Description: 'PicGo-Web 默认首页主题',
    Author: 'YeqingKy',
    Tags: ['现代', '亮暗双主题'],
    Repo: '',
    MinAppVersion: '0.1.0',
    Pages: ['/'],
    IsActive: true,
    IsBuiltin: true,
    CanUninstall: false,
    SettingCount: 5,
    ScreenshotURL: '',
    Valid: true,
    Error: '',
  },
  {
    ID: 'minimal',
    Name: '极简主题',
    Version: '2.1.0',
    Description: '只保留标题与上传区',
    Author: 'someone',
    Tags: ['极简'],
    Repo: '',
    MinAppVersion: '0.1.0',
    Pages: ['/', '/gallery'],
    IsActive: false,
    IsBuiltin: false,
    CanUninstall: true,
    SettingCount: 3,
    ScreenshotURL: '',
    Valid: true,
    Error: '',
  },
  {
    ID: 'broken',
    Name: '损坏的主题',
    Version: '',
    Description: '',
    Author: '',
    Tags: [],
    Repo: '',
    MinAppVersion: '',
    // 声明接管认证页 → 校验必须拒绝（D94.2）
    Pages: ['/login'],
    IsActive: false,
    IsBuiltin: false,
    CanUninstall: true,
    SettingCount: 0,
    ScreenshotURL: '',
    Valid: false,
    Error: 'Pages 非法：/login 是系统保留路径（认证页不可被主题接管）',
  },
]

const THEME_SCHEMA = [
  {
    Key: 'BackgroundURL',
    Name: '背景图地址',
    Type: 'string',
    Required: false,
    Default: 'https://api.yppp.net/api.php',
    Help: '留空则不显示背景图',
  },
  { Key: 'ShowHomeFeatures', Name: '显示核心能力区块', Type: 'switch', Default: true },
  {
    Key: 'HomepageFeatures',
    Name: '核心能力',
    Type: 'json',
    Default: [],
    ItemSchema: [
      { Key: 'Icon', Name: '图标', Type: 'string', Required: true },
      { Key: 'Title', Name: '标题', Type: 'string', Required: true },
      { Key: 'Desc', Name: '描述', Type: 'text', Required: true },
    ],
  },
  {
    Key: 'HomepageFaq',
    Name: '常见问题',
    Type: 'json',
    Default: [],
    ItemSchema: [
      { Key: 'Question', Name: '问题', Type: 'string', Required: true },
      { Key: 'Answer', Name: '答案', Type: 'text', Required: true },
    ],
  },
]

const THEME_VALUES = {
  BackgroundURL: { Value: 'https://api.yppp.net/api.php', Default: '', Source: 'db' },
  ShowHomeFeatures: { Value: true, Default: true, Source: 'default' },
  HomepageFeatures: {
    Value: [{ Icon: 'Server', Title: '多存储驱动', Desc: '同一驱动类型可添加多条实例' }],
    Default: [],
    Source: 'db',
  },
  HomepageFaq: { Value: [], Default: [], Source: 'default' },
}

const SYSTEM_SETTINGS = {
  Groups: [
    {
      Category: 'site',
      Label: '站点',
      Keys: [
        {
          Key: 'site.name',
          Category: 'site',
          Type: 'string',
          Value: 'PicGo Web（mock）',
          Default: 'PicGo Web',
          Source: 'db',
          Secret: false,
          Label: '站点名称',
          Description: '',
          RequiresRestart: false,
        },
        {
          Key: 'site.subtitle',
          Category: 'site',
          Type: 'string',
          Value: '自建图床控制台',
          Default: '',
          Source: 'db',
          Secret: false,
          Label: '副标题',
          Description: '顶栏与首页使用',
          RequiresRestart: false,
        },
        {
          Key: 'site.baseUrl',
          Category: 'site',
          Type: 'string',
          Value: 'http://127.0.0.1:5173',
          Default: '',
          Source: 'db',
          Secret: false,
          Label: '站点地址',
          Description: '用于生成 OAuth 回调地址',
          RequiresRestart: false,
        },
        {
          Key: 'site.notice',
          Category: 'site',
          Type: 'string',
          Value: '',
          Default: '',
          Source: 'default',
          Secret: false,
          Label: '站点公告',
          Description: '支持 Markdown',
          RequiresRestart: false,
        },
      ],
    },
    {
      Category: 'user',
      Label: '用户',
      Keys: [
        {
          Key: 'user.defaultCapacityBytes',
          Category: 'user',
          Type: 'int',
          Value: 5368709120,
          Default: 5368709120,
          Source: 'default',
          Secret: false,
          Label: '新建用户默认配额（字节）',
          Description: '只影响此后新建的用户',
          RequiresRestart: false,
        },
      ],
    },
    {
      Category: 'mail',
      Label: '邮件',
      Keys: [
        {
          Key: 'mail.enabled',
          Category: 'mail',
          Type: 'bool',
          Value: false,
          Default: false,
          Source: 'default',
          Secret: false,
          Label: '启用邮件',
          Description: '',
          RequiresRestart: false,
        },
        {
          Key: 'mail.host',
          Category: 'mail',
          Type: 'string',
          Value: '',
          Default: '',
          Source: 'default',
          Secret: false,
          Label: 'SMTP 主机',
          Description: '',
          RequiresRestart: false,
        },
        {
          Key: 'mail.password',
          Category: 'mail',
          Type: 'secret',
          Value: '******',
          HasValue: false,
          Default: '',
          Source: 'default',
          Secret: true,
          Label: 'SMTP 密码',
          Description: '留空表示不修改',
          RequiresRestart: false,
        },
      ],
    },
    {
      Category: 'oauth',
      Label: 'OAuth',
      Keys: [
        {
          Key: 'oauth.github.enabled',
          Category: 'oauth',
          Type: 'bool',
          Value: false,
          Default: false,
          Source: 'default',
          Secret: false,
          Label: '启用 GitHub 登录',
          Description: '只用于已绑定用户的便捷登录',
          RequiresRestart: false,
        },
        {
          Key: 'oauth.github.clientId',
          Category: 'oauth',
          Type: 'string',
          Value: '',
          Default: '',
          Source: 'default',
          Secret: false,
          Label: 'GitHub Client ID',
          Description: '',
          RequiresRestart: false,
        },
      ],
    },
    {
      Category: 'security',
      Label: '安全',
      Keys: [
        {
          Key: 'security.sessionTtlHours',
          Category: 'security',
          Type: 'int',
          Value: 168,
          Default: 168,
          Source: 'default',
          Secret: false,
          Label: '会话有效期（小时）',
          Description: '',
          RequiresRestart: false,
        },
        {
          Key: 'security.loginMaxAttempts',
          Category: 'security',
          Type: 'int',
          Value: 5,
          Default: 5,
          Source: 'default',
          Secret: false,
          Label: '登录失败上限',
          Description: '',
          RequiresRestart: false,
        },
      ],
    },
    {
      Category: 'log',
      Label: '日志',
      Keys: [
        {
          Key: 'log.retentionDays',
          Category: 'log',
          Type: 'int',
          Value: 180,
          Default: 180,
          Source: 'default',
          Secret: false,
          Label: '操作日志保留天数',
          Description: '0 表示永久保留',
          RequiresRestart: false,
        },
        {
          Key: 'log.jobRetentionDays',
          Category: 'log',
          Type: 'int',
          Value: 7,
          Default: 7,
          Source: 'default',
          Secret: false,
          Label: '任务日志保留天数',
          Description: '',
          RequiresRestart: false,
        },
      ],
    },
    {
      Category: 'upload',
      Label: '上传',
      Keys: [
        {
          Key: 'upload.maxSizeBytes',
          Category: 'upload',
          Type: 'int',
          Value: 20971520,
          Default: 20971520,
          Source: 'default',
          Secret: false,
          Label: '单文件上限（字节）',
          Description: '',
          RequiresRestart: false,
        },
        {
          Key: 'upload.concurrency',
          Category: 'upload',
          Type: 'int',
          Value: 1,
          Default: 1,
          Source: 'default',
          Secret: false,
          Label: '上传并发度',
          Description: '1 = 严格串行',
          RequiresRestart: false,
        },
        {
          Key: 'upload.rateLimit.enabled',
          Category: 'upload',
          Type: 'bool',
          Value: false,
          Default: false,
          Source: 'default',
          Secret: false,
          Label: '启用上传限流',
          Description: '默认禁用',
          RequiresRestart: false,
        },
      ],
    },
    {
      Category: 'picgo',
      Label: 'PicGo',
      Keys: [
        {
          Key: 'picgo.npmRegistry',
          Category: 'picgo',
          Type: 'string',
          Value: 'https://registry.npmmirror.com',
          Default: 'https://registry.npmmirror.com',
          Source: 'default',
          Secret: false,
          Label: 'npm 源',
          Description: '安装插件使用',
          RequiresRestart: false,
        },
      ],
    },
    {
      Category: 'integration',
      Label: '对外集成',
      Keys: [
        {
          Key: 'integration.lsky.enabled',
          Category: 'integration',
          Type: 'bool',
          Value: true,
          Default: true,
          Source: 'default',
          Secret: false,
          Label: '启用 Lsky 兼容层',
          Description: '供第三方客户端把本站当图床',
          RequiresRestart: false,
        },
      ],
    },
  ],
  Meta: { SchemaKeyCount: 42, DBKeyCount: 3, UpdatedAt: now() },
}

// ---------------------------------------------------------------------------
// 路由
// ---------------------------------------------------------------------------

/** 取分页参数（Query 用 PascalCase，见 API.md §0.6）。 */
function pageParams(config: AxiosRequestConfig): { page: number; pageSize: number; raw: URLSearchParams } {
  const raw = new URLSearchParams((config.params ?? {}) as Record<string, string>)
  // axios 的 params 是对象时我们直接读；query string 形式也从 url 里取一次
  const url = config.url ?? ''
  const qIndex = url.indexOf('?')
  if (qIndex >= 0) {
    for (const [key, value] of new URLSearchParams(url.slice(qIndex + 1))) raw.set(key, value)
  }

  const page = Number(config.params?.Page ?? raw.get('Page') ?? 1) || 1
  const pageSize = Number(config.params?.PageSize ?? raw.get('PageSize') ?? 20) || 20
  return { page, pageSize, raw }
}

function paginate<T>(items: T[], page: number, pageSize: number) {
  const start = (page - 1) * pageSize
  return { Items: items.slice(start, start + pageSize), Total: items.length, Page: page, PageSize: pageSize }
}

function paramOf(config: AxiosRequestConfig, key: string): string {
  const fromParams = config.params?.[key]
  if (fromParams !== undefined && fromParams !== null) return String(fromParams)

  const url = config.url ?? ''
  const qIndex = url.indexOf('?')
  if (qIndex < 0) return ''
  return new URLSearchParams(url.slice(qIndex + 1)).get(key) ?? ''
}

/**
 * 处理 W8 各域的请求。未匹配返回 `null`（由调用方回落到真实后端或 501）。
 */
export function handleW8(
  method: string,
  path: string,
  config: AxiosRequestConfig,
): AxiosResponse<Envelope<unknown>> | null {
  const body = (config.data ?? {}) as Record<string, unknown>
  const { page, pageSize } = pageParams(config)

  // ---------------- 存储（API.md §3） ----------------
  if (method === 'get' && path === '/storage/drivers') {
    return ok(config, { Drivers: DRIVERS })
  }

  if (method === 'post' && path === '/storage/drivers/schema') {
    const type = String(body.Type ?? '')
    const driver = DRIVERS.find((item) => item.Type === type)
    return ok(config, { Type: type, Name: driver?.Name ?? type, Config: driver?.Config ?? [] })
  }

  if (method === 'get' && path === '/storage/configs') {
    const type = paramOf(config, 'Type')
    const keyword = paramOf(config, 'Keyword').toLowerCase()
    let list = storageConfigs
    if (type) list = list.filter((item) => item.Type === type)
    if (keyword) list = list.filter((item) => item.Name.toLowerCase().includes(keyword))
    return ok(config, paginate(list, page, pageSize))
  }

  if (method === 'post' && path === '/storage/configs') {
    const name = String(body.Name ?? '').trim()
    if (storageConfigs.some((item) => item.Name === name)) {
      return fail(config, ApiCode.Conflict, '同名存储配置已存在', 409)
    }

    const type = String(body.Type ?? '')
    const driver = DRIVERS.find((item) => item.Type === type)

    const created = {
      UID: `st_mock_${Date.now().toString(36)}`,
      Name: name,
      Type: type,
      PicgoConfigName: String(body.PicgoConfigName ?? 'Default'),
      Enabled: body.Enabled !== false,
      IsDefault: Boolean(body.IsDefault),
      PathTemplate: String(body.PathTemplate ?? ''),
      FileTemplate: String(body.FileTemplate ?? ''),
      Capabilities:
        driver?.Capabilities ?? {
          SupportsPathTemplate: false,
          SupportsRemoteDelete: false,
          ConfigFields: [],
          PathFieldNames: [],
          DetectedAt: now(),
          PicgoVersion: '3.0.2',
        },
      HasSecrets: Boolean(body.Config && Object.keys(body.Config as object).length > 0),
      SecretFields: Object.keys((body.Config as Record<string, unknown>) ?? {}),
      Metadata: {},
      UploadCount: 0,
      CreatedAt: now(),
      UpdatedAt: now(),
    }

    if (created.IsDefault) {
      storageConfigs = storageConfigs.map((item) => ({ ...item, IsDefault: false }))
    }
    storageConfigs = [...storageConfigs, created]
    return ok(config, created)
  }

  {
    const params = match(path, '/storage/configs/{uid}')
    if (params) {
      const index = storageConfigs.findIndex((item) => item.UID === params.uid)

      if (method === 'get' && index >= 0) return ok(config, storageConfigs[index])

      if (method === 'patch' && index >= 0) {
        const patch = body as Partial<(typeof storageConfigs)[number]>
        // `PicgoConfigName` 创建后只读（D64）—— 显式忽略，避免误改
        delete (patch as Record<string, unknown>).PicgoConfigName
        delete (patch as Record<string, unknown>).UID

        if (patch.IsDefault) {
          storageConfigs = storageConfigs.map((item) => ({ ...item, IsDefault: false }))
        }
        storageConfigs[index] = { ...storageConfigs[index], ...patch, UpdatedAt: now() }
        return ok(config, storageConfigs[index])
      }

      if (method === 'delete' && index >= 0) {
        const [removed] = storageConfigs.splice(index, 1)
        return ok(config, { Deleted: true, AffectedUploads: removed.UploadCount, DefaultSwitchedTo: '' })
      }
    }

    const test = match(path, '/storage/configs/{uid}/test')
    if (method === 'post' && test) {
      const target = storageConfigs.find((item) => item.UID === test.uid)
      return ok(config, {
        Ok: Boolean(target?.HasSecrets),
        Message: target?.HasSecrets ? '连接成功' : '尚未配置凭据',
        LatencyMs: 180 + Math.floor(Math.random() * 200),
        DriverVersion: '3.0.2',
      })
    }

    const activate = match(path, '/storage/configs/{uid}/activate')
    if (method === 'post' && activate) {
      storageConfigs = storageConfigs.map((item) => ({
        ...item,
        IsDefault: item.UID === activate.uid,
      }))
      const target = storageConfigs.find((item) => item.UID === activate.uid)
      return target ? ok(config, target) : fail(config, ApiCode.NotFound, '配置不存在', 404)
    }

    const secrets = match(path, '/storage/configs/{uid}/secrets')
    if (method === 'put' && secrets) {
      const target = storageConfigs.find((item) => item.UID === secrets.uid)
      if (!target) return fail(config, ApiCode.NotFound, '配置不存在', 404)
      const patch = (body.Config as Record<string, unknown>) ?? {}
      const fields = new Set([...target.SecretFields, ...Object.keys(patch)])
      target.HasSecrets = fields.size > 0
      target.SecretFields = [...fields]
      return ok(config, { UID: target.UID, HasSecrets: target.HasSecrets, SecretFields: target.SecretFields })
    }
  }

  // ---------------- 图库（API.md §4） ----------------
  if (method === 'post' && path === '/uploads') {
    // ⚠️ 真实实现是 multipart；mock 下不解析文件，只回一个 job
    const jobUID = `job_mock_${Date.now().toString(36)}`
    return ok(config, {
      JobUID: jobUID,
      StorageUID: String(paramOf(config, 'StorageUID') || storageConfigs[0]?.UID || ''),
      Items: [{ Seq: 1, FileName: 'upload.png', UploadUID: `up_mock_${Date.now().toString(36)}`, Status: 'pending' }],
    })
  }

  if (method === 'post' && path === '/uploads/from-url') {
    const jobUID = `job_mock_${Date.now().toString(36)}`
    return ok(config, {
      JobUID: jobUID,
      StorageUID: String(body.StorageUID ?? ''),
      Items: (body.URLs as string[] | undefined)?.map((url, index) => ({
        Seq: index + 1,
        FileName: url.split('/').pop() ?? 'remote.png',
        UploadUID: `up_mock_${index}`,
        Status: 'pending',
      })) ?? [],
    })
  }

  if (method === 'post' && path === '/uploads/batch-delete') {
    const uids = (body.UIDs as string[]) ?? []
    const deleteRemote = Boolean(body.DeleteRemote)

    uploads = uploads.filter((item) => !uids.includes(item.UID))

    return ok(config, {
      Total: uids.length,
      Deleted: uids.length,
      Skipped: 0,
      FailedRemoteDelete: deleteRemote ? uids.length : 0,
      FreedBytes: uids.length * 512_000,
      Items: uids.map((uid) => ({
        UID: uid,
        Deleted: true,
        // 驱动不支持 / 插件未实现 remove 时如实返回 false（D47）
        RemoteDeleted: false,
        Error: deleteRemote ? '该驱动不支持远端删除，已仅删除记录' : '',
      })),
    })
  }

  if (method === 'post' && path === '/uploads/links') {
    const uids = (body.UIDs as string[]) ?? []
    const format = String(body.Format ?? 'markdown')
    const items = uploads.filter((item) => uids.includes(item.UID))
    const text = items
      .map((item) =>
        format === 'url'
          ? item.URL
          : format === 'html'
            ? `<img src="${item.URL}" alt="${item.FileName}" />`
            : `![${item.FileName}](${item.URL})`,
      )
      .join('\n')
    return ok(config, { Format: format, Text: text, Items: items.map((item) => ({ UID: item.UID, URL: item.URL })) })
  }

  if (method === 'get' && path === '/uploads') {
    const keyword = paramOf(config, 'Keyword').toLowerCase()
    const storageUID = paramOf(config, 'StorageUID')
    const albumUID = paramOf(config, 'AlbumUID')
    const status = paramOf(config, 'Status')
    const scope = paramOf(config, 'Scope') || 'mine'
    const sort = paramOf(config, 'Sort') || 'CreatedAt'
    const order = paramOf(config, 'Order') || 'desc'

    let list = [...uploads]
    if (keyword) {
      list = list.filter(
        (item) =>
          item.FileName.toLowerCase().includes(keyword) ||
          item.OriginalName.toLowerCase().includes(keyword) ||
          item.AliasName.toLowerCase().includes(keyword),
      )
    }
    if (storageUID) list = list.filter((item) => item.StorageUID === storageUID)
    if (albumUID === 'none') list = list.filter((item) => !item.AlbumUID)
    else if (albumUID) list = list.filter((item) => item.AlbumUID === albumUID)
    if (status) list = list.filter((item) => item.Status === status)

    list.sort((a, b) => {
      const dir = order === 'asc' ? 1 : -1
      if (sort === 'Size') return (a.Size - b.Size) * dir
      if (sort === 'FileName') return a.FileName.localeCompare(b.FileName) * dir
      return (a.CreatedAt - b.CreatedAt) * dir
    })

    return ok(config, {
      ...paginate(list, page, pageSize),
      // 便于前端走查（真实后端也会返回类似附加字段）
      Scope: scope,
    })
  }

  if (method === 'get' && path === '/uploads/stats') {
    const scope = paramOf(config, 'Scope') || 'mine'
    return ok(config, {
      Scope: scope,
      Total: uploads.length,
      TotalSize: uploads.reduce((acc, item) => acc + item.Size, 0),
      SuccessCount: uploads.filter((item) => item.Status === 'success').length,
      FailedCount: uploads.filter((item) => item.Status === 'failed').length,
      PendingCount: uploads.filter((item) => item.Status === 'pending').length,
      TodayCount: 3,
      WeekCount: 21,
      ByStorage: storageConfigs.map((item) => ({
        StorageUID: item.UID,
        Name: item.Name,
        Count: uploads.filter((upload) => upload.StorageUID === item.UID).length,
      })),
      ByExtension: ['png', 'jpg', 'gif', 'webp'].map((ext) => ({
        Extension: ext,
        Count: uploads.filter((item) => item.Extension === ext).length,
      })),
    })
  }

  {
    const params = match(path, '/uploads/{uid}')
    if (params) {
      const index = uploads.findIndex((item) => item.UID === params.uid)

      if (method === 'get' && index >= 0) return ok(config, uploads[index])

      if (method === 'patch' && index >= 0) {
        uploads[index] = { ...uploads[index], ...(body as object), UpdatedAt: now() }
        return ok(config, uploads[index])
      }

      if (method === 'delete' && index >= 0) {
        const [removed] = uploads.splice(index, 1)
        const deleteRemote = paramOf(config, 'DeleteRemote') === 'true'
        const driver = DRIVERS.find((item) => item.Type === storageConfigs.find((c) => c.UID === removed.StorageUID)?.Type)
        return ok(config, {
          Deleted: true,
          RemoteDeleted: false,
          RemoteDeleteSupported: Boolean(driver?.Capabilities.SupportsRemoteDelete),
          RemoteDeleteError: deleteRemote ? '该驱动不支持远端删除，已仅删除记录' : '',
          FreedBytes: removed.Size,
        })
      }
    }

    const link = match(path, '/uploads/{uid}/link')
    if (method === 'get' && link) {
      const target = uploads.find((item) => item.UID === link.uid)
      if (!target) return fail(config, ApiCode.NotFound, '图片不存在', 404)

      const format = paramOf(config, 'Format') || 'markdown'
      const name = target.AliasName || target.OriginalName || target.FileName
      const text =
        format === 'url'
          ? target.URL
          : format === 'html'
            ? `<img src="${target.URL}" alt="${name}" />`
            : `![${name}](${target.URL})`

      return ok(config, { UID: target.UID, Format: format, Text: text, URL: target.URL })
    }
  }

  // ---------------- 相册（API.md §5） ----------------
  if (method === 'get' && path === '/albums') {
    const keyword = paramOf(config, 'Keyword').toLowerCase()
    const list = keyword ? albums.filter((item) => item.Name.toLowerCase().includes(keyword)) : albums
    return ok(config, { Items: list })
  }

  if (method === 'post' && path === '/albums') {
    const name = String(body.Name ?? '').trim()
    if (albums.some((item) => item.Name === name)) {
      return fail(config, ApiCode.Conflict, '同名相册已存在', 409)
    }
    const created = {
      UID: `al_mock_${Date.now().toString(36)}`,
      UserUID: 'usr_mock_admin',
      ParentUID: '',
      Name: name,
      Intro: String(body.Intro ?? ''),
      CoverUploadUID: '',
      CoverURL: '',
      ImageCount: 0,
      SortOrder: albums.length,
      Metadata: {},
      CreatedAt: now(),
      UpdatedAt: now(),
    }
    albums = [...albums, created]
    return ok(config, created)
  }

  if (method === 'post' && path === '/albums/move-uploads') {
    const uids = (body.UploadUIDs as string[]) ?? []
    const targetUID = String(body.TargetAlbumUID ?? '')
    let moved = 0

    uploads = uploads.map((item) => {
      if (!uids.includes(item.UID)) return item
      moved += 1
      return { ...item, AlbumUID: targetUID, UpdatedAt: now() }
    })

    return ok(config, { Moved: moved, Skipped: uids.length - moved })
  }

  {
    const params = match(path, '/albums/{uid}')
    if (params) {
      const index = albums.findIndex((item) => item.UID === params.uid)

      if (method === 'get' && index >= 0) return ok(config, albums[index])

      if (method === 'patch' && index >= 0) {
        albums[index] = { ...albums[index], ...(body as object), UpdatedAt: now() }
        return ok(config, albums[index])
      }

      if (method === 'delete' && index >= 0) {
        const album = albums[index]
        // 有图片且未显式要求时拒绝（D: 相册删除语义）
        if (album.ImageCount > 0 && paramOf(config, 'WithUploads') !== 'true') {
          return fail(config, ApiCode.Conflict, `相册内有 ${album.ImageCount} 张图片，请先移出`, 409)
        }
        albums.splice(index, 1)
        return ok(config, { Deleted: true, DetachedUploads: 0 })
      }
    }

    const move = match(path, '/albums/{uid}/move-uploads')
    if (method === 'post' && move) {
      const uids = (body.UploadUIDs as string[]) ?? []
      let moved = 0
      uploads = uploads.map((item) => {
        if (!uids.includes(item.UID)) return item
        moved += 1
        return { ...item, AlbumUID: move.uid, UpdatedAt: now() }
      })
      return ok(config, { Moved: moved, Skipped: uids.length - moved })
    }
  }

  // ---------------- 任务（API.md §8） ----------------
  if (method === 'get' && path === '/jobs') {
    const kind = paramOf(config, 'Kind')
    const status = paramOf(config, 'Status')
    let list = JOBS
    if (kind) list = list.filter((item) => item.Kind === kind)
    if (status) list = list.filter((item) => item.Status === status)
    return ok(config, paginate(list, page, pageSize))
  }

  {
    const params = match(path, '/jobs/{uid}')
    if (method === 'get' && params) {
      const job = JOBS.find((item) => item.UID === params.uid)
      if (!job) return fail(config, ApiCode.NotFound, '任务不存在', 404)

      return ok(config, {
        ...job,
        Items: [
          { Seq: 1, UploadUID: 'up_mock_001', FileName: 'a.png', Status: 'succeeded', Attempts: 1, Error: '', StartedAt: now() - 100, FinishedAt: now() - 95 },
          { Seq: 2, UploadUID: 'up_mock_002', FileName: 'b.jpg', Status: job.Status === 'failed' ? 'failed' : 'succeeded', Attempts: 2, Error: job.Status === 'failed' ? '图床返回 422' : '', StartedAt: now() - 95, FinishedAt: now() - 90 },
        ],
      })
    }

    if (method === 'delete' && params) {
      const job = JOBS.find((item) => item.UID === params.uid)
      if (job?.Status === 'running') {
        return fail(config, ApiCode.Conflict, '任务进行中，无法清理', 409)
      }
      return ok(config, null)
    }

    const logs = match(path, '/jobs/{uid}/logs')
    if (method === 'get' && logs) {
      const after = Number(paramOf(config, 'AfterSeq') || 0)
      const items = (JOB_LOGS[logs.uid] ?? []).filter((line) => line.Seq > after)
      return ok(config, {
        Items: items,
        HasMore: false,
        LastSeq: items.length > 0 ? items[items.length - 1].Seq : after,
      })
    }
  }

  // ---------------- 操作日志（API.md §9） ----------------
  if (method === 'get' && path === '/logs/types') {
    return ok(config, { Types: LOG_TYPES })
  }

  if (method === 'get' && path === '/logs/emails') {
    const status = paramOf(config, 'Status')
    let list = emailLogs
    if (status) list = list.filter((item) => item.Status === status)
    return ok(config, paginate(list, page, pageSize))
  }

  if (method === 'get' && path === '/logs') {
    const type = paramOf(config, 'Type')
    const status = paramOf(config, 'Status')
    const keyword = paramOf(config, 'Keyword').toLowerCase()

    let list = operationLogs
    if (type) list = list.filter((item) => item.Type === type)
    if (status) list = list.filter((item) => item.Status === status)
    if (keyword) {
      list = list.filter((item) =>
        [item.Username, item.TargetUID, item.Error, JSON.stringify(item.Detail)]
          .join(' ')
          .toLowerCase()
          .includes(keyword),
      )
    }
    return ok(config, paginate(list, page, pageSize))
  }

  {
    const params = match(path, '/logs/{uid}')
    if (method === 'get' && params) {
      const log = operationLogs.find((item) => item.UID === params.uid)
      return log ? ok(config, log) : fail(config, ApiCode.NotFound, '日志不存在', 404)
    }
  }

  // ---------------- 插件（API.md §6） ----------------
  if (method === 'get' && path === '/plugins') {
    return ok(config, { Items: PLUGINS, PendingJobs: JOBS.filter((job) => job.Status === 'running') })
  }

  if (method === 'get' && path === '/plugins/search') {
    const q = paramOf(config, 'Q') || 'picgo-plugin-'
    const catalog = [
      { Name: 'picgo-plugin-webp', Version: '1.0.2', Description: '转为 WebP', Author: 'picgo', Homepage: '' },
      { Name: 'picgo-plugin-s3', Version: '1.5.2', Description: 'S3 兼容存储', Author: 'picgo', Homepage: '' },
      { Name: 'picgo-plugin-github-plus', Version: '1.2.3', Description: 'GitHub 增强', Author: 'zwing', Homepage: '' },
      { Name: 'picgo-plugin-compress', Version: '1.0.1', Description: '图片压缩', Author: 'picgo', Homepage: '' },
    ]
    const items = catalog
      .filter((item) => item.Name.includes(q) || q.includes('picgo-plugin-'))
      .slice(0, 20)
      .map((item) => ({ ...item, Installed: PLUGINS.some((p) => p.Name === item.Name) }))
    return ok(config, { Items: items, Total: items.length, Page: 1, PageSize: 20 })
  }

  if (method === 'post' && (path === '/plugins/install' || path === '/plugins/uninstall' || path === '/plugins/update')) {
    return ok(config, { JobUID: 'job_mock_3' })
  }

  {
    const readme = match(path, '/plugins/{name}/readme')
    if (method === 'get' && readme) {
      return ok(config, {
        Name: readme.name,
        Content: `# ${readme.name}\n\n这是 mock 的 README。\n\n- 支持的功能 A\n- 支持的功能 B\n`,
        Truncated: false,
      })
    }

    const params = match(path, '/plugins/{name}')
    if (method === 'patch' && params) {
      const plugin = PLUGINS.find((item) => item.Name === params.name)
      if (plugin) plugin.Enabled = Boolean(body.Enabled)
      return ok(config, { Name: params.name, Enabled: Boolean(body.Enabled) })
    }
  }

  // ---------------- 主题（API.md §10） ----------------
  if (method === 'get' && path === '/themes') {
    return ok(config, { Active: 'default', Items: THEMES, ScannedAt: now() })
  }

  if (method === 'post' && path === '/themes/rescan') {
    return ok(config, { Active: 'default', Items: THEMES, ScannedAt: now() })
  }

  if (method === 'post' && path === '/themes/install') {
    return fail(config, ApiCode.InvalidParam, '压缩包内含非法路径（Zip Slip）：../evil.js', 400)
  }

  if (method === 'put' && path === '/themes/active') {
    const target = THEMES.find((item) => item.ID === body.ThemeID)
    if (!target) return fail(config, ApiCode.NotFound, '主题不存在', 404)
    if (!target.Valid) return fail(config, ApiCode.InvalidParam, target.Error, 400)

    const previous = THEMES.find((item) => item.IsActive)?.ID ?? ''
    for (const theme of THEMES) theme.IsActive = theme.ID === target.ID
    return ok(config, { Active: target.ID, Previous: previous })
  }

  {
    const params = match(path, '/themes/{id}')
    if (method === 'delete' && params) {
      const target = THEMES.find((item) => item.ID === params.id)
      if (!target) return fail(config, ApiCode.NotFound, '主题不存在', 404)
      if (target.IsActive) return fail(config, ApiCode.Conflict, '正在使用的主题不能卸载', 409)
      if (target.IsBuiltin) return fail(config, ApiCode.Conflict, '内置主题不可卸载', 409)
      return ok(config, null)
    }

    const settings = match(path, '/themes/{id}/settings')
    if (settings) {
      const target = THEMES.find((item) => item.ID === settings.id)
      if (!target) return fail(config, ApiCode.NotFound, '主题不存在', 404)

      if (method === 'get') {
        return ok(config, {
          ThemeID: settings.id,
          Name: target.Name,
          Pages: target.Pages,
          Schema: THEME_SCHEMA,
          Values: THEME_VALUES,
        })
      }

      if (method === 'put') {
        const values = (body.Values as Record<string, unknown>) ?? {}
        for (const [key, value] of Object.entries(values)) {
          if (THEME_VALUES[key as keyof typeof THEME_VALUES]) {
            ;(THEME_VALUES as Record<string, unknown>)[key] = {
              Value: value,
              Default: (THEME_VALUES as Record<string, { Default: unknown }>)[key].Default,
              Source: 'db',
            }
          }
        }
        return ok(config, { Updated: Object.keys(values).length })
      }

      if (method === 'delete') {
        if (target.IsActive) return fail(config, ApiCode.Conflict, '正在使用的主题不能清理配置', 409)
        return ok(config, null)
      }
    }
  }

  // ---------------- 系统设置（API.md §11） ----------------
  if (method === 'get' && path === '/settings/system') {
    return ok(config, SYSTEMS_SETTINGS_SAFE())
  }

  if (method === 'put' && path === '/settings/system') {
    const applied: string[] = []
    for (const [key, value] of Object.entries(body)) {
      for (const group of SYSTEM_SETTINGS.Groups) {
        const item = group.Keys.find((entry) => entry.Key === key)
        if (item) {
          ;(item as { Value: unknown }).Value = value
          ;(item as { Source: string }).Source = 'db'
          applied.push(key)
        }
      }
    }
    return ok(config, { Applied: applied, Effects: [] })
  }

  if (method === 'post' && path === '/settings/mail/test') {
    const to = String(body.To ?? '')
    if (!to) return fail(config, ApiCode.InvalidParam, '请输入收件地址', 400)
    return ok(config, { Ok: false, Message: '邮件功能未启用（mock）' })
  }

  return null
}

/** 返回一份深拷贝，避免 mock 内部状态被调用方意外修改。 */
function SYSTEMS_SETTINGS_SAFE() {
  return JSON.parse(JSON.stringify(SYSTEM_SETTINGS)) as typeof SYSTEM_SETTINGS
}
