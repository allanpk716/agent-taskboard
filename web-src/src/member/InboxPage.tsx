// 收件箱（#41）：「现在轮到我」的开启工单，卡片顶部是醒目的原因色条与文案，
// 一眼知道轮到我做什么；轮到本部门的未指派工单只进入有效初筛人的收件箱。
// #274 起分成「轮到我 ｜ 协作中」两个分页：协作中只列本人作为协作者（抄送）
// 的开启工单，未读在前；侧栏/底栏角标仍只算「轮到我」。
import { useCallback, useEffect, useState } from 'react'
import { api } from '../api'
import { fetchInbox, fetchWaitingOnCounterparts, type InboxItem, type InboxResponse } from './memberApi'
import { nav, useInvalidate, useLocationSearch } from './ui'
import { TicketCard, ticketLabelFilterPath, type TicketListItem } from './TicketsPage'
import { parsePasteLink } from './pasteLink'
import {
  inboxActivityTime,
  inboxPhrase,
  inboxReasonClass,
  waitingOnCounterpartPhrase,
  type InboxCounterpart,
} from './inboxText'

function inboxDetailPath(number: string, search: string): string {
  const params = new URLSearchParams(search)
  params.set('list', 'inbox')
  return `/t/${number}?${params.toString()}`
}

type InboxMode = 'mine' | 'guest' | 'collab'
type InboxView = 'time' | 'person'
const inboxViewKey = 'tb:inbox-view'

function readInboxView(): InboxView {
  try {
    return localStorage.getItem(inboxViewKey) === 'person' ? 'person' : 'time'
  } catch {
    return 'time'
  }
}

interface CollaboratingResponse {
  total: number
  items: TicketListItem[]
}

/** 协作中一行里的等待方短句（不带「等」字，供列表卡片复用现有文案）。 */
function collaboratingTurnText(w: TicketListItem['waiting_on']): string {
  switch (w.party) {
    case 'assignee':
      return `${w.member ?? w.text} 处理`
    case 'creator':
      return `${w.member ?? w.text} 验收`
    case 'guest':
      return '访客'
    case 'department':
      return '本部门初筛'
    default:
      return w.text
  }
}

/** 协作中一行里的等待方：按当前推导写成「等 A 处理」「等 张三 验收」「等访客」。 */
function collaboratingWaitingText(w: TicketListItem['waiting_on']): string {
  switch (w.party) {
    case 'assignee':
      return `等 ${w.member ?? w.text} 处理`
    case 'creator':
      return `等 ${w.member ?? w.text} 验收`
    case 'guest':
      return '等访客'
    case 'department':
      return '等本部门初筛'
    default:
      return `等${w.text}`
  }
}

/** 未读在前，其余按最近更新时间倒序（与协作中分页的排序约定一致）。 */
function sortCollaborating(items: TicketListItem[]): TicketListItem[] {
  return [...items].sort((a, b) => {
    if (a.unread !== b.unread) return a.unread ? -1 : 1
    return Date.parse(b.updated_at) - Date.parse(a.updated_at)
  })
}

function inboxSearch(item: InboxItem, q: string): boolean {
  return !q || item.title.includes(q) || item.number.includes(q) || item.labels.some((l) => l.includes(q))
}

function counterpartKey(counterpart: InboxCounterpart): string {
  switch (counterpart.type) {
    case 'guest':
      return `guest:${counterpart.name ?? ''}`
    case 'self':
      return 'self'
    case 'pending_triage':
      return 'pending-triage'
    default:
      return `member:${counterpart.member?.id ?? counterpart.member?.name ?? counterpart.name ?? ''}`
  }
}

function counterpartName(counterpart: InboxCounterpart): string {
  switch (counterpart.type) {
    case 'guest':
      return `${counterpart.name || '访客'}（访客）`
    case 'self':
      return '我自己'
    case 'pending_triage':
      return '待初筛'
    default:
      return counterpart.member?.name ?? counterpart.name ?? '对方'
  }
}

function activityTimestamp(item: InboxItem): number {
  return Date.parse(item.last_reply_at ?? item.created_at)
}

