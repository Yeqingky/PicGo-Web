/**
 * 模板引擎与魔法命名的单测（D42–D44、D70）。
 */

import { describe, expect, it } from 'vitest'
import crypto from 'node:crypto'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import {
  buildFileName,
  expandTemplate,
  sanitizeRelativePath,
  uniqid
} from './template.js'

/** 固定时间：2026-02-14 09:30:15 UTC → 用本地时区会漂移，因此断言用 Date 的本地值。 */
const FIXED_NOW = new Date(2026, 1, 14, 9, 30, 15).getTime()

describe('expandTemplate', () => {
  it('展开日期与时间变量（补零）', () => {
    const r = expandTemplate('{Y}/{m}/{d}_{H}{i}{s}', {
      fileName: 'a.png',
      extname: '.png',
      now: FIXED_NOW
    })
    expect(r.value).toBe('2026/02/14_093015')
    expect(r.unknownVars).toEqual([])
  })

  it('展开 {timestamp}（Unix 秒）', () => {
    const r = expandTemplate('{timestamp}', { fileName: 'a.png', extname: '.png', now: FIXED_NOW })
    expect(r.value).toBe(String(Math.floor(FIXED_NOW / 1000)))
  })

  it('展开 {filename} 为不含扩展名的原名', () => {
    const r = expandTemplate('{filename}', { fileName: 'photo.jpeg', extname: '.jpeg' })
    expect(r.value).toBe('photo')
  })

  it('展开 {extname}', () => {
    const r = expandTemplate('x{extname}', { fileName: 'a.webp', extname: '.webp' })
    expect(r.value).toBe('x.webp')
  })

  it('展开 {uid}', () => {
    const r = expandTemplate('{uid}', { fileName: 'a.png', extname: '.png', userUID: 'usr_1' })
    expect(r.value).toBe('usr_1')
  })

  it('{uid} 缺失时展开为空串', () => {
    const r = expandTemplate('a{uid}b', { fileName: 'a.png', extname: '.png' })
    expect(r.value).toBe('ab')
  })

  it('未知变量原样保留并记录（D70：不报错、不阻断）', () => {
    const r = expandTemplate('{Y}-{nope}-{extname}', { fileName: 'a.png', extname: '.png', now: FIXED_NOW })
    expect(r.value).toBe('2026-{nope}-.png')
    expect(r.unknownVars).toEqual(['nope'])
  })

  it('多个未知变量全部记录（去重由调用方负责）', () => {
    const r = expandTemplate('{aa}{bb}{aa}', { fileName: 'a.png', extname: '.png' })
    expect(r.unknownVars).toEqual(['aa', 'bb', 'aa'])
  })

  it('{uniqid} 每次不同且为 8 位十六进制', () => {
    const a = expandTemplate('{uniqid}', { fileName: 'a.png', extname: '.png' }).value
    const b = expandTemplate('{uniqid}', { fileName: 'a.png', extname: '.png' }).value
    expect(a).toMatch(/^[0-9a-f]{8}$/)
    expect(b).toMatch(/^[0-9a-f]{8}$/)
    expect(a).not.toBe(b)
  })

  it('空模板返回空串', () => {
    expect(expandTemplate('', { fileName: 'a.png', extname: '.png' }).value).toBe('')
  })

  it('无花括号的模板原样返回', () => {
    expect(expandTemplate('static/name.png', { fileName: 'a.png', extname: '.png' }).value).toBe(
      'static/name.png'
    )
  })
})

describe('expandTemplate 的惰性哈希', () => {
  it('{md5} 与 {md5-8} 读文件内容', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'tpl-'))
    const file = path.join(dir, 'x.bin')
    const content = Buffer.from('hello picgo')
    fs.writeFileSync(file, content)

    const expected = crypto.createHash('md5').update(content).digest('hex')

    const full = expandTemplate('{md5}', { fileName: 'x.bin', extname: '.bin', filePath: file })
    expect(full.value).toBe(expected)

    const short = expandTemplate('{md5-8}', { fileName: 'x.bin', extname: '.bin', filePath: file })
    expect(short.value).toBe(expected.slice(0, 8))

    fs.rmSync(dir, { recursive: true, force: true })
  })

  it('{sha256-8} 读文件内容', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'tpl-'))
    const file = path.join(dir, 'x.bin')
    const content = Buffer.from('hello picgo')
    fs.writeFileSync(file, content)

    const expected = crypto.createHash('sha256').update(content).digest('hex').slice(0, 8)
    const r = expandTemplate('{sha256-8}', { fileName: 'x.bin', extname: '.bin', filePath: file })
    expect(r.value).toBe(expected)

    fs.rmSync(dir, { recursive: true, force: true })
  })

  it('没有 filePath 时哈希变量展开为空串（不抛错）', () => {
    const r = expandTemplate('{md5}', { fileName: 'x.bin', extname: '.bin' })
    expect(r.value).toBe('')
  })

  it('文件不存在时哈希变量展开为空串（不抛错）', () => {
    const r = expandTemplate('{md5}', {
      fileName: 'x.bin',
      extname: '.bin',
      filePath: '/definitely/not/here.bin'
    })
    expect(r.value).toBe('')
  })
})

