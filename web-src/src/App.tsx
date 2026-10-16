import { LoginPage } from './LoginPage'
import { MemberApp } from './member/MemberApp'
import { GuestPage } from './guest/GuestPage'
import { IntakePage } from './guest/IntakePage'

// 网页路由：/login 登录页；/g/<密钥> 访客链接（#38）；/i/<slug> 反馈入口
// 提交页（#39）；其余是登录后的成员页面（#41：/ 收件箱、/tickets 列表、
// /t/<编号> 详情、/new 建单、/tools、/me）。
export function App() {
  const { pathname } = window.location
  if (pathname === '/login') {
    return <LoginPage />
  }
  if (pathname.startsWith('/g/')) {
    return <GuestPage linkKey={decodeURIComponent(pathname.slice(3))} />
  }
  if (pathname.startsWith('/i/')) {
    return <IntakePage slug={decodeURIComponent(pathname.slice(3).replace(/\/+$/, '')) } />
  }
  return <MemberApp />
}
