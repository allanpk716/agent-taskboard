import { useEffect, useMemo, useState } from 'react'
import { api } from '../api'
import type { Dictionary } from './memberApi'
import { toQuery, type ListFilters } from './TicketsPage'

export type LabelConditionMode = 'include' | 'exclude'

/** #106 复用：点一个标签 chip 时，在 URL 筛选里加入或移除对应条件。 */
export function toggleLabelCondition(
  filters: ListFilters,
  label: string,
  mode: LabelConditionMode = 'include',
): Partial<ListFilters> {
  const dimension = label.includes(':') ? label.split(':', 1)[0] : ''
  if (mode === 'include') {
    return {
      label: dimension ? filters.label.filter((l) => l !== label) : toggleIn(filters.label, label),
      labelAny: dimension ? toggleIn(filters.labelAny, label) : filters.labelAny,
      noLabel: filters.noLabel.filter((l) => l !== label),
    }
  }
  return {
    label: filters.label.filter((l) => l !== label),
    labelAny: filters.labelAny.filter((l) => l !== label),
    noLabel: toggleIn(filters.noLabel, label),
  }
}

function toggleIn(values: string[], value: string): string[] {
  return values.includes(value) ? values.filter((v) => v !== value) : [...values, value]
}

interface CountsResponse {
  groups: { value: string; count: number }[]
}

interface FacetGroup {
  name: string
  labels: string[]
  aliases: Record<string, string[]>
}

export function LabelFacetPanel({
  filters,
  variant,
  onApply,
  onClose,
}: {
  filters: ListFilters
  variant: 'overlay' | 'drawer'
  onApply: (patch: Partial<ListFilters>) => void
  onClose: () => void
}) {
  const [dictionary, setDictionary] = useState<Dictionary | null>(null)
  const [draft, setDraft] = useState<ListFilters>(filters)
  const [counts, setCounts] = useState<Record<string, Record<string, number>>>({})
  const [search, setSearch] = useState('')
  const [openGroups, setOpenGroups] = useState<Record<string, boolean>>({})
  const [expandedGroups, setExpandedGroups] = useState<Record<string, boolean>>({})
  const [previewTotal, setPreviewTotal] = useState<number | null>(null)

  useEffect(() => setDraft(filters), [filters])
  useEffect(() => {
    void api('/api/v1/labels')
      .then((body: Dictionary) => setDictionary(body))
      .catch(() => setDictionary({ dimensions: [], plain: [] }))
  }, [])

  const groups = useMemo<FacetGroup[]>(() => {
    if (!dictionary) return []
    const result = dictionary.dimensions.map((dim) => ({
      name: dim.name,
      labels: dim.values.map((v) => `${dim.name}:${v.value}`),
      aliases: Object.fromEntries(dim.values.map((v) => [`${dim.name}:${v.value}`, v.aliases])),
    }))
    result.push({
      name: '其他标签',
      labels: dictionary.plain ?? [],
      aliases: Object.fromEntries((dictionary.plain ?? []).map((l) => [l, []])),
    })
    return result
  }, [dictionary])

  useEffect(() => {
    if (!groups.length) return
    let alive = true
    void Promise.all(
      groups.map(async (group) => {
        const body = await api(
          `/api/v1/tickets/counts?group_by=${encodeURIComponent(group.name === '其他标签' ? 'undimensioned' : group.name)}&${labelQuery(draft)}`,
        ) as CountsResponse
        return [group.name, Object.fromEntries(body.groups.map((g) => [g.value, g.count]))] as const
      }),
    )
      .then((entries) => {
        if (alive) setCounts(Object.fromEntries(entries))
      })
      .catch(() => {
        if (alive) setCounts({})
      })
    return () => {
      alive = false
    }
  }, [groups, draft])

  useEffect(() => {
    let alive = true
    void api(`/api/v1/tickets?${labelQuery(draft)}&limit=1`)
      .then((body: { total: number }) => {
        if (alive) setPreviewTotal(body.total)
      })
      .catch(() => {
        if (alive) setPreviewTotal(null)
      })
    return () => {
      alive = false
    }
  }, [draft])

  const selectedCount = draft.label.length + draft.labelAny.length + draft.noLabel.length
  const query = search.trim().toLowerCase()

  function toggle(label: string, mode: LabelConditionMode) {
    setDraft((current) => ({ ...current, ...toggleLabelCondition(current, label, mode) }))
  }

  return (
    <section
      className={`label-facet-panel ${variant}`}
      data-testid="label-facet-panel"
      aria-label="标签筛选"
    >
      <header>
        <h3>标签筛选 · 同维度多选取或</h3>
        <button type="button" onClick={onClose} aria-label="关闭标签筛选">
          ×
        </button>
      </header>
      <div className="label-facet-search">
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="搜索维度、值或别名"
          aria-label="搜索标签"
        />
      </div>
      <div className="label-facet-body">
        {!groups.length && <p className="hint">正在读取标签…</p>}
        {groups.map((group) => {
          const hit = (label: string) =>
            !query ||
            group.name.toLowerCase().includes(query) ||
            label.toLowerCase().includes(query) ||
            (group.aliases[label] ?? []).some((a) => a.toLowerCase().includes(query))
          const hits = group.labels.filter(hit)
          if (query && !hits.length && !group.name.toLowerCase().includes(query)) return null
          const groupCounts = counts[group.name] ?? {}
          const ranked = hits
            .map((label) => ({
              label,
              count: groupCounts[group.name === '其他标签' ? label : label.slice(group.name.length + 1)] ?? 0,
            }))
            .sort((a, b) => b.count - a.count || a.label.localeCompare(b.label))
          const visible = query || expandedGroups[group.name] ? ranked : ranked.slice(0, 5)
          const open = openGroups[group.name] !== false
          return (
            <section key={group.name} className="label-facet-group">
              <button
                type="button"
                onClick={() => setOpenGroups({ ...openGroups, [group.name]: !open })}
                aria-expanded={open}
              >
                {open ? '▾' : '▸'} {group.name}
                <span>{group.labels.length} 个值</span>
              </button>
              {open && visible.map(({ label, count }) => {
                const value = group.name === '其他标签' ? label : label.slice(group.name.length + 1)
                const included = draft.label.includes(label) || draft.labelAny.includes(label)
                const excluded = draft.noLabel.includes(label)
                return (
                  <div key={label} className={`label-facet-value${count === 0 ? ' zero' : ''}`}>
                    <label>
                      <input
                        type="checkbox"
                        checked={included}
                        aria-label={value}
                        onChange={() => toggle(label, 'include')}
                      />
                      <span>{value}</span>
                      <span>{count}</span>
                    </label>
                    <button
                      type="button"
                      className={excluded ? 'on' : undefined}
                      aria-pressed={excluded}
                      aria-label={`不含 ${value}`}
                      onClick={() => toggle(label, 'exclude')}
                    >
                      不含
                    </button>
                  </div>
                )
              })}
              {open && !query && ranked.length > 5 && (
                <button
                  type="button"
                  className="label-facet-expand"
                  onClick={() => setExpandedGroups({ ...expandedGroups, [group.name]: true })}
                >
                  展开全部 {ranked.length} 个
                </button>
              )}
              {open && !visible.length && <p className="hint">没有匹配的值</p>}
            </section>
          )
        })}
      </div>
      <footer>
        <button
          type="button"
          className="btn ghost sm"
          onClick={() =>
            setDraft({ ...draft, label: [], labelAny: [], noLabel: [] })
          }
        >
          清空
        </button>
        <span>已选 {selectedCount} 个条件</span>
        <button type="button" className="btn sm" onClick={() => onApply({
          label: draft.label,
          labelAny: draft.labelAny,
          noLabel: draft.noLabel,
        })}>
          显示 {previewTotal ?? '…'} 张
        </button>
      </footer>
    </section>
  )
}

