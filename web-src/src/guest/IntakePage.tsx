// 反馈入口页（#39）：/i/<slug>，匿名。其他部门的人按模板字段分区填写，
// 提交成功显示工单编号和专属访客链接，并提醒收藏。严格校验：网页实时标出
// 未填的必填项，提交被拦下时顶部汇总「还差 N 项必填」、各字段标红并显示
// 提示和示例；服务端的 422 missing_fields（逐项 hint）并入同一展示。
// 入口提交前拿不到任何凭证，图片/附件先暂存本机，建单拿到专属链接后立即
// 上传并把引用补进说明；草稿与自动重试的规则与对话页相同（同一个
// Idempotency-Key，收到 2xx 才清草稿）。
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, api } from '../api'
import { compressImage } from '../shared/compress'
import { FieldForm, appendRef, missingRequired, type StagedFile } from './FieldForm'
import { clearFormDraft, intakeDraftKey, loadAuthor, loadFormDraft, saveAuthor, saveFormDraft } from './draft'
import { guestApi } from './guestApi'
import type { IntakeSubmitResult, IntakeView, MissingFieldHint, TemplateField } from '../shared/types'
import { uploadAttachment } from './upload'
import { fmtDateCN, absoluteUrl } from '../shared/format'

const RETRY_FIRST_MS = 1500
const RETRY_MS = 4000

type Phase =
  | { kind: 'loading' }
  | { kind: 'missing' } // 入口不存在 / 已停用 / slug 已轮换
  | { kind: 'ready'; intake: IntakeView }
  | { kind: 'done'; result: IntakeSubmitResult; warnings: string[] }

/** 表单里必填的额外键（不属于模板字段）。 */
const TITLE_KEY = '__title__'
const AUTHOR_KEY = '__author__'

function missName(fields: TemplateField[], k: string): string {
  if (k === TITLE_KEY) return '一句话标题'
  if (k === AUTHOR_KEY) return '你的署名'
  return fields.find((f) => f.key === k)?.title ?? k
}

