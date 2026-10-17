// 「请人反馈」（#64）：一屏向导。两项必填——「请谁反馈」（作链接备注）和
// 「什么事」（作工单标题）；单选让对方填哪份启用中的反馈类模板（默认 Bug
// 反馈）或「自由描述」；有效期（7/30/90 天，默认 30）和跟进人（默认本人）
// 收在折叠的「更多选项」里。点「生成链接」一次建好工单和带「可填写说明」
// 权限的访客链接（服务端免维度标签）；完成页展示服务端生成的转发文案，
// 可一键复制。视觉参考原型 prototype/feedback-wizard 的 a.html。
import { useEffect, useRef, useState } from 'react'
import { ApiError, api } from '../api'
import {
  fetchMembers,
  fetchTemplate,
  fetchTemplates,
  type MemberRef,
  type Me,
  type TemplateField,
} from './memberApi'
import { copyText, invalidate, nav, toast } from './ui'

/** 反馈类模板 + 「自由描述」。 */
const FREE = '__free__'

interface WizardOption {
  key: string // 模板 key 或 FREE
  name: string
  desc: string
}

interface CreatedFeedback {
  number: string
  seq: number
  guest_link: {
    id: number
    url: string
    note: string
    expires_at: string
    can_edit_brief: boolean
    share_text: string
  }
}

type Phase =
  | { step: 'form' }
  | { step: 'done'; created: CreatedFeedback; who: string; follow: string }

/** 每个模板选项下面的一句白话：有分组时写「会问：组名…」（#63 上线前
 * groups 不在响应里，防御式读取）；没有分组时取前三个字段的问句。 */
function templateDesc(detail: {
  groups?: { title: string }[]
  fields: (TemplateField & { group?: number })[]
}): string {
  const groups = detail.groups ?? []
  if (groups.length > 0) {
    return `会问：${groups.map((g) => g.title).join('、')}`
  }
  const questions = detail.fields.slice(0, 3).map((f) => f.title)
  if (questions.length === 0) return '对方按引导填写说明。'
  return `会问：${questions.join('、')}`
}

