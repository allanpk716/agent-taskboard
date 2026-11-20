// 「我的」页的通知出口自助配置（#372）。webhook key 只写不读；
// 保存/换 bot 都重建基线，所以页面明示“从下一条新通知开始推送”。
import { useEffect, useState } from 'react'
import { ApiError } from '../api'
import {
  deleteNotificationOutlet,
  fetchNotificationOutlet,
  pauseNotificationOutlet,
  resumeNotificationOutlet,
  saveNotificationOutlet,
  sendNotificationOutletTest,
  type NotificationOutletTestResponse,
  type NotificationOutletView,
} from './memberApi'
import { Confirm } from './ui'
import { toast } from './ui'

const botIDPattern = /^[a-z0-9]{1,64}$/

type Busy = 'save' | 'pause' | 'resume' | 'delete' | 'test' | null

export function NotificationOutletCard() {
  const [view, setView] = useState<NotificationOutletView | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [botID, setBotID] = useState('')
  const [webhookKey, setWebhookKey] = useState('')
  const [changedBot, setChangedBot] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const [rateNotice, setRateNotice] = useState<string | null>(null)
  const [testResult, setTestResult] = useState<NotificationOutletTestResponse | null>(null)
  const [busy, setBusy] = useState<Busy>(null)
  const [confirmingTest, setConfirmingTest] = useState(false)
  const [confirmingDelete, setConfirmingDelete] = useState(false)

  async function refresh() {
    try {
      const next = await fetchNotificationOutlet()
      setView(next)
      setBotID(next.outlet?.bot_id ?? '')
      setWebhookKey('')
      setChangedBot(false)
      setLoadError(null)
    } catch {
      setLoadError('读取通知推送配置失败')
    }
  }

  useEffect(() => {
    void refresh()
  }, [])

  function changeBotID(next: string) {
    setBotID(next)
    const changed = view?.outlet != null && next !== view.outlet.bot_id
    setChangedBot(changed)
    if (changed) setWebhookKey('')
  }

  async function save() {
    if (!botIDPattern.test(botID)) {
      setActionError('bot_id 只能包含小写字母和数字，长度 1-64')
      return
    }
    if (!webhookKey) {
      setActionError(changedBot ? '更换 bot 后必须重新粘贴 webhook key' : '请粘贴 webhook key')
      return
    }
    setBusy('save')
    setActionError(null)
    try {
      const next = await saveNotificationOutlet(botID, webhookKey)
      setView(next)
      setBotID(next.outlet?.bot_id ?? '')
      setWebhookKey('')
      setChangedBot(false)
      setTestResult(null)
      toast('✓ 已保存；从下一条新通知开始推送')
    } catch (e) {
      setActionError(e instanceof ApiError ? e.message : '保存通知推送配置失败')
    } finally {
      setBusy(null)
    }
  }

  async function switchStatus(pause: boolean) {
    setBusy(pause ? 'pause' : 'resume')
    setActionError(null)
    try {
      setView(await (pause ? pauseNotificationOutlet() : resumeNotificationOutlet()))
    } catch (e) {
      setActionError(e instanceof ApiError ? e.message : pause ? '暂停失败' : '恢复失败')
    } finally {
      setBusy(null)
    }
  }

  async function remove() {
    setBusy('delete')
    setActionError(null)
    try {
      await deleteNotificationOutlet()
      await refresh()
      setTestResult(null)
      toast('已删除通知推送')
    } catch (e) {
      setActionError(e instanceof ApiError ? e.message : '删除失败')
    } finally {
      setBusy(null)
      setConfirmingDelete(false)
    }
  }

  async function sendTest() {
    setBusy('test')
    setActionError(null)
    setRateNotice(null)
    try {
      setTestResult(await sendNotificationOutletTest())
    } catch (e) {
      if (e instanceof ApiError && e.code === 'outlet_test_rate_limited') {
        setRateNotice('一分钟内只能发送一次测试通知；稍后再试。')
      } else {
        setActionError(e instanceof ApiError ? e.message : '发送测试通知失败')
      }
    } finally {
      setBusy(null)
      setConfirmingTest(false)
    }
  }

  const outlet = view?.outlet
  const disabled = view != null && !view.enabled
  const health = outletHealth(outlet)

  return (
    <div className="mcard" data-testid="notification-outlet-card">
      <div className="sec-t" style={{ marginTop: 0 }}>
        通知推送
      </div>
      <div className="hint">
        有新通知时，平台只向你自己配置的 bot 发一条不含工单内容的门铃信号；bot
        再用只读凭证查通知。
      </div>

      {loadError && <div className="errbox">{loadError}</div>}
      {disabled && (
        <div className="warnbox" role="status">
          这个部署没有开启通知出口。请联系部署管理员确认配置后再使用。
        </div>
      )}
      {!disabled && (
        <>
          {outlet && (
            <>
              <div className="kv">
                <span>当前配置</span>
                <span>
                  <b className="mono">{outlet.bot_id}</b>{' '}
                  <span className={`badge ${outlet.status === 'active' ? 'open' : 'closed'}`}>
                    {outlet.status === 'active' ? '推送中' : '已暂停'}
                  </span>
                  <div className="hint">webhook key 只显示末 4 位：····{outlet.key_last4}</div>
                </span>
              </div>
              {health.tone !== 'ok' && (
                <div
                  className={health.tone === 'error' ? 'errbox' : 'warnbox'}
                  role="status"
                  data-testid="outlet-delivery-state"
                >
                  {health.text}
                </div>
              )}
            </>
          )}

          <label className="fldlab" htmlFor="notification-outlet-bot-id">
            bot_id
          </label>
          <input
            id="notification-outlet-bot-id"
            value={botID}
            autoComplete="off"
            onChange={(e) => changeBotID(e.target.value)}
            disabled={busy != null}
          />
          <label className="fldlab" htmlFor="notification-outlet-key">
            webhook key
          </label>
          <input
            id="notification-outlet-key"
            type="password"
            autoComplete="new-password"
            value={webhookKey}
            onChange={(e) => setWebhookKey(e.target.value)}
            disabled={busy != null}
          />
          <div className="hint">
            {outlet
              ? changedBot
                ? '你换了 bot：旧 key 已清空，必须重新粘贴新 bot 的 webhook key。'
                : '保存配置也需要重新粘贴 webhook key；key 只写不读，不会回填到页面。'
              : 'key 只写不读；保存后页面最多显示末 4 位。'}
          </div>
          <div className="hint">保存或更换 bot 后从下一条新通知开始推送，不补推历史通知。</div>
          {changedBot && webhookKey && <div className="hint">已输入新 bot 的 webhook key。</div>}
          {actionError && <div className="errbox">{actionError}</div>}
          {rateNotice && <div className="warnbox">{rateNotice}</div>}
          {testResult && (
            <div
              className={
                testResult.result === 'ok'
                  ? 'okbox'
                  : testResult.result === 'unauthorized' || testResult.result === 'http_error'
                    ? 'errbox'
                    : 'warnbox'
              }
              role="status"
              aria-live="polite"
              data-testid="outlet-test-result"
            >
              {testResultText(testResult)}
            </div>
          )}

          <div className="btnrow">
            <button
              className="btn"
              onClick={() => void save()}
              disabled={busy != null || !botIDPattern.test(botID) || !webhookKey}
            >
              {busy === 'save' ? '保存中…' : outlet ? '重建配置' : '保存配置'}
            </button>
            {outlet && (
              <>
                <button
                  className="btn ghost"
                  onClick={() => void switchStatus(outlet.status === 'active')}
                  disabled={busy != null}
                >
                  {busy === (outlet.status === 'active' ? 'pause' : 'resume')
                    ? '处理中…'
                    : outlet.status === 'active'
                      ? '暂停'
                      : '恢复'}
                </button>
                <button
                  className="btn ghost"
                  onClick={() => setConfirmingTest(true)}
                  disabled={busy != null}
                >
                  {busy === 'test' ? '发送中…' : '发送测试通知'}
                </button>
                <button
                  className="btn danger"
                  onClick={() => setConfirmingDelete(true)}
                  disabled={busy != null}
                >
                  删除
                </button>
              </>
            )}
          </div>
        </>
      )}

      {confirmingTest && (
        <Confirm
          title="发送测试通知？"
          body={<>每次测试都会真实触发目标 bot 一次 run，请确认现在可以打扰它。</>}
          yesLabel="发送测试通知"
          onYes={() => void sendTest()}
          onCancel={() => setConfirmingTest(false)}
        />
      )}
      {confirmingDelete && (
        <Confirm
          title="删除通知推送？"
          body={<>删除后立即停止推送；以后重建配置会重新建基线，不补推这段时间的通知。</>}
          yesLabel="删除"
          danger
          onYes={() => void remove()}
          onCancel={() => setConfirmingDelete(false)}
        />
      )}
    </div>
  )
}

