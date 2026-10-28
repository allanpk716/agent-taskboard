// 访客对话页的头部与整条对话（#38）：头部（部门平台 · 类型 · 编号 · 标题 ·
// 状态徽标 · 链接备注与到期日）+ 共用的说明气泡、回复气泡与系统事件行
//（shared/conversation，#41 成员工单详情同样使用）。
import { useMemo, useRef, useState } from 'react'
import type { TicketView } from '../shared/types'
import { closeReasonText, fmtDateCN } from '../shared/format'
import { BriefBubble, EventLine, ReplyBubble } from '../shared/conversation'
import { Lightbox } from '../shared/widgets'
import { JumpToLatestReply } from '../shared/JumpToLatestReply'

/** 状态徽标：「● 现在轮到你回复」/「● 等待……处理」/「已关闭 · 原因」。 */
export function TurnBadge({ ticket }: { ticket: TicketView }) {
  if (ticket.status === 'closed') {
    return <span className="turn-badge done">已关闭 · {closeReasonText(ticket.close_reason)}</span>
  }
  if (ticket.viewer.your_turn) {
    return <span className="turn-badge you">● 现在轮到你回复</span>
  }
  const who =
    ticket.waiting_on.party === 'department'
      ? '部门'
      : ticket.waiting_on.party === 'none'
        ? '对方'
        : (ticket.waiting_on.member ?? '对方')
  return <span className="turn-badge them">● 等待{who}处理</span>
}

/** 头部。 */
export function GuestHeader({ ticket }: { ticket: TicketView }) {
  const inviter = ticket.creator.member?.name ?? '同事'
  const handler = ticket.assignee?.member?.name ?? inviter
  return (
    <>
      <header className="guest-top">
        <span>
          工单板 · {ticket.number} · {inviter} 请你反馈
        </span>
      </header>
      <div className="privacy-note persistent">🔒 只有你和处理人（{handler}）能看到这张工单。</div>
      <section className="head guest-title-card">
        <div className="meta">
          {ticket.kind === 'task' ? '任务' : '反馈'} · #{ticket.seq}
        </div>
        <h1>{ticket.title}</h1>
        <div className="badges">
          <TurnBadge ticket={ticket} />
          {ticket.viewer.link_note && (
            <span className="meta link-meta">
              你的链接：{ticket.viewer.link_note}
              {ticket.viewer.link_expires_at && ` · ${fmtDateCN(ticket.viewer.link_expires_at)}到期`}
            </span>
          )}
        </div>
      </section>
    </>
  )
}

/** 整条对话（不含底部输入区）。onEditBrief：持「可填写说明」权限的访客
 * 在说明卡片上点「✏ 修改说明」进入分组填写页（#65）。 */
export function Conversation({
  ticket,
  onEditBrief,
  fetchBriefVersions,
}: {
  ticket: TicketView
  onEditBrief?: () => void
  fetchBriefVersions?: () => Promise<{ versions: import('../shared/types').BriefVersion[] }>
}) {
  const [lightbox, setLightbox] = useState<string | null>(null)
  const latestReplyRef = useRef<HTMLDivElement | null>(null)
  let latestReplySeq: number | undefined
  for (let i = ticket.timeline.length - 1; i >= 0; i--) {
    if (ticket.timeline[i].type === 'reply') {
      latestReplySeq = ticket.timeline[i].seq
      break
    }
  }
  const body = useMemo(
    () => (
      <>
        <GuestHeader ticket={ticket} />
        <BriefBubble
          ticket={ticket}
          onImageClick={setLightbox}
          onEditBrief={onEditBrief}
          fetchVersions={fetchBriefVersions}
        />
        {ticket.timeline.map((e) =>
          e.type === 'reply' ? (
            <ReplyBubble
              key={e.seq}
              event={e}
              ticket={ticket}
              onImageClick={setLightbox}
              latestReply={e.seq === latestReplySeq}
              latestReplyRef={latestReplyRef}
            />
          ) : (
            <EventLine
              key={e.seq}
              event={e}
              onShowBriefVersion={(revision) =>
                window.dispatchEvent(new CustomEvent('tb:show-brief-version', { detail: revision }))
              }
            />
          ),
        )}
      </>
    ),
    [ticket, onEditBrief, fetchBriefVersions],
  )
  return (
    <div className="guest-app">
      {body}
      <JumpToLatestReply
        targetRef={latestReplyRef}
        targetKey={latestReplySeq}
        scrollTopOffset={52}
      />
      {lightbox && <Lightbox url={lightbox} onClose={() => setLightbox(null)} />}
    </div>
  )
}
