import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// 内置 SPA 的构建配置（D94 / DESIGN.md §14.1）
//
// 两条必须遵守的边界（D99.2）：
//  1. base 必须是 "/"，产物通过 /assets/** 提供（由 go:embed web/dist 打进二进制）
//     —— 主题的资源前缀是 /theme-assets/**，两者不得混用
//  2. dev server 把 /api 代理到 Go 后端（同源，避免 CORS；Cookie 也能正常下发）
export default defineConfig({
  base: '/',
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      // 内部 API + Lsky 兼容层 + 健康检查都在 /api 与 /healthz 下
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
      '/healthz': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    // 产物文件名带内容哈希（便于 Go 侧设 immutable 长缓存，见 API.md §10.1）
    sourcemap: false,
    chunkSizeWarningLimit: 1200,
    rollupOptions: {
      output: {
        // 按「很少变动」的第三方库拆包：升级业务代码时这些 chunk 的哈希不变，
        // 用户不必重新下载（配合 /assets/** 的 immutable 缓存）
        manualChunks: {
          'vendor-react': ['react', 'react-dom', 'react-router'],
          'vendor-radix': [
            '@radix-ui/react-accordion',
            '@radix-ui/react-alert-dialog',
            '@radix-ui/react-avatar',
            '@radix-ui/react-checkbox',
            '@radix-ui/react-dialog',
            '@radix-ui/react-dropdown-menu',
            '@radix-ui/react-label',
            '@radix-ui/react-popover',
            '@radix-ui/react-progress',
            '@radix-ui/react-scroll-area',
            '@radix-ui/react-select',
            '@radix-ui/react-separator',
            '@radix-ui/react-slot',
            '@radix-ui/react-switch',
            '@radix-ui/react-tabs',
            '@radix-ui/react-tooltip',
          ],
          'vendor-misc': ['axios', 'zustand', 'sonner', 'lucide-react'],
        },
      },
    },
  },
})
