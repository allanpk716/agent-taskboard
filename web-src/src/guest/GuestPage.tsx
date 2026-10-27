// 访客网页（#38）：打开 /g/<密钥>。先 GET /api/v1/session（密钥直接作 Bearer）
// 查出链接指向哪张工单，再读工单渲染对话流。链接过期或撤销时整页提示原因和联系人。
// #66：持「可填写说明」权限且时间线上还没有访客的说明修订事件时，第一次
// 打开先看落地页（LandingPage），其余情况直接进对话页。
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from '../api'
import { Conversation } from './Conversation'
import { AccessCodePage, ClaimPage, type ClaimInfo } from './ClaimPage'
import { Composer } from './Composer'
import { GroupedBriefForm } from './GroupedBriefForm'
import { LandingPage } from './LandingPage'
import { guestApi } from './guestApi'
import { guestDeviceToken, guestRawApi, saveGuestDeviceToken } from './guestApi'
import { fmtDateCN } from '../shared/format'
import { VersionBanner } from '../shared/VersionBanner'
import { useVersionCheck } from '../shared/versionCheck'
import { Modal, copyText } from '../member/ui'
import type { GuestSession, TicketView } from '../shared/types'

type Mode = 'land' | 'talk' | 'fill'

type Phase =
  | { kind: 'loading' }
  | { kind: 'invalid'; code: string }
  | { kind: 'claim'; info: ClaimInfo }
  | { kind: 'enter'; info: ClaimInfo }
  | { kind: 'ready'; ticket: TicketView }

/** 访客侧的两个常驻 Agent 入口；开启与关闭的工单都显示。 */
function GuestHandoffDialog({
  ticket,
  linkKey,
  which,
  onClose,
}: {
  ticket: TicketView
  linkKey: string
  which: 'agent' | 'peer'
  onClose: () => void
}) {
  const [copied, setCopied] = useState(false)
  // 后端只保存链接密钥的哈希，拿不到明文地址；访客网页用当前页面地址拼。
  const guestLinkURL = `${window.location.origin}/g/${linkKey}`
  const text =
    which === 'agent'
      ? (ticket.guest_agent_instruction ?? '').replaceAll('{GUEST_LINK_URL}', guestLinkURL)
      : (ticket.handoff_share_text ?? '')

  async function copy() {
    const ok = await copyText(text)
    setCopied(ok)
    if (ok) setTimeout(() => setCopied(false), 2000)
  }

  return (
    <Modal
      title={which === 'agent' ? '🤖 复制给我的 Agent' : '📨 复制给对方的 Agent'}
      onClose={onClose}
    >
      {which === 'agent' ? (
        <div className="warnbox" data-guest-agent-warning>
          内含访问凭证（访客链接），只给你自己的 Agent，不要转发给其他人。
        </div>
      ) : (
        <div className="handoff-who">
          发给本部门处理这张工单的成员，对方的 Agent 用对方自己的 token；这段文案不带凭证。
        </div>
      )}
      <pre className="handoff-pre" data-handoff-preview>
        {text}
      </pre>
      <button type="button" className="btn handoff-copy" onClick={() => void copy()}>
        {copied ? '已复制' : which === 'agent' ? '复制访客 Agent 指令' : '复制交接文案'}
      </button>
      <div className="mrow handoff-foot">
        <span className="hint">
          {which === 'agent'
            ? '访问码不在文案里，复制后请自己补上或另行私下告诉你的 Agent。'
            : '对方看不到这张工单时，他的 Agent 打开会被拒绝。'}
        </span>
        <span className="sp" />
        <button className="btn ghost" onClick={onClose}>
          关闭
        </button>
      </div>
    </Modal>
  )
}

/** 链接失效整页提示（prototype a.html 的 expired 状态）。 */
function LinkInvalidPage({ code, expiresAt }: { code: string; expiresAt?: string }) {
  if (code === 'link_claimed') {
    return (
      <div className="full">
        <div style={{ fontSize: 42 }}>🔒</div>
        <h2>这条链接已被认领</h2>
        <p className="muted">链接只归第一位打开并确认的人。</p>
        <p className="muted">如果不是你认领的，请马上联系给你发链接的同事处理。</p>
      </div>
    )
  }
  const reason =
    code === 'link_expired'
      ? `链接已于 ${expiresAt ? fmtDateCN(expiresAt) : '到期时间'} 到期`
      : code === 'link_revoked'
        ? '链接已被发链接的人撤销'
        : '链接不正确或已失效'
  return (
    <div className="full">
      <div style={{ fontSize: 42 }}>⏳</div>
      <h2>这条链接已失效</h2>
      <p className="muted">{reason}。</p>
      <p className="muted">
        如需继续跟进，请联系给你发链接的同事，请对方重新发一条链接。
      </p>
    </div>
  )
}

