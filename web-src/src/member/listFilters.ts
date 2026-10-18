/** 工单列表筛选条件与 URL 查询串之间的转换。 */
export interface ListFilters {
  status: 'open' | 'closed' | 'all'
  kind: '' | 'task' | 'report'
  assignee: '' | 'me' | 'none'
  waiting: '' | 'assignee' | 'creator' | 'guest' | 'department'
  actor: '' | 'member' | 'agent'
  q: string
  label: string[]
  labelAny: string[]
  noLabel: string[]
  template: string
  creatorKind: '' | 'me' | 'guest'
  creatorName: string
  timeField: '' | 'created' | 'updated' | 'closed'
  timeAfter: string
  timeBefore: string
}

function shiftDay(dateStr: string, days: number): string {
  const y = parseInt(dateStr.slice(0, 4), 10)
  const m = parseInt(dateStr.slice(5, 7), 10)
  const d = parseInt(dateStr.slice(8, 10), 10)
  const next = new Date(Date.UTC(y, m - 1, d) + days * 86400_000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${next.getUTCFullYear()}-${pad(next.getUTCMonth() + 1)}-${pad(next.getUTCDate())}`
}

export function toQuery(f: ListFilters): string {
  const q = new URLSearchParams()
  q.set('status', f.status)
  if (f.kind) q.set('kind', f.kind)
  if (f.assignee) q.set('assignee', f.assignee)
  if (f.waiting) q.set('waiting_on', f.waiting)
  if (f.actor) q.set('actor_kind', f.actor)
  if (f.q.trim()) q.set('q', f.q.trim())
  for (const label of f.label) if (label.trim()) q.append('label', label.trim())
  for (const label of f.labelAny) q.append('label_any', label)
  for (const label of f.noLabel) q.append('no_label', label)
  if (f.template) q.set('template', f.template)
  if (f.creatorName.trim()) q.set('creator', f.creatorName.trim())
  else if (f.creatorKind) q.set('creator', f.creatorKind)
  if (f.timeField && f.timeAfter) q.set(`${f.timeField}_after`, f.timeAfter)
  if (f.timeField && f.timeBefore) q.set(`${f.timeField}_before`, shiftDay(f.timeBefore, 1))
  return q.toString()
}

export function filtersFromSearch(search: string): ListFilters {
  const q = new URLSearchParams(search)
  const get = <K extends keyof ListFilters>(key: K, fallback: ListFilters[K]) => {
    const value = q.get(String(key))
    return (value === null ? fallback : value) as ListFilters[K]
  }
  const filters: ListFilters = {
    status: (get('status', 'open') || 'all') as ListFilters['status'],
    kind: get('kind', '') as ListFilters['kind'],
    assignee: get('assignee', '') as ListFilters['assignee'],
    waiting: (q.get('waiting_on') ?? '') as ListFilters['waiting'],
    actor: (q.get('actor_kind') ?? '') as ListFilters['actor'],
    q: get('q', ''),
    label: q.getAll('label').filter(Boolean),
    labelAny: q.getAll('label_any').filter(Boolean),
    noLabel: q.getAll('no_label').filter(Boolean),
    template: get('template', ''),
    creatorKind: '',
    creatorName: '',
    timeField: '',
    timeAfter: '',
    timeBefore: '',
  }
  for (const field of ['created', 'updated', 'closed'] as const) {
    const after = q.get(`${field}_after`)
    const before = q.get(`${field}_before`)
    if (after || before) {
      filters.timeField = field
      filters.timeAfter = after ?? ''
      filters.timeBefore = before ? shiftDay(before, -1) : ''
      break
    }
  }
  const creator = q.get('creator') ?? ''
  if (creator === 'me' || creator === 'guest') filters.creatorKind = creator
  else filters.creatorName = creator
  return filters
}

export type LabelConditionMode = 'include' | 'exclude'

function toggleIn(values: string[], value: string): string[] {
  return values.includes(value) ? values.filter((v) => v !== value) : [...values, value]
}

/** 点一个标签 chip 时，在 URL 筛选里加入或移除对应条件。 */
export function toggleLabelCondition(
  filters: ListFilters,
  label: string,
  mode: LabelConditionMode = 'include',
): Partial<ListFilters> {
  const dimension = labelDimension(label)
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

export function labelDimension(label: string): string {
  return label.includes(':') ? label.split(':', 1)[0] : ''
}
