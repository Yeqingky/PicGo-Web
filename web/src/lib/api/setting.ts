import { get, post, put } from '@/lib/http'
import type {
  MailTestRequest,
  MailTestResponse,
  SettingsResponse,
  SystemInfo,
  SystemSettingsResponse,
  SystemStats,
  UpdateSystemSettingsResponse,
  UpdateUserSettingsResponse,
} from '@/types/api'

/**
 * 设置（API.md §11）。
 *
 * 配置三级分层（D18）：
 *  1. 环境变量 / `.env`（启动引导类，只读，前端改不了）
 *  2. **数据库 `SystemSettings`**（运行时可在后台改）← 本站点设置页操作的就是它
 *  3. 代码默认值
 *
 * ⚠️ 配置键名保持 `dot.lowerCamel` **原样**（D81.3 第 3 条）：
 * 它们是 KV 表的字符串 key，不是列名。响应体的**外层**字段才用 PascalCase。
 */
export const settingsApi = {
  /** 站点公开信息 + 当前用户的用户级设置（需登录）。 */
  get(): Promise<SettingsResponse> {
    return get<SettingsResponse>('/settings')
  },

  /** 更新**用户级**设置（落 `UserSettings` 表；只允许 `user.*` / `ui.*` 前缀）。 */
  updateUser(payload: Record<string, unknown>): Promise<UpdateUserSettingsResponse> {
    return put<UpdateUserSettingsResponse>('/settings', payload)
  },

  /**
   * 全部系统键位 + 元信息（admin）。
   *
   * 返回按 `Category` 分组；`secret` 类型的 `Value` 恒为掩码，
   * 另有 `HasValue` 表示是否已设置（**不泄露值**）。
   */
  getSystem(): Promise<SystemSettingsResponse> {
    return get<SystemSettingsResponse>('/settings/system')
  },

  /** 更新系统设置（admin）。`payload` 的键是配置键（`dot.lowerCamel`）。 */
  updateSystem(payload: Record<string, unknown>): Promise<UpdateSystemSettingsResponse> {
    return put<UpdateSystemSettingsResponse>('/settings/system', payload)
  },

  /**
   * 发送测试邮件（admin，站点设置 → 邮件）。
   *
   * 结果同时会写一条 `EmailLogs` 与 `OperationLogs:mail.send`（后端负责）。
   */
  testMail(payload: MailTestRequest): Promise<MailTestResponse> {
    return post<MailTestResponse>('/settings/mail/test', payload)
  },
}

/** 系统信息与统计（API.md §10）。 */
export const systemApi = {
  info(): Promise<SystemInfo> {
    return get<SystemInfo>('/system/info')
  },

  /** 统计面板数据（admin）。 */
  stats(): Promise<SystemStats> {
    return get<SystemStats>('/system/stats')
  },
}
