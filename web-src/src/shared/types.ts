// GET /api/v1/tickets/{id} 的响应结构（docs/api.md）。访客与成员共用；
// 只声明两个网页都用到的字段。访客响应里没有 labels、没有标签事件。

export interface MemberRef {
  id: number
  name: string
  /** 成员账号已停用（#115）；正常账号没有这个字段。 */
  deactivated?: boolean
}

export interface AgentRef {
  id: number
  name: string
  revoked?: boolean // 已撤销的具名 Agent（历史署名标注「已停用」，评审 P2-10）
}

export interface Author {
  member?: MemberRef | null
  agent?: AgentRef | null
  link_note?: string | null
  author_name?: string | null
  via_agent?: boolean | null
}

export interface TemplateField {
  key: string
  title: string
  guide: string
  example: string
  required: boolean
  type: 'text' | 'radio' | 'dimension'
  options?: string[]
  dimension?: string
  /** 维度字段各候选值的别名（#39：下拉框按别名搜索；随字典现取） */
  option_aliases?: Record<string, string[]>
  /** 所属分组的序号（从 0 起，#63）；不分组的模板没有这个字段 */
  group?: number
}

/** 模板分组（#63）：组名与副标题；不分组时 groups 为空。 */
export interface TemplateGroup {
  title: string
  subtitle?: string
}

export interface Attachment {
  id: string
  filename: string
  size: number // 字节
  url: string // 访客拿到的是与链接绑定的短时效签名地址
  attached_to: string // 「说明」或「回复 N」
}

export interface TimelineEvent {
  seq: number
  change: number
  at: string
  actor: Author
  type: string
  note?: string
  reply_no?: number
  body_md?: string
  deliver?: boolean
  revision?: number
  reason?: string
  duplicate_of?: string
  expires_at?: string
  attachment_ids?: string[]
  from?: string
  to?: string
  collaborator?: string
  label?: string
  signature?: string
  drafting_count?: number
}

export interface DraftView {
  id: string
  kind: string
  status: string
  version: number
  ticket?: { number: string; seq: number; title: string; url: string } | null
  request: { body_md: string; attachment_ids?: string[]; deliver?: boolean }
  attachments: Attachment[]
  author: Author
  versions: { version: number; author: string; at: string }[]
  base: { timeline_seq: number }
  new_events: number
  preview_url: string
  url: string
}

export interface DraftHistoryVersion {
  version: number
  body_md: string
  attachment_ids: string[]
  author: string
  author_kind: 'member' | 'agent'
  at: string
}

export interface Collaborator {
  id: number
  name: string
  deactivated?: boolean
  added_at: string
}

export type MemberSummary = string

export interface Brief {
  fields?: Record<string, string> | null
  markdown: string
  revision: number
  /** 整张工单的附件（#130）；字段/正文内引用不在这里。 */
  attachment_ids: string[]
  last_revised: { type: 'member' | 'guest'; display_name: string; revision: number; at: string }
  template?: string | null
  template_fields?: TemplateField[]
  /** 创建时模板版本的分组（#63）；不分组时省略，由前端合成一组「填写说明」 */
  template_groups?: TemplateGroup[]
}

export interface BriefVersion {
  revision: number
  title: string
  markdown: string
  fields?: Record<string, string> | null
  attachment_ids: string[]
  author: Author
  at: string
}

export interface TicketView {
  number: string
  seq: number
  kind: 'task' | 'report'
  title: string
  status: 'open' | 'closed'
  close_reason?: 'done' | 'wontfix' | 'duplicate' | null
  template?: string | null
  creator: Author
  assignee?: Author | null
  collaborators?: Collaborator[]
  waiting_on: { party: string; member?: string | null; text: string; reason: string }
  /** 仅成员视角：工单当前标签（访客响应没有这个字段）。 */
  labels?: string[]
  my_draft?: DraftView | null
  brief: Brief
  attachments: Attachment[]
  viewer: {
    kind: string
    role: string
    your_turn: boolean
    link_note?: string
    link_expires_at?: string
    can: Record<string, boolean>
  }
  timeline: TimelineEvent[]
}

export interface GuestSession {
  viewer: {
    kind: 'guest'
    link_note: string
    link_expires_at: string
    can_edit_brief: boolean
  }
  ticket: { number: string; seq: number; title: string; api: string }
}

// GET /api/v1/intakes/{slug}（匿名，#35/#39）的响应结构。
export interface IntakeView {
  slug: string
  name: string
  url: string
  kind: string
  department_contact: string
  template: {
    key: string
    name: string
    version: number
    kind: string
    preset_labels: string[]
    agent_note?: string
    fields: TemplateField[]
  }
}

// POST /api/v1/intakes/{slug}/tickets（匿名）成功 201 的响应。
export interface IntakeSubmitResult {
  ticket: { number: string; seq: number; title: string; url: string }
  guest_link: { url: string; note: string; expires_at: string; can_edit_brief: boolean }
  device_token: string
  access_code: string
  department_contact: string
  next: string
}

// 422 missing_fields 附加在 error.fields 里的逐项提示（严格校验）。
export interface MissingFieldHint {
  field: string
  hint: string
  example?: string
}