function labelQuery(filters: ListFilters): string {
  return toQuery(filters)
}

export function LabelFilterSummary({
  filters,
  total,
  onChange,
}: {
  filters: ListFilters
  total: number | null
  onChange: (patch: Partial<ListFilters>) => void
}) {
  const [expanded, setExpanded] = useState(false)
  const dimensions = new Map<string, string[]>()
  for (const label of filters.labelAny) {
    const dim = label.split(':', 1)[0]
    dimensions.set(dim, [...(dimensions.get(dim) ?? []), label.slice(dim.length + 1)])
  }
  const includes = [...dimensions].map(([dim, values]) => ({
    key: `any:${dim}`,
    text: `${dim}：${values.join(' 或 ')}`,
    remove: () => onChange({ labelAny: filters.labelAny.filter((l) => !l.startsWith(`${dim}:`)) }),
  }))
  for (const label of filters.label) {
    includes.push({
      key: `label:${label}`,
      text: label,
      remove: () => onChange(toggleLabelCondition(filters, label, 'include')),
    })
  }
  const excludes = filters.noLabel.map((label) => ({
    key: `no:${label}`,
    text: `不含 ${label}`,
    remove: () => onChange(toggleLabelCondition(filters, label, 'exclude')),
  }))
  const chips = [...includes, ...excludes]
  if (!chips.length) return null
  const words = [
    ...[...dimensions].map(([dim, values]) => `${dim}是 ${values.join(' 或 ')}`),
    ...filters.label.map((label) => `标签是 ${label}`),
    ...filters.noLabel.map((label) => `不含 ${label}`),
  ].join('，且 ')
  return (
    <div className="label-filter-summary" aria-label="已选标签条件">
      <div className={expanded ? 'expanded' : undefined}>
        {chips.map((chip) => (
          <button
            key={chip.key}
            type="button"
            className={chip.text.startsWith('不含') ? 'exclude' : undefined}
            onClick={chip.remove}
          >
            <span>{chip.text}</span> ×
          </button>
        ))}
      </div>
      {chips.length > 3 && (
        <button type="button" className="label-filter-more" onClick={() => setExpanded(!expanded)}>
          {expanded ? '收起' : `+${chips.length - 3}`}
        </button>
      )}
      <button type="button" onClick={() => onChange({ label: [], labelAny: [], noLabel: [] })}>
        清空
      </button>
      <p>{words} · 共 {total ?? '…'} 张</p>
    </div>
  )
}
