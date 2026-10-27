import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { ApiError, api } from './api'

// 登录后去处：?next= 只接受站内路径，否则回首页。
function nextPath(): string {
  const next = new URLSearchParams(window.location.search).get('next')
  return next && next.startsWith('/') && !next.startsWith('//') && !next.startsWith('/\\') ? next : '/'
}

export function LoginPage() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [captchaAnswer, setCaptchaAnswer] = useState('')
  const [captcha, setCaptcha] = useState<{ id: string; image: string } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const refreshCaptcha = useCallback(async () => {
    try {
      setCaptcha(await api('/api/v1/login-captcha'))
      setCaptchaAnswer('')
    } catch {
      setError('无法加载验证码，请稍后重试')
    }
  }, [])

  useEffect(() => {
    void refreshCaptcha()
  }, [refreshCaptcha])

  // 已登录就别停在登录页：iOS 主屏应用常把添加时所在的 /login 当启动地址。
  useEffect(() => {
    api('/api/v1/session')
      .then((s) => {
        if (s?.viewer?.kind === 'member') window.location.replace(nextPath())
      })
      .catch(() => {})
  }, [])

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await api('/api/v1/session', {
        method: 'POST',
        body: JSON.stringify({ username, password, captcha_id: captcha?.id, captcha_answer: captchaAnswer }),
      })
      window.location.href = nextPath()
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.hint ? `${err.message}（${err.hint}）` : err.message)
        if (err.code.startsWith('captcha_')) void refreshCaptcha()
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
          验证码
          <span className="captcha-row">
            <input
              value={captchaAnswer}
              onChange={(e) => setCaptchaAnswer(e.target.value)}
              autoComplete="off"
              inputMode="text"
            />
            {captcha && <img className="captcha-image" src={captcha.image} alt="验证码图片" />}
            <button type="button" className="captcha-refresh" onClick={() => void refreshCaptcha()}>
              换一张
            </button>
          </span>
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
