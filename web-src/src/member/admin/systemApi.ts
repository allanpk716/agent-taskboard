// 管理页「系统状态」面板自己的 API 调用（#45）：更新检查/执行与备份状态。
// 自成一体，避免与 #42 的成员页 API 互相牵连。
import { api } from '../../api'

export interface BackupsStatus {
  local_only: boolean
  last_success_at: string | null
  last_local_fallback_at?: string | null
  target_unreachable?: boolean
  target_unreachable_error?: string
  overdue: boolean
  last_failure: { at: string; kind: string; error: string } | null
  records: { at: string; kind: string; filename: string; success: boolean; location: string }[]
}

export interface ComponentStatus {
  current: string
  latest: string
  update_available: boolean
  min_server?: string
}

export interface UpdateStatus {
  source_used: string
  server: ComponentStatus
  web: ComponentStatus
  client: ComponentStatus
  web_needs_newer_server: boolean
}

export interface UpdateResult {
  source_used: string
  steps: string[]
  message?: string
  server_updated: boolean
  web_updated: boolean
  client_updated: boolean
  restart_needed: boolean
  preupdate_backup?: string
  preupdate_backup_local?: boolean
}

export async function fetchBackupsStatus(): Promise<BackupsStatus> {
  return api('/api/v1/admin/backups')
}

export async function fetchUpdateStatus(): Promise<UpdateStatus> {
  return api('/api/v1/admin/update')
}

export async function applyUpdate(): Promise<UpdateResult> {
  return api('/api/v1/admin/update', { method: 'POST' })
}
