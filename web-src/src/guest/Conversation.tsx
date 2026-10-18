// 访客对话页的头部与整条对话（#38）：头部（部门平台 · 类型 · 编号 · 标题 ·
// 状态徽标 · 链接备注与到期日）+ 共用的说明气泡、回复气泡与系统事件行
//（shared/conversation，#41 成员工单详情同样使用）。
import { useMemo, useState } from 'react'
import type { TicketView } from '../shared/types'
import { closeReasonText, fmtDateCN } from '../shared/format'
import { BriefBubble, EventLine, ReplyBubble } from '../shared/conversation'
import { Lightbox } from '../shared/widgets'

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
  return (
    <>
      <header className="guest-top">
        <span>
          工单板 · {ticket.number} · {inviter} 请你反馈
        </span>
      </header>
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
}: {
  ticket: TicketView
  onEditBrief?: () => void
}) {
  const [lightbox, setLightbox] = useState<string | null>(null)
  const body = useMemo(
    () => (
      <>
        <GuestHeader ticket={ticket} />
        <BriefBubble ticket={ticket} onImageClick={setLightbox} onEditBrief={onEditBrief} />
        {ticket.timeline.map((e) =>
          e.type === 'reply' ? (
            <ReplyBubble key={e.seq} event={e} ticket={ticket} onImageClick={setLightbox} />
          ) : e.type === 'created' ? null : ( // 创建动作由说明气泡代表，不再重复一行
            <EventLine key={e.seq} event={e} />
          ),
        )}
      </>
    ),
    [ticket, onEditBrief],
  )
  return (
    <div className="guest-app">
      {body}
      {lightbox && <Lightbox url={lightbox} onClose={() => setLightbox(null)} />}
    </div>
  )
}
