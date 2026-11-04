// 工单详情「🔑 重置访问」的分步引导：先选有效链接，再选操作，确认后展示
// 一次性新访问码与服务端生成的重置文案。结果弹窗单独抽出来，供交接弹窗在
// 老链接换新地址时复用，保证成员一定能看到同时重置的新访问码。
import { useEffect, useMemo, useRef, useState } from 'react'
import { ApiError } from '../api'
import { absoluteUrl, linkExpired } from '../shared/format'
import {
  resetGuestAccessCode,
  revokeGuestLink,
  rotateGuestLinkKey,
  type GuestLink,
} from './memberApi'
import { Modal, copyText, toast } from './ui'

export type ResetAccessAction = 'code' | 'link' | 'revoke'

export interface ResetAccessStart {
  linkId: number
  action: ResetAccessAction
  step?: 'action' | 'confirm'
}

interface ResetAccessResult {
  action: Exclude<ResetAccessAction, 'revoke'>
  url: string
  accessCode?: string
  resetText: string
  expiresAt: string
}

const actionName: Record<ResetAccessAction, string> = {
  code: '重置访问码',
  link: '重置链接',
  revoke: '撤销链接',
}

function remainingDays(iso: string): number {
  return Math.max(0, Math.ceil((new Date(iso).getTime() - Date.now()) / 86400000))
}

function claimedLink(link: GuestLink): boolean {
  return !!link.claimed_at || link.status === 'claimed' || link.status === 'access_code_locked'
}

function statusText(link: GuestLink): string {
  if (claimedLink(link)) return `已认领 · ${link.claim_name ?? ''}`
  return '未认领'
}

function expiryDate(iso: string): string {
  return iso.slice(0, 10)
}

function absoluteResetText(text: string, url: string): string {
  return text.replaceAll(url, absoluteUrl(url))
}

export function ResetAccessResultDialog({
  result,
  onClose,
}: {
  result: ResetAccessResult
  onClose: () => void
}) {
  const [copied, setCopied] = useState(false)
  const previewRef = useRef<HTMLDivElement | null>(null)
  const hasCode = !!result.accessCode

  async function copy() {
    const ok = await copyText(result.resetText)
    if (ok) {
      setCopied(true)
      toast('已复制，可直接粘贴给对方')
      return
    }
    const node = previewRef.current
    if (node) {
      const range = document.createRange()
      range.selectNodeContents(node)
      const selection = window.getSelection()
      selection?.removeAllRanges()
      selection?.addRange(range)
    }
    toast('浏览器未允许自动复制，已选中文案，可手动复制')
  }

  return (
    <Modal title={`${actionName[result.action]}完成`} onClose={onClose} wide>
      <p className="reset-result-sub">
        {hasCode ? '请立即复制并发给对方；新访问码关闭后不再显示。' : '请立即复制新链接并私发给对方。'}
      </p>
      <div className="reset-result-grid">
        {hasCode && (
          <div className="reset-result-box reset-code-box" data-testid="reset-access-code">
            <div className="reset-result-label">新访问码</div>
            <p className="reset-code">{result.accessCode}</p>
          </div>
        )}
        <div className="reset-result-box">
          <div className="reset-result-label">{result.action === 'code' ? '保持不变的链接' : '新链接'}</div>
          <div className="reset-result-value" data-testid="reset-access-link">
            {absoluteUrl(result.url)}
          </div>
          <div className="reset-result-label top-gap-sm">有效期至</div>
          <div className="reset-result-value">{expiryDate(result.expiresAt)}</div>
        </div>
      </div>
      {hasCode && (
        <div className="warnbox">
          新访问码只显示这一次，关闭后无法再查看，需要的话只能再重置。关闭前可以反复复制。
        </div>
      )}
      <div className="sec-t">发给对方的文案预览</div>
      <div className="reset-copy-preview" ref={previewRef} aria-live="polite">
        {result.resetText}
      </div>
      <div className="mrow">
        <button type="button" className="btn big reset-copy-button" onClick={() => void copy()}>
          {copied ? '✓ 已复制' : '📋 一键复制'}
        </button>
        <span className="sp" />
        <button type="button" className="btn ghost" onClick={onClose}>
          关闭
        </button>
      </div>
    </Modal>
  )
}

