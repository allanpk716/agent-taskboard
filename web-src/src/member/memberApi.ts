// 成员网页调用的 API（网页会话 cookie；错误结构见 api.ts）。
import { api } from '../api'
import { compressImage } from '../shared/compress'
import type {
  Author,
  Collaborator,
  CreateDraftRequest,
  CreateDraftView,
  DraftHistoryVersion,
  DraftView,
  TicketView,
} from '../shared/types'
import type { InboxActor, InboxCounterpart } from './inboxText'

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
  trigger: InboxActor
  last_reply_at: string | null
  created_at: string
  counterpart: InboxCounterpart
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

export interface SessionView {
  viewer: { kind: string; member: Me }
  notices?: string[]
}

export async function fetchSession(): Promise<SessionView> {
  return api('/api/v1/session')
}

export async function markDraftsIntroSeen(): Promise<void> {
  await api('/api/v1/me/notices/drafts_intro/seen', { method: 'POST' })
}

export async function markDraftsDirectIntroSeen(): Promise<void> {
  await api('/api/v1/me/notices/drafts_direct_intro/seen', { method: 'POST' })
}

export async function fetchInbox(): Promise<InboxResponse> {
  return api('/api/v1/inbox')
}

export async function fetchWaitingOnCounterparts(): Promise<InboxResponse> {
  return api('/api/v1/inbox/waiting-on-counterparts')
}

export async function fetchDrafts(): Promise<{ count: number; drafts: DraftView[] }> {
  return api('/api/v1/drafts')
}

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
  draft_mode: 'required' | 'direct'
  created_at: string
  revoked_at?: string | null
  draft_mode_switched_by_launch?: boolean
  draft_mode_switched_to_direct?: boolean
}

export interface IssuedToken {
  id: number
  name: string
  token: string
  created_at: string
  onboarding_text: string
}

export interface AgentTokensView {
  tokens: AgentTokenRow[]
  skill_install_text: string
}

export async function fetchAgentTokens(): Promise<AgentTokensView> {
  const body = await api('/api/v1/me/agent-tokens')
  return {
    tokens: (body.tokens ?? []) as AgentTokenRow[],
    skill_install_text: (body.skill_install_text ?? '') as string,
  }
}

export async function issueAgentToken(
  name: string,
  draftMode: 'required' | 'direct',
): Promise<IssuedToken> {
  return api('/api/v1/me/agent-tokens', {
    method: 'POST',
    body: JSON.stringify({ name, draft_mode: draftMode }),
  })
}

export async function setAgentTokenDraftMode(
  id: number,
  draftMode: 'required' | 'direct',
): Promise<void> {
  await api(`/api/v1/me/agent-tokens/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ draft_mode: draftMode }),
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

/* ---------------- 成员账号（#42，仅管理员） ---------------- */

export interface MemberAccount {
  id: number
  username: string
  name: string
  active: boolean
  triager: boolean
  is_admin: boolean
}

export interface CreatedMember {
  member: MemberAccount
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

export interface LoginLock {
  username: string
  name: string
  ip: string
  remaining_minutes: number
}

export async function fetchLoginLocks(): Promise<LoginLock[]> {
  const body = await api('/api/v1/admin/login-locks')
  return (body.locks ?? []) as LoginLock[]
}

export async function unlockLogin(username: string, ip: string): Promise<void> {
  await api('/api/v1/admin/login-locks/unlock', {
    method: 'POST',
    body: JSON.stringify({ username, ip }),
  })
}

export async function setMemberTriager(id: number, triager: boolean): Promise<void> {
  await api(`/api/v1/members/${id}/triager`, {
    method: 'POST',
    body: JSON.stringify({ triager }),
  })
}

export async function setMemberAdmin(id: number, admin: boolean): Promise<void> {
  await api(`/api/v1/members/${id}/admin`, {
    method: 'POST',
    body: JSON.stringify({ admin }),
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
): Promise<{
  signature: string
  result: {
    number: string
    seq: number
    url: string
    warnings: { field: string; message: string }[]
    assignee?: { id: number; name: string } | null
    guest_link?: { url: string; note: string; expires_at: string; can_edit_brief: boolean; share_text: string }
    share_text?: string | null
  }
}> {
  return api(`/api/v1/drafts/${encodeURIComponent(id)}/send`, {
    method: 'POST',
    body: JSON.stringify({
      version,
      ...(opts.confirmThroughSeq === undefined ? {} : { confirm_through_seq: opts.confirmThroughSeq }),
      ...(opts.deliver === undefined ? {} : { deliver: opts.deliver }),
    }),
  })
}

export async function fetchDraft(id: string): Promise<DraftView | CreateDraftView> {
  return api(`/api/v1/drafts/${encodeURIComponent(id)}`)
}

export async function updateCreateDraft(
  id: string,
  baseVersion: number,
  request: CreateDraftRequest,
): Promise<CreateDraftView> {
  return api(`/api/v1/drafts/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify({ base_version: baseVersion, request }),
  })
}

