import { LoginPage } from './LoginPage'
import { ShellPage } from './ShellPage'
import { GuestPage } from './guest/GuestPage'

// 网页路由：/login 登录页；/g/<密钥> 访客链接（#38）；其余是登录后的成员页面
//（后续工单加 /t/<编号> 等）。
export function App() {
  const { pathname } = window.location
  if (pathname === '/login') {
    return <LoginPage />
  }
  if (pathname.startsWith('/g/')) {
    return <GuestPage linkKey={decodeURIComponent(pathname.slice(3))} />
  }
  return <ShellPage />
}
