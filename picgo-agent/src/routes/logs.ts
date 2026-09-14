/**
 * `GET /api/logs?tail=200` —— 读 `picgo.log` 尾部（docs/API.md §13.6）。
 *
 * **只读、不解析**：picgo 自己写这个文件（`<baseDir>/picgo.log`），
 * 我们只把最后 N 行原样交给 Go（用于「插件安装过程」与「上传失败原因」的可视化）。
 *
 * 实现上刻意用「读整个文件再切尾部」而不是流式倒读：
 * picgo.log 由 `log.rotate` 轮转，单文件不会无限增长（默认几 MB），
 * 而倒读的边界处理（多字节字符、换行）容易出错。
 */

import fs from 'node:fs'
import path from 'node:path'
import { Hono } from 'hono'
import type { AppContext } from '../context.js'
import type { LogsTailData } from '../types.js'
import { errParam, ok } from '../http/envelope.js'

const DEFAULT_TAIL = 200
const MAX_TAIL = 5000
/** 单次最多读入的字节数（防止超大日志把内存打满）。 */
const MAX_READ_BYTES = 2 * 1024 * 1024

function parseTail(raw: string | undefined): number {
  if (raw === undefined || raw.trim() === '') return DEFAULT_TAIL
  const n = Number.parseInt(raw.trim(), 10)
  if (!Number.isFinite(n) || n <= 0) return DEFAULT_TAIL
  return Math.min(n, MAX_TAIL)
}

export function logRoutes(ctx: AppContext): Hono {
  const app = new Hono()

  app.get('/api/logs', (c) => {
    const tail = parseTail(c.req.query('tail'))

    const logPath = path.join(ctx.baseDir, 'picgo.log')

    let content = ''
    let truncatedAtStart = false
    try {
      if (!fs.existsSync(logPath)) {
        const data: LogsTailData = { Path: logPath, Lines: [], Total: 0 }
        return ok(c, data, '日志文件尚不存在')
      }

      const stat = fs.statSync(logPath)
      const size = stat.size
      if (size > MAX_READ_BYTES) {
        truncatedAtStart = true
        const fd = fs.openSync(logPath, 'r')
        try {
          const buf = Buffer.alloc(MAX_READ_BYTES)
          fs.readSync(fd, buf, 0, MAX_READ_BYTES, size - MAX_READ_BYTES)
          content = buf.toString('utf8')
        } finally {
          fs.closeSync(fd)
        }
      } else {
        content = fs.readFileSync(logPath, 'utf8')
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error)
      return errParam(c, `读取日志失败：${message}`)
    }

    const allLines = content.split(/\r?\n/)

    // 文件被截断读取时，第一行很可能是半行（被从中间切开），丢掉
    if (truncatedAtStart && allLines.length > 0) allLines.shift()

    // 去掉末尾空行
    while (allLines.length > 0 && allLines[allLines.length - 1] === '') allLines.pop()

    const total = allLines.length
    const lines = total <= tail ? allLines : allLines.slice(total - tail)

    const data: LogsTailData = { Path: logPath, Lines: lines, Total: total }
    return ok(c, data)
  })

  return app
}
