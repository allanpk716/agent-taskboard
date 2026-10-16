import { LoginPage } from './LoginPage'
import { ShellPage } from './ShellPage'

// 网页路由：/login 是登录页，其余都是登录后的成员页面（后续工单加 /t/<编号> 等）。
export function App() {
  if (window.location.pathname === '/login') {
    return <LoginPage />
  }
  return <ShellPage />
}
