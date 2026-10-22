import { useEffect, useMemo, useRef, useState } from 'react'
import type { BriefVersion, TicketView } from './types'
import { authorName, fmtDateCN, fmtSize } from './format'
import { Markdown } from './markdown'

type VersionLoader = () => Promise<{ versions: BriefVersion[] }>

type DiffLine = { kind: 'same' | 'delete' | 'add'; old?: string; new?: string; label?: string }
type InlinePart = { text: string; changed: boolean }

function inlineParts(a: string, b: string): InlinePart[] {
  const dp = Array.from({ length: a.length + 1 }, () => new Array(b.length + 1).fill(0))
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1])
    }
  }
  const out: InlinePart[] = []
  const push = (text: string, changed: boolean) => {
    const last = out[out.length - 1]
    if (last && last.changed === changed) last.text += text
    else out.push({ text, changed })
  }
  let i = 0
  let j = 0
  while (i < a.length && j < b.length) {
    if (a[i] === b[j] && dp[i][j] === dp[i + 1][j + 1] + 1) {
      push(a[i], false)
      i++
      j++
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      push(a[i++], true)
    } else {
      j++
    }
  }
  while (i < a.length) push(a[i++], true)
  return out.filter((p) => p.text !== '')
}

function splitVersion(v: BriefVersion, ticket: TicketView, draftingKind: 'reply' | 'brief' | null = null): string[] {
  const fields = ticket.brief.template_fields ?? []
  const values = v.fields ?? {}
  const lines = draftingKind === 'reply' ? [] : [`标题：${v.title}`]
  if (draftingKind !== 'reply' && fields.length > 0) {
    for (const f of fields) lines.push(`${f.title}：${values[f.key] ?? ''}`)
  } else if (draftingKind !== 'reply') {
    lines.push(...v.markdown.split(/\r?\n/))
  }
  if (draftingKind === 'reply') lines.push(...v.markdown.split(/\r?\n/))
  for (const id of v.attachment_ids) {
    const attachment = ticket.attachments.find((a) => a.id === id)
    lines.push(draftingKind === 'reply' && !attachment ? `附件已清除：${id}` : `附件：${attachment?.filename ?? id}`)
  }
  return lines
}

function diffLines(oldLines: string[], newLines: string[]): DiffLine[] {
  // O(n*m) 的 LCS 足够说明快照这种短文本使用，输出保持原顺序。
  const dp = Array.from({ length: oldLines.length + 1 }, () => new Array(newLines.length + 1).fill(0))
  for (let i = oldLines.length - 1; i >= 0; i--) {
    for (let j = newLines.length - 1; j >= 0; j--) {
      dp[i][j] = oldLines[i] === newLines[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1])
    }
  }
  const out: DiffLine[] = []
  let i = 0
  let j = 0
  while (i < oldLines.length && j < newLines.length) {
    if (oldLines[i] === newLines[j]) {
      out.push({ kind: 'same', old: oldLines[i], new: newLines[j] })
      i++
      j++
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      out.push({ kind: 'delete', old: oldLines[i], label: '-' })
      i++
    } else {
      out.push({ kind: 'add', new: newLines[j], label: '+' })
      j++
    }
  }
  while (i < oldLines.length) out.push({ kind: 'delete', old: oldLines[i++], label: '-' })
  while (j < newLines.length) out.push({ kind: 'add', new: newLines[j++], label: '+' })
  return markReplacements(out)
}

function markReplacements(rows: DiffLine[]): DiffLine[] {
  const out = [...rows]
  for (let i = 0; i < out.length; i++) {
    if (out[i].kind !== 'delete') continue
    let j = i + 1
    while (j < out.length && out[j].kind === 'delete') j++
    if (j < out.length && out[j].kind === 'add') {
      let k = j
      while (k < out.length && out[k].kind === 'add') {
        out[k] = { ...out[k], label: '~' }
        k++
      }
      out[i] = { ...out[i], label: '~' }
      out[i].new = out[j].new
      out[j].old = out[i].old
    }
    i = j - 1
  }
  return out
}

function collapsedGroups(rows: DiffLine[]): (DiffLine | { kind: 'gap'; count: number })[] {
  const keep = new Set<number>()
  rows.forEach((row, i) => {
    if (row.kind !== 'same') {
      if (i > 0) keep.add(i - 1)
      keep.add(i)
      if (i + 1 < rows.length) keep.add(i + 1)
    }
  })
  const out: (DiffLine | { kind: 'gap'; count: number })[] = []
  let gap = 0
  rows.forEach((row, i) => {
    if (keep.has(i)) {
      if (gap > 0) {
        out.push({ kind: 'gap', count: gap })
        gap = 0
      }
      out.push(row)
    } else {
      gap++
    }
  })
  if (gap > 0) out.push({ kind: 'gap', count: gap })
  return out
}

