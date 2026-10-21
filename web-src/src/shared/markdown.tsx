// 共享 Markdown 渲染（成员端 / 访客端）：CommonMark + GFM + 单换行保留。
import ReactMarkdown, { type Components, type UrlTransform } from 'react-markdown'
import { unified } from 'unified'
import remarkBreaks from 'remark-breaks'
import remarkGfm from 'remark-gfm'
import remarkParse from 'remark-parse'
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
// 尾部可包含空格；遇到表格竖线或下一个 UNC 开头时结束。
const UNC_CORE = String.raw`\\\\[^\s\\|]+(?:\\[^\s\\|]+)+`
const UNC_TAIL = String.raw`(?:[^\n|\\]|\\(?!\\))*`
const BARE_UNC_RE = new RegExp(`${UNC_CORE}${UNC_TAIL}`, 'g')
const INLINE_UNC_RE = new RegExp(`^${UNC_CORE}${UNC_TAIL}$`)

interface PositionNode {
  type: string
  position?: { start: { offset?: number }; end: { offset?: number } }
  children?: PositionNode[]
}

interface CodeRange {
  start: number
  end: number
}

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

function attIdOf(url: string | null | undefined) {
  return typeof url === 'string' && url.startsWith('att:') ? url.slice(4) : null
}

function collectCodeRanges(node: PositionNode, ranges: CodeRange[]) {
  const start = node.position?.start.offset
  const end = node.position?.end.offset
  if ((node.type === 'code' || node.type === 'inlineCode') && start !== undefined && end !== undefined) {
    ranges.push({ start, end })
  }
  for (const child of node.children ?? []) collectCodeRanges(child, ranges)
}

// 裸 UNC 先包成行内代码，避免 CommonMark 把开头的反斜杠当转义吃掉。
function wrapBareUnc(text: string) {
  return text.replace(BARE_UNC_RE, (path) => {
    const trailingWhitespace = path.match(/\s+$/)?.[0] ?? ''
    const barePath = path.slice(0, path.length - trailingWhitespace.length)
    return '`' + barePath + '`' + trailingWhitespace
  })
}

function preprocessMarkdown(body: string) {
  // 先用同一套 Markdown 语法识别代码区，避免靠行首缩进猜测代码块。
  const tree = unified()
    .use(remarkParse)
    .use(remarkGfm)
    .parse(body) as unknown as PositionNode
  const ranges: CodeRange[] = []
  collectCodeRanges(tree, ranges)
  ranges.sort((a, b) => a.start - b.start)

  // CommonMark 会把裸 UNC 开头的反斜杠当作转义；预处理必须避开代码原文。
  let out = ''
  let cursor = 0
  for (const range of ranges) {
    if (range.start < cursor) continue
    out += wrapBareUnc(body.slice(cursor, range.start)) + body.slice(range.start, range.end)
    cursor = range.end
  }
  return out + wrapBareUnc(body.slice(cursor))
}

const urlTransform: UrlTransform = (url) => (/^(?:https?|mailto|att):/i.test(url) ? url : '')

/** 正文里的 att: 引用（插图语法）集合：这些附件不重复列在气泡底部的附件条里。 */
export function inlineImageIds(body: string): Set<string> {
  const ids = new Set<string>()
  const re = /!\[[^\]]*\]\(att:([A-Za-z0-9_]+)\)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(body)) !== null) {
    const attId = attIdOf(`att:${m[1]}`)
    if (attId) ids.add(attId)
  }
  return ids
}

export function Markdown(props: MarkdownProps) {
  const components: Components = {
    a({ children, href }) {
      const attId = attIdOf(href)
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
      const inlineText = text.trim()
      const blockCode = (node as { parent?: { tagName?: string } })?.parent?.tagName === 'pre'
      if (!className && !blockCode && INLINE_UNC_RE.test(inlineText)) {
        return <UncBlock path={inlineText} />
      }
      return <code className={className}>{children}</code>
    },
    img({ src, alt }) {
      const attId = attIdOf(src)
      if (attId) {
        const att = props.attachments.get(attId)
        if (att) {
          return (
            <AuthorizedImage
              url={att.url}
              alt={alt || att.filename}
              onClick={props.onImageClick}
            />
          )
        }
        return <span className="att-missing">🖼 {alt || attId}</span>
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
