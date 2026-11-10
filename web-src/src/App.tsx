import { LoginPage } from './LoginPage'
import { MemberApp } from './member/MemberApp'
import { GuestPage } from './guest/GuestPage'
import { GuestHomePage } from './guest/GuestHomePage'
import { IntakePage } from './guest/IntakePage'
import { ClientConnectPage } from './ClientConnectPage'

// 网页路由：/login 登录页；/g/<密钥> 访客链接（#38）；/i/<slug>
// 反馈入口页（#39）；/h/<主页密钥> 是访客主页（#329）；其余是登录后的成员
// 页面（#41：/ 收件箱、/tickets 列表、
// /t/<编号> 详情、/new 建单、/tools、/me）。
export function App() {
  const { pathname } = window.location
  if (pathname === '/login') {
    return <LoginPage />
  }
  if (pathname.startsWith('/g/')) {
    return <GuestPage linkKey={decodeURIComponent(pathname.slice(3))} />
  }
  if (pathname.startsWith('/h/')) {
    const rest = pathname.slice(3).replace(/\/+$/, '')
    const marker = rest.indexOf('/t/')
    if (marker >= 0) {
      const homeKey = decodeURIComponent(rest.slice(0, marker))
      const number = decodeURIComponent(rest.slice(marker + 3))
      return (
        <GuestPage
          linkKey={homeKey}
          ticketApi={`/api/v1/tickets/${encodeURIComponent(number)}`}
          fromHome
        />
      )
    }
    return <GuestHomePage homeKey={decodeURIComponent(rest)} />
  }
  if (pathname.startsWith('/i/')) {
    return <IntakePage slug={decodeURIComponent(pathname.slice(3).replace(/\/+$/, '')) } />
  }
  if (pathname === '/client/connect') {
    return <ClientConnectPage />
  }
  return <MemberApp />
}
