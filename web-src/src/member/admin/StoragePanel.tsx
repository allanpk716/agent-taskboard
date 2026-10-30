// 管理页「存储」面板（#234/#235/#236）：三类空间占用、按关闭月份分组的可归档
// 工单，以及选择 → 预览 → 生成 / 下载归档包 → 确认已保存后移除。N 只是查询
// 参数（默认 3，范围 1–120），改动后重新请求。
import { useEffect, useMemo, useState } from 'react'
import { ApiError } from '../../api'
import { fmtBeijing } from '../../shared/format'
import {
  createArchivePackage,
  fetchArchivePackages,
  previewArchive,
  removeArchivePackage,
  type ArchivePackage,
  type ArchiveRemoveResult,
  type ArchivePreview,
} from './archiveApi'
import { fetchStorageStats, type StorageStats, type StorageTicketStat } from './storageApi'

/** 字节数 → 「0 B」/「890 KB」/「1.2 MB」/「3.4 GB」（1024 进制）。 */
function fmtBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  const units = ['KB', 'MB', 'GB']
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value >= 10 || unit === 0 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`
}

export function StoragePanel() {
  const [monthsInput, setMonthsInput] = useState('3')
  const [months, setMonths] = useState(3)
  const [stats, setStats] = useState<StorageStats | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [preview, setPreview] = useState<ArchivePreview | null>(null)
  const [packages, setPackages] = useState<ArchivePackage[]>([])
  const [packageError, setPackageError] = useState<string | null>(null)
  const [saved, setSaved] = useState<Set<string>>(new Set())
  const [removal, setRemoval] = useState<ArchiveRemoveResult | null>(null)
  const [busy, setBusy] = useState<'preview' | 'generate' | 'remove' | null>(null)

  useEffect(() => {
    let cancelled = false
    fetchStorageStats(months)
      .then((s) => {
        if (cancelled) return
        setStats(s)
        setError(null)
      })
      .catch((e) => {
        if (cancelled) return
        setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '读取存储统计失败')
      })
    return () => {
      cancelled = true
    }
  }, [months])

  // N 改变后可归档集合变了，旧选择与预览都不能继续沿用。
  useEffect(() => {
    setSelected(new Set())
    setPreview(null)
  }, [months])

  useEffect(() => {
    let cancelled = false
    fetchArchivePackages()
      .then((list) => {
        if (cancelled) return
        setPackages(list)
        setPackageError(null)
      })
      .catch((e) => {
        if (cancelled) return
        setPackageError(e instanceof ApiError ? `${e.message}（${e.code}）` : '读取归档包失败')
      })
    return () => {
      cancelled = true
    }
  }, [])

  const candidates = useMemo(() => stats?.archivable ?? [], [stats])
  const selectedNumbers = useMemo(() => {
    const out: string[] = []
    for (const month of candidates) {
      for (const ticket of month.tickets ?? []) {
        if (selected.has(ticket.number)) out.push(ticket.number)
      }
    }
    return out
  }, [candidates, selected])

  // 输入框即时回显；只有落在 1–120 的整数时才重新请求（越界值后端也会 400）。
  const changeMonths = (raw: string) => {
    setMonthsInput(raw)
    const n = Number.parseInt(raw, 10)
    if (Number.isInteger(n) && n >= 1 && n <= 120) setMonths(n)
  }

  const changeSelection = (numbers: string[], checked: boolean) => {
    setSelected((prev) => {
      const next = new Set(prev)
      for (const number of numbers) {
        if (checked) next.add(number)
        else next.delete(number)
      }
      return next
    })
    setPreview(null)
  }

  const submitPreview = async () => {
    setBusy('preview')
    try {
      setPreview(await previewArchive(months, selectedNumbers))
    } catch (e) {
      setPreview(null)
      setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '预览归档失败')
    } finally {
      setBusy(null)
    }
  }

  const generate = async () => {
    setBusy('generate')
    try {
      await createArchivePackage(months, selectedNumbers)
      setPreview(null)
      setRemoval(null)
      setPackages(await fetchArchivePackages())
      setError(null)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '生成归档包失败')
    } finally {
      setBusy(null)
    }
  }

  const remove = async (name: string) => {
    if (!saved.has(name)) return
    setBusy('remove')
    try {
      const result = await removeArchivePackage(name)
      setRemoval(result)
      setPackages(await fetchArchivePackages())
      setStats(await fetchStorageStats(months))
      setSaved((prev) => {
        const next = new Set(prev)
        next.delete(name)
        return next
      })
      setPackageError(null)
    } catch (e) {
      setPackageError(e instanceof ApiError ? `${e.message}（${e.code}）` : '移除归档工单失败')
    } finally {
      setBusy(null)
    }
  }

  const ticketNumbers = (tickets: StorageTicketStat[] | undefined) => (tickets ?? []).map((t) => t.number)

  return (
    <div className="mcard" data-testid="storage-card">
      <div className="sec-t" style={{ marginTop: 0 }}>
        存储与归档
      </div>
      {error && <div className="errbox">{error}</div>}
      <div className="kv">
        <span>数据库</span>
        <b data-testid="storage-database">{stats ? fmtBytes(stats.database_bytes) : '…'}</b>
      </div>
      <div className="kv">
        <span>附件目录</span>
        <b data-testid="storage-attachments">{stats ? fmtBytes(stats.attachments_bytes) : '…'}</b>
      </div>
      <div className="kv">
        <span>备份目录</span>
        <b data-testid="storage-backups">{stats ? fmtBytes(stats.backups_bytes) : '…'}</b>
      </div>
      <div className="hint">
        数据库含 WAL 与共享内存文件；备份目录是实际生效的配置目录（未配置时在数据目录的
        backups\ 下，含附件镜像）。
      </div>

      <div className="fldlab" style={{ marginTop: 10 }}>
        已关闭满
        <input
          type="number"
          min={1}
          max={120}
          aria-label="已关闭月数"
          data-testid="storage-months-input"
          value={monthsInput}
          onChange={(e) => changeMonths(e.target.value)}
          style={{ width: 72, margin: '0 6px' }}
        />
        个月的工单
      </div>
      {stats && <div className="hint">统计口径：最近一次关闭时间不晚于 {fmtBeijing(stats.cutoff)}（北京时间）。</div>}
      <table className="mini-tbl" data-testid="storage-months-table">
        {candidates.map((row) => {
          const tickets = row.tickets ?? []
          const numbers = ticketNumbers(tickets)
          const chosen = numbers.filter((number) => selected.has(number))
          const all = numbers.length > 0 && chosen.length === numbers.length
          return (
            <tbody key={row.month}>
              <tr data-testid={`storage-month-${row.month}`}>
                <td>
                  <input
                    type="checkbox"
                    aria-label={`选择 ${row.month} 全部工单`}
                    data-testid={`archive-month-${row.month}`}
                    checked={all}
                    onChange={(e) => changeSelection(numbers, e.target.checked)}
                  />{' '}
                  {row.month}
                </td>
                <td>{row.ticket_count} 张工单</td>
                <td>{fmtBytes(row.attachment_bytes)}</td>
              </tr>
              {tickets.map((ticket) => (
                <tr key={ticket.number} data-testid={`archive-ticket-row-${ticket.number}`}>
                  <td className="hint mono">
                    <input
                      type="checkbox"
                      aria-label={`选择工单 ${ticket.number}`}
                      data-testid={`archive-ticket-${ticket.number}`}
                      checked={selected.has(ticket.number)}
                      onChange={(e) => changeSelection([ticket.number], e.target.checked)}
                    />{' '}
                    {ticket.number} {ticket.title}
                  </td>
                  <td colSpan={2}>{fmtBytes(ticket.attachment_bytes)}</td>
                </tr>
              ))}
            </tbody>
          )
        })}
        {stats !== null && candidates.length === 0 && (
          <tbody>
            <tr>
              <td className="hint" colSpan={3} data-testid="storage-empty">
                没有已关闭满 {stats.closed_months} 个月的工单
              </td>
            </tr>
          </tbody>
        )}
      </table>

      <div className="frow" style={{ marginTop: 10 }}>
        <button
          className="btn sm ghost"
          disabled={selectedNumbers.length === 0 || busy !== null}
          onClick={() => void submitPreview()}
        >
          {busy === 'preview' ? '预览中…' : '预览归档'}
        </button>
        <button
          className="btn sm"
          disabled={preview === null || selectedNumbers.length === 0 || busy !== null}
          onClick={() => void generate()}
        >
          {busy === 'generate' ? '生成中…' : '生成归档包'}
        </button>
      </div>

      {preview && (
        <div className="subcard" data-testid="archive-preview" style={{ marginTop: 10 }}>
          <div className="fldlab">将归档 {preview.ticket_count} 张工单，附件 {fmtBytes(preview.attachment_bytes)}</div>
          {preview.drafts.length > 0 ? (
            <>
              <div className="warnbox">生成归档包会丢弃下面这些未发出的草稿。</div>
              <table className="mini-tbl">
                <tbody>
                  {preview.drafts.map((draft) => (
                    <tr key={draft.id}>
                      <td className="mono">{draft.ticket_number}</td>
                      <td>{draft.member_name}{draft.agent_name ? ` / ${draft.agent_name}` : ''}</td>
                      <td>{draft.kind} v{draft.version}</td>
                      <td>{fmtBeijing(draft.updated_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          ) : (
            <div className="hint">这些工单上没有未发出的草稿。</div>
          )}
          <table className="mini-tbl" style={{ marginTop: 6 }}>
            <tbody>
              {preview.tickets.map((ticket) => (
                <tr key={ticket.number}>
                  <td className="mono">{ticket.number}</td>
                  <td>{ticket.title}</td>
                  <td>{fmtBytes(ticket.attachment_bytes)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="fldlab" style={{ marginTop: 12 }}>
        暂存归档包（7 天）
      </div>
      {packageError && <div className="errbox">{packageError}</div>}
      {removal && (
        <div className="okbox" data-testid="archive-released">
          已移除 {removal.ticket_count} 张工单，已释放附件与备份镜像 {fmtBytes(removal.attachment_bytes)}。
        </div>
      )}
      {packages.length === 0 ? (
        <div className="hint">暂存区没有归档包。</div>
      ) : (
        <table className="mini-tbl">
          <tbody>
            {packages.map((pkg) => (
              <tr key={pkg.name} data-testid={`archive-package-${pkg.name}`}>
                <td className="mono">{pkg.name}</td>
                <td>
                  {pkg.ticket_count} 张工单 · {fmtBytes(pkg.attachment_bytes)}
                  <div className="mono hint">{pkg.ticket_numbers.join('、')}</div>
                  <div className="hint">{fmtBeijing(pkg.expires_at)} 到期</div>
                </td>
                <td>
                  <label className="hint" style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
                    <input
                      type="checkbox"
                      checked={saved.has(pkg.name)}
                      onChange={(e) =>
                        setSaved((prev) => {
                          const next = new Set(prev)
                          if (e.target.checked) next.add(pkg.name)
                          else next.delete(pkg.name)
                          return next
                        })
                      }
                      data-testid={`archive-saved-${pkg.name}`}
                    />
                    我已保存
                  </label>
                  <a
                    className="btn sm ghost"
                    href={pkg.download_url}
                    download={pkg.name}
                    data-testid={`archive-download-${pkg.name}`}
                  >
                    下载
                  </a>
                  <button
                    type="button"
                    className="btn sm danger"
                    disabled={!saved.has(pkg.name) || busy === 'remove'}
                    onClick={() => void remove(pkg.name)}
                    data-testid={`archive-remove-${pkg.name}`}
                  >
                    从服务器移除
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="hint">移除会删除服务器上的工单数据与附件，只留下墓碑；导入是后续工单。</div>
    </div>
  )
}
