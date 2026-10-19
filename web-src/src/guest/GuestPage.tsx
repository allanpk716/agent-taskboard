// 访客网页（#38）：打开 /g/<密钥>。先 GET /api/v1/session（密钥直接作 Bearer）
// 查出链接指向哪张工单，再读工单渲染对话流。链接过期或撤销时整页提示原因和联系人。
// #66：持「可填写说明」权限且时间线上还没有访客的说明修订事件时，第一次
// 打开先看落地页（LandingPage），其余情况直接进对话页。
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from '../api'
import { Conversation } from './Conversation'
import { ClaimPage, type ClaimInfo } from './ClaimPage'
import { Composer } from './Composer'
import { GroupedBriefForm } from './GroupedBriefForm'
import { LandingPage } from './LandingPage'
import { guestApi } from './guestApi'
import { guestDeviceToken, guestRawApi, saveGuestDeviceToken } from './guestApi'
import { fmtDateCN } from '../shared/format'
import { fetchServerVersion } from '../shared/health'
import type { GuestSession, TicketView } from '../shared/types'

type Mode = 'land' | 'talk' | 'fill'

type Phase =
  | { kind: 'loading' }
  | { kind: 'invalid'; code: string }
  | { kind: 'claim'; info: ClaimInfo }
  | { kind: 'ready'; ticket: TicketView }

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
  const [serverVersion, setServerVersion] = useState<string | null>(null)

  const load = useCallback(async () => {
    try {
      if (!guestDeviceToken(linkKey)) {
        const info = await guestRawApi(linkKey, '/api/v1/guest-link')
        setPhase({ kind: 'claim', info })
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

  useEffect(() => {
    fetchServerVersion()
      .then(setServerVersion)
      .catch(() => setServerVersion(null))
  }, [])

  if (phase.kind === 'invalid') {
    return (
      <main className="guest-root">
        <LinkInvalidPage code={phase.code} expiresAt={expiresAt} />
      </main>
    )
  }
  if (phase.kind === 'claim') {
    return (
      <ClaimPage
        info={phase.info}
        linkKey={linkKey}
        onClaimed={(token) => {
          saveGuestDeviceToken(linkKey, token)
          setPhase({ kind: 'loading' })
          void load()
        }}
      />
    )
  }
  if (phase.kind !== 'ready') {
    return (
      <main className="guest-root">
        <p className="muted center">加载中…</p>
      </main>
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
      <main className="guest-root landing-root">
        <LandingPage
          ticket={ticket}
          onStart={(author) => {
            setAuthorSeed(author)
            setMode('fill')
          }}
          onTalk={() => setMode('talk')}
        />
      </main>
    )
  }

  if (mode === 'fill') {
    // 分组填写页（#65）：模板工单按组折叠；无模板工单是一组「情况说明」（#66）。
    const leaveFill = () => {
      setAuthorSeed(undefined)
      setMode('talk')
    }
    return (
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
    )
  }

  return (
    <main className={`guest-root${closed || !ticket.viewer.can.reply ? ' readonly' : ''}`}>
      {/* 工单关闭后不能再修订说明（服务端 409 ticket_closed），入口随之隐藏 */}
      <Conversation
        ticket={ticket}
        onEditBrief={ticket.viewer.can.edit_brief && ticket.status !== 'closed' ? () => setMode('fill') : undefined}
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
      {serverVersion && <footer className="guest-version">工单板 v{serverVersion}</footer>}
    </main>
  )
}