function testResultText(result: NotificationOutletTestResponse): string {
  switch (result.result) {
    case 'ok':
      return `✓ 目标平台返回 ${result.http_status}，链路已打通。`
    case 'ignored':
      return `目标平台返回 ${result.http_status} 且 ignored:true：地址可达，但这个 bot 没有接住测试事件。`
    case 'unauthorized':
      return `目标平台返回 ${result.http_status}：webhook key 或 bot 权限不对。`
    case 'timeout':
      return '目标平台超时或网络不通（10 秒内没有返回）。'
    default:
      return `目标平台返回 ${result.http_status}，链路未通过。`
  }
}

// #374：失败分级提示。4xx 自动暂停标红；网络/5xx 标黄，持续超过约 4 小时
// 再标红；ignored 单独标黄（空响≠链路通）。成员手动暂停只是正常状态。
function outletHealth(outlet: NotificationOutletView['outlet'] | undefined): {
  tone: 'ok' | 'warn' | 'error'
  text: string
} {
  if (!outlet) return { tone: 'ok', text: '' }
  if (outlet.status === 'paused') {
    if (outlet.pause_reason === 'outlet_paused') {
      return {
        tone: 'error',
        text: '目标平台连续拒绝请求（4xx），通知推送已自动暂停；请检查配置后重新保存或恢复。',
      }
    }
    if (outlet.pause_reason === 'member_deactivated') {
      return {
        tone: 'error',
        text: '通知推送已随成员账号停用；恢复账号后会从下一条新通知开始推送。',
      }
    }
    return { tone: 'ok', text: '' }
  }
  if (outlet.delivery_state === 'permanent') {
    return { tone: 'error', text: '目标平台拒绝请求（4xx）；这一条不会重试，请检查配置。' }
  }
  if (outlet.delivery_state === 'ignored') {
    return {
      tone: 'warn',
      text: '目标平台可达，但返回 ignored:true：这个 bot 没有接住通知事件（空响≠链路通）。',
    }
  }
  if (outlet.delivery_state === 'transient') {
    const since = outlet.transient_since ? Date.parse(outlet.transient_since) : NaN
    const stale = Number.isFinite(since) && Date.now() - since >= 4 * 60 * 60 * 1000
    return stale
      ? { tone: 'error', text: '暂时送不到已超过约 4 小时；请检查目标平台或网络。' }
      : { tone: 'warn', text: '暂时送不到（网络或目标平台 5xx）；平台会按同一幂等键自动重试。' }
  }
  return { tone: 'ok', text: '' }
}
