// 发链接请人填写（#195、#340）：向导里模板已在第二步选定，访客已在第一
// 步选定；这里只填什么事、有效期和跟进人，一次建好工单和带「可填写说明」
// 权限的访客链接。顶部固定“发给：某某”，可回第一步改人；完成页在访客第一
// 次收到工单时醒目提示转发文案里带了主页地址。
import { useEffect, useRef, useState } from 'react'
import { ApiError, api } from '../api'
import { fetchMembers, type MemberRef, type Me, type TemplateSummary } from './memberApi'
import { guestSelectionBody } from './GuestPicker'
import type { WizardGuest } from './GuestPickerStep'
import { copyText, invalidate, nav, toast } from './ui'

interface CreatedFeedback {
  number: string
  seq: number
  guest_link: {
    id: number
    url: string
    note: string
    guest_id: number
    guest_name: string
    expires_at: string
    can_edit_brief: boolean
    share_text: string
  }
}

type Phase =
  | { step: 'form' }
  | { step: 'done'; created: CreatedFeedback; guestName: string; follow: string }

export function FeedbackRequestPage({
  me,
  template,
  guest,
  onChangeGuest,
  onPickAnother,
}: {
  me: Me
  template: TemplateSummary | null
  guest: WizardGuest
  onChangeGuest: () => void
  onPickAnother: () => void
}) {
  const [phase, setPhase] = useState<Phase>({ step: 'form' })
  const [title, setTitle] = useState('')
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
        guest_link: {
          note: '请填写反馈',
          valid_days: days,
          can_edit_brief: true,
          ...guestSelectionBody(guest),
        },
      }
      if (template) {
        body.template = template.key
        body.fields = {}
      } else {
        body.kind = 'report'
        body.brief_md = ''
      }
      if (!idemKeyRef.current) idemKeyRef.current = crypto.randomUUID()
      const created: CreatedFeedback = await api('/api/v1/tickets', {
        method: 'POST',
        body: JSON.stringify(body),
        idempotencyKey: idemKeyRef.current,
      })
      idemKeyRef.current = undefined
      invalidate()
      const guestName = created.guest_link.guest_name || guest.name
      setPhase({ step: 'done', created, guestName, follow: assignee })
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
    const { created, guestName: w, follow } = phase
    const link = created.guest_link
    // 访客未认领（含当场新建）时，后端会在转发文案里附上主页地址；完成页
    // 要醒目提醒成员这段文案第一次带出了访客主页。
    const firstTicket = (guest.kind === 'new' || !guest.claimed) && link.share_text.includes('/h/')
    return (
      <div className="bpage">
        <div className="bigok">✓</div>
        <div className="donet">
          已创建 <b>{created.number}</b>（#{created.seq}）
        </div>
        <div className="hint" style={{ textAlign: 'center' }}>
          链接发给 {w} 就行
        </div>
        {firstTicket && (
          <div className="guest-first-notice" role="alert">
            <b>{w} 第一次收到工单</b>
            转发文案里已附上 TA 的访客主页地址。对方第一次打开任一链接或主页会认领全部工单，
            访问码只显示一次，请提醒 TA 保存。
          </div>
        )}
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
            {follow === me.name
              ? '回复会出现在你的收件箱（「访客回复了」）。'
              : `回复会出现在 ${follow} 的收件箱。`}
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
              setTitle('')
              onChangeGuest()
            }}
          >
            换一位访客
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
      <div className="crumb">
        <a
          className="btn ghost back"
          href="/new"
          onClick={(e) => {
            e.preventDefault()
            onPickAnother()
          }}
        >
          ← 换一个模板
        </a>
        {` 发链接请人填写 · ${template?.name ?? '自由格式'}`}
      </div>
      <div className="mcard form">
        <div className="guest-send-bar" data-testid="guest-send-bar">
          <span>发给：</span>
          <b>{guest.name}</b>
          <span className="sp" />
          <button type="button" className="btn ghost" onClick={onChangeGuest}>
            改访客
          </button>
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
          disabled={!title.trim() || busy}
          onClick={() => void generate()}
        >
          生成链接
        </button>
      </div>
    </div>
  )
}
