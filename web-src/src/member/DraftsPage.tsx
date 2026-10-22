import { useEffect, useState } from 'react'
import { fetchDrafts } from './memberApi'
import type { DraftView } from '../shared/types'
import { fmtShort } from '../shared/format'
import { nav } from './ui'

const KIND_LABELS: Record<string, string> = {
  create: '建单草稿',
  reply: '回复草稿',
  brief_revision: '说明修订草稿',
}

function draftTitle(d: DraftView): string {
  if (d.kind === 'create') return d.request.body_md.split('\n', 1)[0] || '未命名草稿'
  return d.ticket?.title ?? `#${d.ticket?.seq ?? ''}`
}

function openDraft(d: DraftView) {
  nav(d.preview_url)
}

function DraftRow({ draft }: { draft: DraftView }) {
  const agent = draft.author.agent?.name ?? 'Agent'
  const updatedAt = draft.versions[draft.versions.length - 1]?.at
  return (
    <li className="draft-row">
      <span className="draft-kind">{KIND_LABELS[draft.kind] ?? draft.kind}</span>
      <div>
        <a
          href={draft.preview_url}
          aria-label={`打开 ${draftTitle(draft)}`}
          onClick={(e) => { e.preventDefault(); openDraft(draft) }}
        >
          {draftTitle(draft)}
          {draft.ticket ? ` · ${draft.ticket.number}` : ''}
        </a>
        <div className="hint">
          {agent} · 第 {draft.version} 版{updatedAt ? ` · ${fmtShort(updatedAt)}` : ''}
        </div>
        {draft.new_events > 0 && <span className="draft-changed">工单有 {draft.new_events} 条新变化</span>}
      </div>
      <button type="button" className="btn sm ghost" onClick={() => openDraft(draft)}>
        打开 ›
      </button>
    </li>
  )
}

function DraftGroup({ title, drafts }: { title: string; drafts: DraftView[] }) {
  return (
    <section className="mcard" aria-label={title}>
      <div className="sec-t" style={{ marginTop: 0 }}>{title}</div>
      {drafts.length === 0 ? (
        <div className="hint">暂无{title}</div>
      ) : (
        <ul className="draft-list">
          {drafts.map((d) => <DraftRow key={d.id} draft={d} />)}
        </ul>
      )}
    </section>
  )
}

export function DraftsPage() {
  const [data, setData] = useState<{ count: number; drafts: DraftView[] } | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    fetchDrafts().then(setData).catch(() => setError('读取草稿失败'))
  }, [])
  if (error) return <div className="bpage"><div className="ecard">{error}</div></div>
  if (!data) return <div className="bpage"><div className="ecard">加载中…</div></div>
  const creates = data.drafts.filter((d) => d.kind === 'create')
  const onTickets = data.drafts.filter((d) => d.kind !== 'create')
  return (
    <div className="bpage">
      <div className="page-head">
        <h2>我的草稿</h2>
        <p>只有你能看到 · {data.count} 份待确认</p>
      </div>
      <DraftGroup title="建单草稿" drafts={creates} />
      <DraftGroup title="工单上的草稿" drafts={onTickets} />
    </div>
  )
}
