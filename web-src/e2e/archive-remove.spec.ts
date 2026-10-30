import { test, expect } from '@playwright/test'
import { ADMIN, ageClosedTicket, createTicket, login } from './helpers'

// #236：管理员确认已保存后移除归档包；旧工单地址显示墓碑。
test('管理员确认已保存后移除归档包并看到墓碑页', async ({ page, request }) => {
  const title = `e2e 归档移除 ${Date.now()}`
  const number = await createTicket(request, { title })
  const closed = await request.post(`/api/v1/tickets/${number}/close`, { data: { reason: 'done' } })
  expect(closed.status(), `关闭工单失败：${await closed.text()}`).toBe(200)
  ageClosedTicket(number, new Date(Date.now() - 120 * 24 * 3600 * 1000).toISOString())
  const createdRes = await request.post('/api/v1/admin/archive/packages', {
    data: { closed_months: 3, ticket_numbers: [number] },
  })
  expect(createdRes.status(), `生成归档包失败：${await createdRes.text()}`).toBe(201)
  const created = (await createdRes.json()) as { name: string }

  await login(page, ADMIN)
  await page.goto('/admin')
  const row = page.getByTestId(`archive-package-${created.name}`)
  await expect(row).toContainText(number)
  await page.getByTestId(`archive-saved-${created.name}`).check()
  await page.getByTestId(`archive-remove-${created.name}`).click()
  await expect(page.getByTestId('archive-released')).toContainText('已释放')
  await expect(row).toHaveCount(0)

  await page.goto(`/t/${number}`)
  const tombstone = page.getByTestId('ticket-tombstone')
  await expect(tombstone).toContainText('这张工单已归档')
  await expect(tombstone).toContainText(number)
  await expect(tombstone).toContainText(title)
  await expect(tombstone).toContainText('管理员')
  await expect(tombstone).toContainText(created.name)
})
