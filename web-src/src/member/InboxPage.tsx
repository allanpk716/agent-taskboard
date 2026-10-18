// 收件箱（#41）：「现在轮到我」的开启工单，卡片顶部是醒目的原因色条与文案，
// 一眼知道轮到我做什么。名下所有成员 Agent 共用这一份；未指派的工单不会
// 出现在收件箱里（要用查询接口找）。
import { useCallback, useEffect, useState } from 'react'
import { fetchInbox, reasonClass, type InboxItem, type InboxResponse } from './memberApi'
import { nav, useInvalidate } from './ui'
import { TicketCard } from './TicketsPage'

export function InboxPage({
  desktop,
  selected,
  me,
}: {
  desktop?: boolean
  selected?: string
  me?: { name: string }
}) {
  const [data, setData] = useState<InboxResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [mode, setMode] = useState<'mine' | 'guest'>('mine')
  const [q, setQ] = useState('')

  const load = useCallback(async () => {
    try {
      setData(await fetchInbox())
      setError(null)
    } catch (e: any) {
      setError(e?.message ?? '读取收件箱失败')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])
  useInvalidate(load)

  if (desktop) {
    const filtered = (data?.items ?? []).filter((it) => {
      const guestReply = it.reason.includes('访客')
      return mode === 'mine' ? !guestReply || it.reason === '待初筛' : guestReply
    })
    const shown = q
      ? filtered.filter((it) => it.title.includes(q) || it.number.includes(q) || it.labels.some((l) => l.includes(q)))
      : filtered
    const asCardItem = (it: InboxItem) => ({
      ...it,
      seq: 0,
      status: 'open',
      waiting_on: { party: '', member: it.who || null, text: it.waiting_on, reason: it.reason },
    })
    return (
      <div className="pc-list-pane inbox" data-testid="inbox-list-pane">
        <header className="pc-list-head">
          <div>
            <h2>收件箱</h2>
            <span>{data?.count ?? '…'} 张</span>
          </div>
          <input
            placeholder="搜索"
            aria-label="搜索"
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
        </header>
        <div className="pc-filter-row">
          <button className="pc-primary-chip" type="button" onClick={() => nav('/feedback')}>
            ＋ 请人反馈
          </button>
          <button
            type="button"
            className={`fchip${mode === 'mine' ? ' on' : ''}`}
            aria-pressed={mode === 'mine'}
            onClick={() => setMode('mine')}
          >
            等我处理
          </button>
          <button
            type="button"
            className={`fchip${mode === 'guest' ? ' on' : ''}`}
            aria-pressed={mode === 'guest'}
            onClick={() => setMode('guest')}
          >
            访客新回复
          </button>
        </div>
        <div className="pc-list-scroll">
          {error && <div className="ecard">{error}</div>}
          {!error && shown.length === 0 && data && <div className="ecard">收件箱是空的。</div>}
          {shown.map((it) => (
            <TicketCard
              key={it.number}
              item={asCardItem(it)}
              desktop
              me={me}
              selected={selected === it.number}
              href={`/t/${it.number}?list=inbox`}
            />
          ))}
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="bpage">
        <div className="ecard">{error}</div>
      </div>
    )
  }
  if (!data) {
    return (
      <div className="bpage">
        <div className="ecard">加载中…</div>
      </div>
    )
  }

  return (
    <div className="bpage">
      <div className="mcard fr-card">
        <div className="fr-title">有人要提反馈？</div>
        <div className="hint">写个标题和对方是谁，生成链接发给他，细节让他按引导补全。</div>
        <button
          className="btn big"
          onClick={() => {
            nav('/feedback')
          }}
        >
          ＋ 请人反馈
        </button>
      </div>
      <div className="b-note">
        「现在轮到我」的开启工单，注明原因；名下所有成员 Agent 共用这一份。
      </div>
      {data.items.length === 0 ? (
        <div className="ecard">收件箱是空的。</div>
      ) : (
        data.items.map((it) => (
          <a
            key={it.number}
            className="tcard"
            href={`/t/${it.number}`}
            onClick={(e) => {
              e.preventDefault()
              nav(`/t/${it.number}?list=inbox`)
            }}
          >
            <span className={`strip ${reasonClass(it.reason)}`} aria-hidden="true" />
            <div className="tc-top">
              <span className="badge reason">{it.reason}</span>
              <span className="hint">{it.role === 'assignee' ? '我是指派人' : '我是发起人'}</span>
            </div>
            <div className="tc-title">{it.title}</div>
            <div className="tc-meta">{it.number}</div>
          </a>
        ))
      )}
    </div>
  )
}
