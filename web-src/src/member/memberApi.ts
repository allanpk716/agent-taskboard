// 成员网页调用的 API（网页会话 cookie；错误结构见 api.ts）。
import { api, ApiError } from '../api'
import { compressImage } from '../shared/compress'
import { fetchServerVersion } from '../shared/health'
import type { Author, Collaborator, DraftHistoryVersion, DraftView, TicketView } from '../shared/types'

export type { Collaborator } from '../shared/types'

export interface Me {
  id: number
  username: string
  name: string
  is_admin: boolean
}

export interface InboxItem {
  number: string
  title: string
  role: 'assignee' | 'creator' | 'triager'
  reason: string
  reason_code: string
  since_seq: number | null
  read_new: string
  read_full: string
  kind: string
  creator: string
  assignee?: Author | null
  labels: string[]
  waiting_on: string
  who: string
  updated_at: string
  unread: boolean
}

export interface InboxResponse {
  member: string
  count: number
  items: InboxItem[]
}

export interface MemberRef {
  id: number
  name: string
  /** #42 起名单带启用状态；已停用成员不再出现在指派下拉里。 */
  active?: boolean
}

/** 成员视角的工单（比访客多 labels；viewer.can 决定按钮是否出现）。 */
export interface MemberTicket extends TicketView {
  labels: string[]
  /** #110：Agent 段（成员读工单总有）。 */
  handoff_text?: string
  /** #110：人话行 + Agent 段；有指派人时才有。 */
  handoff_share_text?: string | null
}

export interface MarkNotificationsReadResult {
  marked: number
  unread_count: number
}

export interface NotificationTicket {
  number: string
  title: string
  url: string
}

export interface NotificationItem {
  id: number
  ticket: NotificationTicket | null
  url: string
  kind: 'turn' | 'fyi' | string
  reason: string
  text: string
  event: {
    seq: number
    type: string
    actor: { kind: string; member_id: number; agent_id: number; name: string }
  }
  summary: string
  created_at: string
  read_at?: string | null
}

export interface NotificationsResponse {
  items: NotificationItem[]
  cursor: number
  unread_count: number
  next: string | null
}

export interface NotificationsUnreadResponse {
  unread_count: number
  latest_id: number
  client_online: boolean
}

export interface AccessRecord {
  actor: {
    kind: 'member' | 'agent' | 'admin' | 'guest'
    name: string
    link_note?: string
    entry_method?: string
  }
  action: 'view' | 'download' | 'denied'
  attachment?: { id: string; filename: string }
  first_at: string
  last_at: string
  count: number
}

export interface AccessRecordsResponse {
  records: AccessRecord[]
  since: string
}

export async function fetchSession(): Promise<{ viewer: { kind: string; member: Me } }> {
  return api('/api/v1/session')
}

export async function fetchInbox(): Promise<InboxResponse> {
  return api('/api/v1/inbox')
}

export { fetchServerVersion }

export async function fetchMembers(): Promise<MemberRef[]> {
  const body = await api('/api/v1/members')
  return body.members as MemberRef[]
}

export async function fetchTicket(ref: string): Promise<MemberTicket> {
  return api(`/api/v1/tickets/${encodeURIComponent(ref)}`)
}

export async function fetchAccessRecords(ref: string): Promise<AccessRecordsResponse> {
  return api(`/api/v1/tickets/${encodeURIComponent(ref)}/access-records`)
}

export function markTicketNotificationsRead(number: string): Promise<MarkNotificationsReadResult> {
  return api('/api/v1/notifications/read', {
    method: 'POST',
    body: JSON.stringify({ ticket: number }),
  })
}

export async function fetchNotifications(
  opts: { unread?: boolean; before?: number; limit?: number } = {},
): Promise<NotificationsResponse> {
  const params = new URLSearchParams()
  if (opts.unread) params.set('unread', '1')
  if (opts.before !== undefined) params.set('before', String(opts.before))
  params.set('limit', String(opts.limit ?? 30))
  return api(`/api/v1/notifications?${params}`)
}

