// 成员网页（#41）：移动优先卡片 + 底部标签栏（拍板的 mock 方向 B）。
// 顶栏一栏标题；底部五个标签（收件箱 / 工单 / 大号「＋」建单 / 工具 / 我的）。
// 工具里是标签字典与别名、模板维护（#42）；我的里是个人设置（Agent token）
// 与管理页入口（仅管理员，#42）。
import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../api'
import { fetchInbox, fetchSession, type Me } from './memberApi'
import { InboxPage } from './InboxPage'
import { TicketsPage } from './TicketsPage'
import { TicketDetailPage } from './TicketDetailPage'
import { NewTicketPage } from './NewTicketPage'
import { ToolsPage } from './ToolsPage'
import { TemplatesPage } from './TemplatesPage'
import { TemplateEditPage } from './TemplateEditPage'
import { MePage } from './MePage'
import { AdminPage } from './AdminPage'
import { nav, useNavSeq, useRoute, type Route } from './ui'

function pageTitle(route: Route): string {
  switch (route.page) {
    case 'inbox':
      return '收件箱'
    case 'tickets':
      return '工单'
    case 'new':
      return '建单'
    case 'detail':
      return '工单'
    case 'tools':
      return '工具'
    case 'me':
      return '我的 · 设置'
    case 'templates':
      return '模板维护'
    case 'templateEdit':
      return '模板'
    case 'admin':
      return '管理'
  }
}

export function MemberApp() {
  const route = useRoute()
  const navSeq = useNavSeq()
  const [me, setMe] = useState<Me | null>(null)
  const [denied, setDenied] = useState(false)
  const [inboxCount, setInboxCount] = useState(0)

  useEffect(() => {
    fetchSession()
      .then((s) => setMe(s.viewer.member))
      .catch((err) => {
        if (err instanceof ApiError && err.status === 401) {
          window.location.replace('/login')
          return
        }
        setDenied(true)
      })
  }, [])

  const refreshInbox = useCallback(() => {
    fetchInbox()
      .then((d) => setInboxCount(d.count))
      .catch(() => {})
  }, [])
  useEffect(() => {
    refreshInbox()
    window.addEventListener('tb:invalidate', refreshInbox)
    return () => window.removeEventListener('tb:invalidate', refreshInbox)
  }, [refreshInbox, route])

  if (denied) {
    return (
      <main className="member-root">
        <div className="bpage">
          <div className="ecard">无法连接服务，请刷新重试。</div>
        </div>
      </main>
    )
  }
  if (!me) {
    return (
      <main className="member-root">
        <div className="bpage">
          <div className="ecard">加载中…</div>
        </div>
      </main>
    )
  }

  const h = route
  const toolsOn = h.page === 'tools' || h.page === 'templates' || h.page === 'templateEdit'
  const meOn = h.page === 'me' || h.page === 'admin'
  const tab = (k: Route['page'], icon: string, name: string, badge?: number, on?: boolean) => (
    <a
      className={`tabbtn${(on ?? h.page === k) ? ' on' : ''}`}
      href={k === 'inbox' ? '/' : `/${k}`}
      onClick={(e) => {
        e.preventDefault()
        nav(k === 'inbox' ? '/' : `/${k}`)
      }}
    >
      {icon}
      {!!badge && <b className="dot">{badge}</b>}
      <span>{name}</span>
    </a>
  )

  return (
    <main className="member-root">
      <div className="b-app">
        <header className="b-top">
          <h1 className="b-title">{pageTitle(route)}</h1>
          <div className="b-me">
            {me.name}
            <i>{me.is_admin ? '管理员' : '成员'}</i>
          </div>
        </header>
        <div className="b-body">
          {route.page === 'inbox' && <InboxPage key={navSeq} />}
          {route.page === 'tickets' && <TicketsPage key={navSeq} />}
          {route.page === 'new' && <NewTicketPage key={navSeq} />}
          {route.page === 'detail' && <TicketDetailPage key={navSeq} ticketRef={route.ref} />}
          {route.page === 'tools' && <ToolsPage key={navSeq} />}
          {route.page === 'me' && <MePage key={navSeq} me={me} />}
          {route.page === 'templates' && <TemplatesPage key={navSeq} />}
          {route.page === 'templateEdit' && <TemplateEditPage key={navSeq} templateKey={route.key} />}
          {route.page === 'admin' && <AdminPage key={navSeq} me={me} />}
        </div>
        <nav className="b-tabs">
          {tab('inbox', '📥', '收件箱', inboxCount)}
          {tab('tickets', '🗂', '工单')}
          <a
            className={`tabbtn plus${h.page === 'new' ? ' on' : ''}`}
            href="/new"
            onClick={(e) => {
              e.preventDefault()
              nav('/new')
            }}
          >
            <b>＋</b>
            <span>建单</span>
          </a>
          {tab('tools', '🧰', '工具', undefined, toolsOn)}
          {tab('me', '👤', '我的', undefined, meOn)}
        </nav>
      </div>
    </main>
  )
}
