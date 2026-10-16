// 访客网页（#38）：打开 /g/<密钥>。先 GET /api/v1/session（密钥直接作 Bearer）
// 查出链接指向哪张工单，再读工单渲染对话流。链接过期或撤销时整页提示原因和联系人。
import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../api'
import { Conversation } from './Conversation'
import { Composer } from './Composer'
import { BriefForm } from './BriefForm'
import { guestApi } from './guestApi'
import { fmtDateCN } from './format'
import type { GuestSession, TicketView } from './types'

type Phase =
  | { kind: 'loading' }
  | { kind: 'invalid'; code: string }
  | { kind: 'ready'; ticket: TicketView }

/** 链接失效整页提示（prototype a.html 的 expired 状态）。 */
function LinkInvalidPage({ code, expiresAt }: { code: string; expiresAt?: string }) {
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
  // #39：✏ 补全说明进入字段表单，保存后回到对话。
  const [mode, setMode] = useState<'talk' | 'fill'>('talk')

  const load = useCallback(async () => {
    try {
      const sess: GuestSession = await guestApi(linkKey, '/api/v1/session')
      setExpiresAt(sess.viewer.link_expires_at)
      const ticket: TicketView = await guestApi(linkKey, sess.ticket.api)
      setPhase({ kind: 'ready', ticket })
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
      <main className="guest-root">
        <LinkInvalidPage code={phase.code} expiresAt={expiresAt} />
      </main>
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

  if (mode === 'fill') {
    return (
      <main className="guest-root">
        <BriefForm
          linkKey={linkKey}
          ticket={ticket}
          onSaved={() => {
            setMode('talk')
            void load()
          }}
          onBack={() => setMode('talk')}
          onLinkDead={(code) => setPhase({ kind: 'invalid', code })}
        />
      </main>
    )
  }

  return (
    <main className={`guest-root${closed || !ticket.viewer.can.reply ? ' readonly' : ''}`}>
      <Conversation ticket={ticket} />
      {closed || !ticket.viewer.can.reply ? (
        <div className="ro">工单已关闭，只能查看。如还有问题，请联系 {contact}。</div>
      ) : (
        <Composer
          linkKey={linkKey}
          ticketNumber={ticket.number}
          onSent={load}
          onTicketClosed={load}
          onLinkDead={(code) => setPhase({ kind: 'invalid', code })}
          onEditBrief={ticket.viewer.can.edit_brief ? () => setMode('fill') : undefined}
        />
      )}
    </main>
  )
}
