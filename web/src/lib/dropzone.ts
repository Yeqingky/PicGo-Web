/**
 * 拖拽与粘贴的文件提取（DESIGN.md §9.2）。
 *
 * 单独成文件而不是放在组件里：这些是**纯函数**（可单测、可复用），
 * 放在组件文件里会破坏 React Fast Refresh 的「只导出组件」约定。
 */

/** 允许的图片扩展名（与后端 `upload.allowedExts` 默认值一致；后端仍会再校验一次）。 */
export const DEFAULT_ACCEPT =
  'image/jpeg,image/png,image/gif,image/webp,image/bmp,image/svg+xml,image/x-icon,image/avif'

/** 从 `DataTransfer` 里取文件（兼容 `items` 与 `files` 两种来源）。 */
export function filesFromDataTransfer(dt: DataTransfer | null): File[] {
  if (!dt) return []

  const out: File[] = []

  // 优先用 items（能拿到目录里的文件与剪贴板图片）
  if (dt.items && dt.items.length > 0) {
    for (const item of Array.from(dt.items)) {
      if (item.kind !== 'file') continue
      const file = item.getAsFile()
      if (file) out.push(file)
    }
  }

  // 回退到 files（某些浏览器/场景下 items 为空）
  if (out.length === 0 && dt.files) {
    out.push(...Array.from(dt.files))
  }

  return out
}

/** 从粘贴事件里取文件。 */
export function filesFromClipboard(event: ClipboardEvent): File[] {
  return filesFromDataTransfer(event.clipboardData)
}
