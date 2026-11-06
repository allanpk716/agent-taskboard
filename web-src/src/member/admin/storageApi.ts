// 管理页「存储」面板的 API 调用（#234）：三类空间占用与按关闭月份汇总的
// 可归档工单。closed_months 只是查询参数，默认 3，范围 1–120。
import { api } from '../../api'

export interface StorageMonthStat {
  month: string
  ticket_count: number
  attachment_bytes: number
  /** 月份内可逐张选择的工单；老 mock / 旧服务可没有这个字段。 */
  tickets?: StorageTicketStat[]
}

export interface StorageTicketStat {
  number: string
  title: string
  attachment_bytes: number
}

export interface StorageStats {
  database_bytes: number
  attachments_bytes: number
  attachments_backend?: string
  backups_bytes: number
  /** 按当前 N 可瘦身的工单数（口径与瘦身预览一致）。 */
  slimmable_ticket_count: number
  /** 这些工单里仍留在服务器上的附件字节数（不含插图）。 */
  slimmable_attachment_bytes: number
  closed_months: number
  cutoff: string
  archivable: StorageMonthStat[]
}

export async function fetchStorageStats(closedMonths: number): Promise<StorageStats> {
  return api(`/api/v1/admin/storage?closed_months=${closedMonths}`)
}