export async function updateDraft(
  id: string,
  baseVersion: number,
  request: {
    body_md?: string
    title?: string | null
    brief_md?: string | null
    fields?: Record<string, string> | null
    attachment_ids?: string[]
    deliver?: boolean
    base_revision?: number
  },
): Promise<DraftView> {
  return api(`/api/v1/drafts/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify({ base_version: baseVersion, request }),
  })
}

export async function fetchBriefDrafting(
  ticketNumber: string,
  revision: number,
): Promise<{ versions: import('../shared/types').BriefVersion[] }> {
  return api(
    `/api/v1/tickets/${encodeURIComponent(ticketNumber)}/brief-drafting/${revision}`,
  )
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
  guest_id?: number
  guest_name?: string
  expires_at: string
  revoked_at?: string | null
  can_edit_brief: boolean
  created_by: { member?: { name: string }; agent?: { name: string } | null }
  created_at: string
  use_count?: number
  share_text_available?: boolean
  status: 'unclaimed' | 'claimed' | 'access_code_locked' | 'revoked'
  claim_name?: string
  claimed_at?: string
  device_count?: number
}

export interface CreatedGuestLink {
  id: number
  url: string
  note: string
  guest_id?: number
  guest_name?: string
  expires_at: string
  can_edit_brief: boolean
}

export interface GuestSummary {
  id: number
  name: string
  disabled_at?: string
  merged_into_guest_id?: number
  merged_at?: string
  claimed_at?: string
  claim_name?: string
  access_code_locked_at?: string
  link_count: number
  /** 待处理数：当前轮到访客的工单数（按查看者可见范围过滤）。 */
  pending_count: number
  /** 最近联系：访客最近一次经名下链接打开工单的时间。 */
  last_contact_at?: string
  device_count: number
  created_at: string
}

export interface GuestDetailLink {
  id: number
  note: string
  expires_at: string
  revoked_at?: string | null
  can_edit_brief: boolean
  status: string
  claim_name?: string
}

export interface GuestDetailTicket {
  number: string
  title: string
  status: string
  waiting_on_guest: boolean
  waiting_reason: string
  api: string
  links: GuestDetailLink[]
}

export interface GuestDetail {
  guest: GuestSummary & { created_by?: { member?: { name: string }; agent?: { name: string } | null } }
  tickets: GuestDetailTicket[]
}

export interface UnassignedGuestLink {
  id: number
  ticket_number: string
  ticket_title: string
  note: string
  claimed: boolean
  claim_name: string
  last_used_at?: string | null
  possible_person: boolean
}

export interface GuestLinkAssignmentPreview {
  guest_id: number
  guest_name: string
  guest_claimed: boolean
  link_count: number
  ticket_count: number
  claimed_link_count: number
  device_count: number
  access_code_action:
    | 'keep_guest'
    | 'remain_unclaimed'
    | 'choice_required'
    | 'reuse_source_link'
    | 'generate'
  source_link_ids: number[]
  source_link_id?: number
  source_device_count: number
  invalidated_device_count: number
}

export type GuestAccessCodeChoiceBody =
  | { source_link_id: number }
  | 'generate'

export interface GuestLinkAssignmentResult {
  guest_id: number
  guest_name: string
  link_count: number
  ticket_count: number
  access_code_action: 'keep_guest' | 'remain_unclaimed' | 'reuse_source_link' | 'generate'
  access_code?: string
  links: Array<{ id: number; ticket_number: string; ticket_title: string }>
}

/** 部门访客名单：按名字包含搜索（#328）。 */
export async function fetchGuests(query = ''): Promise<GuestSummary[]> {
  const path = query
    ? `/api/v1/guests?q=${encodeURIComponent(query)}`
    : '/api/v1/guests'
  const body = await api(path)
  return body.guests as GuestSummary[]
}

export async function fetchGuestLinks(number: string): Promise<GuestLink[]> {
  const body = await api(`/api/v1/tickets/${encodeURIComponent(number)}/guest-links`)
  return body.links as GuestLink[]
}

/** 单独新建访客（#339）：不建工单、不带链接；新访客未认领、名下没有链接。 */
export async function createGuest(name: string): Promise<{ guest: GuestSummary }> {
  return api('/api/v1/guests', {
    method: 'POST',
    body: JSON.stringify({ name }),
  })
}

export async function createGuestLink(
  number: string,
  body: {
    note: string
    guest_id?: number
    new_guest_name?: string
    valid_days?: number
    expires_at?: string
    can_edit_brief?: boolean
  },
): Promise<CreatedGuestLink> {
  return api(`/api/v1/tickets/${encodeURIComponent(number)}/guest-links`, {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export async function copyGuestLinkShareText(
  number: string,
  id: number,
): Promise<{ share_text: string }> {
  return api(`/api/v1/tickets/${encodeURIComponent(number)}/guest-links/${id}/share-text`, {
    method: 'POST',
  })
}

/** 复制访客主页文案：地址可重复生成，响应不含访问码。 */
export async function copyGuestHomeText(
  guestId: number,
): Promise<{ home_url: string; share_text: string }> {
  return api(`/api/v1/guests/${guestId}/home-share-text`, { method: 'POST' })
}

/** 访客详情：工单与链接已按当前成员可见范围过滤。 */
export async function fetchGuestDetail(guestId: number): Promise<GuestDetail> {
  return api(`/api/v1/guests/${guestId}`)
}

/** 无主老链接列表：q 搜索；name 会把名字相近的链接排前并标记。 */
export async function fetchUnassignedGuestLinks(
  opts: { q?: string; name?: string } = {},
): Promise<UnassignedGuestLink[]> {
  const params = new URLSearchParams()
  if (opts.q) params.set('q', opts.q)
  if (opts.name) params.set('name', opts.name)
  const suffix = params.toString() ? `?${params}` : ''
  const body = await api(`/api/v1/guest-links/unassigned${suffix}`)
  return body.links as UnassignedGuestLink[]
}

/** 批量归属影响预览：choice_required 时带所选访问码方案，后果随选项变化。 */
export async function previewGuestLinkAssignment(
  guestId: number,
  linkIds: number[],
  accessCode?: GuestAccessCodeChoiceBody,
): Promise<GuestLinkAssignmentPreview> {
  const params = new URLSearchParams({ link_ids: linkIds.join(',') })
  if (accessCode === 'generate') params.set('access_code', 'generate')
  else if (accessCode) params.set('source_link_id', String(accessCode.source_link_id))
  return api(`/api/v1/guests/${guestId}/assign-links/preview?${params}`)
}

/** 批量归属无主老链接；需要选择访问码时必须带 source_link_id 或 generate。 */
export async function assignGuestLinks(
  guestId: number,
  linkIds: number[],
  accessCode?: GuestAccessCodeChoiceBody,
): Promise<GuestLinkAssignmentResult> {
  return api(`/api/v1/guests/${guestId}/assign-links`, {
    method: 'POST',
    body: JSON.stringify({ link_ids: linkIds, ...(accessCode ? { access_code: accessCode } : {}) }),
    idempotencyKey: crypto.randomUUID(),
  })
}

/** 给访客改名：访客建立者、管理员或名下链接管理人可用，成员 Agent 同样可用。 */
export async function renameGuest(guestId: number, name: string): Promise<{ guest: GuestSummary }> {
  return api(`/api/v1/guests/${guestId}/rename`, {
    method: 'POST',
    body: JSON.stringify({ name }),
  })
}

/** 停用访客：主页与名下全部链接一起失效；仅网页成员会话可用。 */
export async function disableGuest(guestId: number): Promise<void> {
  await api(`/api/v1/guests/${guestId}/disable`, { method: 'POST' })
}

/** 恢复已停用的访客：未过期链接按原有效期重新生效。 */
export async function enableGuest(guestId: number): Promise<void> {
  await api(`/api/v1/guests/${guestId}/enable`, { method: 'POST' })
}

export async function rotateGuestLinkKey(
  number: string,
  id: number,
  idemKey: string = crypto.randomUUID(),
): Promise<{
  url: string
  share_text: string
  reset_text: string
  access_code?: string
  expires_at: string
}> {
  return api(`/api/v1/tickets/${encodeURIComponent(number)}/guest-links/${id}/rotate-key`, {
    method: 'POST',
    idempotencyKey: idemKey,
  })
}

export async function revokeGuestLink(number: string, id: number): Promise<void> {
  await api(`/api/v1/tickets/${encodeURIComponent(number)}/guest-links/${id}`, { method: 'DELETE' })
}

/** 无主老链接的链接级重置访问码：有主链接请改用访客级接口。 */
export async function resetGuestAccessCode(
  number: string,
  id: number,
): Promise<{
  access_code: string
  url: string
  reset_text: string
  expires_at: string
}> {
  return api(`/api/v1/tickets/${encodeURIComponent(number)}/guest-links/${id}/reset-access-code`, {
    method: 'POST',
  })
}

export interface GuestAccessCodeResetPreview {
  guest_id: number
  guest_name: string
  device_count: number
  ticket_count: number
  access_code_locked: boolean
}

/** 访客级重置访问码的影响预览：将作废的设备数与涉及的工单数。 */
export async function previewGuestAccessCodeReset(
  guestId: number,
): Promise<GuestAccessCodeResetPreview> {
  return api(`/api/v1/guests/${guestId}/reset-access-code/preview`)
}

/** 访客级重置访问码：作废名下全部设备并清除锁定；新码只返回一次。 */
export async function resetGuestAccessCodeForGuest(
  guestId: number,
): Promise<{
  guest_id: number
  access_code: string
  home_url: string
  reset_text: string
}> {
  return api(`/api/v1/guests/${guestId}/reset-access-code`, {
    method: 'POST',
    idempotencyKey: crypto.randomUUID(),
  })
}

/** 重置访客主页地址：旧主页地址失效，访问码与设备不变。 */
export async function resetGuestHomeKey(
  guestId: number,
): Promise<{ guest_id: number; home_url: string; share_text: string }> {
  return api(`/api/v1/guests/${guestId}/reset-home-key`, {
    method: 'POST',
    idempotencyKey: crypto.randomUUID(),
  })
}

export interface GuestMergePreview {
  source_guest_id: number
  source_guest_name: string
  target_guest_id: number
  target_guest_name: string
  link_count: number
  device_count: number
}

/** 合并重复访客的影响预览：B 将改归多少条链接、作废多少台设备。 */
export async function previewGuestMerge(
  sourceGuestId: number,
  targetGuestId: number,
): Promise<GuestMergePreview> {
  return api(`/api/v1/guests/${sourceGuestId}/merge-into/${targetGuestId}/preview`)
}

/** 合并重复访客：不可撤回；仅网页成员本人可操作。 */
export async function mergeGuest(
  sourceGuestId: number,
  targetGuestId: number,
): Promise<{
  source_guest_id: number
  target_guest_id: number
  link_count: number
  device_count: number
}> {
  return api(`/api/v1/guests/${sourceGuestId}/merge-into`, {
    method: 'POST',
    body: JSON.stringify({ target_guest_id: targetGuestId }),
    idempotencyKey: crypto.randomUUID(),
  })
}

/** 把上线前的无主老链接归到已有访客或新建访客名下。 */
export async function assignGuestLinkToGuest(
  number: string,
  id: number,
  body: { guest_id?: number; new_guest_name?: string },
): Promise<GuestLink> {
  return api(`/api/v1/tickets/${encodeURIComponent(number)}/guest-links/${id}/assign-guest`, {
    method: 'POST',
    body: JSON.stringify(body),
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
