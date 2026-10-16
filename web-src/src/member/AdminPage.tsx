// 管理页（#42，仅管理员）：成员账号（创建、停用）与标签治理（停用、改名、合并、
// 加维度、改别名指向）。备份状态与更新是 #45 的内容，这里留出明确占位。
// 非管理员在 UI 上看不到本页入口；直接访问 /admin 时页面只显示无权限提示，
// 后端接口同样拒绝（403 admin_required，双保险）。
import { useEffect, useState } from 'react'
import { ApiError } from '../api'
import {
  addDimension,
  createMember,
  deactivateLabel,
  deactivateMemberAccount,
  fetchDictionary,
  fetchMemberAccounts,
  mergeLabels,
  renameLabel,
  repointAlias,
  type CreatedMember,
  type Dictionary,
  type LabelDimension,
  type MemberAccount,
  type Me,
} from './memberApi'
import { Confirm, Modal, copyText, toast } from './ui'
import { chipClass } from './TicketsPage'
import { SystemPanel } from './admin/SystemPanel'

type AdminTab = 'members' | 'governance'

export function AdminPage({ me }: { me: Me }) {
  const [tab, setTab] = useState<AdminTab>('members')

  if (!me.is_admin) {
    return (
      <div className="bpage">
        <div className="ecard" style={{ marginTop: 12 }}>
          管理功能只对管理员开放。
        </div>
      </div>
    )
  }

  return (
    <div className="bpage">
      <div className="mcard" style={{ marginTop: 12 }}>
        <div className="sec-t" style={{ marginTop: 0 }}>
          管理
        </div>
        <div className="hint">仅管理员可见。</div>
        <div className="frow" role="tablist" aria-label="管理分区">
          <button
            className={`fchip${tab === 'members' ? ' on' : ''}`}
            role="tab"
            aria-selected={tab === 'members'}
            onClick={() => setTab('members')}
          >
            成员账号
          </button>
          <button
            className={`fchip${tab === 'governance' ? ' on' : ''}`}
            role="tab"
            aria-selected={tab === 'governance'}
            onClick={() => setTab('governance')}
          >
            标签治理
          </button>
        </div>
      </div>
      {tab === 'members' ? <MembersTab me={me} /> : <GovernanceTab />}
      {/* 系统状态（#45）：备份状态（最近一次成功备份、超 36 小时标红、
          「备份未离开本机」）与版本更新（管理页按钮与命令行、管理 API 共用
          同一套逻辑）。 */}
      <SystemPanel />
    </div>
  )
}

/* ---------------- 成员账号 ---------------- */

function MembersTab({ me }: { me: Me }) {
  const [members, setMembers] = useState<MemberAccount[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [created, setCreated] = useState<CreatedMember | null>(null)
  const [revoking, setRevoking] = useState<MemberAccount | null>(null)

  async function refresh() {
    try {
      setMembers(await fetchMemberAccounts())
      setError(null)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '读取成员失败')
    }
  }

  useEffect(() => {
    void refresh()
  }, [])

  async function doDeactivate() {
    if (!revoking) return
    try {
      await deactivateMemberAccount(revoking.id)
      toast(`✓ 已停用 ${revoking.name}；不能登录，历史内容保留`)
      await refresh()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '停用失败')
    } finally {
      setRevoking(null)
    }
  }

  return (
    <div className="mcard">
      <div className="hint" style={{ marginTop: 0 }}>
        创建和停用成员账号只能在网页上操作（管理员）；账号只能停用，不能删除。
      </div>
      {error && <div className="errbox">{error}</div>}
      <table className="mini-tbl">
        <tbody>
          {(members ?? []).map((m) => (
            <tr key={m.id} data-member={m.username}>
              <td>
                <b>{m.name}</b>
                {m.name === me.name && <span className="hint">（我）</span>}
                <div className="hint mono">{m.username}</div>
              </td>
              <td>
                {m.active ? <span className="badge open">使用中</span> : <span className="badge closed">已停用</span>}
              </td>
              <td>
                {m.name === me.name ? (
                  <span className="hint">—</span>
                ) : m.active ? (
                  <button className="btn sm ghost" onClick={() => setRevoking(m)}>
                    停用
                  </button>
                ) : null}
              </td>
            </tr>
          ))}
          {members !== null && members.length === 0 && (
            <tr>
              <td className="hint" colSpan={3}>
                还没有成员。
              </td>
            </tr>
          )}
        </tbody>
      </table>
      <div className="btnrow">
        <button className="btn" onClick={() => setCreating(true)}>
          ＋ 新建成员账号
        </button>
      </div>

      {creating && (
        <CreateMemberDialog
          onClose={() => setCreating(false)}
          onCreated={async (c) => {
            setCreated(c)
            setCreating(false)
            await refresh()
          }}
        />
      )}
      {created && <InitialPasswordDialog created={created} onClose={() => setCreated(null)} />}
      {revoking && (
        <Confirm
          title={`停用 ${revoking.name}`}
          body={<>停用后不能登录，名下 Agent token 一并失效；已有的回复和事件保留。</>}
          yesLabel="停用"
          danger
          onYes={() => void doDeactivate()}
          onCancel={() => setRevoking(null)}
        />
      )}
    </div>
  )
}

function CreateMemberDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void
  onCreated: (c: CreatedMember) => void
}) {
  const [username, setUsername] = useState('')
  const [name, setName] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit() {
    if (!username.trim()) {
      setError('登录名不能为空')
      return
    }
    setBusy(true)
    try {
      onCreated(await createMember(username.trim(), name.trim()))
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal title="新建成员账号" onClose={onClose}>
      <div className="fld">
        <label htmlFor="mm-username">登录名（成员用它登录）</label>
        <input
          id="mm-username"
          placeholder="如 zhao"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
        />
      </div>
      <div className="fld">
        <label htmlFor="mm-name">姓名（显示在署名里）</label>
        <input id="mm-name" placeholder="例如：赵工" value={name} onChange={(e) => setName(e.target.value)} />
      </div>
      <div className="hint">
        账号由管理员创建，初始密码会显示一次；成员用它首次登录。MVP 不接 LDAP/SSO。
      </div>
      {error && <div className="errbox">{error}</div>}
      <div className="mrow">
        <button className="btn ghost" onClick={onClose}>
          取消
        </button>
        <span className="sp" />
        <button className="btn" onClick={() => void submit()} disabled={busy}>
          创建
        </button>
      </div>
    </Modal>
  )
}

/** 初始密码只显示这一次：显示 + 复制，关闭后不再出现。 */
function InitialPasswordDialog({ created, onClose }: { created: CreatedMember; onClose: () => void }) {
  return (
    <Modal title={`✓ 已创建 ${created.member.name}，初始密码只显示这一次`} onClose={onClose}>
      <div className="linkbox mono" data-initial-password>
        {created.initial_password}
      </div>
      <div className="hint">
        登录名 <span className="mono">{created.member.username}</span>
        。请把登录名和初始密码交给这位成员；首次登录后即以本人身份操作。
      </div>
      <div className="mrow">
        <button className="btn ghost" onClick={onClose}>
          关闭
        </button>
        <span className="sp" />
        <button
          className="btn"
          onClick={() =>
            void copyText(`${created.member.username} / ${created.initial_password}`).then((ok) =>
              toast(ok ? '已复制「登录名 / 初始密码」' : '复制失败，请手动选择复制'),
            )
          }
        >
          复制
        </button>
      </div>
    </Modal>
  )
}

/* ---------------- 标签治理 ---------------- */

function GovernanceTab() {
  const [dict, setDict] = useState<Dictionary | null>(null)
  const [dim, setDim] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [renaming, setRenaming] = useState<string | null>(null)
  const [merging, setMerging] = useState<string | null>(null)
  const [deactivating, setDeactivating] = useState<string | null>(null)
  const [addingDim, setAddingDim] = useState(false)
  const [repointing, setRepointing] = useState(false)

  async function refresh() {
    try {
      const d = await fetchDictionary()
      setDict(d)
      setError(null)
      return d
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '读取字典失败')
      return null
    }
  }

  useEffect(() => {
    void refresh()
  }, [])

  const dimension: LabelDimension | undefined = dict?.dimensions.find((d) => d.name === dim) ?? dict?.dimensions[0]

  /** 执行一项治理操作：成功后刷新字典并返回 true（调用方据此关掉弹窗）。 */
  async function govern(action: () => Promise<void>): Promise<boolean> {
    try {
      await action()
      await refresh()
      return true
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '操作失败')
      return false
    }
  }

  return (
    <div className="mcard">
      <div className="hint" style={{ marginTop: 0 }}>
        停用、改名、合并标签值，合并时历史工单上的标签一并改写；添加新维度、修改别名的指向也只有管理员能做。
      </div>
      {error && <div className="errbox">{error}</div>}
      <div className="inrow">
        <span className="flbl" style={{ margin: 0 }}>
          维度
        </span>
        <select className="sel" aria-label="治理维度" value={dimension?.name ?? ''} onChange={(e) => setDim(e.target.value)}>
          {(dict?.dimensions ?? []).map((d) => (
            <option key={d.name} value={d.name}>
              {d.name}（{d.values.length}）
            </option>
          ))}
        </select>
      </div>
      {dimension && (
        <table className="mini-tbl">
          <tbody>
            {dimension.values.map((v) => (
              <tr key={v.value} data-label={v.label}>
                <td>
                  <span className={chipClass(v.label)}>{v.label}</span>
                  {v.aliases.length > 0 && (
                    <div className="hint">
                      别名：{v.aliases.map((a) => (
                        <span key={a} className="aliasbox">
                          {a}
                        </span>
                      ))}
                    </div>
                  )}
                </td>
                <td>
                  <button className="btn sm ghost" onClick={() => setRenaming(v.label)}>
                    改名
                  </button>
                  <button className="btn sm ghost" onClick={() => setMerging(v.label)}>
                    合并到…
                  </button>
                  <button className="btn sm ghost" onClick={() => setDeactivating(v.label)}>
                    停用
                  </button>
                </td>
              </tr>
            ))}
            {dimension.values.length === 0 && (
              <tr>
                <td className="hint" colSpan={2}>
                  此维度还没有值。
                </td>
              </tr>
            )}
          </tbody>
        </table>
      )}
      <div className="btnrow">
        <button className="btn ghost" onClick={() => setAddingDim(true)}>
          ＋ 新建维度
        </button>
        <button className="btn ghost" onClick={() => setRepointing(true)}>
          改别名指向
        </button>
      </div>

      {renaming && dimension && (
        <RenameDialog
          label={renaming}
          onClose={() => setRenaming(null)}
          onSubmit={(to) =>
            void govern(async () => {
              const r = await renameLabel(renaming, to)
              toast(`✓ 已改名，${r.tickets_updated} 张工单上的标签已改写`)
            }).then((ok) => ok && setRenaming(null))
          }
        />
      )}
      {merging && dimension && (
        <MergeDialog
          label={merging}
          others={dimension.values.filter((v) => v.label !== merging)}
          onClose={() => setMerging(null)}
          onSubmit={(into) =>
            void govern(async () => {
              const r = await mergeLabels(merging, into)
              toast(`✓ 已合并到 ${into}，${r.tickets_updated} 张工单的标签已改写`)
            }).then((ok) => ok && setMerging(null))
          }
        />
      )}
      {deactivating && (
        <Confirm
          title={`停用 ${deactivating}`}
          body={<>停用后不能再打这个值；已有工单上的标签保留。</>}
          yesLabel="停用"
          danger
          onYes={() =>
            void govern(async () => {
              await deactivateLabel(deactivating)
              toast(`✓ 已停用 ${deactivating}`)
            }).then((ok) => ok && setDeactivating(null))
          }
          onCancel={() => setDeactivating(null)}
        />
      )}
      {addingDim && (
        <AddDimensionDialog
          onClose={() => setAddingDim(false)}
          onSubmit={(name) =>
            void govern(async () => {
              await addDimension(name)
              setDim(name)
              toast(`✓ 已新建维度「${name}」`)
            }).then((ok) => ok && setAddingDim(false))
          }
        />
      )}
      {repointing && dict && (
        <RepointDialog
          dict={dict}
          currentDimension={dimension?.name ?? ''}
          onClose={() => setRepointing(false)}
          onSubmit={(alias, label) =>
            void govern(async () => {
              await repointAlias(alias, label)
              toast(`✓ 别名「${alias}」已改指向 ${label}`)
            }).then((ok) => ok && setRepointing(false))
          }
        />
      )}
    </div>
  )
}

