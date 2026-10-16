// 对话流（#38）：头部（部门平台 · 类型 · 编号 · 标题 · 状态徽标 · 链接备注与到期日）、
// 说明作为第一条气泡（按字段显示问句和回答，未填写的写「（未填写）」，附件列在
// 气泡底部）、回复气泡（成员/成员 Agent 各有头像和署名，Agent 带机器人角标，访客
// 自报的 Agent 只写「（自报）」）、系统事件居中灰色小字。
// #39（补全说明/反馈入口）与以后的成员工单页复用这些组件。
import { useMemo, useState } from 'react'
import type { Attachment, TicketView, TimelineEvent } from './types'
import { authorName, closeReasonText, fmtDateCN, fmtShort } from './format'
import { Markdown, inlineImageIds } from './markdown'
import { AttachmentChips, Avatar, Lightbox } from './widgets'

function attachmentsOf(ticket: TicketView): Map<string, Attachment> {
  return new Map(ticket.attachments.map((a) => [a.id, a]))
}

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
  return (
    <header className="head">
      <div className="meta">
        部门工单平台 · {ticket.kind === 'task' ? '任务' : '反馈'} · {ticket.number}（#{ticket.seq}）
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
    </header>
  )
}

/** 说明 = 第一条气泡：模板工单按字段逐个显示问句/回答，其余按 Markdown 渲染。 */
export function BriefBubble({
  ticket,
  onImageClick,
}: {
  ticket: TicketView
  onImageClick: (url: string) => void
}) {
  const atts = attachmentsOf(ticket)
  const fields = ticket.brief.template_fields ?? []
  const values = ticket.brief.fields ?? {}
  const briefAtts = ticket.attachments.filter((a) => a.attached_to === '说明')
  const created = ticket.timeline.find((e) => e.type === 'created')
  const creator = authorName(ticket.creator)
  return (
    <div className="bubble" data-bubble="brief">
      <Avatar author={ticket.creator} />
      <div className="card">
        <div className="who">
          <b>{creator}</b>
          {created && <> · {fmtShort(created.at)}</>} · 说明
        </div>
        {fields.length > 0 ? (
          fields.map((f) => {
            const v = (values[f.key] ?? '').trim()
            return (
              <div className="f" key={f.key}>
                <div className="q">{f.title}</div>
                {v ? (
                  <div className="v">
                    <Markdown body={v} attachments={atts} onImageClick={onImageClick} />
                  </div>
                ) : (
                  <div className="v empty">（未填写）</div>
                )}
              </div>
            )
          })
        ) : (
          <Markdown body={ticket.brief.markdown} attachments={atts} onImageClick={onImageClick} />
        )}
        <AttachmentChips attachments={briefAtts} />
      </div>
    </div>
  )
}

function eventText(e: TimelineEvent): string {
  switch (e.type) {
    case 'created':
      return '创建了工单'
    case 'assigned':
      return e.from ? `改派：${e.from} → ${e.to}` : `指派给 ${e.to}`
    case 'guest_link_created':
      return `创建了访客链接「${e.note}」`
    case 'guest_link_revoked':
      return `撤销了访客链接「${e.note}」`
    case 'guest_link_extended':
      return `延长了访客链接「${e.note}」${e.expires_at ? `至 ${fmtDateCN(e.expires_at)}` : ''}`
    case 'brief_revised':
      return `修订了说明（第 ${e.revision} 版）`
    case 'closed':
      return `关闭了工单（${closeReasonText(e.reason)}${e.duplicate_of ? `：${e.duplicate_of}` : ''}）`
    case 'reopened':
      return '重新开启了工单'
    default:
      return e.type
  }
}

/** 一条回复的气泡。 */
function ReplyBubble({
  event,
  ticket,
  onImageClick,
}: {
  event: TimelineEvent
  ticket: TicketView
  onImageClick: (url: string) => void
}) {
  const atts = attachmentsOf(ticket)
  const inline = inlineImageIds(event.body_md ?? '')
  const chips = (event.attachment_ids ?? [])
    .filter((id) => !inline.has(id))
    .map((id) => ticket.attachments.find((a) => a.id === id))
    .filter((a): a is Attachment => !!a)
  return (
    <div className="bubble" data-bubble="reply">
      <Avatar author={event.actor} />
      <div className="card">
        <div className="who">
          <b>{authorName(event.actor)}</b> · {fmtShort(event.at)}
          {event.deliver && <span className="deliver-tag">交付</span>}
        </div>
        <Markdown body={event.body_md ?? ''} attachments={atts} onImageClick={onImageClick} />
        <AttachmentChips attachments={chips} />
      </div>
    </div>
  )
}

/** 整条对话（不含底部输入区）。 */
export function Conversation({ ticket }: { ticket: TicketView }) {
  const [lightbox, setLightbox] = useState<string | null>(null)
  const body = useMemo(
    () => (
      <>
        <GuestHeader ticket={ticket} />
        <BriefBubble ticket={ticket} onImageClick={setLightbox} />
        {ticket.timeline.map((e) =>
          e.type === 'reply' ? (
            <ReplyBubble key={e.seq} event={e} ticket={ticket} onImageClick={setLightbox} />
          ) : e.type === 'created' ? null : ( // 创建动作由说明气泡代表，不再重复一行
            <div className="event" key={e.seq}>
              {authorName(e.actor)} {eventText(e)} · {fmtShort(e.at)}
            </div>
          ),
        )}
      </>
    ),
    [ticket],
  )
  return (
    <div className="guest-app">
      {body}
      {lightbox && <Lightbox url={lightbox} onClose={() => setLightbox(null)} />}
    </div>
  )
}
