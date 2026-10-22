// 工单详情（#41）：整页对话流（沿用访客网页的对话流观感，复用 shared/
// conversation 的气泡），另加成员才有的东西——可展开的元信息区（标签、指派、
// 等待方、访客链接）、一排小按钮（打标签/改派/访客链接/关闭/重开）、验收面板、
// 带「交付」标记的回复输入区、时间线按「本人 / Agent」筛选（客户端筛，与
// 服务端 actor_kind 同一语义：成员 Agent 的回复算 Agent 一方）。
import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError } from '../api'
import type { MemberTicket } from './memberApi'
import type { DraftView } from '../shared/types'
import {
  addCollaborator,
  assignTicket,
  changeLabels,
  closeTicket,
  createGuestLink,
  discardDraft,
  extendGuestLink,
  fetchBriefDrafting,
  fetchAccessRecords,
  fetchGuestLinks,
  fetchReplyDrafting,
  fetchMembers,
  fetchTicket,
  leaveCollaborators,
  reopenTicket,
  replyTicket,
  sendDraft,
  updateDraft,
  removeCollaborator,
  resetGuestAccessCode,
  revokeGuestLink,
  uploadMemberAttachment,
  type GuestLink,
  type CreatedGuestLink,
  type AccessRecordsResponse,
  type Me,
  type MemberRef,
  type Collaborator,
} from './memberApi'
import { BriefBubble, EventLine, ReplyBubble, eventText } from '../shared/conversation'
import { DimensionSelect } from '../guest/DimensionSelect'
import { AttachmentChips, DeactivatedBadge, Lightbox } from '../shared/widgets'
import { absoluteUrl, authorName, closeReasonText, fmtBeijing, fmtLinkExpiry, fmtShort, linkExpired } from '../shared/format'
import { Confirm, Modal, copyText, invalidate, nav, toast, useLocationSearch } from './ui'
import { HandoffDialog } from './HandoffDialog'
import { LabelChips, chipClass, turnLabel } from './TicketsPage'
import { detailListSource, filtersFromSearch, toQuery, toggleLabelCondition } from './listFilters'
import { useLayoutTier } from './useLayoutTier'

/* ---------------- 弹窗：打标签 ---------------- */

function LabelDialog({
  ticket,
  onChanged,
  onClose,
}: {
  ticket: MemberTicket
  onChanged: () => void
  onClose: () => void
}) {
  const [labels, setLabels] = useState<string[]>(ticket.labels ?? [])
  const [input, setInput] = useState('')
  const [allowNew, setAllowNew] = useState(false)
  const [err, setErr] = useState<{ msg: string; hint?: string; suggestions?: string[] } | null>(null)

  async function apply(add: string[], remove: string[], allow: boolean): Promise<boolean> {
    try {
      const res = await changeLabels(ticket.number, { add, remove, allow_new: allow })
      setLabels(res.labels)
      setErr(null)
      const notes: string[] = []
      for (const r of res.alias_resolved ?? []) {
        notes.push(`别名「${r.requested}」已换成标准值 ${r.label}`)
      }
      for (const v of res.created_dictionary_values ?? []) notes.push(`已新建字典值 ${v}`)
      if (notes.length) toast(notes.join('；'))
      onChanged()
      return true
    } catch (e) {
      if (e instanceof ApiError) {
        setErr({ msg: `${e.code}：${e.message}`, hint: e.hint })
      } else {
        setErr({ msg: '打标签失败，请重试' })
      }
      return false
    }
  }

  async function addLabel(raw?: string) {
    const s = (raw ?? input).trim()
    if (!s) {
      setErr({ msg: '请输入标签，例如 机型:UD-2300 或 待初筛' })
      return
    }
    if (await apply([s], [], allowNew)) setInput('')
  }

  return (
    <Modal title={`打标签 · ${ticket.number}`} onClose={onClose}>
      <div className="lblrow" aria-label="当前标签">
        {labels.length ? (
          labels.map((l) => (
            <span key={l} className={chipClass(l)}>
              {l}
              <i
                title="移除"
                onClick={() => {
                  void apply([], [l], false)
                }}
              >
                ×
              </i>
            </span>
          ))
        ) : (
          <span className="hint">还没有标签</span>
        )}
      </div>
      <div className="inrow">
        <input
          aria-label="标签输入"
          placeholder="机型:UD-2300 或 待初筛（试试别名 2300 / BM10）"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              void addLabel()
            }
          }}
        />
        <button className="btn" onClick={() => void addLabel()}>
          添加
        </button>
      </div>
      <label className="ckrow">
        <input type="checkbox" checked={allowNew} onChange={(e) => setAllowNew(e.target.checked)} /> 允许新建字典值
        （allow_new）
      </label>
      <div className="hint">
        值不在字典会被拒绝并给出近似值；写别名会自动换成标准值；不带维度的标签可随手新建；新维度只能由管理员添加。
      </div>
      {err && (
        <div className="errbox">
          {err.msg}
          {err.hint && (
            <div className="hint">
              {err.hint}
              {err.suggestions?.length ? `建议：${err.suggestions.join('、')}` : ''}
            </div>
          )}
        </div>
      )}
      <div className="mrow">
        <span className="sp" />
        <button className="btn ghost" onClick={onClose}>
          完成
        </button>
      </div>
    </Modal>
  )
}

/* ---------------- 弹窗：指派 / 改派 ---------------- */

function AssignDialog({
  ticket,
  members,
  me,
  onChanged,
  onClose,
}: {
  ticket: MemberTicket
  members: MemberRef[]
  me?: Me
  onChanged: () => void
  onClose: () => void
}) {
  const [choice, setChoice] = useState('')
  const [handoff, setHandoff] = useState<{ text: string; shareText: string; assignee: string } | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const changing = !!ticket.assignee?.member
  const assignable = members.filter(
    (m) => m.active !== false && m.id !== ticket.assignee?.member?.id,
  )

  async function confirm() {
    if (!choice) return
    try {
      const res = await assignTicket(ticket.number, choice)
      const fresh = await fetchTicket(ticket.number)
      onChanged()
      if (res.assignee?.id === me?.id) {
        onClose()
        toast(`已指派给 ${choice}`)
        return
      }
      if (!res.share_text || !fresh.handoff_text) {
        onClose()
        toast(`已指派给 ${choice}`)
        return
      }
      setHandoff({
        text: fresh.handoff_text,
        shareText: res.share_text,
        assignee: res.assignee?.name ?? choice,
      })
    } catch (e) {
      setErr(e instanceof ApiError ? `${e.code}：${e.message}` : '指派失败，请重试')
    }
  }

  if (handoff !== null) {
    return (
      <HandoffDialog
        handoffText={handoff.text}
        handoffShareText={handoff.shareText}
        assigneeName={handoff.assignee}
        mode="share"
        onClose={onClose}
      />
    )
  }

  return (
    <Modal title={`${changing ? '改派' : '指派'} · ${ticket.number}`} onClose={onClose}>
      <label className="fldlab" htmlFor="as-sel">
        指派给（只能指派成员，成员 Agent 不能被指派）
      </label>
      <select
        id="as-sel"
        className="sel"
        aria-label="指派给"
        value={choice}
        onChange={(e) => setChoice(e.target.value)}
        style={{ width: '100%' }}
      >
        <option value="">— 选择{changing ? '新' : ''}指派人 —</option>
        {assignable.map((m) => (
          <option key={m.id} value={m.name}>
            {m.name}
          </option>
        ))}
      </select>
      {assignable.length === 0 && (
        <div className="errbox">{changing ? '没有其他可改派的成员' : '没有可指派的成员'}</div>
      )}
      <div className="hint">改派后，原指派人和新指派人的收件箱都会变化。</div>
      {err && <div className="errbox">{err}</div>}
      <div className="mrow">
        <button className="btn ghost" onClick={onClose}>
          取消
        </button>
        <span className="sp" />
        <button className="btn" disabled={!choice || assignable.length === 0} onClick={() => void confirm()}>
          确认{changing ? '改派' : '指派'}
        </button>
      </div>
    </Modal>
  )
}

