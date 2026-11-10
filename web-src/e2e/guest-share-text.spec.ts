import { expect, test } from '@playwright/test'
import { ADMIN, createTicket, login } from './helpers'

test('详情卡片和管理弹窗都能再次复制访客转发文案', async ({ page, request }) => {
  const number = await createTicket(request, { title: '再次复制转发文案的工单' })
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '检验科-张三', new_guest_name: '检验科-张三', valid_days: 30 },
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

test('老访客链接换新地址前先确认再复制', async ({ page, request }) => {
  const number = await createTicket(request, { title: '老链接换新地址的工单' })
  const linkRes = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '检验科-张三', new_guest_name: '检验科-张三', valid_days: 30 },
  })
  expect(linkRes.status(), `创建访客链接失败：${await linkRes.text()}`).toBe(201)
  const link = (await linkRes.json()) as { id: number; url: string }
  const newURL = '/g/tbg-e2e-rotated-link'
  const resetText = '检验科-张三：这张工单的链接已更换，旧链接已失效。请打开新地址，不要转发。'

  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.setViewportSize({ width: 1440, height: 800 })
  await page.route(`**/api/v1/tickets/${number}/guest-links`, async (route) => {
    const response = await route.fetch()
    const raw = JSON.parse(await response.text()) as {
      links: Array<{ id: number; share_text_available?: boolean }>
    }
    for (const item of raw.links) {
      if (item.id === link.id) item.share_text_available = false
    }
    await route.fulfill({ json: raw })
  })
  await page.route(`**/api/v1/tickets/${number}/guest-links/${link.id}/rotate-key`, async (route) => {
    await route.fulfill({
      json: {
        url: newURL,
        share_text: `检验科-张三：请打开新地址 ${newURL}`,
        reset_text: resetText,
        expires_at: '2026-11-15T09:00:00+08:00',
      },
    })
  })
  await page.route(`**/api/v1/tickets/${number}`, async (route) => {
    const response = await route.fetch()
    const body = JSON.parse(await response.text()) as {
      timeline?: Array<Record<string, unknown> & { seq: number; change: number }>
    }
    const last = body.timeline?.[body.timeline.length - 1]
    body.timeline?.push({
      ...(last ?? {}),
      seq: (last?.seq ?? 0) + 1,
      change: (last?.change ?? 0) + 1,
      type: 'guest_link_rotated',
      note: '检验科-张三',
    })
    await route.fulfill({ json: body })
  })
  await login(page, ADMIN)
  await page.goto(`/t/${number}?status=open`)

  const info = page.getByRole('region', { name: '工单信息', exact: true })
  const cardButton = info.getByRole('button', { name: '🔑 重置链接' })
  await expect(cardButton).toBeVisible()
  await expect(page.getByText('更换了访客链接「检验科-张三」的地址')).toBeVisible()
  await cardButton.click()
  const confirm = page.getByRole('dialog', { name: '🔑 重置访问' })
  await expect(confirm).toContainText('旧链接地址立即失效，无法再打开')
  await confirm.getByRole('button', { name: '确认重置链接' }).click()
  const result = page.getByRole('dialog', { name: '重置链接完成' })
  await result.getByRole('button', { name: '📋 一键复制' }).click()
  await expect(result.locator('.reset-copy-button')).toHaveText('✓ 已复制')
  await expect(page.locator('#toast')).toHaveText('已复制，可直接粘贴给对方')
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(resetText)
  await result.getByRole('button', { name: '关闭' }).click()

  await info
    .locator('article.link-card', { hasText: '检验科-张三' })
    .getByRole('button', { name: '管理' })
    .click()
  const dialogButton = page.locator('.modal').getByRole('button', { name: '🔑 重置链接' })
  await expect(dialogButton).toBeVisible()
  await dialogButton.click()
  const rotateConfirm = page.getByRole('dialog', { name: '🔑 重置访问' })
  await rotateConfirm.getByRole('button', { name: '确认重置链接' }).click()
  const rotatedResult = page.getByRole('dialog', { name: '重置链接完成' })
  await rotatedResult.getByRole('button', { name: '📋 一键复制' }).click()
  await expect(rotatedResult.locator('.reset-copy-button')).toHaveText('✓ 已复制')
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(resetText)
  await page.unrouteAll({ behavior: 'ignoreErrors' })
})