export function fetchNotificationsUnread(): Promise<NotificationsUnreadResponse> {
  return api('/api/v1/notifications/unread')
}

export function markNotificationsRead(
  target: { ids?: number[]; ticket?: string; up_to?: number },
): Promise<MarkNotificationsReadResult> {
  return api('/api/v1/notifications/read', {
    method: 'POST',
    body: JSON.stringify(target),
  })
}

export async function logout(): Promise<void> {
  await api('/api/v1/session', { method: 'DELETE' })
}

export async function changeOwnPassword(oldPassword: string, newPassword: string): Promise<void> {
  await api('/api/v1/me/password', {
    method: 'POST',
    body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
  })
}

/* ---------------- 模板维护（#42） ---------------- */

export interface TemplateSummary {
  key: string
  name: string
  kind: string
  scenario: string
  version: number
  active: boolean
  preset_labels: string[]
}

export interface TemplateField {
  key: string
  title: string
  guide: string
  example: string
  required: boolean
  type: string
  options: string[]
  dimension?: string
}

export interface TemplateDetail extends TemplateSummary {
  agent_note: string
  fields: TemplateField[]
  groups?: { title: string; subtitle?: string }[]
  markdown: string
  updated_by: AuthorRef | null
  updated_at: string
}

export interface TemplateVersion {
  version: number
  name: string
  markdown: string
  updated_by: AuthorRef | null
  updated_at: string
}

export interface AuthorRef {
  member: { name: string } | null
  agent: { name: string } | null
}

export function authorText(a: AuthorRef | null | undefined): string {
  if (!a?.member) return '—'
  return a.agent ? `${a.member.name} 的 Agent · ${a.agent.name}` : a.member.name
}

export async function fetchTemplates(): Promise<TemplateSummary[]> {
  const body = await api('/api/v1/templates')
  return (body.templates ?? []) as TemplateSummary[]
}

export async function fetchTemplate(key: string): Promise<TemplateDetail> {
  return api(`/api/v1/templates/${encodeURIComponent(key)}`)
}

/** idemKey：同一次用户动作自动重试时复用同一个 Idempotency-Key（ADR 0002
 *  「放心自动重试」，评审 P2-13）；不传就按一次性动作现生成。 */
export async function createTemplate(
  key: string,
  markdown: string,
  idemKey: string = crypto.randomUUID(),
): Promise<TemplateSummary> {
  return api('/api/v1/templates', {
    method: 'POST',
    body: JSON.stringify({ key, markdown }),
    idempotencyKey: idemKey,
  })
}

export async function updateTemplate(
  key: string,
  markdown: string,
  idemKey: string = crypto.randomUUID(),
): Promise<TemplateSummary> {
  return api(`/api/v1/templates/${encodeURIComponent(key)}`, {
    method: 'PATCH',
    body: JSON.stringify({ markdown }),
    idempotencyKey: idemKey,
  })
}

export async function deactivateTemplate(key: string): Promise<void> {
  await api(`/api/v1/templates/${encodeURIComponent(key)}/deactivate`, { method: 'POST' })
}

export async function fetchTemplateVersions(key: string): Promise<TemplateVersion[]> {
  const body = await api(`/api/v1/templates/${encodeURIComponent(key)}/versions`)
  return (body.versions ?? []) as TemplateVersion[]
}

/* ---------------- 标签字典与别名（#42） ---------------- */

export interface LabelValue {
  value: string
  label: string
  aliases: string[]
}

export interface LabelDimension {
  name: string
  values: LabelValue[]
}

export interface Dictionary {
  dimensions: LabelDimension[]
  plain?: string[]
}

export interface ResolveMatch {
  label: string
  via: 'exact' | 'alias' | 'fuzzy'
  alias?: string
}

export interface ResolveResult {
  q: string
  matches: ResolveMatch[]
  hint?: string
}

export async function fetchDictionary(): Promise<Dictionary> {
  return api('/api/v1/labels')
}