function groupInbox(items: InboxItem[], waiting: InboxItem[]) {
  const groups = new Map<
    string,
    { key: string; name: string; avatar: string; avatarClass: string; mine: InboxItem[]; waiting: InboxItem[] }
  >()
  const groupOf = (item: InboxItem) => {
    const key = counterpartKey(item.counterpart)
    if (!groups.has(key)) {
      const name = counterpartName(item.counterpart)
      groups.set(key, {
        key,
        name,
        avatar:
          item.counterpart.type === 'pending_triage'
            ? '筛'
            : item.counterpart.type === 'self'
              ? '我'
              : name.slice(0, 1),
        avatarClass:
          item.counterpart.type === 'guest'
            ? ' visitor'
            : item.counterpart.type === 'self'
              ? ' self'
              : item.counterpart.type === 'pending_triage'
                ? ' triage'
                : '',
        mine: [],
        waiting: [],
      })
    }
    return groups.get(key)!
  }
  for (const item of items) groupOf(item).mine.push(item)
  for (const item of waiting) groupOf(item).waiting.push(item)
  return [...groups.values()]
    .filter((group) => group.mine.length > 0)
    .sort((a, b) => {
      const latest = (group: typeof a) =>
        Math.max(...group.mine.map(activityTimestamp), ...group.waiting.map(activityTimestamp))
      return latest(b) - latest(a)
    })
}

function asTicketListItem(it: InboxItem): TicketListItem {
  return {
    ...it,
    seq: 0,
    status: 'open',
    creator: it.creator,
    assignee: it.assignee?.member?.name ?? null,
    assignee_deactivated: it.assignee?.member?.deactivated,
    waiting_on: { party: '', member: it.who || null, text: it.waiting_on, reason: it.reason },
  }
}

function inboxCardInfo(it: InboxItem, me?: { name: string }, waiting = false) {
  const activity = inboxActivityTime(it)
  return {
    phrase: waiting ? waitingOnCounterpartPhrase(it) : inboxPhrase(it, me),
    time: activity.label,
    timeTitle: activity.full,
    timeIso: activity.iso,
    reasonClass: waiting ? 'new' : inboxReasonClass(it.reason_code),
    waiting,
    showDetails: !waiting,
  }
}

function InboxGroups({
  items,
  waiting,
  me,
  search,
  selected,
  desktop,
  expanded,
  onToggle,
}: {
  items: InboxItem[]
  waiting: InboxItem[]
  me?: { name: string }
  search: string
  selected?: string
  desktop?: boolean
  expanded: Set<string>
  onToggle: (key: string) => void
}) {
  const groups = groupInbox(items, waiting)
  return (
    <div className="inbox-person-view">
      {groups.map((group) => (
        <section className="person-group" key={group.key} aria-label={group.name}>
          <header className="group-head">
            <span className={`group-avatar${group.avatarClass}`} aria-hidden="true">
              {group.avatar}
            </span>
            <span className="group-name">{group.name}</span>
            <span className="group-counts">
              轮到我 {group.mine.length} · 等他 {group.waiting.length}
            </span>
          </header>
          <div className="group-body">
            {group.mine.map((it) => (
              <TicketCard
                key={it.number}
                item={asTicketListItem(it)}
                desktop={desktop}
                me={me}
                selected={selected === it.number}
                href={inboxDetailPath(it.number, search)}
                inbox={inboxCardInfo(it, me)}
                onLabelClick={(label) => nav(ticketLabelFilterPath(label))}
              />
            ))}
          </div>
          {group.waiting.length > 0 && (
            <>
              <button
                type="button"
                className="wait-toggle"
                aria-expanded={expanded.has(group.key)}
                aria-controls={`${group.key}-waiting`}
                onClick={() => onToggle(group.key)}
              >
                <span>在等他 {group.waiting.length} 张</span>
                <span className="arrow" aria-hidden="true">
                  ▸
                </span>
              </button>
              <div className="waiting-list" id={`${group.key}-waiting`} hidden={!expanded.has(group.key)}>
                {group.waiting.map((it) => (
                  <TicketCard
                    key={it.number}
                    item={asTicketListItem(it)}
                    desktop={desktop}
                    href={inboxDetailPath(it.number, search)}
                    inbox={inboxCardInfo(it, me, true)}
                  />
                ))}
              </div>
            </>
          )}
        </section>
      ))}
    </div>
  )
}

