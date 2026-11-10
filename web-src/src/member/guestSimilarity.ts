// 相似访客判定（#339）：新建访客时避免建出重复的人。规则按规格取简单：
// 同名、互相包含、去掉“部门-”前缀后相同。#341/#343 的老链接推荐也复用。
import type { GuestSummary } from './memberApi'

/** 去掉名字里第一个“-”前的部门前缀；没有“-”就原样返回。 */
function withoutDept(name: string): string {
  const i = name.indexOf('-')
  return i >= 0 ? name.slice(i + 1) : name
}

export function similarGuestName(input: string, name: string): boolean {
  const a = input.trim()
  const b = name.trim()
  if (!a || !b) return false
  return a === b || a.includes(b) || b.includes(a) || withoutDept(a) === withoutDept(b)
}

/** 输入名字命中的重名/相似已有访客；完全同名的排最前，其余按名字排序。 */
export function similarGuests(input: string, guests: GuestSummary[]): GuestSummary[] {
  const wanted = input.trim()
  return guests
    .filter((g) => similarGuestName(wanted, g.name))
    .sort((a, b) => {
      const ea = a.name === wanted ? 0 : 1
      const eb = b.name === wanted ? 0 : 1
      return ea - eb || a.name.localeCompare(b.name, 'zh-Hans-CN')
    })
}
