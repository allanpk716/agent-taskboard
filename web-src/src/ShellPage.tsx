import { useEffect, useState } from 'react'
import { api } from './api'

interface Member {
  id: number
  username: string
  name: string
  is_admin: boolean
}

// 登录后的空壳页面：先确认登录态，再放占位内容。
export function ShellPage() {
  const [me, setMe] = useState<Member | null>(null)

  useEffect(() => {
    api('/api/v1/session')
      .then((body) => setMe(body.member))
      .catch(() => {
        window.location.href = '/login'
      })
  }, [])

  async function logout() {
    try {
      await api('/api/v1/session', { method: 'DELETE' })
    } finally {
      window.location.href = '/login'
    }
  }

  if (!me) {
    return (
      <main className="shell">
        <p>加载中…</p>
      </main>
    )
  }

  return (
    <main className="shell">
      <header>
        <h1>部门工单平台</h1>
        <div className="me">
          <span>
            {me.name}（{me.username}）
          </span>
          <button onClick={logout}>登出</button>
        </div>
      </header>
      <section className="placeholder" aria-label="空壳页面">
        <p>这里是空壳页面：收件箱、工单列表等将在后续版本加入。</p>
      </section>
    </main>
  )
}
