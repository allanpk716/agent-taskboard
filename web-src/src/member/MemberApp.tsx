// 成员网页（#41）：移动优先卡片 + 底部标签栏（拍板的 mock 方向 B）。
// 顶栏一栏标题；底部五个标签（收件箱 / 工单 / 大号「＋」建单 / 工具 / 我的）。
// 工具里是标签字典与别名、模板维护（#42）；我的里是个人设置（Agent token）
// 与管理页入口（仅管理员，#42）；收件箱顶部是两屏建单流程入口（#195）。
import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../api'
import { fetchDrafts, fetchInbox, fetchServerVersion, fetchSession, type Me } from './memberApi'
import { InboxPage } from './InboxPage'
import { DraftsPage } from './DraftsPage'
import { TicketsPage } from './TicketsPage'
import { TicketDetailPage } from './TicketDetailPage'
import { NewTicketPage } from './NewTicketPage'
import { CreateDraftPage } from './CreateDraftPage'
import { ToolsPage } from './ToolsPage'
import { TemplatesPage } from './TemplatesPage'
import { TemplateEditPage } from './TemplateEditPage'
import { MePage } from './MePage'
import { AdminPage } from './AdminPage'
import { NotificationBell } from './NotificationBell'
import { detailListSource } from './listFilters'
import { nav, useLocationSearch, useNavSeq, useRoute, type Route } from './ui'
import { useLayoutTier } from './useLayoutTier'

