import { createBrowserRouter, Navigate } from 'react-router'

import { AppShell } from '@/components/layout/app-shell'
import { ForbiddenPage, NotFoundPage } from '@/components/layout/error-pages'
import { RequireAdmin, RequireAnonymous, RequireAuth } from '@/components/layout/route-guards'
import { AdminLogsPage } from '@/features/admin/logs/admin-logs-page'
import { AdminPluginsPage } from '@/features/admin/plugins/admin-plugins-page'
import { AdminSitePage } from '@/features/admin/site/admin-site-page'
import { AdminStoragePage } from '@/features/admin/storage/admin-storage-page'
import { AdminThemesPage } from '@/features/admin/themes/admin-themes-page'
import { AdminUsersPage } from '@/features/admin/users/admin-users-page'
import { AlbumsPage } from '@/features/albums/albums-page'
import { FirstLoginPage } from '@/features/auth/first-login-page'
import { ForgotPasswordPage } from '@/features/auth/forgot-password-page'
import { LoginPage } from '@/features/auth/login-page'
import { ResetPasswordPage } from '@/features/auth/reset-password-page'
import { GalleryDetailPage } from '@/features/gallery/gallery-detail-page'
import { GalleryPage } from '@/features/gallery/gallery-page'
import { HomePage } from '@/features/home/home-page'
import { JobsPage } from '@/features/jobs/jobs-page'
import { LogsPage } from '@/features/logs/logs-page'
import { SettingsPage } from '@/features/settings/settings-page'
import { UploadPage } from '@/features/upload/upload-page'

/**
 * 路由表（DESIGN.md §3）。
 *
 * 分组：
 *  - **公开**：`/login`、`/forgot-password`、`/reset-password`
 *  - **需登录**：AppShell 内的页面
 *  - **需管理员**：`/admin/**`
 *
 * 守卫语义（DESIGN.md §3 / §8）：
 *  - `RequireAuth`：未登录 → `/login?redirect=<原地址>`；`MustChangePassword` → `/first-login`
 *  - `RequireAdmin`：非 admin → 渲染 403（**不跳转**，避免用户困惑）
 *  - `RequireAnonymous`：已登录访问登录页 → 送回 `/`
 *
 * ⚠️ 生产环境中 `/` 由**当前主题**渲染（D94），下面的 `/` 路由只在
 *    Vite dev server 下可达 —— 见 `features/home/home-page.tsx` 的说明。
 */
export const router = createBrowserRouter([
  // ---- 公开（未登录可访问）----
  {
    element: <RequireAnonymous />,
    children: [
      { path: '/login', element: <LoginPage /> },
      { path: '/forgot-password', element: <ForgotPasswordPage /> },
      { path: '/reset-password', element: <ResetPasswordPage /> },
    ],
  },

  // ---- 首启强制改密（需登录，但需允许在 MustChangePassword 状态下访问）----
  { path: '/first-login', element: <FirstLoginPage /> },

  // ---- 需登录 ----
  {
    element: <RequireAuth />,
    children: [
      {
        element: <AppShell />,
        children: [
          { index: true, element: <HomePage /> },
          { path: 'upload', element: <UploadPage /> },
          { path: 'gallery', element: <GalleryPage /> },
          { path: 'gallery/:uid', element: <GalleryDetailPage /> },
          { path: 'albums', element: <AlbumsPage /> },
          { path: 'jobs', element: <JobsPage /> },
          { path: 'logs', element: <LogsPage /> },
          { path: 'settings', element: <SettingsPage /> },

          // ---- 需管理员 ----
          {
            path: 'admin',
            element: <RequireAdmin />,
            children: [
              { index: true, element: <Navigate to="/admin/users" replace /> },
              { path: 'users', element: <AdminUsersPage /> },
              { path: 'storage', element: <AdminStoragePage /> },
              { path: 'plugins', element: <AdminPluginsPage /> },
              { path: 'themes', element: <AdminThemesPage /> },
              { path: 'site', element: <AdminSitePage /> },
              { path: 'logs', element: <AdminLogsPage /> },
              // 直接访问 `/admin/xxx` 且非 admin 时由 RequireAdmin 渲染 403；
              // 是 admin 但子路径不存在则落到 404
              { path: '*', element: <NotFoundPage /> },
            ],
          },

          { path: '403', element: <ForbiddenPage /> },
          { path: '*', element: <NotFoundPage /> },
        ],
      },
    ],
  },
])
