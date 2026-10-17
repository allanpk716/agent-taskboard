// 访客分组填写页（#65）：持「可填写说明」链接的访客按模板分组折叠补全说明，
// 取代旧的整页表单（BriefForm）。手机（<768）一次展开一组，组末「下一组」、
// 顶部吸顶必填进度；宽屏（≥768）双栏：左边全部分组，右边吸顶必填进度、
// 逐字段清单（点一下滚到该字段）、整张工单的附件区、署名与提交。保存沿用
// 旧逻辑：草稿实时写本机、一次 PATCH（带 base_revision）+ 幂等键重试、版本
// 冲突时重读重试；文件立即上传拿 att: 引用写进字段值（统一附件区挂到最后
// 一个文本字段）。必填校验只在前端拦提交，服务端规则不变。无模板的工单
// （#66 自由格式）合成一组「情况说明」，修订时提交 brief_md。
// 视觉参考 prototype/guest-fill 的 c.html（原型代码不合并）。
import { useEffect, useRef, useState } from 'react'
import { ApiError } from '../api'
import { compressImage } from '../shared/compress'
import { DimensionSelect } from './DimensionSelect'
import { briefDraftKey, clearFormDraft, loadAuthor, loadFormDraft, saveAuthor, saveFormDraft } from './draft'
import { guestApi } from './guestApi'
import { uploadAttachment } from './upload'
import type { TemplateField, TicketView } from '../shared/types'
import { fmtSize } from '../shared/format'
import { appendRef, lastTextFieldKey, type StagedFile } from './FieldForm'

const RETRY_FIRST_MS = 1500
const RETRY_MS = 4000
const DESKTOP_QUERY = '(min-width: 768px)'
const UNSURE = '不清楚'
/** 追问提示的长度阈值：多行文本少于 20 字且不是「不清楚」时出现。 */
const NUDGE_MIN_CHARS = 20
/** 整张工单的附件区（手机）放在这一组的组末（#65 spec：照片、日志跟
 *  时间/操作/现象同在一组）；模板里没有时退回最后一组组末。 */
const ATTACH_AREA_GROUP_TITLE = '发生了什么'

/** 自由格式（#66）：无模板工单合成的一组「情况说明」的大文本框，
 *  上方列出可以按哪几点写；tips / nudgeExample 只在前端用，服务端不下发。 */
export interface FillField extends TemplateField {
  /** 提示点列表（照原型，大文本框上方逐条列出） */
  tips?: string[]
  /** 追问提示的内容（没有时取示例 / 引导说明；自由格式取提示点） */
  nudgeExample?: string
}

/** 自由格式字段的键（草稿与统一附件区都用它；不与模板字段键冲突）。
 *  值 __brief__ 是写进本机草稿的持久键，改了会让旧草稿读不回来，保持不变。 */
export const NO_TEMPLATE_FIELD_KEY = '__brief__'

const FREE_FIELD: FillField = {
  key: NO_TEMPLATE_FIELD_KEY,
  title: '请把情况写详细',
  type: 'text',
  required: true,
  guide: '可以按下面几点写，想到哪写到哪，不用写成一段完整的话：',
  example: '',
  tips: [
    '哪台设备、哪个系统',
    '什么时候发生的',
    '做了哪些操作（按顺序）',
    '看到了什么（报警、提示原文照抄）',
    '本来应该怎样',
    '出现得频繁吗',
  ],
  nudgeExample: '哪台设备、什么时候、做了什么、看到什么、本来应该怎样、频繁吗',
}

export interface GroupedBriefFormProps {
  linkKey: string
  ticket: TicketView
  /** 落地页「你的称呼」带过来的署名初值（#66；没经过落地页时省略）。 */
  initialAuthor?: string
  /** 保存成功并点「查看对话」：回到对话并刷新（时间线出现修订事件）。 */
  onSaved: () => void
  onBack: () => void
  onLinkDead: (code: string) => void
}

/** 展示用的一组字段：模板的分组；不分组时全部字段合成一组「填写说明」；
 *  无模板（自由格式）合成一组「情况说明」（#66）。 */
export interface FieldGroup {
  title: string
  subtitle?: string
  fields: FillField[]
}

