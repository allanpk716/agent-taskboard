// 建单（#41）：第一步选「用模板」或「自由格式」；第二步填表单——模板工单
// 复用 #39 的 FieldForm（按字段分区、维度字段可按别名搜索、统一附件区，
// 附件先暂存本机，建单后立即上传并挂到整张工单的附件）；自由格式一整块
// Markdown 说明。维度标签先经 resolve 换成标准值（建单接口只收标准值）；
// 缺必填只弹确认、不拦截（spec 用户故事 6）；创建指派给同事后嵌入
// 「发给指派人」交接面板。
import { useEffect, useRef, useState } from 'react'
import { ApiError, api } from '../api'
import type { TemplateField } from '../shared/types'
import {
  fetchMembers,
  fetchTicket,
  uploadMemberAttachment,
  type MemberRef,
  type Me,
  type TemplateSummary,
} from './memberApi'
import { FeedbackRequestPage } from './FeedbackRequestPage'
import { HandoffPanel } from './HandoffDialog'
import { Confirm, invalidate, nav, toast } from './ui'
import { chipClass } from './TicketsPage'
import { FieldForm, appendRef, missingRequired, type StagedFile } from '../guest/FieldForm'
import {
  clearUnsent,
  loadUnsentNewTicket,
  saveUnsentNewTicket,
  unsentNewTicketKey,
} from '../shared/unsentInput'

interface TemplateDetail {
  key: string
  name: string
  kind: string
  fields: TemplateField[]
}

interface Created {
  number: string
  seq: number
  warnings: { field: string; message: string }[]
  assignee?: { id: number; name: string } | null
  share_text?: string | null
  handoff_text?: string
}

type Filler = 'self' | 'guest'

type Step =
  | { phase: 'who' }
  | { phase: 'pick'; filler: Filler }
  | { phase: 'guest'; template: TemplateSummary | null }
  | { phase: 'form'; template: TemplateDetail | null }
  | { phase: 'done'; created: Created; title: string; assignee: string }

/** 标签输入 → 标准值：先走 resolve（exact/alias/fuzzy），建单只收标准值。 */
async function resolveLabel(q: string): Promise<{ label: string; via: string; alias?: string } | null> {
  const body = await api(`/api/v1/labels/resolve?q=${encodeURIComponent(q)}`)
  const first = (body.matches ?? [])[0]
  return first ? { label: first.label, via: first.via, alias: first.alias } : null
}

const KIND_TEXT: Record<string, string> = { task: '任务', report: '反馈' }

