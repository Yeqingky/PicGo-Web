/**
 * 与 `docs/API.md` §13（picgo-agent 内部契约）一一对应的请求/响应类型。
 *
 * 命名规则（D81）：**外层字段一律 PascalCase**。
 * **唯一例外**：从 picgo 读来、需要原样透传的结构（`picgoConfig` / `_configName` /
 * 驱动字段名 / `IImgInfo`）保持原样，见下方 `RawPicgo` 系列类型。
 */

import type { IImgInfo, IPluginConfig, IUploaderConfigItem } from 'picgo'

// ---------------------------------------------------------------------------
// 响应信封
// ---------------------------------------------------------------------------

/** agent 的响应码是**字符串**（与 Go 侧的数字码不同）。 */
export const Code = {
  OK: 'OK',
  ERR_PARAM: 'ERR_PARAM',
  ERR_PICGO: 'ERR_PICGO',
  ERR_NOT_FOUND: 'ERR_NOT_FOUND',
  ERR_INTERNAL: 'ERR_INTERNAL'
} as const

export type CodeValue = (typeof Code)[keyof typeof Code]

export interface Envelope<T = unknown> {
  Code: CodeValue
  Message: string
  Data: T | null
}

// ---------------------------------------------------------------------------
// 原样透传的 picgo 结构（**字段名不得转换**，D81.3 第 5 条）
// ---------------------------------------------------------------------------

/**
 * picgo 的完整配置对象。
 *
 * 键名是 picgo 的原生结构（`picBed` / `picgoPlugins` / 插件私有键如 `uploaded`）。
 * **不要**对它做 PascalCase 转换，也不要删减键。
 */
export type RawPicgoConfig = Record<string, unknown>

/** 某个 uploader 的一条命名配置（`_id` / `_configName` 及驱动字段）。 */
export type RawUploaderConfig = IUploaderConfigItem

/** 上传返回的单张图片信息（plugin 会往里回写 `sha` 等字段）。 */
export type RawImgInfo = IImgInfo

// ---------------------------------------------------------------------------
// 1) 健康与生命周期
// ---------------------------------------------------------------------------

export interface HealthzData {
  Ok: boolean
  PicgoVersion: string
  ConfigPath: string
  Uptime: number
  PID: number
  PluginsLoaded: number
}

// ---------------------------------------------------------------------------
// 2) 配置
// ---------------------------------------------------------------------------

export interface ConfigGetData {
  /** **原样**的 picgo 配置。 */
  Config: RawPicgoConfig
  ConfigPath: string
}

export interface ConfigPutRequest {
  Config: RawPicgoConfig
}

export interface ConfigPatchRequest {
  /** 点路径 → 值，如 `{ "picBed.uploader": "github" }`。键是 picgo 的结构，原样。 */
  Patch: Record<string, unknown>
}

export interface ConfigPatchData {
  /** 实际写入的键。 */
  Applied: string[]
  /** 检测到并保留的插件私有键（未被动过）。 */
  PreservedPluginKeys: string[]
}

// ---------------------------------------------------------------------------
// 3) 上传器（驱动）
// ---------------------------------------------------------------------------

/**
 * 驱动能力探测结果（D44 / D77.2：**不硬编码驱动名**）。
 *
 * 由 agent 运行时从驱动 schema 推断，Go 侧缓存进 `StorageConfigs.Capabilities`。
 */
export interface Capabilities {
  /** 驱动 schema 中是否存在路径类字段（支持魔法路径）。 */
  SupportsPathTemplate: boolean
  /** 该驱动是否支持远端删除（走 `remove` 事件约定，D47）。 */
  SupportsRemoteDelete: boolean
  /** 该驱动的配置字段名（原样，如 repo/token/path）。 */
  ConfigFields: string[]
  /** 命中的路径类字段名。 */
  PathFieldNames: string[]
  /** 探测时间（Unix 秒）。 */
  DetectedAt: number
  /** picgo-core 版本。 */
  PicgoVersion: string
}

/** 求值后的驱动配置字段（`IPluginConfig` 的静态形态）。 */
export type DriverConfigField = IPluginConfig

export interface UploaderItem {
  Type: string
  Name: string
  /** 内置驱动（picgo-core 自带）为 true；插件提供为 false。 */
  Builtin: boolean
  /** 含 `guiMenu` / `commands`（Electron 专属），Web 端不可用。 */
  GuiOnly: boolean
  /** 已用 `evaluatePluginConfig` 求值过的表单 schema。 */
  Config: DriverConfigField[]
  Capabilities: Capabilities
  /** 该类型下已保存的配置名列表。 */
  ConfigNames: string[]
}

export interface UploadersListData {
  Uploaders: UploaderItem[]
  Current: { Type: string; ConfigName: string }
  Transformer: string
}

export interface UploaderSchemaRequest {
  Type: string
  /** 当前表单的字段值快照，供 `dependsOn` 联动求值使用。 */
  Answers?: Record<string, unknown>
}

