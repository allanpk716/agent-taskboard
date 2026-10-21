// 共享 Markdown 渲染（成员端 / 访客端）：CommonMark + GFM + 单换行保留。
import ReactMarkdown, { type Components, type UrlTransform } from 'react-markdown'
import remarkBreaks from 'remark-breaks'
import remarkGfm from 'remark-gfm'
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
const UNC_REPLACE_RE = /\\\\[^\s\\]+(?:\\[^\s\\]+)+/
const UNC_TEST_RE = /\\\\[^\s\\]+(?:\\[^\s\\]+)+/
const INLINE_CODE_RE = /(`+)[\s\S]*?\1(?!`)/g

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

function textOf(children: ReactNode): string {
  if (typeof children === 'string') return children
  if (Array.isArray(children)) return children.map(textOf).join('')
  return ''
}

function ordinaryText(line: string) {
  return line.replace(/</g, '\\<').replace(UNC_REPLACE_RE, (path) => `\`${path}\``)
}

function preprocessLine(line: string) {
  let out = ''
  let last = 0
  let match: RegExpExecArray | null
  INLINE_CODE_RE.lastIndex = 0
  while ((match = INLINE_CODE_RE.exec(line)) !== null) {
    out += ordinaryText(line.slice(last, match.index)) + match[0]
    last = match.index + match[0].length
  }
  return out + ordinaryText(line.slice(last))
}

/** 裸 UNC 先入行内代码；同时把代码区外的原始 HTML 起始符转成文本。 */
export function preprocessMarkdown(body: string) {
  const lines = body.split('\n')
  let fence: string | null = null
  return lines
    .map((line) => {
      const open = /^\s{0,3}(`{3,}|~{3,})/.exec(line)
      if (fence) {
        if (open?.[1].startsWith(fence[0][0]) && open[1].length >= fence.length) fence = null
        return line
      }
      if (open) {
        fence = open[1]
        return line
      }
      // 缩进代码块也不做 UNC / HTML 预处理。
      if (/^(?: {4}|\t)/.test(line)) return line
      return preprocessLine(line)
    })
    .join('\n')
}

const urlTransform: UrlTransform = (url) => (/^(?:https?|mailto|att):/i.test(url) ? url : '')

/** 正文里的 att: 引用（插图语法）集合：这些附件不重复列在气泡底部的附件条里。 */
export function inlineImageIds(body: string): Set<string> {
  const ids = new Set<string>()
  const re = /!\[[^\]]*\]\(att:([A-Za-z0-9_]+)\)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(body)) !== null) ids.add(m[1])
  return ids
}

export function Markdown(props: MarkdownProps) {
  const components: Components = {
    a({ children, href }) {
      const attId = href?.startsWith('att:') ? href.slice(4) : null
      if (attId) {
        const att = props.attachments.get(attId)
        if (!att) return <span>{children}</span>
        return (
          <a className="att-link" href={att.url} target="_blank" rel="noreferrer">
            {children}
          </a>
        )
      }
      if (!href) return <span>{children}</span>
      const external = /^https?:/i.test(href)
      return (
        <a href={href} {...(external ? { target: '_blank', rel: 'noopener noreferrer' } : {})}>
          {children}
        </a>
      )
    },
    code({ children, className, node }) {
      const text = textOf(children)
      const blockCode = (node as { parent?: { tagName?: string } })?.parent?.tagName === 'pre'
      if (!className && !blockCode && UNC_TEST_RE.test(text)) {
        return <UncBlock path={text} />
      }
      return <code className={className}>{children}</code>
    },
    img({ src, alt }) {
      if (typeof src === 'string' && src.startsWith('att:')) {
        const att = props.attachments.get(src.slice(4))
        if (att) {
          return (
            <AuthorizedImage
              url={att.url}
              alt={alt || att.filename}
              onClick={props.onImageClick}
            />
          )
        }
        return <span className="att-missing">🖼 {alt || src.slice(4)}</span>
      }
      // 不加载外部图片，保留替代文字。
      return <span>{alt}</span>
    },
    table({ children }) {
      return (
        <div className="md-table-wrap">
          <table>{children}</table>
        </div>
      )
    },
  }

  return (
    <div className="md">
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkBreaks]}
        urlTransform={urlTransform}
        components={components}
      >
        {preprocessMarkdown(props.body)}
      </ReactMarkdown>
    </div>
  )
}
