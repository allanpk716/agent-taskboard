import { test, expect } from '@playwright/test'
import { createTicket, login, wang } from './helpers'

test('成员打开工单详情后通知仍保持未读', async ({ page, request }) => {
  const number = await createTicket(request, { title: '打开详情会标已读的工单', assignee: '王工' })
  await login(page, await wang())

  const before = await page.context().request.get('/api/v1/notifications')
  expect(before.ok()).toBeTruthy()
  const beforeBody = await before.json()
  const item = beforeBody.items.find((n: any) => n.ticket.number === number)
  expect(item, `没有找到 ${number} 的通知`).toBeTruthy()
  expect(item.read_at).toBeNull()

  await page.goto(`/t/${number}`)
  await expect(page.getByRole('heading', { name: '打开详情会标已读的工单' })).toBeVisible()

  const after = await page.context().request.get('/api/v1/notifications')
  expect(after.ok()).toBeTruthy()
  const afterBody = await after.json()
  expect(afterBody.items.find((n: any) => n.ticket.number === number).read_at).toBeNull()
})
