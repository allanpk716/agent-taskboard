// 极简 Markdown 渲染（照 prototype/guest-web shared.js 的 md()，React 化）：
// 段落、粗体、行内代码、有序/无序列表、正文中的 att: 引用（插图/附件）
// 以及 UNC（NAS）路径文本块（带「复制路径」按钮，不做成链接）。
import { useEffect, useState, type ReactNode } from 'react'
import type { Attachment } from './types'

export interface MarkdownProps {
  body: string
  /** id → 附件（拿签名下载地址）。 */
  attachments: Map<string, Attachment>
  /** 点击插图查看大图。 */
  onImageClick?: (url: string) => void
}

// UNC 路径：\\server\share\…（反斜杠两道开头，后面至少一段 \xxx）。
const UNC_RE = /\\\\[^\s\\]+(?:\\.+)+/g

// 行内元素：插图 / 附件链接 / 粗体 / 行内代码 / UNC 路径。
const INLINE_RE = new RegExp(
  [
    '!\\[([^\\]]*)\\]\\(att:([A-Za-z0-9_]+)\\)', // ![名字](att:att_N)
    '\\[([^\\]]+)\\]\\(att:([A-Za-z0-9_]+)\\)', // [名字](att:att_N)
    '\\*\\*([^*]+)\\*\\*', // **粗体**
    '`([^`]+)`', // `代码`
    `(${UNC_RE.source})`, // UNC 路径进第 7 组（UNC_RE 自身只有非捕获组）
  ].join('|'),
  'g',
)

function copyText(text: string) {
  const done = () => {}
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(text).then(done, done)
    return
  }
  const ta = document.createElement('textarea')
  ta.value = text
  ta.style.position = 'fixed'
  ta.style.opacity = '0'
  document.body.appendChild(ta)
  ta.select()
  try {
    document.execCommand('copy')
  } catch {
    // 复制失败就算了，路径文本本身可以手动选中。
  }
  ta.remove()
}

function UncBlock({ path }: { path: string }) {
  return (
    <span className="unc">
      <code>{path}</code>
      <button type="button" onClick={() => copyText(path)}>
        复制路径
      </button>
    </span>
  )
}

function AuthorizedImage({
  url,
  alt,
  onClick,
}: {
  url: string
  alt: string
  onClick?: (url: string) => void
}) {
  const [objectUrl, setObjectUrl] = useState<string | null>(null)

  useEffect(() => {
    let blobUrl: string | null = null
    let revoked = false
    // 图片与页面同源：成员会话或访客设备 cookie 由浏览器自动带上。
    void fetch(url)
      .then((res) => (res.ok ? res.blob() : null))
      .then((blob) => {
        if (!blob || revoked) return
        blobUrl = URL.createObjectURL(blob)
        setObjectUrl(blobUrl)
      })
      .catch(() => {})
    return () => {
      revoked = true
      if (blobUrl) URL.revokeObjectURL(blobUrl)
    }
  }, [url])

  if (!objectUrl) return <span className="att-missing">🖼 {alt}</span>
  return (
    <img
      className="inline-img"
      src={objectUrl}
      alt={alt}
      onClick={() => onClick?.(objectUrl)}
    />
  )
}

function renderInline(text: string, props: MarkdownProps, keyPrefix: string): ReactNode[] {
  const out: ReactNode[] = []
  let last = 0
  let m: RegExpExecArray | null
  INLINE_RE.lastIndex = 0
  for (let i = 0; (m = INLINE_RE.exec(text)) !== null; i++) {
    if (m.index > last) out.push(text.slice(last, m.index))
    const key = `${keyPrefix}-${i}`
    if (m[2] !== undefined) {
      // 插图：最宽 800px，点击查看大图。
      const att = props.attachments.get(m[2])
      if (att) {
        out.push(
          <AuthorizedImage
            key={key}
            url={att.url}
            alt={m[1] || att.filename}
            onClick={props.onImageClick}
          />,
        )
      } else {
        out.push(
          <span key={key} className="att-missing">
            🖼 {m[1] || m[2]}
          </span>,
        )
      }
    } else if (m[4] !== undefined) {
      const att = props.attachments.get(m[4])
      out.push(
        att ? (
          <a key={key} className="att-link" href={att.url} target="_blank" rel="noreferrer">
            {m[3]}
          </a>
        ) : (
          <span key={key}>{m[3]}</span>
        ),
      )
    } else if (m[5] !== undefined) {
      out.push(<b key={key}>{m[5]}</b>)
    } else if (m[6] !== undefined) {
      out.push(<code key={key}>{m[6]}</code>)
    } else if (m[7] !== undefined) {
      out.push(<UncBlock key={key} path={m[7]} />)
    }
    last = m.index + m[0].length
  }
  if (last < text.length) out.push(text.slice(last))
  return out
}

/** 正文里的 att: 引用（插图语法）集合：这些附件不重复列在气泡底部的附件条里。 */
export function inlineImageIds(body: string): Set<string> {
  const ids = new Set<string>()
  const re = /!\[[^\]]*\]\(att:([A-Za-z0-9_]+)\)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(body)) !== null) ids.add(m[1])
  return ids
}

export function Markdown(props: MarkdownProps) {
  const blocks = props.body.replace(/\s+$/, '').split(/\n{2,}/)
  return (
    <div className="md">
      {blocks.map((block, bi) => {
        const lines = block.split('\n')
        if (lines.every((l) => /^\d+\.\s/.test(l))) {
          return (
            <ol key={bi}>
              {lines.map((l, li) => (
                <li key={li}>{renderInline(l.replace(/^\d+\.\s/, ''), props, `${bi}-${li}`)}</li>
              ))}
            </ol>
          )
        }
        if (lines.every((l) => /^[-*]\s/.test(l))) {
          return (
            <ul key={bi}>
              {lines.map((l, li) => (
                <li key={li}>{renderInline(l.replace(/^[-*]\s/, ''), props, `${bi}-${li}`)}</li>
              ))}
            </ul>
          )
        }
        return (
          <p key={bi}>
            {lines.map((l, li) => (
              <span key={li}>
                {li > 0 && <br />}
                {renderInline(l, props, `${bi}-${li}`)}
              </span>
            ))}
          </p>
        )
      })}
    </div>
  )
}
