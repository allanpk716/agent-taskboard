// 新版本横幅（#262）：useVersionCheck 检测到服务器上的网页版本与当前加载的不
// 同时固定显示在页面最上方；点正文刷新，点 × 关闭（切回前台仍检测到新版本会
// 再出现）。成员端与访客端共用。
import type { VersionCheck } from './versionCheck'

export function VersionBanner({ check }: { check: VersionCheck }) {
  if (!check.hasUpdate) return null
  return (
    <div className="version-banner" role="alert">
      <button type="button" className="version-banner-refresh" onClick={check.reloadForUpdate}>
        已有新版本，点此刷新
      </button>
      <button
        type="button"
        className="version-banner-close"
        aria-label="关闭新版本提示"
        onClick={check.dismissUpdate}
      >
        ×
      </button>
    </div>
  )
}