export function groupsOf(ticket: TicketView): FieldGroup[] {
  const fields = ticket.brief.template_fields ?? []
  if (fields.length === 0) {
    return [{ title: '情况说明', subtitle: '写一段，附上照片和日志', fields: [FREE_FIELD] }]
  }
  const gs = ticket.brief.template_groups ?? []
  if (gs.length > 0) {
    return gs.map((g, i) => ({ title: g.title, subtitle: g.subtitle, fields: fields.filter((f) => f.group === i) }))
  }
  return [{ title: '填写说明', fields }]
}

/** ≥768 算宽屏（双栏布局），窗口缩放时实时切换。 */
function useDesktop(): boolean {
  const [desktop, setDesktop] = useState(() => window.matchMedia(DESKTOP_QUERY).matches)
  useEffect(() => {
    const mq = window.matchMedia(DESKTOP_QUERY)
    const onChange = () => setDesktop(mq.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])
  return desktop
}

function filled(value: string | undefined): boolean {
  return !!(value ?? '').trim()
}

/** 追问提示文案：统一「能再具体一点吗？比如：…」的形式，内容取字段专属
 *  内容，否则取示例，再否则取引导说明（不调 AI）。 */
function nudgeText(f: FillField, value: string): string | null {
  if (f.type !== 'text') return null
  const v = value.trim()
  if (!v || v === UNSURE || v.length >= NUDGE_MIN_CHARS) return null
  const src = f.nudgeExample || f.example || f.guide
  return src ? `能再具体一点吗？比如：${src}` : '能再具体一点吗？'
}

/** 必填进度：已填必填数 / 必填总数。 */
function reqProgress(fields: FillField[], values: Record<string, string>): [number, number] {
  const reqs = fields.filter((f) => f.required)
  return [reqs.filter((f) => filled(values[f.key])).length, reqs.length]
}

/** 需要在说明里提交给对方的成员名（建单的成员；兜底同对话页的联系人）。 */
export function memberNameOf(ticket: TicketView): string {
  return ticket.creator.member?.name ?? ticket.assignee?.member?.name ?? '给你发链接的同事'
}

/** 必填进度条 + 文案（填满后变绿「必填都写好了」）。 */
function Progress({ done, total }: { done: number; total: number }) {
  const all = done === total
  return (
    <div className={`fill-prog${all ? ' ok' : ''}`}>
      <div className="bar">
        <div className="barin" style={{ width: total > 0 ? `${Math.round((done / total) * 100)}%` : '100%' }} />
      </div>
      <span className="txt">{all ? '必填都写好了' : `必填 ${done}/${total}`}</span>
    </div>
  )
}

interface FieldBlockProps {
  field: FillField
  value: string
  onChange: (key: string, value: string) => void
  onPickFiles: (fieldKey: string, files: File[], viaArea: boolean) => void
  files: StagedFile[]
  onRemoveFile: (id: string) => void
  desktop: boolean
}

/** 单个字段：问句标题 + 必填/选填 + 引导说明 + 示例（多行默认折叠）+ 输入 +
 * 追问提示 + 「不清楚」快捷 + 上传入口（每个文本字段都有）+ 该字段的上传结果。 */
function FieldBlock({ field: f, value, onChange, onPickFiles, files, onRemoveFile, desktop }: FieldBlockProps) {
  const [showExample, setShowExample] = useState(false)
  const fileRef = useRef<HTMLInputElement | null>(null)
  const multiExample = !!f.example && f.example.includes('\n')
  const myFiles = files.filter((x) => x.fieldKey === f.key && !x.area)

  /** 手机三个入口对应不同的 accept/capture；宽屏一个按钮选任意文件。 */
  function openPicker(mode: 'photo' | 'album' | 'file') {
    const input = fileRef.current
    if (!input) return
    if (mode === 'photo') {
      input.setAttribute('accept', 'image/*')
      input.setAttribute('capture', 'environment')
    } else if (mode === 'album') {
      input.setAttribute('accept', 'image/*')
      input.removeAttribute('capture')
    } else {
      input.removeAttribute('accept')
      input.removeAttribute('capture')
    }
    input.click()
  }

  return (
    <div className="cf" data-field={f.title} id={`f-${f.key}`}>
      <label>
        {f.title}
        {f.required ? <i className="req">必填</i> : <i className="opt">选填</i>}
      </label>
      {f.guide && <div className="guide">{f.guide}</div>}
      {f.type === 'dimension' ? (
        <DimensionSelect field={f} value={value} onChange={(v) => onChange(f.key, v)} />
      ) : f.type === 'radio' ? (
        <div className="radios">
          {(f.options ?? []).map((o) => (
            <label key={o}>
              <input type="radio" name={f.key} value={o} checked={value === o} onChange={() => onChange(f.key, o)} /> {o}
            </label>
          ))}
        </div>
      ) : (
        <>
          {f.tips && (
            <ul className="tips">
              {f.tips.map((t) => (
                <li key={t}>{t}</li>
              ))}
            </ul>
          )}
          <textarea
            className={f.tips ? 'big' : undefined}
            placeholder={f.example ? `示例：${f.example.split('\n')[0]}` : '在这里写…'}
            value={value}
            onChange={(e) => onChange(f.key, e.target.value)}
            onPaste={(e) => {
              const imgs = Array.from(e.clipboardData?.files ?? []).filter((file) => file.type.startsWith('image/'))
              if (imgs.length === 0) return
              e.preventDefault()
              onPickFiles(f.key, imgs, false)
            }}
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault()
              const dropped = Array.from(e.dataTransfer?.files ?? [])
              if (dropped.length > 0) onPickFiles(f.key, dropped, false)
            }}
          />
          {nudgeText(f, value) && <div className="nudge">{nudgeText(f, value)}</div>}
          <div className="tools">
            {multiExample && (
              <button type="button" className="linkish" onClick={() => setShowExample((s) => !s)}>
                {showExample ? '收起示例' : '看完整示例'}
              </button>
            )}
            {f.required && (
              <button type="button" className="linkish" onClick={() => onChange(f.key, UNSURE)}>
                {UNSURE}
              </button>
            )}
            {desktop ? (
              <button type="button" className="linkish" onClick={() => openPicker('file')}>
                上传图片或文件
              </button>
            ) : (
              <>
                <button type="button" className="linkish" onClick={() => openPicker('photo')}>
                  📷 拍照
                </button>
                <button type="button" className="linkish" onClick={() => openPicker('album')}>
                  🖼 相册
                </button>
                <button type="button" className="linkish" onClick={() => openPicker('file')}>
                  📄 文件
                </button>
              </>
            )}
            <input
              ref={fileRef}
              type="file"
              multiple
              hidden
              onChange={(e) => {
                const picked = Array.from(e.target.files ?? [])
                e.target.value = ''
                if (picked.length > 0) onPickFiles(f.key, picked, false)
              }}
            />
          </div>
          {multiExample && showExample && (
            <div className="ex">
              <b>示例</b>
              {'\n'}
              {f.example}
            </div>
          )}
          {f.example && !multiExample && (
            <div className="ex">
              <b>示例</b>　{f.example}
            </div>
          )}
          <FileChips files={myFiles} onRemove={onRemoveFile} />
        </>
      )}
    </div>
  )
}

