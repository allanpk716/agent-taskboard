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

  const loadCaptcha = useCallback(async (): Promise<boolean> => {
    try {
      setCaptcha(await api('/api/v1/login-captcha'))
      setCaptchaAnswer('')
      return true
    } catch {
      return false
    }
  }, [])

  const refreshCaptcha = useCallback(async () => {
    if (!(await loadCaptcha())) setError('无法加载验证码，请稍后重试')
  }, [loadCaptcha])

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
        // 验证码是一次性的，而且服务端在核对密码之前就把它消费掉了：密码错、账号
        // 锁定、账号停用之后这张图都已经作废，不换图的话改对密码再提交只会得到
        // 「验证码已使用」。限流（429）在消费之前就返回，那张图还能用，不必换。
        if (err.code.startsWith('captcha_')) void refreshCaptcha()
        else if (err.status !== 429) void loadCaptcha()
      } else {
        setError('无法连接服务，请稍后重试')
        // 请求可能其实已经到过服务端（验证码已被消费）：悄悄换一张，换不到就保持原样。
        void loadCaptcha()
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
