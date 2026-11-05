// 对话流里的共用小部件：头像、附件条、大图查看。
import { useEffect } from 'react'
import type { Attachment, Author, TicketSlim } from './types'
import { authorName, avatarChar, fmtDateCN, fmtSize } from './format'

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

/** 已停用成员署名旁的灰色小徽标（#115）。 */
export function DeactivatedBadge() {
  return <span className="deactivated-badge">已停用</span>
}

/** 瘦身横幅（#284）：详情页与访客页顶部提示附件去哪了、怎么取回。 */
export function SlimBanner({ slim }: { slim: TicketSlim | null | undefined }) {
  if (!slim || slim.status === 'none' || slim.removed_count === 0) return null
  const when = slim.last_removed_at ? fmtDateCN(slim.last_removed_at) : ''
  return (
    <div className="slim-banner" data-testid="slim-banner" role="status">
      本工单的 {slim.removed_count} 个附件已于{when}瘦身移出，见包{' '}
      <span className="mono">{slim.packages.join('、')}</span>，需要时联系管理员。
    </div>
  )
}

/** 附件条：列在气泡底部；点击打开（图片/PDF/txt/log 在线预览，其余由服务端按下载返回）。 */
export function AttachmentChips({
  attachments,
  onImageClick,
}: {
  attachments: Attachment[]
  onImageClick?: (url: string) => void
}) {
  if (attachments.length === 0) return null
  return (
    <div className="atts">
      {attachments.map((a) => (
        a.slim_removed_at ? (
          <span
            key={a.id}
            className="att slimmed"
            data-testid={`slim-attachment-${a.id}`}
            title="附件已瘦身移出服务器，下载置灰"
          >
            📎 {a.filename} · {fmtSize(a.size)} · {fmtDateCN(a.slim_removed_at)}移出 · 包{' '}
            {a.slim_package_name}
            <button type="button" disabled>
              下载
            </button>
          </span>
        ) : isImageAttachment(a) && onImageClick ? (
          <button type="button" key={a.id} className="att" onClick={() => onImageClick(a.url)}>
            📎 {a.filename} · {fmtSize(a.size)}
          </button>
        ) : (
          <a key={a.id} className="att" href={a.url} target="_blank" rel="noreferrer">
            📎 {a.filename} · {fmtSize(a.size)}
          </a>
        )
      ))}
    </div>
  )
}

function isImageAttachment(attachment: Attachment) {
  return /\.(?:png|jpe?g|gif|webp)$/i.test(attachment.filename)
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
