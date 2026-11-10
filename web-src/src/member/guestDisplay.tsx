// 访客展示共用件：部门分组、短名与头像。名单（#345）、建单向导的选访客
// （#340）与后续归属面板（#341/#342）都按同一套通讯录样式展示访客。
import type { GuestSummary } from './memberApi'

/** 部门 = 名字里第一个“-”前面的部分；没有“-”的归“未分组”。 */
export function guestDept(name: string): string {
  const i = name.indexOf('-')
  return i > 0 ? name.slice(0, i) : '未分组'
}

/** 名字展示：通讯录行用全名；最近联系横排用“-”后的短名。 */
export function guestShortName(name: string): string {
  const i = name.indexOf('-')
  return i >= 0 && i < name.length - 1 ? name.slice(i + 1) : name
}

const AVATAR_COLORS = 5

function avatarClass(name: string): string {
  let hash = 0
  for (const ch of name) hash = (hash * 31 + ch.codePointAt(0)!) % 997
  return `gav-c${hash % AVATAR_COLORS}`
}

function avatarInitial(name: string): string {
  return [...guestShortName(name)][0] ?? '?'
}

export function Avatar({ guest, size }: { guest: GuestSummary; size: 'sm' | 'md' }) {
  return (
    <span className={`guest-avatar ${size} ${avatarClass(guest.name)}`} aria-hidden="true">
      {avatarInitial(guest.name)}
    </span>
  )
}
