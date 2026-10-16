// 成员网页调用的 API（网页会话 cookie；错误结构见 api.ts）。
import { api } from '../api'

export interface Me {
  id: number
  username: string
  name: string
  is_admin: boolean
}

export interface InboxItem {
  number: string
  title: string
  role: 'assignee' | 'creator'
  reason: string
  since_seq: number | null
  read_new: string
  read_full: string
}

export interface InboxResponse {
  member: string
  count: number
  items: InboxItem[]
}

export interface MemberRef {
  id: number
  name: string
}

export async function fetchSession(): Promise<{ viewer: { kind: string; member: Me } }> {
  return api('/api/v1/session')
}

export async function fetchInbox(): Promise<InboxResponse> {
  return api('/api/v1/inbox')
}

export async function fetchMembers(): Promise<MemberRef[]> {
  const body = await api('/api/v1/members')
  return body.members as MemberRef[]
}

export async function logout(): Promise<void> {
  await api('/api/v1/session', { method: 'DELETE' })
}

/** 收件箱原因 → 色条 / 徽标配色（mock 的 REASONS + 原因色条）。 */
export function reasonClass(reason: string): string {
  if (reason.includes('交付')) return 'delivered'
  if (reason.includes('访客')) return 'guest'
  if (reason.includes('退回') || reason.includes('回复了')) return 'back'
  return 'new'
}
