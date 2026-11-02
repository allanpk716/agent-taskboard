import { test, expect } from '@playwright/test'
import {
  ADMIN,
  ageClosedTicket,
  createTicket,
  login,
  loginAdmin,
} from './helpers'

// #286：瘦过的工单再归档时，管理页提示需要把引用的瘦身包一并保存。
test('归档已瘦身工单时管理页提示一并保存瘦身包', async ({ page, request }) => {
  await loginAdmin(request)
  const title = `e2e 归档瘦身衔接 ${Date.now()}`
  const number = await createTicket(request, { title })
  const uploaded = await request.post(`/api/v1/tickets/${number}/attachments`, {
    multipart: {
      file: { name: '服务日志.log', mimeType: 'text/plain', buffer: Buffer.from('slim-archive-e2e') },
    },
  })
  expect(uploaded.status(), `上传附件失败：${await uploaded.text()}`).toBe(201)
  const attachment = (await uploaded.json()) as { id: string }
  const revised = await request.patch(`/api/v1/tickets/${number}`, {
    data: {
      brief_md: `请查收 [服务日志](att:${attachment.id})。`,
      attachment_ids: [attachment.id],
      base_revision: 1,
    },
  })
  expect(revised.status(), `挂说明附件失败：${await revised.text()}`).toBe(200)
  const closed = await request.post(`/api/v1/tickets/${number}/close`, { data: { reason: 'done' } })
  expect(closed.status(), `关闭工单失败：${await closed.text()}`).toBe(200)
  ageClosedTicket(number, new Date(Date.now() - 120 * 24 * 3600 * 1000).toISOString())

  const slimCreated = await request.post('/api/v1/admin/slim/packages', {
    data: { closed_months: 3, ticket_numbers: [number] },
  })
  expect(slimCreated.status(), `生成瘦身包失败：${await slimCreated.text()}`).toBe(201)
  const slim = (await slimCreated.json()) as { name: string }
  const slimRemoved = await request.post(`/api/v1/admin/slim/packages/${slim.name}/remove`, {
    data: { saved: true },
  })
  expect(slimRemoved.status(), `确认瘦身移出失败：${await slimRemoved.text()}`).toBe(200)

  await login(page, ADMIN)
  await page.goto('/admin')
  await page.getByTestId(`archive-ticket-${number}`).check()
  await page.getByRole('button', { name: '预览归档' }).click()
  const preview = page.getByTestId('archive-preview')
  await expect(preview).toContainText('这些工单引用了瘦身包')
  await expect(preview).toContainText(slim.name)

  await page.getByRole('button', { name: '生成归档包' }).click()
  const hint = page.locator('.warn', { hasText: `需一并保存瘦身包：${slim.name}` })
  await expect(hint).toBeVisible()

  // 共享 e2e 服务不能留下“已关闭满 3 个月”的工单；恢复关闭时间供其他用例筛选。
  ageClosedTicket(number, new Date().toISOString())
})
