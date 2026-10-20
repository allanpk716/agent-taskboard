import { expect, test } from '@playwright/test'
import { createTicket, login, loginAdmin, wang } from './helpers'

test('ticket unread dot appears and clears after detail view', async ({ page, request }) => {
  const number = await createTicket(request, {
    title: '未读点端到端',
    assignee: 'wang',
  })
  const b = await wang()
  await login(page, b)
  await page.goto(`/t/${number}`)

  await loginAdmin(request)
  const reply = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: '成员 A 的回复' },
  })
  expect(reply.status(), '造成员 A 回复失败').toBe(201)

  await page.goto('/tickets')
  const title = page.locator('.tcard', { hasText: '未读点端到端' }).locator('.tc-title')
  await expect(title).toHaveClass(/unread/)
  await expect(title.locator('.unread-dot')).toHaveCount(1)

  await page.setViewportSize({ width: 1200, height: 800 })
  await page.goto('/tickets')
  const row = page.locator('.pc-row-title', { hasText: '未读点端到端' })
  await expect(row).toHaveClass(/unread/)
  await expect(row.locator('.unread-dot')).toHaveCount(1)

  await page.locator('.tcard', { hasText: '未读点端到端' }).click()
  await expect(page).toHaveURL(new RegExp(`/t/${number}`))
  await page.goto('/tickets')
  const readTitle = page.locator('.pc-row-title', { hasText: '未读点端到端' })
  await expect(readTitle).not.toHaveClass(/unread/)
  await expect(readTitle.locator('.unread-dot')).toHaveCount(0)
})
