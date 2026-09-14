import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import { App } from '@/app'
import { http } from '@/lib/http'
import { installMockAdapter } from '@/mocks'
import { applyThemeToDocument, useUIStore } from '@/store/ui-store'

import '@/styles/globals.css'

/**
 * 入口。
 *
 * 启动顺序：
 *  1. 应用持久化的主题（`index.html` 的内联脚本已先设过一次，这里是兜底与再同步）
 *  2. 按需安装 mock adapter（`VITE_USE_MOCK=true` 且 DEV 时才生效）
 *  3. 挂载 React
 */
applyThemeToDocument(useUIStore.getState().theme)
installMockAdapter(http)

const container = document.getElementById('root')
if (!container) {
  throw new Error('找不到 #root 容器：index.html 可能被改坏了')
}

createRoot(container).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
