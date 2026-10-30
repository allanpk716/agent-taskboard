// 管理页「存储」面板（#234）：数据库 / 附件目录 / 备份目录的空间占用，
// 以及按关闭月份分组的「已关闭满 N 个月」可归档工单。N 是查询参数
// （默认 3，范围 1–120），改动后重新请求；本面板只做统计，不移动数据。
import { useEffect, useState } from 'react'
import { ApiError } from '../../api'
import { fmtBeijing } from '../../shared/format'
import { fetchStorageStats, type StorageStats } from './storageApi'

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

  // 输入框即时回显；只有落在 1–120 的整数时才重新请求（越界值后端也会 400）。
  const changeMonths = (raw: string) => {
    setMonthsInput(raw)
    const n = Number.parseInt(raw, 10)
    if (Number.isInteger(n) && n >= 1 && n <= 120) setMonths(n)
  }

  return (
    <div className="mcard" data-testid="storage-card">
      <div className="sec-t" style={{ marginTop: 0 }}>
        存储
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
        <tbody>
          {(stats?.archivable ?? []).map((row) => (
            <tr key={row.month} data-testid={`storage-month-${row.month}`}>
              <td>{row.month}</td>
              <td>{row.ticket_count} 张工单</td>
              <td>{fmtBytes(row.attachment_bytes)}</td>
            </tr>
          ))}
          {stats !== null && stats.archivable.length === 0 && (
            <tr>
              <td className="hint" colSpan={3} data-testid="storage-empty">
                没有已关闭满 {stats.closed_months} 个月的工单
              </td>
            </tr>
          )}
        </tbody>
      </table>
      <div className="hint">这里只做统计；选中工单、生成归档包与移除是后续功能。</div>
    </div>
  )
}