function fmtExact(iso: string): string {
  return `${fmtDateCN(iso)} ${iso.slice(11, 19)}`
}

function VersionFull({
  version,
  ticket,
  drafting = false,
}: {
  version: BriefVersion
  ticket: TicketView
  drafting?: boolean
}) {
  const currentIDs = new Set(ticket.brief.attachment_ids)
  const isCurrent = !drafting && version.revision === ticket.brief.revision
  const attachments = version.attachment_ids.map((id) => ({
    id,
    attachment: ticket.attachments.find((a) => a.id === id),
  }))
  return (
    <div className="version-full">
      {!isCurrent && <div className="archived-warning">历史版本 · 不是当前内容</div>}
      <Markdown body={version.markdown} attachments={new Map(ticket.attachments.map((a) => [a.id, a]))} />
      {attachments.length > 0 && (
        <div className="atts">
          {attachments.map(({ id, attachment }) =>
            attachment ? (
              <a key={id} className="att" href={attachment.url} target="_blank" rel="noreferrer">
                📎 {attachment.filename} · {fmtSize(attachment.size)}
                {!currentIDs.has(id) && <span className="att-removed">后来的版本中已移除</span>}
              </a>
            ) : (
              <span key={id} className="att removed">
                📎 附件已清除 · {id}
              </span>
            ),
          )}
        </div>
      )}
    </div>
  )
}

