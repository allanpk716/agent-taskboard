// 建单（#41）：第一步选「用模板」或「自由格式」；第二步填表单（模板工单按
// 字段逐项填写，自由格式一整块 Markdown 说明）；维度标签先经 resolve 换成
// 标准值（建单接口只收标准值）；缺必填只弹确认、不拦截（spec 用户故事 6）；
// 创建后显示编号与可直接转发的 share_text。
import { useEffect, useState, type ReactNode } from 'react'
import { ApiError, api } from '../api'
import type { TemplateField } from '../shared/types'
import { fetchMembers, type MemberRef } from './memberApi'
import { Confirm, copyText, invalidate, nav, toast } from './ui'
import { chipClass } from './TicketsPage'

interface TemplateSummary {
  key: string
  name: string
  kind: string
  version: number
  active: boolean
  preset_labels: string[]
}

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
  share_text?: string | null
}

type Step =
  | { phase: 'pick' }
  | { phase: 'form'; template: TemplateDetail | null }
  | { phase: 'done'; created: Created; title: string }

/** 标签输入 → 标准值：先走 resolve（exact/alias/fuzzy），建单只收标准值。 */
async function resolveLabel(q: string): Promise<{ label: string; via: string; alias?: string } | null> {
  const body = await api(`/api/v1/labels/resolve?q=${encodeURIComponent(q)}`)
  const first = (body.matches ?? [])[0]
  return first ? { label: first.label, via: first.via, alias: first.alias } : null
}

export function NewTicketPage() {
  const [step, setStep] = useState<Step>({ phase: 'pick' })
  const [templates, setTemplates] = useState<TemplateSummary[] | null>(null)

  useEffect(() => {
    if (step.phase !== 'pick' || templates) return
    api('/api/v1/templates')
      .then((body) => setTemplates((body.templates ?? []).filter((t: TemplateSummary) => t.active)))
      .catch(() => setTemplates([]))
  }, [step.phase, templates])

  async function pickTemplate(key: string | null) {
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

  if (step.phase === 'pick') {
    return (
      <div className="bpage">
        <div className="sec-t">用模板创建（类型由模板决定，字段逐项填写）</div>
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
              onClick={() => void pickTemplate(t.key)}
              onKeyDown={(e) => e.key === 'Enter' && void pickTemplate(t.key)}
            >
              <div className="tp-name">
                {t.name} <span className="badge kind">{t.kind}</span>
              </div>
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
          <div className="hint">适合简单的事：标题 + Markdown 说明，类型自己选（任务 / 反馈）</div>
        </div>
      </div>
    )
  }

  if (step.phase === 'done') {
    const t = step.created
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
            <div className="sec-t">share_text（可直接转发）</div>
            <div className="linkbox">{t.share_text}</div>
            <div className="btnrow">
              <button
                className="btn"
                onClick={() => {
                  void copyText(t.share_text!).then((ok) =>
                    toast(ok ? '✓ 已复制到剪贴板' : '复制失败，请手动选择文字复制'),
                  )
                }}
              >
                复制 share_text
              </button>
              <button
                className="btn ghost"
                onClick={() => {
                  setStep({ phase: 'pick' })
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
        {!t.share_text && (
          <div className="hint" style={{ textAlign: 'center' }}>
            未指派的工单不会出现在任何人的收件箱里，要在工单列表里找到它。
          </div>
        )}
      </div>
    )
  }

  return <NewTicketForm template={step.template} onDone={(created, title) => setStep({ phase: 'done', created, title })} />
}

/* ---------------- 第二步：表单 ---------------- */

function FieldInput({
  field,
  value,
  onChange,
}: {
  field: TemplateField
  value: string
  onChange: (v: string) => void
}) {
  const id = `nf-${field.key}`
  let ctl: ReactNode
  if (field.type === 'dimension') {
    ctl = (
      <select className="sel" id={id} aria-label={field.title} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">请选择</option>
        {(field.options ?? []).map((o) => (
          <option key={o} value={o === '不确定' ? '' : o}>
            {o}
          </option>
        ))}
      </select>
    )
  } else if (field.type === 'radio') {
    ctl = (
      <div className="radios" aria-label={field.title} role="radiogroup">
        {(field.options ?? []).map((o) => (
          <label key={o}>
            <input type="radio" name={id} value={o} checked={value === o} onChange={() => onChange(o)} /> {o}
          </label>
        ))}
      </div>
    )
  } else {
    ctl = (
      <textarea
        id={id}
        aria-label={field.title}
        placeholder={field.example ? `示例：${field.example}` : ''}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
    )
  }
  return (
    <div className="fld">
      <label htmlFor={id}>
        {field.title}
        {field.required && <i className="req">必填</i>}
      </label>
      {field.guide && <div className="guide">{field.guide}</div>}
      {ctl}
    </div>
  )
}

function NewTicketForm({
  template,
  onDone,
}: {
  template: TemplateDetail | null
  onDone: (created: Created, title: string) => void
}) {
  const [title, setTitle] = useState('')
  const [kind, setKind] = useState('task')
  const [brief, setBrief] = useState('')
  const [fields, setFields] = useState<Record<string, string>>({})
  const [labels, setLabels] = useState<string[]>([])
  const [labelInput, setLabelInput] = useState('')
  const [labelErr, setLabelErr] = useState<string | null>(null)
  const [assignee, setAssignee] = useState('')
  const [members, setMembers] = useState<MemberRef[]>([])
  const [submitErr, setSubmitErr] = useState<string | null>(null)
  const [missing, setMissing] = useState<string[] | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    fetchMembers()
      .then(setMembers)
      .catch(() => {})
  }, [])

  function paintLabels(next: string[]) {
    setLabels(next)
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
    const out: string[] = []
    for (const f of template.fields) {
      if (f.required && !(fields[f.title] ?? '').trim()) out.push(f.title)
    }
    return out
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
      if (f.type === 'dimension' && (fields[f.title] ?? '').trim()) out.push(`${f.dimension}:${fields[f.title]}`)
    }
    return out
  }

  async function create() {
    setBusy(true)
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
          const v = fields[f.title] ?? ''
          if (v.trim()) filled[f.title] = v
        }
        body.fields = filled
      } else {
        body.kind = kind
        body.brief_md = brief
      }
      const created: Created = await api('/api/v1/tickets', {
        method: 'POST',
        body: JSON.stringify(body),
        idempotencyKey: crypto.randomUUID(),
      })
      invalidate()
      onDone(created, title.trim())
      window.scrollTo(0, 0)
    } catch (e) {
      setSubmitErr(
        e instanceof ApiError ? `${e.code}：${e.message}${e.hint ? `（${e.hint}）` : ''}` : '创建失败，请重试',
      )
      window.scrollTo(0, 0)
    } finally {
      setBusy(false)
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
          template.fields.map((f) => (
            <FieldInput key={f.key} field={f} value={fields[f.title] ?? ''} onChange={(v) => setFields({ ...fields, [f.title]: v })} />
          ))
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
            {members.map((m) => (
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
