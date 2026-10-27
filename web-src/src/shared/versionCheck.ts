// 新版本提示（#262）：主屏应用从后台切回前台时页面不会重新加载，这里把
// /api/v1/health 的 web_version 与页面加载时的基准比较——切回前台和每 30 分钟
// 检查一次，不同就在顶部提示刷新；只有 server_version 变化时不提示，仅原地上移
// 底部显示的服务器版本。请求失败一律静默忽略。
import { useCallback, useEffect, useRef, useState } from 'react'
import { fetchHealth } from './health'

/** 定时检查间隔：30 分钟。 */
export const VERSION_CHECK_INTERVAL_MS = 30 * 60 * 1000

export interface VersionCheck {
  /** 底部显示的服务器版本；检查到新值时原地上移。 */
  serverVersion?: string
  /** 当前加载的网页版本（页面加载时的基准，刷新前不变）。 */
  webVersion?: string
  /** 检测到服务器上已有新网页版本，等待用户刷新。 */
  hasUpdate: boolean
  dismissUpdate: () => void
  reloadForUpdate: () => void
}

export function useVersionCheck(): VersionCheck {
  const baselineRef = useRef<string | undefined>(undefined)
  const dismissedRef = useRef(false)
  const [serverVersion, setServerVersion] = useState<string | undefined>()
  const [webVersion, setWebVersion] = useState<string | undefined>()
  const [hasUpdate, setHasUpdate] = useState(false)

  const check = useCallback(async (fromForeground: boolean) => {
    let health
    try {
      health = await fetchHealth()
    } catch {
      return
    }
    setServerVersion(health.server_version)
    if (baselineRef.current === undefined) {
      // 第一次成功拿到 health：记下当前加载的网页版本作为基准。
      baselineRef.current = health.web_version
      setWebVersion(health.web_version)
      return
    }
    // 基准为空（服务器没有版本标记文件）或版本没变都不提示，避免误报。
    if (health.web_version && baselineRef.current && health.web_version !== baselineRef.current) {
      // 用户关掉过提示：再切回前台仍检测到新版本时重新显示。
      if (fromForeground || !dismissedRef.current) setHasUpdate(true)
    }
  }, [])

  useEffect(() => {
    void check(false)
    const onVisibility = () => {
      if (document.visibilityState === 'visible') void check(true)
    }
    document.addEventListener('visibilitychange', onVisibility)
    const timer = setInterval(() => void check(false), VERSION_CHECK_INTERVAL_MS)
    return () => {
      document.removeEventListener('visibilitychange', onVisibility)
      clearInterval(timer)
    }
  }, [check])

  const dismissUpdate = useCallback(() => {
    dismissedRef.current = true
    setHasUpdate(false)
  }, [])
  const reloadForUpdate = useCallback(() => window.location.reload(), [])
  return { serverVersion, webVersion, hasUpdate, dismissUpdate, reloadForUpdate }
}

/** 底部版本号：服务器 v1.2.3 · 网页 v20261027；网页版本缺失时只显示服务器部分。 */
export function versionFooterText(serverVersion?: string, webVersion?: string): string {
  const parts: string[] = []
  if (serverVersion) parts.push(`服务器 v${serverVersion}`)
  if (webVersion) parts.push(`网页 v${webVersion}`)
  return parts.join(' · ')
}
