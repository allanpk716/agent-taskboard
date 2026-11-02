// 管理页「附件瘦身」（#283）：筛选、勾选、预览、生成 / 下载瘦身包，
// 确认已保存后只移出附件；交互与归档面板保持一致。
import { useEffect, useMemo, useState } from 'react'
import { ApiError } from '../../api'
import { fmtBeijing } from '../../shared/format'
import {
  createSlimPackage,
  fetchSlimCandidates,
  fetchSlimPackages,
  previewSlim,
  removeSlimPackage,
  type SlimCandidateMonth,
  type SlimPackage,
  type SlimPreview,
  type SlimRemoveResult,
} from './slimApi'

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

export function SlimPanel() {
  const [monthsInput, setMonthsInput] = useState('3')
  const [months, setMonths] = useState(3)
  const [candidateMonths, setCandidateMonths] = useState<SlimCandidateMonth[]>([])
  const [error, setError] = useState<string | null>(null)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [preview, setPreview] = useState<SlimPreview | null>(null)
  const [packages, setPackages] = useState<SlimPackage[]>([])
  const [packageError, setPackageError] = useState<string | null>(null)
  const [saved, setSaved] = useState<Set<string>>(new Set())
  const [removal, setRemoval] = useState<SlimRemoveResult | null>(null)
  const [busy, setBusy] = useState<'preview' | 'generate' | 'remove' | null>(null)

  useEffect(() => {
    let cancelled = false
    fetchSlimCandidates(months)
      .then((list) => {
        if (cancelled) return
        setCandidateMonths(list)
        setError(null)
      })
      .catch((e) => {
        if (cancelled) return
        setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '读取瘦身候选失败')
      })
    return () => {
      cancelled = true
    }
  }, [months])

  useEffect(() => {
    let cancelled = false
    fetchSlimPackages()
      .then((list) => {
        if (cancelled) return
        setPackages(list)
        setPackageError(null)
      })
      .catch((e) => {
        if (cancelled) return
        setPackageError(e instanceof ApiError ? `${e.message}（${e.code}）` : '读取瘦身包失败')
      })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    setSelected(new Set())
    setPreview(null)
  }, [months])

  const selectedNumbers = useMemo(() => {
    const out: string[] = []
    for (const month of candidateMonths) {
      for (const ticket of month.tickets ?? []) {
        if (selected.has(ticket.number)) out.push(ticket.number)
      }
    }
    return out
  }, [candidateMonths, selected])

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
      setPreview(await previewSlim(months, selectedNumbers))
      setError(null)
    } catch (e) {
      setPreview(null)
      setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '预览瘦身失败')
    } finally {
      setBusy(null)
    }
  }

  const generate = async () => {
    setBusy('generate')
    try {
      await createSlimPackage(months, selectedNumbers)
      setPreview(null)
      setRemoval(null)
      setPackages(await fetchSlimPackages())
      setError(null)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}（${e.code}）` : '生成瘦身包失败')
    } finally {
      setBusy(null)
    }
  }

  const remove = async (name: string) => {
    if (!saved.has(name)) return
    setBusy('remove')
    try {
      const result = await removeSlimPackage(name)
      setRemoval(result)
      setPackages(await fetchSlimPackages())
      setCandidateMonths(await fetchSlimCandidates(months))
      setSaved((prev) => {
        const next = new Set(prev)
        next.delete(name)
        return next
      })
      setPackageError(null)
    } catch (e) {
      setPackageError(e instanceof ApiError ? `${e.message}（${e.code}）` : '确认瘦身移出失败')
    } finally {
      setBusy(null)
    }
  }

  return (
    <div className="mcard" data-testid="slim-card">
      <div className="sec-t" style={{ marginTop: 0 }}>
        附件瘦身
      </div>
      <div className="hint">
        只导出和移出附件，工单说明、回复、插图与时间线留在平台；下载并确认保存后才会删除服务器文件。
      </div>
      {error && <div className="errbox">{error}</div>}
      <div className="fldlab" style={{ marginTop: 10 }}>
        已关闭满
        <input
          type="number"
          min={1}
          max={120}
          aria-label="瘦身已关闭月数"
          data-testid="slim-months-input"
          value={monthsInput}
          onChange={(e) => changeMonths(e.target.value)}
          style={{ width: 72, margin: '0 6px' }}
        />
        个月的工单
      </div>
      <table className="mini-tbl" data-testid="slim-months-table">
        {candidateMonths.map((row) => {
          const tickets = row.tickets ?? []
          const numbers = tickets.map((ticket) => ticket.number)
          const chosen = numbers.filter((number) => selected.has(number))
          const all = numbers.length > 0 && chosen.length === numbers.length
          return (
            <tbody key={row.month}>
              <tr data-testid={`slim-month-${row.month}`}>
                <td>
                  <input
                    type="checkbox"
                    aria-label={`选择 ${row.month} 全部瘦身工单`}
                    data-testid={`slim-month-select-${row.month}`}
                    checked={all}
                    onChange={(e) => changeSelection(numbers, e.target.checked)}
                  />{' '}
                  {row.month}
                </td>
                <td>{row.ticket_count} 张工单</td>
                <td>
                  {row.attachment_count} 个附件 / {fmtBytes(row.attachment_bytes)}
                </td>
              </tr>
              {tickets.map((ticket) => (
                <tr key={ticket.number} data-testid={`slim-ticket-row-${ticket.number}`}>
                  <td className="hint mono">
                    <input
                      type="checkbox"
                      aria-label={`选择瘦身工单 ${ticket.number}`}
                      data-testid={`slim-ticket-${ticket.number}`}
                      checked={selected.has(ticket.number)}
                      onChange={(e) => changeSelection([ticket.number], e.target.checked)}
                    />{' '}
                    {ticket.number} {ticket.title}
                  </td>
                  <td colSpan={2}>
                    {ticket.attachment_count} 个附件 / {fmtBytes(ticket.attachment_bytes)}
                  </td>
                </tr>
              ))}
            </tbody>
          )
        })}
        {candidateMonths.length === 0 && (
          <tbody>
            <tr>
              <td className="hint" colSpan={3} data-testid="slim-empty">
                没有可瘦身的工单
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
          {busy === 'preview' ? '预览中…' : '预览瘦身'}
        </button>
        <button
          className="btn sm"
          disabled={preview === null || selectedNumbers.length === 0 || busy !== null}
          onClick={() => void generate()}
        >
          {busy === 'generate' ? '生成中…' : '生成瘦身包'}
        </button>
      </div>

      {preview && (
        <div className="subcard" data-testid="slim-preview" style={{ marginTop: 10 }}>
          <div className="fldlab">
            将瘦身 {preview.ticket_count} 张工单，{preview.attachment_count} 个附件，共{' '}
            {fmtBytes(preview.attachment_bytes)}
          </div>
          <table className="mini-tbl" style={{ marginTop: 6 }}>
            <tbody>
              {preview.tickets.map((ticket) => (
                <tr key={ticket.number}>
                  <td className="mono">{ticket.number}</td>
                  <td>{ticket.title}</td>
                  <td>
                    {ticket.attachment_count} 个 / {fmtBytes(ticket.attachment_bytes)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="fldlab" style={{ marginTop: 12 }}>
        暂存瘦身包（7 天）
      </div>
      {packageError && <div className="errbox">{packageError}</div>}
      {packages.length === 0 ? (
        <div className="hint" data-testid="slim-package-empty">
          还没有暂存瘦身包。
        </div>
      ) : (
        <table className="mini-tbl">
          <tbody>
            {packages.map((pkg) => (
              <tr key={pkg.name} data-testid={`slim-package-${pkg.name}`}>
                <td>
                  <div className="mono">{pkg.name}</div>
                  <div className="hint">
                    {pkg.attachment_count} 个附件 / {fmtBytes(pkg.attachment_bytes)}；生成于{' '}
                    {fmtBeijing(pkg.generated_at)}，到期 {fmtBeijing(pkg.expires_at)}
                  </div>
                </td>
                <td>{pkg.removed_attachment_count > 0 ? '附件已移出' : '等待确认保存'}</td>
                <td>
                  <label className="hint" style={{ display: 'inline-flex', gap: 4 }}>
                    <input
                      type="checkbox"
                      checked={saved.has(pkg.name)}
                      disabled={pkg.removed_attachment_count > 0}
                      onChange={(e) =>
                        setSaved((prev) => {
                          const next = new Set(prev)
                          if (e.target.checked) next.add(pkg.name)
                          else next.delete(pkg.name)
                          return next
                        })
                      }
                      data-testid={`slim-saved-${pkg.name}`}
                    />
                    我已保存
                  </label>
                  <a
                    className="btn sm ghost"
                    href={pkg.download_url}
                    download={pkg.name}
                    data-testid={`slim-download-${pkg.name}`}
                  >
                    下载
                  </a>
                  <button
                    type="button"
                    className="btn sm danger"
                    disabled={pkg.removed_attachment_count > 0 || !saved.has(pkg.name) || busy === 'remove'}
                    onClick={() => void remove(pkg.name)}
                    data-testid={`slim-remove-${pkg.name}`}
                  >
                    移出附件
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {removal && (
        <div className="okbox" data-testid="slim-remove-result">
          已移出 {removal.attachment_count} 个附件，释放 {fmtBytes(removal.attachment_bytes)}；
          清理失败 {removal.cleanup_failures} 个。
        </div>
      )}
    </div>
  )
}
