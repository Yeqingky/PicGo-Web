/**
 * 启动引导类配置（只读，来自环境变量）。
 *
 * 与 PicGo-Web 主服务的分层一致（docs/DECISIONS.md D18）：
 * 本文件只读「进程能否启动」级别的配置；业务配置由 Go 侧通过
 * `PATCH /api/config` 与 `POST /api/uploaders/configs` 推给 picgo。
 *
 * 命名例外（D81.3 第 2 条）：环境变量保持 UPPER_SNAKE_CASE；变量名本身用 camelCase。
 */

import path from 'node:path'

const DEFAULT_PORT = 36678
/** 必须只监听回环（D7）：agent 只对同机的 Go 服务开放。 */
const DEFAULT_HOST = '127.0.0.1'

function readString(key: string, fallback: string): string {
  const raw = process.env[key]
  if (raw === undefined) return fallback
  const trimmed = raw.trim()
  return trimmed === '' ? fallback : trimmed
}

function readInt(key: string, fallback: number): number {
  const raw = process.env[key]
  if (raw === undefined) return fallback
  const n = Number.parseInt(raw.trim(), 10)
  return Number.isFinite(n) && n > 0 ? n : fallback
}

function readBool(key: string, fallback: boolean): boolean {
  const raw = process.env[key]
  if (raw === undefined) return fallback
  switch (raw.trim().toLowerCase()) {
    case '1':
    case 'true':
    case 'yes':
    case 'on':
      return true
    case '0':
    case 'false':
    case 'no':
    case 'off':
      return false
    default:
      return fallback
  }
}

export interface AgentEnv {
  /** 监听端口，默认 36678（刻意避开 picgo-core 内置 server 的 36677）。 */
  Port: number
  /** 监听地址。**必须保持回环**，一般不改。 */
  Host: string
  /** 与 Go 之间的共享令牌（`X-Agent-Token`）。空字符串表示「不校验」——仅允许显式开发模式。 */
  Token: string
  /** picgo 的 config.json 路径。 */
  ConfigPath: string
  /** 允许无令牌访问（仅本地调试；绝不可用于部署）。 */
  AllowNoToken: boolean
  /** npm 源（插件安装用）。 */
  NpmRegistry: string
  /** npm 代理（插件安装用）。 */
  NpmProxy: string
  /** 上传时使用的代理。 */
  UploadProxy: string
  /** picgo config 的备份份数（写前轮转）。 */
  ConfigBackupCount: number
  /** 远端删除时等待插件响应的超时（毫秒）。 */
  RemoveTimeoutMs: number
  /** 日志级别。 */
  LogLevel: 'debug' | 'info' | 'warn' | 'error'
}

function defaultConfigPath(): string {
  // 默认落在仓库根的 data/picgo/config.json（与 Go 侧默认值一致）。
  // 由 Go 拉起时总会显式注入 PICGO_AGENT_CONFIG_PATH，这里是独立运行的兜底。
  return path.resolve(process.cwd(), 'data', 'picgo', 'config.json')
}

function readLogLevel(): AgentEnv['LogLevel'] {
  const raw = readString('PICGO_AGENT_LOG_LEVEL', 'info').toLowerCase()
  if (raw === 'debug' || raw === 'info' || raw === 'warn' || raw === 'error') return raw
  return 'info'
}

export function loadEnv(): AgentEnv {
  return {
    Port: readInt('PICGO_AGENT_PORT', DEFAULT_PORT),
    Host: readString('PICGO_AGENT_HOST', DEFAULT_HOST),
    Token: readString('PICGO_AGENT_TOKEN', ''),
    ConfigPath: path.resolve(readString('PICGO_AGENT_CONFIG_PATH', defaultConfigPath())),
    AllowNoToken: readBool('PICGO_AGENT_ALLOW_NO_TOKEN', false),
    NpmRegistry: readString('PICGO_AGENT_NPM_REGISTRY', ''),
    NpmProxy: readString('PICGO_AGENT_NPM_PROXY', ''),
    UploadProxy: readString('PICGO_AGENT_UPLOAD_PROXY', ''),
    ConfigBackupCount: readInt('PICGO_AGENT_CONFIG_BACKUP_COUNT', 5),
    RemoveTimeoutMs: readInt('PICGO_AGENT_REMOVE_TIMEOUT_MS', 3000),
    LogLevel: readLogLevel()
  }
}
