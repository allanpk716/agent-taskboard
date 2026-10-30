// 管理页「归档包」的 API 调用（#235）：预览、生成、列出与下载。
// 下载用响应里的相对地址直接作为链接，仍携带管理员网页会话 cookie。
import { api } from '../../api'

export interface ArchiveTicketSummary {
  number: string
  title: string
  closed_at: string
  attachment_bytes: number
}

export interface ArchiveDraftSummary {
  id: string
  ticket_number: string
  kind: string
  member_name: string
  agent_name: string
  version: number
  updated_at: string
  attachment_ids: string[]
}

export interface ArchivePreview {
  ticket_count: number
  attachment_bytes: number
  tickets: ArchiveTicketSummary[]
  drafts: ArchiveDraftSummary[]
}

export interface ArchivePackage {
  name: string
  ticket_count: number
  attachment_bytes: number
  generated_at: string
  expires_at: string
  ticket_numbers: string[]
  download_url: string
  operator_name: string
}

export interface ArchiveTombstone {
  seq: number
  number: string
  title: string
  closed_at: string
  archived_at: string
  operator_name: string
  package_name: string
}

export interface ArchiveRemoveResult {
  ticket_count: number
  attachment_bytes: number
  package_name: string
  removed_at: string
  tombstones: ArchiveTombstone[]
}

export interface ArchiveImportCandidate {
  seq: number
  number: string
  title: string
  closed_at: string
  attachment_bytes: number
  importable: boolean
  reason: string
}

export interface ArchiveImportInspection {
  format_version: number
  platform_version: string
  package_name: string
  generated_at: string
  ticket_count: number
  attachment_bytes: number
  tickets: ArchiveImportCandidate[]
}

export interface ArchiveImportResult {
  package_name: string
  imported_at: string
  imported: ArchiveImportCandidate[]
  rejected: ArchiveImportCandidate[]
}

export async function previewArchive(closedMonths: number, ticketNumbers: string[]): Promise<ArchivePreview> {
  return api('/api/v1/admin/archive/preview', {
    method: 'POST',
    body: JSON.stringify({ closed_months: closedMonths, ticket_numbers: ticketNumbers }),
  })
}

export async function createArchivePackage(
  closedMonths: number,
  ticketNumbers: string[],
): Promise<ArchivePackage> {
  return api('/api/v1/admin/archive/packages', {
    method: 'POST',
    body: JSON.stringify({ closed_months: closedMonths, ticket_numbers: ticketNumbers }),
  })
}

export async function fetchArchivePackages(): Promise<ArchivePackage[]> {
  const body = await api('/api/v1/admin/archive/packages')
  return body.packages ?? []
}

export function removeArchivePackage(name: string): Promise<ArchiveRemoveResult> {
  return api(`/api/v1/admin/archive/packages/${encodeURIComponent(name)}/remove`, {
    method: 'POST',
    body: JSON.stringify({ saved: true }),
  })
}

function archivePackageForm(file: File, ticketNumbers?: string[]): FormData {
  const form = new FormData()
  form.append('package', file)
  if (ticketNumbers) {
    for (const number of ticketNumbers) form.append('ticket_numbers', number)
  }
  return form
}

export function inspectArchivePackage(file: File): Promise<ArchiveImportInspection> {
  return api('/api/v1/admin/archive/import/inspect', {
    method: 'POST',
    body: archivePackageForm(file),
  })
}

export function importArchivePackage(
  file: File,
  ticketNumbers: string[],
): Promise<ArchiveImportResult> {
  return api('/api/v1/admin/archive/import', {
    method: 'POST',
    body: archivePackageForm(file, ticketNumbers),
  })
}
