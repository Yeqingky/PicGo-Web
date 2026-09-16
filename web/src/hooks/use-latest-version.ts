import { useEffect, useState } from 'react'

/** GitHub 仓库的最新正式 Release 页面。 */
export const PICGO_WEB_RELEASE_URL = 'https://github.com/YeqingKy/PicGo-Web/releases/latest'

const LATEST_RELEASE_API = 'https://api.github.com/repos/YeqingKy/PicGo-Web/releases/latest'
const REQUEST_TIMEOUT_MS = 5000

type GitHubLatestRelease = {
  tag_name?: unknown
}

interface ParsedVersion {
  major: number
  feature: number
  patch: number
  fix: number | null
}

function versionParts(value: string | undefined): ParsedVersion | null {
  const normalized = value?.trim().replace(/^v/i, '') ?? ''
  const match = normalized.match(/^(\d+)\.(\d+)\.(\d+)(?:-fix(\d+))?$/i)
  if (!match) return null
  return {
    major: Number(match[1]),
    feature: Number(match[2]),
    patch: Number(match[3]),
    fix: match[4] === undefined ? null : Number(match[4]),
  }
}

/**
 * Compare `major.feature.patch` and optional `-fixN` versions.
 * A fix release is newer than the same base version; `fix2` is newer than `fix1`.
 */
export function compareVersions(left: string | undefined, right: string | undefined): number {
  const a = versionParts(left)
  const b = versionParts(right)
  if (!a || !b) return 0

  for (const key of ['major', 'feature', 'patch'] as const) {
    if (a[key] !== b[key]) return a[key] > b[key] ? 1 : -1
  }

  if (a.fix === b.fix) return 0
  if (a.fix === null) return -1
  if (b.fix === null) return 1
  return a.fix > b.fix ? 1 : -1
}

interface LatestVersionState {
  latestVersion: string
  updateAvailable: boolean
}

/**
 * 检查 GitHub 最新正式 Release。
 *
 * 失败、没有 Release、版本格式无法识别或远端版本不高于当前版本时，均不提示更新。
 */
export function useLatestVersion(currentVersion?: string): LatestVersionState {
  const [latestVersion, setLatestVersion] = useState('')

  useEffect(() => {
    const current = versionParts(currentVersion)
    if (!current) {
      setLatestVersion('')
      return
    }

    const controller = new AbortController()
    const timeout = window.setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS)

    fetch(LATEST_RELEASE_API, {
      headers: { Accept: 'application/vnd.github+json' },
      cache: 'no-store',
      signal: controller.signal,
    })
      .then((response) => {
        if (!response.ok) throw new Error(`GitHub release request failed: ${response.status}`)
        return response.json() as Promise<GitHubLatestRelease>
      })
      .then((release) => {
        if (controller.signal.aborted) return
        const tag = typeof release.tag_name === 'string' ? release.tag_name : ''
        setLatestVersion(versionParts(tag) ? tag : '')
      })
      .catch(() => {
        if (!controller.signal.aborted) setLatestVersion('')
      })
      .finally(() => window.clearTimeout(timeout))

    return () => {
      window.clearTimeout(timeout)
      controller.abort()
    }
  }, [currentVersion])

  return {
    latestVersion,
    updateAvailable: compareVersions(latestVersion, currentVersion) > 0,
  }
}
