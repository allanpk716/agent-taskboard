// 补全说明（#39）：无模板工单的访客仍用这张整页表单（自由描述接入是
// #66）；模板工单由 #65 的分组填写页（GroupedBriefForm）取代。上传图片
// 立即拿 att: 引用插进对应字段；草稿实时写入 localStorage，服务中断时自动
// 重试（与对话页同一套规则）；版本冲突时重读工单、保留访客的修改后再保存。
import { useEffect, useRef, useState } from 'react'
import { ApiError } from '../api'
import { compressImage } from '../shared/compress'
import { FieldForm, appendRef, type StagedFile } from './FieldForm'
import { briefDraftKey, clearFormDraft, loadAuthor, loadFormDraft, saveAuthor, saveFormDraft } from './draft'
import { guestApi } from './guestApi'
import type { TicketView } from '../shared/types'
import { uploadAttachment } from './upload'

const RETRY_FIRST_MS = 1500
const RETRY_MS = 4000

export interface BriefFormProps {
  linkKey: string
  ticket: TicketView
  /** 保存成功：回到对话并刷新（时间线出现修订事件） */
  onSaved: () => void
  onBack: () => void
  onLinkDead: (code: string) => void
}

export function BriefForm({ linkKey, ticket, onSaved, onBack, onLinkDead }: BriefFormProps) {
  const fields = ticket.brief.template_fields ?? []
  const key = briefDraftKey(linkKey, ticket.number)
  const initialDraft = useRef(loadFormDraft(key))
  const [values, setValues] = useState<Record<string, string>>(() => ({
    ...(ticket.brief.fields ?? {}),
    ...(initialDraft.current?.fields ?? {}),
  }))
  const [author, setAuthor] = useState(initialDraft.current?.author ?? loadAuthor())
  const [files, setFiles] = useState<StagedFile[]>([])
  const [notice, setNotice] = useState<{ kind: 'down' | 'err'; text: string } | null>(null)
  const [missing, setMissing] = useState<ReadonlySet<string>>(new Set())
  const [saving, setSaving] = useState(false)
  const baseRevisionRef = useRef(ticket.brief.revision)
  const idemKeyRef = useRef<string | undefined>(initialDraft.current?.idemKey)
  const pendingRef = useRef(!!initialDraft.current?.pending)
  const valuesRef = useRef(values)
  valuesRef.current = values
  const authorRef = useRef(author)
  authorRef.current = author
  const retryTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // 实时保存草稿；字段值里的 att: 引用随字段值一并保存。
  useEffect(() => {
    saveFormDraft(key, { fields: values, author, idemKey: idemKeyRef.current, pending: pendingRef.current })
  }, [key, values, author])

  // 刷新后若有一次没保存成功的修订，立即自动重试。
  useEffect(() => {
    if (initialDraft.current?.pending) scheduleRetry(RETRY_FIRST_MS)
    return () => {
      if (retryTimer.current) clearTimeout(retryTimer.current)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  function scheduleRetry(delay: number) {
    if (retryTimer.current) clearTimeout(retryTimer.current)
    retryTimer.current = setTimeout(() => void save(), delay)
  }

  function setField(k: string, v: string) {
    setValues((prev) => ({ ...prev, [k]: v }))
  }

  async function pickFiles(fieldKey: string, picked: File[], viaArea: boolean) {
    for (const file of picked) {
      const id = crypto.randomUUID()
      const staged: StagedFile = {
        id,
        name: file.name,
        size: file.size,
        fieldKey,
        area: viaArea,
        status: 'uploading',
        previewUrl: file.type.startsWith('image/') ? URL.createObjectURL(file) : undefined,
      }
      setFiles((prev) => [...prev, staged])
      try {
        const prepared = file.type.startsWith('image/') ? await compressImage(file) : file
        const att = await uploadAttachment(linkKey, ticket.number, prepared)
        setFiles((prev) =>
          prev.map((f) => (f.id === id ? { ...f, status: 'ready', markdown: att.markdown, size: att.size } : f)),
        )
        // 引用立即插进对应字段值（PATCH 时随字段提交，服务端认领挂到「说明」）。
        setValues((prev) => ({ ...prev, [fieldKey]: appendRef(prev[fieldKey] ?? '', att.markdown) }))
      } catch (err) {
        const text =
          err instanceof ApiError
            ? err.status === 413
              ? `⚠ ${file.name} 超过上限：${err.message}${err.hint ? `。${err.hint}` : ''}`
              : `${err.message}${err.hint ? `（${err.hint}）` : ''}`
            : `${file.name} 上传失败，请重试。`
        if (err instanceof ApiError && err.status === 401 && (err.code === 'link_revoked' || err.code === 'link_expired')) {
          onLinkDead(err.code)
          return
        }
        setFiles((prev) => prev.map((f) => (f.id === id ? { ...f, status: 'failed', error: text } : f)))
      }
    }
  }

  function removeFile(id: string) {
    setFiles((prev) => prev.filter((f) => f.id !== id))
  }

  async function save() {
    if (saving) {
      scheduleRetry(RETRY_MS)
      return
    }
    // 修订说明不再做必填校验（docs/api.md）：有值的字段都提交。
    setSaving(true)
    setMissing(new Set())
    try {
      if (!idemKeyRef.current) idemKeyRef.current = crypto.randomUUID()
      pendingRef.current = true
      saveFormDraft(key, { fields: valuesRef.current, author: authorRef.current, idemKey: idemKeyRef.current, pending: true })
      const out: Record<string, string> = {}
      for (const f of fields) out[f.key] = valuesRef.current[f.key] ?? ''
      const name = authorRef.current.trim()
      await guestApi(linkKey, `/api/v1/tickets/${encodeURIComponent(ticket.number)}`, {
        method: 'PATCH',
        body: JSON.stringify({ fields: out, base_revision: baseRevisionRef.current, ...(name ? { author_name: name } : {}) }),
        idempotencyKey: idemKeyRef.current,
      })
      pendingRef.current = false
      idemKeyRef.current = undefined
      clearFormDraft(key)
      saveAuthor(authorRef.current)
      setNotice(null)
      onSaved()
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 401 && (err.code === 'link_revoked' || err.code === 'link_expired')) {
          onLinkDead(err.code)
          return
        }
        if (err.status === 409 && err.code === 'revision_conflict') {
          // 说明被别人改过：重读工单拿最新版本号，保留访客的修改后立即重试
          //（同一个 Idempotency-Key：首次其实已成功时会重放首次结果，不会重复修订）。
          try {
            const fresh = await guestApi(linkKey, `/api/v1/tickets/${encodeURIComponent(ticket.number)}`)
            baseRevisionRef.current = fresh.brief.revision
            setNotice({ kind: 'err', text: '说明刚被别人更新过，正在按最新版本重新保存你的修改。' })
            scheduleRetry(RETRY_FIRST_MS)
            return
          } catch {
            serviceDown()
            return
          }
        }
        if (err.status >= 500 || err.status === 429) {
          serviceDown()
          return
        }
        setNotice({ kind: 'err', text: err.hint ? `${err.message}（${err.hint}）` : err.message })
        pendingRef.current = false
      } else {
        serviceDown()
      }
    } finally {
      setSaving(false)
    }
  }

  function serviceDown() {
    setNotice({ kind: 'down', text: '服务更新中，内容已保存，正在自动重试…' })
    scheduleRetry(RETRY_MS)
  }

  return (
    <div className="guest-app brief-form">
      <header className="head">
        <div className="meta">
          {ticket.number} · 补全说明
        </div>
        <h1>{ticket.title}</h1>
        <span className="saved">输入内容实时保存在本机</span>
      </header>
      {notice && (
        <div className={`notice ${notice.kind}`} role="status">
          {notice.text}
        </div>
      )}
      <FieldForm
        fields={fields}
        values={values}
        onChange={setField}
        missing={missing}
        files={files}
        onPickFiles={(fieldKey, picked, viaArea) => void pickFiles(fieldKey, picked, viaArea)}
        onRemoveFile={removeFile}
      />
      <div className="form-actions">
        <label className="author-label" htmlFor="brief-author">
          署名
        </label>
        <input
          id="brief-author"
          value={author}
          placeholder="如：检验科 张三"
          onChange={(e) => setAuthor(e.target.value)}
        />
        <span className="sp" />
        <button type="button" className="btn ghost" onClick={onBack}>
          返回
        </button>
        <button type="button" className="btn" onClick={() => void save()} disabled={saving || files.some((f) => f.status === 'uploading')}>
          {saving ? '保存中…' : '保存说明'}
        </button>
      </div>
    </div>
  )
}