export function NewTicketPage({
  me,
  initialFiller = 'self',
  onPhaseTitleChange,
}: {
  me: Me
  initialFiller?: Filler
  onPhaseTitleChange?: (title: string) => void
}) {
  const [step, setStep] = useState<Step>(initialFiller === 'guest' ? { phase: 'pick', filler: 'guest' } : { phase: 'who' })
  const [templates, setTemplates] = useState<TemplateSummary[] | null>(null)

  useEffect(() => {
    onPhaseTitleChange?.(step.phase === 'guest' ? '发链接请人填写' : '建单')
  }, [step.phase, onPhaseTitleChange])

  useEffect(() => {
    if (step.phase !== 'pick' || templates) return
    api('/api/v1/templates')
      .then((body) => setTemplates((body.templates ?? []).filter((t: TemplateSummary) => t.active)))
      .catch(() => setTemplates([]))
  }, [step.phase, templates])

  async function pickTemplate(summary: TemplateSummary | null) {
    if (step.phase !== 'pick') return
    if (step.filler === 'guest') {
      setStep({ phase: 'guest', template: summary })
      return
    }
    const key = summary?.key ?? null
    if (key === null) {
      setStep({ phase: 'form', template: null })
      return
    }
    try {
      const detail: TemplateDetail = await api(`/api/v1/templates/${encodeURIComponent(key)}`)
      setStep({ phase: 'form', template: detail })
    } catch (e) {
      toast(e instanceof ApiError ? e.message : '读取模板失败')
    }
  }

  if (step.phase === 'who') {
    return (
      <div className="bpage">
        <div className="mcard create-choice">
          <h2 className="page-title">建单</h2>
          <p className="guide">先选这张工单的说明由谁来填写</p>
          <div className="create-cards">
            <button type="button" className="create-card" onClick={() => setStep({ phase: 'pick', filler: 'self' })}>
              <span className="tp-name">✍️ 我自己填</span>
              <span className="hint">派任务、提需求、问问题：现在就把说明填好，再指派给同事</span>
              <span className="next">下一步：选模板 →</span>
            </button>
            <button type="button" className="create-card" onClick={() => setStep({ phase: 'pick', filler: 'guest' })}>
              <span className="tp-name">🔗 发链接请人填写</span>
              <span className="hint">给部门外的人发链接，由对方填写，对方不需要账号</span>
              <span className="next">下一步：选模板 →</span>
            </button>
          </div>
        </div>
      </div>
    )
  }

  if (step.phase === 'pick') {
    return (
      <div className="bpage">
        <div className="mcard template-screen">
          <button type="button" className="btn ghost back" onClick={() => setStep({ phase: 'who' })}>
            ← 建单
          </button>
          <h2 className="page-title">选一个模板</h2>
          <span className="badge filler">{step.filler === 'self' ? '我自己填' : '发链接请人填写'}</span>
          <p className="guide">
            {step.filler === 'self'
              ? '按你要做的事选，模板决定要填哪些内容'
              : '对方会按这个模板的引导来填写'}
          </p>
        </div>
        {templates === null ? (
          <div className="ecard">加载中…</div>
        ) : templates.length === 0 ? (
          <div className="ecard">还没有启用中的模板。</div>
        ) : (
          templates.map((t) => (
            <div
              key={t.key}
              className="tplpick"
              role="button"
              tabIndex={0}
              onClick={() => void pickTemplate(t)}
              onKeyDown={(e) => e.key === 'Enter' && void pickTemplate(t)}
            >
              <div className="tp-name">
                {t.name} <span className="badge kind">{KIND_TEXT[t.kind] ?? t.kind}</span>
              </div>
              {t.scenario && <div className="hint">适用：{t.scenario}</div>}
              {t.preset_labels?.length > 0 && (
                <div className="hint">预设 {t.preset_labels.join('、')}</div>
              )}
            </div>
          ))
        )}
        <div className="sec-t">不用模板（直接写标题和一整块 Markdown 说明）</div>
        <div
          className="tplpick"
          role="button"
          tabIndex={0}
          onClick={() => void pickTemplate(null)}
          onKeyDown={(e) => e.key === 'Enter' && void pickTemplate(null)}
        >
          <div className="tp-name">自由格式</div>
          <div className="hint">
            {step.filler === 'self'
              ? '适合简单的事：标题 + Markdown 说明，类型自己选（任务 / 反馈）'
              : '不用模板，对方自由填写，工单类型为反馈'}
          </div>
        </div>
      </div>
    )
  }

  if (step.phase === 'guest') {
    return (
      <FeedbackRequestPage
        me={me}
        template={step.template}
        onPickAnother={() => setStep({ phase: 'pick', filler: 'guest' })}
      />
    )
  }

  if (step.phase === 'done') {
    const t = step.created
    const assignee = step.assignee
    const unassigned = t.assignee === undefined ? !assignee : t.assignee === null
    return (
      <div className="bpage">
        <div className="bigok">✓</div>
        <div className="donet">
          已创建 <b>{t.number}</b>（#{t.seq}）
        </div>
        <div className="hint" style={{ textAlign: 'center' }}>
          {step.title}
        </div>
        {t.warnings?.length > 0 && (
          <div className="warnbox">
            <b>{t.warnings.length} 项必填未填（已按约定放行）：</b>
            {t.warnings.map((w) => w.field).join('、')}。可以发一条「可填写说明」的访客链接请对方补全。
          </div>
        )}
        {t.share_text ? (
          <>
            <div className="sec-t">📨 发给 {assignee}</div>
            <HandoffPanel
              handoffText={t.handoff_text ?? ''}
              handoffShareText={t.share_text}
              assigneeName={assignee}
              mode="share"
            />
            <div className="btnrow">
              <button
                className="btn ghost"
                onClick={() => {
                  setStep({ phase: 'pick', filler: 'self' })
                  window.scrollTo(0, 0)
                }}
              >
                再建一张
              </button>
            </div>
          </>
        ) : (
          <div className="btnrow" />
        )}
        <div className="btnrow">
          <button
            className="btn ghost"
            onClick={() => {
              invalidate()
              nav(`/t/${t.number}`)
            }}
          >
            打开工单
          </button>
          <button
            className="btn ghost"
            onClick={() => {
              invalidate()
              nav('/')
            }}
          >
            返回收件箱
          </button>
        </div>
        {unassigned && (
          <div className="hint" style={{ textAlign: 'center' }}>
            未指派的工单不会出现在任何人的收件箱里，要在工单列表里找到它。
          </div>
        )}
      </div>
    )
  }

  return (
    <NewTicketForm
      me={me}
      template={step.template}
      onDone={(created, title, assignee) => setStep({ phase: 'done', created, title, assignee })}
    />
  )
}