export function InboxPage({
  desktop,
  selected,
  me,
}: {
  desktop?: boolean
  selected?: string
  me?: { name: string }
}) {
  const search = useLocationSearch()
  const [data, setData] = useState<InboxResponse | null>(null)
  const [waitingData, setWaitingData] = useState<InboxResponse | null>(null)
  const [collab, setCollab] = useState<CollaboratingResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [waitingError, setWaitingError] = useState<string | null>(null)
  const [collabError, setCollabError] = useState<string | null>(null)
  const [pasteOpen, setPasteOpen] = useState(false)
  const [pasteValue, setPasteValue] = useState('')
  const [pasteNotice, setPasteNotice] = useState<string | null>(null)
  const params = new URLSearchParams(search)
  const modeParam = params.get('inbox')
  const mode: InboxMode = modeParam === 'collab' ? 'collab' : modeParam === 'guest' ? 'guest' : 'mine'
  const q = params.get('q') ?? ''
  const [view, setView] = useState<InboxView>(readInboxView)
  const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set())

  const load = useCallback(async () => {
    // 两个分页各自读取：一边失败不影响另一边展示。
    void fetchInbox()
      .then((inbox) => {
        setData(inbox)
        setError(null)
      })
      .catch((e: any) => setError(e?.message ?? '读取收件箱失败'))
    void fetchWaitingOnCounterparts()
      .then((body) => {
        setWaitingData(body)
        setWaitingError(null)
      })
      .catch((e: any) => setWaitingError(e?.message ?? '读取在等他失败'))
    // 复用列表接口的 collaborator=me + status=open；limit 取上限，翻页场景
    // （超过 200 张协作工单）按更新时间倒序取最前面的即可。
    void api('/api/v1/tickets?collaborator=me&status=open&sort=-updated&limit=200')
      .then((body) => {
        setCollab(body as CollaboratingResponse)
        setCollabError(null)
      })
      .catch((e: any) => setCollabError(e?.message ?? '读取协作中失败'))
  }, [])

  useEffect(() => {
    void load()
  }, [load])
  useInvalidate(load)

  useEffect(() => {
    try {
      localStorage.setItem(inboxViewKey, view)
    } catch {}
  }, [view])

  useEffect(() => {
    const head = document.querySelector('.b-top')
    if (!head) return
    const syncOffset = () => {
      document.documentElement.style.setProperty(
        '--inbox-sticky-top',
        `${Math.max(0, head.getBoundingClientRect().bottom)}px`,
      )
    }
    syncOffset()
    window.addEventListener('resize', syncOffset)
    return () => window.removeEventListener('resize', syncOffset)
  }, [mode, view])

  function updateInbox(patch: Record<string, string>) {
    const params = new URLSearchParams(search)
    for (const [key, value] of Object.entries(patch)) params.set(key, value)
    nav(`/?${params.toString()}`)
  }

  const collaborating = sortCollaborating(collab?.items ?? [])
  const collaboratingUnread = collaborating.filter((it) => it.unread).length
  const collaboratingLabel = collaboratingUnread > 0 ? `协作中 · ${collaboratingUnread}` : '协作中'
  const mobileInboxItems = data?.items ?? []

  function toggleWaitingGroup(key: string) {
    setExpandedGroups((current) => {
      const next = new Set(current)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  function recognizePastedText(text: string) {
    const result = parsePasteLink(text)
    if (result.kind === 'path') {
      setPasteOpen(false)
      setPasteNotice(null)
      nav(result.path)
      return
    }
    setPasteOpen(true)
    setPasteNotice(
      result.kind === 'guest-link'
        ? '这是访客链接，成员打开会把它认领掉'
        : '剪贴板里没有找到工单链接',
    )
  }

  // iOS 只把「粘贴」弹窗记在点击时的用户手势上；这里必须先发起 readText，再做其他异步工作。
  function pasteFromClipboard() {
    if (typeof navigator.clipboard?.readText !== 'function') {
      setPasteOpen(true)
      setPasteNotice('浏览器不允许读取剪贴板，请长按粘贴到下面')
      return
    }
    try {
      navigator.clipboard.readText().then(recognizePastedText, () => {
        setPasteOpen(true)
        setPasteNotice('没能读取剪贴板，请长按粘贴到下面')
      })
    } catch {
      setPasteOpen(true)
      setPasteNotice('没能读取剪贴板，请长按粘贴到下面')
    }
  }

  const pasteButton = (
    <button type="button" className="btn sm" onClick={pasteFromClipboard}>
      📋 粘贴链接
    </button>
  )
  const pasteFallback = pasteOpen && (
    <form
      className="mcard"
      onSubmit={(e) => {
        e.preventDefault()
        recognizePastedText(pasteValue)
      }}
    >
      <div className="hint">{pasteNotice}</div>
      <div className="inrow">
        <input
          type="text"
          aria-label="粘贴的链接"
          placeholder="粘贴工单或草稿链接"
          value={pasteValue}
          onChange={(e) => setPasteValue(e.target.value)}
        />
        <button type="submit" className="btn">
          打开
        </button>
      </div>
    </form>
  )

  if (desktop) {
    const filtered = (data?.items ?? []).filter((it) => {
      return mode === 'mine' || it.reason_code === 'guest_replied'
    })
    const shown = q
      ? filtered.filter((it) => it.title.includes(q) || it.number.includes(q) || it.labels.some((l) => l.includes(q)))
      : filtered
    const collabShown = q
      ? collaborating.filter((it) => it.title.includes(q) || it.number.includes(q) || it.labels.some((l) => l.includes(q)))
      : collaborating
    const asCardItem = (it: InboxItem) => asTicketListItem(it)
    const asInboxCard = (it: InboxItem) => inboxCardInfo(it, me)
    return (
      <div className="pc-list-pane inbox" data-testid="inbox-list-pane">
        <header className="pc-list-head">
          <div>
            <h2>收件箱</h2>
            <span>{mode === 'collab' ? (collab?.total ?? '…') : (data?.count ?? '…')} 张</span>
            <span className="sp" />
            {pasteButton}
          </div>
          <input
            placeholder="搜索"
            aria-label="搜索"
            defaultValue={q}
            onKeyDown={(e) => {
              if (e.key === 'Enter') updateInbox({ q: (e.target as HTMLInputElement).value })
            }}
            onBlur={(e) => {
              if (e.target.value !== q) updateInbox({ q: e.target.value })
            }}
          />
        </header>
        {mode === 'mine' && (
          <div className="inbox-view-switch" role="tablist" aria-label="收件箱视图">
            <button
              type="button"
              role="tab"
              aria-selected={view === 'time'}
              onClick={() => setView('time')}
            >
              按时间
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={view === 'person'}
              onClick={() => setView('person')}
            >
              按人
            </button>
          </div>
        )}
        {pasteFallback}
        <div className="pc-filter-row">
          <button
            type="button"
            className={`fchip${mode === 'mine' ? ' on' : ''}`}
            aria-pressed={mode === 'mine'}
            onClick={() => updateInbox({ inbox: 'mine' })}
          >
            等我处理
          </button>
          <button
            type="button"
            className={`fchip${mode === 'guest' ? ' on' : ''}`}
            aria-pressed={mode === 'guest'}
            onClick={() => updateInbox({ inbox: 'guest' })}
          >
            访客新回复
          </button>
          <button
            type="button"
            className={`fchip${mode === 'collab' ? ' on' : ''}`}
            aria-pressed={mode === 'collab'}
            onClick={() => updateInbox({ inbox: 'collab' })}
          >
            {collaboratingLabel}
          </button>
        </div>
        <div className={`pc-list-scroll${view === 'person' && mode === 'mine' ? ' inbox-person-scroll' : ''}`}>
          {error && <div className="ecard">{error}</div>}
          {view === 'person' && waitingError && <div className="ecard">{waitingError}</div>}
          {mode !== 'collab' ? (
            <>
              {!error && shown.length === 0 && data && <div className="ecard">收件箱是空的。</div>}
              {view === 'person' && mode === 'mine' ? (
                <InboxGroups
                  items={shown}
                  waiting={(waitingData?.items ?? []).filter((it) => inboxSearch(it, q))}
                  me={me}
                  search={search}
                  selected={selected}
                  desktop
                  expanded={expandedGroups}
                  onToggle={toggleWaitingGroup}
                />
              ) : (
                shown.map((it) => (
                  <TicketCard
                    key={it.number}
                    item={asCardItem(it)}
                    desktop
                    me={me}
                    inbox={asInboxCard(it)}
                    selected={selected === it.number}
                    href={inboxDetailPath(it.number, search)}
                    onLabelClick={(label) => nav(ticketLabelFilterPath(label))}
                  />
                ))
              )}
            </>
          ) : (
            <>
              {collabError && <div className="ecard">{collabError}</div>}
              {!collabError && collabShown.length === 0 && collab && (
                <div className="ecard">没有协作中的工单。</div>
              )}
              {collabShown.map((it) => (
                <TicketCard
                  key={it.number}
                  item={{ ...it, waiting_on: { ...it.waiting_on, text: collaboratingTurnText(it.waiting_on) } }}
                  desktop
                  selected={selected === it.number}
                  href={inboxDetailPath(it.number, search)}
                  onLabelClick={(label) => nav(ticketLabelFilterPath(label))}
                />
              ))}
            </>
          )}
        </div>
      </div>
    )
  }

  if (mode === 'collab' ? collabError || !collab : error || !data) {
    const text = mode === 'collab' ? collabError ?? '加载中…' : error ?? '加载中…'
    return (
      <div className="bpage">
        <div className="ecard">{text}</div>
      </div>
    )
  }

  return (
    <div className="bpage">
      <div className="frow">
        <span className="sp" />
        {pasteButton}
      </div>
      {pasteFallback}
      <div className="mcard fr-card">
        <div className="fr-title">要建一张工单？</div>
        <div className="hint">派任务、提需求、问问题，或者请部门外的人来填，都从这里开始。</div>
        <button
          className="btn big"
          onClick={() => {
            nav('/new')
          }}
        >
          ＋ 建单
        </button>
      </div>
      <div className="frow inbox-tabs" role="tablist" aria-label="收件箱分页">
        <button
          type="button"
          role="tab"
          className={`fchip${mode === 'mine' ? ' on' : ''}`}
          aria-selected={mode === 'mine'}
          onClick={() => updateInbox({ inbox: 'mine' })}
        >
          轮到我
        </button>
        <button
          type="button"
          role="tab"
          className={`fchip${mode === 'collab' ? ' on' : ''}`}
          aria-selected={mode === 'collab'}
          onClick={() => updateInbox({ inbox: 'collab' })}
        >
          {collaboratingLabel}
        </button>
      </div>
      {mode === 'mine' && (
        <div className="inbox-view-switch mobile" role="tablist" aria-label="收件箱视图">
          <button
            type="button"
            role="tab"
            aria-selected={view === 'time'}
            onClick={() => setView('time')}
          >
            按时间
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={view === 'person'}
            onClick={() => setView('person')}
          >
            按人
          </button>
        </div>
      )}
      {mode === 'collab' ? (
        <>
          <div className="b-note">你作为协作者（抄送）跟进的开启工单。</div>
          {collabError && <div className="ecard">{collabError}</div>}
          {!collabError && collaborating.length === 0 && collab && (
            <div className="ecard">没有协作中的工单。</div>
          )}
          {collaborating.map((it) => (
            <a
              key={it.number}
              className="tcard"
              href={`/t/${it.number}`}
              onClick={(e) => {
                e.preventDefault()
                nav(`/t/${it.number}?list=inbox`)
              }}
            >
              <div className="tc-top">
                <span className="badge kind">{it.kind === 'task' ? '任务' : '反馈'}</span>
                <span className="hint">{collaboratingWaitingText(it.waiting_on)}</span>
                <span className="hint" style={{ marginLeft: 'auto' }}>
                  协作中
                </span>
              </div>
              <div className={`tc-title${it.unread ? ' unread' : ''}`}>
                {it.unread && <span className="unread-dot" aria-hidden="true" />}
                {it.title}
              </div>
              <div className="tc-meta">{it.number}</div>
            </a>
          ))}
        </>
      ) : (
        <>
          <div className="b-note">现在轮到你处理的工单。</div>
          {view === 'person' && waitingError && <div className="ecard">{waitingError}</div>}
          {mobileInboxItems.length === 0 ? (
            <div className="ecard">收件箱是空的。</div>
          ) : (
            view === 'person' && mode === 'mine' ? (
              <InboxGroups
                items={mobileInboxItems}
                waiting={waitingData?.items ?? []}
                me={me}
                search={search}
                expanded={expandedGroups}
                onToggle={toggleWaitingGroup}
              />
            ) : (
              mobileInboxItems.map((it) => (
                <TicketCard
                  key={it.number}
                  item={asTicketListItem(it)}
                  me={me}
                  href={inboxDetailPath(it.number, search)}
                  inbox={inboxCardInfo(it, me)}
                  onLabelClick={(label) => nav(ticketLabelFilterPath(label))}
                />
              ))
            )
          )}
        </>
      )}
    </div>
  )
}
