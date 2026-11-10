// 单独新建访客（#339 基础版 + #343 渐进展开）：单页先填名字，实时提示
// 重名或相似访客（“就是 TA”直达详情，避免建出重复的人）；名字之下展开
// “可能是 TA 的老工单”勾选区（推荐默认不勾），勾了已认领链接时选择沿用
// 访问码或生成新码；底部固定摘要栏一次确认。建立后结果页复制主页文案
// （与访客详情访问标签同一套），生成的新码只显示一次。
import { useEffect, useMemo, useRef, useState } from 'react'
import { ApiError } from '../api'
import {
  assignGuestLinks,
  copyGuestHomeText,
  createGuest,
  fetchGuests,
  fetchUnassignedGuestLinks,
  type GuestAccessCodeChoiceBody,
  type GuestLinkAssignmentResult,
  type GuestSummary,
  type UnassignedGuestLink,
} from './memberApi'
import { similarGuests } from './guestSimilarity'
import { GuestAccessCodeCard } from './GuestAccessCodeCard'
import { GuestAssignConsequence } from './GuestAssignConsequence'
import { GuestLegacyLinkPicker } from './GuestLegacyLinkPicker'
import { guestCreatedAssignmentFailed } from './guestAssignmentFailure'
import { copyText, nav, toast } from './ui'

