import { useState } from 'react'
import { guestRawApi } from './guestApi'
import { fmtDateCN } from '../shared/format'

export interface ClaimInfo {
  state: 'unclaimed'
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
      onClaimed(res.device_token as string)
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
          {error && <div className="claim-error">{error}</div>}
          <button className="btn claim-button" disabled={busy} onClick={() => void claim()}>
            {busy ? '正在打开…' : '是我，打开工单'}
          </button>
          <div className="claim-footnote">不是你？请不要打开，告诉 {info.inviter}。</div>
        </section>
      </div>
    </main>
  )
}
