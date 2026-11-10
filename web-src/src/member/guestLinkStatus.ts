import { linkExpired } from '../shared/format'
import type { GuestLink } from './memberApi'

/** 成员侧各类访客链接列表共用的状态文案；撤销与过期优先于认领状态。 */
export function guestLinkStatusText(link: GuestLink): string {
  if (link.revoked_at || link.status === 'revoked') return '已撤销'
  if (linkExpired(link.expires_at)) return '已过期'
  if (link.status === 'access_code_locked') {
    return `已认领 · ${link.claim_name ?? ''} · 输码已锁定`
  }
  if (link.claimed_at || link.status === 'claimed') {
    return `已认领 · ${link.claim_name ?? ''}`
  }
  return '未认领'
}
