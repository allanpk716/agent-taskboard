// 工单列表（#41）：横向滑动的筛选 chips（状态/类型/指派/等待方/操作方）+
// 关键词搜索 + 卡片。「操作方」用查询接口的 actor_kind（本人/Agent，按创建者
// 署名区分）；翻页跟着响应里的 next 走。
import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../api'
import { closeReasonText, fmtShort } from '../shared/format'
import { api } from '../api'
import { nav } from './ui'

export interface ListFilters {
  status: 'open' | 'closed' | 'all'
  kind: '' | 'task' | 'report'
  assignee: '' | 'me' | 'none'
  waiting: '' | 'assignee' | 'creator' | 'guest' | 'department'
  actor: '' | 'member' | 'agent'
  q: string
}

const DEFAULT_FILTERS: ListFilters = {
  status: 'open',
  kind: '',
  assignee: '',
  waiting: '',
  actor: '',
  q: '',
}

interface TicketListItem {
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

function toQuery(f: ListFilters): string {
  const q = new URLSearchParams()
  q.set('status', f.status)
  if (f.kind) q.set('kind', f.kind)
  if (f.assignee) q.set('assignee', f.assignee)
  if (f.waiting) q.set('waiting_on', f.waiting)
  if (f.actor) q.set('actor_kind', f.actor)
  if (f.q.trim()) q.set('q', f.q.trim())
  return q.toString()
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
        <span key={l} className={chipClass(l)}>
          {l}
        </span>
      ))}
    </>
  )
}

export function TicketCard({ item }: { item: TicketListItem }) {
  return (
    <a
      className="tcard"
      href={`/t/${item.number}`}
      onClick={(e) => {
        e.preventDefault()
        nav(`/t/${item.number}`)
      }}
    >
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

export function TicketsPage() {
  const [filters, setFilters] = useState<ListFilters>(DEFAULT_FILTERS)
  const [data, setData] = useState<ListResponse | null>(null)
  const [extra, setExtra] = useState<TicketListItem[]>([])
  const [error, setError] = useState<string | null>(null)

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

  function set<K extends keyof ListFilters>(key: K, value: ListFilters[K]) {
    setFilters({ ...filters, [key]: value })
  }

  const items = [...(data?.items ?? []), ...extra]
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
          </div>
        </div>
      </div>
      <div className="b-note">
        共 <b>{data?.total ?? '…'}</b> 张（当前条件）
      </div>
      {error && <div className="ecard">{error}</div>}
      {!error && items.length === 0 && data && <div className="ecard">没有符合条件的工单</div>}
      {items.map((it) => (
        <TicketCard key={it.number} item={it} />
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
