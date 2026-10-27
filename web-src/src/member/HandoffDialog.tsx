// 「复制给我的 Agent」「复制给对方的 Agent」两个常驻入口的弹窗。前者预览
// 工单指令；后者在成员/访客之间二选一——成员分支是中性的交接文案，访客
// 分支复用现有访客链接转发文案的生成与复制接口。指派后的点名交接流程
// （AssignDialog、建单完成区）仍用 mode="share" 的 HandoffPanel。
import { useEffect, useState } from 'react'
import { ApiError } from '../api'
import { linkExpired } from '../shared/format'
import {
  copyGuestLinkShareText,
  createGuestLink,
  fetchAgentTokens,
  rotateGuestLinkKey,
  type GuestLink,
} from './memberApi'
import { Confirm, Modal, copyText, nav, toast } from './ui'

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
      .then((tokens) => {
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
    <Modal title={mode === 'share' ? `📨 发给 ${assigneeName}` : '🤖 复制给我的 Agent'} onClose={onClose}>
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

/* ---------------- 复制给对方的 Agent：成员 / 访客二选一 ---------------- */

export type HandoffPeer = 'member' | 'guest'

export interface PeerHandoffPanelProps {
  ticketNumber: string
  /** 中性交接文案（成员分支）：不点名，任何工单都有。 */
  handoffShareText?: string | null
  links: GuestLink[] | null
  canManageGuestLinks: boolean
  defaultPeer: HandoffPeer
  reloadLinks: () => Promise<void>
  onChanged: () => void
}

/** 与访客链接管理弹窗一致的新建链接字段；建好后由弹窗立即选中并显示转发文案。 */
function CreateGuestLinkFields({
  ticketNumber,
  onCreated,
  reloadLinks,
  onChanged,
}: {
  ticketNumber: string
  onCreated: (id: number) => void
  reloadLinks: () => Promise<void>
  onChanged: () => void
}) {
  const [note, setNote] = useState('')
  const [days, setDays] = useState('30')
  const [date, setDate] = useState('')
  const [canEditBrief, setCanEditBrief] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function create() {
    if (!note.trim()) {
      setErr('note_required：备注必填，写上给谁用的，如「检验科-张三」')
      return
    }
    try {
      const link =
        days === 'date'
          ? await createGuestLink(ticketNumber, {
              note: note.trim(),
              expires_at: date,
              can_edit_brief: canEditBrief,
            })
          : await createGuestLink(ticketNumber, {
              note: note.trim(),
              valid_days: Number(days),
              can_edit_brief: canEditBrief,
            })
      await reloadLinks()
      onChanged()
      onCreated(link.id)
      toast('✓ 已创建访客链接')
    } catch (e) {
      setErr(e instanceof ApiError ? `${e.code}：${e.message}` : '创建失败，请重试')
    }
  }

  return (
    <div className="peer-create">
      <div className="sec-t">新建访客链接</div>
      <div className="inrow">
        <input
          aria-label="备注"
          placeholder="备注（必填），如：检验科-张三"
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />
      </div>
      <div className="inrow">
        <select className="sel" aria-label="有效期" value={days} onChange={(e) => setDays(e.target.value)}>
          <option value="7">7 天</option>
          <option value="30">30 天（默认）</option>
          <option value="90">90 天</option>
          <option value="date">指定日期（当天有效）</option>
        </select>
        {days === 'date' && (
          <input type="date" aria-label="到期日期" value={date} onChange={(e) => setDate(e.target.value)} />
        )}
        <label className="ckrow" style={{ margin: 0 }}>
          <input type="checkbox" checked={canEditBrief} onChange={(e) => setCanEditBrief(e.target.checked)} /> 可填写说明
        </label>
      </div>
      <div className="hint">没有永久有效的选项；链接的创建、撤销和延期都会记入时间线。</div>
      {err && <div className="errbox">{err}</div>}
      <button type="button" className="btn handoff-copy" onClick={() => void create()}>
        创建并显示转发文案
      </button>
    </div>
  )
}

export function PeerHandoffPanel({
  ticketNumber,
  handoffShareText,
  links,
  canManageGuestLinks,
  defaultPeer,
  reloadLinks,
  onChanged,
}: PeerHandoffPanelProps) {
  const [peer, setPeer] = useState<HandoffPeer>(defaultPeer)
  const activeLinks = (links ?? []).filter((l) => !l.revoked_at && !linkExpired(l.expires_at))
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [shareText, setShareText] = useState<string | null>(null)
  const [loadingShare, setLoadingShare] = useState(false)
  const [shareErr, setShareErr] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [rotating, setRotating] = useState<GuestLink | null>(null)
  const [copied, setCopied] = useState(false)
  const selected = activeLinks.find((l) => l.id === selectedId) ?? null

  // 一条有效链接直接选中；一条都没有时直接进入新建。
  useEffect(() => {
    if (selectedId !== null) return
    if (activeLinks.length === 1) {
      setSelectedId(activeLinks[0].id)
    } else if (activeLinks.length === 0 && links !== null && canManageGuestLinks) {
      setCreating(true)
    }
  }, [activeLinks, links, selectedId, canManageGuestLinks])

  const selectedKey = selected?.id
  useEffect(() => {
    if (peer !== 'guest' || !selectedKey) {
      setShareText(null)
      setShareErr(null)
      return
    }
    let alive = true
    setLoadingShare(true)
    setShareText(null)
    setShareErr(null)
    copyGuestLinkShareText(ticketNumber, selectedKey)
      .then((result) => {
        if (alive) setShareText(result.share_text)
      })
      .catch((e) => {
        if (alive) setShareErr(e instanceof ApiError ? `${e.code}：${e.message}` : '生成转发文案失败')
      })
      .finally(() => {
        if (alive) setLoadingShare(false)
      })
    return () => {
      alive = false
    }
  }, [peer, selectedKey, ticketNumber])

  async function copy() {
    const text = peer === 'member' ? handoffShareText ?? '' : shareText ?? ''
    const ok = await copyText(text)
    setCopied(ok)
    if (ok) setTimeout(() => setCopied(false), 2000)
  }

  const preview = peer === 'member' ? handoffShareText ?? '' : shareText ?? ''
  const copyLabel = peer === 'member' ? '复制交接文案' : '复制转发文案'
  const showNewLinkButton = canManageGuestLinks && !creating

  return (
    <div className="peer-handoff" data-peer={peer}>
      <div className="inrow peer-choice" role="radiogroup" aria-label="对方是">
        <label className="ckrow" style={{ margin: 0 }}>
          <input
            type="radio"
            name="handoff-peer"
            checked={peer === 'member'}
            onChange={() => setPeer('member')}
          />{' '}
          成员
        </label>
        <label className="ckrow" style={{ margin: 0 }} data-guest-peer-option>
          <input
            type="radio"
            name="handoff-peer"
            checked={peer === 'guest'}
            disabled={!canManageGuestLinks}
            onChange={() => setPeer('guest')}
          />{' '}
          访客
        </label>
        {!canManageGuestLinks && (
          <span className="hint" data-guest-link-denied>
            你不能管理这张工单的访客链接，请找创建者或指派人
          </span>
        )}
      </div>

      {peer === 'member' && (
        <>
          <div className="handoff-who">
            对方是成员时，把这段发给对方本人，由对方转交给他自己的 Agent；不含任何凭证。
          </div>
          <pre className="handoff-pre" data-handoff-preview>
            {preview}
          </pre>
          <button type="button" className="btn handoff-copy" onClick={() => void copy()}>
            {copied ? '已复制' : copyLabel}
          </button>
        </>
      )}

      {peer === 'guest' && (
        <>
          <div className="handoff-who">对方是访客时，转发文案自带访客链接凭证，只发给这一位访客。</div>
          {links === null && <div className="hint">访客链接加载中…</div>}
          {links !== null && activeLinks.length === 0 && !creating && (
            <div className="hint">这张工单还没有有效的访客链接。</div>
          )}
          {activeLinks.length > 0 && (activeLinks.length > 1 || creating) && (
            <div className="inrow">
              <select
                className="sel"
                aria-label="选择访客链接"
                value={selectedId ?? ''}
                onChange={(e) => setSelectedId(Number(e.target.value))}
              >
                {activeLinks.map((l) => (
                  <option key={l.id} value={l.id}>
                    {l.note}
                  </option>
                ))}
              </select>
              {showNewLinkButton && (
                <button type="button" className="btn sm ghost" onClick={() => setCreating(true)}>
                  ＋ 新建链接
                </button>
              )}
            </div>
          )}
          {activeLinks.length === 1 && !creating && (
            <div className="inrow">
              <span className="hint">{activeLinks[0].note}</span>
              <span className="sp" />
              {showNewLinkButton && (
                <button type="button" className="btn sm ghost" onClick={() => setCreating(true)}>
                  ＋ 新建链接
                </button>
              )}
            </div>
          )}
          {selected?.claimed_at && (
            <div className="warnbox" data-claimed-link>
              这条链接已被认领，只能发给原来那位访客；要发给新的人，请新建一条。
            </div>
          )}
          {selected && !selected.share_text_available && (
            <div className="warnbox">
              这是老访客链接，要先换新地址才能复制转发文案。
              <button
                type="button"
                className="btn sm"
                style={{ marginLeft: 8 }}
                onClick={() => setRotating(selected)}
              >
                换新地址并复制
              </button>
            </div>
          )}
          {loadingShare && <div className="hint">正在生成转发文案…</div>}
          {shareErr && (
            <div className="errbox">
              {shareErr}
              {selected && !selected.share_text_available ? '（需先换新地址）' : ''}
            </div>
          )}
          {!loadingShare && !shareErr && preview && (
            <pre className="handoff-pre" data-handoff-preview>
              {preview}
            </pre>
          )}
          {preview && (
            <button type="button" className="btn handoff-copy" onClick={() => void copy()}>
              {copied ? '已复制' : copyLabel}
            </button>
          )}
          {creating && canManageGuestLinks && (
            <CreateGuestLinkFields
              ticketNumber={ticketNumber}
              reloadLinks={reloadLinks}
              onChanged={onChanged}
              onCreated={(id) => {
                setCreating(false)
                setSelectedId(id)
              }}
            />
          )}
        </>
      )}

      {rotating && (
        <Confirm
          title="换新地址并复制？"
          body={
            <div>
              对方手里的旧地址会失效，请把新文案发过去。备注、认领状态、访问码和有效期都保持不变。
            </div>
          }
          yesLabel="换新地址并复制"
          danger
          onYes={() => {
            const target = rotating
            setRotating(null)
            void rotateGuestLinkKey(ticketNumber, target.id)
              .then(async (result) => {
                setShareText(result.share_text)
                setShareErr(null)
                await reloadLinks()
                onChanged()
                toast('✓ 已换新地址')
              })
              .catch((e) => toast(e instanceof ApiError ? `${e.code}：${e.message}` : '换新地址失败'))
          }}
          onCancel={() => setRotating(null)}
        />
      )}
    </div>
  )
}

export function PeerHandoffDialog(props: PeerHandoffPanelProps & { onClose: () => void }) {
  const { onClose, ...panel } = props
  return (
    <Modal title="📨 复制给对方的 Agent" onClose={onClose}>
      <PeerHandoffPanel {...panel} />
      <div className="mrow handoff-foot">
        <span className="hint">成员分支不含凭证；访客分支的转发文案自带访客链接，只发给对应访客。</span>
        <span className="sp" />
        <button className="btn ghost" onClick={onClose}>
          关闭
        </button>
      </div>
    </Modal>
  )
}
