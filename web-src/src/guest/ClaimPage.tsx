import { useState } from 'react'
import { ApiError } from '../api'
import { guestRawApi } from './guestApi'
import { fmtDateCN } from '../shared/format'

export interface ClaimInfo {
  state: 'unclaimed' | 'claimed' | 'access_code_locked'
  inviter: string
  note: string
  valid_until: string
  visibility_hint: string
}

export function ClaimPage({
  info,
  linkKey,
  onClaimed,
}: {
  info: ClaimInfo
  linkKey: string
  onClaimed: (token: string) => void
}) {
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [issued, setIssued] = useState<{ token: string; code: string } | null>(null)

  async function claim() {
    if (!name.trim()) {
      setError('请填写你的称呼。')
      return
    }
    setBusy(true)
    setError('')
    try {
      const res = await guestRawApi(linkKey, '/api/v1/guest-links/claim', {
        method: 'POST',
        body: JSON.stringify({ author_name: name.trim() }),
      })
      setIssued({ token: res.device_token as string, code: String(res.access_code ?? '') })
    } catch (err) {
      setError(err instanceof Error ? err.message : '认领失败，请重试。')
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="guest-root claim-root">
      <div className="guest-app claim-page">
        <header className="guest-top">
          <span>工单板 · 访客页</span>
        </header>
        <section className="head guest-title-card claim-card">
          <div className="meta">访客链接认领</div>
          <h1>请确认是你本人打开</h1>
          <div className="claim-meta">
            {info.inviter} 邀请你处理一张工单
            <br />
            链接备注：{info.note}
            <br />
            有效期至：{fmtDateCN(info.valid_until)}
          </div>
          <div className="privacy-note">🔒 {info.visibility_hint}。</div>
          <label className="claim-label" htmlFor="guest-claim-name">
            你的称呼（{info.inviter}会看到）
          </label>
          <input
            id="guest-claim-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="如：检验科 张三"
          />
          {issued ? (
            <>
              <div className="access-code-card">
                <div>访问码</div>
                <strong data-access-code>{issued.code}</strong>
                <p>请截图保存访问码。它只显示这一次；换设备或清除浏览器记录后凭它重新进入。</p>
              </div>
              <button className="btn claim-button" onClick={() => onClaimed(issued.token)}>
                我已保存，打开工单
              </button>
            </>
          ) : (
            <>
              {error && <div className="claim-error">{error}</div>}
              <button className="btn claim-button" disabled={busy} onClick={() => void claim()}>
                {busy ? '正在打开…' : '是我，打开工单'}
              </button>
              <div className="claim-footnote">不是你？请不要打开，告诉 {info.inviter}。</div>
            </>
          )}
        </section>
      </div>
    </main>
  )
}

export function AccessCodePage({
  info,
  linkKey,
  onEntered,
}: {
  info: ClaimInfo
  linkKey: string
  onEntered: (token: string) => void
}) {
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const locked = info.state === 'access_code_locked'

  async function enter() {
    setBusy(true)
    setError('')
    try {
      const res = await guestRawApi(linkKey, '/api/v1/guest-links/enter', {
        method: 'POST',
        body: JSON.stringify({ access_code: code.trim() }),
      })
      onEntered(res.device_token as string)
    } catch (err) {
      if (err instanceof ApiError && err.code === 'access_code_locked') {
        window.location.reload()
        return
      }
      setError(err instanceof Error ? err.message : '访问码不正确，请重试。')
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="guest-root claim-root">
      <div className="guest-app claim-page">
        <header className="guest-top">
          <span>工单板 · 访客页</span>
        </header>
        <section className="head guest-title-card claim-card">
          <div className="meta">访问码验证</div>
          <h1>输入访问码</h1>
          <div className="claim-meta">
            链接备注：{info.note}
            <br />
            有效期至：{fmtDateCN(info.valid_until)}
          </div>
          {locked ? (
            <div className="claim-error">访问码已锁定，请联系 {info.inviter} 重置。</div>
          ) : (
            <>
              <label className="claim-label" htmlFor="guest-access-code">
                认领时显示的 6 位访问码
              </label>
              <input
                id="guest-access-code"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
              />
              {error && <div className="claim-error">{error}</div>}
              <button
                className="btn claim-button"
                disabled={busy || code.length !== 6}
                onClick={() => void enter()}
              >
                {busy ? '正在进入…' : '凭访问码进入'}
              </button>
              <div className="claim-footnote">丢失访问码请联系 {info.inviter}。</div>
            </>
          )}
        </section>
      </div>
    </main>
  )
}