function CollaboratorsDialog({
  ticket,
  members,
  me,
  onChanged,
  onClose,
}: {
  ticket: MemberTicket
  members: MemberRef[]
  me?: Me
  onChanged: () => void
  onClose: () => void
}) {
  const [choice, setChoice] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const collaborators = ticket.collaborators ?? []
  const blocked = new Set<number>([
    ticket.creator.member?.id ?? 0,
    ticket.assignee?.member?.id ?? 0,
    ...collaborators.map((c) => c.id),
  ])
  const candidates = members.filter((m) => m.active !== false && !blocked.has(m.id))

  function handle(e: unknown, fallback: string) {
    setErr(e instanceof ApiError ? `${e.code}：${e.message}` : fallback)
  }

  async function add() {
    if (!choice) return
    try {
      await addCollaborator(ticket.number, choice)
      setChoice('')
      setErr(null)
      onChanged()
    } catch (e) {
      handle(e, '加入协作者失败，请重试')
    }
  }

  async function remove(c: Collaborator) {
    try {
      await removeCollaborator(ticket.number, c.id)
      setErr(null)
      onChanged()
    } catch (e) {
      handle(e, '移除协作者失败，请重试')
    }
  }

  async function leave() {
    try {
      await leaveCollaborators(ticket.number)
      setErr(null)
      onChanged()
      onClose()
    } catch (e) {
      handle(e, '退出协作失败，请重试')
    }
  }

  return (
    <Modal title={`协作者 · ${ticket.number}`} onClose={onClose}>
      <table className="mini-tbl">
        <tbody>
          {collaborators.length === 0 && (
            <tr>
              <td className="hint">还没有协作者</td>
            </tr>
          )}
          {collaborators.map((c) => (
            <tr key={c.id} data-collaborator={c.id}>
              <td>
                {c.name}
                {c.deactivated && <DeactivatedBadge />}
              </td>
              <td className="hint">{new Date(c.added_at).toLocaleDateString('zh-CN')}</td>
              <td>
                {c.id === me?.id ? (
                  <button className="btn sm ghost" onClick={() => void leave()}>
                    退出
                  </button>
                ) : (
                  ticket.viewer.can.manage_collaborators && (
                    <button
                      className="btn sm ghost"
                      aria-label={`移除 ${c.name}`}
                      onClick={() => {
                        void remove(c)
                      }}
                    >
                      移除
                    </button>
                  )
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {ticket.viewer.can.manage_collaborators && (
        <>
          <div className="inrow top-gap">
            <select
              className="sel"
              aria-label="选择成员"
              value={choice}
              onChange={(e) => setChoice(e.target.value)}
            >
              <option value="">— 选择成员 —</option>
              {candidates.map((m) => (
                <option key={m.id} value={m.name}>
                  {m.name}
                </option>
              ))}
            </select>
            <button className="btn" disabled={!choice} onClick={() => void add()}>
              加入
            </button>
          </div>
          <div className="hint">创建者、指派人和管理员可以加人；未指派时初筛人也可以加人。</div>
        </>
      )}
      {ticket.viewer.can.leave_collaborators && !ticket.viewer.can.manage_collaborators && (
        <div className="inrow top-gap">
          <button className="btn ghost" onClick={() => void leave()}>
            退出协作
          </button>
        </div>
      )}
      {err && <div className="errbox">{err}</div>}
      <div className="mrow">
        <span className="sp" />
        <button className="btn ghost" onClick={onClose}>
          完成
        </button>
      </div>
    </Modal>
  )
}

/* ---------------- 弹窗：访客链接 ---------------- */

function LinksDialog({
  ticket,
  links,
  reload,
  onCreated,
  onChanged,
  onClose,
}: {
  ticket: MemberTicket
  links: GuestLink[] | null
  reload: () => Promise<void>
  onCreated: (link: CreatedGuestLink) => void
  onChanged: () => void
  onClose: () => void
}) {
  const [note, setNote] = useState('')
  const [days, setDays] = useState('30')
  const [date, setDate] = useState('') // 「指定日期」写法的到期日（YYYY-MM-DD，管满该日全天）
  const [canEdit, setCanEdit] = useState(false)
  const [created, setCreated] = useState<{ url: string } | null>(null)
  const [resetCode, setResetCode] = useState<string | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [extendDates, setExtendDates] = useState<Record<number, string>>({}) // 每条链接「延期到该日」的日期

  async function create() {
    if (!note.trim()) {
      setErr('note_required：备注必填，写上给谁用的，如「检验科-张三」')
      return
    }
    try {
      const link =
        days === 'date'
          ? await createGuestLink(ticket.number, {
              note: note.trim(),
              expires_at: date,
              can_edit_brief: canEdit,
            })
          : await createGuestLink(ticket.number, {
              note: note.trim(),
              valid_days: Number(days),
              can_edit_brief: canEdit,
            })
      setCreated({ url: link.url })
      setResetCode(null)
      setErr(null)
      setNote('')
      onCreated(link)
      onChanged()
      void reload()
    } catch (e) {
      setErr(e instanceof ApiError ? `${e.code}：${e.message}` : '创建失败，请重试')
    }
  }

  return (
    <Modal title={`访客链接 · ${ticket.number}`} onClose={onClose} wide>
      <table className="mini-tbl">
        <tbody>
          {links === null ? (
            <tr>
              <td className="hint">加载中…</td>
            </tr>
          ) : links.length === 0 ? (
            <tr>
              <td className="hint">还没有访客链接</td>
            </tr>
          ) : (
            links.map((g) => {
              const dead = !!g.revoked_at || linkExpired(g.expires_at)
              return (
                <tr key={g.id}>
                  <td>
                    {g.note}
                    {g.can_edit_brief && <span className="mini">可填写说明</span>}
                  </td>
                  <td>
                    {fmtLinkExpiry(g.expires_at)}
                    {g.revoked_at ? ' · 已撤销' : ''}
                    {!g.revoked_at && linkExpired(g.expires_at) ? ' · 已过期' : ''}
                    <div className="mini">
                      {g.status === 'claimed'
                        ? `已认领 · ${g.claim_name ?? ''} · ${g.claimed_at ? fmtShort(g.claimed_at) : ''} · ${g.device_count ?? 0} 台设备`
                        : g.status === 'access_code_locked'
                          ? `已认领 · ${g.claim_name ?? ''} · 输码已锁定`
                        : g.status === 'revoked' || g.revoked_at
                          ? '已认领状态不可用'
                          : '未认领'}
                    </div>
                  </td>
                  <td>
                    {dead ? (
                      <span className="hint">{g.revoked_at ? '已失效，不能延期' : '已过期，不能延期'}</span>
                    ) : (
                      <>
                        <button
                          className="btn sm ghost"
                          onClick={() => {
                            void extendGuestLink(ticket.number, g.id, { valid_days: 30 })
                              .then(() => {
                                toast('✓ 已延期 30 天')
                                void reload()
                                onChanged()
                              })
                              .catch((e) => toast(e instanceof ApiError ? e.message : '延期失败'))
                          }}
                        >
                          延期 30 天
                        </button>{' '}
                        <input
                          type="date"
                          aria-label={`指定日期 ${g.note}`}
                          value={extendDates[g.id] ?? ''}
                          onChange={(e) => setExtendDates((m) => ({ ...m, [g.id]: e.target.value }))}
                        />{' '}
                        <button
                          className="btn sm ghost"
                          disabled={!extendDates[g.id]}
                          onClick={() => {
                            const until = extendDates[g.id] ?? ''
                            void extendGuestLink(ticket.number, g.id, { expires_at: until })
                              .then(() => {
                                toast(`✓ 已延期到 ${until}（当天有效）`)
                                setExtendDates((m) => ({ ...m, [g.id]: '' }))
                                void reload()
                                onChanged()
                              })
                              .catch((e) => toast(e instanceof ApiError ? e.message : '延期失败'))
                          }}
                        >
                          延期到该日
                        </button>{' '}
                        {g.status !== 'unclaimed' && (
                          <button
                            className="btn sm ghost"
                            onClick={() => {
                              void resetGuestAccessCode(ticket.number, g.id)
                                .then((result) => {
                                  setResetCode(result.access_code)
                                  toast('✓ 已重置访问码，旧设备已退出')
                                  void reload()
                                  onChanged()
                                })
                                .catch((e) => toast(e instanceof ApiError ? e.message : '重置访问码失败'))
                            }}
                          >
                            重置访问码
                          </button>
                        )}{' '}
                        <button
                          className="btn sm ghost"
                          onClick={() => {
                            void revokeGuestLink(ticket.number, g.id)
                              .then(() => {
                                toast('已撤销，对方再打开会看到链接失效页')
                                void reload()
                                onChanged()
                              })
                              .catch((e) => toast(e instanceof ApiError ? e.message : '撤销失败'))
                          }}
                        >
                          撤销
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              )
            })
          )}
        </tbody>
      </table>
      <div className="sec-t">创建新链接</div>
      <div className="inrow">
        <input
          aria-label="备注"
          placeholder="备注（必填），如：检验科-张三"
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />
      </div>
      <div className="inrow">
        <select className="sel" aria-label="有效期" value={days} onChange={(e) => setDays(e.target.value)}>
          <option value="7">7 天</option>
          <option value="30">30 天（默认）</option>
          <option value="90">90 天</option>
          <option value="date">指定日期（当天有效）</option>
        </select>
        {days === 'date' && (
          <input type="date" aria-label="到期日期" value={date} onChange={(e) => setDate(e.target.value)} />
        )}
        <label className="ckrow" style={{ margin: 0 }}>
          <input type="checkbox" checked={canEdit} onChange={(e) => setCanEdit(e.target.checked)} /> 可填写说明
        </label>
      </div>
      <div className="hint">没有永久有效的选项；链接的创建、撤销和延期都会记入时间线。</div>
      {err && <div className="errbox">{err}</div>}
      {created && (
        <div className="okbox">
          ✓ 已创建（完整链接只在创建时出现一次）。
          <div className="linkbox">{absoluteUrl(created.url)}</div>
          <button
            className="btn sm"
            onClick={() => {
              // 复制出去的链接必须能直接打开：API 给了绝对地址就用它，
              // 相对路径用当前页面的 origin 拼绝对（评审 P2-7）。
              void copyText(absoluteUrl(created.url)).then((ok) => toast(ok ? '✓ 已复制到剪贴板' : '复制失败，请手动选择文字复制'))
            }}
          >
            复制链接
          </button>
        </div>
      )}
      {resetCode && (
        <div className="okbox">
          ✓ 新访问码只显示这一次，请私下转给访客本人。
          <div className="linkbox" data-reset-code>
            {resetCode}
          </div>
        </div>
      )}
      <div className="mrow">
        <button className="btn ghost" onClick={onClose}>
          关闭
        </button>
        <span className="sp" />
        <button className="btn" onClick={() => void create()}>
          创建
        </button>
      </div>
    </Modal>
  )
}

/* ---------------- 弹窗：关闭 ---------------- */

function CloseDialog({
  ticket,
  onChanged,
  onClose,
}: {
  ticket: MemberTicket
  onChanged: () => void
  onClose: () => void
}) {
  const [reason, setReason] = useState('done')
  const [dupOf, setDupOf] = useState('')
  const [err, setErr] = useState<string | null>(null)

  async function confirm() {
    if (reason === 'duplicate' && !dupOf.trim()) {
      setErr('duplicate_of_required：重复关闭必须注明重复于哪一张')
      return
    }
    try {
      await closeTicket(ticket.number, {
        reason,
        ...(reason === 'duplicate' ? { duplicate_of: dupOf.trim() } : {}),
      })
      onChanged()
      onClose()
      toast('✓ 已关闭')
    } catch (e) {
      setErr(e instanceof ApiError ? `${e.code}：${e.message}` : '关闭失败，请重试')
    }
  }

  return (
    <Modal title={`关闭工单 · ${ticket.number}`} onClose={onClose}>
      <label className="ckrow">
        <input type="radio" name="close-reason" checked={reason === 'done'} onChange={() => setReason('done')} /> 完成
        —— 验收通过，工作闭环
      </label>
      <label className="ckrow">
        <input type="radio" name="close-reason" checked={reason === 'wontfix'} onChange={() => setReason('wontfix')} />{' '}
        不处理 —— 说明理由后结束
      </label>
      <label className="ckrow">
        <input
          type="radio"
          name="close-reason"
          checked={reason === 'duplicate'}
          onChange={() => setReason('duplicate')}
        />{' '}
        重复 —— 注明重复于哪一张
      </label>
      {reason === 'duplicate' && (
        <div className="inrow">
          <input
            aria-label="重复于"
            placeholder="重复于哪张工单，如 20261005-002"
            value={dupOf}
            onChange={(e) => setDupOf(e.target.value)}
          />
        </div>
      )}
      <div className="hint">关闭后访客链接变为只读；重新开启后恢复。工单永远不能删除。</div>
      {err && <div className="errbox">{err}</div>}
      <div className="mrow">
        <button className="btn ghost" onClick={onClose}>
          取消
        </button>
        <span className="sp" />
        <button className="btn" onClick={() => void confirm()}>
          关闭工单
        </button>
      </div>
    </Modal>
  )
}

function remainingDays(iso: string): number {
  return Math.max(0, Math.ceil((new Date(iso).getTime() - Date.now()) / 86_400_000))
}

function AccessRecordsPanel({ data }: { data: AccessRecordsResponse | null }) {
  return (
    <section aria-label="访问记录">
      <div className="sec-t">访问记录</div>
      {data === null ? (
        <div className="hint">加载中…</div>
      ) : data.records.length === 0 ? (
        <div className="hint">自 {data.since} 起记录</div>
      ) : (
        <ol className="access-list">
          {data.records.map((r, i) => (
            <li key={`${r.first_at}-${i}`}>
              <div className="access-title">
                <b>{r.actor.name}</b>
                {r.actor.kind === 'admin' && <span className="badge mini-admin">管理员</span>}
              </div>
              <div className="access-meta">
                {r.action === 'view' && `查看详情 · ${r.count} 次`}
                {r.action === 'download' && `下载附件 ${r.attachment?.filename ?? ''}`}
                {r.action === 'denied' &&
                  `尝试访问被拒（${r.actor.entry_method === 'expired' ? '链接已过期' : '链接已撤销'}）`}
              </div>
              <div className="access-meta">
                {r.action === 'view' && r.count > 1
                  ? `${fmtBeijing(r.first_at)} 至 ${fmtBeijing(r.last_at)}`
                  : fmtBeijing(r.first_at)}
              </div>
            </li>
          ))}
        </ol>
      )}
    </section>
  )
}

function TicketInfoColumn({
  ticket,
  links,
  knownUrls,
  accessRecords,
  onAssign,
  onCollaborators,
  onLabels,
  onLinks,
  onLabelToggle,
}: {
  ticket: MemberTicket
  links: GuestLink[] | null
  knownUrls: Record<number, string>
  accessRecords: AccessRecordsResponse | null
  onAssign: () => void
  onCollaborators: () => void
  onLabels: () => void
  onLinks: () => void
  onLabelToggle: (label: string) => void
}) {
  const open = ticket.status === 'open'
  return (
    <aside className="dinfo" role="region" aria-label="工单信息">
      <section>
        <div className="sec-t">状态</div>
        <div className="ival">
          <span className="badge kind">{ticket.kind === 'task' ? '任务' : '反馈'}</span>
          <span>{open ? turnLabel(ticket.waiting_on, ticket.viewer.your_turn) : '已关闭'}</span>
        </div>
      </section>
      <section>
        <div className="sec-t">协作者</div>
        <div className="ival stacked">
          {(ticket.collaborators ?? []).length === 0 ? (
            <span className="hint">暂无</span>
          ) : (
            (ticket.collaborators ?? []).map((c) => (
              <span key={c.id}>
                {c.name}
                {c.deactivated && <DeactivatedBadge />}
              </span>
            ))
          )}
          <button className="btn sm ghost" onClick={onCollaborators}>
            管理
          </button>
        </div>
      </section>
      <section>
        <div className="sec-t">指派人</div>
        <div className="ival">
          <b>{ticket.assignee?.member?.name ?? '未指派'}</b>
          {ticket.assignee?.member?.deactivated && <DeactivatedBadge />}
          <button className="btn sm ghost" onClick={onAssign}>
            {ticket.assignee?.member ? '改派' : '指派'}
          </button>
        </div>
      </section>
      <section>
        <div className="sec-t">发起人</div>
        <div className="ival">
          {authorName(ticket.creator)}
          {ticket.creator.member?.deactivated && <DeactivatedBadge />}
          {ticket.template ? ` · 模板 ${ticket.template}` : ''}
        </div>
      </section>
      <section>
        <div className="sec-t">标签</div>
        <div className="ival">
          <LabelChips labels={ticket.labels ?? []} onToggle={onLabelToggle} />
          <button className="btn sm ghost" onClick={onLabels}>
            编辑
          </button>
        </div>
      </section>
      <section>
        <div className="sec-t">访客链接</div>
        {links === null ? (
          <div className="hint">加载中…</div>
        ) : links.length === 0 ? (
          <div className="hint">还没有访客链接</div>
        ) : (
          links.map((link) => {
            const dead = !!link.revoked_at || linkExpired(link.expires_at)
            return (
              <article className="link-card" key={link.id}>
                <b>{link.note}</b>
                <div>{link.can_edit_brief ? '可填写说明' : '不能填写说明'}</div>
                <div className="short-link">{knownUrls[link.id] ?? '/g/••••••'}</div>
                <div>
                  {dead ? '已失效' : `剩余 ${remainingDays(link.expires_at)} 天`}
                  {' · '}
                  {`已使用 ${link.use_count ?? 0} 次`}
                </div>
                <button className="btn sm ghost" onClick={onLinks}>
                  管理
                </button>
              </article>
            )
          })
        )}
      </section>
      <AccessRecordsPanel data={accessRecords} />
    </aside>
  )
}

/* ---------------- 详情页 ---------------- */

type ActorFilter = 'all' | 'member' | 'agent'

function BriefEditDialog({
  ticket,
  onChanged,
  onClose,
}: {
  ticket: MemberTicket
  onChanged: () => void
  onClose: () => void
}) {
  const templateFields = ticket.brief.template_fields ?? []
  const [fields, setFields] = useState<Record<string, string>>(ticket.brief.fields ?? {})
  const [markdown, setMarkdown] = useState(ticket.brief.markdown)
  const [attachmentIds, setAttachmentIds] = useState(ticket.brief.attachment_ids)
  const [uploadedNames, setUploadedNames] = useState<Record<string, string>>({})
  const [uploading, setUploading] = useState(0)
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function addFiles(files: File[]) {
    for (const file of files) {
      setUploading((n) => n + 1)
      try {
        const att = await uploadMemberAttachment(ticket.number, file)
        setAttachmentIds((ids) => [...ids, att.id])
        setUploadedNames((names) => ({ ...names, [att.id]: file.name }))
      } catch (e) {
        setErr(e instanceof ApiError ? `${e.code}：${e.message}${e.hint ? `（${e.hint}）` : ''}` : `${file.name} 上传失败`)
      } finally {
        setUploading((n) => n - 1)
      }
    }
  }

  async function save() {
    setSaving(true)
    setErr(null)
    try {
      const body = templateFields.length
        ? { fields, attachment_ids: attachmentIds, base_revision: ticket.brief.revision }
        : { brief_md: markdown, attachment_ids: attachmentIds, base_revision: ticket.brief.revision }
      await api(`/api/v1/tickets/${encodeURIComponent(ticket.number)}`, {
        method: 'PATCH',
        body: JSON.stringify(body),
        idempotencyKey: crypto.randomUUID(),
      })
      onChanged()
      onClose()
    } catch (e) {
      setErr(e instanceof ApiError ? `${e.code}：${e.message}${e.hint ? `（${e.hint}）` : ''}` : '保存失败，请重试')
    } finally {
      setSaving(false)
    }
  }

  const attachmentsById = new Map(ticket.attachments.map((a) => [a.id, a]))
  return (
    <Modal title={`修改说明 · ${ticket.number}`} onClose={onClose} wide>
      <div className="brief-edit-dialog">
        {templateFields.length > 0 ? (
          templateFields.map((f) => (
            <div className="fld" key={f.key}>
              <label htmlFor={`be-${f.key}`}>{f.title}</label>
              {f.type === 'dimension' ? (
                <DimensionSelect
                  field={f}
                  value={fields[f.key] ?? ''}
                  onChange={(v) => setFields({ ...fields, [f.key]: v })}
                />
              ) : (
                <textarea
                  id={`be-${f.key}`}
                  value={fields[f.key] ?? ''}
                  onChange={(e) => setFields({ ...fields, [f.key]: e.target.value })}
                />
              )}
            </div>
          ))
        ) : (
          <div className="fld">
            <label htmlFor="be-markdown">说明（一整块 Markdown）</label>
            <textarea id="be-markdown" style={{ minHeight: 160 }} value={markdown} onChange={(e) => setMarkdown(e.target.value)} />
          </div>
        )}
        <div className="fld">
          <label>附件</label>
          <div className="atts edit-atts">
            {attachmentIds.length === 0 && <span className="hint">还没有整张工单的附件。</span>}
            {attachmentIds.map((id) => {
              const att = attachmentsById.get(id)
              const name = att?.filename ?? uploadedNames[id] ?? id
              return (
                <span key={id} className="att">
                  📎 {name}
                  <button type="button" aria-label={`删除 ${name}`} onClick={() => setAttachmentIds((ids) => ids.filter((x) => x !== id))}>
                    ×
                  </button>
                </span>
              )
            })}
          </div>
          <input
            aria-label="添加附件"
            type="file"
            multiple
            onChange={(e) => {
              const files = Array.from(e.target.files ?? [])
              e.target.value = ''
              void addFiles(files)
            }}
          />
        </div>
        {err && <div className="errbox">{err}</div>}
        <div className="mrow">
          <button type="button" className="btn ghost" onClick={onClose}>
            取消
          </button>
          <span className="sp" />
          <button type="button" className="btn" disabled={saving || uploading > 0} onClick={() => void save()}>
            {uploading > 0 ? '上传附件中…' : '保存修改'}
          </button>
        </div>
      </div>
    </Modal>
  )
}

export function TicketDetailPage({
  ticketRef,
  me,
  returnTo = '/tickets',
}: {
  ticketRef: string
  me?: Me
  returnTo?: string
}) {
  const actorHelp =
    '（访客与系统事件始终显示；成员 Agent 的回复算它所属成员一方；访客自报「由 Agent 写成」的算 Agent）'
  const tier = useLayoutTier()
  const search = useLocationSearch()
  const [ticket, setTicket] = useState<MemberTicket | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [lightbox, setLightbox] = useState<string | null>(null)
  const [actor, setActor] = useState<ActorFilter>('all')
  const [dialog, setDialog] = useState<'label' | 'brief' | 'assign' | 'collaborators' | 'links' | 'close' | 'reopen' | 'agent' | 'share' | null>(null)
  const [mobileMenu, setMobileMenu] = useState(false)
  const [members, setMembers] = useState<MemberRef[]>([])
  const [guestLinks, setGuestLinks] = useState<GuestLink[] | null>(null)
  const [knownLinkUrls, setKnownLinkUrls] = useState<Record<number, string>>({})
  const [accessRecords, setAccessRecords] = useState<AccessRecordsResponse | null>(null)
  const [allInfoOpen, setAllInfoOpen] = useState(false)
  const [draftingSeq, setDraftingSeq] = useState<number | null>(null)
  const [briefDraftingOpen, setBriefDraftingOpen] = useState(false)
  const load = useCallback(async () => {
    try {
      const got = await fetchTicket(ticketRef)
      setTicket(got)
      setError(null)
    } catch (e) {
      setError(
        e instanceof ApiError && e.code === 'ticket_not_found'
          ? '找不到这张工单，或你没有权限查看'
          : e instanceof ApiError
            ? `${e.message}（${e.code}）`
            : '读取工单失败',
      )
    }
  }, [ticketRef])
  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    fetchMembers()
      .then(setMembers)
      .catch(() => {})
  }, [])

  const reloadGuestLinks = useCallback(async () => {
    try {
      setGuestLinks(await fetchGuestLinks(ticketRef))
    } catch {
      setGuestLinks([])
    }
  }, [ticketRef])
  useEffect(() => {
    void reloadGuestLinks()
  }, [reloadGuestLinks, tier])

  useEffect(() => {
    fetchAccessRecords(ticketRef)
      .then(setAccessRecords)
      .catch(() => setAccessRecords({ records: [], since: '' }))
  }, [ticketRef])

  function changed() {
    void load()
    invalidate()
    if (ticket) window.dispatchEvent(new CustomEvent('tb:ticket-changed', { detail: ticket.number }))
  }

  if (error) {
    return (
      <div className="bpage detail">
        <div className="ecard">{error}</div>
      </div>
    )
  }
  if (!ticket) {
    return (
      <div className="bpage detail">
        <div className="ecard">加载中…</div>
      </div>
    )
  }

  const open = ticket.status === 'open'
  const can = ticket.viewer.can ?? {}
  const closed = !open
  const deliveredToMe =
    open && ticket.viewer.your_turn && ticket.waiting_on.reason.includes('交付') && ticket.waiting_on.reason.includes('验收')
  const hasActiveGuestLink =
    guestLinks?.some((link) => !link.revoked_at && !linkExpired(link.expires_at)) ?? false

  function toggleDetailLabel(label: string) {
    const { fromInbox, query: sourceQuery } = detailListSource(search)
    const filters = fromInbox
      ? filtersFromSearch('status=open')
      : filtersFromSearch(sourceQuery)
    const query = toQuery({ ...filters, ...toggleLabelCondition(filters, label) })
    if (tier === 'narrow' || tier === 'mobile') {
      nav(`/tickets?${query}`)
      return
    }
    nav(`/t/${ticket!.number}?${query}`)
  }

  function StatusBadge() {
    if (closed) return <span className="badge closed">已关闭 · {closeReasonText(ticket!.close_reason)}</span>
    if (ticket!.viewer.your_turn)
      return (
        <span className="badge myturn">
          ● 现在轮到你{ticket!.waiting_on.reason ? `（${ticket!.waiting_on.reason}）` : ''}
        </span>
      )
    return <span className="badge waiting">等待 {ticket!.waiting_on.text}</span>
  }

  const detailMain = (
    <>
      {deliveredToMe && (
        <div className="accept">
          <div>📦 指派人已交付，等待你的验收</div>
          <div className="btns">
            <button
              className="btn"
              onClick={() => {
                void closeTicket(ticket.number, { reason: 'done' }).then(changed)
              }}
            >
              验收通过，关闭（完成）
            </button>
            <span className="hint">验收不通过：直接在下面回复说明问题，工单会退回指派人。</span>
          </div>
        </div>
      )}

      <div className="conv">
        <div className="tlfilter">
          <span className="lbl">操作方</span>
          <span role="group" aria-label="操作方">
            {(
              [
                ['all', '全部'],
                ['member', '本人'],
                ['agent', 'Agent'],
              ] as [ActorFilter, string][]
            ).map(([v, name]) => (
              <button key={v} type="button" className={`fchip${actor === v ? ' on' : ''}`} onClick={() => setActor(v)}>
                {name}
              </button>
            ))}
          </span>
          <details className="actor-help">
            <summary title={actorHelp} aria-label="操作方筛选说明">
              ⓘ
            </summary>
            <span className="hint">{actorHelp}</span>
          </details>
        </div>
        <BriefBubble
          key={ticket.number}
          ticket={ticket}
          onImageClick={setLightbox}
          onEditBrief={open && can.edit_brief ? () => setDialog('brief') : undefined}
          collapseEmpty
          briefDraftingOpen={briefDraftingOpen}
          onToggleBriefDrafting={() => setBriefDraftingOpen((current) => !current)}
          fetchBriefDrafting={async () => fetchBriefDrafting(ticket.number, ticket.brief.revision)}
        />
        {ticket.timeline.map((e) =>
          e.type === 'reply' ? (
            (actor === 'all' || (actor === 'agent') === (!!e.actor.agent || !!e.actor.via_agent) ? (
              <ReplyBubble
                key={e.seq}
                event={e}
                ticket={ticket}
                onImageClick={setLightbox}
                draftingOpen={draftingSeq === e.seq}
                onToggleDrafting={() => setDraftingSeq((current) => (current === e.seq ? null : e.seq))}
                fetchDrafting={() => fetchReplyDrafting(ticket.number, e.reply_no ?? 0)}
              />
            ) : null)
          ) : (
            <EventLine
              key={e.seq}
              event={e}
              onShowBriefVersion={(revision) =>
                window.dispatchEvent(new CustomEvent('tb:show-brief-version', { detail: revision }))
              }
            />
          ),
        )}
        {ticket.my_draft && <DraftCard draft={ticket.my_draft} ticket={ticket} onChanged={changed} />}
      </div>

      {ticket.my_draft ? (
        <div className="composer ro draft-lock">
          <span>你在这张工单上有一份草稿，发出或丢弃后才能直接回复或修订说明。</span>
          <a className="btn sm ghost" href={`#draft`}>↑ 跳到草稿</a>
        </div>
      ) : open ? (
        <>
          {ticket.kind === 'report' && guestLinks !== null && !hasActiveGuestLink && (
            <div className="warnbox no-guest-link">
              <span>这张反馈没有可用的访客链接，访客看不到回复</span>
              <button type="button" className="btn sm" onClick={() => setDialog('links')}>
                发访客链接
              </button>
            </div>
          )}
          <Composer ticket={ticket} canDeliver={!!can.deliver} onSent={changed} />
        </>
      ) : (
        <div className="composer ro">
          <span>工单已关闭（{closeReasonText(ticket.close_reason)}）</span>
          <button
            className="btn sm ghost"
            onClick={() => {
              void reopenTicket(ticket.number)
                .then(() => {
                  toast('已重新开启。请回复说明原因，让对方知道下一步做什么。')
                  changed()
                })
                .catch((e) => toast(e instanceof ApiError ? e.message : '重开失败'))
            }}
          >
            重新开启
          </button>
        </div>
      )}
    </>
  )

  return (
    <div className="bpage detail">
      <div className="dhead">
        <a
          className="back"
          href={returnTo}
          onClick={(e) => {
            e.preventDefault()
            nav(returnTo)
          }}
        >
          ‹ 列表
        </a>
        <div className="dmeta">
          {ticket.number} · #{ticket.seq} · {ticket.kind === 'task' ? '任务' : '反馈'}
          {ticket.template ? ` · 模板 ${ticket.template}` : ''}
        </div>
        <h1>{ticket.title}</h1>
        <div className="tc-top">
          <StatusBadge />
          {ticket.my_draft && (
            <span className="draft-pill">
              ✎ 有{ticket.my_draft.kind === 'brief_revision' ? '说明修订' : '回复'}草稿待你确认
            </span>
          )}
        </div>
        <button
          type="button"
          className="dmore"
          aria-label="更多操作"
          aria-expanded={mobileMenu}
          onClick={() => setMobileMenu((v) => !v)}
        >
          ⋯
        </button>
        {mobileMenu && (
          <div className="dmenu" role="menu" aria-label="工单操作">
            <button
              type="button"
              role="menuitem"
              onClick={() => {
                setMobileMenu(false)
                setDialog('agent')
              }}
            >
              🤖 让我的 Agent 处理
            </button>
            {ticket.assignee?.member && ticket.assignee.member.id !== me?.id && (
              <button
                type="button"
                role="menuitem"
                onClick={() => {
                  setMobileMenu(false)
                  setDialog('share')
                }}
              >
                📨 发给 {ticket.assignee.member.name}
              </button>
            )}
          </div>
        )}
        {(tier === 'mobile' || tier === 'narrow') && (
          <details className="mmeta">
            <summary>标签 · 指派 · 访客链接</summary>
            <div className="kv">
              <span>标签</span>
              <span>
                <LabelChips labels={ticket.labels ?? []} onToggle={toggleDetailLabel} />{' '}
                <button className="btn sm ghost" onClick={() => setDialog('label')}>
                  ＋
                </button>
              </span>
            </div>
            <div className="kv">
              <span>发起人</span>
              <b>{authorName(ticket.creator)}</b>
              {ticket.creator.member?.deactivated && <DeactivatedBadge />}
            </div>
            <div className="kv">
              <span>指派人</span>
              <span>
                <b>{ticket.assignee?.member?.name ?? '未指派'}</b>{' '}
                {ticket.assignee?.member?.deactivated && <DeactivatedBadge />}{' '}
                <button className="btn sm ghost" onClick={() => setDialog('assign')}>
                  {ticket.assignee?.member ? '改派' : '指派'}
                </button>
              </span>
            </div>
            <div className="kv">
              <span>等待方</span>
              <b>{closed ? '无（已关闭）' : turnLabel(ticket.waiting_on, ticket.viewer.your_turn)}</b>
            </div>
            <div className="kv">
              <span>协作者</span>
              <span>
                {(ticket.collaborators ?? []).map((c) => c.name).join('、') || '暂无'}{' '}
                <button className="btn sm ghost" onClick={() => setDialog('collaborators')}>
                  管理
                </button>
              </span>
            </div>
            <div className="kv">
              <span>访客链接</span>
              <span>
                <button className="btn sm ghost" onClick={() => setDialog('links')}>
                  管理
                </button>
              </span>
            </div>
            <div className="kv">
              <span>更多</span>
              <span>
                {open ? (
                  <button className="btn sm ghost" onClick={() => setDialog('close')}>
                    关闭工单
                  </button>
                ) : (
                  <button className="btn sm ghost" onClick={() => setDialog('reopen')}>
                    重新开启
                  </button>
                )}
              </span>
            </div>
            <AccessRecordsPanel data={accessRecords} />
          </details>
        )}
        <div className="actrow" role="region" aria-label="工单操作">
          <button className="btn sm ghost" onClick={() => setDialog('label')}>
            🏷 标签
          </button>
          <button className="btn sm ghost" onClick={() => setDialog('assign')}>
            👤 {ticket.assignee?.member ? '改派' : '指派'}
          </button>
          <button className="btn sm ghost handoff-btn" onClick={() => setDialog('agent')}>
            🤖 让我的 Agent 处理
          </button>
          {ticket.assignee?.member && ticket.assignee.member.id !== me?.id && (
            <button className="btn sm ghost handoff-btn" onClick={() => setDialog('share')}>
              📨 发给 {ticket.assignee.member.name}
            </button>
          )}
          <button className="btn sm ghost" onClick={() => setDialog('links')}>
            🔗 访客链接
          </button>
          {(can.manage_collaborators || can.leave_collaborators) && (
            <button className="btn sm ghost" onClick={() => setDialog('collaborators')}>
              🤝 协作者
            </button>
          )}
          {open ? (
            <button className="btn sm ghost" onClick={() => setDialog('close')}>
              ✔ 关闭
            </button>
          ) : (
            <button className="btn sm ghost" onClick={() => setDialog('reopen')}>
              ↺ 重开
            </button>
          )}
        </div>
        {tier === 'medium' && (
          <div className="dsummary" role="region" aria-label="工单信息摘要">
            <span>{ticket.kind === 'task' ? '任务' : '反馈'}</span>
            <span>{open ? turnLabel(ticket.waiting_on, ticket.viewer.your_turn) : '已关闭'}</span>
            <span>指派 {ticket.assignee?.member?.name ?? '未指派'}</span>
            <span>{`协作者 ${(ticket.collaborators ?? []).length} 人`}</span>
            <span>{(ticket.labels ?? []).join('、') || '无标签'}</span>
            <span>{`访客链接 ${guestLinks?.length ?? 0} 条`}</span>
            <button type="button" className="btn sm ghost" onClick={() => setAllInfoOpen((v) => !v)}>
              {allInfoOpen ? '收起 ▴' : '全部信息 ▾'}
            </button>
          </div>
        )}
      </div>

      {tier === 'medium' && allInfoOpen && (
        <TicketInfoColumn
          ticket={ticket}
          links={guestLinks}
          knownUrls={knownLinkUrls}
          accessRecords={accessRecords}
          onAssign={() => setDialog('assign')}
          onCollaborators={() => setDialog('collaborators')}
          onLabels={() => setDialog('label')}
          onLinks={() => setDialog('links')}
          onLabelToggle={toggleDetailLabel}
        />
      )}

      {tier === 'wide' ? (
        <div className="detail-body">
          <div className="detail-main">{detailMain}</div>
          <TicketInfoColumn
            ticket={ticket}
            links={guestLinks}
            knownUrls={knownLinkUrls}
            accessRecords={accessRecords}
            onAssign={() => setDialog('assign')}
            onCollaborators={() => setDialog('collaborators')}
            onLabels={() => setDialog('label')}
            onLinks={() => setDialog('links')}
            onLabelToggle={toggleDetailLabel}
          />
        </div>
      ) : (
        <div className="detail-main">{detailMain}</div>
      )}

      {dialog === 'label' && <LabelDialog ticket={ticket} onChanged={changed} onClose={() => setDialog(null)} />}
      {dialog === 'brief' && <BriefEditDialog ticket={ticket} onChanged={changed} onClose={() => setDialog(null)} />}
      {dialog === 'assign' && (
        <AssignDialog
          ticket={ticket}
          members={members}
          me={me}
          onChanged={changed}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog === 'collaborators' && (
        <CollaboratorsDialog
          ticket={ticket}
          members={members}
          me={me}
          onChanged={changed}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog === 'links' && (
        <LinksDialog
          ticket={ticket}
          links={guestLinks}
          reload={reloadGuestLinks}
          onCreated={(link) => {
            setKnownLinkUrls((m) => ({ ...m, [link.id]: absoluteUrl(link.url) }))
          }}
          onChanged={changed}
          onClose={() => setDialog(null)}
        />
      )}
      {(dialog === 'agent' || dialog === 'share') && (
        <HandoffDialog
          handoffText={ticket.handoff_text ?? ''}
          handoffShareText={ticket.handoff_share_text}
          assigneeName={ticket.assignee?.member?.name ?? null}
          mode={dialog}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog === 'close' && <CloseDialog ticket={ticket} onChanged={changed} onClose={() => setDialog(null)} />}
      {dialog === 'reopen' && (
        <Confirm
          title="重新开启"
          body="重新开启后访客链接恢复可回复；请回复说明原因，让对方知道下一步做什么。"
          yesLabel="重新开启"
          onYes={() => {
            setDialog(null)
            void reopenTicket(ticket.number)
              .then(() => {
                toast('已重新开启。请回复说明原因，让对方知道下一步做什么。')
                changed()
              })
              .catch((e) => toast(e instanceof ApiError ? e.message : '重开失败'))
          }}
          onCancel={() => setDialog(null)}
        />
      )}
      {lightbox && <Lightbox url={lightbox} onClose={() => setLightbox(null)} />}
    </div>
  )
}

/* ---------------- 回复输入区（带交付标记与附件） ---------------- */
function DraftCard({
  draft,
  ticket,
  onChanged,
}: {
  draft: DraftView
  ticket: MemberTicket
  onChanged: () => void
}) {
  const [confirmDiscard, setConfirmDiscard] = useState(false)
  const [changed, setChanged] = useState<{ throughSeq: number; count: number } | null>(null)
  const [editing, setEditing] = useState(false)
  const [body, setBody] = useState(draft.request.body_md || draft.request.brief_md || '')
  const [title, setTitle] = useState(draft.request.title ?? '')
  const [fields, setFields] = useState<Record<string, string>>(() => {
    const initial = { ...(draft.request.fields ?? {}) }
    for (const field of ticket.brief.template_fields ?? []) {
      if (!(field.key in initial)) initial[field.key] = ticket.brief.fields?.[field.key] ?? ''
    }
    return initial
  })
  const [deliver, setDeliver] = useState(!!draft.request.deliver)
  const [showInstruction, setShowInstruction] = useState(false)
  const [busy, setBusy] = useState(false)
  const agent = draft.author.agent?.name ?? 'Agent'
  const member = draft.author.member?.name ?? ''
  const closed = ticket.status === 'closed'
  const brief = draft.kind === 'brief_revision'
  const revisionStale = brief && ticket.brief.revision !== draft.base.brief_revision
  const newEvents = ticket.timeline.filter((e) => e.seq > draft.base.timeline_seq)

  async function send(confirmThroughSeq?: number) {
    setBusy(true)
    try {
      await sendDraft(draft.id, draft.version, {
        ...(confirmThroughSeq === undefined ? {} : { confirmThroughSeq }),
        ...(deliver !== !!draft.request.deliver ? { deliver } : {}),
      })
      toast('草稿已发出')
      onChanged()
    } catch (e) {
      if (e instanceof ApiError && e.code === 'ticket_changed') {
        setChanged({ throughSeq: e.body?.through_seq ?? 0, count: e.body?.new_events ?? 0 })
      } else if (e instanceof ApiError) {
        toast(`${e.code}：${e.message}`)
      } else {
        toast('发出失败，请重试')
      }
    } finally {
      setBusy(false)
    }
  }

  async function save() {
    if (!body.trim() && !/att:[A-Za-z0-9_]+/.test(body)) {
      toast('422 empty_reply：空回复会被拒绝，请写内容或保留附件。')
      return
    }
    setBusy(true)
    try {
      if (brief) {
        await updateDraft(draft.id, draft.version, {
          ...(draft.request.title != null ? { title } : {}),
          ...(ticket.brief.template_fields?.length
            ? { fields, attachment_ids: draft.request.attachment_ids ?? ticket.brief.attachment_ids }
            : { brief_md: body, attachment_ids: draft.request.attachment_ids ?? ticket.brief.attachment_ids }),
          base_revision: draft.request.base_revision,
        })
      } else {
        await updateDraft(draft.id, draft.version, {
          body_md: body,
          attachment_ids: draft.request.attachment_ids ?? [],
          deliver,
        })
      }
      setEditing(false)
      toast('草稿已保存为新一版')
      onChanged()
    } catch (e) {
      if (e instanceof ApiError) {
        toast(`${e.code}：${e.message}`)
        if (e.code === 'draft_version_conflict') onChanged()
      } else {
        toast('保存失败，请重试')
      }
    } finally {
      setBusy(false)
    }
  }

  function instructionText() {
    const base = window.location.origin
    return `请修改我在 agent-taskboard 工单 ${ticket.number}「${ticket.title}」上的${brief ? '说明修订' : '回复'}草稿 ${draft.id}（当前第 ${draft.version} 版）。
1. 凭证：用环境变量 TASKBOARD_TOKEN；读不到或返回 401 就停下来告诉我，不要用密码登录。
2. 先读当前草稿：curl -H "Authorization: Bearer $TASKBOARD_TOKEN" "${base}/api/v1/drafts/${draft.id}"
   我可能亲手改过，以最新一版为准，不要把我的改动改回去。${brief ? '说明当前以第 ' + ticket.brief.revision + ' 版为准。' : `需要上下文时再读工单：${base}/api/v1/tickets/${ticket.number}?format=markdown`}
3. 按我下面的意见修改，然后整份写回：PUT ${base}/api/v1/drafts/${draft.id}，请求体 {"base_version": <刚读到的 version>, "request": {…完整内容…}}。
   返回 409 draft_version_conflict，说明你读完之后草稿又被改过；重新读，在最新版上改。
4. 改完把查看地址 ${absoluteUrl(draft.preview_url)} 发给我，说明这一版改了什么。发出或丢弃由我在网页上做，你做不了。

我的修改意见：`
  }

  async function copyInstruction() {
    const ok = await copyText(instructionText())
    toast(ok ? '修改指令已复制' : '复制失败，请手动选择文字复制')
    setShowInstruction(true)
  }

  return (
    <article id="draft" className="draft-card" aria-label={brief ? '说明修订草稿' : '回复草稿'}>
      <div className="draft-label">草稿 · 未发出 · 只有你能看到</div>
      <div className="draft-meta">
        {agent}（{member}的 Agent）写的{brief ? '说明修订' : '回复'}草稿 · 第 {draft.version} 版
        {deliver && <span className="draft-deliver-pill">将作为交付</span>}
      </div>
      {newEvents.length > 0 && (
        <div className="draft-new-events">
          ↓ 草稿写成（{fmtBeijing(draft.versions[draft.versions.length - 1].at)}）之后的新变化
        </div>
      )}
      {closed && <div className="draft-blocked">工单已关闭，不能发出回复草稿；请先重新开启，或丢弃草稿。</div>}
      {revisionStale && (
        <div className="draft-blocked">说明已被访客修订到第 {ticket.brief.revision} 版，这份草稿基于第 {draft.base.brief_revision} 版；请让 Agent 重写修订。</div>
      )}
      {editing ? (
        <>
          {brief && draft.request.title != null && (
            <input aria-label="草稿标题" value={title} onChange={(e) => setTitle(e.target.value)} />
          )}
          {brief && (ticket.brief.template_fields?.length ?? 0) > 0 ? (
            (ticket.brief.template_fields ?? []).map((field) => (
              <label className="draft-edit-field" key={field.key}>
                <span>{field.title}</span>
                <textarea aria-label={field.title} value={fields[field.key] ?? ''} onChange={(e) => setFields({ ...fields, [field.key]: e.target.value })} />
              </label>
            ))
          ) : (
            <textarea aria-label="草稿正文" value={brief ? draft.request.brief_md ?? body : body} onChange={(e) => setBody(e.target.value)} />
          )}
          <AttachmentChips attachments={draft.attachments} />
          <div className="draft-actions">
            <button type="button" className="btn ghost" disabled={busy} onClick={() => setEditing(false)}>
              取消
            </button>
            <span className="sp" />
            <button type="button" className="btn" disabled={busy} onClick={() => void save()}>
              保存草稿
            </button>
          </div>
        </>
      ) : (
        <>
          {brief ? (
            <DraftBriefChanges draft={draft} ticket={ticket} />
          ) : (
            <>
              <div className="draft-body">{draft.request.body_md}</div>
              <AttachmentChips attachments={draft.attachments} />
            </>
          )}
          <div className="draft-actions">
            <button type="button" className="btn ghost" onClick={() => void copyInstruction()}>
              复制修改指令
            </button>
            <button type="button" className="btn ghost" onClick={() => setEditing(true)}>
              直接修改
            </button>
            {!brief && (
              <button
                type="button"
                className={`btn ghost${deliver ? ' on' : ''}`}
                aria-pressed={deliver}
                onClick={() => setDeliver((v) => !v)}
              >
                {deliver ? '✓ 作为交付发出' : '作为交付发出'}
              </button>
            )}
            <span className="sp" />
            <button type="button" className="btn danger ghost" disabled={busy} onClick={() => setConfirmDiscard(true)}>
              丢弃
            </button>
            <button type="button" className="btn" disabled={busy || closed || revisionStale} onClick={() => void send()}>
              {brief ? '发出修订' : '发出'}
            </button>
          </div>
          {showInstruction && (
            <details className="instruction-preview">
              <summary>指令预览</summary>
              <pre>{instructionText()}</pre>
            </details>
          )}
        </>
      )}
      {confirmDiscard && (
        <Confirm
          title="丢弃草稿？"
          body={<div>丢弃后工单上不会留下这份草稿的痕迹。</div>}
          yesLabel="丢弃"
          danger
          onYes={() => {
            setConfirmDiscard(false)
            void discardDraft(draft.id).then(() => {
              toast('草稿已丢弃')
              onChanged()
            })
          }}
          onCancel={() => setConfirmDiscard(false)}
        />
      )}
      {changed && (
        <Modal title={`草稿写成后工单有 ${changed.count} 条新变化`} onClose={() => setChanged(null)}>
          <ul className="draft-change-list">
            {newEvents.map((e) => (
              <li key={e.seq}>
                <b>{authorName(e.actor)}</b> {eventText(e)} · {fmtShort(e.at)}
                {e.body_md ? `：${e.body_md}` : ''}
              </li>
            ))}
          </ul>
          <div className="mrow">
            <button className="btn ghost" onClick={() => void copyInstruction().then(() => setChanged(null))}>
              先不发，复制修改指令给 Agent
            </button>
            <button className="btn ghost" onClick={() => setChanged(null)}>
              回去看看
            </button>
            <span className="sp" />
            <button
              className="btn"
              onClick={() => {
                const seq = changed.throughSeq
                setChanged(null)
                void send(seq)
              }}
            >
              仍然发出
            </button>
          </div>
        </Modal>
      )}
    </article>
  )
}

function DraftBriefChanges({ draft, ticket }: { draft: DraftView; ticket: MemberTicket }) {
  const oldFields = ticket.brief.fields ?? {}
  const newFields = { ...oldFields, ...(draft.request.fields ?? {}) }
  const oldIDs = new Set(ticket.brief.attachment_ids)
  const newIDs = new Set(draft.request.attachment_ids ?? ticket.brief.attachment_ids)
  const attachmentName = (id: string) =>
    ticket.attachments.find((a) => a.id === id)?.filename ??
    draft.attachments.find((a) => a.id === id)?.filename ?? id
  const row = (label: string, oldText: string, newText: string) => (
    <div className="draft-field-change" key={label}>
      <span className="name">{label}</span>
      {oldText === newText ? (
        <span className="unchanged">（未改）</span>
      ) : (
        <>
          <span className="old">{oldText || '（空）'}</span>
          <span className="new">{newText || '（空）'}</span>
        </>
      )}
    </div>
  )
  return (
    <div className="draft-brief-changes">
      {row('标题', ticket.title, draft.request.title ?? ticket.title)}
      {(ticket.brief.template_fields ?? []).length > 0
        ? (ticket.brief.template_fields ?? []).map((field) =>
            row(field.title, oldFields[field.key] ?? '', newFields[field.key] ?? ''),
          )
        : row('说明', ticket.brief.markdown, draft.request.brief_md ?? ticket.brief.markdown)}
      {[...new Set([...oldIDs, ...newIDs])].map((id) =>
        row(`附件：${attachmentName(id)}`, oldIDs.has(id) ? '有' : '无', newIDs.has(id) ? '有' : '无'),
      )}
    </div>
  )
}

function Composer({
  ticket,
  canDeliver,
  onSent,
}: {
  ticket: MemberTicket
  canDeliver: boolean
  onSent: () => void
}) {
  const [body, setBody] = useState('')
  const [deliver, setDeliver] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [down, setDown] = useState<string | null>(null)
  const [uploading, setUploading] = useState(0)
  const [bigWarn, setBigWarn] = useState<string | null>(null)
  // 同一次发送动作复用同一个 Idempotency-Key（ADR 0002「放心自动重试」，评审
  // P2-13）：网络失败/5xx 自动重试不会产生重复回复（照访客 Composer 的做法）。
  const idemKeyRef = useRef<string | undefined>(undefined)
  const sendingRef = useRef(false)
  const retryTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const stateRef = useRef({ body, deliver })
  stateRef.current = { body, deliver }

  useEffect(() => {
    return () => {
      if (retryTimer.current) clearTimeout(retryTimer.current)
    }
  }, [])

  function scheduleRetry() {
    if (retryTimer.current) clearTimeout(retryTimer.current)
    retryTimer.current = setTimeout(() => void trySend(), 3000)
  }

  async function send() {
    // 新的用户动作：清掉上一条的服务中断提示再进入发送。
    setDown(null)
    await trySend()
  }

  async function trySend() {
    if (sendingRef.current) {
      scheduleRetry() // 上一轮还没结束，稍后再看
      return
    }
    const { body: text, deliver: mark } = stateRef.current
    if (!text.trim() && !/att:[A-Za-z0-9_]+/.test(text)) {
      setNotice('422 empty_reply：空回复会被拒绝，请写内容或添加附件。')
      idemKeyRef.current = undefined
      return
    }
    sendingRef.current = true
    try {
      if (!idemKeyRef.current) idemKeyRef.current = crypto.randomUUID()
      await replyTicket(ticket.number, { body_md: text, ...(mark ? { deliver: true } : {}) }, idemKeyRef.current)
      // 成功：这次动作结束，下一次发送是新动作（新 key）。
      idemKeyRef.current = undefined
      setBody('')
      setDeliver(false)
      setNotice(null)
      setDown(null)
      onSent()
      toast(mark ? '✓ 已交付，轮到创建者验收' : '✓ 已发送')
    } catch (e) {
      if (e instanceof ApiError) {
        if (e.status >= 500 || e.status === 429) {
          serviceDown()
          return
        }
        // 4xx：内容被拒，改完再发是新动作。
        idemKeyRef.current = undefined
        setNotice(e.hint ? `${e.code}：${e.message}（${e.hint}）` : `${e.code}：${e.message}`)
      } else {
        serviceDown() // 网络错误（TypeError: Failed to fetch）等
      }
    } finally {
      sendingRef.current = false
    }
  }

  function serviceDown() {
    setDown('服务更新中，回复没有丢失，正在自动重试…')
    scheduleRetry()
  }

  async function handleFiles(files: File[]) {
    for (const file of files) {
      setUploading((n) => n + 1)
      try {
        const att = await uploadMemberAttachment(ticket.number, file)
        setBigWarn(null)
        setBody((b) => b + att.markdown)
      } catch (e) {
        if (e instanceof ApiError && e.status === 413) {
          setBigWarn(`⚠ ${file.name}：${e.message}${e.hint ? `。${e.hint}` : ''}`)
        } else {
          setNotice(e instanceof ApiError ? `${e.code}：${e.message}` : `${file.name} 上传失败，请重试`)
        }
      } finally {
        setUploading((n) => n - 1)
      }
    }
  }

  return (
    <div className="composer mcard">
      {bigWarn && <div className="toobig">{bigWarn}</div>}
      {down && (
        <div className="notice down" role="status">
          {down}
        </div>
      )}
      {notice && (
        <div className="notice err" role="alert">
          {notice}
        </div>
      )}
      <textarea
        placeholder="写回复（Markdown），可以直接粘贴截图…"
        value={body}
        onChange={(e) => setBody(e.target.value)}
      />
      <div className="crow frow">
        <button
          type="button"
          className="btn ghost sm"
          onClick={() => document.getElementById('member-file-input')?.click()}
        >
          📎 附件
        </button>
        <input
          id="member-file-input"
          type="file"
          multiple
          hidden
          onChange={(e) => {
            const files = Array.from(e.target.files ?? [])
            e.target.value = ''
            void handleFiles(files)
          }}
        />
        <button
          type="button"
          className={`btn ghost sm${deliver ? ' on' : ''}`}
          disabled={!canDeliver}
          title={canDeliver ? '' : '只有指派人一方能交付'}
          onClick={() => setDeliver(!deliver)}
        >
          {deliver ? '✓ 作为交付发送' : '标记为交付'}
        </button>
        <span className="hint">回复会记入时间线</span>
        <span className="sp" />
        <button type="button" className="btn" disabled={uploading > 0} onClick={() => void send()}>
          {uploading > 0 ? '上传附件中…' : '发送'}
        </button>
      </div>
    </div>
  )
}