export function GuestPage({ linkKey }: { linkKey: string }) {
  const [phase, setPhase] = useState<Phase>({ kind: 'loading' })
  const [expiresAt, setExpiresAt] = useState<string | undefined>()
  // #65：✏ 修改说明进入分组填写页，提交后回到对话；#66：第一次打开且
  // 还没补全过说明时先进落地页。
  const [mode, setMode] = useState<Mode>('talk')
  // 落地页只在首次加载时决策一次：之后发回复等刷新不再把人拉回落地页。
  const decidedRef = useRef(false)
  // 落地页「你的称呼」带进填写页的署名初值。
  const [authorSeed, setAuthorSeed] = useState<string | undefined>()
  const [handoff, setHandoff] = useState<'agent' | 'peer' | null>(null)
  // 版本检查（#262）：与成员端共用同一个 hook 与横幅。
  const versionCheck = useVersionCheck()
  const banner = <VersionBanner check={versionCheck} />

  const load = useCallback(async () => {
    try {
      if (!guestDeviceToken(linkKey)) {
        const info = await guestRawApi(linkKey, '/api/v1/guest-link')
        setPhase(info.state === 'unclaimed' ? { kind: 'claim', info } : { kind: 'enter', info })
        return
      }
      const sess: GuestSession = await guestApi(linkKey, '/api/v1/session')
      setExpiresAt(sess.viewer.link_expires_at)
      const ticket: TicketView = await guestApi(linkKey, sess.ticket.api)
      setPhase({ kind: 'ready', ticket })
      if (!decidedRef.current) {
        decidedRef.current = true
        // 落地页的显示条件（#66）：可填写说明，且时间线上还没有访客的
        // 说明修订事件（工单关闭后填写必失败，也直接进对话页）。
        const guestRevised = ticket.timeline.some(
          (e) => e.type === 'brief_revised' && !e.actor.member && !e.actor.agent,
        )
        const showLanding = ticket.viewer.can.edit_brief && ticket.status !== 'closed' && !guestRevised
        setMode(showLanding ? 'land' : 'talk')
      }
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        setPhase({ kind: 'invalid', code: err.code })
        return
      }
      // 瞬时网络问题等：提示稍后重试。
      setPhase({ kind: 'invalid', code: 'unreachable' })
    }
  }, [linkKey])

  useEffect(() => {
    void load()
  }, [load])

  if (phase.kind === 'invalid') {
    return (
      <>
        {banner}
        <main className="guest-root">
          <LinkInvalidPage code={phase.code} expiresAt={expiresAt} />
        </main>
      </>
    )
  }
  if (phase.kind === 'claim') {
    return (
      <>
        {banner}
        <ClaimPage
          info={phase.info}
          linkKey={linkKey}
          onClaimed={(token) => {
            saveGuestDeviceToken(linkKey, token)
            setPhase({ kind: 'loading' })
            void load()
          }}
        />
      </>
    )
  }
  if (phase.kind === 'enter') {
    return (
      <>
        {banner}
        <AccessCodePage
          info={phase.info}
          linkKey={linkKey}
          onEntered={(token) => {
            saveGuestDeviceToken(linkKey, token)
            setPhase({ kind: 'loading' })
            void load()
          }}
        />
      </>
    )
  }
  if (phase.kind !== 'ready') {
    return (
      <>
        {banner}
        <main className="guest-root">
          <p className="muted center">加载中…</p>
        </main>
      </>
    )
  }

  const { ticket } = phase
  const closed = ticket.status === 'closed'
  const contact = ticket.creator.member
    ? ticket.creator.member.name
    : ticket.assignee?.member
      ? ticket.assignee.member.name
      : '给你发链接的同事'

  if (mode === 'land') {
    return (
      <>
        {banner}
        <main className="guest-root landing-root">
          <LandingPage
            ticket={ticket}
            linkKey={linkKey}
            onStart={(author) => {
              setAuthorSeed(author)
              setMode('fill')
            }}
            onTalk={() => setMode('talk')}
          />
        </main>
      </>
    )
  }

  if (mode === 'fill') {
    // 分组填写页（#65）：模板工单按组折叠；无模板工单是一组「情况说明」（#66）。
    const leaveFill = () => {
      setAuthorSeed(undefined)
      setMode('talk')
    }
    return (
      <>
        {banner}
        <main className="guest-root fillroot">
          <GroupedBriefForm
            linkKey={linkKey}
            ticket={ticket}
            initialAuthor={authorSeed}
            onSaved={() => {
              leaveFill()
              void load()
            }}
            onBack={leaveFill}
            onLinkDead={(code) => setPhase({ kind: 'invalid', code })}
          />
        </main>
      </>
    )
  }

  return (
    <>
      {banner}
      <main className={`guest-root${closed || !ticket.viewer.can.reply ? ' readonly' : ''}`}>
        <div className="guest-handoff-bar" role="region" aria-label="Agent 入口">
          <button type="button" className="btn sm ghost" onClick={() => setHandoff('agent')}>
            🤖 复制给我的 Agent
          </button>
          <button type="button" className="btn sm ghost" onClick={() => setHandoff('peer')}>
            📨 复制给对方的 Agent
          </button>
        </div>
        {/* 工单关闭后不能再修订说明（服务端 409 ticket_closed），入口随之隐藏 */}
        <Conversation
          ticket={ticket}
          onEditBrief={ticket.viewer.can.edit_brief && ticket.status !== 'closed' ? () => setMode('fill') : undefined}
          fetchBriefVersions={async () =>
            guestApi(linkKey, `/api/v1/tickets/${encodeURIComponent(ticket.number)}/brief-versions`)
          }
        />
        {closed || !ticket.viewer.can.reply ? (
          <div className="ro">工单已关闭，只能查看。如还有问题，请联系 {contact}。</div>
        ) : (
          <Composer
            linkKey={linkKey}
            ticketNumber={ticket.number}
            onSent={load}
            onTicketClosed={load}
            onLinkDead={(code) => setPhase({ kind: 'invalid', code })}
          />
        )}
        {handoff && (
          <GuestHandoffDialog ticket={ticket} linkKey={linkKey} which={handoff} onClose={() => setHandoff(null)} />
        )}
        {versionCheck.serverVersion && (
          <footer className="guest-version">工单板 v{versionCheck.serverVersion}</footer>
        )}
      </main>
    </>
  )
}
