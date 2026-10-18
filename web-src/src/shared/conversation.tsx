// 对话流的共用气泡与事件行（#38 访客网页 / #41 成员工单详情都用）：
// 说明 = 第一条气泡（按字段显示问句和回答）、回复气泡（头像 + 署名 + Markdown
// + 附件条）、居中灰色系统事件行。成员视角比访客多看到标签事件。
import type { TicketView, TimelineEvent } from './types'
import { authorName, closeReasonText, fmtDateCN, fmtShort } from './format'
import { Markdown, inlineImageIds } from './markdown'
import { AttachmentChips, Avatar, DeactivatedBadge } from './widgets'
import type { Attachment } from './types'

/** 事件文案（成员视角可见全部类型；访客看不到标签事件，服务端已过滤）。 */
export function eventText(e: TimelineEvent): string {
  switch (e.type) {
    case 'created':
      return '创建了工单'
    case 'assigned':
      return e.from ? `改派：${e.from} → ${e.to}` : `指派给 ${e.to}`
    case 'label_added':
      return `打上了标签 ${e.label}`
    case 'label_removed':
      return `摘掉了标签 ${e.label}`
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

/** 居中的灰色系统事件行。 */
export function EventLine({ event }: { event: TimelineEvent }) {
  return (
    <div className="event">
      {authorName(event.actor)}
      {event.actor.member?.deactivated && <DeactivatedBadge />}
      {eventText(event)} · {fmtShort(event.at)}
    </div>
  )
}

function attachmentsOf(ticket: TicketView): Map<string, Attachment> {
  return new Map(ticket.attachments.map((a) => [a.id, a]))
}

/** 说明 = 第一条气泡：模板工单按字段逐个显示问句/回答，其余按 Markdown 渲染。
 * onEditBrief（访客可填写说明时）：卡片上有「✏ 修改说明」，点开回到分组填写页（#65）。 */
export function BriefBubble({
  ticket,
  onImageClick,
  onEditBrief,
}: {
  ticket: TicketView
  onImageClick: (url: string) => void
  onEditBrief?: () => void
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
          {ticket.creator.member?.deactivated && <DeactivatedBadge />}
          {created && <> · {fmtShort(created.at)}</>} · 说明
          {onEditBrief && (
            <button type="button" className="btn ghost sm edit-brief" onClick={onEditBrief}>
              ✏ 修改说明
            </button>
          )}
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

/** 一条回复的气泡。 */
export function ReplyBubble({
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
    <div className="bubble" data-bubble="reply" data-author-kind={event.actor.agent ? 'agent' : 'member'}>
      <Avatar author={event.actor} />
      <div className="card">
        <div className="who">
          <b>{authorName(event.actor)}</b>
          {event.actor.member?.deactivated && <DeactivatedBadge />} · {fmtShort(event.at)}
          {event.deliver && <span className="deliver-tag">交付</span>}
        </div>
        <Markdown body={event.body_md ?? ''} attachments={atts} onImageClick={onImageClick} />
        <AttachmentChips attachments={chips} />
      </div>
    </div>
  )
}