export function FeedbackRequestPage({ me }: { me: Me }) {
  const [phase, setPhase] = useState<Phase>({ step: 'form' })
  const [options, setOptions] = useState<WizardOption[] | null>(null)
  const [who, setWho] = useState('')
  const [title, setTitle] = useState('')
  const [tpl, setTpl] = useState<string | null>(null)
  const [days, setDays] = useState(30)
  const [assignee, setAssignee] = useState(me.name)
  const [members, setMembers] = useState<MemberRef[]>([])
  const [submitErr, setSubmitErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // 同一次「生成链接」动作复用同一个 Idempotency-Key：网络失败/5xx 自动重试
  // 不会建出第二张单（照 NewTicketPage 的做法，ADR 0002）。
  const idemKeyRef = useRef<string | undefined>(undefined)
  const retryTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    return () => {
      if (retryTimer.current) clearTimeout(retryTimer.current)
    }
  }, [])

  // 模板选项：启用中的反馈类模板 + 自由描述；默认 Bug 反馈（不在时取第一项）。
  useEffect(() => {
    if (options !== null) return
    let alive = true
    ;(async () => {
      try {
        const list = (await fetchTemplates()).filter((t) => t.active && t.kind === 'report')
        const opts: WizardOption[] = []
        for (const t of list) {
          const detail = await fetchTemplate(t.key).catch(() => null)
          opts.push({
            key: t.key,
            name: t.name,
            desc: detail ? templateDesc(detail) : '对方按模板引导填写。',
          })
        }
        opts.push({ key: FREE, name: '自由描述', desc: '对方写一整段情况说明，可以传日志和照片。' })
        if (!alive) return
        setOptions(opts)
        setTpl(opts.some((o) => o.key === 'bug-report') ? 'bug-report' : (opts[0]?.key ?? FREE))
      } catch {
        if (alive) setOptions([{ key: FREE, name: '自由描述', desc: '对方写一整段情况说明，可以传日志和照片。' }])
      }
    })()
    return () => {
      alive = false
    }
  }, [options])

  useEffect(() => {
    fetchMembers()
      .then((ms) => {
        if (ms.length > 0) setMembers(ms)
      })
      .catch(() => {})
  }, [])

  function scheduleRetry() {
    if (retryTimer.current) clearTimeout(retryTimer.current)
    retryTimer.current = setTimeout(() => void generate(), 3000)
  }

  async function generate() {
    setBusy(true)
    let willRetry = false
    try {
      const body: Record<string, unknown> = {
        title: title.trim(),
        assignee,
        labels: [],
        guest_link: { note: who.trim(), valid_days: days, can_edit_brief: true },
      }
      if (tpl === FREE) {
        // 自由描述：一张不用模板的反馈工单，说明为空（访客整块写）。
        body.kind = 'report'
        body.brief_md = ''
      } else {
        body.template = tpl
        body.fields = {}
      }
      if (!idemKeyRef.current) idemKeyRef.current = crypto.randomUUID()
      const created: CreatedFeedback = await api('/api/v1/tickets', {
        method: 'POST',
        body: JSON.stringify(body),
        idempotencyKey: idemKeyRef.current,
      })
      idemKeyRef.current = undefined
      invalidate()
      setPhase({ step: 'done', created, who: who.trim(), follow: assignee })
      window.scrollTo(0, 0)
    } catch (e) {
      if (!(e instanceof ApiError) || e.status >= 500 || e.status === 429) {
        willRetry = true
        setSubmitErr('服务暂时不可用，正在自动重试…')
        scheduleRetry()
        return
      }
      idemKeyRef.current = undefined
      setSubmitErr(`${e.code}：${e.message}${e.hint ? `（${e.hint}）` : ''}`)
    } finally {
      if (!willRetry) setBusy(false)
    }
  }

  if (phase.step === 'done') {
    const { created, who: w, follow } = phase
    const link = created.guest_link
    return (
      <div className="bpage">
        <div className="bigok">✓</div>
        <div className="donet">
          已创建 <b>{created.number}</b>（#{created.seq}）
        </div>
        <div className="hint" style={{ textAlign: 'center' }}>
          链接发给 {w} 就行
        </div>
        <div className="mcard fr-card">
          <div style={{ fontWeight: 600 }}>转发文案</div>
          <div className="linkbox">{link.share_text}</div>
          <div className="btnrow">
            <button
              className="btn"
              onClick={() => {
                void copyText(link.share_text).then((ok) =>
                  toast(ok ? '✓ 已复制到剪贴板' : '复制失败，请手动选择文字复制'),
                )
              }}
            >
              复制文案
            </button>
            <button
              className="btn ghost"
              onClick={() => {
                void copyText(link.url).then((ok) =>
                  toast(ok ? '✓ 已复制链接' : '复制失败，请手动选择文字复制'),
                )
              }}
            >
              只复制链接
            </button>
          </div>
          <div className="hint">
            对方打开后会看到你写的标题，并被引导补全说明（可传日志和照片）。
            {follow === me.name ? '回复会出现在你的收件箱（「访客回复了」）。' : `回复会出现在 ${follow} 的收件箱。`}
          </div>
        </div>
        <div className="btnrow" style={{ justifyContent: 'center' }}>
          <button
            className="btn ghost"
            onClick={() => {
              invalidate()
              nav(`/t/${created.number}`)
            }}
          >
            打开工单
          </button>
          <button
            className="btn ghost"
            onClick={() => {
              setWho('')
              setTitle('')
              setPhase({ step: 'form' })
              window.scrollTo(0, 0)
            }}
          >
            再请一个人
          </button>
          <button
            className="btn ghost"
            onClick={() => {
              invalidate()
              nav('/')
            }}
          >
            回收件箱
          </button>
        </div>
      </div>
    )
  }

  const followText = assignee === me.name ? '由我跟进' : `由${assignee}跟进`
  return (
    <div className="bpage">
      <div className="mcard form">
        <div className="fld">
          <label htmlFor="fr-who">
            请谁反馈<i className="req">必填</i>
          </label>
          <div className="guide">写部门和名字，之后用来区分是谁回的。</div>
          <input
            id="fr-who"
            aria-label="请谁反馈"
            type="text"
            placeholder="例如：检验科-张三"
            value={who}
            onChange={(e) => setWho(e.target.value)}
          />
        </div>
        <div className="fld">
          <label htmlFor="fr-title">
            什么事<i className="req">必填</i>
          </label>
          <div className="guide">一句话就行，对方口头讲过的大概情况。</div>
          <input
            id="fr-title"
            aria-label="什么事"
            type="text"
            placeholder="例如：UD-2300 质控报 E-203"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
          />
        </div>
        <div className="fld">
          <label>请对方填写</label>
          {options === null ? (
            <div className="hint">加载模板…</div>
          ) : (
            <>
              <div className="chips" role="radiogroup" aria-label="请对方填写">
                {options.map((o) => (
                  <label key={o.key} className={`chip${tpl === o.key ? ' on' : ''}`}>
                    <input
                      type="radio"
                      name="fr-tpl"
                      checked={tpl === o.key}
                      onChange={() => setTpl(o.key)}
                    />
                    {o.name}
                    {o.key === 'bug-report' ? '（推荐）' : ''}
                  </label>
                ))}
              </div>
              <div className="hint" style={{ marginTop: 6 }}>
                {options.find((o) => o.key === tpl)?.desc}
              </div>
            </>
          )}
        </div>
        <details className="fr-more">
          <summary>
            更多选项：有效期 {days} 天 · {followText}
          </summary>
          <div className="fld">
            <label>链接有效期</label>
            <div className="chips" role="radiogroup" aria-label="链接有效期">
              {[7, 30, 90].map((d) => (
                <label key={d} className={`chip${days === d ? ' on' : ''}`}>
                  <input type="radio" name="fr-days" checked={days === d} onChange={() => setDays(d)} />
                  {d} 天
                </label>
              ))}
            </div>
          </div>
          <div className="fld">
            <label htmlFor="fr-follow">谁来跟进</label>
            <select
              id="fr-follow"
              className="sel"
              aria-label="谁来跟进"
              value={assignee}
              onChange={(e) => setAssignee(e.target.value)}
            >
              <option value={me.name}>我（{me.name}）</option>
              {members
                .filter((m) => m.name !== me.name && m.active !== false)
                .map((m) => (
                  <option key={m.id} value={m.name}>
                    {m.name}
                  </option>
                ))}
            </select>
            <div className="guide">对方回复后出现在跟进人的收件箱。</div>
          </div>
        </details>
      </div>
      {submitErr && <div className="errbox">{submitErr}</div>}
      <div className="submitrow">
        <button
          className="btn big"
          disabled={!who.trim() || !title.trim() || busy || options === null}
          onClick={() => void generate()}
        >
          生成链接
        </button>
        <div className="hint" style={{ textAlign: 'center', marginTop: 8 }}>
          需要填完整说明、打标签？用{' '}
          <a
            href="/new"
            onClick={(e) => {
              e.preventDefault()
              nav('/new')
            }}
          >
            建单（高级）
          </a>
        </div>
      </div>
    </div>
  )
}
