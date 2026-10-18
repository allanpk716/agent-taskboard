import { test, expect } from '@playwright/test'
import { createTicket, login, wang } from './helpers'

test('成员打开工单详情后这张工单的通知变为已读', async ({ page, request }) => {
  const number = await createTicket(request, { title: '打开详情会标已读的工单', assignee: '王工' })
  await login(page, await wang())

  const before = await page.context().request.get('/api/v1/notifications')
  expect(before.ok()).toBeTruthy()
  const beforeBody = await before.json()
  const item = beforeBody.items.find((n: any) => n.ticket.number === number)
  expect(item, `没有找到 ${number} 的通知`).toBeTruthy()
  expect(item.read_at).toBeNull()

  const marking = page.waitForResponse((res) =>
    res.request().method() === 'POST' && res.url().includes('/api/v1/notifications/read'))
  await page.goto(`/t/${number}`)
  await expect(page.getByRole('heading', { name: '打开详情会标已读的工单' })).toBeVisible()
  const marked = await marking
  expect(marked.status()).toBe(200)
  expect(await marked.json()).toEqual(expect.objectContaining({ unread_count: 0 }))

  const after = await page.context().request.get('/api/v1/notifications')
  expect(after.ok()).toBeTruthy()
  const afterBody = await after.json()
  expect(afterBody.items.find((n: any) => n.ticket.number === number).read_at).toBeTruthy()
  expect(afterBody.unread_count).toBe(0)
})
