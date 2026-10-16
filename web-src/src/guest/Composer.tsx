// 底部固定输入区：可以直接粘贴截图、📎 附件、署名输入框（浏览器记住署名）、
// 「已自动保存在本机」提示。上传前压缩图片；附件超限（413）提示改用 NAS 路径。
// 草稿实时写入 localStorage，收到 2xx 才清除；服务不可用时显示「服务更新中，
// 内容已保存」并带同一个 Idempotency-Key 自动重试（ADR 0002）。
import { useEffect, useRef, useState } from 'react'
import { ApiError } from '../api'
import { compressImage } from './compress'
import { clearDraft, draftKey, loadAuthor, loadDraft, saveAuthor, saveDraft, type Draft } from './draft'
import { guestApi } from './guestApi'
import { uploadAttachment } from './upload'

const RETRY_FIRST_MS = 1500 // 第一次重试快一点：多数服务重启只有几秒
const RETRY_MS = 4000

export interface ComposerProps {
  linkKey: string
  ticketNumber: string
  /** 发送成功后刷新工单。 */
  onSent: () => void
  /** 工单在别处被关闭了（409 ticket_closed）：刷新成只读。 */
  onTicketClosed: () => void
  /** 链接失效（401 link_revoked / link_expired）：整页提示。 */
  onLinkDead: (code: string) => void
  /** 链接带「可填写说明」权限：点「✏ 补全说明」进入字段表单（#39）。 */
  onEditBrief?: () => void
}

