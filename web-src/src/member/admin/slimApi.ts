// 管理页「瘦身」的 API 调用（#283）：候选、预览、生成、下载与确认移出。
import { api } from '../../api'

export interface SlimCandidate {
  number: string
  title: string
  attachment_count: number
  attachment_bytes: number
}

export interface SlimCandidateMonth {
  month: string
  ticket_count: number
  attachment_count: number
  attachment_bytes: number
  tickets: SlimCandidate[]
}

export interface SlimCandidates {
  closed_months: number
  months: SlimCandidateMonth[]
}

export interface SlimPreview {
  ticket_count: number
  attachment_count: number
  attachment_bytes: number
  tickets: Array<{
    number: string
    title: string
    closed_at: string
    attachment_count: number
    attachment_bytes: number
  }>
}

export interface SlimPackage {
  name: string
  ticket_numbers: string[]
  ticket_count: number
  attachment_count: number
  attachment_bytes: number
  generated_at: string
  expires_at: string
  operator_name: string
  download_url: string
  removed_attachment_count: number
}

export interface SlimRemoveResult {
  package_name: string
  ticket_numbers: string[]
  attachment_count: number
  attachment_bytes: number
  removed_at: string
  cleanup_failures: number
}

export async function fetchSlimCandidates(closedMonths: number): Promise<SlimCandidateMonth[]> {
  const body = await api(`/api/v1/admin/slim/candidates?closed_months=${closedMonths}`)
  return body.months ?? []
}

export async function previewSlim(closedMonths: number, ticketNumbers: string[]): Promise<SlimPreview> {
  return api('/api/v1/admin/slim/preview', {
    method: 'POST',
    body: JSON.stringify({ closed_months: closedMonths, ticket_numbers: ticketNumbers }),
  })
}

export async function createSlimPackage(closedMonths: number, ticketNumbers: string[]): Promise<SlimPackage> {
  return api('/api/v1/admin/slim/packages', {
    method: 'POST',
    body: JSON.stringify({ closed_months: closedMonths, ticket_numbers: ticketNumbers }),
  })
}

export async function fetchSlimPackages(): Promise<SlimPackage[]> {
  const body = await api('/api/v1/admin/slim/packages')
  return body.packages ?? []
}

export function removeSlimPackage(name: string): Promise<SlimRemoveResult> {
  return api(`/api/v1/admin/slim/packages/${encodeURIComponent(name)}/remove`, {
    method: 'POST',
    body: JSON.stringify({ saved: true }),
  })
}
