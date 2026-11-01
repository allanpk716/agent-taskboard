import { expect, test } from '@playwright/test'
import { ADMIN, li, login, wang } from './helpers'

/** A（管理员）在网页建单：指派给王工、抄送李工，返回工单编号。 */
async function createTicketWithCollaborator(page: import('@playwright/test').Page): Promise<string> {
  await login(page, ADMIN)
  await page.goto('/new')
  await page.getByRole('button', { name: /我自己填/ }).click()
  await page.getByText('自由格式').click()
  await page.getByLabel('标题').fill('抄送李工的派活工单')
  await page.getByLabel('说明').fill('请王工处理，李工知情跟进。')
  await page.getByLabel('标签输入').fill('机型:UD-2300')
  await page.getByRole('button', { name: '添加', exact: true }).click()
  await expect(page.locator('.lblrow .chip').filter({ hasText: '机型:UD-2300' })).toBeVisible()
  await page.getByLabel('指派给').selectOption('王工')
  await page.getByLabel('选择协作者').selectOption({ label: '李工' })
  await expect(page.locator('.collaborator-chip')).toContainText('李工')
  await page.getByRole('button', { name: '创建工单' }).click()
  await expect(page.locator('.donet')).toContainText('已创建')
  const doneText = (await page.locator('.donet').textContent()) ?? ''
  return doneText.match(/(\d{8}-\d+)/)![1]
}

test('协作中分页列出抄送工单并显示等待方与未读', async ({ page }) => {
  const number = await createTicketWithCollaborator(page)

  // 李工（协作者）的收件箱：默认仍打开「轮到我」，切到「协作中」后看到这张单。
  await login(page, await li())
  await page.goto('/')
  await expect(page.getByRole('tab', { name: '轮到我' })).toHaveAttribute('aria-selected', 'true')
  await expect(page.locator('.tcard', { hasText: number })).toHaveCount(0)
  const collabTab = page.getByRole('tab', { name: /协作中 · 1/ })
  await expect(collabTab).toBeVisible()
  await collabTab.click()
  const card = page.locator('.tcard', { hasText: number })
  await expect(card).toBeVisible()
  await expect(card.locator('.hint').first()).toHaveText('等 王工 处理')
  await expect(card.locator('.tc-title')).toHaveClass(/unread/)
  await expect(card.locator('.unread-dot')).toHaveCount(1)
  await expect(card.locator('.tc-meta')).toContainText(number)

  // 打开工单后未读消失；「轮到我」与收件箱角标不受影响。
  await card.click()
  await expect(page).toHaveURL(new RegExp(`/t/${number}`))
  await page.goto('/')
  await page.getByRole('tab', { name: '协作中' }).click()
  const readCard = page.locator('.tcard', { hasText: number })
  await expect(readCard.locator('.tc-title')).not.toHaveClass(/unread/)
  await expect(readCard.locator('.unread-dot')).toHaveCount(0)
  await page.getByRole('tab', { name: '轮到我' }).click()
  await expect(page.locator('.tcard', { hasText: number })).toHaveCount(0)
  await expect(page.locator('.b-tabs .tabbtn', { hasText: '收件箱' }).locator('.dot')).toHaveCount(0)

  // 工单列表的「我协作的」也能筛出这张单。
  await page.goto('/tickets')
  await page.getByRole('button', { name: '我协作的' }).click()
  const listCard = page.locator('.tcard', { hasText: number })
  await expect(listCard).toBeVisible()
  await expect(listCard.locator('.tc-meta')).toContainText(number)
  await expect(listCard.locator('.tc-top .hint').first()).toContainText('等待 王工')

  // 宽屏：左侧收件箱栏同样有「协作中」分页。
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto('/')
  await page.getByRole('button', { name: /协作中/ }).click()
  const row = page.locator('[data-testid="inbox-list-pane"] .pc-row', { hasText: number })
  await expect(row).toBeVisible()
  await expect(row.locator('.pc-row-turn')).toContainText('王工 处理')

  // 王工（指派人）不会在「协作中」里看到这张单。
  await login(page, await wang())
  await page.goto('/?inbox=collab')
  await expect(page.locator('.tcard', { hasText: number })).toHaveCount(0)
})

test('手机视口切换收件箱两个分页并截图', async ({ page }) => {
  const number = await createTicketWithCollaborator(page)
  await login(page, await li())
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')
  await expect(page.getByRole('tab', { name: '轮到我' })).toBeVisible()
  await page.getByRole('tab', { name: /协作中/ }).click()
  await expect(page.locator('.tcard', { hasText: number })).toBeVisible()
  await page.screenshot({ path: 'test-results/inbox-collaborating-mobile.png', fullPage: true })
  await page.getByRole('tab', { name: '轮到我', exact: true }).click()
  await expect(page.locator('.b-note')).toHaveText('现在轮到你处理的工单。')
})
