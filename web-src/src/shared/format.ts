// 时间与文件大小的展示格式（照 prototype/guest-web a.html 的写法）。
import type { Author } from './types'

/** RFC3339 → 「10-15 09:32」（直接取字符串，避免时区换算）。 */
export function fmtShort(iso: string): string {
  return `${iso.slice(5, 7)}-${iso.slice(8, 10)} ${iso.slice(11, 16)}`
}

/** RFC3339 → 「11月14日」。 */
export function fmtDateCN(iso: string): string {
  return `${parseInt(iso.slice(5, 7), 10)}月${parseInt(iso.slice(8, 10), 10)}日`
}

/** 字节数 → 「890 KB」/「1.2 MB」。 */
export function fmtSize(bytes: number): string {
  if (bytes < 1024 * 1024) return `${Math.max(1, Math.round(bytes / 1024))} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

/** 站内地址 → 能直接发给别人打开的绝对地址（评审 P2-7）：API 已给绝对
 *  地址（服务端配了 public_url）就原样返回；相对路径用当前页面的 origin 拼出。 */
export function absoluteUrl(path: string): string {
  return new URL(path, window.location.origin).href
}

/** 访客链接到期展示：时刻到期的显示「X月X日到期」；恰好在某日 00:00 到期
 *  （日期写法，管满前一天全天，评审 P2-8）的显示「X月X日为止」，不让人误读。
 *  日历运算直接在字符串上做（服务端固定回北京时间），不经过本地时区。 */
export function fmtLinkExpiry(iso: string): string {
  if (iso.slice(11, 19) === '00:00:00') {
    const y = parseInt(iso.slice(0, 4), 10)
    const m = parseInt(iso.slice(5, 7), 10)
    const d = parseInt(iso.slice(8, 10), 10)
    const prev = new Date(Date.UTC(y, m - 1, d) - 86400_000) // 前一天
    return `${prev.getUTCMonth() + 1}月${prev.getUTCDate()}日为止`
  }
  return `${fmtDateCN(iso)}到期`
}

/** 访客链接是否已过期（已撤销另算）。 */
export function linkExpired(iso: string): boolean {
  return new Date(iso).getTime() <= Date.now()
}

/** 关闭原因文案。 */
export function closeReasonText(reason?: string | null): string {
  switch (reason) {
    case 'done':
      return '完成'
    case 'wontfix':
      return '不处理'
    case 'duplicate':
      return '重复'
    default:
      return '已关闭'
  }
}

/** 作者的展示署名（成员 / 成员 Agent / 访客及其自报，ADR 0003 与 docs/api.md）。
 * 已撤销的成员 Agent 追加「（已停用）」（评审 P2-10）。 */
export function authorName(a: Author | undefined | null): string {
  if (!a) return '访客'
  if (a.member) {
    if (!a.agent) return a.member.name
    return a.agent.revoked
      ? `${a.member.name} 的 Agent · ${a.agent.name}（已停用）`
      : `${a.member.name} 的 Agent · ${a.agent.name}`
  }
  const name = a.author_name?.trim()
  if (name) return a.via_agent ? `${name} 的 Agent（自报）` : name
  const note = a.link_note
  return note ? `访客（经「${note}」链接）` : '访客'
}

/** 头像字符：成员/访客取署名首字，成员 Agent 显示机器人角标（调用方处理）。 */
export function avatarChar(display: string): string {
  const ch = display.trim().charAt(0)
  return ch || '?'
}
