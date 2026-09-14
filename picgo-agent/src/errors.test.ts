/**
 * 错误信息提取的单测。
 *
 * 回归背景：picgo 的驱动（与底层 axios）**不总是抛 Error 实例**，
 * 早期实现用 `String(err)` 会得到 `"[object Object]"`，把失败原因彻底丢掉。
 */

import { describe, expect, it } from 'vitest'
import { describeError } from './errors.js'

describe('describeError', () => {
  it('Error → 取 message', () => {
    expect(describeError(new Error('boom'))).toBe('boom')
  })

  it('字符串 → 原样', () => {
    expect(describeError('plain error')).toBe('plain error')
  })

  it('数字 / 布尔 → 转字符串', () => {
    expect(describeError(42)).toBe('42')
    expect(describeError(false)).toBe('false')
  })

  it('null / undefined → 空串', () => {
    expect(describeError(null)).toBe('')
    expect(describeError(undefined)).toBe('')
  })

  it('**关键回归**：普通对象不得变成 [object Object]', () => {
    const result = describeError({ statusCode: 422, body: { message: '仓库不存在' } })
    expect(result).not.toBe('[object Object]')
    expect(result).toContain('仓库不存在')
  })

  it('常见 message 字段优先', () => {
    expect(describeError({ message: 'M' })).toBe('M')
    expect(describeError({ msg: 'MSG' })).toBe('MSG')
    expect(describeError({ error: 'ERR' })).toBe('ERR')
    expect(describeError({ detail: 'D' })).toBe('D')
    expect(describeError({ reason: 'R' })).toBe('R')
  })

  it('axios 形态：response.data.message（带状态码前缀）', () => {
    const r = describeError({ response: { status: 403, data: { message: '权限不足' } } })
    expect(r).toBe('HTTP 403: 权限不足')
  })

  it('axios 形态：只有 statusText', () => {
    expect(describeError({ response: { status: 500, statusText: 'Internal Server Error' } })).toBe(
      '500 Internal Server Error'
    )
  })

  it('picgo 驱动形态：{ statusCode, body }（字符串 body）', () => {
    expect(describeError({ statusCode: 400, body: 'bad request' })).toContain('bad request')
  })

  it('picgo 驱动形态：{ statusCode, body } 优先取 body 里的 message 类字段', () => {
    // 实测 tcyun / github 这类驱动会 throw { statusCode, body }
    const r = describeError({ statusCode: 422, body: { message: '仓库不存在' } })
    expect(r).toBe('HTTP 422: 仓库不存在')
  })

  it('picgo 驱动形态：body 里没有 message 类字段时给 JSON 摘要', () => {
    const r = describeError({ statusCode: 422, body: { code: 'X', detail2: 'y' } })
    expect(r).toContain('422')
    expect(r).toContain('code')
  })

  it('完全无法识别时 JSON 序列化（仍比 [object Object] 有用）', () => {
    const r = describeError({ weird: { nested: true } })
    expect(r).not.toBe('[object Object]')
    expect(r).toContain('weird')
  })

  it('**关键回归**：循环引用不抛错，且绝不返回 [object Object]', () => {
    const cycle: Record<string, unknown> = { a: 1 }
    cycle.self = cycle
    expect(() => describeError(cycle)).not.toThrow()
    const r = describeError(cycle)
    expect(r).not.toBe('[object Object]')
    expect(r).not.toContain('[object Object]')
    expect(r).toContain('[Circular]')
  })

  it('超长信息被截断（避免把巨量响应体灌进日志与前端）', () => {
    const huge = 'x'.repeat(5000)
    const r = describeError(new Error(huge))
    expect(r.length).toBeLessThanOrEqual(501) // 500 + 省略号
    expect(r.endsWith('…')).toBe(true)
  })

  it('多行信息被压成单行', () => {
    expect(describeError(new Error('line1\nline2\r\nline3'))).toBe('line1 line2 line3')
  })

  it('空 message 的 Error 回退到 name', () => {
    const e = new Error('')
    e.name = 'CustomError'
    expect(describeError(e)).toBe('CustomError')
  })
})
