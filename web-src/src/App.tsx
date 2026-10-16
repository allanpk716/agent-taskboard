import { LoginPage } from './LoginPage'
import { ShellPage } from './ShellPage'
import { GuestPage } from './guest/GuestPage'
import { IntakePage } from './guest/IntakePage'

// 网页路由：/login 登录页；/g/<密钥> 访客链接（#38）；/i/<slug> 反馈入口
// 提交页（#39）；其余是登录后的成员页面（后续工单加 /t/<编号> 等）。
export function App() {
  const { pathname } = window.location
  if (pathname === '/login') {
    return <LoginPage />
  }
  if (pathname.startsWith('/g/')) {
    return <GuestPage linkKey={decodeURIComponent(pathname.slice(3))} />
  }
  if (pathname.startsWith('/i/')) {
    return <IntakePage slug={decodeURIComponent(pathname.slice(3).replace(/\/+$/, ''))} />
  }
  return <ShellPage />
}
