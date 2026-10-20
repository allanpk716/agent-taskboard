import { expect, test } from '@playwright/test'
import { ADMIN, createTicket, login, wang } from './helpers'

test('详情页管理协作者并支持本人退出', async ({ page, request, browser }) => {
  const number = await createTicket(request, { title: '协作者界面流程', assignee: ADMIN.username })
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
  const addedBody = await added.json()
  expect(addedBody.collaborators.map((c: any) => c.name)).toContain('王工')

  const adminContext = await browser.newContext()
  const adminView = await adminContext.newPage()
  await login(adminView, ADMIN)
  await adminView.goto(`/t/${number}`)
  const who = await wang()
  await login(page, who)
  await page.goto(`/t/${number}`)
  await page.getByRole('button', { name: '🤝 协作者' }).click()
  const leaveDone = page.waitForResponse(
    (res) => res.url().includes('/collaborators/leave') && res.request().method() === 'POST',
  )
  await page.getByRole('button', { name: '退出', exact: true }).click()
  await leaveDone

  const view = await (await request.get(`/api/v1/tickets/${number}`)).json()
  const leftAt = view.timeline.filter((e: any) => e.type === 'collaborator_left').at(-1).at as string
  const expected = `王工 退出了协作 · ${leftAt.slice(5, 7)}-${leftAt.slice(8, 10)} ${leftAt.slice(11, 16)}`
  await adminView.goto(`/t/${number}`)
  await expect(adminView.locator('.event').last()).toHaveText(expected)
  await adminContext.close()
  await page.reload()
  await expect(page.locator('.ecard')).toHaveText('找不到这张工单，或你没有权限查看')
})
