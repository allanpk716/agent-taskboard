import { useState, type FormEvent } from 'react'
import { ApiError, api } from './api'

export function LoginPage() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await api('/api/v1/session', {
        method: 'POST',
        body: JSON.stringify({ username, password }),
      })
      const next = new URLSearchParams(window.location.search).get('next')
      window.location.href =
        next && next.startsWith('/') && !next.startsWith('//') && !next.startsWith('/\\') ? next : '/'
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.hint ? `${err.message}（${err.hint}）` : err.message)
      } else {
        setError('无法连接服务，请稍后重试')
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="login">
      <h1>部门工单平台</h1>
      <form onSubmit={onSubmit}>
        <label>
          用户名
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            autoFocus
          />
        </label>
        <label>
          密码
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
          />
        </label>
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
        <button type="submit" disabled={busy}>
          登录
        </button>
      </form>
    </main>
  )
}
