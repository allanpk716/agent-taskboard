import { useMemo, useState } from 'react'
import type { UnassignedGuestLink } from './memberApi'
import { fmtDateCN } from '../shared/format'

interface Props {
  links: UnassignedGuestLink[]
  selectedIds: number[]
  lockedIds?: number[]
  onToggle: (id: number) => void
}

/** 老链接勾选列表：推荐置顶并标记，默认不自动勾选；其余可展开再搜索。 */
export function GuestLegacyLinkPicker({ links, selectedIds, lockedIds = [], onToggle }: Props) {
  const [query, setQuery] = useState('')
  const [othersOpen, setOthersOpen] = useState(false)
  const locked = new Set(lockedIds)
  const selected = new Set(selectedIds)
  const keyword = query.trim().toLowerCase()
  const { likely, others } = useMemo(() => {
    const matched = links.filter((link) => {
      if (!keyword) return true
      return [link.ticket_number, link.ticket_title, link.note, link.claim_name]
        .join(' ')
        .toLowerCase()
        .includes(keyword)
    })
    return {
      likely: matched.filter((link) => link.possible_person),
      others: matched.filter((link) => !link.possible_person),
    }
  }, [keyword, links])

  return (
    <div className="legacy-link-picker">
      <div className="legacy-search">
        <input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="搜索工单号、标题、备注或称呼"
          aria-label="搜索老链接"
        />
      </div>
      <div className="legacy-group-title">
        可能是这个人 <span>名字相近的排前面 · 默认不自动勾选</span>
      </div>
      {likely.map((link) => (
        <LegacyLinkRow
          key={link.id}
          link={link}
          recommended
          checked={selected.has(link.id)}
          locked={locked.has(link.id)}
          onToggle={onToggle}
        />
      ))}
      {likely.length === 0 && <div className="legacy-empty">没有名字相近的老链接。</div>}

      <button
        type="button"
        className="legacy-others-toggle"
        aria-expanded={othersOpen}
        onClick={() => setOthersOpen((open) => !open)}
      >
        其他老链接 <span>{othersOpen ? '收起 ›' : '展开 ›'}</span>
      </button>
      {othersOpen && (
        <div className="legacy-others">
          {others.map((link) => (
            <LegacyLinkRow
              key={link.id}
              link={link}
              checked={selected.has(link.id)}
              locked={locked.has(link.id)}
              onToggle={onToggle}
            />
          ))}
          {others.length === 0 && <div className="legacy-empty">没有匹配的其他老链接。</div>}
        </div>
      )}
    </div>
  )
}

function LegacyLinkRow({
  link,
  recommended,
  checked,
  locked,
  onToggle,
}: {
  link: UnassignedGuestLink
  recommended?: boolean
  checked: boolean
  locked: boolean
  onToggle: (id: number) => void
}) {
  return (
    <label className="legacy-link-row">
      <input
        type="checkbox"
        checked={checked || locked}
        disabled={locked}
        onChange={() => {
          if (!locked) onToggle(link.id)
        }}
      />
      <span className="legacy-link-main">
        <span className="legacy-link-title">
          {link.ticket_number} {link.ticket_title}
        </span>
        <span className="legacy-link-meta">
          备注「{link.note}」· {link.claimed ? `已认领 · 自报称呼「${link.claim_name}」` : '未认领'}
          {link.last_used_at ? ` · 最近使用 ${fmtDateCN(link.last_used_at)}` : ' · 未记录使用'}
        </span>
      </span>
      {recommended && <span className="legacy-likely">可能是这个人</span>}
      {locked && <span className="legacy-locked">已锁定</span>}
    </label>
  )
}