export interface UploaderSchemaData {
  Type: string
  Name: string
  Config: DriverConfigField[]
  Capabilities: Capabilities
}

export interface UploaderTestRequest {
  Type: string
  /** 测已保存的配置。 */
  ConfigName?: string
  /** 或直接测一份未落盘的配置（用于新建表单）。 */
  Config?: Record<string, unknown>
  Answers?: Record<string, unknown>
}

export interface UploaderTestData {
  Ok: boolean
  Message: string
  LatencyMs: number
  Detail?: string
}

export interface UploaderConfigsListData {
  Type: string
  DefaultConfigName: string
  /** **原样**的 picgo 配置项（含明文凭据）。脱敏是 Go 侧的职责。 */
  Configs: RawUploaderConfig[]
}

export interface UploaderConfigUpsertRequest {
  Type: string
  ConfigName: string
  /** 驱动字段名原样（repo / token / path …）。 */
  Config: Record<string, unknown>
  /** 是否同时切换为当前激活配置。 */
  Activate?: boolean
}

export interface UploaderConfigUpsertData {
  Type: string
  /** 至少含 `_id` 与 `_configName`。 */
  Config: RawUploaderConfig
}

export interface UploaderUseRequest {
  Type: string
  ConfigName?: string
}

export interface TransformersListData {
  Current: string
  Transformers: { Type: string; Name: string }[]
}

// ---------------------------------------------------------------------------
// 4) 插件
// ---------------------------------------------------------------------------

export interface PluginItem {
  Name: string
  Version: string
  Enabled: boolean
  /** 含 guiMenu / commands → Web 端不可用。 */
  GuiOnly: boolean
  Uploader: string
  Transformer: string
  Description: string
  Author: string
  Homepage: string
}

export interface PluginsListData {
  Plugins: PluginItem[]
  /** 已安装但被禁用的插件名（`picgoPlugins[name] === false`）。 */
  Disabled: string[]
}

export interface PluginReadmeData {
  Content: string
  Path: string
}

export interface PluginToggleRequest {
  Enabled: boolean
}

export interface PluginOpRequest {
  Names: string[]
}

export interface PluginOpData {
  JobUID: string
  /** 安装/卸载成功后 agent 会重启自身；提示调用方。 */
  RestartPending: boolean
  Message: string
}

// ---------------------------------------------------------------------------
// 5) 上传
// ---------------------------------------------------------------------------

export interface UploadRequest {
  /** 本地文件绝对路径。 */
  Path: string
  /** 省略则用当前激活配置。 */
  Uploader?: { Type: string; ConfigName?: string }
  /** 仅用于事件归属与日志。 */
  JobUID?: string
  Seq?: number
  /** 可选：直接用这个名字（覆盖模板结果）。 */
  FileName?: string
  /** 可选：魔法路径模板（D43/D70）。 */
  PathTemplate?: string
  /** 可选：魔法文件名模板（D43/D70）。 */
  FileTemplate?: string
  /** 上传者 UID，供 `{uid}` 变量使用。 */
  UserUID?: string
  /** 驱动是否支持自定义路径（来自 Go 侧缓存的 Capabilities）；假时路径降级为文件名前缀。 */
  SupportsPathTemplate?: boolean
}

export interface UploadData {
  Seq: number
  URL: string
  ThumbURL: string
  FileName: string
  Extname: string
  Width: number
  Height: number
  Size: number
  ContentType: string
  UploaderType: string
  /**
   * 完整 `IImgInfo`（**字段名是 picgo 的原生形态**），含插件回写字段。
   *
   * Go 侧必须原样存入 `UploadResults.RawOutput`，否则删远端时字段会丢（D47）。
   */
  Raw: RawImgInfo | null
}

export interface UploadErrorData {
  Seq: number
  Error: string
  Raw: null
}

// ---------------------------------------------------------------------------
// 6) 远端删除
// ---------------------------------------------------------------------------

export interface RemoveRequest {
  /** 驱动类型，用于日志。 */
  UploaderType?: string
  /** 上传时的完整 `IImgInfo` 数组（原样，可能多个）。 */
  Items: RawImgInfo[]
}

export interface RemoveData {
  /** 是否真的删掉了远端文件。 */
  RemoteDeleted: boolean
  /** 该驱动是否支持远端删除（false 时 Go 侧只删本地记录）。 */
  Supported: boolean
  Message: string
}

// ---------------------------------------------------------------------------
// 7) 任务、事件与日志
// ---------------------------------------------------------------------------

export type JobStatus = 'queued' | 'running' | 'succeeded' | 'failed'

export interface JobItem {
  UID: string
  Kind: string
  Status: JobStatus
  Progress: number
  Payload: Record<string, unknown>
  Result: Record<string, unknown> | null
  Error: string
  CreatedAt: number
  StartedAt: number
  FinishedAt: number
}

export interface JobListData {
  Jobs: JobItem[]
}

export interface JobsClearData {
  Deleted: number
}

export interface LogsTailData {
  Path: string
  Lines: string[]
  Total: number
}