function RenameDialog({ label, onClose, onSubmit }: { label: string; onClose: () => void; onSubmit: (to: string) => void }) {
  const [to, setTo] = useState(label.includes(':') ? label.split(':').slice(1).join(':') : label)
  const [busy, setBusy] = useState(false)
  return (
    <Modal title={`改名 · ${label}`} onClose={onClose}>
      <input value={to} aria-label="新值" onChange={(e) => setTo(e.target.value)} />
      <div className="hint">改名后，历史工单上的这个标签一并改写。</div>
      <div className="mrow">
        <button className="btn ghost" onClick={onClose}>
          取消
        </button>
        <span className="sp" />
        <button
          className="btn"
          disabled={busy || !to.trim()}
          onClick={() => {
            setBusy(true)
            onSubmit(to.trim())
          }}
        >
          改名
        </button>
      </div>
    </Modal>
  )
}

function MergeDialog({
  label,
  others,
  onClose,
  onSubmit,
}: {
  label: string
  others: { label: string; value: string }[]
  onClose: () => void
  onSubmit: (into: string) => void
}) {
  const [into, setInto] = useState(others[0]?.label ?? '')
  const [busy, setBusy] = useState(false)
  return (
    <Modal title={`合并 · ${label}`} onClose={onClose}>
      <div className="fldlab">合并到</div>
      <select className="sel" aria-label="合并目标" value={into} onChange={(e) => setInto(e.target.value)}>
        {others.map((o) => (
          <option key={o.label} value={o.label}>
            {o.label}
          </option>
        ))}
      </select>
      <div className="warnbox">
        合并时，历史工单上的标签「{label}」会全部改写成目标值；别名一并转移，原值停用。
      </div>
      <div className="mrow">
        <button className="btn ghost" onClick={onClose}>
          取消
        </button>
        <span className="sp" />
        <button
          className="btn danger"
          disabled={busy || !into}
          onClick={() => {
            setBusy(true)
            onSubmit(into)
          }}
        >
          合并
        </button>
      </div>
    </Modal>
  )
}