/* ---------------- 第二步：表单 ---------------- */

/** 暂存选中的文件（本机预览；建单成功后才上传，照 #39 入口页的模式）。 */
function stageFilesOf(fieldKey: string, picked: File[], area: boolean): StagedFile[] {
  return picked.map((file, i) => ({
    id: `${fieldKey}-${Date.now()}-${i}-${file.name}`,
    name: file.name,
    size: file.size,
    fieldKey,
    area,
    status: 'ready' as const,
    previewUrl: file.type.startsWith('image/') ? URL.createObjectURL(file) : undefined,
    blob: file,
  }))
}

/** 建单后补传暂存附件：字段内的引用写回字段，附件区的文件挂整张工单的附件。 */
async function attachStagedFiles(
  number: string,
  stagedFiles: StagedFile[],
): Promise<{ field: string; message: string }[]> {
  const staged = stagedFiles.filter((f) => f.blob)
  if (staged.length === 0) return []
  const warnings: { field: string; message: string }[] = []
  const refsByField: Record<string, string[]> = {}
  const attachmentIds: string[] = []
  for (const f of staged) {
    try {
      const att = await uploadMemberAttachment(number, f.blob!)
      if (f.area) attachmentIds.push(att.id)
      else (refsByField[f.fieldKey] ??= []).push(att.markdown)
    } catch (err) {
      const why = err instanceof ApiError ? `${err.message}${err.hint ? `（${err.hint}）` : ''}` : '上传失败'
      warnings.push({ field: f.name, message: `未能附上（${why}），请打开工单后用回复补充。` })
    }
  }
  if (attachmentIds.length === 0 && Object.keys(refsByField).length === 0) return warnings
  try {
    const fresh = await api(`/api/v1/tickets/${encodeURIComponent(number)}`)
    const body: Record<string, unknown> = { base_revision: fresh.brief.revision }
    if (Object.keys(refsByField).length > 0) {
      const merged: Record<string, string> = { ...(fresh.brief.fields ?? {}) }
      for (const [fieldKey, refs] of Object.entries(refsByField)) {
        merged[fieldKey] = appendRef(merged[fieldKey] ?? '', refs.join('\n'))
      }
      body.fields = merged
    }
    if (attachmentIds.length > 0) body.attachment_ids = attachmentIds
    await api(`/api/v1/tickets/${encodeURIComponent(number)}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
      idempotencyKey: crypto.randomUUID(),
    })
  } catch (err) {
    const why = err instanceof ApiError ? err.message : '网络问题'
    warnings.push({ field: '附件', message: `已上传，但没能挂到整张工单的附件（${why}），请打开工单后补充。` })
  }
  return warnings
}

function NewTicketForm({
  me,
  template,
  onDone,
}: {
  me: Me
  template: TemplateDetail | null
  onDone: (created: Created, title: string, assignee: string) => void
}) {
  // 未提交的输入（#262）：挂载时读回本机保存的内容；键按成员和模板区分。
  const unsentKey = unsentNewTicketKey(me.id, template?.key ?? null)
  const [unsent] = useState(() => loadUnsentNewTicket(unsentKey))
  const [title, setTitle] = useState(unsent?.title ?? '')
  const [kind, setKind] = useState(unsent?.kind ?? 'task')
  const [brief, setBrief] = useState(unsent?.brief ?? '')
  const [values, setValues] = useState<Record<string, string>>(unsent?.values ?? {})
  const [files, setFiles] = useState<StagedFile[]>([])
  const [labels, setLabels] = useState<string[]>(unsent?.labels ?? [])
  const [labelInput, setLabelInput] = useState(unsent?.labelInput ?? '')
  const [labelErr, setLabelErr] = useState<string | null>(null)
  const [assignee, setAssignee] = useState(unsent?.assignee ?? '')
  const [members, setMembers] = useState<MemberRef[]>([])
  const [submitErr, setSubmitErr] = useState<string | null>(null)
  const [missing, setMissing] = useState<string[] | null>(null)
  const [busy, setBusy] = useState(false)
  // 建单动作的幂等键与自动重试（评审 P2-13，照访客 Composer 的做法）。
  const idemKeyRef = useRef<string | undefined>(undefined)
  const retryTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  function scheduleCreateRetry() {
    if (retryTimer.current) clearTimeout(retryTimer.current)
    retryTimer.current = setTimeout(() => void create(), 3000)
  }

  useEffect(() => {
    return () => {
      if (retryTimer.current) clearTimeout(retryTimer.current)
    }
  }, [])

  useEffect(() => {
    fetchMembers()
      .then(setMembers)
      .catch(() => {})
  }, [])

  // 文字内容实时写进本机；全部清空即视为没有未提交的内容，直接清除。
  // 暂存附件还是本机文件对象，没法跨刷新保存（建单成功后才上传）。
  useEffect(() => {
    const empty =
      title.trim() === '' &&
      brief.trim() === '' &&
      labelInput.trim() === '' &&
      assignee === '' &&
      kind === 'task' &&
      labels.length === 0 &&
      Object.values(values).every((v) => !v.trim())
    if (empty) {
      clearUnsent(unsentKey)
      return
    }
    saveUnsentNewTicket(unsentKey, { title, kind, brief, values, labels, labelInput, assignee })
  }, [unsentKey, title, kind, brief, values, labels, labelInput, assignee])

  function paintLabels(next: string[]) {
    setLabels(next)
  }

  function stageFiles(fieldKey: string, picked: File[], area: boolean) {
    setFiles((fs) => [...fs, ...stageFilesOf(fieldKey, picked, area)])
  }

  async function addLabel() {
    const s = labelInput.trim()
    if (!s) {
      setLabelErr('请输入标签，例如 机型:UD-2300 或 待初筛')
      return
    }
    try {
      const hit = await resolveLabel(s)
      if (!hit) {
        setLabelErr(
          `字典里没有 ${s}。建单只能用字典中的标准值（可以先去详情页打标签时选「允许新建」）`,
        )
        return
      }
      if (labels.includes(hit.label)) {
        setLabelErr('已经加过这个标签了')
        return
      }
      paintLabels([...labels, hit.label])
      setLabelInput('')
      setLabelErr(null)
      if (hit.via === 'alias') toast(`别名「${hit.alias ?? s}」已换成标准值 ${hit.label}`)
      else if (hit.via === 'fuzzy') toast(`按近似值匹配到 ${hit.label}`)
    } catch (e) {
      setLabelErr(e instanceof ApiError ? `${e.code}：${e.message}` : '解析标签失败，请重试')
    }
  }

  function collectMissing(): string[] {
    if (!template) return []
    return missingRequired(template.fields, values).map(
      (k) => template!.fields.find((f) => f.key === k)?.title ?? k,
    )
  }

  async function submit() {
    setSubmitErr(null)
    if (!title.trim()) {
      setSubmitErr('title_required：标题必填')
      window.scrollTo(0, 0)
      return
    }
    const hasDim = labels.some((l) => l.includes(':')) || collectTemplateDimLabels().length > 0
    if (!hasDim) {
      setSubmitErr('dimension_label_required：至少要打一个维度标签，否则不能提交。例如 机型:UD-2300 或 立项:PRJ-2411。')
      return
    }
    const miss = collectMissing()
    if (miss.length > 0) {
      setMissing(miss)
      return
    }
    await create()
  }

  /** 模板维度字段选中的值会自动成为标签（服务端），前端只做提示。 */
  function collectTemplateDimLabels(): string[] {
    if (!template) return []
    const out: string[] = []
    for (const f of template.fields) {
      const v = values[f.key] ?? ''
      if (f.type === 'dimension' && v.trim() && v !== '不确定') out.push(`${f.dimension}:${v}`)
    }
    return out
  }

  async function create() {
    setBusy(true)
    let willRetry = false
    try {
      const body: Record<string, unknown> = {
        title: title.trim(),
        labels,
        ...(assignee ? { assignee } : {}),
      }
      if (template) {
        body.template = template.key
        const filled: Record<string, string> = {}
        for (const f of template.fields) {
          const v = values[f.key] ?? ''
          if (v.trim()) filled[f.title] = v
        }
        body.fields = filled
      } else {
        body.kind = kind
        body.brief_md = brief
      }
      // 同一次建单动作复用同一个 Idempotency-Key（ADR 0002「放心自动重试」，
      // 评审 P2-13）：网络失败/5xx 自动重试不会建出第二张单。
      if (!idemKeyRef.current) idemKeyRef.current = crypto.randomUUID()
      const created: Created = await api('/api/v1/tickets', {
        method: 'POST',
        body: JSON.stringify(body),
        idempotencyKey: idemKeyRef.current,
      })
      idemKeyRef.current = undefined // 成功：下一次建单是新动作
      clearUnsent(unsentKey) // 工单已建出（2xx）：本机保存的未提交内容随之清除
      // 暂存的附件此刻上传并挂到整张工单的附件（建单本身不带附件）。
      const warnings = await attachStagedFiles(created.number, files)
      created.warnings = [...created.warnings, ...warnings]
      if (created.share_text) {
        try {
          created.handoff_text = (await fetchTicket(created.number)).handoff_text
        } catch {
          created.share_text = undefined
        }
      }
      invalidate()
      onDone(created, title.trim(), assignee)
      window.scrollTo(0, 0)
    } catch (e) {
      if (!(e instanceof ApiError) || e.status >= 500 || e.status === 429) {
        // 服务暂时不可用/网络错误：自动重试（同一个 key），表单保持锁定。
        willRetry = true
        setSubmitErr('服务更新中，正在自动重试…')
        window.scrollTo(0, 0)
        scheduleCreateRetry()
        return
      }
      // 4xx：内容被拒，改完再提交是新动作。
      idemKeyRef.current = undefined
      setSubmitErr(`${e.code}：${e.message}${e.hint ? `（${e.hint}）` : ''}`)
      window.scrollTo(0, 0)
    } finally {
      if (!willRetry) setBusy(false)
    }
  }

  return (
    <div className="bpage">
      <div className="crumb">
        <a
          href="/new"
          onClick={(e) => {
            e.preventDefault()
            nav('/new')
          }}
        >
          ← 换一种方式
        </a>
        {template ? ` 用模板「${template.name}」创建` : ' 自由格式创建'}
      </div>
      <div className="mcard form">
        <div className="fld">
          <label htmlFor="nt-title">
            标题<i className="req">必填</i>
          </label>
          <div className="guide">一句话讲清这件事。</div>
          <input
            id="nt-title"
            aria-label="标题"
            type="text"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="例如：整理 SL-1000 遗留问题清单"
          />
        </div>
        {template ? (
          <FieldForm
            fields={template.fields}
            values={values}
            onChange={(k, v) => setValues({ ...values, [k]: v })}
            onPickFiles={(fieldKey, picked, area) => stageFiles(fieldKey, picked, area)}
            files={files}
            onRemoveFile={(id) => setFiles((fs) => fs.filter((f) => f.id !== id))}
          />
        ) : (
          <>
            <div className="fld">
              <label>类型</label>
              <div className="radios">
                <label>
                  <input type="radio" name="nt-kind" checked={kind === 'task'} onChange={() => setKind('task')} /> 任务
                  （派出去请人完成）
                </label>
                <label>
                  <input type="radio" name="nt-kind" checked={kind === 'report'} onChange={() => setKind('report')} />{' '}
                  反馈（外部提进来请部门处理）
                </label>
              </div>
            </div>
            <div className="fld">
              <label htmlFor="nt-brief">说明（一整块 Markdown）</label>
              <div className="guide">背景、要求、验收标准都可以写在这里。</div>
              <textarea
                id="nt-brief"
                aria-label="说明"
                style={{ minHeight: 120 }}
                value={brief}
                onChange={(e) => setBrief(e.target.value)}
              />
            </div>
          </>
        )}
        <div className="fld">
          <label>维度标签（至少一个维度）</label>
          <div className="guide">
            至少打一个「维度:值」标签才能提交；同一维度可以打多个值；写别名会自动换成标准值（试试 1680）。
            {template && ' 模板维度字段选中的值会自动并入标签。'}
          </div>
          <div className="lblrow">
            {labels.length === 0 ? (
              <span className="hint">还没打标签</span>
            ) : (
              labels.map((l) => (
                <span key={l} className={chipClass(l)}>
                  {l}
                  <i
                    title="移除"
                    onClick={() => {
                      paintLabels(labels.filter((x) => x !== l))
                    }}
                  >
                    ×
                  </i>
                </span>
              ))
            )}
          </div>
          <div className="inrow">
            <input
              aria-label="标签输入"
              placeholder="机型:UD-2300、系统:中间件、待初筛 …"
              value={labelInput}
              onChange={(e) => setLabelInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault()
                  void addLabel()
                }
              }}
            />
            <button className="btn ghost" type="button" onClick={() => void addLabel()}>
              添加
            </button>
          </div>
          {labelErr && <div className="errbox">{labelErr}</div>}
        </div>
        <div className="fld">
          <label htmlFor="nt-assignee">指派给</label>
          <div className="guide">可以现在指派，也可以建好之后再说；只能指派成员。</div>
          <select
            id="nt-assignee"
            className="sel"
            aria-label="指派给"
            value={assignee}
            onChange={(e) => setAssignee(e.target.value)}
          >
            <option value="">暂不指派</option>
            {members.filter((m) => m.active !== false).map((m) => (
              <option key={m.id} value={m.name}>
                {m.name}
              </option>
            ))}
          </select>
        </div>
      </div>
      {submitErr && <div className="errbox">{submitErr}</div>}
      <div className="submitrow">
        <button className="btn big" onClick={() => void submit()} disabled={busy}>
          创建工单
        </button>
      </div>
      {missing !== null && (
        <Confirm
          title="还有必填项没填"
          body={
            <div className="warnbox">
              按约定，成员建单缺必填字段<b>只提醒、不拦截</b>——可以先建单，再交给访客或对方补全：
              <ul className="misslist">
                {missing.map((m) => (
                  <li key={m}>{m}</li>
                ))}
              </ul>
            </div>
          }
          yesLabel="仍然创建"
          onYes={() => {
            setMissing(null)
            void create()
          }}
          onCancel={() => setMissing(null)}
        />
      )}
    </div>
  )
}
