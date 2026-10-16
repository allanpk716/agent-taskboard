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
