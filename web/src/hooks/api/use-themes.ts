import { useCallback, useState } from 'react'

import { useAsync } from '@/hooks/api/use-async'
import { themeApi } from '@/lib/api'
import type { ThemeListItem, ThemeSettingsResponse } from '@/types/api'

/**
 * 主题（API.md §10，admin）。
 *
 * ⚠️ 安全边界（D94.2 / DESIGN.md §5.8.4）：
 *  - **主题 = 服务器上的任意前端代码** → 只安装可信主题
 *  - **认证页（`/login` 等）与 `/admin/**` 由系统内置、永久保留**，主题**无法接管**
 *  - 主题缺失 / 损坏时首页回退内置默认主题（**永不白屏**）
 */

export function useThemes() {
  const state = useAsync(() => themeApi.list(), [])
  const themes: ThemeListItem[] = state.data?.Items ?? []

  return {
    themes,
    active: state.data?.Active ?? '',
    scannedAt: state.data?.ScannedAt ?? 0,
    loading: state.loading,
    error: state.error,
    refresh: state.refresh,
    /** 重新扫描（用于「手动把目录放进 data/themes/」的情形） */
    rescan: useCallback(async () => {
      const result = await themeApi.rescan()
      state.refresh()
      return result
    }, [state]),
  }
}

/**
 * 某主题的设置（`Schema` + `Values`），供 §7 的渲染器渲染。
 *
 * `skip` 为真时不请求（抽屉未打开时）。
 */
export function useThemeSettings(themeID: string, options: { skip?: boolean } = {}) {
  const { skip = true } = options

  const state = useAsync(() => themeApi.settings(themeID), [themeID], {
    skip: skip || !themeID,
    immediate: !skip,
  })

  /** 把 `Values` 拍平成 `{ [Key]: Value }`，供表单受控使用。 */
  const values = useCallback((data: ThemeSettingsResponse | undefined) => {
    const out: Record<string, unknown> = {}
    if (!data) return out
    for (const [key, entry] of Object.entries(data.Values ?? {})) {
      out[key] = entry.Value
    }
    return out
  }, [])

  return {
    settings: state.data as ThemeSettingsResponse | undefined,
    values: values(state.data),
    loading: state.loading,
    error: state.error,
    refresh: state.refresh,
  }
}

/**
 * 主题上传对话框的本地状态（文件 + 覆盖开关 + 提交中 + 错误）。
 *
 * 放在 hook 里而不是组件里，是为了让「失败时逐条展示校验原因」这件事
 * 有稳定的归属（错误来自后端的 `Message`，可能含多条校验说明）。
 */
export function useThemeInstall() {
  const [file, setFile] = useState<File | null>(null)
  const [overwrite, setOverwrite] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const submit = useCallback(async () => {
    if (!file) {
      setError('请先选择 zip 文件')
      return null
    }
    setSubmitting(true)
    setError('')
    try {
      const result = await themeApi.install(file, overwrite)
      return result
    } catch (err) {
      // 后端会给出具体的校验原因（Zip Slip / 超限 / ID 冲突 / 缺 index.html / Pages 非法）
      setError(err instanceof Error ? err.message : String(err))
      return null
    } finally {
      setSubmitting(false)
    }
  }, [file, overwrite])

  const reset = useCallback(() => {
    setFile(null)
    setOverwrite(false)
    setError('')
    setSubmitting(false)
  }, [])

  return { file, setFile, overwrite, setOverwrite, submitting, error, setError, submit, reset }
}
