interface Props {
  guestName: string
  guestClaimed: boolean
  selectedCount: number
  claimedCount: number
}

/** 批量归属的后果说明：先讲访问码，再讲旧凭证和链接适用范围。 */
export function GuestAssignConsequence({
  guestName,
  guestClaimed,
  selectedCount,
  claimedCount,
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
    return (
      <div className="assign-consequence" data-testid="guest-assign-consequence">
        <strong>归属后果</strong>
        <p>
          {person}还未认领，而所选链接里有 {claimedCount}{' '}
          条已认领：需要选择沿用哪条访问码或生成新的访问码。当前版本暂未开放这一步，请先调整所选链接。
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