describe('sanitizeRelativePath', () => {
  it('保留 / 作为路径分隔符', () => {
    expect(sanitizeRelativePath('img/2026/02')).toBe('img/2026/02')
  })

  it('替换段内的非法字符', () => {
    expect(sanitizeRelativePath('a:b*c?.png')).toBe('a_b_c_.png')
    expect(sanitizeRelativePath('a\\b.png')).toBe('a_b.png')
    expect(sanitizeRelativePath('a|b"c<d>e.png')).toBe('a_b_c_d_e.png')
  })

  it('丢弃空段与纯点段（. / .. / ...）', () => {
    expect(sanitizeRelativePath('a//b')).toBe('a/b')
    expect(sanitizeRelativePath('a/./b')).toBe('a/b')
    expect(sanitizeRelativePath('a/../b')).toBe('a/b')
    expect(sanitizeRelativePath('a/.../b')).toBe('a/b')
    expect(sanitizeRelativePath('/leading/slash')).toBe('leading/slash')
  })

  it('去掉控制字符', () => {
    expect(sanitizeRelativePath('a\u0001b\u001fc.png')).toBe('a_b_c.png')
  })

  it('全是非法/纯点段时返回空串', () => {
    expect(sanitizeRelativePath('///')).toBe('')
    expect(sanitizeRelativePath('...')).toBe('')
    expect(sanitizeRelativePath('./../.')).toBe('')
  })
})

describe('buildFileName', () => {
  it('两个模板都为空时回退为 {uniqid}{extname}', () => {
    const r = buildFileName({ fileName: 'a.png' })
    expect(r.fileName).toMatch(/^[0-9a-f]{8}\.png$/)
    expect(r.pathDowngraded).toBe(false)
  })

  it('只给文件名模板时按模板生成并补扩展名', () => {
    const r = buildFileName({
      fileName: 'photo.png',
      fileTemplate: 'x{timestamp}',
      now: FIXED_NOW
    })
    expect(r.fileName).toBe(`x${Math.floor(FIXED_NOW / 1000)}.png`)
  })

  it('模板已带扩展名时不重复追加', () => {
    const r = buildFileName({ fileName: 'photo.png', fileTemplate: 'fixed.png' })
    expect(r.fileName).toBe('fixed.png')
  })

  it('路径模板与文件名模板组合（支持子目录）', () => {
    const r = buildFileName({
      fileName: 'photo.png',
      pathTemplate: 'img/{Y}/{m}',
      fileTemplate: '{filename}{extname}',
      now: FIXED_NOW,
      supportsPathTemplate: true
    })
    expect(r.fileName).toBe('img/2026/02/photo.png')
    expect(r.pathDowngraded).toBe(false)
  })

  it('驱动不支持路径时降级为文件名前缀（不含斜杠）', () => {
    const r = buildFileName({
      fileName: 'photo.png',
      pathTemplate: 'img/{Y}/{m}',
      fileTemplate: '{filename}{extname}',
      now: FIXED_NOW,
      supportsPathTemplate: false
    })
    expect(r.fileName).toBe('img_2026_02_photo.png')
    expect(r.fileName).not.toContain('/')
    expect(r.pathDowngraded).toBe(true)
  })

  it('未知变量原样保留并上报', () => {
    const r = buildFileName({
      fileName: 'a.png',
      fileTemplate: 'x{whatever}{extname}'
    })
    expect(r.fileName).toBe('x{whatever}.png')
    expect(r.unknownVars).toContain('whatever')
  })

  it('文件名里的非法字符被替换', () => {
    const r = buildFileName({ fileName: 'a.png', fileTemplate: 'my:file?.png' })
    expect(r.fileName).toBe('my_file_.png')
  })

  it('清洗后为空时回退为随机名（极端兜底）', () => {
    const r = buildFileName({ fileName: 'a.png', fileTemplate: '...' })
    expect(r.fileName).toMatch(/^[0-9a-f]{8}\.png$/)
  })

  it('原始文件名没有扩展名时也能工作', () => {
    const r = buildFileName({ fileName: 'noext', fileTemplate: '{filename}-x{extname}' })
    expect(r.fileName).toBe('noext-x')
  })

  it('{md5-8} 与 {filename} 组合（端到端常见用法）', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'tpl-'))
    const file = path.join(dir, 'p.png')
    const content = Buffer.from('data')
    fs.writeFileSync(file, content)
    const md5 = crypto.createHash('md5').update(content).digest('hex').slice(0, 8)

    const r = buildFileName({
      fileName: 'p.png',
      fileTemplate: '{filename}-{md5-8}{extname}',
      filePath: file
    })
    expect(r.fileName).toBe(`p-${md5}.png`)

    fs.rmSync(dir, { recursive: true, force: true })
  })
})

describe('uniqid', () => {
  it('长度 8、十六进制、大量调用不重复', () => {
    const seen = new Set<string>()
    for (let i = 0; i < 5000; i += 1) {
      const id = uniqid()
      expect(id).toMatch(/^[0-9a-f]{8}$/)
      seen.add(id)
    }
    // 4 字节随机，5000 次碰撞概率极低
    expect(seen.size).toBe(5000)
  })
})
