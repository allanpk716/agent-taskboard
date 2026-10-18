// 「交给 Agent」（#110）：共用预览与复制面板；详情页用它包弹窗，手机视口由
// Modal 的既有样式变成底部抽屉。#111 的改派/建单页可以直接复用内嵌面板。
import { useEffect, useState } from 'react'
import { fetchAgentTokens } from './memberApi'
import { Modal, copyText, nav } from './ui'

export type HandoffMode = 'share' | 'agent'

export interface HandoffPanelProps {
  handoffText: string
  handoffShareText?: string | null
  assigneeName?: string | null
  isAssignee: boolean
  defaultMode?: HandoffMode
}

export function HandoffPanel({
  handoffText,
  handoffShareText,
  assigneeName,
  isAssignee,
  defaultMode = 'share',
}: HandoffPanelProps) {
  const chooseShare = !isAssignee && !!assigneeName && !!handoffShareText
  const [mode, setMode] = useState<HandoffMode>(chooseShare ? defaultMode : 'agent')
  const [noToken, setNoToken] = useState(false)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    let alive = true
    fetchAgentTokens()
      .then((tokens) => {
        if (alive) setNoToken(!tokens.some((t) => !t.revoked_at))
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [])

  const text = mode === 'share' && handoffShareText ? handoffShareText : handoffText

  async function copy() {
    const ok = await copyText(text)
    setCopied(ok)
    if (ok) setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div>
      {chooseShare && (
        <div className="handoff-modes" role="radiogroup" aria-label="交接对象">
          <label>
            <input
              type="radio"
              name="handoff-mode"
              checked={mode === 'share'}
              onChange={() => setMode('share')}
            />{' '}
            发给{assigneeName}（含人话行）
          </label>
          <label>
            <input
              type="radio"
              name="handoff-mode"
              checked={mode === 'agent'}
              onChange={() => setMode('agent')}
            />{' '}
            只要 Agent 段
          </label>
        </div>
      )}
      <pre className="handoff-pre" data-handoff-preview>
        {text}
      </pre>
      <button type="button" className="btn handoff-copy" onClick={() => void copy()}>
        {copied ? '已复制' : '复制交接文案'}
      </button>
      {noToken && (
        <div className="no-token">
          你还没签发 Agent token →{' '}
          <a
            href="/me"
            onClick={(e) => {
              e.preventDefault()
              nav('/me')
            }}
          >
            去「我的 → Agent」
          </a>
        </div>
      )}
    </div>
  )
}

export function HandoffDialog(props: HandoffPanelProps & { onClose: () => void }) {
  const { onClose, ...panel } = props
  return (
    <Modal title="交给 Agent" onClose={onClose}>
      <HandoffPanel {...panel} />
      <div className="mrow handoff-foot">
        <span className="hint">文案不含凭证，对方的 Agent 用它主人自己签的 token</span>
        <span className="sp" />
        <button className="btn ghost" onClick={onClose}>
          关闭
        </button>
      </div>
    </Modal>
  )
}
