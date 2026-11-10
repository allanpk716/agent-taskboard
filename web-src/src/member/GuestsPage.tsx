// 访客管理（#332）：部门访客名单，按名字搜索后进入详情。
// 详情页（GuestDetailPage）承担改名、停用/恢复与复制主页文案等管理操作。
import { useEffect, useState } from 'react'
import { ApiError } from '../api'
import { fetchGuests, type GuestSummary } from './memberApi'
import { nav } from './ui'

export function GuestsPage() {
  const [query, setQuery] = useState('')
  const [guests, setGuests] = useState<GuestSummary[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let alive = true
    fetchGuests(query)
      .then((list) => {
        if (alive) setGuests(list)
      })
      .catch((e) => {
        if (alive) setError(e instanceof ApiError ? e.message : '读取部门访客名单失败')
      })
    return () => {
      alive = false
    }
  }, [query])

  return (
    <div className="bpage">
      <div className="mcard" style={{ marginTop: 12 }}>
        <div className="sec-t" style={{ marginTop: 0 }}>
          访客管理 <span className="hint">{guests?.length ?? '…'} 位访客</span>
        </div>
        <div className="hint">
          部门共享的访客名单；查看详情时只列出你可见范围内的工单和链接。人名记在访客上，
          建链接时选择或新建访客。
        </div>
        <div className="inrow" style={{ marginTop: 10 }}>
          <input
            type="search"
            aria-label="搜索访客"
            placeholder="按名字搜索，如：检验科-张三"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
      </div>
      {error && <div className="ecard">{error}</div>}
      {guests !== null && guests.length === 0 && (
        <div className="mcard">
          <div className="hint">
            {query ? `没有名字包含「${query}」的访客；建访客链接时可当场新建一位。` : '还没有访客；建访客链接时会自动建立。'}
          </div>
        </div>
      )}
      {guests?.map((g) => (
        <div className="mcard guest-row" key={g.id}>
          <a
            className="guest-row-main"
            href={`/guests/${g.id}`}
            onClick={(e) => {
              e.preventDefault()
              nav(`/guests/${g.id}`)
            }}
          >
            <strong>{g.name}</strong>
            <span className="hint">
              {g.disabled_at ? '已停用' : g.claimed_at ? `已认领${g.claim_name ? ` · ${g.claim_name}` : ''}` : '未认领'}
              {' · '}
              {g.link_count} 条链接
            </span>
          </a>
          {g.disabled_at && <span className="badge closed">停用</span>}
          {g.access_code_locked_at && !g.disabled_at && <span className="badge myturn">访问码锁定</span>}
        </div>
      ))}
    </div>
  )
}
