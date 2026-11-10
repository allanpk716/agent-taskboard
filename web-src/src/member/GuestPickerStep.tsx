// 先选访客再建单（#340）向导第一步：搜索、最近联系、全部访客（按部门分组、
// 显示名下链接数与认领状态），或当场新建一位访客；输入新名字时用
// guestSimilarity 提示已有相似访客，避免建出重复的人。
import { useEffect, useMemo, useState } from 'react'
import { fmtShort } from '../shared/format'
import { fetchGuests, type GuestSummary } from './memberApi'
import { Avatar, guestDept, guestShortName } from './guestDisplay'
import { similarGuests } from './guestSimilarity'

/** 向导已选定的访客：已有访客带认领状态与链接数，用于完成页首次提示。 */
export type WizardGuest =
  | { kind: 'existing'; id: number; name: string; claimed: boolean; linkCount: number }
  | { kind: 'new'; name: string }

export function selectWizardGuest(g: GuestSummary): WizardGuest {
  return {
    kind: 'existing',
    id: g.id,
    name: g.name,
    claimed: !!g.claimed_at,
    linkCount: g.link_count,
  }
}

/** 访客选择列表本体：搜索、最近联系、全部、当场新建与相似提示。
 *  供建单向导（#340）和工单详情「归到访客」面板（#344）共用。 */
export function GuestPickerList({
  onSelect,
  onError,
  newHint = '将随这张工单一起建立「{name}」；同一个人的工单会归在一起。',
}: {
  onSelect: (guest: WizardGuest) => void
  onError?: (message: string) => void
  newHint?: string
}) {
  const [query, setQuery] = useState('')
  const [guests, setGuests] = useState<GuestSummary[] | null>(null)
  const [newName, setNewName] = useState('')

  useEffect(() => {
    let alive = true
    fetchGuests()
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
  }, [onError])

  // 全量名单在浏览器内过滤：搜索时也能保留最近联系之外的全部匹配。
  const available = useMemo(
    () => (guests ?? []).filter((g) => !g.disabled_at && !g.merged_into_guest_id),
    [guests],
  )
  const keyword = query.trim()
  const matched = useMemo(
    () => (keyword ? available.filter((g) => g.name.includes(keyword)) : available),
    [available, keyword],
  )
  const recent = useMemo(
    () =>
      keyword
        ? []
        : available
            .filter((g) => g.last_contact_at)
            .sort((a, b) => (a.last_contact_at! < b.last_contact_at! ? 1 : -1))
            .slice(0, 8),
    [available, keyword],
  )
  const groups = useMemo(() => {
    const byDept = new Map<string, GuestSummary[]>()
    for (const g of matched) {
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
  }, [matched])

  const trimmedNew = newName.trim()
  const similar = useMemo(
    () => (trimmedNew ? similarGuests(trimmedNew, available) : []),
    [trimmedNew, available],
  )

  function statusText(g: GuestSummary): string {
    const claimed = g.claimed_at
      ? `已认领${g.claim_name ? ` · ${g.claim_name}` : ''}`
      : '未认领'
    const contact = g.last_contact_at ? ` · 最近联系 ${fmtShort(g.last_contact_at)}` : ''
    return `${claimed} · ${g.link_count} 条访客链接${contact}`
  }

  return (
    <>
      <div className="guest-search">
        <input
          type="search"
          aria-label="搜索访客"
          placeholder="搜索访客名字、部门"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      </div>
      {guests === null && <div className="ecard">读取部门访客名单…</div>}
      {guests !== null && (
        <>
          {recent.length > 0 && (
            <section className="mcard guest-recent" aria-label="最近联系">
              <div className="sec-t" style={{ marginTop: 0 }}>
                最近联系 <span className="hint">{recent.length} 位</span>
              </div>
              <div className="guest-recent-scroll">
                {recent.map((g) => (
                  <button
                    type="button"
                    className="guest-recent-item"
                    key={g.id}
                    title={g.name}
                    onClick={() => onSelect(selectWizardGuest(g))}
                  >
                    <Avatar guest={g} size="md" />
                    <span>{guestShortName(g.name)}</span>
                  </button>
                ))}
              </div>
            </section>
          )}
          {groups.map(({ dept, list }) => (
            <section className="mcard guest-group" role="group" aria-label={dept} key={dept}>
              <div className="guest-group-title">
                <span>{dept}</span>
                <span className="hint">{list.length} 位</span>
              </div>
              {list.map((g) => (
                <button
                  type="button"
                  className="guest-row contact pick"
                  key={g.id}
                  onClick={() => onSelect(selectWizardGuest(g))}
                >
                  <Avatar guest={g} size="sm" />
                  <span className="guest-row-main">
                    <strong>{g.name}</strong>
                    <span className="hint">{statusText(g)}</span>
                  </span>
                  <span className="guest-pick-go" aria-hidden="true">
                    选 ›
                  </span>
                </button>
              ))}
            </section>
          ))}
          {matched.length === 0 && (
            <div className="mcard">
              <div className="hint">
                {keyword
                  ? `没有名字包含「${keyword}」的访客；可在下方当场新建。`
                  : '还没有访客；可在下方当场新建一位。'}
              </div>
            </div>
          )}
        </>
      )}

      <div className="mcard guest-create">
        <div className="sec-t" style={{ marginTop: 0 }}>
          当场新建
        </div>
        <div className="fld">
          <label htmlFor="guest-pick-new-name">
            新建访客名字<i className="req">必填</i>
          </label>
          <input
            id="guest-pick-new-name"
            aria-label="新建访客名字"
            type="text"
            placeholder="例如：检验科-张三"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
          />
        </div>
        {trimmedNew && similar.length > 0 && (
          <div className="guest-similar" role="region" aria-label="相似访客提示">
            <div className="guest-similar-title">已有名字相同或相似的访客</div>
            {similar.map((g) => (
              <div className="guest-similar-row" key={g.id}>
                <span className="guest-similar-main">
                  <strong>{g.name}</strong>
                  <span className="hint">{statusText(g)}</span>
                </span>
                <button type="button" className="btn ghost" onClick={() => onSelect(selectWizardGuest(g))}>
                  就是 TA
                </button>
              </div>
            ))}
          </div>
        )}
        {trimmedNew && guests !== null && similar.length === 0 && (
          <div className="hint">没有名字相同或相似的访客，可以新建。</div>
        )}
        <div className="guest-new-actions">
          <button
            type="button"
            className="btn big"
            disabled={!trimmedNew}
            onClick={() => trimmedNew && onSelect({ kind: 'new', name: trimmedNew })}
          >
            新建并继续
          </button>
          <div className="hint">{newHint.replace('{name}', trimmedNew || '新访客')}</div>
        </div>
      </div>
    </>
  )
}

export function GuestPickerStep({
  onSelect,
  onBack,
  onError,
}: {
  onSelect: (guest: WizardGuest) => void
  onBack: () => void
  onError?: (message: string) => void
}) {
  return (
    <div className="bpage guest-pick-page">
      <div className="mcard template-screen">
        <button type="button" className="btn ghost back" onClick={onBack}>
          ← 返回
        </button>
        <h2 className="page-title">选访客</h2>
        <span className="badge filler">发链接请人填写</span>
        <p className="guide">先选要发给谁；人名记在访客上，名下链接一码通行。</p>
      </div>
      <GuestPickerList onSelect={onSelect} onError={onError} />
    </div>
  )
}