export function NewGuestPage() {
  const [name, setName] = useState('')
  const [guests, setGuests] = useState<GuestSummary[] | null>(null)
  const [guestsError, setGuestsError] = useState<string | null>(null)
  const [created, setCreated] = useState<GuestSummary | null>(null)
  const [assigned, setAssigned] = useState<GuestLinkAssignmentResult | null>(null)
  const [generatedCode, setGeneratedCode] = useState<string | null>(null)
  const [links, setLinks] = useState<UnassignedGuestLink[] | null>(null)
  const [linksError, setLinksError] = useState<string | null>(null)
  const [selectedIds, setSelectedIds] = useState<number[]>([])
  const [selectedSourceId, setSelectedSourceId] = useState<number | undefined>()
  const [generateSelected, setGenerateSelected] = useState(false)
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

  // 输入名字后（防抖）读无主老链接，带 name 让后端标记“可能是这个人”。
  const linksTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => {
    if (linksTimer.current) clearTimeout(linksTimer.current)
    if (!trimmed) {
      setLinks(null)
      setLinksError(null)
      setSelectedIds([])
      return
    }
    let alive = true
    linksTimer.current = setTimeout(() => {
      fetchUnassignedGuestLinks({ name: trimmed })
        .then((got) => {
          if (!alive) return
          setLinks(got)
          setLinksError(null)
        })
        .catch(() => {
          if (!alive) return
          setLinks(null)
          setLinksError('读取可能是 TA 的老工单失败；可先建立访客，之后在详情页归入')
        })
    }, 300)
    return () => {
      alive = false
      if (linksTimer.current) clearTimeout(linksTimer.current)
    }
  }, [trimmed])

  // 名字变了会重新拉列表；不再返回的链接从勾选里去掉。
  useEffect(() => {
    if (!links) return
    setSelectedIds((current) => current.filter((id) => links.some((link) => link.id === id)))
  }, [links])

  const selectedLinks = useMemo(
    () => (links ?? []).filter((link) => selectedIds.includes(link.id)),
    [links, selectedIds],
  )
  const claimedSelectedLinks = useMemo(
    () => selectedLinks.filter((link) => link.claimed),
    [selectedLinks],
  )
  const requiresChoice = claimedSelectedLinks.length > 0

  // 默认沿用最近使用过的已认领链接（与批量归属页同一规则）。
  const latestSourceId = useMemo(() => {
    const sorted = [...claimedSelectedLinks].sort((a, b) => {
      const at = a.last_used_at ? Date.parse(a.last_used_at) : 0
      const bt = b.last_used_at ? Date.parse(b.last_used_at) : 0
      if (bt !== at) return bt - at
      return b.id - a.id
    })
    return sorted[0]?.id
  }, [claimedSelectedLinks])

  useEffect(() => {
    if (!claimedSelectedLinks.length) {
      setSelectedSourceId(undefined)
      setGenerateSelected(false)
      return
    }
    if (generateSelected) return
    if (!selectedSourceId || !claimedSelectedLinks.some((link) => link.id === selectedSourceId)) {
      setSelectedSourceId(latestSourceId)
    }
  }, [claimedSelectedLinks, generateSelected, latestSourceId, selectedSourceId])

  const selectedSource = claimedSelectedLinks.find((link) => link.id === selectedSourceId)
  const accessCodeChoice: GuestAccessCodeChoiceBody | undefined = requiresChoice
    ? generateSelected
      ? ('generate' as const)
      : selectedSourceId
        ? ({ source_link_id: selectedSourceId } as const)
        : undefined
    : undefined
  const disableSubmit =
    !trimmed || busy || (requiresChoice && !generateSelected && !selectedSourceId)

  const summaryMiddle = selectedIds.length ? `归入 ${selectedIds.length} 张工单` : '不归入老工单'
  const summaryAccess = !selectedIds.length
    ? ''
    : ` · ${
        generateSelected
          ? '生成新的访问码'
          : !requiresChoice
            ? '不用选访问码'
            : selectedSource
              ? `沿用 ${selectedSource.ticket_number} 的访问码`
              : '选择访问码'
      }`

  async function submit() {
    if (!trimmed || busy || disableSubmit) return
    setBusy(true)
    setError(null)
    try {
      const { guest } = await createGuest(trimmed)
      if (selectedIds.length) {
        try {
          const result = await assignGuestLinks(guest.id, selectedIds, accessCodeChoice)
          window.dispatchEvent(new Event('tb:invalidate'))
          setAssigned(result)
          if (result.access_code) setGeneratedCode(result.access_code)
        } catch (e) {
          guestCreatedAssignmentFailed(guest.id, guest.name, e)
          return
        }
      }
      setCreated(guest)
      toast(
        selectedIds.length
          ? `✓ 已建立访客「${guest.name}」并归入 ${selectedIds.length} 张工单`
          : `✓ 已建立访客「${guest.name}」`,
      )
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
          {assigned ? (
            <div className="hint">
              {assigned.access_code_action === 'remain_unclaimed' ? '未认领' : '已认领'} · 已归入{' '}
              {assigned.link_count} 张工单
              {assigned.access_code_action === 'reuse_source_link' && '（沿用了所选老链接的访问码）'}
              {assigned.access_code_action === 'remain_unclaimed' &&
                '；TA 第一次打开主页或链接时再设置新访问码'}
              。
            </div>
          ) : (
            <div className="hint">
              未认领 · {created.link_count} 条访客链接；名下有有效链接后，TA 的访客主页会列出这些工单。
            </div>
          )}
          {generatedCode && (
            <div
              className="reset-result-box reset-code-box"
              role="region"
              aria-label="新访问码只显示一次"
            >
              <div className="reset-result-label">新访问码</div>
              <p className="reset-code" data-testid="guest-new-access-code">
                {generatedCode}
              </p>
              <p className="hint">只显示这一次，请立即复制并发给对方；离开本页后平台不会再返回它。</p>
            </div>
          )}
          <div className="guest-new-home">
            <strong>访客主页文案</strong>
            <div className="hint">发给访客本人；文案含主页地址，不含访问码，可重复复制。</div>
            <div className="actrow">
              <button className="btn" onClick={() => void copyHome()}>
                复制主页文案
              </button>
              <a
                className="btn ghost"
                href={`/tickets/new?guest=${created.id}`}
                onClick={(e) => {
                  e.preventDefault()
                  nav(`/tickets/new?guest=${created.id}`)
                }}
              >
                给 TA 发新工单
              </a>
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
      </section>

      {trimmed && (
        <section className="mcard" role="region" aria-label="可能是 TA 的老工单">
          <div className="sec-t" style={{ marginTop: 0 }}>
            可能是 TA 的老工单
          </div>
          <div className="hint" style={{ marginBottom: 10 }}>
            名字相近的排前面且带标记，默认不自动勾选；可展开其他老工单并搜索。
          </div>
          {linksError && <div className="hint">{linksError}</div>}
          {links === null && !linksError && <div className="hint">正在读取无主老链接…</div>}
          {links !== null && (
            <GuestLegacyLinkPicker
              links={links}
              selectedIds={selectedIds}
              onToggle={(linkId) =>
                setSelectedIds((current) =>
                  current.includes(linkId)
                    ? current.filter((item) => item !== linkId)
                    : [...current, linkId],
                )
              }
            />
          )}
        </section>
      )}

      {selectedIds.length > 0 && (
        <section className="mcard">
          <GuestAccessCodeCard
            mode={requiresChoice ? 'choice_required' : 'remain_unclaimed'}
            guestName={trimmed}
            sourceLinks={claimedSelectedLinks}
            selectedSourceId={selectedSourceId}
            generateSelected={generateSelected}
            onSelectSource={(sourceId) => {
              setGenerateSelected(false)
              setSelectedSourceId(sourceId)
            }}
            onSelectGenerate={() => {
              setGenerateSelected(true)
              setSelectedSourceId(undefined)
            }}
          />
          <GuestAssignConsequence
            guestName={trimmed}
            guestClaimed={false}
            selectedCount={selectedIds.length}
            claimedCount={claimedSelectedLinks.length}
            sourceLinks={claimedSelectedLinks}
            selectedSourceId={selectedSourceId}
            generateSelected={generateSelected}
          />
        </section>
      )}

      {error && <div className="ecard">{error}</div>}
      <div className="guest-new-summary" data-testid="guest-new-summary">
        <div className="guest-new-summary-text">
          新建访客『{trimmed || '…'}』 · {summaryMiddle}
          {summaryAccess}
        </div>
        <button type="button" className="btn big" disabled={disableSubmit} onClick={() => void submit()}>
          {busy ? '正在建立…' : '建立访客'}
        </button>
      </div>
    </div>
  )
}
