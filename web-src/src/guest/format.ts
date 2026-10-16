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

/** 作者的展示署名（成员 / 成员 Agent / 访客及其自报，ADR 0003 与 docs/api.md）。 */
export function authorName(a: Author | undefined | null): string {
  if (!a) return '访客'
  if (a.member) {
    return a.agent ? `${a.member.name} 的 Agent · ${a.agent.name}` : a.member.name
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
