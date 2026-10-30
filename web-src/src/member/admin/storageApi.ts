// 管理页「存储」面板的 API 调用（#234）：三类空间占用与按关闭月份汇总的
// 可归档工单。closed_months 只是查询参数，默认 3，范围 1–120。
import { api } from '../../api'

export interface StorageMonthStat {
  month: string
  ticket_count: number
  attachment_bytes: number
}

export interface StorageStats {
  database_bytes: number
  attachments_bytes: number
  backups_bytes: number
  closed_months: number
  cutoff: string
  archivable: StorageMonthStat[]
}

export async function fetchStorageStats(closedMonths: number): Promise<StorageStats> {
  return api(`/api/v1/admin/storage?closed_months=${closedMonths}`)
}
