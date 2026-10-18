// 工单列表（#41）：横向滑动的筛选 chips（状态/类型/指派/等待方/操作方）+
// 关键词搜索 + 卡片；「更多筛选」里是标签（别名可用）、模板、创建人与
// 时间范围（评审 P2-11）。「操作方」用查询接口的 actor_kind（本人/Agent，
// 按创建者署名区分）；翻页跟着响应里的 next 走。
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ApiError } from '../api'
import { closeReasonText, fmtShort } from '../shared/format'
import { api } from '../api'
import { nav, useInvalidate, useLocationSearch } from './ui'
import { LabelFacetPanel, LabelFilterSummary } from './LabelFacets'
import { useLayoutTier } from './useLayoutTier'

export interface ListFilters {
  status: 'open' | 'closed' | 'all'
  kind: '' | 'task' | 'report'
  assignee: '' | 'me' | 'none'
  waiting: '' | 'assignee' | 'creator' | 'guest' | 'department'
  actor: '' | 'member' | 'agent'
  q: string
  // 更多筛选（评审 P2-11）
  label: string[] // 标签（可重复）：维度:值 / 值 / 别名，服务端解析后回显标准值
  labelAny: string[] // 维度:值；同一维度取或（#104）
  noLabel: string[] // 排除条件（可重复）
  template: string // 模板 key
  creatorKind: '' | 'me' | 'guest'
  creatorName: string // 成员姓名（填了优先于 creatorKind）
  timeField: '' | 'created' | 'updated' | 'closed'
  timeAfter: string // YYYY-MM-DD（含当天）
  timeBefore: string // YYYY-MM-DD（含当天；查询用次日 00:00，API 的 before 不含时刻）
}

export interface TicketListItem {
  number: string
  seq: number
  kind: string
  title: string
  status: string
  close_reason?: string | null
  creator: string
  assignee?: string | null
  labels: string[]
  waiting_on: { party: string; member?: string | null; text: string; reason: string }
  updated_at: string
}

interface ListResponse {
  total: number
  count: number
  items: TicketListItem[]
  next: string | null
}

/** 日期字符串（YYYY-MM-DD）+1 天：查询的 *_before 不含时刻，填「到某天（含）」
 *  时用次日 00:00，让所选那天整天都在范围内。日历运算不经过本地时区。 */
function nextDay(dateStr: string): string {
  const y = parseInt(dateStr.slice(0, 4), 10)
  const m = parseInt(dateStr.slice(5, 7), 10)
  const d = parseInt(dateStr.slice(8, 10), 10)
  const next = new Date(Date.UTC(y, m - 1, d) + 86400_000)
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
  if (f.timeField && f.timeBefore) q.set(`${f.timeField}_before`, nextDay(f.timeBefore))
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
      filters.timeBefore = before ? prevDay(before) : ''
      break
    }
  }
  const creator = q.get('creator') ?? ''
  if (creator === 'me' || creator === 'guest') filters.creatorKind = creator
  else filters.creatorName = creator
  return filters
}

