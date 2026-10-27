// 成员端未提交的输入（#262）：建单表单、工单回复框、说明修订框里已经填写、
// 还没提交的内容实时写进本机 localStorage，页面刷新后自动填回；提交成功或
// 用户清空后清除。它不是「草稿」——草稿是成员 Agent 代写、存在平台上等成员
// 确认的内容（见 GLOSSARY），这里只保存在这台设备上，键里带成员身份和对象，
// 多账号、多工单互不串内容。localStorage 不可用（隐私模式等）时静默降级。

export interface UnsentNewTicket {
  title: string
  kind: string
  /** 自由格式的整块 Markdown 说明。 */
  brief: string
  /** 模板字段的键 → 值。 */
  values: Record<string, string>
  labels: string[]
  labelInput: string
  assignee: string
}

export interface UnsentReply {
  body: string
  deliver: boolean
}

export interface UnsentBriefRevision {
  /** 开始编辑时的说明版本；恢复后仍用它做乐观锁基线。 */
  baseRevision?: number
  /** 模板说明的字段值；自由格式说明没有这项。 */
  fields?: Record<string, string>
  /** 自由格式说明的整块 Markdown。 */
  markdown?: string
  /** 已上传并挂进说明的附件 id（文本里的 att: 引用随正文一起保存）。 */
  attachmentIds?: string[]
  /** 新上传附件的文件名：刷新后工单数据里还没有，靠它显示可读的名字。 */
  attachmentNames?: Record<string, string>
}

function read(key: string): unknown | null {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return null
    return JSON.parse(raw)
  } catch {
    return null
  }
}

function write(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value))
  } catch {
    // 存储不可用（隐私模式等）就退化为不保存，页面功能不受影响。
  }
}

export function clearUnsent(key: string) {
  try {
    localStorage.removeItem(key)
  } catch {}
}

/** 建单表单的键按模板区分：换一个模板（或自由格式）重新开始，互不覆盖。 */
export function unsentNewTicketKey(memberId: number, templateKey: string | null): string {
  return `tb:unsent:${memberId}:new-ticket:${templateKey ?? 'free'}`
}

export function unsentReplyKey(memberId: number, ticketNumber: string): string {
  return `tb:unsent:${memberId}:reply:${ticketNumber}`
}

export function unsentBriefKey(memberId: number, ticketNumber: string): string {
  return `tb:unsent:${memberId}:brief:${ticketNumber}`
}

export function loadUnsentNewTicket(key: string): UnsentNewTicket | null {
  const d = read(key)
  if (typeof d !== 'object' || d === null || typeof (d as UnsentNewTicket).title !== 'string') return null
  const v = d as UnsentNewTicket
  return {
    title: v.title,
    kind: typeof v.kind === 'string' ? v.kind : 'task',
    brief: typeof v.brief === 'string' ? v.brief : '',
    values: typeof v.values === 'object' && v.values !== null ? v.values : {},
    labels: Array.isArray(v.labels) ? v.labels.filter((l) => typeof l === 'string') : [],
    labelInput: typeof v.labelInput === 'string' ? v.labelInput : '',
    assignee: typeof v.assignee === 'string' ? v.assignee : '',
  }
}

export function saveUnsentNewTicket(key: string, value: UnsentNewTicket) {
  write(key, value)
}

export function loadUnsentReply(key: string): UnsentReply | null {
  const d = read(key)
  if (typeof d !== 'object' || d === null || typeof (d as UnsentReply).body !== 'string') return null
  return { body: (d as UnsentReply).body, deliver: !!(d as UnsentReply).deliver }
}

export function saveUnsentReply(key: string, value: UnsentReply) {
  write(key, value)
}

export function loadUnsentBrief(key: string): UnsentBriefRevision | null {
  const d = read(key)
  if (typeof d !== 'object' || d === null) return null
  const v = d as UnsentBriefRevision
  if (typeof v.fields !== 'object' && typeof v.markdown !== 'string') return null
  return {
    baseRevision: typeof v.baseRevision === 'number' ? v.baseRevision : undefined,
    fields: typeof v.fields === 'object' && v.fields !== null ? v.fields : undefined,
    markdown: typeof v.markdown === 'string' ? v.markdown : undefined,
    attachmentIds: Array.isArray(v.attachmentIds)
      ? v.attachmentIds.filter((id) => typeof id === 'string')
      : undefined,
    attachmentNames:
      typeof v.attachmentNames === 'object' && v.attachmentNames !== null ? v.attachmentNames : undefined,
  }
}

export function saveUnsentBrief(key: string, value: UnsentBriefRevision) {
  write(key, value)
}