/** 上传结果的展示条（字段下 / 统一附件区共用）。 */
function FileChips({ files, onRemove }: { files: StagedFile[]; onRemove?: (id: string) => void }) {
  if (files.length === 0) return null
  return (
    <div className="staged-files">
      {files.map((f) => (
        <span key={f.id} className={`staged-file ${f.status}`}>
          {f.previewUrl ? <img src={f.previewUrl} alt="" /> : '📄'}
          <span className="name">
            {f.name} · {fmtSize(f.size)}
          </span>
          {f.status === 'uploading' && <span className="state">上传中…</span>}
          {f.status === 'failed' && <span className="state">{f.error ?? '失败'}</span>}
          {onRemove && (
            <button type="button" className="rm" aria-label={`移除 ${f.name}`} onClick={() => onRemove(f.id)}>
              ×
            </button>
          )}
        </span>
      ))}
    </div>
  )
}

/** 整张工单的附件区：手机在「发生了什么」组末（没有时最后一组组末，
 *  三入口），宽屏在右栏（拖放区）。 */
function AttachArea({
  fieldKey,
  onPickFiles,
  files,
  onRemoveFile,
  desktop,
}: {
  fieldKey?: string
  onPickFiles: (fieldKey: string, files: File[], viaArea: boolean) => void
  files: StagedFile[]
  onRemoveFile: (id: string) => void
  desktop: boolean
}) {
  const fileRef = useRef<HTMLInputElement | null>(null)
  const areaFiles = fieldKey ? files.filter((f) => f.fieldKey === fieldKey && f.area) : []
  function openPicker(mode: 'photo' | 'album' | 'file') {
    const input = fileRef.current
    if (!input) return
    if (mode === 'photo') {
      input.setAttribute('accept', 'image/*')
      input.setAttribute('capture', 'environment')
    } else if (mode === 'album') {
      input.setAttribute('accept', 'image/*')
      input.removeAttribute('capture')
    } else {
      input.removeAttribute('accept')
      input.removeAttribute('capture')
    }
    input.click()
  }
  const body = desktop ? (
    <label
      className="drop"
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => {
        e.preventDefault()
        const dropped = Array.from(e.dataTransfer?.files ?? [])
        if (dropped.length > 0 && fieldKey) onPickFiles(fieldKey, dropped, true)
      }}
    >
      把日志、照片拖到这里，或点击选择文件
      <input
        type="file"
        multiple
        hidden
        onChange={(e) => {
          const picked = Array.from(e.target.files ?? [])
          e.target.value = ''
          if (picked.length > 0 && fieldKey) onPickFiles(fieldKey, picked, true)
        }}
      />
    </label>
  ) : (
    <div className="att-btns">
      <button type="button" onClick={() => openPicker('photo')}>
        <span className="ic">📷</span>拍照
      </button>
      <button type="button" onClick={() => openPicker('album')}>
        <span className="ic">🖼</span>相册
      </button>
      <button type="button" onClick={() => openPicker('file')}>
        <span className="ic">📄</span>文件
      </button>
      <input
        ref={fileRef}
        type="file"
        multiple
        hidden
        onChange={(e) => {
          const picked = Array.from(e.target.files ?? [])
          e.target.value = ''
          if (picked.length > 0 && fieldKey) onPickFiles(fieldKey, picked, true)
        }}
      />
    </div>
  )
  return (
    <div className="cf area">
      <label>
        照片、截图和日志<i className="opt">选填</i>
      </label>
      <div className="guide">报警画面拍张照、导出的日志，都能帮我们更快找到原因。</div>
      {body}
      <FileChips files={areaFiles} onRemove={onRemoveFile} />
      <div className="small muted note">单个文件不超过 5 MB；日志太大的话，把 NAS 共享盘路径写在说明里就行。</div>
    </div>
  )
}

