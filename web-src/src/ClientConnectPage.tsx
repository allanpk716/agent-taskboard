import { useEffect, useState } from 'react'
import { ApiError, api } from './api'
import { nav } from './member/ui'

export function ClientConnectPage() {
  const params = new URLSearchParams(window.location.search)
  const port = params.get('port') ?? ''
  const state = params.get('state') ?? ''
  const challenge = params.get('code_challenge') ?? ''
  const hostname = params.get('hostname') ?? ''
  const [ready, setReady] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api('/api/v1/session')
      .then(() => setReady(true))
      .catch((err) => {
        if (err instanceof ApiError && err.status === 401) {
          window.location.replace(`/login?next=${encodeURIComponent(window.location.pathname + window.location.search)}`)
          return
        }
        setError('无法连接服务，请稍后重试')
      })
  }, [])

  async function connect() {
    setBusy(true)
    setError(null)
    try {
      const body = await api('/api/v1/client/connect', {
        method: 'POST',
        body: JSON.stringify({ port: Number(port), state, code_challenge: challenge, hostname }),
      })
      window.location.href = (body as { redirect_url: string }).redirect_url
    } catch (err) {
      setError(err instanceof ApiError ? `${err.message}（${err.code}）` : '无法连接服务，请稍后重试')
    } finally {
      setBusy(false)
    }
  }

  if (!ready && !error) {
    return (
      <main className="login">
        <h1>连接这台电脑</h1>
        <p>加载中…</p>
      </main>
    )
  }

  return (
    <main className="login">
      <h1>连接这台电脑</h1>
      <p className="client-hostname">{hostname || '未知电脑'}</p>
      {error && <p role="alert" className="error">{error}</p>}
      <div className="btnrow">
        <button className="btn ghost" onClick={() => nav('/')} disabled={busy}>
          取消
        </button>
        <span className="sp" />
        <button className="btn" onClick={() => void connect()} disabled={busy || !ready}>
          连接
        </button>
      </div>
    </main>
  )
}