function pageTitle(route: Route, newTicketTitle: string): string {
  switch (route.page) {
    case 'inbox':
      return '收件箱'
    case 'tickets':
      return '工单'
    case 'drafts':
      return '我的草稿'
    case 'new':
      return newTicketTitle
    case 'feedback':
      return newTicketTitle
    case 'detail':
      return '工单'
    case 'draft':
      return '建单草稿'
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
  const search = useLocationSearch()
  const navSeq = useNavSeq()
  const tier = useLayoutTier()
  const [me, setMe] = useState<Me | null>(null)
  const [denied, setDenied] = useState(false)
  const [inboxCount, setInboxCount] = useState(0)
  const [draftCount, setDraftCount] = useState(0)
  const [serverVersion, setServerVersion] = useState<string | undefined>()
  const [newTicketTitle, setNewTicketTitle] = useState('建单')

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

  useEffect(() => {
    fetchServerVersion().then(setServerVersion).catch(() => {})
  }, [])

  const refreshInbox = useCallback(() => {
    fetchInbox()
      .then((d) => setInboxCount(d.count))
      .catch(() => {})
  }, [])
  const refreshDrafts = useCallback(() => {
    fetchDrafts().then((d) => setDraftCount(d.count)).catch(() => {})
  }, [])
  useEffect(() => {
    refreshInbox()
    refreshDrafts()
    const invalidate = () => {
      refreshInbox()
      refreshDrafts()
    }
    window.addEventListener('tb:invalidate', invalidate)
    return () => window.removeEventListener('tb:invalidate', invalidate)
  }, [refreshInbox, refreshDrafts, route])

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
  const centered =
    ['tools', 'me', 'admin', 'new', 'templates', 'templateEdit', 'feedback', 'draft'].includes(h.page)
  const { fromInbox, query: detailQuery } = detailListSource(search)
  const detailReturnTo = fromInbox
    ? `/${detailQuery ? '?' + detailQuery : ''}`
    : `/tickets${detailQuery ? '?' + detailQuery : ''}`
  const bell = <NotificationBell tier={tier} onNavigate={() => window.dispatchEvent(new Event('tb:invalidate'))} />
  const page = (
    <>
      {route.page === 'inbox' && <InboxPage key={route.page} />}
      {route.page === 'tickets' && <TicketsPage key={route.page} me={me} />}
      {route.page === 'drafts' && <DraftsPage key={navSeq} />}
      {(route.page === 'new' || route.page === 'feedback') && (
        <NewTicketPage
          key={navSeq}
          me={me}
          initialFiller={route.page === 'feedback' ? 'guest' : 'self'}
          onPhaseTitleChange={setNewTicketTitle}
        />
      )}
      {route.page === 'detail' && (
        <TicketDetailPage key={navSeq} ticketRef={route.ref} me={me} returnTo={detailReturnTo} />
      )}
      {route.page === 'draft' && <CreateDraftPage key={navSeq} id={route.id} />}
      {route.page === 'tools' && <ToolsPage key={navSeq} />}
      {route.page === 'me' && <MePage key={navSeq} me={me} serverVersion={serverVersion} />}
      {route.page === 'templates' && <TemplatesPage key={navSeq} />}
      {route.page === 'templateEdit' && <TemplateEditPage key={navSeq} templateKey={route.key} />}
      {route.page === 'admin' && <AdminPage key={navSeq} me={me} />}
    </>
  )
  const tab = (
    k: Route['page'],
    icon: string,
    name: string,
    badge?: number,
    on?: boolean,
    badgeTone?: 'red' | 'amber',
  ) => (
    <a
      className={`tabbtn${(on ?? h.page === k) ? ' on' : ''}${badgeTone ? ` ${badgeTone}-badge` : ''}`}
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

  if (tier !== 'mobile') {
    const compact = tier !== 'wide'
    const item = (
      href: string,
      icon: string,
      full: string,
      short: string,
      on: boolean,
      badge?: number,
      primary?: boolean,
      badgeTone?: 'red' | 'amber',
    ) => (
      <a
        className={`pc-nav-item${primary ? ' primary' : ''}${on ? ' on' : ''}${badgeTone ? ` ${badgeTone}-badge` : ''}`}
        href={href}
        aria-current={on ? 'page' : undefined}
        onClick={(e) => {
          e.preventDefault()
          nav(href)
        }}
      >
        <span aria-hidden="true">{icon}</span>
        <span>{compact ? short : full}</span>
        {!!badge && <b>{badge}</b>}
      </a>
    )
    const sidebar = (
      <nav className="pc-sidebar" aria-label="侧栏">
        <a className="pc-wordmark" href="/">
          工单板
        </a>
        {item('/new', '✚', '＋ 建单', '建单', h.page === 'new' || h.page === 'feedback', undefined, true)}
        {item('/', '📥', '收件箱', '收件箱', h.page === 'inbox' || (h.page === 'detail' && fromInbox), inboxCount)}
        {item('/drafts', '✎', '✎ 草稿', '草稿', h.page === 'drafts', draftCount, false, 'amber')}
        {item('/tickets', '🗂', '工单', '工单', h.page === 'tickets' || (h.page === 'detail' && !fromInbox))}
        {item('/tools', '🧰', '工具', '工具', toolsOn)}
        {item('/me', '👤', '我的', '我的', meOn)}
        {me.is_admin && item('/admin', '🛡', '管理', '管理', h.page === 'admin')}
        <div className="pc-side-foot">
          {!compact && (
            <div>
              {me.name} · {me.is_admin ? '管理员' : '成员'}
            </div>
          )}
          {serverVersion && <small>工单板 v{serverVersion}</small>}
        </div>
      </nav>
    )
    const listish = h.page === 'tickets' || h.page === 'inbox' || h.page === 'detail'
    if (listish && (tier === 'wide' || tier === 'medium')) {
      return (
        <>
          {bell}
          <div className={`pc-shell${compact ? ' compact' : ''}`}>
            {sidebar}
            <div className="pc-split">
              <section className="pc-list-slot" aria-label="工单列表">
                {h.page === 'inbox' || fromInbox ? (
                  <InboxPage desktop selected={route.page === 'detail' ? route.ref : undefined} me={me} />
                ) : (
                  <TicketsPage desktop selected={route.page === 'detail' ? route.ref : undefined} me={me} />
                )}
              </section>
              <main className="pc-detail">
                {route.page === 'detail' && (
                  <TicketDetailPage ticketRef={route.ref} me={me} returnTo={detailReturnTo} />
                )}
              </main>
            </div>
          </div>
        </>
      )
    }
    return (
      <>
        {bell}
        <div className={`pc-shell${compact ? ' compact' : ''}`}>
          {sidebar}
          <main className="pc-main">
            <div className={centered ? 'pc-centered' : undefined}>
              {h.page === 'tickets' ? (
                <TicketsPage desktop me={me} />
              ) : h.page === 'inbox' ? (
                <InboxPage desktop me={me} />
              ) : (
                page
              )}
            </div>
          </main>
        </div>
      </>
    )
  }

  return (
    <main className="member-root">
      <div className="b-app">
        <header className="b-top">
          <h1 className="b-title">{pageTitle(route, newTicketTitle)}</h1>
          {bell}
          <div className="b-me">
            {me.name}
            <i>{me.is_admin ? '管理员' : '成员'}</i>
          </div>
        </header>
        <div className="b-body">{page}</div>
        <nav className="b-tabs" aria-label="主导航">
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
          {tab('me', '👤', '我的', draftCount, meOn, 'amber')}
        </nav>
      </div>
    </main>
  )
}
