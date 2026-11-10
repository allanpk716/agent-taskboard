// 访客名单（#345）：通讯录式一级入口。顶部搜索，最近联系的访客横排，
// 下面按部门分组（部门取名字里第一个“-”前的部分，没有则归“未分组”），
// 每行显示待处理数（轮到访客的工单数）；浮动「＋」提供新建访客与给访客
// 发新工单两个入口。详情页（GuestDetailPage）承担改名、停用/恢复等管理操作。
import { useEffect, useMemo, useState } from 'react'
import { ApiError } from '../api'
import { fmtShort } from '../shared/format'
import { fetchGuests, type GuestSummary } from './memberApi'
import { Avatar, guestDept, guestShortName } from './guestDisplay'
import { nav } from './ui'

function guestStatusBadges(g: GuestSummary) {
  return (
    <>
      {g.disabled_at && <span className="badge closed">停用</span>}
      {g.access_code_locked_at && !g.disabled_at && <span className="badge myturn">访问码锁定</span>}
    </>
  )
}

export function GuestsPage() {
  const [query, setQuery] = useState('')
  const [guests, setGuests] = useState<GuestSummary[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [fabOpen, setFabOpen] = useState(false)

  useEffect(() => {
    let alive = true
    fetchGuests(query.trim())
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

  const recent = useMemo(
    () =>
      (guests ?? [])
        .filter((g) => g.last_contact_at && !g.disabled_at)
        .sort((a, b) => (a.last_contact_at! < b.last_contact_at! ? 1 : -1))
        .slice(0, 8),
    [guests],
  )
  const groups = useMemo(() => {
    const byDept = new Map<string, GuestSummary[]>()
    for (const g of guests ?? []) {
      const dept = guestDept(g.name)
      byDept.set(dept, [...(byDept.get(dept) ?? []), g])
    }
    return [...byDept.entries()]
      .sort(([a], [b]) => {
        if (a === '未分组') return 1
        if (b === '未分组') return -1
        return a.localeCompare(b, 'zh-Hans-CN')
      })
      .map(([dept, list]) => ({ dept, list }))
  }, [guests])

  return (
    <div className="bpage guests-page">
      <div className="guest-search">
        <input
          type="search"
          aria-label="搜索访客"
          placeholder="搜索访客名字、部门"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      </div>
      {error && <div className="ecard">{error}</div>}
      {guests === null && !error && <div className="mcard"><div className="hint">读取部门访客名单…</div></div>}
      {guests !== null && (
        <>
          <section className="mcard guest-recent" aria-label="最近联系">
            <div className="sec-t" style={{ marginTop: 0 }}>
              最近联系 <span className="hint">{recent.length} 位</span>
            </div>
            {recent.length > 0 ? (
              <div className="guest-recent-scroll">
                {recent.map((g) => (
                  <a className="guest-recent-item" key={g.id} href={`/guests/${g.id}`} title={g.name}
                    onClick={(e) => {
                      e.preventDefault()
                      nav(`/guests/${g.id}`)
                    }}
                  >
                    <Avatar guest={g} size="md" />
                    <span>{guestShortName(g.name)}</span>
                  </a>
                ))}
              </div>
            ) : (
              <div className="hint">最近还没有访客打开过工单；给访客发新工单后，TA 打开会出现在这里。</div>
            )}
          </section>
          {groups.map(({ dept, list }) => (
            <section className="mcard guest-group" role="group" aria-label={dept} key={dept}>
              <div className="guest-group-title">
                <span>{dept}</span>
                <span className="hint">{list.length} 位</span>
              </div>
              {list.map((g) => (
                <a
                  className="guest-row contact"
                  key={g.id}
                  href={`/guests/${g.id}`}
                  onClick={(e) => {
                    e.preventDefault()
                    nav(`/guests/${g.id}`)
                  }}
                >
                  <Avatar guest={g} size="sm" />
                  <span className="guest-row-main">
                    <strong>
                      {g.name}
                      {guestStatusBadges(g)}
                    </strong>
                    <span className="hint">
                      {g.disabled_at ? '已停用' : g.claimed_at ? `已认领${g.claim_name ? ` · ${g.claim_name}` : ''}` : '未认领'}
                      {g.last_contact_at ? ` · 最近联系 ${fmtShort(g.last_contact_at)}` : ''}
                      {' · '}
                      {g.link_count} 条访客链接
                    </span>
                  </span>
                  <span className={`guest-pending${g.pending_count > 0 ? ' hot' : ''}`}>
                    <b>{g.pending_count}</b>
                    <small>待处理</small>
                  </span>
                </a>
              ))}
            </section>
          ))}
          {guests.length === 0 && (
            <div className="mcard">
              <div className="hint">
                {query.trim()
                  ? `没有名字包含「${query.trim()}」的访客；可以点右下角「＋」新建访客。`
                  : '还没有访客；点右下角「＋」可以新建访客，或建访客链接时当场新建。'}
              </div>
            </div>
          )}
        </>
      )}

      {/* 浮动「＋」（#345）：新建访客页由 #339 接上；给访客发新工单进 #340 向导。 */}
      <button
        type="button"
        className="guest-fab"
        aria-label="＋"
        aria-expanded={fabOpen}
        onClick={() => setFabOpen((v) => !v)}
      >
        ＋
      </button>
      {fabOpen && (
        <>
          <div className="guest-fab-scrim" onClick={() => setFabOpen(false)} />
          <div className="guest-fab-sheet" aria-label="访客操作">
            <a
              className="guest-fab-action"
              href="/guests/new"
              onClick={(e) => {
                e.preventDefault()
                nav('/guests/new')
              }}
            >
              <span className="guest-fab-icon" aria-hidden="true">
                👤
              </span>
              <span>
                <b>新建访客</b>
                <small>先建好访客，再给 TA 发工单或归入老工单</small>
              </span>
            </a>
            <a
              className="guest-fab-action"
              href="/tickets/new"
              onClick={(e) => {
                e.preventDefault()
                nav('/tickets/new')
              }}
            >
              <span className="guest-fab-icon" aria-hidden="true">
                ✉️
              </span>
              <span>
                <b>给访客发新工单</b>
                <small>先选已有访客或当场新建，再选模板填写</small>
              </span>
            </a>
          </div>
        </>
      )}
    </div>
  )
}
