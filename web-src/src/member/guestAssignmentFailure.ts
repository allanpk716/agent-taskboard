import { ApiError } from '../api'
import { nav, toast } from './ui'

/** 新建访客后归属失败：访客不回滚，转到详情页并把失败原因留在提示里。 */
export function guestCreatedAssignmentFailed(
  guestID: number,
  guestName: string,
  error: unknown,
): void {
  window.dispatchEvent(new Event('tb:invalidate'))
  const reason = error instanceof ApiError ? `${error.code}：${error.message}` : '请到详情页重试'
  toast(`已建立访客「${guestName}」，但归入老工单失败：${reason}`, 6000)
  nav(`/guests/${guestID}`)
}
