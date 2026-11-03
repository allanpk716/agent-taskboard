import { fmtBeijing } from '../shared/format'

export interface InboxActor {
  type: 'member' | 'guest' | 'member_agent' | string
  name: string
  member?: { id: number; name: string; deactivated?: boolean } | null
  agent?: { id: number; name: string } | null
}

export interface InboxCounterpart {
  type: 'member' | 'guest' | 'self' | 'pending_triage' | string
  name?: string | null
  member?: { id: number; name: string; deactivated?: boolean } | null
}

export interface InboxPhraseInput {
  reason_code: string
  trigger: InboxActor
  role: 'assignee' | 'creator' | 'triager' | string
  counterpart: InboxCounterpart
}

/** 收件箱卡片第一行：让工单进入收件箱的一方 + 做了什么。 */
export function inboxPhrase(item: InboxPhraseInput, me?: { name: string } | string): string {
  const viewer = typeof me === 'string' ? me : me?.name
  const actor = item.trigger
  const memberName = actor.member?.name ?? actor.name
  const memberActorLabel = () => {
    if (actor.type !== 'member_agent') return memberName
    return actor.member?.name === viewer ? '你的 Agent' : `${actor.member?.name ?? memberName} 的 Agent`
  }
  switch (item.reason_code) {
    case 'delivered':
      return `${memberActorLabel()} 交付了，等你验收`
    case 'assignee_replied':
    case 'creator_replied':
      return `${memberActorLabel()} 回复了，问你一个问题`
    case 'sent_back':
      return `${memberActorLabel()} 退回了，要你修改`
    case 'agent_replied':
      return `${memberActorLabel()} 回复了`
    case 'guest_replied':
      return `访客 ${actor.name || '访客'} 回复了`
    case 'guest_completed':
      return `访客 ${actor.name || '访客'} 补全了说明`
    case 'guest_revised':
      return `访客 ${actor.name || '访客'} 修订了说明`
    case 'guest_not_completed':
      return `访客 ${actor.name || '访客'} 还没填说明`
    case 'pending_triage':
      return `${memberActorLabel()} 新建，等你初筛`
    case 'new':
    default:
      return `${memberActorLabel()} 派给你`
  }
}

/** 「在等他」参考卡的弱化短句。 */
export function waitingOnCounterpartPhrase(item: InboxPhraseInput): string {
  const counterpart = item.counterpart
  if (counterpart.type === 'guest') return `等 访客 ${counterpart.name || '访客'} 回复`
  const name = counterpart.member?.name ?? counterpart.name ?? '对方'
  return `等 ${name} ${item.role === 'assignee' ? '回复' : '处理'}`
}

/** 最后回复时间；无回复时用创建时间并写明「创建于」。 */
export function inboxActivityTime(item: {
  last_reply_at?: string | null
  created_at: string
}): { label: string; full: string; iso: string; hasReply: boolean } {
  const iso = item.last_reply_at ?? item.created_at
  const relative = relativeInboxTime(iso)
  return {
    label: item.last_reply_at ? relative : `创建于 ${relative}`,
    full: fmtBeijing(iso),
    iso,
    hasReply: Boolean(item.last_reply_at),
  }
}

/** 收件箱用相对时间：刚刚 / N 分钟前 / N 小时前 / 昨天 HH:mm / N 天前。 */
export function relativeInboxTime(iso: string, now = new Date()): string {
  const at = new Date(iso).getTime()
  const diff = now.getTime() - at
  if (!Number.isFinite(at) || diff < 60_000) return '刚刚'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`
  const beijingParts = (value: Date) =>
    new Intl.DateTimeFormat('en-US', {
      timeZone: 'Asia/Shanghai',
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
    }).format(value)
  const yesterday = new Date(now.getTime() - 86_400_000)
  if (beijingParts(yesterday) === beijingParts(new Date(at))) {
    return `昨天 ${fmtBeijing(iso).slice(11)}`
  }
  return `${Math.floor(diff / 86_400_000)} 天前`
}

/** 原因色条：沿用收件箱既有的新指派 / 交付 / 访客 / 退回配色。 */
export function inboxReasonClass(reasonCode: string): string {
  if (reasonCode === 'delivered') return 'delivered'
  if (reasonCode.startsWith('guest_')) return 'guest'
  if (reasonCode === 'sent_back') return 'back'
  return 'new'
}
