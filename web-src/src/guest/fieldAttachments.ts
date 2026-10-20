// 字段值与字段附件的拆分/拼接。填写页把纯文本留在 textarea 里，把附件
// 引用单独挂在暂存列表上；提交时仍按上传时的规则追加到文本末尾。

export interface FieldAttachmentDraft {
  attachmentId: string
  name: string
  size?: number
  image?: boolean
  /** 上传接口返回的原始 Markdown 引用；旧草稿缺失时由解析结果补齐。 */
  markdown: string
}

const ATTACHMENT_REF = /(!?)\[([^\]]*)\]\(att:([A-Za-z0-9_]+)\)/g

export interface SplitFieldValue {
  text: string
  attachments: FieldAttachmentDraft[]
}

export function splitFieldValue(value: string | undefined): SplitFieldValue {
  const attachments: FieldAttachmentDraft[] = []
  const text = (value ?? '').replace(ATTACHMENT_REF, (_, bang: string, name: string, id: string) => {
    const markdown = `${bang}[${name}](att:${id})`
    attachments.push({
      attachmentId: id,
      name: name || id,
      image: bang === '!',
      markdown,
    })
    return ''
  })
  return { text: normalizeStrippedText(text), attachments }
}

export function joinFieldValue(text: string | undefined, attachments: readonly FieldAttachmentDraft[]): string {
  return attachments.reduce(
    (value, attachment) =>
      value.trim() === '' ? attachment.markdown : `${value.replace(/\s+$/, '')}\n${attachment.markdown}`,
    text ?? '',
  )
}

export function filledFieldValue(text: string | undefined, hasAttachment = false): boolean {
  return !!(text ?? '').trim() || hasAttachment
}

export interface SplitFormDraftValues {
  fields: Record<string, string>
  fieldAttachments: Record<string, FieldAttachmentDraft[]>
}

/** 兼容旧草稿：从字段文本里解析 att: 引用；新草稿的附件元数据优先。 */
export function splitFormDraftValues(
  fields: Record<string, string>,
  stored: Record<string, FieldAttachmentDraft[]> = {},
): SplitFormDraftValues {
  const out: SplitFormDraftValues = { fields: {}, fieldAttachments: {} }
  for (const [key, value] of Object.entries(fields)) {
    const split = splitFieldValue(value)
    out.fields[key] = split.text
    const byId = new Map((stored[key] ?? []).map((a) => [a.attachmentId, a]))
    for (const parsed of split.attachments) {
      if (!byId.has(parsed.attachmentId)) byId.set(parsed.attachmentId, parsed)
    }
    const attachments = [...byId.values()]
    if (attachments.length > 0) out.fieldAttachments[key] = attachments
  }
  return out
}

function normalizeStrippedText(text: string): string {
  const lines = text.split('\n')
  while (lines.length > 0 && lines[0].trim() === '') lines.shift()
  while (lines.length > 0 && lines[lines.length - 1].trim() === '') lines.pop()
  return lines.join('\n')
}
