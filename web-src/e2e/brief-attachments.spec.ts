import { expect, test, type APIRequestContext } from '@playwright/test'
import { createTicket, login, ADMIN } from './helpers'

async function upload(request: APIRequestContext, number: string, name: string, body: string) {
  const res = await request.post(`/api/v1/tickets/${number}/attachments`, {
    multipart: { file: { name, mimeType: 'text/plain', buffer: Buffer.from(body) } },
  })
  expect(res.status(), `上传 ${name} 失败：${await res.text()}`).toBe(201)
  return (await res.json()) as { id: string }
}

test('说明卡片底部附件只列整张工单的附件', async ({ page, request }) => {
  const number = await createTicket(request, { title: '说明附件卡片的工单' })
  const field = await upload(request, number, '字段资料.txt', 'field')
  let res = await request.patch(`/api/v1/tickets/${number}`, {
    data: { brief_md: `补充资料见 [字段资料](att:${field.id})。`, base_revision: 1 },
  })
  expect(res.status(), `挂字段附件失败：${await res.text()}`).toBe(200)

  const whole = await upload(request, number, '服务日志.log', 'log')
  res = await request.patch(`/api/v1/tickets/${number}`, {
    data: { attachment_ids: [whole.id], base_revision: 2 },
  })
  expect(res.status(), `挂整单附件失败：${await res.text()}`).toBe(200)

  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  const brief = page.locator('[data-bubble="brief"]')
  await expect(brief.getByRole('link', { name: '字段资料' })).toBeVisible()
  await expect(brief.getByText('服务日志.log')).toBeVisible()
  await expect(brief.locator('.atts .att')).toHaveCount(1)
  await expect(brief.locator('.atts .att')).toContainText('服务日志.log')

  const emptyNumber = await createTicket(request, { title: '没有整单附件的工单' })
  await page.goto(`/t/${emptyNumber}`)
  await expect(page.locator('[data-bubble="brief"] .atts')).toHaveCount(0)
})
