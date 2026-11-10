import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../api'
import { Modal, copyText } from '../member/ui'
import { VersionBanner } from '../shared/VersionBanner'
import { fmtDateCN } from '../shared/format'
import { useVersionCheck } from '../shared/versionCheck'
import { guestApi, guestRawApi, saveGuestDeviceToken } from './guestApi'

interface GuestHomeTicket {
  number: string
  title: string
  status: string
  api: string
  waiting_on_you: boolean
  reason?: string
  read_only: boolean
  expires_at: string
  can_edit_brief: boolean
}

interface GuestHome {
  state: 'unclaimed' | 'claimed' | 'access_code_locked'
  guest_name: string
  tickets: GuestHomeTicket[]
  guest_agent_instruction: string
}

type Phase =
  | { kind: 'loading' }
  | { kind: 'claim'; home: GuestHome }
  | { kind: 'enter'; home: GuestHome }
  | { kind: 'ready'; home: GuestHome }

export function GuestHomePage({ homeKey }: { homeKey: string }) {
  const [phase, setPhase] = useState<Phase>({ kind: 'loading' })
  const [name, setName] = useState('')
  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [issued, setIssued] = useState<{ token: string; code: string } | null>(null)
  const [instructionOpen, setInstructionOpen] = useState(false)
  const [copied, setCopied] = useState(false)
  const versionCheck = useVersionCheck()
  const banner = <VersionBanner check={versionCheck} />

  const load = useCallback(async () => {
    setBusy(true)
    setError('')
    try {
      const token = localStorage.getItem('tb:guest-device')
      if (token) {
        const home: GuestHome = await guestApi(homeKey, '/api/v1/guest-home')
        setPhase({ kind: 'ready', home })
        return
      }
      const home: GuestHome = await guestRawApi(homeKey, '/api/v1/guest-home')
      setPhase(home.state === 'unclaimed' ? { kind: 'claim', home } : { kind: 'enter', home })
    } catch (err) {
      setError(err instanceof Error ? err.message : '主页暂时打不开，请稍后重试。')
    } finally {
      setBusy(false)
    }
  }, [homeKey])

  useEffect(() => {
    void load()
  }, [load])

  async function claim() {
    if (!name.trim()) {
      setError('请填写你的称呼。')
      return
    }
    setBusy(true)
    setError('')
    try {
      const res = await guestRawApi(homeKey, '/api/v1/guest-home/claim', {
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

  async function enter() {
    setBusy(true)
    setError('')
    try {
      const res = await guestRawApi(homeKey, '/api/v1/guest-home/enter', {
        method: 'POST',
        body: JSON.stringify({ access_code: code.trim() }),
      })
      saveGuestDeviceToken(homeKey, res.device_token as string)
      await load()
    } catch (err) {
      if (err instanceof ApiError && err.code === 'access_code_locked') {
        setError('访问码已锁定，请联系给你发主页的同事重置。')
        return
      }
      setError(err instanceof Error ? err.message : '访问码不正确，请重试。')
    } finally {
      setBusy(false)
    }
  }

  const instruction = (phase.kind === 'ready' || phase.kind === 'claim' || phase.kind === 'enter'
    ? phase.home.guest_agent_instruction
    : ''
  ).replaceAll('{GUEST_HOME_URL}', `${window.location.origin}/h/${homeKey}`)

  return (
    <>
      {banner}
      <main className="guest-root guest-home-root">
        <header className="guest-top">
          <span>工单板 · 访客主页</span>
        </header>
        <section className="head guest-title-card guest-home-card" data-guest-home>
          <div className="meta">访客主页</div>
          <h1>{phase.kind === 'loading' ? '加载中…' : phase.home.guest_name || '访客'}</h1>
          <div className="mrow">
            <button type="button" className="btn sm ghost" onClick={() => setInstructionOpen(true)}>
              🤖 复制主页版 Agent 指令
            </button>
            <button type="button" className="btn sm ghost" onClick={() => void load()}>
              刷新
            </button>
          </div>
        </section>

        {error && <div className="errbox" style={{ margin: '8px 16px' }}>{error}</div>}

        {phase.kind === 'loading' && <p className="muted center">正在打开主页…</p>}

        {phase.kind === 'claim' && (
          <section className="head guest-title-card guest-home-card">
            <h2>请确认是你本人打开</h2>
            <label className="claim-label" htmlFor="guest-home-name">你的称呼</label>
            <input
              id="guest-home-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="如：检验科 张三"
            />
            {issued ? (
              <>
                <div className="access-code-card">
                  <div>访问码</div>
                  <strong data-access-code>{issued.code}</strong>
                  <p>只显示这一次，请截图保存；换设备时凭它重新进入。</p>
                </div>
                <button
                  className="btn claim-button"
                  onClick={() => {
                    saveGuestDeviceToken(homeKey, issued.token)
                    setIssued(null)
                    void load()
                  }}
                >
                  我已保存，打开主页
                </button>
              </>
            ) : (
              <button className="btn claim-button" disabled={busy} onClick={() => void claim()}>
                {busy ? '正在打开…' : '是我，打开主页'}
              </button>
            )}
          </section>
        )}

        {phase.kind === 'enter' && (
          <section className="head guest-title-card guest-home-card">
            <h2>输入访问码进入</h2>
            <label className="claim-label" htmlFor="guest-home-code">6 位访问码</label>
            <input
              id="guest-home-code"
              value={code}
              inputMode="numeric"
              maxLength={6}
              onChange={(e) => setCode(e.target.value)}
            />
            <button className="btn claim-button" disabled={busy} onClick={() => void enter()}>
              {busy ? '正在进入…' : '进入主页'}
            </button>
          </section>
        )}

        {phase.kind === 'ready' && (
          <section className="guest-home-list">
            {phase.home.tickets.length === 0 && (
              <p className="muted center">现在还没有你能参与的工单。</p>
            )}
            {phase.home.tickets.map((ticket) => (
              <a
                key={ticket.number}
                className="guest-home-ticket"
                data-ticket-number={ticket.number}
                data-waiting={ticket.waiting_on_you ? 'yes' : 'no'}
                data-read-only={ticket.read_only ? 'yes' : 'no'}
                href={`/h/${encodeURIComponent(homeKey)}/t/${encodeURIComponent(ticket.number)}`}
              >
                <div>
                  <strong>{ticket.title}</strong>
                  <div className="muted">
                    {ticket.number} · {ticket.read_only ? '已关闭，只读' : `可参与至 ${fmtDateCN(ticket.expires_at)}`}
                  </div>
                </div>
                {ticket.waiting_on_you ? (
                  <span className="turn-badge you">{ticket.reason || '轮到你处理'}</span>
                ) : ticket.read_only ? (
                  <span className="turn-badge done">只读</span>
                ) : (
                  <span className="turn-badge them">等对方</span>
                )}
              </a>
            ))}
          </section>
        )}

        {versionCheck.serverVersion && (
          <footer className="guest-version">工单板 v{versionCheck.serverVersion}</footer>
        )}
      </main>

      {instructionOpen && (
        <Modal title="🤖 复制主页版 Agent 指令" onClose={() => setInstructionOpen(false)}>
          <div className="warnbox" data-guest-agent-warning>
            内含主页凭证，只给你自己的 Agent，不要转发给其他人；访问码由你自己另行补给它。
          </div>
          <pre className="handoff-pre">{instruction}</pre>
          <button
            type="button"
            className="btn handoff-copy"
            onClick={async () => {
              const ok = await copyText(instruction)
              setCopied(ok)
              if (ok) setTimeout(() => setCopied(false), 2000)
            }}
          >
            {copied ? '已复制' : '复制主页版 Agent 指令'}
          </button>
        </Modal>
      )}
    </>
  )
}
