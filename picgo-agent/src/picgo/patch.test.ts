import { describe, it, expect } from 'vitest'
import { detectPatches, patchStatus, patchesComplete } from './patch.js'

describe('picgo-core 补丁探测', () => {
  it('探测结果带包名与版本（便于排查「装错包」）', () => {
    const s = detectPatches()
    expect(typeof s.PackageName).toBe('string')
    expect(s.PackageName).not.toBe('')
    expect(typeof s.PackageVersion).toBe('string')
    expect(s.PackageVersion).not.toBe('')
  })

  it('当前依赖（@yeqingky/picgo-core）应包含全部补丁', () => {
    // 若这条失败，说明 package.json 被改成了上游原版 picgo，
    // 或装了没有补丁的版本 —— 并发上传会静默传错图床（见 patch.ts 注释）。
    const s = detectPatches()
    expect(s.PackageName).toBe('@yeqingky/picgo-core')
    expect(s.UploaderTarget, '缺少 UploadOptions.uploader 支持').toBe(true)
    expect(s.ContextData, '缺少 UploadOptions.contextData 支持').toBe(true)
    expect(s.Error).toBeUndefined()
    expect(patchesComplete()).toBe(true)
  })

  it('patchStatus 结果被缓存（多次调用同一对象）', () => {
    expect(patchStatus()).toBe(patchStatus())
  })

  it('缺补丁时 Error 会说明缺什么与后果', () => {
    const s = detectPatches()
    if (!s.Error) {
      // 当前依赖有补丁 → 跳过
      expect(s.Error).toBeUndefined()
      return
    }
    expect(s.Error).toContain('缺少')
    expect(s.Error).toContain('@yeqingky/picgo-core')
  })
})