function prevDay(dateStr: string): string {
  const y = parseInt(dateStr.slice(0, 4), 10)
  const m = parseInt(dateStr.slice(5, 7), 10)
  const d = parseInt(dateStr.slice(8, 10), 10)
  const prev = new Date(Date.UTC(y, m - 1, d) - 86400_000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${prev.getUTCFullYear()}-${pad(prev.getUTCMonth() + 1)}-${pad(prev.getUTCDate())}`
}

/** 标签 chip 配色：按维度在种子维度表中的位置循环取色（只影响观感）。 */
const DIMENSIONS = ['产品线', '系统', '机型', '部门', '算法', '立项', '子项目']
export function chipClass(label: string): string {
  const dim = label.includes(':') ? label.split(':', 1)[0] : ''
  const i = DIMENSIONS.indexOf(dim)
  return `chip c${i < 0 ? 0 : (i % 6) + 1}`
}

export function LabelChips({ labels }: { labels: string[] }) {
  if (!labels?.length) return null
  return (
    <>
      {labels.map((l) => (
        <span key={l} className={`${chipClass(l)}${l.includes(':') ? ' dimension' : ''}`}>
          {l}
        </span>
      ))}
    </>
  )
}

export function TicketCard({
  item,
  href,
  selected,
  desktop,
  me,
}: {
  item: TicketListItem
  href?: string
  selected?: boolean
  desktop?: boolean
  me?: { name: string }
}) {
  const target = href ?? `/t/${item.number}`
  return (
    <a
      className={`tcard${desktop ? ' pc-row' : ''}${selected ? ' selected' : ''}`}
      aria-current={selected ? 'true' : undefined}
      href={target}
      onClick={(e) => {
        e.preventDefault()
        nav(target)
      }}
    >
      {desktop ? (
        <>
          <div className="pc-row-top">
            <span className="badge kind">{item.kind === 'task' ? '任务' : '反馈'}</span>
            <span className="pc-row-number">{item.number}</span>
            <span className="pc-row-time">{fmtShort(item.updated_at)}</span>
          </div>
          <div className="pc-row-title">
            <i className="unread-dot" aria-hidden="true" />
            {item.title}
          </div>
          <div className={`pc-row-turn${item.waiting_on.member === me?.name ? ' mine' : ''}`}>
            {item.status === 'closed' ? '已关闭' : `等待 ${item.waiting_on.text}`} · 指派{' '}
            {item.assignee ?? '未指派'}
          </div>
          <div className="pc-row-labels">
            <LabelChips labels={item.labels} />
          </div>
        </>
      ) : (
        <>
          <div className="tc-top">
            <span className="badge kind">{item.kind === 'task' ? '任务' : '反馈'}</span>
            {item.status === 'closed' ? (
              <span className="badge closed">已关闭 · {closeReasonText(item.close_reason)}</span>
            ) : (
              <span className="hint">等待 {item.waiting_on.text}</span>
            )}
            <span className="hint" style={{ marginLeft: 'auto' }}>
              {fmtShort(item.updated_at)}
            </span>
          </div>
          <div className="tc-title">{item.title}</div>
          <div className="tc-meta">
            {item.number} · 发起人 {item.creator} · 指派 {item.assignee ?? '未指派'}
          </div>
          <div className="tc-foot">
            <LabelChips labels={item.labels} />
          </div>
        </>
      )}
    </a>
  )
}

function FilterGroup({
  label,
  options,
  value,
  onChange,
}: {
  label: string
  options: [string, string][]
  value: string
  onChange: (v: string) => void
}) {
  return (
    <span className="fgroup" role="group" aria-label={label}>
      <span className="flbl">{label}</span>
      {options.map(([v, name]) => (
        <button
          key={v}
          type="button"
          className={`fchip${value === v ? ' on' : ''}`}
          aria-pressed={value === v}
          onClick={() => onChange(v)}
        >
          {name}
        </button>
      ))}
    </span>
  )
}

export function TicketsPage({
  desktop,
  selected,
  me,
}: {
  desktop?: boolean
  selected?: string
  me?: { name: string }
}) {
  const search = useLocationSearch()
  const filters = useMemo(() => filtersFromSearch(search), [search])
  const [draftTimeField, setDraftTimeField] = useState<ListFilters['timeField']>(filters.timeField)
  useEffect(() => setDraftTimeField(filters.timeField), [filters.timeField])
  const [data, setData] = useState<ListResponse | null>(null)
  const [extra, setExtra] = useState<TicketListItem[]>([])
  const [error, setError] = useState<string | null>(null)
  const [moreOpen, setMoreOpen] = useState(false)
  const [labelPanelOpen, setLabelPanelOpen] = useState(false)
  const [templates, setTemplates] = useState<{ key: string; name: string }[]>([])
  const [labelHints, setLabelHints] = useState<string[]>([])
  const hintTimer = useRef<number | undefined>(undefined)

  // 标签输入的候选：resolve 接口把俗称/别名换成标准值（评审 P2-11，别名可用）。
  useEffect(() => {
    window.clearTimeout(hintTimer.current)
    const text = filters.label.join(' ').trim()
    if (!text) {
      setLabelHints([])
      return
    }
    hintTimer.current = window.setTimeout(async () => {
      try {
        const body = await api(`/api/v1/labels/resolve?q=${encodeURIComponent(text)}`)
        setLabelHints((body.matches ?? []).slice(0, 8).map((m: { label: string }) => m.label))
      } catch {
        setLabelHints([])
      }
    }, 250)
  }, [filters.label])

  const load = useCallback(async () => {
    try {
      const body: ListResponse = await api(`/api/v1/tickets?${toQuery(filters)}`)
      setData(body)
      setExtra([])
      setError(null)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '读取列表失败')
    }
  }, [filters])

  useEffect(() => {
    void load()
  }, [load])
  useInvalidate(() => {
    void load()
  })

  useEffect(() => {
    const refresh = (e: Event) => {
      if (!(e as CustomEvent<string>).detail) return
      void load()
    }
    window.addEventListener('tb:ticket-changed', refresh)
    return () => window.removeEventListener('tb:ticket-changed', refresh)
  })

  // 模板筛选的选项（含已停用：历史工单还能按它筛）。
  useEffect(() => {
    void api('/api/v1/templates')
      .then((body: { templates?: { key: string; name: string }[] }) =>
        setTemplates(body.templates ?? []))
      .catch(() => setTemplates([]))
  }, [])

  async function loadMore() {
    if (!data?.next) return
    try {
      const body: ListResponse = await api(data.next)
      setExtra([...extra, ...body.items])
      setData({ ...data, next: body.next })
    } catch {
      // 翻页失败：保持现状，用户可再点一次
    }
  }

  function update(patch: Partial<ListFilters>) {
    const next = { ...filters, ...patch }
    const base = window.location.pathname.startsWith('/t/') ? window.location.pathname : '/tickets'
    nav(`${base}?${toQuery(next)}`)
  }

  function set<K extends keyof ListFilters>(key: K, value: ListFilters[K]) {
    update({ [key]: value } as Partial<ListFilters>)
  }

  const hasLabelFilters =
    !!filters.label.length || !!filters.labelAny.length || !!filters.noLabel.length
  const hasMoreFilters =
    !!filters.template || !!filters.creatorKind || !!filters.creatorName.trim() || !!filters.timeField

  const items = [...(data?.items ?? []), ...extra]
  const changeFilter = update
  const detailHref = (number: string) => `/t/${number}?${toQuery(filters)}`
  const tier = useLayoutTier()
  const labelPanel = labelPanelOpen && (
    <LabelFacetPanel
      filters={filters}
      variant={tier === 'mobile' ? 'drawer' : 'overlay'}
      onApply={(patch) => {
        update(patch)
        setLabelPanelOpen(false)
      }}
      onClose={() => setLabelPanelOpen(false)}
    />
  )
  const labelSummary = (
    <LabelFilterSummary filters={filters} total={data?.total ?? null} onChange={update} />
  )
  const labelButton = (
    <button
      type="button"
      className={`fchip${labelPanelOpen || hasLabelFilters ? ' on' : ''}`}
      aria-pressed={labelPanelOpen}
      aria-expanded={labelPanelOpen}
      onClick={() => setLabelPanelOpen(!labelPanelOpen)}
    >
      🏷 标签 ▾
    </button>
  )

  if (desktop) {
    return (
      <div className="pc-list-pane" data-testid="ticket-list-pane">
        <header className="pc-list-head">
          <div>
            <h2>工单</h2>
            <span>{data?.total ?? '…'} 张</span>
          </div>
          <input
            placeholder="搜索"
            aria-label="搜索"
            defaultValue={filters.q}
            onKeyDown={(e) => {
              if (e.key === 'Enter') changeFilter({ q: (e.target as HTMLInputElement).value })
            }}
            onBlur={(e) => {
              if (e.target.value !== filters.q) changeFilter({ q: e.target.value })
            }}
          />
        </header>
        <div className="pc-filter-row">
          {labelButton}
          <button
            type="button"
            className={`fchip${filters.status === 'open' ? ' on' : ''}`}
            aria-pressed={filters.status === 'open'}
            onClick={() => changeFilter({ status: 'open' })}
          >
            进行中
          </button>
          <button
            type="button"
            className={`fchip${filters.status === 'closed' ? ' on' : ''}`}
            aria-pressed={filters.status === 'closed'}
            onClick={() => changeFilter({ status: 'closed' })}
          >
            已关闭
          </button>
          <button
            type="button"
            className={`fchip${filters.assignee === 'me' ? ' on' : ''}`}
            aria-pressed={filters.assignee === 'me'}
            onClick={() => changeFilter({ assignee: filters.assignee === 'me' ? '' : 'me' })}
          >
            指派给我
          </button>
          <button
            type="button"
            className={`fchip${filters.waiting === 'guest' ? ' on' : ''}`}
            aria-pressed={filters.waiting === 'guest'}
            onClick={() => changeFilter({ waiting: filters.waiting === 'guest' ? '' : 'guest' })}
          >
            等访客
          </button>
          <button
            type="button"
            className={`fchip${moreOpen || hasMoreFilters ? ' on' : ''}`}
            aria-pressed={moreOpen}
            aria-expanded={moreOpen}
            onClick={() => setMoreOpen(!moreOpen)}
          >
            更多筛选 ▾
          </button>
        </div>
        {moreOpen && (
          <div className="pc-more-filters">
            {/* #105 会在这里接管动态标签筛选；本票只保留既有条件。 */}
            <label>
              类型
              <select value={filters.kind} onChange={(e) => changeFilter({ kind: e.target.value as ListFilters['kind'] })}>
                <option value="">全部</option>
                <option value="task">任务</option>
                <option value="report">反馈</option>
              </select>
            </label>
            <label>
              指派
              <select
                value={filters.assignee}
                onChange={(e) => changeFilter({ assignee: e.target.value as ListFilters['assignee'] })}
              >
                <option value="">全部</option>
                <option value="me">指派给我</option>
                <option value="none">未指派</option>
              </select>
            </label>
            <label>
              等待方
              <select
                value={filters.waiting}
                onChange={(e) => changeFilter({ waiting: e.target.value as ListFilters['waiting'] })}
              >
                <option value="">全部</option>
                <option value="assignee">指派人</option>
                <option value="creator">发起人</option>
                <option value="guest">访客</option>
                <option value="department">本部门</option>
              </select>
            </label>
            <label>
              操作方
              <select
                value={filters.actor}
                onChange={(e) => changeFilter({ actor: e.target.value as ListFilters['actor'] })}
              >
                <option value="">全部</option>
                <option value="member">本人</option>
                <option value="agent">Agent</option>
              </select>
            </label>
            <label>
              标签
              <input
                list="pc-label-suggestions"
                placeholder="如 机型:UD-2300"
                aria-label="标签"
                defaultValue={filters.label.join(' ')}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') changeFilter({ label: [(e.target as HTMLInputElement).value] })
                }}
                onBlur={(e) => {
                  if (e.target.value !== filters.label.join(' ')) changeFilter({ label: [e.target.value] })
                }}
              />
              <datalist id="pc-label-suggestions">
                {labelHints.map((l) => (
                  <option key={l} value={l} />
                ))}
              </datalist>
            </label>
            <label>
              模板
              <select value={filters.template} onChange={(e) => changeFilter({ template: e.target.value })}>
                <option value="">全部</option>
                {templates.map((t) => (
                  <option key={t.key} value={t.key}>
                    {t.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              创建人
              <select
                value={filters.creatorKind}
                onChange={(e) => changeFilter({ creatorKind: e.target.value as ListFilters['creatorKind'], creatorName: '' })}
              >
                <option value="">全部</option>
                <option value="me">我</option>
                <option value="guest">访客（经入口）</option>
              </select>
            </label>
            <label>
              按姓名
              <input
                aria-label="按姓名"
                placeholder="成员姓名"
                defaultValue={filters.creatorName}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') changeFilter({ creatorName: (e.target as HTMLInputElement).value })
                }}
                onBlur={(e) => {
                  if (e.target.value !== filters.creatorName) changeFilter({ creatorName: e.target.value })
                }}
              />
            </label>
            <label>
              时间
              <select
                aria-label="时间字段"
                value={draftTimeField}
                onChange={(e) => setDraftTimeField(e.target.value as ListFilters['timeField'])}
              >
                <option value="">不限</option>
                <option value="created">创建于</option>
                <option value="updated">更新于</option>
                <option value="closed">关闭于</option>
              </select>
            </label>
            <label>
              从
              <input
                type="date"
                aria-label="从（含当天）"
                disabled={!draftTimeField}
                value={filters.timeAfter}
                onChange={(e) => changeFilter({ timeField: draftTimeField, timeAfter: e.target.value })}
              />
            </label>
            <label>
              到
              <input
                type="date"
                aria-label="到（含当天）"
                disabled={!draftTimeField}
                value={filters.timeBefore}
                onChange={(e) => changeFilter({ timeField: draftTimeField, timeBefore: e.target.value })}
              />
            </label>
          </div>
        )}
        {labelSummary}
        {labelPanel}
        <div className="pc-list-scroll" data-testid="ticket-list-scroll">
          {error && <div className="ecard">{error}</div>}
          {!error && items.length === 0 && data && <div className="ecard">没有符合条件的工单</div>}
          {items.map((it) => (
            <TicketCard
              key={it.number}
              item={it}
              desktop
              me={me}
              selected={selected === it.number}
              href={detailHref(it.number)}
            />
          ))}
          {data?.next && (
            <div className="submitrow">
              <button className="btn ghost" onClick={() => void loadMore()}>
                加载更多
              </button>
            </div>
          )}
        </div>
      </div>
    )
  }

  return (
    <div className="bpage">
      <div className="sticky-fb">
        <div className="filterbar">
          <div className="inrow">
            <input
              placeholder="搜索标题、说明、回复（试试 E203）"
              defaultValue={filters.q}
              aria-label="搜索"
              onKeyDown={(e) => {
                if (e.key === 'Enter') set('q', (e.target as HTMLInputElement).value)
              }}
              onBlur={(e) => {
                if (e.target.value !== filters.q) set('q', e.target.value)
              }}
            />
          </div>
          <div className="frow">
            {labelButton}
            <FilterGroup
              label="状态"
              value={filters.status}
              onChange={(v) => set('status', v as ListFilters['status'])}
              options={[
                ['open', '开启'],
                ['closed', '已关闭'],
                ['all', '全部'],
              ]}
            />
          </div>
          <div className="frow">
            <FilterGroup
              label="类型"
              value={filters.kind}
              onChange={(v) => set('kind', v as ListFilters['kind'])}
              options={[
                ['', '全部'],
                ['task', '任务'],
                ['report', '反馈'],
              ]}
            />
            <FilterGroup
              label="指派"
              value={filters.assignee}
              onChange={(v) => set('assignee', v as ListFilters['assignee'])}
              options={[
                ['', '全部'],
                ['me', '指派给我'],
                ['none', '未指派'],
              ]}
            />
          </div>
          <div className="frow">
            <FilterGroup
              label="等待方"
              value={filters.waiting}
              onChange={(v) => set('waiting', v as ListFilters['waiting'])}
              options={[
                ['', '全部'],
                ['assignee', '指派人'],
                ['creator', '发起人'],
                ['guest', '访客'],
                ['department', '本部门'],
              ]}
            />
            <FilterGroup
              label="操作方"
              value={filters.actor}
              onChange={(v) => set('actor', v as ListFilters['actor'])}
              options={[
                ['', '全部'],
                ['member', '本人'],
                ['agent', 'Agent'],
              ]}
            />
            <button
              type="button"
              className={`fchip${moreOpen || hasMoreFilters ? ' on' : ''}`}
              aria-pressed={moreOpen}
              aria-expanded={moreOpen}
              onClick={() => setMoreOpen(!moreOpen)}
            >
              更多筛选{hasMoreFilters ? ' ·' : ''}
            </button>
          </div>
          {moreOpen && (
            <div className="frow">
              <span className="fgroup" role="group" aria-label="标签">
                <span className="flbl">标签</span>
                <input
                  className="finput"
                  style={{ width: 150 }}
                  list="label-suggestions"
                  placeholder="如 机型:UD-2300 或俗称"
                  defaultValue={filters.label.join(' ')}
                  aria-label="标签"
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') set('label', [(e.target as HTMLInputElement).value])
                  }}
                  onBlur={(e) => {
                    if (e.target.value !== filters.label.join(' ')) set('label', [e.target.value])
                  }}
                />
                <datalist id="label-suggestions">
                  {labelHints.map((l) => (
                    <option key={l} value={l} />
                  ))}
                </datalist>
              </span>
              <span className="fgroup" role="group" aria-label="模板">
                <span className="flbl">模板</span>
                <select
                  className="sel finput"
                  aria-label="模板"
                  value={filters.template}
                  onChange={(e) => set('template', e.target.value)}
                >
                  <option value="">全部</option>
                  {templates.map((t) => (
                    <option key={t.key} value={t.key}>
                      {t.name}
                    </option>
                  ))}
                </select>
              </span>
              <FilterGroup
                label="创建人"
                value={filters.creatorName.trim() ? '' : filters.creatorKind}
                onChange={(v) => {
                  set('creatorKind', v as ListFilters['creatorKind'])
                  if (v) set('creatorName', '')
                }}
                options={[
                  ['', '全部'],
                  ['me', '我'],
                  ['guest', '访客（经入口）'],
                ]}
              />
              <span className="fgroup" role="group" aria-label="按姓名">
                <span className="flbl">按姓名</span>
                <input
                  className="finput"
                  style={{ width: 90 }}
                  placeholder="成员姓名"
                  defaultValue={filters.creatorName}
                  aria-label="按姓名"
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') set('creatorName', (e.target as HTMLInputElement).value)
                  }}
                  onBlur={(e) => {
                    if (e.target.value !== filters.creatorName) set('creatorName', e.target.value)
                  }}
                />
              </span>
              <span className="fgroup" role="group" aria-label="时间范围">
                <span className="flbl">时间</span>
                <select
                  className="sel finput"
                  aria-label="时间字段"
                  value={draftTimeField}
                  onChange={(e) => setDraftTimeField(e.target.value as ListFilters['timeField'])}
                >
                  <option value="">不限</option>
                  <option value="created">创建于</option>
                  <option value="updated">更新于</option>
                  <option value="closed">关闭于</option>
                </select>
                <input
                  type="date"
                  className="finput"
                  aria-label="从（含当天）"
                  disabled={!draftTimeField}
                  value={filters.timeAfter}
                  onChange={(e) => update({ timeField: draftTimeField, timeAfter: e.target.value })}
                />
                <span className="flbl">到</span>
                <input
                  type="date"
                  className="finput"
                  aria-label="到（含当天）"
                  disabled={!draftTimeField}
                  value={filters.timeBefore}
                  onChange={(e) => update({ timeField: draftTimeField, timeBefore: e.target.value })}
                />
                <span className="flbl">（含当天）</span>
              </span>
            </div>
          )}
          {labelSummary}
        </div>
      </div>
      {labelPanel}
      <div className="b-note">
        共 <b>{data?.total ?? '…'}</b> 张（当前条件）
      </div>
      {error && <div className="ecard">{error}</div>}
      {!error && items.length === 0 && data && <div className="ecard">没有符合条件的工单</div>}
      {items.map((it) => (
        <TicketCard key={it.number} item={it} href={detailHref(it.number)} />
      ))}
      {data?.next && (
        <div className="submitrow">
          <button className="btn ghost" onClick={() => void loadMore()}>
            加载更多
          </button>
        </div>
      )}
    </div>
  )
}
