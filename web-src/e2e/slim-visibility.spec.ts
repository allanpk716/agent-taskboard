import { test, expect } from '@playwright/test'
import { ADMIN, ageClosedTicket, createTicket, login, loginAdmin } from './helpers'

// #284：瘦身标记处处可见——列表行内标记、详情横幅、附件占位（下载置灰），
// 访客页同样展示。
test('瘦身标记在列表详情与访客页可见', async ({ page, request }) => {
  await loginAdmin(request)
  const title = `e2e 瘦身标记 ${Date.now()}`
  const number = await createTicket(request, { title })
  const uploaded = await request.post(`/api/v1/tickets/${number}/attachments`, {
    multipart: {
      file: { name: '服务日志.log', mimeType: 'text/plain', buffer: Buffer.from('slim-mark') },
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

  await login(page, ADMIN)
  await page.goto('/admin')
  await page.getByTestId(`slim-ticket-${number}`).check()
  await page.getByRole('button', { name: '预览瘦身' }).click()
  await expect(page.getByTestId('slim-preview')).toContainText('将瘦身 1 张工单')
  await page.getByRole('button', { name: '生成瘦身包' }).click()
  let created: { name: string } | undefined
  for (let i = 0; i < 40 && !created; i++) {
    const listRes = await request.get('/api/v1/admin/slim/packages')
    const list = (await listRes.json()) as { packages: Array<{ name: string; ticket_numbers: string[] }> }
    created = list.packages.find((pkg) => pkg.ticket_numbers.includes(number))
    if (!created) await new Promise((resolve) => setTimeout(resolve, 250))
  }
  expect(created, '生成后瘦身包列表应包含新包').toBeTruthy()
  await page.getByTestId(`slim-saved-${created!.name}`).check()
  await page.getByTestId(`slim-remove-${created!.name}`).click()
  await expect(page.getByTestId('slim-remove-result')).toContainText('已移出 1 个附件')

  await page.goto(`/tickets?status=closed`)
  const card = page.locator('.tcard', { hasText: number })
  await expect(card).toContainText('已瘦身')

  await page.goto(`/t/${number}`)
  const banner = page.getByTestId('slim-banner')
  await expect(banner).toContainText('本工单的 1 个附件已于')
  await expect(banner).toContainText('瘦身移出')
  await expect(banner).toContainText(created!.name)
  await expect(banner).toContainText('需要时联系管理员')
  const placeholder = page.getByTestId(`slim-attachment-${attachment.id}`)
  await expect(placeholder).toContainText('服务日志.log')
  await expect(placeholder).toContainText(created!.name)
  await expect(placeholder).toContainText('移出')
  await expect(placeholder.getByRole('button', { name: '下载' })).toBeDisabled()

  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '现场对接', new_guest_name: '检验科-张三', valid_days: 30 },
  })
  expect(link.status(), `创建访客链接失败：${await link.text()}`).toBe(201)
  const linkBody = (await link.json()) as { url: string }
  await page.goto(linkBody.url)
  await page.locator('#guest-claim-name').fill('现场 张三')
  await page.getByRole('button', { name: '是我，打开工单' }).click()
  await page.getByRole('button', { name: '我已保存，打开工单' }).click()
  await expect(page.getByTestId('slim-banner')).toContainText('本工单的 1 个附件已于')
  await expect(page.getByTestId(`slim-attachment-${attachment.id}`)).toContainText('服务日志.log')

  // 共享 e2e 服务不能留下“已关闭满 3 个月”的工单；恢复关闭时间供其他用例筛选。
  ageClosedTicket(number, new Date().toISOString())
})
