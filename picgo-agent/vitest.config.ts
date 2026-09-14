import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    globals: false,
    environment: 'node',
    include: ['src/**/*.test.ts'],
    // agent 的部分测试会起本地 HTTP 服务与临时目录，给足超时
    testTimeout: 20000,
    hookTimeout: 20000
  }
})