export async function resolveLabels(q: string): Promise<ResolveResult> {
  return api(`/api/v1/labels/resolve?q=${encodeURIComponent(q)}`)
}

export async function addAlias(label: string, alias: string): Promise<{ label: string; aliases: string[] }> {
  return api('/api/v1/labels/aliases', {
    method: 'POST',
    body: JSON.stringify({ label, alias }),
  })
}

/* 标签治理（仅管理员；后端同样只认管理员） */
export async function renameLabel(label: string, to: string): Promise<{ tickets_updated: number }> {
  return api('/api/v1/labels/rename', {
    method: 'POST',
    body: JSON.stringify({ label, to }),
  })
}

export async function mergeLabels(from: string, into: string): Promise<{ tickets_updated: number }> {
  return api('/api/v1/labels/merge', {
    method: 'POST',
    body: JSON.stringify({ from, into }),
  })
}

export async function deactivateLabel(label: string): Promise<void> {
  await api('/api/v1/labels/deactivate', {
    method: 'POST',
    body: JSON.stringify({ label }),
  })
}

export async function addDimension(name: string): Promise<void> {
  await api('/api/v1/labels/dimensions', {
    method: 'POST',
    body: JSON.stringify({ name }),
  })
}

export async function repointAlias(alias: string, label: string): Promise<void> {
  await api('/api/v1/labels/aliases', {
    method: 'PATCH',
    body: JSON.stringify({ alias, label }),
  })
}

/* ---------------- Agent token（#42 个人设置） ---------------- */

export interface AgentTokenRow {
  id: number
  name: string
  created_at: string
  revoked_at?: string | null
}

export interface IssuedToken {
  id: number
  name: string
  token: string
  created_at: string
  onboarding_text: string
}

export async function fetchAgentTokens(): Promise<AgentTokenRow[]> {
  const body = await api('/api/v1/me/agent-tokens')
  return (body.tokens ?? []) as AgentTokenRow[]
}

export async function issueAgentToken(name: string): Promise<IssuedToken> {
  return api('/api/v1/me/agent-tokens', {
    method: 'POST',
    body: JSON.stringify({ name }),
  })
}

export async function revokeAgentToken(id: number): Promise<void> {
  await api(`/api/v1/me/agent-tokens/${id}`, { method: 'DELETE' })
}

export async function rotateAgentToken(id: number): Promise<IssuedToken> {
  return api(`/api/v1/me/agent-tokens/${id}/rotate`, { method: 'POST' })
}

/* ---------------- 桌面客户端（#80） ---------------- */

export interface ClientCredentialRow {
  id: number
  hostname: string
  created_at: string
  last_used_at: string
  client_version: string
}

export async function fetchClients(): Promise<ClientCredentialRow[]> {
  const body = await api('/api/v1/me/clients')
  return (body.clients ?? []) as ClientCredentialRow[]
}

export async function revokeClient(id: number): Promise<void> {
  await api(`/api/v1/me/clients/${id}`, { method: 'DELETE' })
}

export interface ClientLatest {
  version: string
  sha256: string
  size: number
  download_url: string
}

/** 未代管时返回 null（404 client_not_available），页面显示管理员尚未发布。 */
export async function fetchClientLatest(): Promise<ClientLatest | null> {
  try {
    return await api('/api/v1/client/latest')
  } catch (e) {
    if (e instanceof ApiError && e.status === 404 && e.code === 'client_not_available') return null
    throw e
  }
}

/* ---------------- 成员账号（#42，仅管理员） ---------------- */

export interface MemberAccount {
  id: number
  username: string
  name: string
  active: boolean
  triager: boolean
}

export interface CreatedMember {
  member: MemberAccount & { is_admin: boolean }
  initial_password: string
}

export async function fetchMemberAccounts(): Promise<MemberAccount[]> {
  const body = await api('/api/v1/members')
  return (body.members ?? []) as MemberAccount[]
}

export async function createMember(username: string, name: string): Promise<CreatedMember> {
  return api('/api/v1/members', {
    method: 'POST',
    body: JSON.stringify({ username, name }),
  })
}

