import { test, expect } from '@playwright/test'
import { ADMIN, createTicket, login, loginAdmin } from './helpers'

test('详情页访问记录面板展示查看与附件下载', async ({ page, request }) => {
  const number = await createTicket(request, { title: '访问记录面板的工单' })
  await loginAdmin(request)

  const uploaded = await request.post(`/api/v1/tickets/${number}/attachments`, {
    multipart: {
      file: { name: '现场日志.log', mimeType: 'text/plain', buffer: Buffer.from('E-203\n') },
    },
  })
  expect(uploaded.status(), await uploaded.text()).toBe(201)
  const att = await uploaded.json()
  const claimed = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: '见附件。', attachment_ids: [att.id] },
  })
  expect(claimed.ok()).toBeTruthy()
  const ticketRes = await request.get(`/api/v1/tickets/${number}`)
  expect(ticketRes.ok()).toBeTruthy()
  const ticket = await ticketRes.json()
  const downloaded = await request.get(ticket.attachments[0].url)
  expect(downloaded.status()).toBe(200)

  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await page.getByText('标签 · 指派 · 访客链接').click()
  const panel = page.getByRole('region', { name: '访问记录' })
  await expect(panel).toBeVisible()
  await expect(panel).toContainText('管理员')
  await expect(panel).toContainText(/查看详情 · \d+ 次/)
  await expect(panel).toContainText('下载附件 现场日志.log')
})
