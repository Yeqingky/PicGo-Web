import en from '@/i18n/locales/en.json'
import zhCN from '@/i18n/locales/zh-CN.json'

/**
 * 极简 i18n（D75 / DESIGN.md §13）。
 *
 * 设计取舍：
 *  - 只做「中文为源、英文预留」，**不引入 i18next**（内置 SPA 只有一个完整语言，代价不值）
 *  - key 用 `SCREAMING_SNAKE_CASE`
 *  - 后端返回的 `Message` 已是中文，**直接展示，不二次翻译**
 *  - 需要新增语言时：加 `locales/<lang>.json` 并登记到 `dictionaries`
 */

export type Locale = 'zh-CN' | 'en'

const dictionaries: Record<Locale, Record<string, string>> = {
  'zh-CN': zhCN as Record<string, string>,
  en: en as Record<string, string>,
}

/** 默认语言（DESIGN.md §13：中文为默认与唯一完整语言）。 */
const DEFAULT_LOCALE: Locale = 'zh-CN'

const STORAGE_KEY = 'picgo-web.ui.locale'

function isLocale(value: unknown): value is Locale {
  return value === 'zh-CN' || value === 'en'
}

function readLocale(): Locale {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (isLocale(stored)) return stored
  } catch {
    // localStorage 不可用 → 用默认语言
  }
  return DEFAULT_LOCALE
}

let currentLocale: Locale = readLocale()

/** 当前语言。 */
export function getLocale(): Locale {
  return currentLocale
}

/** 切换语言（仅影响 i18n key 的解析；后端 Message 不参与）。 */
export function setLocale(locale: Locale): void {
  currentLocale = locale
  try {
    localStorage.setItem(STORAGE_KEY, locale)
  } catch {
    // 忽略：写不进 localStorage 也应在本次会话内生效
  }
  if (typeof document !== 'undefined') {
    document.documentElement.lang = locale
  }
}

/** 可用语言列表（供设置页展示）。 */
export function availableLocales(): { value: Locale; label: string }[] {
  return [
    { value: 'zh-CN', label: '简体中文' },
    { value: 'en', label: 'English' },
  ]
}

/**
 * 取文案。
 *
 * 查找顺序：当前语言 → 默认语言 → **返回 key 本身**（便于一眼看出漏翻的 key，而不是留空白）。
 *
 * @param key   i18n key（SCREAMING_SNAKE_CASE）
 * @param vars  可选插值：`{ name: 'a.png' }` 会替换文案里的 `{{name}}`
 */
export function t(key: string, vars?: Record<string, string | number>): string {
  const dict = dictionaries[currentLocale]
  let text = dict[key] ?? dictionaries[DEFAULT_LOCALE][key] ?? key

  if (vars) {
    for (const [name, value] of Object.entries(vars)) {
      text = text.replaceAll(`{{${name}}}`, String(value))
    }
  }
  return text
}
