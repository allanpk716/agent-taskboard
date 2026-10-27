// 「让我的 Agent 处理」与「发给指派人」共用预览与复制面板；详情页用它包
// 弹窗，手机视口由 Modal 的既有样式变成底部抽屉。建单页直接内嵌交接面板。
import { useEffect, useState } from 'react'
import { fetchAgentTokens } from './memberApi'
import { Modal, copyText, nav } from './ui'

export type HandoffMode = 'agent' | 'share'

export interface HandoffPanelProps {
  handoffText: string
  handoffShareText?: string | null
  assigneeName?: string | null
  mode: HandoffMode
}

export function HandoffPanel({
  handoffText,
  handoffShareText,
  assigneeName,
  mode,
}: HandoffPanelProps) {
  const [noToken, setNoToken] = useState(false)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (mode !== 'agent') return
    let alive = true
    fetchAgentTokens()
      .then(({ tokens }) => {
        if (alive) setNoToken(!tokens.some((t) => !t.revoked_at))
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [])

  const share = mode === 'share'
  const text = share ? handoffShareText ?? '' : handoffText

  async function copy() {
    const ok = await copyText(text)
    setCopied(ok)
    if (ok) setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div>
      <div className="handoff-who" data-handoff-role="hint">
        {share ? `复制后发给${assigneeName}，他再把后半段交给自己的 Agent` : '复制后贴给你自己的 Agent'}
      </div>
      {mode === 'agent' && noToken && (
        <div className="warnbox handoff-onboarding">
          <b>你的 Agent 还没接入</b>，第一次需要两步：
          <ol>
            <li>
              去{' '}
              <a
                href="/me"
                onClick={(e) => {
                  e.preventDefault()
                  nav('/me')
                }}
              >
                「我的 → Agent」
              </a>{' '}
              生成 token，把<b>接入文案</b>贴给你的 Agent（只需一次）；
            </li>
            <li>回到这里复制工单指令贴给它。</li>
          </ol>
        </div>
      )}
      <pre className="handoff-pre" data-handoff-preview>
        {text}
      </pre>
      <button type="button" className="btn handoff-copy" onClick={() => void copy()}>
        {copied ? '已复制' : share ? '复制交接文案' : '复制工单指令'}
      </button>
    </div>
  )
}

export function HandoffDialog(props: HandoffPanelProps & { onClose: () => void }) {
  const { onClose, mode, assigneeName, ...panel } = props
  return (
    <Modal title={mode === 'share' ? `📨 发给 ${assigneeName}` : '🤖 让我的 Agent 处理'} onClose={onClose}>
      <HandoffPanel {...panel} mode={mode} assigneeName={assigneeName} />
      <div className="mrow handoff-foot">
        <span className="hint">
          {mode === 'share'
            ? `不含凭证：${assigneeName}的 Agent 用${assigneeName}自己的 token。`
            : '不含凭证：Agent 用接入时存好的 TASKBOARD_TOKEN。'}
        </span>
        <span className="sp" />
        <button className="btn ghost" onClick={onClose}>
          关闭
        </button>
      </div>
    </Modal>
  )
}