export async function deactivateMemberAccount(id: number): Promise<void> {
  await api(`/api/v1/members/${id}/deactivate`, { method: 'POST' })
}

export async function reactivateMemberAccount(id: number): Promise<void> {
  await api(`/api/v1/members/${id}/reactivate`, { method: 'POST' })
}

export interface ResetMemberPasswordResult {
  initial_password: string
}

export async function resetMemberPassword(id: number): Promise<ResetMemberPasswordResult> {
  return api(`/api/v1/members/${id}/reset-password`, { method: 'POST' })
}

export async function setMemberTriager(id: number, triager: boolean): Promise<void> {
  await api(`/api/v1/members/${id}/triager`, {
    method: 'POST',
    body: JSON.stringify({ triager }),
  })
}

/* ---------------- 反馈入口（#165，联系方式仅管理员） ---------------- */

export interface IntakeAccount {
  slug: string
  name: string
  template: string
  active: boolean
  url: string
  department_contact: string
}

export async function fetchIntakes(): Promise<IntakeAccount[]> {
  const body = await api('/api/v1/intakes')
  return (body.intakes ?? []) as IntakeAccount[]
}

export async function updateIntakeContact(slug: string, contact: string): Promise<IntakeAccount> {
  return api(`/api/v1/intakes/${encodeURIComponent(slug)}/contact`, {
    method: 'PATCH',
    body: JSON.stringify({ department_contact: contact }),
  })
}

/* ---------------- 工单写操作 ---------------- */

export async function replyTicket(
  number: string,
  body: { body_md: string; deliver?: boolean },
  idemKey: string = crypto.randomUUID(),
): Promise<void> {
  await api(`/api/v1/tickets/${encodeURIComponent(number)}/replies`, {
    method: 'POST',
    body: JSON.stringify(body),
    idempotencyKey: idemKey,
  })
}

export async function sendDraft(
  id: string,
  version: number,
  opts: { confirmThroughSeq?: number; deliver?: boolean } = {},
): Promise<{ signature: string }> {
  return api(`/api/v1/drafts/${encodeURIComponent(id)}/send`, {
    method: 'POST',
    body: JSON.stringify({
      version,
      ...(opts.confirmThroughSeq === undefined ? {} : { confirm_through_seq: opts.confirmThroughSeq }),
      ...(opts.deliver === undefined ? {} : { deliver: opts.deliver }),
    }),
  })
}

