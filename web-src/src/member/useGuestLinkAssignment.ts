import { useEffect, useMemo, useState } from 'react'
import { ApiError } from '../api'
import {
  assignGuestLinks,
  previewGuestLinkAssignment,
  type GuestAccessCodeAction,
  type GuestAccessCodeChoiceBody,
  type GuestLinkAssignmentPreview,
  type GuestLinkAssignmentResult,
  type UnassignedGuestLink,
} from './memberApi'

interface Options {
  links: UnassignedGuestLink[] | null
  guestClaimed: boolean
  initialSelectedIds?: number[]
  preview?: { enabled: boolean; guestID: number }
}

/** 三处批量归属共用：勾选、默认访问码来源、官方预览与提交结果。 */
export function useGuestLinkAssignment({
  links,
  guestClaimed,
  initialSelectedIds = [],
  preview,
}: Options) {
  const [selectedIds, setSelectedIds] = useState<number[]>(initialSelectedIds)
  const [selectedSourceId, setSelectedSourceId] = useState<number | undefined>()
  const [generateSelected, setGenerateSelected] = useState(false)
  const [previewData, setPreviewData] = useState<GuestLinkAssignmentPreview | null>(null)
  const [previewError, setPreviewError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [lastResult, setLastResult] = useState<GuestLinkAssignmentResult | null>(null)
  const [generatedCode, setGeneratedCode] = useState<string | null>(null)

  // 名字搜索会换掉整份候选列表；已消失的链接不能继续留在提交集合里。
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
  const requiresChoice = !guestClaimed && claimedSelectedLinks.length > 0
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

  const accessCode = useMemo<GuestAccessCodeChoiceBody | undefined>(
    () =>
      requiresChoice
        ? generateSelected
          ? 'generate'
          : selectedSourceId
            ? { source_link_id: selectedSourceId }
            : undefined
        : undefined,
    [requiresChoice, generateSelected, selectedSourceId],
  )

  const previewEnabled = !!preview?.enabled
  const previewGuestID = preview?.guestID ?? 0
  useEffect(() => {
    if (!previewEnabled || !selectedIds.length) {
      setPreviewData(null)
      setPreviewError(null)
      return
    }
    let alive = true
    previewGuestLinkAssignment(previewGuestID, selectedIds, accessCode)
      .then((got) => {
        if (!alive) return
        setPreviewData(got)
        setPreviewError(null)
      })
      .catch((e) => {
        if (!alive) return
        setPreviewData(null)
        setPreviewError(e instanceof ApiError ? `${e.code}：${e.message}` : '读取归属影响失败')
      })
    return () => {
      alive = false
    }
  }, [previewEnabled, previewGuestID, selectedIds, accessCode])

  async function submitAssignment(guestID: number): Promise<GuestLinkAssignmentResult | undefined> {
    if (busy) return undefined
    setBusy(true)
    try {
      const result = await assignGuestLinks(guestID, selectedIds, accessCode)
      setLastResult(result)
      if (result.access_code) setGeneratedCode(result.access_code)
      return result
    } finally {
      setBusy(false)
    }
  }

  return {
    selectedIds,
    setSelectedIds,
    selectedLinks,
    claimedSelectedLinks,
    requiresChoice,
    selectedSourceId,
    generateSelected,
    selectSource(sourceID: number) {
      setGenerateSelected(false)
      setSelectedSourceId(sourceID)
    },
    selectGenerate() {
      setGenerateSelected(true)
      setSelectedSourceId(undefined)
    },
    accessCode,
    preview: previewData,
    previewError,
    busy,
    lastResult,
    generatedCode,
    submitAssignment,
  }
}

export function accessResultText(
  action: Exclude<GuestAccessCodeAction, 'choice_required'>,
): string {
  if (action === 'keep_guest') return '保留了 TA 的访问码'
  if (action === 'reuse_source_link') return '沿用了所选老链接的访问码'
  if (action === 'generate') return '已生成新的访问码'
  return '访客保持未认领'
}