export function Composer(props: ComposerProps) {
  const { linkKey, ticketNumber } = props
  const key = draftKey(linkKey, ticketNumber)
  const initial = useRef<Draft | null>(loadDraft(key))
  const [body, setBody] = useState(initial.current?.body ?? '')
  const [author, setAuthor] = useState(initial.current?.author ?? loadAuthor())
  const [notice, setNotice] = useState<{ kind: 'down' | 'err'; text: string } | null>(null)
  const [bigWarn, setBigWarn] = useState<string | null>(null)
  const [uploading, setUploading] = useState(0)
  const idemKeyRef = useRef<string | undefined>(initial.current?.idemKey)
  const pendingRef = useRef<boolean>(!!initial.current?.pending)
  const sendingRef = useRef(false)
  const retryTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const taRef = useRef<HTMLTextAreaElement | null>(null)
  const stateRef = useRef({ body, author })
  stateRef.current = { body, author }

  // 实时保存草稿（包括正文里的 att: 引用）；发送成功后正文为空，顺手把键清掉。
  useEffect(() => {
    if (body === '' && !pendingRef.current) {
      clearDraft(key)
    } else {
      saveDraft(key, { body, author, idemKey: idemKeyRef.current, pending: pendingRef.current })
    }
  }, [key, body, author])

  // 刷新后若有一次没发出去的草稿，立即开始自动重试。
  useEffect(() => {
    if (initial.current?.pending) scheduleRetry(RETRY_FIRST_MS)
    return () => {
      if (retryTimer.current) clearTimeout(retryTimer.current)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  function scheduleRetry(delay: number) {
    if (retryTimer.current) clearTimeout(retryTimer.current)
    retryTimer.current = setTimeout(() => void trySend(), delay)
  }

  async function trySend() {
    if (sendingRef.current) {
      scheduleRetry(RETRY_MS) // 上一轮还没结束，稍后再看
      return
    }
    const { body: text, author: name } = stateRef.current
    if (!text.trim() && !/att:[A-Za-z0-9_]+/.test(text)) {
      setNotice({ kind: 'err', text: '请先写点内容，或粘贴截图 / 添加附件。' })
      pendingRef.current = false
      return
    }
    sendingRef.current = true
    try {
      if (!idemKeyRef.current) idemKeyRef.current = crypto.randomUUID()
      pendingRef.current = true
      saveDraft(key, { body: text, author: name, idemKey: idemKeyRef.current, pending: true })
      await guestApi(linkKey, `/api/v1/tickets/${encodeURIComponent(ticketNumber)}/replies`, {
        method: 'POST',
        body: JSON.stringify({
          body_md: text,
          ...(name.trim() ? { author_name: name.trim() } : {}),
        }),
        idempotencyKey: idemKeyRef.current,
      })
      // 2xx：清草稿、清署名草稿键、刷新对话。
      pendingRef.current = false
      idemKeyRef.current = undefined
      clearDraft(key)
      setBody('')
      setNotice(null)
      setBigWarn(null)
      props.onSent()
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 401 && (err.code === 'link_revoked' || err.code === 'link_expired')) {
          props.onLinkDead(err.code)
          return
        }
        if (err.status === 409 && err.code === 'ticket_closed') {
          setNotice({ kind: 'err', text: '工单已关闭，只能查看。' })
          pendingRef.current = false
          props.onTicketClosed()
          return
        }
        if (err.status >= 500 || err.status === 429) {
          serviceDown()
          return
        }
        setNotice({ kind: 'err', text: err.hint ? `${err.message}（${err.hint}）` : err.message })
        pendingRef.current = false
      } else {
        serviceDown() // 网络错误（TypeError: Failed to fetch）等
      }
    } finally {
      sendingRef.current = false
    }
  }

  function serviceDown() {
    setNotice({ kind: 'down', text: '服务更新中，内容已保存，正在自动重试…' })
    scheduleRetry(RETRY_MS)
  }

  /** 粘贴 / 选择文件：图片先压缩再上传，成功后把现成的 Markdown 片段插进正文。 */
  async function handleFiles(files: File[]) {
    for (const file of files) {
      setUploading((n) => n + 1)
      try {
        const prepared = file.type.startsWith('image/') ? await compressImage(file) : file
        const att = await uploadAttachment(linkKey, ticketNumber, prepared)
        setBigWarn(null)
        insertAtCursor(att.markdown)
      } catch (err) {
        if (err instanceof ApiError) {
          if (err.status === 413) {
            // 超限：提示改用 NAS 路径（外部文件引用），hint 由服务端给出。
            setBigWarn(`⚠ ${file.name}：${err.message}${err.hint ? `。${err.hint}` : ''}`)
          } else if (err.status === 401 && (err.code === 'link_revoked' || err.code === 'link_expired')) {
            props.onLinkDead(err.code)
            return
          } else {
            setNotice({ kind: 'err', text: err.hint ? `${err.message}（${err.hint}）` : err.message })
          }
        } else {
          setNotice({ kind: 'err', text: `${file.name} 上传失败，请重试。` })
        }
      } finally {
        setUploading((n) => n - 1)
      }
    }
  }

  function insertAtCursor(text: string) {
    const ta = taRef.current
    if (!ta) return
    const start = ta.selectionStart ?? ta.value.length
    const end = ta.selectionEnd ?? ta.value.length
    const next = ta.value.slice(0, start) + text + ta.value.slice(end)
    setBody(next)
    queueMicrotask(() => {
      ta.selectionStart = ta.selectionEnd = start + text.length
    })
  }

  return (
    <div className="composer">
      {bigWarn && <div className="toobig">{bigWarn}</div>}
      {notice && (
        <div className={`notice ${notice.kind}`} role="status">
          {notice.text}
        </div>
      )}
      <textarea
        ref={taRef}
        placeholder="写回复，可以直接粘贴截图…"
        value={body}
        onChange={(e) => setBody(e.target.value)}
        onPaste={async (e) => {
          const files = Array.from(e.clipboardData?.files ?? []).filter((f) =>
            f.type.startsWith('image/') || /\.(log|txt|pdf|zip)$/i.test(f.name),
          )
          if (files.length === 0) return
          e.preventDefault()
          await handleFiles(files)
        }}
      />
      <div className="row">
        <button type="button" className="btn ghost" onClick={() => document.getElementById('guest-file-input')?.click()}>
          📎 附件
        </button>
        {props.onEditBrief && (
          <button type="button" className="btn ghost" onClick={props.onEditBrief}>
            ✏ 补全说明
          </button>
        )}
        <input
          id="guest-file-input"
          type="file"
          multiple
          hidden
          onChange={(e) => {
            const files = Array.from(e.target.files ?? [])
            e.target.value = ''
            void handleFiles(files)
          }}
        />
        <span className="sp" />
        <label className="author-label" htmlFor="guest-author">
          署名
        </label>
        <input
          id="guest-author"
          aria-label="署名"
          value={author}
          placeholder="如：检验科 张三"
          onChange={(e) => {
            setAuthor(e.target.value)
            saveAuthor(e.target.value)
          }}
        />
        <button type="button" className="btn" onClick={() => void trySend()} disabled={uploading > 0}>
          {uploading > 0 ? '上传附件中…' : '发送'}
        </button>
      </div>
      <div className="saved">已自动保存在本机</div>
    </div>
  )
}