function AddDimensionDialog({ onClose, onSubmit }: { onClose: () => void; onSubmit: (name: string) => void }) {
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  return (
    <Modal title="新建维度" onClose={onClose}>
      <input placeholder="维度名，如：客户" value={name} aria-label="维度名" onChange={(e) => setName(e.target.value)} />
      <div className="hint">新维度只能由管理员添加；建好后成员就能往里加值。</div>
      <div className="mrow">
        <button className="btn ghost" onClick={onClose}>
          取消
        </button>
        <span className="sp" />
        <button
          className="btn"
          disabled={busy || !name.trim()}
          onClick={() => {
            setBusy(true)
            onSubmit(name.trim())
          }}
        >
          创建
        </button>
      </div>
    </Modal>
  )
}

/** 改别名指向：从全部别名里选一个，改指向当前维度（或任选维度）的标准值。 */
function RepointDialog({
  dict,
  currentDimension,
  onClose,
  onSubmit,
}: {
  dict: Dictionary
  currentDimension: string
  onClose: () => void
  onSubmit: (alias: string, label: string) => void
}) {
  const aliases: { alias: string; from: string }[] = []
  for (const d of dict.dimensions) {
    for (const v of d.values) {
      for (const a of v.aliases) aliases.push({ alias: a, from: v.label })
    }
  }
  const [dim, setDim] = useState(currentDimension)
  const [alias, setAlias] = useState(aliases[0]?.alias ?? '')
  const [value, setValue] = useState('')
  const dimension = dict.dimensions.find((d) => d.name === dim)
  const target = dimension?.values.find((v) => v.value === value) ?? dimension?.values[0]

  if (aliases.length === 0) {
    return (
      <Modal title="改别名指向" onClose={onClose}>
        <div className="hint">字典里还没有别名；先在「工具 → 标签字典与别名」里添加。</div>
        <div className="mrow">
          <span className="sp" />
          <button className="btn" onClick={onClose}>
            知道了
          </button>
        </div>
      </Modal>
    )
  }

  return (
    <Modal title="改别名指向" onClose={onClose}>
      <div className="fldlab">别名</div>
      <select className="sel" aria-label="别名" value={alias} onChange={(e) => setAlias(e.target.value)}>
        {aliases.map((a) => (
          <option key={`${a.from}-${a.alias}`} value={a.alias}>
            {a.alias}（当前指向 {a.from}）
          </option>
        ))}
      </select>
      <div className="fldlab">改指向</div>
      <div className="inrow">
        <select className="sel" aria-label="目标维度" value={dim} onChange={(e) => setDim(e.target.value)}>
          {dict.dimensions.map((d) => (
            <option key={d.name} value={d.name}>
              {d.name}
            </option>
          ))}
        </select>
        <select className="sel" aria-label="目标值" value={target?.value ?? ''} onChange={(e) => setValue(e.target.value)}>
          {(dimension?.values ?? []).map((v) => (
            <option key={v.value} value={v.value}>
              {v.label}
            </option>
          ))}
        </select>
      </div>
      <div className="mrow">
        <button className="btn ghost" onClick={onClose}>
          取消
        </button>
        <span className="sp" />
        <button className="btn" disabled={!alias || !target} onClick={() => onSubmit(alias, target!.label)}>
          修改
        </button>
      </div>
    </Modal>
  )
}
