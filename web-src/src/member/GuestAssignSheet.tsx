// 工单详情「归到访客」底部面板三步（#344）：① 归给谁（搜索/已有/当场
// 新建）；② 顺带勾老工单（当前链接锁定，其余只推荐不自动勾）；③ 确认访问码
// 后果。提交走批量归属接口；新建访客先建人再归属，归属失败不假装成功。
import { useEffect, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import { ApiError } from '../api'
import {
  assignGuestLinks,
  createGuest,
  fetchUnassignedGuestLinks,
  previewGuestLinkAssignment,
  type GuestLink,
  type GuestLinkAssignmentPreview,
  type GuestLinkAssignmentResult,
  type UnassignedGuestLink,
} from './memberApi'
import { GuestAccessCodeCard } from './GuestAccessCodeCard'
import { GuestAssignConsequence } from './GuestAssignConsequence'
import { GuestLegacyLinkPicker } from './GuestLegacyLinkPicker'
import { GuestPickerList, type WizardGuest } from './GuestPickerStep'
import { toast } from './ui'

export function GuestAssignSheet({
  ticketNumber,
  ticketTitle,
  currentLink,
  onClose,
  onAssigned,
}: {
  ticketNumber: string
  ticketTitle: string
  currentLink: GuestLink
  onClose: () => void
  onAssigned: (result: GuestLinkAssignmentResult) => void
}) {
  const [step, setStep] = useState<1 | 2 | 3>(1)
  const [guest, setGuest] = useState<WizardGuest | null>(null)
  const [links, setLinks] = useState<UnassignedGuestLink[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [selectedIds, setSelectedIds] = useState<number[]>([currentLink.id])
  const [preview, setPreview] = useState<GuestLinkAssignmentPreview | null>(null)
  const [previewError, setPreviewError] = useState<string | null>(null)
  const [selectedSourceId, setSelectedSourceId] = useState<number | undefined>()
  const [generateSelected, setGenerateSelected] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [generated, setGenerated] = useState<GuestLinkAssignmentResult | null>(null)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  // 当前链接在无主列表里的形状：接口正常会返回它，这里兜底防止漏掉。
  const current = useMemo<UnassignedGuestLink>(
    () => ({
      id: currentLink.id,
      ticket_number: ticketNumber,
      ticket_title: ticketTitle,
      note: currentLink.note,
      claimed:
        !!currentLink.claimed_at ||
        currentLink.status === 'claimed' ||
        currentLink.status === 'access_code_locked',
      claim_name: currentLink.claim_name ?? '',
      possible_person: false,
    }),
    [
      currentLink.id,
      currentLink.note,
      currentLink.claim_name,
      currentLink.claimed_at,
      currentLink.status,
      ticketNumber,
      ticketTitle,
    ],
  )

  // 第二步的数据源：name 用所选访客名字，让相近老链接排前并带推荐标记。
  const guestName = guest?.name ?? ''
  const guestKey = guest ? (guest.kind === 'existing' ? `e${guest.id}` : `n${guest.name}`) : ''
  useEffect(() => {
    if (!guestKey) return
    let alive = true
    setLinks(null)
    setLoadError(null)
    setSelectedIds([currentLink.id])
    fetchUnassignedGuestLinks({ name: guestName })
      .then((list) => {
        if (!alive) return
        // 当前链接一定在列表里（接口按可见范围返回无主链接）；万一漏了也补上。
        const merged = list.some((link) => link.id === current.id)
          ? list
          : [
              {
                ...current,
              },
              ...list,
            ]
        setLinks(
          [...merged].sort((a, b) => {
            if (a.possible_person !== b.possible_person) return a.possible_person ? -1 : 1
            return b.id - a.id
          }),
        )
      })
      .catch((e) => {
        if (!alive) return
        setLoadError(e instanceof ApiError ? `${e.code}：${e.message}` : '读取老链接失败')
      })
    return () => {
      alive = false
    }
  }, [guestKey, guestName, current])

  const selectedLinks = useMemo(
    () => (links ?? []).filter((link) => selectedIds.includes(link.id)),
    [links, selectedIds],
  )
  const claimedSelectedLinks = useMemo(
    () => selectedLinks.filter((link) => link.claimed),
    [selectedLinks],
  )
  const latestSourceId = useMemo(() => {
    const sorted = [...claimedSelectedLinks].sort((a, b) => {
      const at = a.last_used_at ? Date.parse(a.last_used_at) : 0
      const bt = b.last_used_at ? Date.parse(b.last_used_at) : 0
      if (bt !== at) return bt - at
      return b.id - a.id
    })
    return sorted[0]?.id
  }, [claimedSelectedLinks])

  // 默认沿用最近使用的已认领链接；重新选择来源或生成时让用户主动改。
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

  const guestClaimed = guest?.kind === 'existing' ? (preview?.guest_claimed ?? guest.claimed) : false
  const requiresChoice = !guestClaimed && claimedSelectedLinks.length > 0
  const accessCode = useMemo(
    () =>
      requiresChoice
        ? generateSelected
          ? ('generate' as const)
          : selectedSourceId
            ? ({ source_link_id: selectedSourceId } as const)
            : undefined
        : undefined,
    [requiresChoice, generateSelected, selectedSourceId],
  )

  // 已有访客在第三步拉取官方预览，实时更新后果与设备数；新建访客没有 id，
  // 由共用组件按所选链接推导同样的文案。
  useEffect(() => {
    if (step !== 3 || guest?.kind !== 'existing') return
    let alive = true
    previewGuestLinkAssignment(guest.id, selectedIds, accessCode)
      .then((got) => {
        if (!alive) return
        setPreview(got)
        setPreviewError(null)
      })
      .catch((e) => {
        if (!alive) return
        setPreview(null)
        setPreviewError(e instanceof ApiError ? `${e.code}：${e.message}` : '读取归属影响失败')
      })
    return () => {
      alive = false
    }
  }, [step, guest, selectedIds, accessCode])

  const fallbackMode = guestClaimed ? 'keep_guest' : 'remain_unclaimed'
  const cardMode = requiresChoice
    ? 'choice_required'
    : (preview?.access_code_action ?? fallbackMode)
  const claimedCount = preview?.claimed_link_count ?? claimedSelectedLinks.length
  const disableSubmit =
    busy || !!previewError || (requiresChoice && !generateSelected && !selectedSourceId)

  function selectGuest(next: WizardGuest) {
    setGuest(next)
    setSelectedIds([currentLink.id])
    setPreview(null)
    setPreviewError(null)
    setError(null)
  }

  async function submit() {
    if (!guest || busy) return
    setBusy(true)
    setError(null)
    let guestCreated = false
    try {
      let targetId = guest.kind === 'existing' ? guest.id : 0
      if (guest.kind === 'new') {
        // 先建访客再批量归属；归属失败时人已建好，下面会转成已有访客，重试不会重复建人。
        const created = await createGuest(guest.name)
        targetId = created.guest.id
        guestCreated = true
        setGuest({
          kind: 'existing',
          id: created.guest.id,
          name: created.guest.name,
          claimed: false,
          linkCount: 0,
        })
      }
      const result = await assignGuestLinks(targetId, selectedIds, accessCode)
      onAssigned(result)
      if (result.access_code) {
        setGenerated(result)
        return
      }
      toast(`✓ 已归到「${result.guest_name}」；${accessResultText(result.access_code_action)}`)
      onClose()
    } catch (e) {
      if (guestCreated) {
        setError(
          `访客已建立，但归属失败：${e instanceof ApiError ? `${e.code}：${e.message}` : '请重试'}。可在这里重试，或关闭后到访客详情继续处理。`,
        )
      } else if (guest.kind === 'new') {
        setError(e instanceof ApiError ? `${e.code}：${e.message}` : '建立访客失败，请重试')
      } else {
        setError(e instanceof ApiError ? `${e.code}：${e.message}` : '归属失败，请重试')
      }
    } finally {
      setBusy(false)
    }
  }

  return createPortal(
    <>
      <div className="assign-sheet-scrim" onClick={onClose} />
      <section
        className="assign-sheet"
        role="dialog"
        aria-label="归到访客"
        data-testid="ticket-assign-guest-sheet"
      >
        <header className="assign-sheet-head">
          <h2>归到访客</h2>
          <button type="button" className="assign-sheet-close" aria-label="关闭归到访客面板" onClick={onClose}>
            ×
          </button>
        </header>
        <div className="assign-sheet-steps" aria-hidden="true">
          {[1, 2, 3].map((n) => (
            <span
              key={n}
              className={`assign-step-pill${generated || n < step ? ' done' : n === step ? ' on' : ''}`}
            />
          ))}
        </div>

        {generated ? (
          <div className="assign-sheet-body">
            <h3 className="assign-step-title">新访问码已生成</h3>
            <p className="hint">已归到「{generated.guest_name}」；旧访问码和设备已作废。</p>
            <div
              className="mcard reset-result-box reset-code-box"
              role="region"
              aria-label="新访问码只显示一次"
              data-testid="ticket-assign-access-result"
            >
              <div className="reset-result-label">新访问码</div>
              <p className="reset-code">{generated.access_code}</p>
              <p className="hint">只显示这一次，请立即复制并发给对方；关闭面板后平台不会再返回它。</p>
            </div>
          </div>
        ) : (
          <div className="assign-sheet-body">
            {step === 1 && (
              <section aria-labelledby="assign-step-1">
                <h3 className="assign-step-title" id="assign-step-1">
                  1 · 归给谁
                </h3>
                <p className="hint">当前链接会归给这位访客；同一访客名下的链接共用一个访问码。</p>
                <GuestPickerList
                  onSelect={selectGuest}
                  onError={(message) => setError(message)}
                  newHint="确认归属时会先建立「{name}」，再把这些链接归给 TA。"
                />
                {guest && (
                  <div className="okbox">
                    已选择：{guest.name}
                    {guest.kind === 'new' ? '（当场新建）' : guest.claimed ? ' · 已认领' : ' · 未认领'}
                  </div>
                )}
              </section>
            )}
            {step === 2 && (
              <section aria-labelledby="assign-step-2">
                <h3 className="assign-step-title" id="assign-step-2">
                  2 · TA 可能还有这些老工单
                </h3>
                <p className="hint">当前链接已勾选并锁定；其余只按名字推荐，不会自动勾选。</p>
                {loadError && <div className="ecard">{loadError}</div>}
                {links === null ? (
                  <div className="ecard">读取老链接…</div>
                ) : (
                  <GuestLegacyLinkPicker
                    links={links}
                    selectedIds={selectedIds}
                    lockedIds={[currentLink.id]}
                    pinnedIds={[currentLink.id]}
                    onToggle={(linkId) => {
                      if (linkId === currentLink.id) return
                      setSelectedIds((current) =>
                        current.includes(linkId)
                          ? current.filter((item) => item !== linkId)
                          : [...current, linkId],
                      )
                    }}
                  />
                )}
              </section>
            )}
            {step === 3 && guest && (
              <section aria-labelledby="assign-step-3">
                <h3 className="assign-step-title" id="assign-step-3">
                  3 · 确认归属和访问码
                </h3>
                <p className="hint">
                  共 {selectedIds.length} 条链接将归给「{guest.name}」。
                </p>
                <GuestAccessCodeCard
                  mode={cardMode}
                  guestName={guest.name}
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
                  guestName={guest.name}
                  guestClaimed={guestClaimed}
                  selectedCount={selectedIds.length}
                  claimedCount={claimedCount}
                  sourceLinks={claimedSelectedLinks}
                  selectedSourceId={selectedSourceId}
                  generateSelected={generateSelected}
                />
                {previewError && <div className="ecard">{previewError}</div>}
                {error && <div className="errbox">{error}</div>}
              </section>
            )}
          </div>
        )}

        <footer className="assign-sheet-foot">
          {generated ? (
            <>
              <span className="hint">新码只显示一次</span>
              <span className="sp" />
              <button type="button" className="btn" onClick={onClose}>
                我已保存，完成
              </button>
            </>
          ) : step === 1 ? (
            <>
              <span className="hint">第 1 / 3 步</span>
              <span className="sp" />
              <button type="button" className="btn" disabled={!guest} onClick={() => setStep(2)}>
                下一步
              </button>
            </>
          ) : step === 2 ? (
            <>
              <span className="hint">第 2 / 3 步 · 已勾选 {selectedIds.length} 条</span>
              <button type="button" className="btn ghost" onClick={() => setStep(1)}>
                上一步
              </button>
              <span className="sp" />
              <button
                type="button"
                className="btn"
                disabled={links === null || !selectedIds.length}
                onClick={() => setStep(3)}
              >
                下一步
              </button>
            </>
          ) : (
            <>
              <span className="hint">第 3 / 3 步 · 共 {selectedIds.length} 条</span>
              <button type="button" className="btn ghost" disabled={busy} onClick={() => setStep(2)}>
                上一步
              </button>
              <span className="sp" />
              <button
                type="button"
                className="btn"
                disabled={disableSubmit}
                onClick={() => void submit()}
              >
                {busy ? '正在归属…' : `确认归属（${selectedIds.length} 条）`}
              </button>
            </>
          )}
        </footer>
      </section>
    </>,
    document.body,
  )
}

function accessResultText(action: 'keep_guest' | 'remain_unclaimed' | 'reuse_source_link' | 'generate'): string {
  if (action === 'keep_guest') return '保留了 TA 的访问码'
  if (action === 'reuse_source_link') return '沿用了所选老链接的访问码'
  if (action === 'generate') return '已生成新的访问码'
  return '访客保持未认领'
}