export function IntakePage({ slug }: { slug: string }) {
  const [phase, setPhase] = useState<Phase>({ kind: 'loading' })
  const [title, setTitle] = useState('')
  const [values, setValues] = useState<Record<string, string>>({})
  const [author, setAuthor] = useState('')
  const [files, setFiles] = useState<StagedFile[]>([])
  const [notice, setNotice] = useState<{ kind: 'down' | 'err'; text: string } | null>(null)
  const [serverHints, setServerHints] = useState<Record<string, string>>({})
  const [showMiss, setShowMiss] = useState(false)
  const [touched, setTouched] = useState<ReadonlySet<string>>(new Set())
  const [submitting, setSubmitting] = useState(false)

  // 重试与提交要用最新状态，一律走 ref（定时器里的闭包会过期）。
  const stateRef = useRef({ title, values, author, files })
  stateRef.current = { title, values, author, files }
  const fieldsRef = useRef<TemplateField[]>([])
  const idemKeyRef = useRef<string | undefined>(undefined)
  const pendingRef = useRef(false)
  const doneRef = useRef(false)
  const submittingRef = useRef(false)
  const retryTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const scheduleRetry = useCallback((delay: number) => {
    if (retryTimer.current) clearTimeout(retryTimer.current)
    retryTimer.current = setTimeout(() => void submitRef.current?.(), delay)
  }, [])
  const submitRef = useRef<(() => Promise<void>) | null>(null)

  const submit = useCallback(async () => {
    if (submittingRef.current) {
      scheduleRetry(RETRY_MS) // 上一轮还没结束，稍后再看
      return
    }
    if (doneRef.current) return // 已提交成功：迟到的重试直接作废

    const { title: t, values: v, author: a } = stateRef.current
    // 严格校验（与成员建单只 warnings 相反）：缺必填直接拦下。
    const missing = computeMissing(fieldsRef.current, t, v, a)
    if (missing.length > 0) {
      pendingRef.current = false
      setShowMiss(true)
      return
    }
    submittingRef.current = true
    setSubmitting(true)
    try {
      if (!idemKeyRef.current) idemKeyRef.current = crypto.randomUUID()
      pendingRef.current = true
      saveFormDraft(intakeDraftKey(slug), {
        title: t,
        fields: v,
        author: a,
        idemKey: idemKeyRef.current,
        pending: true,
      })
      const out: Record<string, string> = {}
      for (const [k, val] of Object.entries(v)) {
        if (val.trim() !== '') out[k] = val
      }
      const result: IntakeSubmitResult = await api(`/api/v1/intakes/${encodeURIComponent(slug)}/tickets`, {
        method: 'POST',
        headers: { 'Idempotency-Key': idemKeyRef.current },
        body: JSON.stringify({ title: t.trim(), fields: out, author_name: a.trim() }),
      })
      // 图片/附件此刻才有凭证（专属链接）：上传、把引用补进说明。
      const warnings = await attachFiles(result, a, stateRef.current.files)
      pendingRef.current = false
      idemKeyRef.current = undefined
      doneRef.current = true
      if (retryTimer.current) clearTimeout(retryTimer.current)
      clearFormDraft(intakeDraftKey(slug))
      saveAuthor(a)
      setPhase({ kind: 'done', result, warnings })
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 422 && err.code === 'missing_fields') {
          // 服务端的逐项提示（引导 + 示例）并入字段标红的展示。
          const hints = (err.body?.fields ?? []) as MissingFieldHint[]
          const byKey: Record<string, string> = {}
          for (const h of hints) {
            byKey[h.field] = h.example ? `${h.hint}（示例：${h.example}）` : h.hint
          }
          setServerHints(byKey)
          setShowMiss(true)
          pendingRef.current = false
          return
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
      submittingRef.current = false
      setSubmitting(false)
    }

    function serviceDown() {
      setNotice({ kind: 'down', text: '服务更新中，内容已保存，正在自动重试…' })
      scheduleRetry(RETRY_MS)
    }
  }, [slug, scheduleRetry])
  submitRef.current = submit

  // 加载入口（匿名）；顺带恢复草稿，未完成的提交立即自动重试。
  useEffect(() => {
    let alive = true
    ;(async () => {
      try {
        const intake: IntakeView = await api(`/api/v1/intakes/${encodeURIComponent(slug)}`)
        if (!alive) return
        fieldsRef.current = intake.template.fields
        const draft = loadFormDraft(intakeDraftKey(slug))
        if (draft) {
          setTitle(draft.title ?? '')
          setValues(draft.fields ?? {})
          setAuthor(draft.author ?? loadAuthor())
          idemKeyRef.current = draft.idemKey
          pendingRef.current = !!draft.pending
        } else {
          setAuthor(loadAuthor())
        }
        setPhase({ kind: 'ready', intake })
        if (draft?.pending) scheduleRetry(RETRY_FIRST_MS)
      } catch (err) {
        if (!alive) return
        if (err instanceof ApiError && err.status === 404) {
          setPhase({ kind: 'missing' })
          return
        }
        setNotice({ kind: 'down', text: '暂时连不上服务，稍后自动重试…' })
        setTimeout(() => window.location.reload(), RETRY_MS)
      }
    })()
    return () => {
      alive = false
      if (retryTimer.current) clearTimeout(retryTimer.current)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [slug])

  // 实时保存草稿（提交自动重试也靠它带上同一个 Idempotency-Key）。
  useEffect(() => {
    if (phase.kind !== 'ready' || doneRef.current) return
    saveFormDraft(intakeDraftKey(slug), {
      title,
      fields: values,
      author,
      idemKey: idemKeyRef.current,
      pending: pendingRef.current,
    })
  }, [phase, slug, title, values, author])

  function setField(k: string, v: string) {
    setValues((prev) => ({ ...prev, [k]: v }))
    setTouched((prev) => new Set(prev).add(k))
  }

  /** 入口提交前拿不到凭证：文件先暂存本机（刷新会丢，页面有提示），建单后补传。 */
  async function pickFiles(fieldKey: string, picked: File[], viaArea: boolean) {
    setFiles((prev) => [
      ...prev,
      ...picked.map((file) => ({
        id: crypto.randomUUID(),
        name: file.name,
        size: file.size,
        fieldKey,
        area: viaArea,
        status: 'ready' as const,
        previewUrl: file.type.startsWith('image/') ? URL.createObjectURL(file) : undefined,
        blob: file,
      })),
    ])
  }

  if (phase.kind === 'loading') {
    return (
      <main className="guest-root">
        <p className="muted center">加载中…</p>
      </main>
    )
  }
  if (phase.kind === 'missing') {
    return (
      <main className="guest-root">
        <div className="full">
          <div style={{ fontSize: 42 }}>🔍</div>
          <h2>没有这个反馈入口</h2>
          <p className="muted">入口可能已停用，或地址已轮换。请联系部门成员，索取当前的入口地址。</p>
        </div>
      </main>
    )
  }
  if (phase.kind === 'done') {
    const url = absoluteUrl(phase.result.guest_link.url)
    return (
      <main className="guest-root">
        <div className="full intake-done">
          <div style={{ fontSize: 42 }}>✅</div>
          <h2>已提交，编号 {phase.result.ticket.number}</h2>
          <p>
            这是你的专属链接，<b>请收藏或保存</b>，之后在这里查看回复、补充信息：
          </p>
          <div className="linkbox">{url}</div>
          <button type="button" className="btn" onClick={() => void navigator.clipboard?.writeText(url)}>
            复制链接
          </button>
          <p>
            <a className="att-link" href={phase.result.guest_link.url}>
              打开这条反馈
            </a>
          </p>
          <p className="muted" style={{ fontSize: 13 }}>
            部门回复后，轮到你时页面顶部会显示「现在轮到你回复」。链接{' '}
            {fmtDateCN(phase.result.guest_link.expires_at)}到期。
          </p>
          {phase.warnings.length > 0 && (
            <div className="notice err" role="status">
              {phase.warnings.map((w, i) => (
                <div key={i}>{w}</div>
              ))}
            </div>
          )}
        </div>
      </main>
    )
  }

  const fields = phase.intake.template.fields
  const currentMissing = computeMissing(fields, title, values, author)
  // 实时标缺项：提交被拦过（或碰过的字段）实时标红，没碰过的不打扰。
  const displayMissing = new Set([...currentMissing].filter((k) => showMiss || touched.has(k)))

  return (
    <main className="guest-root intake-page">
      <div className="guest-app">
        <header className="head">
          <div className="meta">部门工单平台 · 反馈入口</div>
          <h1>{phase.intake.name}</h1>
          <div className="meta">填好后提交，部门会在这里回复你。会得到一条专属链接，用来查看进展。</div>
        </header>
        {notice && (
          <div className={`notice ${notice.kind}`} role="status">
            {notice.text}
          </div>
        )}
        {showMiss && currentMissing.length > 0 && (
          <div className="missbox" role="alert">
            <b>还差 {currentMissing.length} 项必填：</b>
            <ul>
              {currentMissing.map((k) => (
                <li key={k}>{missName(fields, k)}</li>
              ))}
            </ul>
          </div>
        )}
        <div className="form">
          <div className={`fld${displayMissing.has(TITLE_KEY) ? ' miss' : ''}`}>
            <label htmlFor="intake-title">
              一句话标题<span className="req">必填</span>
            </label>
            <div className="guide">哪台仪器、哪个功能、什么现象。</div>
            <input
              id="intake-title"
              type="text"
              placeholder="示例：BH-7600CS 质控时报警 E-203"
              value={title}
              onChange={(e) => {
                setTitle(e.target.value)
                setTouched((prev) => new Set(prev).add(TITLE_KEY))
              }}
            />
            {displayMissing.has(TITLE_KEY) && (
              <div className="err">请填写这一项。哪台仪器、哪个功能、什么现象。</div>
            )}
          </div>
        </div>
        <FieldForm
          fields={fields}
          values={values}
          onChange={setField}
          missing={displayMissing}
          hints={serverHints}
          files={files}
          onPickFiles={(fieldKey, picked, viaArea) => void pickFiles(fieldKey, picked, viaArea)}
          onRemoveFile={(id) => setFiles((prev) => prev.filter((f) => f.id !== id))}
        />
        <div className="form">
          <div className={`fld${displayMissing.has(AUTHOR_KEY) ? ' miss' : ''}`}>
            <label htmlFor="intake-author">
              你的署名<span className="req">必填</span>
            </label>
            <div className="guide">部门会知道是谁反馈的；之后回复也会带上这个署名。</div>
            <input
              id="intake-author"
              type="text"
              placeholder="例如：检验科 张三"
              value={author}
              onChange={(e) => {
                setAuthor(e.target.value)
                setTouched((prev) => new Set(prev).add(AUTHOR_KEY))
              }}
            />
            {displayMissing.has(AUTHOR_KEY) && <div className="err">请填写这一项，例如：检验科 张三。</div>}
          </div>
        </div>
        {files.some((f) => f.blob) && (
          <div className="meta staged-note">附件会在提交成功后自动上传；提交前请别刷新页面。</div>
        )}
        <div className="composer intake-composer">
          <div className="row">
            <span className="saved">已自动保存在本机</span>
            <span className="sp" />
            <button type="button" className="btn" onClick={() => void submit()} disabled={submitting}>
              {submitting ? '提交中…' : '提交反馈'}
            </button>
          </div>
        </div>
      </div>
    </main>
  )
}

/** 缺的必填项（顺序：标题、模板字段、署名）。「不确定」算已填。 */
function computeMissing(
  fields: TemplateField[],
  title: string,
  values: Record<string, string>,
  author: string,
): string[] {
  const out: string[] = []
  if (title.trim() === '') out.push(TITLE_KEY)
  out.push(...missingRequired(fields, values))
  if (author.trim() === '') out.push(AUTHOR_KEY)
  return out
}

/** 建单后补传暂存文件：用专属链接上传拿引用，再 PATCH 说明把引用挂上（挂到「说明」）。 */
async function attachFiles(result: IntakeSubmitResult, author: string, stagedFiles: StagedFile[]): Promise<string[]> {
  const warnings: string[] = []
  const staged = stagedFiles.filter((f) => f.blob)
  const linkKey = result.guest_link.url.slice(3)
  const number = result.ticket.number
  const refsByField: Record<string, string[]> = {}
  for (const f of staged) {
    try {
      const blob = f.blob!
      const prepared = blob.type.startsWith('image/') ? await compressImage(blob) : blob
      const att = await uploadAttachment(linkKey, number, prepared)
      ;(refsByField[f.fieldKey] ??= []).push(att.markdown)
    } catch (err) {
      const why = err instanceof ApiError ? `${err.message}${err.hint ? `（${err.hint}）` : ''}` : '上传失败'
      warnings.push(`「${f.name}」未能附上（${why}），请打开链接后用「补全说明」或回复补充。`)
    }
  }
  if (Object.keys(refsByField).length === 0) return warnings
  try {
    const fresh = await guestApi(linkKey, `/api/v1/tickets/${encodeURIComponent(number)}`)
    const merged: Record<string, string> = { ...(fresh.brief.fields ?? {}) }
    for (const [fieldKey, refs] of Object.entries(refsByField)) {
      merged[fieldKey] = appendRef(merged[fieldKey] ?? '', refs.join('\n'))
    }
    const name = author.trim()
    await guestApi(linkKey, `/api/v1/tickets/${encodeURIComponent(number)}`, {
      method: 'PATCH',
      body: JSON.stringify({ fields: merged, base_revision: fresh.brief.revision, ...(name ? { author_name: name } : {}) }),
      idempotencyKey: crypto.randomUUID(),
    })
  } catch (err) {
    const why = err instanceof ApiError ? `${err.message}${err.hint ? `（${err.hint}）` : ''}` : '网络问题'
    warnings.push(`附件已上传，但没能写进说明（${why}），请打开链接后用「补全说明」补充。`)
  }
  return warnings
}
