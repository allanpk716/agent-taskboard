import { expect, test } from '@playwright/test'
import { ADMIN, createTicket, login, wang } from './helpers'

test('详情页管理协作者并支持本人退出', async ({ page, request }) => {
  const number = await createTicket(request, { title: '协作者界面流程' })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.getByRole('button', { name: '🤝 协作者' }).click()
  await page.getByLabel('选择成员').selectOption({ label: '王工' })
  await page.getByRole('button', { name: '加入', exact: true }).click()
  const row = page.locator('[data-collaborator]', { hasText: '王工' })
  await expect(row).toBeVisible()
  await page.getByRole('button', { name: '完成', exact: true }).click()
  await expect(page.locator('.conv .event').last()).toContainText('把 王工 加入协作')

  await page.getByRole('button', { name: '🤝 协作者' }).click()
  await row.getByRole('button', { name: '移除 王工' }).click()
  await expect(row).toHaveCount(0)
  await expect(page.locator('.conv .event').last()).toContainText('把 王工 移出协作')
  await page.getByRole('button', { name: '完成', exact: true }).click()

  const added = await request.post(`/api/v1/tickets/${number}/collaborators`, { data: { member: '王工' } })
  expect(added.ok(), await added.text()).toBeTruthy()
  const who = await wang()
  await login(page, who)
  await page.goto(`/t/${number}`)
  await page.getByRole('button', { name: '🤝 协作者' }).click()
  await page.getByRole('button', { name: '退出', exact: true }).click()
  await expect(page.locator('.conv .event').last()).toContainText('王工 退出了协作')
  await expect(page.locator('[data-collaborator]', { hasText: '王工' })).toHaveCount(0)
})
