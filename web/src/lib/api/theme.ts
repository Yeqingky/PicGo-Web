import { del, get, post, put } from '@/lib/http'
import type {
  ThemeActivateResponse,
  ThemeInstallResponse,
  ThemeListResponse,
  ThemeSettingsResponse,
  UpdateThemeSettingsRequest,
} from '@/types/api'

/**
 * 主题（API.md §10）。**全部端点需要 admin**。
 *
 * 安全边界（D94.2 / DESIGN.md §5.8.4，**不是 bug，也不可配置**）：
 *  - **主题 = 服务器上的任意前端代码**（影响它接管的页面）→ 只安装可信主题
 *  - **认证页（`/login` 等）与 `/admin/**` 由系统内置、永久保留**，
 *    主题无法接管（manifest 声明了也会被后端校验拒绝）
 *  - 主题资源走 `/theme-assets/**`，内置 SPA 资源走 `/assets/**`，互不干扰
 *  - 主题缺失 / 损坏时首页回退**内置默认主题**（永不白屏）
 */
export const themeApi = {
  /** 列出**扫描文件系统**得到的全部主题（D94：不建表，文件系统即真相源）。 */
  list(): Promise<ThemeListResponse> {
    return get<ThemeListResponse>('/themes')
  },

  /** 重新扫描（用于「手动把目录放进 `data/themes/`」的情形）。 */
  rescan(): Promise<ThemeListResponse> {
    return post<ThemeListResponse>('/themes/rescan')
  },

  /**
   * zip 上传安装（D96）。
   *
   * 后端会做全套安全校验（防 Zip Slip / 拒绝符号链接 / 体积与文件数上限 /
   * manifest 校验 / 冲突检测 / 原子性），**失败时返回具体原因**。
   */
  install(file: File, overwrite = false): Promise<ThemeInstallResponse> {
    const form = new FormData()
    form.append('File', file)
    form.append('Overwrite', String(overwrite))

    return post<ThemeInstallResponse>('/themes/install', form, {
      headers: { 'Content-Type': 'multipart/form-data' },
      timeout: 120_000,
    })
  },

  /** 卸载（当前启用中 / `default` 会被拒绝，前端应提前禁用按钮）。 */
  remove(themeID: string): Promise<null> {
    return del<null>(`/themes/${encodeURIComponent(themeID)}`)
  },

  /** 切换当前主题（**立即生效**，无需重启；已打开的页面需刷新）。 */
  activate(themeID: string): Promise<ThemeActivateResponse> {
    return put<ThemeActivateResponse>('/themes/active', { ThemeID: themeID })
  },

  /** 取某主题的设置（`Schema` + `Values`，供 §7 的渲染器渲染）。 */
  settings(themeID: string): Promise<ThemeSettingsResponse> {
    return get<ThemeSettingsResponse>(`/themes/${encodeURIComponent(themeID)}/settings`)
  },

  /** 保存主题设置（**只提交该主题声明过的键**，否则后端返 40001）。 */
  updateSettings(themeID: string, payload: UpdateThemeSettingsRequest): Promise<{ Updated: number }> {
    return put<{ Updated: number }>(
      `/themes/${encodeURIComponent(themeID)}/settings`,
      payload,
    )
  },

  /** 清理该主题的全部配置值（**不允许**对当前启用主题操作）。 */
  clearSettings(themeID: string): Promise<null> {
    return del<null>(`/themes/${encodeURIComponent(themeID)}/settings`)
  },

  /** 预览图 URL（不存在时后端返 40401，前端用 `onError` 兜底）。 */
  screenshotUrl(themeID: string): string {
    return `/api/web/v1/themes/${encodeURIComponent(themeID)}/screenshot`
  },
}
