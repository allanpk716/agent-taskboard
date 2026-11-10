import type { UnassignedGuestLink } from './memberApi'

interface Props {
  guestName: string
  guestClaimed: boolean
  selectedCount: number
  claimedCount: number
  sourceLinks?: UnassignedGuestLink[]
  selectedSourceId?: number
  generateSelected?: boolean
}

/** 批量归属的后果说明：先讲访问码，再讲旧凭证和链接适用范围。 */
export function GuestAssignConsequence({
  guestName,
  guestClaimed,
  selectedCount,
  claimedCount,
  sourceLinks = [],
  selectedSourceId,
  generateSelected,
}: Props) {
  const person = guestName.includes('-') ? guestName.split('-').slice(1).join('-') : guestName
  if (guestClaimed) {
    return (
      <div className="assign-consequence" data-testid="guest-assign-consequence">
        <strong>归属后果</strong>
        <p>
          保留 {person} 的访问码；所选老链接原来的访问码和设备都会作废。这几条链接以后用
          TA 的访问码。
        </p>
      </div>
    )
  }
  if (claimedCount > 0) {
    const source = sourceLinks.find((link) => link.id === selectedSourceId)
    if (generateSelected) {
      return (
        <div className="assign-consequence" data-testid="guest-assign-consequence">
          <strong>归属后果</strong>
          <p>
            为 {person}生成新的 6 位访问码，只显示一次；TA 变为已认领，称呼取访客名字，没有已登录设备。
            所选链接原来的访问码和设备都会作废。
          </p>
        </div>
      )
    }
    if (source) {
      return (
        <div className="assign-consequence" data-testid="guest-assign-consequence">
          <strong>归属后果</strong>
          <p>
            {source.ticket_number} 的访问码和已登录设备会转给 {person}，认领状态、称呼、失败计数和锁定一并沿用
            {selectedCount > 1
              ? `；其余 ${selectedCount - 1} 条所选链接原来的访问码和设备都会作废。`
              : '。'}
          </p>
        </div>
      )
    }
    return (
      <div className="assign-consequence" data-testid="guest-assign-consequence">
        <strong>归属后果</strong>
        <p>
          {person}还未认领，而所选链接里有 {claimedCount}{' '}
          条已认领：请选择沿用哪条访问码，或生成新的访问码。
        </p>
      </div>
    )
  }
  return (
    <div className="assign-consequence" data-testid="guest-assign-consequence">
      <strong>归属后果</strong>
      <p>
        访客保持未认领；所选 {selectedCount} 条老链接都没有被认领过，不用选择访问码。TA
        第一次打开主页或链接时再设置新访问码。
      </p>
    </div>
  )
}