export function BriefVersions({
  ticket,
  loadVersions,
  selected,
  onSelect,
  drafting,
  draftingKind,
}: {
  ticket: TicketView
  loadVersions: VersionLoader
  selected: number | null
  onSelect: (revision: number, shift: boolean) => void
  drafting?: { replyNo: number; author: string; sentAt: string }
  draftingKind?: 'reply' | 'brief'
}) {
  const [versions, setVersions] = useState<BriefVersion[]>([])
  const [rangeOld, setRangeOld] = useState<number | null>(null)
  const anchorRef = useRef<number | null>(null)
  const defaultRangeRef = useRef(true)
  const [view, setView] = useState<'diff' | 'full'>('diff')
  const [layout, setLayout] = useState<'split' | 'merged'>(window.innerWidth < 900 ? 'merged' : 'split')
  const [message, setMessage] = useState('')
  useEffect(() => {
    const show = (ev: Event) => {
      const revision = (ev as CustomEvent<number>).detail
      defaultRangeRef.current = true
      anchorRef.current = null
      setRangeOld(revision > 1 ? revision - 1 : null)
      setMessage('')
      document.querySelector('[data-bubble="brief"]')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
      onSelect(revision, false)
    }
    window.addEventListener('tb:show-brief-version', show)
    return () => window.removeEventListener('tb:show-brief-version', show)
  }, [onSelect])
  useEffect(() => {
    void loadVersions().then((body) => setVersions(body.versions)).catch(() => setMessage('说明版本暂时读不到'))
  }, [loadVersions])

  const byRevision = useMemo(() => new Map(versions.map((v) => [v.revision, v])), [versions])
  const current = selected ? byRevision.get(selected) : undefined
  useEffect(() => {
    if (selected === null) {
      setRangeOld(null)
      anchorRef.current = null
      defaultRangeRef.current = true
      return
    }
    if (selected > 1 && !byRevision.has(selected - 1)) {
      setRangeOld(null)
      setMessage('上一版上线前未存档，无法对比')
      return
    }
    setMessage('')
    if (!defaultRangeRef.current) return
    setRangeOld(selected > 1 ? selected - 1 : null)
  }, [selected, byRevision])
  useEffect(() => {
    const capture = () => {
      const raw = document.querySelector('[data-bubble="brief"] .version-pill.new')?.textContent ?? ''
      if (!raw) return
      const revision = Number(raw.replace(/\D/g, ''))
      anchorRef.current = Math.max(anchorRef.current ?? 0, revision)
    }
    document.addEventListener('pointerdown', capture, true)
    return () => document.removeEventListener('pointerdown', capture, true)
  }, [])

  const oldVersion = rangeOld !== null ? byRevision.get(rangeOld) : undefined
  const firstOnly = current?.revision === 1 && !oldVersion
  const renderCell = (row: DiffLine, side: 'old' | 'new') => {
    const text = row[side] ?? ''
    const other = side === 'old' ? row.new : row.old
    if (other === undefined || row.kind === 'same') return <>{text}</>
    const parts = inlineParts(text, other)
    return (
      <>
        {parts.map((part, i) =>
          part.changed ? (
            <mark className="inline-change" key={i}>{part.text}</mark>
          ) : (
            <span key={i}>{part.text}</span>
          ),
        )}
      </>
    )
  }
  const rows = useMemo(
    () =>
      oldVersion && current
      ? diffLines(splitVersion(oldVersion, ticket, draftingKind ?? null), splitVersion(current, ticket, draftingKind ?? null))
        : [],
    [oldVersion, current, ticket, draftingKind],
  )
  const allRevisions = drafting
    ? versions.map((v) => v.revision)
    : Array.from({ length: ticket.brief.revision }, (_, i) => i + 1)

  return (
    <div className="brief-versions">
      {(drafting ? versions.length > 0 : ticket.brief.revision > 1) && (
        <div className="version-bar" role="group" aria-label={drafting ? '起草过程版本' : '说明版本'}>
          {allRevisions.map((revision) => {
            const saved = byRevision.has(revision)
            return (
              <button
                key={revision}
                type="button"
                className={`version-pill${saved ? '' : ' missing'}${rangeOld === revision ? ' old' : ''}${selected === revision ? ' new' : ''}`}
                data-role={rangeOld === revision ? '旧' : selected === revision ? '新' : ''}
                title={saved ? `${authorName(byRevision.get(revision)!.author)} · ${fmtExact(byRevision.get(revision)!.at)}` : '上线前未存档'}
                disabled={!saved}
                onClick={(e) => {
                  if (!e.shiftKey) anchorRef.current = Math.max(anchorRef.current ?? 0, revision)
                  if (e.shiftKey && anchorRef.current !== null) {
                    defaultRangeRef.current = false
                    setRangeOld(Math.min(anchorRef.current, revision))
                  } else if (!e.shiftKey) {
                    defaultRangeRef.current = true
                    setRangeOld(null)
                  }
                  onSelect(revision, e.shiftKey)
                }}
              >
                v{revision}
              </button>
            )
          })}
        </div>
      )}
      {selected !== null && current && (
        <div className={`brief-compare${drafting ? ' drafting' : ''}`}>
          <div className="diff-context">
            <span className={`object${drafting ? ' drafting' : ''}`}>
              {draftingKind === 'brief'
                ? `📄 说明修订 #${drafting?.replyNo ?? 0} 的起草过程 · ${drafting?.author ?? ''} · ${drafting ? fmtExact(drafting.sentAt) : ''}`
                : drafting
                  ? `💬 回复 #${drafting.replyNo} 的起草过程 · ${drafting.author} · ${fmtExact(drafting.sentAt)}`
                  : '📄 工单说明'}
            </span>
            <strong>{firstOnly ? '第 1 版全文' : `第 ${oldVersion ? rangeOld : current.revision - 1} 版 → 第 ${current.revision} 版`}</strong>
            <span className="actions">
              <button type="button" className={view === 'full' || firstOnly ? 'on' : ''} onClick={() => setView('full')}>只看这一版全文</button>
              {oldVersion && view === 'diff' && (
                <>
                  <button type="button" className={layout === 'split' ? 'on' : ''} onClick={() => setLayout('split')}>并排</button>
                  <button type="button" className={layout === 'merged' ? 'on' : ''} onClick={() => setLayout('merged')}>合并</button>
                </>
              )}
              <button type="button" onClick={() => onSelect(-1, false)}>✕ 退出对比</button>
            </span>
          </div>
          {drafting && (
            <div className="drafting-note">
              {draftingKind === 'brief' ? '发出后不可修改；这里是发出前的草稿各版' : '回复发出后不可修改；这里是发出前的草稿各版'}
            </div>
          )}
          {message && <div className="archived-warning">{message}</div>}
          {view === 'full' || firstOnly ? (
            <VersionFull version={current} ticket={ticket} drafting={!!drafting} />
          ) : oldVersion ? (
            <div className={`diff-table ${layout}`}>
              <div className="diff-heads">
                <span>{oldVersion.revision} · {authorName(oldVersion.author)} · {fmtExact(oldVersion.at)}</span>
                <span>{current.revision} · {authorName(current.author)} · {fmtExact(current.at)}</span>
              </div>
              {collapsedGroups(rows).map((row, i) =>
                row.kind === 'gap' ? (
                  <div className="diff-gap" key={`gap-${i}`}>⋯ 展开 {row.count} 行未改动</div>
                ) : (
                  <div className={`diff-row ${row.kind}`} key={`row-${i}`}>
                    <span className="mark">{row.label ?? ' '}</span>
                    {layout === 'split' ? (
                      <>
                        <span className="diff-cell old">{renderCell(row, 'old')}</span>
                        <span className="diff-cell new">{renderCell(row, 'new')}</span>
                      </>
                    ) : (
                      <span className={`diff-cell ${row.kind === 'delete' ? 'old' : 'new'}`}>
                        {renderCell(row, row.kind === 'delete' ? 'old' : 'new')}
                      </span>
                    )}
                  </div>
                ),
              )}
            </div>
          ) : null}
        </div>
      )}
    </div>
  )
}