export function ResetAccessDialog({
  ticketNumber,
  ticketTitle,
  links,
  knownUrls = {},
  start,
  reload,
  onChanged,
  onLinkRotated,
  onClose,
}: {
  ticketNumber: string
  ticketTitle: string
  links: GuestLink[] | null
  knownUrls?: Record<number, string>
  start?: ResetAccessStart | null
  reload: () => Promise<void>
  onChanged: () => void
  onLinkRotated?: (id: number, url: string) => void
  onClose: () => void
}) {
  const activeLinks = useMemo(
    () => (links ?? []).filter((link) => !link.revoked_at && !linkExpired(link.expires_at)),
    [links],
  )
  const startLinkId = start?.linkId ?? null
  const startAction = start?.action ?? null
  const startStep = start?.step ?? 'action'
  const [selectedId, setSelectedId] = useState<number | null>(
    startLinkId !== null && activeLinks.some((link) => link.id === startLinkId) ? startLinkId : null,
  )
  const [action, setAction] = useState<ResetAccessAction | null>(startAction)
  const [step, setStep] = useState<'link' | 'action' | 'confirm'>(
    startAction && startStep === 'confirm' ? 'confirm' : startAction ? 'action' : 'link',
  )
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<ResetAccessResult | null>(null)

  useEffect(() => {
    if (selectedId !== null && activeLinks.some((link) => link.id === selectedId)) return
    const requested = activeLinks.find((link) => link.id === startLinkId)
    if (requested) {
      setSelectedId(requested.id)
      return
    }
    if (activeLinks.length === 1) {
      setSelectedId(activeLinks[0].id)
      return
    }
    setSelectedId(null)
  }, [activeLinks, selectedId, startLinkId])

  // 单条链接按方案 A 跳过选择；直接从旧入口进入时也保持已选链接。
  useEffect(() => {
    if (step !== 'link' || selectedId === null || activeLinks.length !== 1) return
    setStep('action')
  }, [step, selectedId, activeLinks.length])

  const selected = activeLinks.find((link) => link.id === selectedId) ?? null
  const selectedClaimed = selected ? claimedLink(selected) : false

  if (result) return <ResetAccessResultDialog result={result} onClose={onClose} />
  if (links === null) {
    return (
      <Modal title="🔑 重置访问" onClose={onClose} wide>
        <div className="hint">访客链接加载中…</div>
      </Modal>
    )
  }
  if (activeLinks.length === 0) {
    return (
      <Modal title="🔑 重置访问" onClose={onClose} wide>
        <div className="warnbox">这张工单没有有效的访客链接。可以先新建一条，再把它发给访客。</div>
        <div className="mrow">
          <span className="sp" />
          <button type="button" className="btn ghost" onClick={onClose}>
            关闭
          </button>
        </div>
      </Modal>
    )
  }

  async function submit() {
    if (!selected || !action || busy) return
    setBusy(true)
    try {
      if (action === 'code') {
        const res = await resetGuestAccessCode(ticketNumber, selected.id)
        setResult({
          action,
          url: res.url,
          accessCode: res.access_code,
          resetText: absoluteResetText(res.reset_text, res.url),
          expiresAt: res.expires_at,
        })
      } else if (action === 'link') {
        const res = await rotateGuestLinkKey(ticketNumber, selected.id)
        setResult({
          action,
          url: res.url,
          accessCode: res.access_code,
          resetText: absoluteResetText(res.reset_text, res.url),
          expiresAt: res.expires_at,
        })
        onLinkRotated?.(selected.id, absoluteUrl(res.url))
      } else {
        await revokeGuestLink(ticketNumber, selected.id)
        toast('已撤销，对方再打开会看到链接失效页')
        onClose()
      }
      await reload()
      onChanged()
    } catch (e) {
      toast(e instanceof ApiError ? e.message : `${actionName[action]}失败，请重试`)
    } finally {
      setBusy(false)
    }
  }

  const warnings =
    action === 'code'
      ? [
          '对方所有已登录设备（含对方的 Agent）会立即失效',
          '链接地址保持不变',
          '将生成新的 6 位访问码',
        ]
      : action === 'link'
        ? selectedClaimed
          ? [
              '旧链接地址立即失效，无法再打开',
              '旧设备全部失效（含对方的 Agent）',
              '将生成新链接，并生成新的 6 位访问码',
            ]
          : ['旧链接地址立即失效，无法再打开', '将生成新链接；本条链接尚无访问码']
        : ['对方再打开会看到链接失效页', '撤销后不能恢复', '其他访客链接不受影响']

  return (
    <Modal title="🔑 重置访问" onClose={onClose} wide>
      <p className="reset-help">按引导选择链接和操作，每一步都会说明影响。</p>
      <ol className="reset-steps" aria-label="流程进度">
        <li className={step === 'link' ? 'active' : 'done'}>
          <span className="dot">{step === 'link' ? '1' : '✓'}</span>
          <span>选链接</span>
        </li>
        <li className={step === 'action' ? 'active' : step === 'confirm' ? 'done' : ''}>
          <span className="dot">{step === 'confirm' ? '✓' : '2'}</span>
          <span>选操作</span>
        </li>
        <li className={step === 'confirm' ? 'active' : ''}>
          <span className="dot">3</span>
          <span>确认</span>
        </li>
      </ol>

      {step === 'link' && (
        <fieldset className="reset-panel">
          <legend className="sr-only">选择要重置的访客链接</legend>
          <p className="reset-lead">
            这张工单有多条有效访客链接。请先选择要处理的一条，避免把发给其他访客的链接一起重置。
          </p>
          <div className="reset-link-options">
            {activeLinks.map((link) => (
              <button
                key={link.id}
                type="button"
                className={`reset-link-option${selectedId === link.id ? ' selected' : ''}`}
                aria-pressed={selectedId === link.id}
                onClick={() => setSelectedId(link.id)}
              >
                <span className="reset-option-title">
                  <span>{link.note}</span>
                  <span className={`reset-chip ${claimedLink(link) ? 'claimed' : 'unclaimed'}`}>
                    {statusText(link)}
                  </span>
                </span>
                <span className="reset-meta">
                  <span className="reset-chip plain">剩余 {remainingDays(link.expires_at)} 天</span>
                  <span className="reset-chip plain">已使用 {link.use_count ?? 0} 次</span>
                  <span className="reset-chip plain">{claimedLink(link) ? '有访问码' : '无访问码'}</span>
                </span>
                <span className="short-link">{knownUrls[link.id] ?? '/g/••••••'}</span>
              </button>
            ))}
          </div>
          <div className="reset-info">
            已撤销的链接不会出现在这里；选中的链接会在下一步继续显示，方便核对。
          </div>
          <div className="mrow">
            <span className="sp" />
            <button
              type="button"
              className="btn"
              disabled={selectedId === null}
              onClick={() => setStep('action')}
            >
              下一步：选操作
            </button>
          </div>
        </fieldset>
      )}

      {step === 'action' && selected && (
        <fieldset className="reset-panel">
          <legend className="sr-only">选择重置操作</legend>
          <p className="reset-lead">已选择「{selected.note}」。请选择适合的处理方式。</p>
          <div className="reset-operation-options">
            <button
              type="button"
              className={`reset-operation-option${action === 'code' ? ' selected' : ''}`}
              disabled={!selectedClaimed}
              aria-pressed={action === 'code'}
              aria-label="重置访问码"
              onClick={() => setAction('code')}
            >
              <span className="reset-op-icon">🔐</span>
              <span className="reset-op-content">
                <span className="reset-option-title">
                  <span>重置访问码</span>
                </span>
                <span className="reset-fit">适合：对方忘了访问码，或换了设备需要重新登录。</span>
                <span className="reset-invalidates">会失效：对方所有已登录设备（含对方的 Agent）。</span>
                <span className="reset-option-note">链接地址保持不变，只生成新的 6 位访问码。</span>
              </span>
            </button>
            <button
              type="button"
              className={`reset-operation-option${action === 'link' ? ' selected' : ''}`}
              aria-pressed={action === 'link'}
              aria-label="重置链接"
              onClick={() => setAction('link')}
            >
              <span className="reset-op-icon">🔗</span>
              <span className="reset-op-content">
                <span className="reset-option-title">
                  <span>重置链接</span>
                </span>
                <span className="reset-fit">适合：对方找不到链接，或链接可能被别人拿到。</span>
                <span className="reset-invalidates">
                  会失效：{selectedClaimed ? '旧地址、旧设备全部立即失效。' : '旧地址立即失效。'}
                </span>
                <span className="reset-option-note">
                  {selectedClaimed
                    ? '会生成新地址，并同时重置访问码。'
                    : '会生成新地址；对方首次打开并认领后才会获得访问码。'}
                </span>
              </span>
            </button>
            <button
              type="button"
              className={`reset-operation-option danger${action === 'revoke' ? ' selected' : ''}`}
              aria-pressed={action === 'revoke'}
              aria-label="撤销链接"
              onClick={() => setAction('revoke')}
            >
              <span className="reset-op-icon danger">⛔</span>
              <span className="reset-op-content">
                <span className="reset-option-title">
                  <span>撤销链接</span>
                </span>
                <span className="reset-fit">适合：这张链接不该再被任何人使用。</span>
                <span className="reset-invalidates">会失效：对方手里的这条链接。</span>
                <span className="reset-option-note">
                  对方再打开会看到失效页，不能恢复；其他链接不受影响。
                </span>
              </span>
            </button>
          </div>
          {!selectedClaimed && (
            <div className="warnbox">
              这条链接还没有被访客认领，尚没有访问码，因此不能只重置访问码。请选择「重置链接」或「撤销链接」。
            </div>
          )}
          <div className="mrow">
            {activeLinks.length > 1 && (
              <button type="button" className="btn ghost" onClick={() => setStep('link')}>
                上一步
              </button>
            )}
            <span className="sp" />
            <button type="button" className="btn" disabled={!action} onClick={() => setStep('confirm')}>
              下一步：确认
            </button>
          </div>
        </fieldset>
      )}

      {step === 'confirm' && selected && action && (
        <fieldset className="reset-panel">
          <legend className="sr-only">确认重置操作</legend>
          <p className="reset-lead">请核对以下信息。确认后会立即生效，不能撤销。</p>
          <div className="reset-summary">
            <div className="reset-option-title">
              <span>{selected.note}</span>
              <span className={`reset-chip ${selectedClaimed ? 'claimed' : 'unclaimed'}`}>
                {statusText(selected)}
              </span>
            </div>
            <div className="reset-meta">
              <span className="reset-chip plain">操作：{actionName[action]}</span>
              <span className="reset-chip plain">工单：{ticketTitle}</span>
              <span className="reset-chip plain">有效期至 {expiryDate(selected.expires_at)}</span>
            </div>
            <div className="short-link">
              {action === 'code' ? (knownUrls[selected.id] ?? '/g/••••••') : '将生成新的链接地址'}
            </div>
          </div>
          <div className="warnbox">
            <strong>确认后立即生效：</strong>
            <ul className="reset-warning-list">
              {warnings.map((text) => (
                <li key={text}>{text}</li>
              ))}
            </ul>
          </div>
          <div className="mrow">
            <button type="button" className="btn ghost" onClick={() => setStep('action')}>
              上一步
            </button>
            <span className="sp" />
            <button
              type="button"
              className={`btn big${action === 'code' ? '' : ' danger'}`}
              disabled={busy}
              onClick={() => void submit()}
            >
              {busy ? '正在处理…' : `确认${actionName[action]}`}
            </button>
          </div>
        </fieldset>
      )}
    </Modal>
  )
}
