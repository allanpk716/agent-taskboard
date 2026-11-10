// 访客详情（#332）：名字、认领与停用状态、可见范围内的工单和链接；
// 集中提供改名、停用/恢复、复制主页文案。访客级重置访问码与重置主页地址
// 的入口由 #331 接入时加在这里的操作区。
import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../api'
import {
  copyGuestHomeText,
  disableGuest,
  enableGuest,
  fetchGuestDetail,
  previewGuestAccessCodeReset,
  renameGuest,
  resetGuestAccessCodeForGuest,
  resetGuestHomeKey,
  type GuestAccessCodeResetPreview,
  type GuestDetail,
} from './memberApi'
import { ResetAccessResultDialog } from './ResetAccessDialog'
import { Confirm, Modal, copyText, nav, toast } from './ui'
import { absoluteUrl, fmtDateCN } from '../shared/format'

export function GuestDetailPage({ guestId }: { guestId: string }) {
  const id = Number(guestId)
  const [detail, setDetail] = useState<GuestDetail | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [renaming, setRenaming] = useState(false)
  const [newName, setNewName] = useState('')
  const [busy, setBusy] = useState(false)
  const [confirmDisable, setConfirmDisable] = useState(false)
  const [confirmEnable, setConfirmEnable] = useState(false)
  const [confirmCodeReset, setConfirmCodeReset] = useState(false)
  const [confirmHomeReset, setConfirmHomeReset] = useState(false)
  const [codePreview, setCodePreview] = useState<GuestAccessCodeResetPreview | null>(null)
  const [previewError, setPreviewError] = useState<string | null>(null)
  const [resetResult, setResetResult] = useState<{
    action: 'code' | 'home'
    url: string
    urlLabel: string
    accessCode?: string
    resetText: string
  } | null>(null)

  const refresh = useCallback(() => {
    fetchGuestDetail(id)
      .then(setDetail)
      .catch((e) => setError(e instanceof ApiError ? e.message : '读取访客详情失败'))
  }, [id])

  useEffect(() => {
    refresh()
  }, [refresh])

  // 打开重置访问码确认时先取影响预览（作废设备数 / 涉及工单数）。
  useEffect(() => {
    if (!confirmCodeReset || codePreview || previewError) return
    let cancelled = false
    previewGuestAccessCodeReset(id)
      .then((got) => {
        if (!cancelled) setCodePreview(got)
      })
      .catch((e) => {
        if (!cancelled) {
          setPreviewError(e instanceof ApiError ? e.message : '影响预览加载失败，请稍后重试')
        }
      })
    return () => {
      cancelled = true
    }
  }, [confirmCodeReset, codePreview, previewError, id])

  if (error) {
    return (
      <div className="bpage">
        <div className="ecard">{error}</div>
        <div className="mcard">
          <button className="btn ghost" onClick={() => nav('/guests')}>
            返回访客名单
          </button>
        </div>
      </div>
    )
  }
  if (!detail) {
    return (
      <div className="bpage">
        <div className="ecard">加载中…</div>
      </div>
    )
  }

  const guest = detail.guest
  const disabled = !!guest.disabled_at

  async function submitRename() {
    const name = newName.trim()
    if (!name) {
      toast('请填写去空白后的访客名字')
      return
    }
    setBusy(true)
    try {
      await renameGuest(id, name)
      toast(`✓ 已改名为「${name}」；新回复将使用新名字，历史署名保持原样`)
      setRenaming(false)
      setNewName('')
      refresh()
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : '改名失败')
    } finally {
      setBusy(false)
    }
  }

  async function copyHome() {
    try {
      const result = await copyGuestHomeText(id)
      const ok = await copyText(result.share_text)
      toast(ok ? `✓ ${guest.name}的主页文案已复制（不含访问码）` : '复制失败，请手动选择文字复制')
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : '复制主页文案失败')
    }
  }

  async function toggleDisabled(enable: boolean) {
    setBusy(true)
    try {
      if (enable) {
        await enableGuest(id)
        toast(`✓ 已恢复「${guest.name}」，未过期链接按原有效期重新生效`)
      } else {
        await disableGuest(id)
        toast(`✓ 已停用「${guest.name}」，主页与名下全部链接一起失效`)
      }
      setConfirmDisable(false)
      setConfirmEnable(false)
      refresh()
      window.dispatchEvent(new Event('tb:invalidate'))
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : enable ? '恢复失败' : '停用失败')
    } finally {
      setBusy(false)
    }
  }

  async function resetCode() {
    setBusy(true)
    try {
      const res = await resetGuestAccessCodeForGuest(id)
      setResetResult({
        action: 'code',
        url: res.home_url,
        urlLabel: '访客主页',
        accessCode: res.access_code,
        resetText: res.reset_text.replaceAll(res.home_url, absoluteUrl(res.home_url)),
      })
      setConfirmCodeReset(false)
      setCodePreview(null)
      setPreviewError(null)
      refresh()
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : '重置访问码失败')
    } finally {
      setBusy(false)
    }
  }

  async function resetHome() {
    setBusy(true)
    try {
      const res = await resetGuestHomeKey(id)
      setResetResult({
        action: 'home',
        url: res.home_url,
        urlLabel: '新的主页地址',
        resetText: res.share_text.replaceAll(res.home_url, absoluteUrl(res.home_url)),
      })
      setConfirmHomeReset(false)
      refresh()
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : '重置主页地址失败')
    } finally {
      setBusy(false)
    }
  }

  if (resetResult) {
    return <ResetAccessResultDialog result={resetResult} onClose={() => setResetResult(null)} />
  }

  return (
    <div className="bpage">
      <div className="mcard" style={{ marginTop: 12 }}>
        <div className="sec-t" style={{ marginTop: 0 }}>
          {guest.name}
          {disabled && <span className="badge closed">已停用</span>}
        </div>
        <div className="hint">
          {disabled
            ? `停用于 ${fmtDateCN(guest.disabled_at!)}；主页与名下全部链接一起失效`
            : guest.claimed_at
              ? `已认领${guest.claim_name ? ` · ${guest.claim_name}` : ''} · ${fmtDateCN(guest.claimed_at)}`
              : '未认领'}
          {' · '}
          {guest.link_count} 条链接
        </div>
        <div className="guest-actions">
          <button className="btn ghost" onClick={() => void copyHome()}>
            复制主页文案
          </button>
          <button
            className="btn ghost"
            onClick={() => {
              setCodePreview(null)
              setPreviewError(null)
              setConfirmCodeReset(true)
            }}
          >
            重置访问码
          </button>
          <button className="btn ghost" onClick={() => setConfirmHomeReset(true)}>
            重置主页地址
          </button>
          <button
            className="btn ghost"
            onClick={() => {
              setNewName(guest.name)
              setRenaming(true)
            }}
          >
            改名
          </button>
          {disabled ? (
            <button className="btn ghost" onClick={() => setConfirmEnable(true)} disabled={busy}>
              恢复
            </button>
          ) : (
            <button className="btn ghost danger" onClick={() => setConfirmDisable(true)} disabled={busy}>
              停用
            </button>
          )}
        </div>
        {renaming && (
          <div className="inrow" style={{ marginTop: 10 }}>
            <input
              aria-label="访客新名字"
              value={newName}
              placeholder="如：检验科-张三"
              onChange={(e) => setNewName(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && void submitRename()}
            />
            <button className="btn" onClick={() => void submitRename()} disabled={busy}>
              保存
            </button>
            <button className="btn ghost" onClick={() => setRenaming(false)}>
              取消
            </button>
          </div>
        )}
        <div className="hint">
          只有访客建立者、管理员或名下链接的管理人可以管理这位访客；停用和恢复只能在网页上操作。
        </div>
      </div>

      <div className="mcard">
        <div className="sec-t" style={{ marginTop: 0 }}>
          名下工单 <span className="hint">只列你可见范围内的工单</span>
        </div>
        {detail.tickets.length === 0 ? (
          <div className="hint">你可见范围内没有这位访客名下的工单。</div>
        ) : (
          detail.tickets.map((t) => (
            <div className="guest-ticket" key={t.number}>
              <a
                href={`/t/${t.number}`}
                onClick={(e) => {
                  e.preventDefault()
                  nav(`/t/${t.number}`)
                }}
              >
                <span className={`badge ${t.status === 'closed' ? 'closed' : 'open'}`}>
                  {t.status === 'closed' ? '已关闭' : '开启'}
                </span>
                {t.title}
              </a>
              <div className="guest-links">
                {t.links.map((l) => (
                  <span className="hint" key={l.id}>
                    「{l.note}」{l.can_edit_brief ? ' · 可填写说明' : ''}
                    {l.revoked_at ? ' · 已撤销' : ` · 到 ${fmtDateCN(l.expires_at)}`}
                  </span>
                ))}
              </div>
            </div>
          ))
        )}
      </div>

      {confirmDisable && (
        <Confirm
          title="停用这位访客？"
          body={`停用后「${guest.name}」的访客主页和名下全部链接一起失效，需要他补全说明的工单会回到指派人。各链接的撤销与有效期字段不改写；恢复后未过期的链接按原有效期重新生效。`}
          yesLabel="停用"
          onYes={() => void toggleDisabled(false)}
          onCancel={() => setConfirmDisable(false)}
          danger
        />
      )}
      {confirmEnable && (
        <Confirm
          title="恢复这位访客？"
          body={`恢复后「${guest.name}」的主页重新可用，名下未撤销、未过期的链接按原有效期重新生效；已过期的链接仍然失效。`}
          yesLabel="恢复"
          onYes={() => void toggleDisabled(true)}
          onCancel={() => setConfirmEnable(false)}
        />
      )}
      {confirmCodeReset && (
        <Modal title="🔐 重置访问码" onClose={() => setConfirmCodeReset(false)} wide>
          <p className="reset-lead">
            将按访客「{guest.name}」重置访问码：主页与名下全部链接共用同一个访问码。
          </p>
          {codePreview ? (
            <div className="reset-info">
              影响范围：作废 {codePreview.device_count} 台设备，涉及 {codePreview.ticket_count} 张工单
              {codePreview.access_code_locked ? '，并解除当前的输错锁定' : ''}。
            </div>
          ) : previewError ? (
            <div className="warnbox">{previewError}</div>
          ) : (
            <div className="hint">正在加载影响范围…</div>
          )}
          <div className="warnbox">
            这位访客的所有已登录设备（主页、名下全部链接与对方的 Agent）会立即失效，连续输错造成的锁定会解除；
            将生成新的 6 位访问码，链接地址与主页地址保持不变。新码只显示一次。
          </div>
          <div className="mrow">
            <button className="btn ghost" onClick={() => setConfirmCodeReset(false)}>
              取消
            </button>
            <span className="sp" />
            <button className="btn" onClick={() => void resetCode()} disabled={busy}>
              重置访问码
            </button>
          </div>
        </Modal>
      )}
      {confirmHomeReset && (
        <Confirm
          title="🔄 重置主页地址？"
          body={`「${guest.name}」的旧主页地址立即失效；访问码、已登录设备和名下链接都不受影响。将生成新的主页地址。`}
          yesLabel="重置主页地址"
          onYes={() => void resetHome()}
          onCancel={() => setConfirmHomeReset(false)}
        />
      )}
    </div>
  )
}
