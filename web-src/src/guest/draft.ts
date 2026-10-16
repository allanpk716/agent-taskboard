// 草稿：以「访客链接 + 工单」为键实时写入 localStorage（覆盖回复，包括已上传
// 附件的 att: 引用——引用就在正文里），收到 2xx 后才清除；服务中断时保留并带
// 同一个 Idempotency-Key 自动重试（ADR 0002）。

export interface Draft {
  body: string
  author: string
  /** 这份草稿有一次未成功的发送：恢复后用同一个 key 自动重试。 */
  idemKey?: string
  pending?: boolean
}

export function draftKey(linkKey: string, ticketNumber: string): string {
  return `tb:guest:draft:${linkKey}:${ticketNumber}`
}

export function loadDraft(key: string): Draft | null {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return null
    const d = JSON.parse(raw)
    if (typeof d?.body !== 'string') return null
    return { body: d.body, author: typeof d.author === 'string' ? d.author : '', idemKey: d.idemKey, pending: !!d.pending }
  } catch {
    return null
  }
}

export function saveDraft(key: string, draft: Draft) {
  try {
    localStorage.setItem(key, JSON.stringify(draft))
  } catch {
    // 存储不可用（隐私模式等）就退化为不保存，发送功能不受影响。
  }
}

export function clearDraft(key: string) {
  try {
    localStorage.removeItem(key)
  } catch {}
}

/** 署名在整个浏览器里记住（跨工单复用）。 */
const AUTHOR_KEY = 'tb:guest:author'

export function loadAuthor(): string {
  try {
    return localStorage.getItem(AUTHOR_KEY) ?? ''
  } catch {
    return ''
  }
}

export function saveAuthor(name: string) {
  try {
    localStorage.setItem(AUTHOR_KEY, name)
  } catch {}
}

// #39：补全说明与反馈入口的表单草稿也是实时写入 localStorage（同一个“键 =
// 凭证/入口 + 工单”思路）；文本字段值里的 att: 引用随字段值一并保存。
export interface FormDraft {
  title?: string
  fields: Record<string, string>
  author?: string
  idemKey?: string
  pending?: boolean
}

export function loadFormDraft(key: string): FormDraft | null {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return null
    const d = JSON.parse(raw)
    if (typeof d?.fields !== 'object' || d.fields === null) return null
    return d
  } catch {
    return null
  }
}

export function saveFormDraft(key: string, draft: FormDraft) {
  try {
    localStorage.setItem(key, JSON.stringify(draft))
  } catch {}
}

export function clearFormDraft(key: string) {
  try {
    localStorage.removeItem(key)
  } catch {}
}

/** 补全说明的草稿键（对话回复草稿的键再加 :brief 后缀，互不覆盖）。 */
export function briefDraftKey(linkKey: string, ticketNumber: string): string {
  return `${draftKey(linkKey, ticketNumber)}:brief`
}

/** 反馈入口提交页的草稿键（还没有链接，以入口为单位）。 */
export function intakeDraftKey(slug: string): string {
  return `tb:guest:intake:${slug}`
}
