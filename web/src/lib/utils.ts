import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

/**
 * 合并 className：clsx 处理条件，tailwind-merge 消除冲突的 Tailwind 类。
 * 所有 `components/ui/*` 的统一入口（shadcn 风格）。
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs))
}

/** 把数组按 key 去重（保留首次出现的元素）。 */
export function uniqBy<T, K>(items: T[], keyOf: (item: T) => K): T[] {
  const seen = new Set<K>()
  const out: T[] = []
  for (const item of items) {
    const key = keyOf(item)
    if (seen.has(key)) continue
    seen.add(key)
    out.push(item)
  }
  return out
}

/** 从数组中移除指定值（返回新数组）。 */
export function without<T>(items: T[], value: T): T[] {
  return items.filter((item) => item !== value)
}

/** 切换数组成员（存在则移除，不存在则追加）。 */
export function toggle<T>(items: T[], value: T): T[] {
  return items.includes(value) ? without(items, value) : [...items, value]
}

/**
 * 仅包含真值的对象（用于构造可选请求体，避免提交 `undefined`）。
 */
export function compact<T extends Record<string, unknown>>(obj: T): Partial<T> {
  const out: Partial<T> = {}
  for (const [k, v] of Object.entries(obj)) {
    if (v !== undefined && v !== null && v !== '') {
      out[k as keyof T] = v as T[keyof T]
    }
  }
  return out
}
