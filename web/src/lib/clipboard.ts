/**
 * 外链格式化（D68）。
 *
 * 图库与详情页提供三种格式的复制：
 *   - `markdown` → `![name](url)`
 *   - `url`      → `url`
 *   - `html`     → `<img src="url" alt="name" />`
 *
 * ⚠️ 这里**不做任何 HTML 转义以外的处理**，也不依赖图床差异（D66）。
 *    Lsky 兼容层额外提供 bbcode 等格式（见 `LskyUploadResult.links`），
 *    但那属于第三方契约，内置 SPA 不消费。
 */

import { t } from '@/i18n'

export type LinkFormat = 'markdown' | 'url' | 'html'

export const LINK_FORMATS: LinkFormat[] = ['markdown', 'url', 'html']

export interface LinkFormatInput {
  URL: string
  /** 展示用文件名；缺省回退到 URL 的最后一段 */
  Name?: string
}

/** 把文件名里的 HTML 特殊字符转义（用于 alt / title 属性）。 */
function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
}

/** Markdown 的 `[]()` 里需要转义方括号与反斜杠。 */
function escapeMarkdownText(value: string): string {
  return value.replace(/([\\[\]])/g, '\\$1')
}

/** 从 URL 推断文件名（用于 Name 缺省）。 */
export function fileNameFromUrl(url: string): string {
  try {
    const parsed = new URL(url, window.location.origin)
    const last = parsed.pathname.split('/').filter(Boolean).pop()
    return last ? decodeURIComponent(last) : url
  } catch {
    const last = url.split('/').filter(Boolean).pop()
    return last ?? url
  }
}

/** 格式的展示名（i18n key）。 */
export function linkFormatLabel(format: LinkFormat): string {
  switch (format) {
    case 'markdown':
      return t('LINK_FORMAT_MARKDOWN')
    case 'html':
      return t('LINK_FORMAT_HTML')
    case 'url':
    default:
      return t('LINK_FORMAT_URL')
  }
}

/** 按指定格式生成外链文本。 */
export function formatLink(input: LinkFormatInput, format: LinkFormat): string {
  const name = input.Name?.trim() || fileNameFromUrl(input.URL)
  const url = input.URL

  switch (format) {
    case 'markdown':
      return `![${escapeMarkdownText(name)}](${url})`
    case 'html':
      return `<img src="${escapeHtml(url)}" alt="${escapeHtml(name)}" />`
    case 'url':
    default:
      return url
  }
}

/** 批量格式化（多选复制时用换行连接）。 */
export function formatLinks(inputs: LinkFormatInput[], format: LinkFormat): string {
  return inputs.map((item) => formatLink(item, format)).join('\n')
}

/**
 * 复制文本到剪贴板。
 *
 * 优先用 `navigator.clipboard`（需要安全上下文）；
 * 非 HTTPS 或权限被拒时回退到 `document.execCommand` 的老办法，
 * 保证在内网 HTTP 部署下也能复制。
 */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // 落到下面的兜底
  }

  try {
    const textarea = document.createElement('textarea')
    textarea.value = text
    textarea.setAttribute('readonly', '')
    textarea.style.position = 'fixed'
    textarea.style.top = '-9999px'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(textarea)
    return ok
  } catch {
    return false
  }
}
