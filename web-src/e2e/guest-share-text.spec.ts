import { expect, test } from '@playwright/test'
import { ADMIN, createTicket, login } from './helpers'

test('详情卡片和管理弹窗都能再次复制访客转发文案', async ({ page, request }) => {
  const number = await createTicket(request, { title: '再次复制转发文案的工单' })
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '检验科-张三', valid_days: 30 },
  })
  expect(link.status(), `创建访客链接失败：${await link.text()}`).toBe(201)
  const linkURL = ((await link.json()) as { url: string }).url

  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.setViewportSize({ width: 1440, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}?status=open`)

  const info = page.getByRole('region', { name: '工单信息', exact: true })
  const cardButton = info.getByRole('button', { name: '复制转发文案' })
  await expect(cardButton).toBeVisible()
  await cardButton.click()
  await expect(page.locator('#toast')).toHaveText('✓ 转发文案已复制')
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(linkURL)

  await info
    .locator('article.link-card', { hasText: '检验科-张三' })
    .getByRole('button', { name: '管理' })
    .click()
  const dialogButton = page.locator('.modal').getByRole('button', { name: '复制转发文案' })
  await expect(dialogButton).toBeVisible()
  await dialogButton.click()
  await expect(page.locator('#toast')).toHaveText('✓ 转发文案已复制')
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(linkURL)
})
