// 成员网页的共用 UI：路由导航、toast、复制、弹窗与确认框（照 mock 的
// shared.js，React 化）。
import { useEffect, useState, type ReactNode } from 'react'

/* ---------------- 路由（pathname，SPA 回退由服务端负责） ---------------- */

export type Route =
  | { page: 'inbox' }
  | { page: 'tickets' }
  | { page: 'new' }
  | { page: 'detail'; ref: string }
  | { page: 'tools' }
  | { page: 'me' }
  | { page: 'templates' }
  | { page: 'templateEdit'; key: string }
  | { page: 'admin' }

export function parseRoute(pathname: string): Route {
  if (pathname.startsWith('/t/')) return { page: 'detail', ref: decodeURIComponent(pathname.slice(3)) }
  if (pathname.startsWith('/templates/'))
    return { page: 'templateEdit', key: decodeURIComponent(pathname.slice('/templates/'.length)) }
  if (pathname === '/templates') return { page: 'templates' }
  if (pathname === '/tickets') return { page: 'tickets' }
  if (pathname === '/new') return { page: 'new' }
  if (pathname === '/tools') return { page: 'tools' }
  if (pathname === '/me') return { page: 'me' }
  if (pathname === '/admin') return { page: 'admin' }
  return { page: 'inbox' }
}

/** SPA 内部导航：pushState + 自定义事件，让 useRoute 重新解析。 */
export function nav(to: string) {
  if (window.location.pathname === to) {
    window.dispatchEvent(new Event('tb:nav'))
    return
  }
  window.history.pushState(null, '', to)
  window.dispatchEvent(new Event('tb:nav'))
  window.scrollTo(0, 0)
}

export function useRoute(): Route {
  const [route, setRoute] = useState<Route>(() => parseRoute(window.location.pathname))
  useEffect(() => {
    const update = () => setRoute(parseRoute(window.location.pathname))
    window.addEventListener('popstate', update)
    window.addEventListener('tb:nav', update)
    return () => {
      window.removeEventListener('popstate', update)
      window.removeEventListener('tb:nav', update)
    }
  }, [])
  return route
}

/** 导护序号：每次导航（含同路径重复点击）递增，用作页面组件的 key，
 * 让「换一种方式」这类回到同一页的操作能重置页面状态。 */
export function useNavSeq(): number {
  const [seq, setSeq] = useState(0)
  useEffect(() => {
    const bump = () => setSeq((n) => n + 1)
    window.addEventListener('popstate', bump)
    window.addEventListener('tb:nav', bump)
    return () => {
      window.removeEventListener('popstate', bump)
      window.removeEventListener('tb:nav', bump)
    }
  }, [])
  return seq
}

/** 动作之后让别的页面（如收件箱角标）刷新。 */
export function invalidate() {
  window.dispatchEvent(new Event('tb:invalidate'))
}

export function useInvalidate(listener: () => void) {
  useEffect(() => {
    window.addEventListener('tb:invalidate', listener)
    return () => window.removeEventListener('tb:invalidate', listener)
  }, [listener])
}

/* ---------------- toast / 复制 ---------------- */

export function toast(msg: string, ms = 2600) {
  let t = document.getElementById('toast')
  if (!t) {
    t = document.createElement('div')
    t.id = 'toast'
    document.body.appendChild(t)
  }
  t.textContent = msg
  t.className = 'show'
  clearTimeout((t as any)._h)
  ;(t as any)._h = setTimeout(() => {
    t!.className = ''
  }, ms)
}

export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // 降级到 execCommand
  }
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    ta.remove()
    return ok
  } catch {
    return false
  }
}

/* ---------------- 弹窗与确认框 ---------------- */

export function Modal({
  title,
  onClose,
  children,
  wide,
}: {
  title: string
  onClose: () => void
  children: ReactNode
  wide?: boolean
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])
  return (
    <div className="mask" onClick={(e) => e.target === e.currentTarget && onClose()} role="dialog" aria-label={title}>
      <div className={`modal${wide ? ' wide' : ''}`}>
        <div className="mtitle">{title}</div>
        <div className="mbody">{children}</div>
      </div>
    </div>
  )
}

export function Confirm({
  title,
  body,
  yesLabel,
  onYes,
  onCancel,
  danger,
}: {
  title: string
  body: ReactNode
  yesLabel: string
  onYes: () => void
  onCancel: () => void
  danger?: boolean
}) {
  return (
    <Modal title={title} onClose={onCancel}>
      {body}
      <div className="mrow">
        <button className="btn ghost" onClick={onCancel}>
          取消
        </button>
        <span className="sp" />
        <button className={`btn${danger ? ' danger' : ''}`} onClick={onYes}>
          {yesLabel}
        </button>
      </div>
    </Modal>
  )
}
