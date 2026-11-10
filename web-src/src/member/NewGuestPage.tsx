// 单独新建访客（#339 基础版）：单页渐进展开。先填名字，实时提示重名或
// 相似访客（“就是 TA”直达详情，避免建出重复的人）；建立后进入结果页，
// 复制主页文案（与访客详情访问标签同一套）并去访客详情。
// #343 扩展点：名字与相似提示之下插入“可能是 TA 的老工单”勾选区与访问码
// 卡片；底部动作区升级为固定摘要栏（组件结构见 guest-new-actions）。
import { useEffect, useMemo, useState } from 'react'
import { ApiError } from '../api'
import { copyGuestHomeText, createGuest, fetchGuests, type GuestSummary } from './memberApi'
import { similarGuests } from './guestSimilarity'
import { copyText, nav, toast } from './ui'

export function NewGuestPage() {
  const [name, setName] = useState('')
  const [guests, setGuests] = useState<GuestSummary[] | null>(null)
  const [guestsError, setGuestsError] = useState<string | null>(null)
  const [created, setCreated] = useState<GuestSummary | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let alive = true
    fetchGuests()
      .then((list) => {
        if (alive) setGuests(list)
      })
      .catch(() => {
        if (alive) setGuestsError('读取部门访客名单失败，暂时无法提示相似访客；仍可继续建立')
      })
    return () => {
      alive = false
    }
  }, [])

  const trimmed = name.trim()
  const similar = useMemo(
    () => (created || !trimmed ? [] : similarGuests(trimmed, guests ?? [])),
    [created, trimmed, guests],
  )

  async function submit() {
    if (!trimmed || busy) return
    setBusy(true)
    setError(null)
    try {
      const result = await createGuest(trimmed)
      setCreated(result.guest)
      toast(`✓ 已建立访客「${result.guest.name}」`)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.code}：${e.message}` : '建立访客失败，请稍后再试')
    } finally {
      setBusy(false)
    }
  }

  async function copyHome() {
    if (!created) return
    try {
      const result = await copyGuestHomeText(created.id)
      const ok = await copyText(result.share_text)
      toast(ok ? `✓ ${created.name}的主页文案已复制（不含访问码）` : '复制失败，请手动选择文字复制')
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : '复制主页文案失败')
    }
  }

  if (created) {
    return (
      <div className="bpage guest-new-page">
        <section className="mcard" role="region" aria-label="新建访客结果">
          <div className="sec-t" style={{ marginTop: 0 }}>
            已建立访客「{created.name}」
          </div>
          <div className="hint">
            未认领 · {created.link_count} 条访客链接；名下有有效链接后，TA 的访客主页会列出这些工单。
          </div>
          <div className="guest-new-home">
            <strong>访客主页文案</strong>
            <div className="hint">发给访客本人；文案含主页地址，不含访问码，可重复复制。</div>
            <div className="actrow">
              <button className="btn" onClick={() => void copyHome()}>
                复制主页文案
              </button>
              <a
                className="btn ghost"
                href={`/guests/${created.id}`}
                onClick={(e) => {
                  e.preventDefault()
                  nav(`/guests/${created.id}`)
                }}
              >
                去访客详情
              </a>
            </div>
          </div>
        </section>
      </div>
    )
  }

  return (
    <div className="bpage guest-new-page">
      <section className="mcard form">
        <div className="fld">
          <label htmlFor="guest-new-name">
            访客名字<i className="req">必填</i>
          </label>
          <div className="guide">先填名字；如果提示里就是 TA 本人，直接进详情，避免建出重复的访客。</div>
          <input
            id="guest-new-name"
            aria-label="访客名字"
            type="text"
            placeholder="例如：检验科-张三"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </div>

        {guestsError && <div className="fld"><div className="hint">{guestsError}</div></div>}
        {trimmed && similar.length > 0 && (
          <div className="guest-similar" role="region" aria-label="相似访客提示">
            <div className="guest-similar-title">已有名字相同或相似的访客</div>
            {similar.map((g) => (
              <div className="guest-similar-row" key={g.id}>
                <span className="guest-similar-main">
                  <strong>{g.name}</strong>
                  <span className="hint">
                    {g.disabled_at
                      ? '已停用'
                      : g.claimed_at
                        ? `已认领${g.claim_name ? ` · ${g.claim_name}` : ''}`
                        : '未认领'}
                    {' · '}
                    {g.link_count} 条访客链接
                  </span>
                </span>
                <button type="button" className="btn ghost" onClick={() => nav(`/guests/${g.id}`)}>
                  就是 TA
                </button>
              </div>
            ))}
          </div>
        )}
        {trimmed && guests !== null && similar.length === 0 && !guestsError && (
          <div className="hint">没有名字相同或相似的访客，可以继续建立。</div>
        )}

        {/* #343 扩展点：这里插入“可能是 TA 的老工单”勾选列表与访问码卡片。 */}

        {error && <div className="ecard">{error}</div>}
        <div className="guest-new-actions">
          <button type="button" className="btn big" disabled={!trimmed || busy} onClick={() => void submit()}>
            {busy ? '正在建立…' : '建立访客'}
          </button>
          <div className="hint">建立时不写工单；建立后可复制主页文案，或之后再给 TA 发新工单。</div>
        </div>
      </section>
    </div>
  )
}