export async function updateDraft(
  id: string,
  baseVersion: number,
  request: { body_md: string; attachment_ids?: string[]; deliver?: boolean },
): Promise<DraftView> {
  return api(`/api/v1/drafts/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify({ base_version: baseVersion, request }),
  })
}

export async function discardDraft(id: string): Promise<void> {
  await api(`/api/v1/drafts/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export async function fetchReplyDrafting(
  ticketNumber: string,
  replyNo: number,
): Promise<{ versions: DraftHistoryVersion[] }> {
  return api(
    `/api/v1/tickets/${encodeURIComponent(ticketNumber)}/drafting/${replyNo}`,
  )
}

export interface LabelChangeResult {
  labels: string[]
  alias_resolved?: { requested: string; label: string; via: string }[]
  created_dictionary_values?: string[]
}

export async function changeLabels(
  number: string,
  body: { add?: string[]; remove?: string[]; allow_new?: boolean },
): Promise<LabelChangeResult> {
  return api(`/api/v1/tickets/${encodeURIComponent(number)}/labels`, {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export async function assignTicket(
  number: string,
  assignee: string,
): Promise<{ assignee: MemberRef | null; share_text?: string | null }> {
  return api(`/api/v1/tickets/${encodeURIComponent(number)}/assign`, {
    method: 'POST',
    body: JSON.stringify({ assignee }),
  })
}

export async function addCollaborator(number: string, member: string): Promise<{ collaborators: Collaborator[] }> {
  return api(`/api/v1/tickets/${encodeURIComponent(number)}/collaborators`, {
    method: 'POST',
    body: JSON.stringify({ member }),
    idempotencyKey: crypto.randomUUID(),
  })
}

export async function removeCollaborator(number: string, memberID: number): Promise<{ collaborators: Collaborator[] }> {
  return api(`/api/v1/tickets/${encodeURIComponent(number)}/collaborators/${memberID}`, {
    method: 'DELETE',
    idempotencyKey: crypto.randomUUID(),
  })
}

export async function leaveCollaborators(number: string): Promise<{ collaborators: Collaborator[] }> {
  return api(`/api/v1/tickets/${encodeURIComponent(number)}/collaborators/leave`, {
    method: 'POST',
    idempotencyKey: crypto.randomUUID(),
  })
}

export async function closeTicket(
  number: string,
  body: { reason: string; duplicate_of?: string },
): Promise<void> {
  await api(`/api/v1/tickets/${encodeURIComponent(number)}/close`, {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export async function reopenTicket(number: string): Promise<void> {
  await api(`/api/v1/tickets/${encodeURIComponent(number)}/reopen`, { method: 'POST' })
}

/* ---------------- 访客链接 ---------------- */

export interface GuestLink {
  id: number
  note: string
  expires_at: string
  revoked_at?: string | null
  can_edit_brief: boolean
  created_by: { member?: { name: string }; agent?: { name: string } | null }
  created_at: string
  use_count?: number
  status: 'unclaimed' | 'claimed' | 'access_code_locked' | 'revoked'
  claim_name?: string
  claimed_at?: string
  device_count?: number
}

export interface CreatedGuestLink {
  id: number
  url: string
  note: string
  expires_at: string
  can_edit_brief: boolean
}

export async function fetchGuestLinks(number: string): Promise<GuestLink[]> {
  const body = await api(`/api/v1/tickets/${encodeURIComponent(number)}/guest-links`)
  return body.links as GuestLink[]
}

export async function createGuestLink(
  number: string,
  body: { note: string; valid_days?: number; expires_at?: string; can_edit_brief?: boolean },
): Promise<CreatedGuestLink> {
  return api(`/api/v1/tickets/${encodeURIComponent(number)}/guest-links`, {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export async function revokeGuestLink(number: string, id: number): Promise<void> {
  await api(`/api/v1/tickets/${encodeURIComponent(number)}/guest-links/${id}`, { method: 'DELETE' })
}

export async function resetGuestAccessCode(
  number: string,
  id: number,
): Promise<{ access_code: string }> {
  return api(`/api/v1/tickets/${encodeURIComponent(number)}/guest-links/${id}/reset-access-code`, {
    method: 'POST',
  })
}

/** 延期：valid_days（7/30/90）或 expires_at（YYYY-MM-DD，管满该日全天）二选一。 */
export async function extendGuestLink(
  number: string,
  id: number,
  body: { valid_days?: number; expires_at?: string },
): Promise<void> {
  await api(`/api/v1/tickets/${encodeURIComponent(number)}/guest-links/${id}/extend`, {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

/* ---------------- 附件（两步上传的第一步；图片先压缩） ---------------- */

export async function uploadMemberAttachment(
  number: string,
  file: File,
  idemKey: string = crypto.randomUUID(),
): Promise<{ id: string; markdown: string; filename: string }> {
  const prepared = file.type.startsWith('image/') ? await compressImage(file) : file
  const form = new FormData()
  form.append('file', prepared, prepared.name)
  return api(`/api/v1/tickets/${encodeURIComponent(number)}/attachments`, {
    method: 'POST',
    body: form,
    idempotencyKey: idemKey,
  })
}

/** 收件箱原因 → 色条 / 徽标配色（mock 的 REASONS + 原因色条）。 */
export function reasonClass(reason: string): string {
  if (reason.includes('交付')) return 'delivered'
  if (reason.includes('访客')) return 'guest'
  if (reason.includes('退回') || reason.includes('回复了')) return 'back'
  return 'new'
}