export function GroupedBriefForm({ linkKey, ticket, initialAuthor, onSaved, onBack, onLinkDead }: GroupedBriefFormProps) {
  // 自由格式（#66）：无模板工单只有一个合成字段，初值取整块说明。
  const templateFields = ticket.brief.template_fields ?? []
  const free = templateFields.length === 0
  const fields: FillField[] = free ? [FREE_FIELD] : templateFields
  const groups = groupsOf(ticket)
  const key = briefDraftKey(linkKey, ticket.number)
  const initialDraft = useRef(loadFormDraft(key))
  const [values, setValues] = useState<Record<string, string>>(() =>
    free
      ? { [NO_TEMPLATE_FIELD_KEY]: ticket.brief.markdown, ...(initialDraft.current?.fields ?? {}) }
      : { ...(ticket.brief.fields ?? {}), ...(initialDraft.current?.fields ?? {}) },
  )
  const [author, setAuthor] = useState(initialDraft.current?.author ?? initialAuthor ?? loadAuthor())
  const [files, setFiles] = useState<StagedFile[]>([])
  const [notice, setNotice] = useState<{ kind: 'down' | 'err'; text: string } | null>(null)
  const [saving, setSaving] = useState(false)
  const [submitted, setSubmitted] = useState(false)
  const [openGroup, setOpenGroup] = useState(0)
  const desktop = useDesktop()
  const member = memberNameOf(ticket)
  const areaField = free ? NO_TEMPLATE_FIELD_KEY : lastTextFieldKey(templateFields)
  const baseRevisionRef = useRef(ticket.brief.revision)
  const idemKeyRef = useRef<string | undefined>(initialDraft.current?.idemKey)
  const pendingRef = useRef(!!initialDraft.current?.pending)
  const valuesRef = useRef(values)
  valuesRef.current = values
  const authorRef = useRef(author)
  authorRef.current = author
  const retryTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const [reqDone, reqTotal] = reqProgress(fields, values)
  const uploading = files.some((f) => f.status === 'uploading')
  const canSubmit = reqDone === reqTotal && !uploading && !saving

  // 实时保存草稿；字段值里的 att: 引用随字段值一并保存。
  useEffect(() => {
    if (submitted) return
    saveFormDraft(key, { fields: values, author, idemKey: idemKeyRef.current, pending: pendingRef.current })
  }, [key, values, author, submitted])

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

  /** 立即上传，把 att: 引用写进对应字段的值（统一附件区挂最后一个文本字段）。 */
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
        setFiles((prev) => prev.map((f) => (f.id === id ? { ...f, status: 'ready', markdown: att.markdown, size: att.size } : f)))
        setValues((prev) => ({ ...prev, [fieldKey]: appendRef(prev[fieldKey] ?? '', att.markdown) }))
      } catch (err) {
        if (err instanceof ApiError && err.status === 401 && (err.code === 'link_revoked' || err.code === 'link_expired')) {
          onLinkDead(err.code)
          return
        }
        const text =
          err instanceof ApiError
            ? err.status === 413
              ? `⚠ ${file.name} 超过上限：${err.message}${err.hint ? `。${err.hint}` : ''}`
              : `${err.message}${err.hint ? `（${err.hint}）` : ''}`
            : `${file.name} 上传失败，请重试。`
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
    // 必填校验只在前端（提交按钮拦截）；修订说明服务端不校验必填。
    setSaving(true)
    try {
      if (!idemKeyRef.current) idemKeyRef.current = crypto.randomUUID()
      pendingRef.current = true
      saveFormDraft(key, { fields: valuesRef.current, author: authorRef.current, idemKey: idemKeyRef.current, pending: true })
      const name = authorRef.current.trim()
      // 自由格式提交整块 brief_md（非模板工单）；模板工单逐字段提交。
      const body = free
        ? { brief_md: valuesRef.current[NO_TEMPLATE_FIELD_KEY] ?? '', base_revision: baseRevisionRef.current, ...(name ? { author_name: name } : {}) }
        : (() => {
            const out: Record<string, string> = {}
            for (const f of templateFields) out[f.key] = valuesRef.current[f.key] ?? ''
            return { fields: out, base_revision: baseRevisionRef.current, ...(name ? { author_name: name } : {}) }
          })()
      await guestApi(linkKey, `/api/v1/tickets/${encodeURIComponent(ticket.number)}`, {
        method: 'PATCH',
        body: JSON.stringify(body),
        idempotencyKey: idemKeyRef.current,
      })
      pendingRef.current = false
      idemKeyRef.current = undefined
      clearFormDraft(key)
      saveAuthor(authorRef.current)
      setNotice(null)
      setSubmitted(true)
      window.scrollTo(0, 0)
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

  function scrollToField(k: string) {
    document.getElementById(`f-${k}`)?.scrollIntoView({ block: 'center' })
  }

  // 提交后的确认页：✓ 已提交给成员，提醒收藏链接；「查看对话」回到对话页。
  if (submitted) {
    return (
      <div className="fill-done">
        <div className="bigok">✓</div>
        <div className="done-title">已提交给{member}</div>
        <div className="done-card">
          {member}看过后会在这里回复你。<b>请收藏这个链接</b>，之后打开就能看到回复，也可以接着补充情况或修改说明。
        </div>
        <button type="button" className="btn big block" onClick={onSaved}>
          查看对话
        </button>
      </div>
    )
  }

  const submitArea = (
    <div className="fill-submit">
      <label className="author-label" htmlFor="fill-author">
        署名
      </label>
      <input id="fill-author" value={author} placeholder="如：检验科 张三" onChange={(e) => setAuthor(e.target.value)} />
      <button type="button" className="btn big block subbtn" disabled={!canSubmit} onClick={() => void save()}>
        {saving ? '保存中…' : `提交给${member}`}
      </button>
      <div className="subhint">不知道的必填项写“不清楚”就行</div>
    </div>
  )

  const fieldProps = {
    onChange: setField,
    onPickFiles: (fieldKey: string, picked: File[], viaArea: boolean) => void pickFiles(fieldKey, picked, viaArea),
    files,
    onRemoveFile: removeFile,
    desktop,
  }

  // 整张工单的附件区（手机）的组：有「发生了什么」组时在它组末，否则退回
  // 最后一组组末（不分组模板与自由格式都只有一组）。附件引用仍挂最后一个
  // 文本字段（areaField 不变）。
  const areaGroup = groups.findIndex((g) => g.title === ATTACH_AREA_GROUP_TITLE)
  const areaGroupIndex = areaGroup >= 0 ? areaGroup : groups.length - 1

  const groupsView = (openAll: boolean) => (
    <>
      {groups.map((g, i) => {
        const [done, total] = reqProgress(g.fields, values)
        const groupDone = done === total
        const open = openAll || openGroup === i
        const last = i === groups.length - 1
        return (
          <section key={g.title} className={`grp${open ? ' open' : ''}`} data-group={g.title}>
            <button type="button" className="grp-hd" onClick={openAll ? undefined : () => setOpenGroup(open ? -1 : i)}>
              <span className={`grp-num${groupDone ? ' ok' : ''}`}>{groupDone ? '✓' : i + 1}</span>
              <span className="grp-t">
                <span className="grp-name">{g.title}</span>
                {g.subtitle && <span className="grp-sub">{g.subtitle}</span>}
              </span>
              <span className="grp-req">{total > 0 ? `必填 ${done}/${total}` : '选填'}</span>
              {!openAll && <span className="chev">{open ? '▴' : '▾'}</span>}
            </button>
            {open && (
              <div className="grp-bd">
                {g.fields.map((f) => (
                  <FieldBlock key={f.key} field={f} value={values[f.key] ?? ''} {...fieldProps} />
                ))}
                {i === areaGroupIndex && !desktop && (
                  <AttachArea fieldKey={areaField} onPickFiles={fieldProps.onPickFiles} files={files} onRemoveFile={removeFile} desktop={desktop} />
                )}
                {!openAll && !last && (
                  <button
                    type="button"
                    className="btn ghost block next-btn"
                    onClick={() => {
                      setOpenGroup(i + 1)
                      window.setTimeout(() => {
                        document.querySelector(`.grp[data-group="${groups[i + 1].title}"]`)?.scrollIntoView({ block: 'start' })
                      }, 0)
                    }}
                  >
                    下一组：{groups[i + 1].title} ›
                  </button>
                )}
              </div>
            )}
          </section>
        )
      })}
    </>
  )

  return (
    <div className={`brief-fill${desktop ? ' desktop' : ' mobile'}`}>
      {notice && (
        <div className={`notice ${notice.kind}`} role="status">
          {notice.text}
        </div>
      )}
      {desktop ? (
        <div className="fill-pc">
          <header className="fill-head">
            <div className="small muted">
              {member} 请你补充 · <button className="linkish back" onClick={onBack}>‹ 返回</button>
            </div>
            <h1>{ticket.title}</h1>
          </header>
          <div className="fill-twocol">
            <div className="fill-groups">{groupsView(true)}</div>
            <aside className="fill-side">
              <div className="fill-card">
                <Progress done={reqDone} total={reqTotal} />
                <div className="fill-check">
                  {fields.map((f) => (
                    <div
                      key={f.key}
                      className={`check-item${filled(values[f.key]) ? ' ok' : ''}`}
                      role="button"
                      tabIndex={0}
                      onClick={() => scrollToField(f.key)}
                      onKeyDown={(e) => e.key === 'Enter' && scrollToField(f.key)}
                    >
                      <span className={`mark${filled(values[f.key]) ? ' ok' : ''}`}>{filled(values[f.key]) ? '✓' : '○'}</span>
                      {f.title}
                      {f.required && <i className="req">*</i>}
                    </div>
                  ))}
                </div>
              </div>
              <div className="fill-card">
                <AttachArea fieldKey={areaField} onPickFiles={fieldProps.onPickFiles} files={files} onRemoveFile={removeFile} desktop={desktop} />
              </div>
              <div className="fill-card">{submitArea}</div>
            </aside>
          </div>
        </div>
      ) : (
        <div className="fill-mob">
          <div className="fill-sticky">
            <div className="fill-toprow">
              <button type="button" className="linkish back" onClick={onBack}>
                ‹ 返回
              </button>
              <span className="fill-title">{ticket.title}</span>
            </div>
            <Progress done={reqDone} total={reqTotal} />
          </div>
          <div className="fill-groups">{groupsView(false)}</div>
          {submitArea}
        </div>
      )}
    </div>
  )
}
