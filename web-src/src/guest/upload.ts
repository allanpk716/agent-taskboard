// 两步上传的第一步：POST …/attachments（multipart）拿 att: 引用与现成的
// Markdown 片段（docs/api.md「附件」）。图片已在上传前压缩（compress.ts）。
import { guestApi } from './guestApi'

export interface UploadedAttachment {
  id: string
  filename: string
  size: number
  state: string
  markdown: string
}

export function uploadAttachment(linkKey: string, ticketNumber: string, file: File): Promise<UploadedAttachment> {
  const form = new FormData()
  form.append('file', file, file.name)
  return guestApi(linkKey, `/api/v1/tickets/${encodeURIComponent(ticketNumber)}/attachments`, {
    method: 'POST',
    body: form,
    idempotencyKey: crypto.randomUUID(),
  })
}
