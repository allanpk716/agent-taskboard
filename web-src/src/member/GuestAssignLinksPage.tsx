import { useEffect, useMemo, useState } from 'react'
import { ApiError } from '../api'
import {
  assignGuestLinks,
  fetchGuestDetail,
  fetchUnassignedGuestLinks,
  previewGuestLinkAssignment,
  type GuestLinkAssignmentPreview,
  type UnassignedGuestLink,
} from './memberApi'
import { GuestAccessCodeCard } from './GuestAccessCodeCard'
import { GuestAssignConsequence } from './GuestAssignConsequence'
import { GuestLegacyLinkPicker } from './GuestLegacyLinkPicker'
import { nav, toast } from './ui'

/** 访客详情的全屏批量归属页：多选无主老链接，确认访问码后果后一次归入。 */
export function GuestAssignLinksPage({ guestId }: { guestId: string }) {
  const id = Number(guestId)
  const [guestName, setGuestName] = useState('')
  const [guestClaimed, setGuestClaimed] = useState(false)
  const [links, setLinks] = useState<UnassignedGuestLink[] | null>(null)
  const [selectedIds, setSelectedIds] = useState<number[]>([])
  const [preview, setPreview] = useState<GuestLinkAssignmentPreview | null>(null)
  const [previewError, setPreviewError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)

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

  const selectedLinks = useMemo(
    () => (links ?? []).filter((link) => selectedIds.includes(link.id)),
    [links, selectedIds],
  )

  useEffect(() => {
    if (!selectedIds.length) {
      setPreview(null)
      setPreviewError(null)
      return
    }
    let alive = true
    previewGuestLinkAssignment(id, selectedIds)
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
  }, [id, selectedIds])

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
  const claimedCount =
    preview?.claimed_link_count ?? selectedLinks.filter((link) => link.claimed).length
  const disableSubmit =
    busy || selectedIds.length === 0 || !!previewError || accessMode === 'choice_required'

  async function submit() {
    setBusy(true)
    try {
      const result = await assignGuestLinks(id, selectedIds)
      toast(`✓ 已归入 ${result.link_count} 张工单；${accessResultText(result.access_code_action)}`)
      window.dispatchEvent(new Event('tb:invalidate'))
      nav(`/guests/${id}`)
    } catch (e) {
      toast(e instanceof ApiError ? `${e.code}：${e.message}` : '批量归属失败')
    } finally {
      setBusy(false)
    }
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
        mode={accessMode}
        guestName={guestName}
        sourceLinks={selectedLinks.filter((link) => link.claimed)}
      />
      <GuestAssignConsequence
        guestName={guestName}
        guestClaimed={guestClaimed}
        selectedCount={selectedIds.length}
        claimedCount={claimedCount}
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

function accessResultText(action: 'keep_guest' | 'remain_unclaimed'): string {
  if (action === 'keep_guest') return '保留了 TA 的访问码'
  return '访客保持未认领'
}
