// 工具页（#42）：标签字典与别名——resolve 解析、添加别名（任何成员）、按维度
// 浏览字典（每个值带别名），另有「模板维护」入口。治理（停用/改名/合并/加
// 维度/改别名指向）仅管理员，在「我的 → 管理」里（AdminPage）。
import { useEffect, useState } from 'react'
import { ApiError } from '../api'
import {
  addAlias,
  fetchDictionary,
  resolveLabels,
  type Dictionary,
  type ResolveResult,
} from './memberApi'
import { chipClass } from './TicketsPage'
import { nav, toast } from './ui'

const VIA_TEXT: Record<string, string> = {
  exact: 'exact 精确匹配',
  alias: 'alias 别名匹配',
  fuzzy: 'fuzzy 模糊匹配',
}

export function ToolsPage() {
  const [dict, setDict] = useState<Dictionary | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetchDictionary()
      .then(setDict)
      .catch((e) => setError(e instanceof ApiError ? e.message : '读取字典失败'))
  }, [])

  async function refresh() {
    try {
      setDict(await fetchDictionary())
    } catch {
      // 刷新失败保持现状
    }
  }

  return (
    <div className="bpage">
      <div className="mcard" style={{ marginTop: 12 }}>
        <div className="sec-t" style={{ marginTop: 0 }}>
          标签字典与别名
        </div>
        <div className="hint">
          值来自字典，别名把俗称和常见错写对齐到标准值；打标签、筛选和下拉里写别名也能匹配。
        </div>
      </div>
      <ResolveCard />
      <AddAliasCard dict={dict} onAdded={() => void refresh()} />
      {error && <div className="ecard">{error}</div>}
      {dict?.dimensions.map((dim) => (
        <div className="mcard" key={dim.name}>
          <div className="sec-t" style={{ marginTop: 0 }}>
            {dim.name} <span className="hint">{dim.values.length} 个值</span>
          </div>
          {dim.values.length === 0 ? (
            <div className="hint">（此维度还没有值；成员打标签时勾「允许新建」即可往里加）</div>
          ) : (
            <table className="mini-tbl">
              <tbody>
                {dim.values.map((v) => (
                  <tr key={v.value}>
                    <td>
                      <span className={chipClass(`${dim.name}:${v.value}`)}>
                        {dim.name}:{v.value}
                      </span>
                    </td>
                    <td className="hint">
                      {v.aliases.length > 0 ? (
                        <>
                          别名：
                          {v.aliases.map((a) => (
                            <span key={a} className="aliasbox">
                              {a}
                            </span>
                          ))}
                        </>
                      ) : (
                        <i>（无别名）</i>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      ))}
      {dict?.plain && dict.plain.length > 0 && (
        <div className="mcard">
          <div className="sec-t" style={{ marginTop: 0 }}>
            不带维度的标签
          </div>
          <div>
            {dict.plain.map((p) => (
              <span key={p} className={chipClass(p)}>
                {p}
              </span>
            ))}
          </div>
          <div className="hint">
            成员可随手新建；别名只能挂在带维度的值上。治理（停用、改名、合并、加维度）在管理页。
          </div>
        </div>
      )}
      <div className="mcard">
        <div className="sec-t" style={{ marginTop: 0 }}>
          更多工具
        </div>
        <a
          className="toolrow"
          href="/templates"
          onClick={(e) => {
            e.preventDefault()
            nav('/templates')
          }}
        >
          📄 模板维护
          <span className="hint">Markdown 模板 · 版本 · 停用</span>
        </a>
      </div>
    </div>
  )
}

/** 解析（resolve）：输入俗称或错写，看它换到哪个标准值、以什么方式命中。 */
function ResolveCard() {
  const [q, setQ] = useState('')
  const [result, setResult] = useState<ResolveResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function run() {
    if (!q.trim()) return
    setBusy(true)
    setError(null)
    try {
      setResult(await resolveLabels(q.trim()))
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '解析失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mcard">
      <div className="sec-t" style={{ marginTop: 0 }}>
        解析（resolve）
      </div>
      <div className="inrow">
        <input
          placeholder="输入俗称或错写试试：2300 / 生化免疫 / 软件组"
          value={q}
          aria-label="解析输入"
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && void run()}
        />
        <button className="btn" onClick={() => void run()} disabled={busy}>
          解析
        </button>
      </div>
      {error && <div className="errbox">{error}</div>}
      {result && (
        <>
          {result.matches.length === 0 ? (
            <div className="hint" style={{ padding: '8px 0' }}>
              没有匹配。可以换一种写法，或打标签时勾「允许新建」；经常被叫错的话，在下面加一个别名。
            </div>
          ) : (
            <table className="mini-tbl">
              <tbody>
                {result.matches.map((m) => (
                  <tr key={`${m.label}-${m.via}`}>
                    <td>
                      <span className={chipClass(m.label)}>{m.label}</span>
                    </td>
                    <td className="hint">
                      {VIA_TEXT[m.via] ?? m.via}
                      {m.via === 'alias' && m.alias ? `：「${m.alias}」→ 标准值` : ''}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      )}
    </div>
  )
}

/** 添加别名（任何成员）：选「维度:值」，写别名；冲突由服务端拒绝（409）。 */
function AddAliasCard({ dict, onAdded }: { dict: Dictionary | null; onAdded: () => void }) {
  const [dim, setDim] = useState('')
  const [value, setValue] = useState('')
  const [alias, setAlias] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const dimension = dict?.dimensions.find((d) => d.name === dim) ?? dict?.dimensions[0]
  useEffect(() => {
    setValue('')
  }, [dim])

  async function submit() {
    if (!dimension || !value || !alias.trim()) {
      setError('先选一个标准值，再写别名')
      return
    }
    setBusy(true)
    setError(null)
    try {
      await addAlias(`${dimension.name}:${value}`, alias.trim())
      toast(`✓ 已添加：${dimension.name}:${value} 的别名「${alias.trim()}」`)
      setAlias('')
      onAdded()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '添加失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mcard">
      <div className="sec-t" style={{ marginTop: 0 }}>
        添加别名
      </div>
      <div className="hint">别名只能挂在「维度:值」的标准值上；同一维度里一个别名只能指向一个标准值。</div>
      <div className="inrow">
        <select
          className="sel"
          aria-label="别名维度"
          value={dimension?.name ?? ''}
          onChange={(e) => setDim(e.target.value)}
        >
          {(dict?.dimensions ?? []).map((d) => (
            <option key={d.name} value={d.name}>
              {d.name}
            </option>
          ))}
        </select>
        <select
          className="sel"
          aria-label="标准值"
          value={value}
          onChange={(e) => setValue(e.target.value)}
        >
          <option value="">选标准值…</option>
          {(dimension?.values ?? []).map((v) => (
            <option key={v.value} value={v.value}>
              {dimension?.name}:{v.value}
            </option>
          ))}
        </select>
      </div>
      <div className="inrow">
        <input
          placeholder="别名，如 1680"
          value={alias}
          aria-label="别名输入"
          onChange={(e) => setAlias(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && void submit()}
        />
        <button className="btn" onClick={() => void submit()} disabled={busy}>
          添加
        </button>
      </div>
      {error && <div className="errbox">{error}</div>}
    </div>
  )
}
