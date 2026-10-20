// 管理页「系统状态」面板（#45）：备份状态（超过 36 小时标红、
// 「备份未离开本机」）与前后端版本/更新按钮。服务只提示有新版本，
// 绝不自动安装；更新走与管理 API、taskboard update 同一套逻辑。
import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../api'
import { fmtBeijing } from '../../shared/format'
import {
  applyUpdate,
  fetchBackupsStatus,
  fetchUpdateStatus,
  type BackupsStatus,
  type UpdateResult,
  type UpdateStatus,
} from './systemApi'
import { fetchHealth } from '../../shared/health'

// 平台时区固定为北京时间：展示不随浏览器所在时区变（评审 P2-14）。
function fmtTime(iso: string | null): string {
  return iso ? fmtBeijing(iso) : '从未成功备份'
}

export function SystemPanel() {
  const [backups, setBackups] = useState<BackupsStatus | null>(null)
  const [update, setUpdate] = useState<UpdateStatus | null>(null)
  const [health, setHealth] = useState<{ server: string; web: string } | null>(null)
  const [updateError, setUpdateError] = useState<string | null>(null)
  const [applying, setApplying] = useState(false)
  const [result, setResult] = useState<UpdateResult | null>(null)

  const refresh = useCallback(() => {
    fetchBackupsStatus()
      .then(setBackups)
      .catch(() => {})
    fetchUpdateStatus()
      .then((s) => {
        setUpdate(s)
        setUpdateError(null)
      })
      .catch((err) => {
        setUpdate(null)
        setUpdateError(err instanceof ApiError ? err.message : String(err))
        // 检查失败时仍展示当前版本（健康检查公开可读）。
        fetchHealth()
          .then((h) => setHealth({ server: h.server_version, web: h.web_version }))
          .catch(() => {})
      })
  }, [])
  useEffect(refresh, [refresh])

  const runUpdate = () => {
    if (!window.confirm('立即更新平台组件？更新前会自动备份，后端更新后服务会短暂重启。')) return
    setApplying(true)
    setResult(null)
    applyUpdate()
      .then((r) => {
        setResult(r)
        refresh()
      })
      .catch((err) => setResult({ source_used: '', steps: [], message: err.message, server_updated: false, web_updated: false, client_updated: false, restart_needed: false }))
      .finally(() => setApplying(false))
  }

  const serverUpdate = update?.server.update_available
  const webUpdate = update?.web.update_available
  const clientUpdate = update?.client.update_available
  const anyUpdate = !!(serverUpdate || webUpdate || clientUpdate)

  return (
    <>
      <div className="mcard" id="backup" data-testid="backup-card">
        <div className="sec-t" style={{ marginTop: 0 }}>
          备份
        </div>
        <div className="kv">
          <span>最近一次成功备份</span>
          <b className={backups?.overdue ? 'sys-red' : undefined} data-testid="backup-last-success">
            {backups ? fmtTime(backups.last_success_at) : '…'}
          </b>
        </div>
        {backups?.overdue && (
          <div className="hint sys-red" data-testid="backup-overdue">
            超过 36 小时没有成功备份，请检查备份目标是否可连（每小时会自动重试）。
          </div>
        )}
        {backups?.target_unreachable && backups.last_local_fallback_at && (
          <div className="hint sys-red" data-testid="backup-target-unreachable">
            备份目标不可达（{backups.target_unreachable_error || '原因未知'}）。最近一份只在本机：
            {fmtBeijing(backups.last_local_fallback_at)}
          </div>
        )}
        {backups?.local_only && (
          <div className="hint" data-testid="backup-local-only">
            备份未离开本机（未配置备份目标，副本在数据目录的 backups\ 下）。配置 NAS
            共享目标见 docs/deploy-and-release.md「备份与恢复」。
          </div>
        )}
        {backups?.last_failure && !backups.overdue && (
          <div className="hint">最近一次失败：{backups.last_failure.error || backups.last_failure.at}</div>
        )}
        <div className="hint">手动备份用 <code>taskboard backup</code>；恢复演练见部署文档。</div>
      </div>

      <div className="mcard" data-testid="update-card">
        <div className="sec-t" style={{ marginTop: 0 }}>
          版本与更新
        </div>
        {updateError && (
          <div className="hint" data-testid="update-error">
            暂时查不到更新：{updateError}
          </div>
        )}
        {(update || health) && (
          <>
            <div className="kv">
              <span>后端</span>
              <b data-testid="server-version">
                {update
                  ? `${update.server.current || '未知'}${serverUpdate ? ` → ${update.server.latest}（有新版本）` : '（已是最新）'}`
                  : `${health?.server || '未知'}（未检查更新）`}
              </b>
            </div>
            <div className="kv">
              <span>前端</span>
              <b data-testid="web-version">
                {update
                  ? `${update.web.current || '未知'}${webUpdate ? ` → ${update.web.latest}（有新版本）` : '（已是最新）'}`
                  : `${health?.web || '未知'}（未检查更新）`}
              </b>
            </div>
            <div className="kv">
              <span>桌面客户端</span>
              <b data-testid="client-version">
                {update
                  ? `${update.client.current || '未代管'}${
                      clientUpdate
                        ? ` → ${update.client.latest}（有新版本）`
                        : update.client.latest
                          ? '（已是最新）'
                          : '（尚未发布）'
                    }`
                  : '未知（未检查更新）'}
              </b>
            </div>
            {webUpdate && update?.web.min_server && update.web_needs_newer_server && (
              <div className="hint">
                前端 {update.web.latest} 需要后端 ≥ {update.web.min_server}，更新时会先升级后端。
              </div>
            )}
            {update && <div className="hint">更新源：{update.source_used}。服务只提示有新版本，不会自动安装。</div>}
            <div className="btnrow">
              <button className="btn" disabled={!anyUpdate || applying} onClick={runUpdate} data-testid="update-btn">
                {applying ? '更新中…' : anyUpdate ? '立即更新' : '没有可用更新'}
              </button>
            </div>
          </>
        )}
        {result && (
          <div className="hint" data-testid="update-result">
            {result.message
              ? result.message
              : `已更新（${result.source_used}）：${result.steps.join('；')}${
                  result.preupdate_backup_local ? '；更新前备份只在本机。' : ''
                }${result.restart_needed ? '；后端已更新，服务正在重启。' : ''
                }`}
          </div>
        )}
      </div>
    </>
  )
}
