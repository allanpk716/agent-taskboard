// 对话流里的共用小部件：头像、附件条、大图查看。
import { useEffect } from 'react'
import type { Attachment, Author } from './types'
import { authorName, avatarChar, fmtSize } from './format'

/** 头像：访客绿、成员蓝、成员 Agent 紫且带机器人角标（prototype a.html）。 */
export function Avatar({ author }: { author: Author | undefined | null }) {
  const display = authorName(author)
  const cls = author?.member ? (author.agent ? 'av a' : 'av m') : 'av g'
  return (
    <div className={cls} aria-hidden="true">
      {avatarChar(display)}
      {author?.agent && <span className="robot-badge">🤖</span>}
    </div>
  )
}

/** 附件条：列在气泡底部；点击打开（图片/PDF/txt/log 在线预览，其余由服务端按下载返回）。 */
export function AttachmentChips({ attachments }: { attachments: Attachment[] }) {
  if (attachments.length === 0) return null
  return (
    <div className="atts">
      {attachments.map((a) => (
        <a key={a.id} className="att" href={a.url} target="_blank" rel="noreferrer">
          📎 {a.filename} · {fmtSize(a.size)}
        </a>
      ))}
    </div>
  )
}

/** 点击插图查看大图。 */
export function Lightbox({ url, onClose }: { url: string; onClose: () => void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])
  return (
    <div className="lightbox" onClick={onClose} role="dialog" aria-label="查看大图">
      <img src={url} alt="" onClick={(e) => e.stopPropagation()} />
    </div>
  )
}
