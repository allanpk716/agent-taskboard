// 访客选择器（#328）：建访客链接时从部门访客名单里按名字搜索选已有访客，
// 或当场新建一位并命名。二选一：填了新名字就按新建，否则必须选中一位已有访客。
import { useEffect, useState } from 'react'
import { fetchGuests, type GuestSummary } from './memberApi'

export type GuestSelection =
  | { kind: 'existing'; id: number; name: string }
  | { kind: 'new'; name: string }

export function guestSelectionBody(guest: GuestSelection): { guest_id: number } | { new_guest_name: string } {
  return guest.kind === 'existing' ? { guest_id: guest.id } : { new_guest_name: guest.name }
}

export function GuestPicker({
  value,
  onChange,
  onError,
}: {
  value: GuestSelection | null
  onChange: (guest: GuestSelection | null) => void
  onError?: (message: string) => void
}) {
  const [query, setQuery] = useState('')
  const [guests, setGuests] = useState<GuestSummary[] | null>(null)

  useEffect(() => {
    let alive = true
    fetchGuests(query)
      .then((list) => {
        if (alive) setGuests(list)
      })
      .catch(() => {
        if (alive) {
          setGuests([])
          onError?.('读取部门访客名单失败，可直接在下方新建访客')
        }
      })
    return () => {
      alive = false
    }
  }, [query, onError])

  const selected = value?.kind === 'existing' ? value : null
  const newName = value?.kind === 'new' ? value.name : ''
  const candidates = (guests ?? []).filter((g) => (selected && g.id === selected.id ? true : g.name.includes(query)))

  return (
    <div className="fld">
      <label>
        访客<i className="req">必填</i>
      </label>
      <div className="inrow">
        <input
          aria-label="搜索访客"
          type="search"
          placeholder="按名字搜索已有访客"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <select
          className="sel"
          aria-label="选择访客"
          value={selected ? String(selected.id) : ''}
          onChange={(e) => {
            const id = Number(e.target.value)
            const found = candidates.find((g) => g.id === id)
            onChange(found ? { kind: 'existing', id: found.id, name: found.name } : null)
          }}
        >
          <option value="">
            {candidates.length === 0 ? '没有匹配的访客，可在下方新建' : '— 选择访客 —'}
          </option>
          {candidates.map((g) => (
            <option key={g.id} value={g.id}>
              {g.name}
              {g.claim_name ? `（已认领 · ${g.claim_name}）` : g.claimed_at ? '（已认领）' : ''}
            </option>
          ))}
        </select>
      </div>
      <div className="inrow top-gap">
        <input
          aria-label="新建访客名字"
          type="text"
          placeholder="或新建一位访客，如：检验科-张三"
          value={newName}
          onChange={(e) => {
            const name = e.target.value.trim()
            onChange(name ? { kind: 'new', name } : null)
          }}
        />
      </div>
      <div className="hint">
        {value?.kind === 'new'
          ? `将新建访客「${value.name}」；同一个人的工单会归在一起`
          : '人名记在访客上，链接备注只写用途；同一访客名下的链接一码通行'}
      </div>
    </div>
  )
}
