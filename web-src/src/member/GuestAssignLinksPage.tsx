import { useEffect, useState } from 'react'
import { ApiError } from '../api'
import {
  fetchGuestDetail,
  fetchUnassignedGuestLinks,
  type UnassignedGuestLink,
} from './memberApi'
import { GuestAccessCodeCard } from './GuestAccessCodeCard'
import { GuestAssignConsequence } from './GuestAssignConsequence'
import { GuestLegacyLinkPicker } from './GuestLegacyLinkPicker'
import { nav, toast } from './ui'
import { accessResultText, useGuestLinkAssignment } from './useGuestLinkAssignment'

/** 访客详情的全屏批量归属页：多选无主老链接，确认访问码后果后一次归入。 */
export function GuestAssignLinksPage({ guestId }: { guestId: string }) {
  const id = Number(guestId)
  const [guestName, setGuestName] = useState('')
  const [guestClaimed, setGuestClaimed] = useState(false)
  const [links, setLinks] = useState<UnassignedGuestLink[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const {
    selectedIds,
    setSelectedIds,
    selectedLinks,
    claimedSelectedLinks,
    requiresChoice,
    selectedSourceId,
    generateSelected,
    selectSource,
    selectGenerate,
    preview,
    previewError,
    busy,
    generatedCode,
    submitAssignment,
  } = useGuestLinkAssignment({
    links,
    guestClaimed,
    preview: { enabled: true, guestID: id },
  })

  useEffect(() => {
    let alive = true
    fetchGuestDetail(id)
      .then((detail) => {
        if (!alive) return
        setGuestName(detail.guest.name)
        setGuestClaimed(!!detail.guest.claimed_at)
        // name 查询会返回全部可见链接，同时给名字相近者打推荐标记。
        return fetchUnassignedGuestLinks({ name: detail.guest.name }).then((links) => {
          if (!alive) return
          setLinks([...links].sort((a, b) => {
            if (a.possible_person !== b.possible_person) return a.possible_person ? -1 : 1
            return b.id - a.id
          }))
        })
      })
      .catch((e) => {
        if (alive) setLoadError(e instanceof ApiError ? e.message : '读取老链接失败')
      })
    return () => {
      alive = false
    }
  }, [id])

  if (loadError) {
    return (
      <div className="bpage">
        <div className="ecard">{loadError}</div>
        <div className="mcard">
          <button className="btn ghost" onClick={() => nav(`/guests/${id}`)}>
            返回访客详情
          </button>
        </div>
      </div>
    )
  }
  if (!links) {
    return (
      <div className="bpage">
        <div className="ecard">加载中…</div>
      </div>
    )
  }

  const accessMode = preview?.access_code_action ?? (guestClaimed ? 'keep_guest' : 'remain_unclaimed')
  const cardMode = generatedCode
    ? 'generate'
    : requiresChoice
      ? 'choice_required'
      : accessMode
  const claimedCount =
    preview?.claimed_link_count ?? selectedLinks.filter((link) => link.claimed).length
  const disableSubmit =
    busy ||
    selectedIds.length === 0 ||
    !!previewError ||
    (requiresChoice && !generateSelected && !selectedSourceId)

  async function submit() {
    try {
      const result = await submitAssignment(id)
      if (!result || result.access_code) return
      toast(`✓ 已归入 ${result.link_count} 张工单；${accessResultText(result.access_code_action)}`)
      window.dispatchEvent(new Event('tb:invalidate'))
      nav(`/guests/${id}`)
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : '批量归属失败')
    }
  }

  if (generatedCode) {
    return (
      <div className="bpage guest-assign-page" data-testid="guest-assign-page">
        <header className="guest-assign-header">
          <button className="btn ghost" onClick={() => nav(`/guests/${id}`)} aria-label="取消归属">
            ‹
          </button>
          <div>
            <h1>新访问码已生成</h1>
            <div className="guest-assign-target">
              <strong>{guestName || '…'}</strong>
              <span className="hint">已认领 · 旧访问码和设备已作废</span>
            </div>
          </div>
        </header>
        <div
          className="mcard reset-result-box reset-code-box"
          role="region"
          aria-label="新访问码只显示一次"
          data-testid="guest-assign-access-result"
        >
          <div className="reset-result-label">新访问码</div>
          <p className="reset-code" data-testid="guest-assign-access-code">
            {generatedCode}
          </p>
          <p className="hint">只显示这一次，请立即复制并发给对方；离开本页后平台不会再返回它。</p>
        </div>
        <div className="guest-assign-footer mcard">
          <div className="hint">确认已保存后再返回访客详情。</div>
          <button className="btn block" onClick={() => nav(`/guests/${id}`)}>
            我已保存，回访客详情
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="bpage guest-assign-page" data-testid="guest-assign-page">
      <header className="guest-assign-header">
        <button className="btn ghost" onClick={() => nav(`/guests/${id}`)} aria-label="取消归属">
          ‹
        </button>
        <div>
          <h1>把老工单归到访客</h1>
          <div className="guest-assign-target">
            <strong>{guestName || '…'}</strong>
            <span className="hint">
              {guestClaimed ? '已认领 · 保留现有访问码' : '未认领 · 按所选链接确认访问码'}
            </span>
          </div>
        </div>
      </header>

      <div className="mcard">
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
      </div>

      <GuestAccessCodeCard
        mode={cardMode}
        guestName={guestName}
        sourceLinks={claimedSelectedLinks}
        selectedSourceId={selectedSourceId}
        generateSelected={generateSelected}
        onSelectSource={selectSource}
        onSelectGenerate={selectGenerate}
      />
      <GuestAssignConsequence
        guestName={guestName}
        guestClaimed={guestClaimed}
        selectedCount={selectedIds.length}
        claimedCount={claimedCount}
        sourceLinks={claimedSelectedLinks}
        selectedSourceId={selectedSourceId}
        generateSelected={generateSelected}
      />
      {previewError && <div className="ecard">{previewError}</div>}

      <div className="guest-assign-footer mcard">
        <div className="hint">
          已勾选 {selectedIds.length} 条 · 不自动勾选，请逐条确认
        </div>
        <button className="btn block" disabled={disableSubmit} onClick={() => void submit()}>
          {selectedIds.length ? `确认归到访客（${selectedIds.length} 条）` : '确认归到访客'}
        </button>
      </div>
    </div>
  )
}
