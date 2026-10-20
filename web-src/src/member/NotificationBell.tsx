import { useCallback, useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react'
import { createPortal } from 'react-dom'
import {
  fetchNotifications,
  fetchNotificationsUnread,
  markNotificationsRead,
  type NotificationItem,
  type NotificationsResponse,
} from './memberApi'
import { nav } from './ui'
import type { LayoutTier } from './useLayoutTier'

const storageKey = 'tb:browser-notifications'
const pageSize = 30

const reasonText: Record<string, string> = {
  assigned: '新指派',
  delivered: '已交付，待你验收',
  assignee_replied: '指派人回复了',
  returned: '退回修改',
  creator_replied: '创建者回复了',
  agent_replied: '你的 Agent 回复了',
  guest_replied: '访客回复了',
  guest_completed: '访客补全了说明',
  guest_revised: '访客修订了说明',
  department: '轮到本部门',
  closed: '工单被关闭',
  reopened: '工单重新开启',
  unassigned: '取消指派',
  brief_revised: '说明被修订',
  third_party_reply: '其他人回复了',
  collaborator_added: '加入协作',
  collaborator_removed: '移出协作',
  collaborator_replied: '协作者回复了',
  backup_failing: '备份失败',
  backup_recovered: '备份已恢复',
}

function badgeText(n: number) {
  return n > 99 ? '99+' : String(n)
}

function actorText(n: NotificationItem) {
  const actor = n.event.actor
  if (actor.kind === 'guest') return `访客 · ${actor.name}`
  if (actor.kind === 'agent') return `${actor.name} 🤖`
  return actor.name
}

function relativeTime(iso: string) {
  const then = new Date(iso).getTime()
  if (!Number.isFinite(then)) return ''
  const diff = Math.max(0, Date.now() - then)
  const minute = 60_000
  if (diff < minute) return '刚刚'
  if (diff < 60 * minute) return `${Math.floor(diff / minute)} 分钟前`
  if (diff < 24 * 60 * minute) return `${Math.floor(diff / (60 * minute))} 小时前`
  if (diff < 7 * 24 * 60 * minute) return `${Math.floor(diff / (24 * 60 * minute))} 天前`
  return new Date(iso).toLocaleDateString('zh-CN')
}

export function NotificationBell({ tier, onNavigate }: { tier: LayoutTier; onNavigate: () => void }) {
  const [open, setOpen] = useState(false)
  const [filter, setFilter] = useState<'all' | 'unread'>('all')
  const [items, setItems] = useState<NotificationItem[]>([])
  const [next, setNext] = useState<string | null>(null)
  const [unread, setUnread] = useState(0)
  const [loading, setLoading] = useState(false)
  const [reminders, setReminders] = useState(
    () => localStorage.getItem(storageKey) === '1' && 'Notification' in window,
  )
  const [permissionHint, setPermissionHint] = useState('')
  const latestID = useRef(0)
  const openRef = useRef(false)
  const clientOnlineRef = useRef(false)
  const onNavigateRef = useRef(onNavigate)
  const remindersRef = useRef(reminders)
  openRef.current = open
  onNavigateRef.current = onNavigate
  remindersRef.current = reminders

  const refreshUnread = useCallback(async (): Promise<boolean> => {
    try {
      const out = await fetchNotificationsUnread()
      setUnread(out.unread_count)
      clientOnlineRef.current = out.client_online
      if (out.latest_id > latestID.current) latestID.current = out.latest_id
      return out.client_online
    } catch {}
    return clientOnlineRef.current
  }, [])

  useEffect(() => {
    void refreshUnread()
  }, [refreshUnread])

  const pageState = useRef({ filter, items })
  pageState.current = { filter, items }
  const loadPage = useCallback(async (mode: 'replace' | 'more') => {
    setLoading(true)
    try {
      const current = pageState.current
      const before = mode === 'more' && current.items.length ? current.items[current.items.length - 1].id : undefined
      const out: NotificationsResponse = await fetchNotifications({
        unread: current.filter === 'unread',
        before,
        limit: pageSize,
      })
      setItems((old) => (mode === 'more' ? [...old, ...out.items] : out.items))
      setNext(out.next)
    } catch {
      if (mode === 'replace') setItems([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (open) void loadPage('replace')
    if (open) void refreshUnread()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, filter, refreshUnread])

  useEffect(() => {
    const source = new EventSource('/api/v1/notifications/stream')
    const sourceRef = source
    sourceRef.addEventListener('open', () => void refreshUnread())
    sourceRef.addEventListener('hello', (ev) => {
      const data = JSON.parse((ev as MessageEvent).data)
      setUnread(data.unread_count)
      if (data.latest_id > latestID.current) latestID.current = data.latest_id
    })
    sourceRef.addEventListener('notification', (ev) => {
      const data = JSON.parse((ev as MessageEvent).data) as NotificationItem
      setUnread((n) => n + 1)
      if (data.id > latestID.current) latestID.current = data.id
      if (openRef.current) void loadPage('replace')
      if (
        remindersRef.current &&
        document.hidden &&
        typeof Notification !== 'undefined' &&
        Notification.permission === 'granted'
      ) {
        void refreshUnread().then((clientOnline) => {
          if (clientOnline) return
          try {
            const title = data.ticket ? `${data.ticket.number} ${data.ticket.title}` : data.text
            const notice = new Notification(title, { body: data.text, tag: `tb-notification-${data.id}` })
            notice.onclick = () => {
              window.focus()
              nav(data.url)
              onNavigateRef.current()
            }
          } catch {}
        })
      }
    })
    sourceRef.addEventListener('read', (ev) => {
      const data = JSON.parse((ev as MessageEvent).data)
      setUnread(data.unread_count)
      if (openRef.current) void loadPage('replace')
    })
    sourceRef.addEventListener('error', () => void refreshUnread())
    return () => sourceRef.close()
    // The stream must outlive prop/state changes; refs carry fresh values into the listeners.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open])

  const markAll = async () => {
    if (!unread) return
    const out = await markNotificationsRead({ up_to: latestID.current || 0 })
    setUnread(out.unread_count)
    setItems((old) => old.map((n) => ({ ...n, read_at: n.read_at ?? new Date().toISOString() })))
  }

  const openItem = async (n: NotificationItem) => {
    if (n.ticket) await markNotificationsRead({ ticket: n.ticket.number })
    else await markNotificationsRead({ ids: [n.id] })
    setItems((old) => old.map((x) => (x.id === n.id ? { ...x, read_at: x.read_at ?? new Date().toISOString() } : x)))
    setOpen(false)
    nav(n.ticket ? n.ticket.url : n.url)
    onNavigate()
  }

  const toggleReminders = async () => {
    if (!reminders) {
      if (typeof Notification === 'undefined') {
        setPermissionHint('此浏览器不支持系统提醒')
        return
      }
      const permission = await Notification.requestPermission()
      if (permission !== 'granted') {
        setPermissionHint(permission === 'denied' ? '浏览器已拒绝通知权限，请在站点设置里开启' : '没有授权，无法弹出提醒')
        return
      }
      localStorage.setItem(storageKey, '1')
      setReminders(true)
      setPermissionHint('')
      return
    }
    localStorage.removeItem(storageKey)
    setReminders(false)
    setPermissionHint('')
  }

  const row = (n: NotificationItem) => (
    <button
      key={n.id}
      className={`nrow${tier === 'mobile' ? ' mrow' : ''}${!n.read_at ? ' unread' : ''}${n.ticket ? '' : ' system'}`}
      onClick={() => void openItem(n)}
    >
      <span className="ndot" />
      <span>
        <div>
          <span className={`nkind${n.kind === 'turn' ? ' action' : ''}`}>
            {n.ticket ? null : '🛡 '}
            {n.kind === 'turn' ? '轮到你' : '知会'}
          </span>
          <span className="nreason">{reasonText[n.reason] ?? n.reason}</span>
        </div>
        <div className="ntitle">{n.ticket ? `${n.ticket.number} ${n.ticket.title}` : `🛡 ${n.text}`}</div>
        <div className="nmeta">
          <span>{n.ticket ? actorText(n) : '工单板'}</span>
          <span>{relativeTime(n.created_at)}</span>
        </div>
      </span>
    </button>
  )

  const listBody = useMemo(
    () => (
      <>
        {items.length ? items.map(row) : <div className="empty">{filter === 'unread' ? '没有未读通知' : '暂无通知'}</div>}
        {next && (
          <button className="loadmore" onClick={() => void loadPage('more')} disabled={loading}>
            {loading ? '加载中…' : '加载更多'}
          </button>
        )}
      </>
    ),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [items, filter, tier, next, loading, loadPage],
  )

  const controls = (
    <div className="notice-controls">
      <div className="seg" role="tablist" aria-label="通知范围">
        {(['all', 'unread'] as const).map((key) => (
          <button key={key} role="tab" aria-selected={filter === key} className={filter === key ? 'on' : ''} onClick={() => setFilter(key)}>
            {key === 'all' ? '全部' : '未读'}
          </button>
        ))}
      </div>
      <p className="scope-note">收件箱 = 轮到你处理；通知 = 发生了什么。</p>
    </div>
  )
  const footer = (
    <footer className="notice-foot">
      <div className="switchrow">
        <label htmlFor="notify-switch">在此浏览器弹出提醒</label>
        <button
          id="notify-switch"
          className={`switch${reminders ? ' on' : ''}`}
          role="switch"
          aria-checked={reminders}
          onClick={() => void toggleReminders()}
        >
          <i />
        </button>
      </div>
      <p className="switchsub">桌面客户端在线时不会重复弹{permissionHint ? ` · ${permissionHint}` : ''}</p>
    </footer>
  )
  const bell = (
    <button className={`bellbtn${tier === 'mobile' ? '' : ' floating-bell'}`} aria-label="通知" onClick={() => setOpen(true)}>
      🔔
      {!!unread && <b>{badgeText(unread)}</b>}
    </button>
  )

  if (tier === 'mobile') {
    const dragStart = { y: 0 }
    const dragProps = {
      onPointerDown: (e: ReactPointerEvent<HTMLDivElement>) => {
        dragStart.y = e.clientY
        try {
          e.currentTarget.setPointerCapture(e.pointerId)
        } catch {}
      },
      onPointerMove: (e: ReactPointerEvent<HTMLDivElement>) => {
        e.currentTarget.style.transform = `translateY(${Math.max(0, e.clientY - dragStart.y)}px)`
      },
      onPointerUp: (e: ReactPointerEvent<HTMLDivElement>) => {
        const offset = e.clientY - dragStart.y
        e.currentTarget.style.transform = ''
        if (offset > 80) setOpen(false)
      },
    }
    return (
      <>
        {bell}
        {createPortal(
          <>
            {open && <div className="notice-scrim open" onClick={() => setOpen(false)} />}
            {open && (
              <section className="notice-sheet open" aria-label="通知">
                <header className="notice-head mobile">
                  <div className="notice-title-row">
                    <h2>通知</h2>
                    <button className="markread" onClick={() => void markAll()} disabled={!unread}>
                      全部标为已读
                    </button>
                  </div>
                  {controls}
                </header>
                <div className="sheet-handle" aria-hidden="true" {...dragProps} />
                <div className="notice-scroll">{listBody}</div>
                {footer}
              </section>
            )}
          </>,
          document.body,
        )}
      </>
    )
  }

  return (
    <>
      {bell}
      {createPortal(
        <>
          {open && <div className="notice-scrim open" onClick={() => setOpen(false)} />}
          <section className={`noticepanel${open ? ' open' : ''}`} aria-label="通知">
            <header className="notice-head">
              <div className="notice-title-row">
                <h2>通知</h2>
                <button className="markread" onClick={() => void markAll()} disabled={!unread}>
                  全部标为已读
                </button>
                <button className="panel-close" aria-label="关闭通知" onClick={() => setOpen(false)}>
                  ×
                </button>
              </div>
              {controls}
            </header>
            <div className="notice-scroll">{listBody}</div>
            {footer}
          </section>
        </>,
        document.body,
      )}
    </>
  )
}
