import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, createTicket, login } from './helpers'

async function createGuestLink(
  request: APIRequestContext,
  number: string,
  note: string,
): Promise<{ id: number; url: string }> {
  const res = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note, valid_days: 30 },
  })
  expect(res.status(), `创建访客链接失败：${await res.text()}`).toBe(201)
  return (await res.json()) as { id: number; url: string }
}

async function claimGuestLink(
  request: APIRequestContext,
  linkKey: string,
): Promise<{ deviceToken: string; accessCode: string }> {
  const res = await request.post('/api/v1/guest-links/claim', {
    data: { author_name: '检验科 张三' },
    headers: { Authorization: `Bearer ${linkKey}` },
  })
  expect(res.status(), `访客链接认领失败：${await res.text()}`).toBe(201)
  return (await res.json()) as { deviceToken: string; accessCode: string }
}

test('多条有效链接时先选链接，手机上也能完成重置访问码', async ({ page, request }) => {
  const number = await createTicket(request, { title: '血球仪 BH-7600CS 开机报错 E203' })
  const first = await createGuestLink(request, number, '检验科-张三')
  await createGuestLink(request, number, '设备科-李四')
  const oldCode = (await claimGuestLink(request, first.url.slice(3))).accessCode

  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.setViewportSize({ width: 390, height: 844 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await page.locator('details.mmeta summary').click()
  await page.locator('details.mmeta').getByRole('button', { name: '🔑 重置访问' }).click()

  const wizard = page.getByRole('dialog', { name: '🔑 重置访问' })
  await wizard.locator('.reset-link-option', { hasText: '检验科-张三' }).click()
  await wizard.getByRole('button', { name: '下一步：选操作' }).click()
  await wizard.getByRole('button', { name: '重置访问码' }).click()
  await wizard.getByRole('button', { name: '下一步：确认' }).click()
  await expect(wizard).toContainText('对方所有已登录设备（含对方的 Agent）会立即失效')
  await wizard.getByRole('button', { name: '确认重置访问码' }).click()

  const result = page.getByRole('dialog', { name: '重置访问码完成' })
  const code = result.locator('.reset-code')
  await expect(code).toHaveText(/^\d{6}$/)
  const newCode = await code.textContent()
  expect(newCode).not.toBe(oldCode)
  await expect(result).toContainText('新访问码只显示这一次')
  await result.getByRole('button', { name: '📋 一键复制' }).click()
  await expect(result.locator('.reset-copy-button')).toHaveText('✓ 已复制')
  await expect(page.locator('#toast')).toHaveText('已复制，可直接粘贴给对方')
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  expect(copied).toContain('检验科-张三：你的访问码已重置。')
  expect(copied).toContain(`工单「血球仪 BH-7600CS 开机报错 E203」的链接：${page.url().split('/t/')[0]}${first.url}`)
  expect(copied).toContain(`新访问码：${newCode}`)
  expect(copied).toContain('也请把新访问码告诉它。')
})

test('重置链接返回不同的新地址和新访问码', async ({ page, request }) => {
  const number = await createTicket(request, { title: '重置链接的工单' })
  const link = await createGuestLink(request, number, '检验科-张三')
  const oldCode = (await claimGuestLink(request, link.url.slice(3))).accessCode

  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.setViewportSize({ width: 1280, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await page.getByRole('region', { name: '工单操作' }).getByRole('button', { name: '🔑 重置访问' }).click()

  const wizard = page.getByRole('dialog', { name: '🔑 重置访问' })
  await expect(wizard.getByRole('button', { name: '重置访问码' })).toBeVisible()
  await wizard.getByRole('button', { name: '重置链接' }).click()
  await wizard.getByRole('button', { name: '下一步：确认' }).click()
  await expect(wizard).toContainText('旧链接地址立即失效，无法再打开')
  await expect(wizard).toContainText('旧设备全部失效（含对方的 Agent）')
  await wizard.getByRole('button', { name: '确认重置链接' }).click()

  const result = page.getByRole('dialog', { name: '重置链接完成' })
  await expect(result.locator('.reset-code')).toHaveText(/^\d{6}$/)
  const newCode = await result.locator('.reset-code').textContent()
  expect(newCode).not.toBe(oldCode)
  const newLink = await result.locator('[data-testid="reset-access-link"]').textContent()
  expect(newLink).toBeTruthy()
  expect(newLink).not.toContain(link.url.slice(3))
  await result.getByRole('button', { name: '📋 一键复制' }).click()
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  expect(copied).toContain('这张工单的链接已更换，旧链接已失效。')
  expect(copied).toContain(newLink ?? '')
  expect(copied).toContain(`新访问码：${newCode}`)
})

test('撤销链接前先确认，成功后刷新数据并隐藏入口', async ({ page, request }) => {
  const number = await createTicket(request, { title: '撤销链接的工单' })
  await createGuestLink(request, number, '检验科-张三')

  await page.setViewportSize({ width: 1280, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  const entry = page.getByRole('region', { name: '工单操作' }).getByRole('button', { name: '🔑 重置访问' })
  await entry.click()
  const wizard = page.getByRole('dialog', { name: '🔑 重置访问' })
  await wizard.getByRole('button', { name: '撤销链接' }).click()
  await wizard.getByRole('button', { name: '下一步：确认' }).click()
  await expect(wizard).toContainText('对方再打开会看到链接失效页')
  await expect(wizard).toContainText('撤销后不能恢复')
  await wizard.getByRole('button', { name: '确认撤销链接' }).click()
  await expect(page.locator('#toast')).toHaveText('已撤销，对方再打开会看到链接失效页')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(entry).toHaveCount(0)
})

test('未认领链接不能重置访问码，只能换地址或撤销', async ({ page, request }) => {
  const number = await createTicket(request, { title: '未认领链接的重置访问' })
  await createGuestLink(request, number, '设备科-李四')

  await page.setViewportSize({ width: 1280, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await page.getByRole('region', { name: '工单操作' }).getByRole('button', { name: '🔑 重置访问' }).click()
  const wizard = page.getByRole('dialog', { name: '🔑 重置访问' })
  await expect(wizard.getByRole('button', { name: '重置访问码' })).toBeDisabled()
  await expect(wizard).toContainText('这条链接还没有被访客认领，尚没有访问码')
  await expect(wizard.getByRole('button', { name: '重置链接' })).toContainText(
    '对方首次打开并认领后才会获得访问码',
  )
})

test('没有有效链接时不显示重置访问入口', async ({ page, request }) => {
  const number = await createTicket(request, { title: '没有有效链接的工单' })
  const link = await createGuestLink(request, number, '检验科-张三')
  const revoked = await request.delete(`/api/v1/tickets/${number}/guest-links/${link.id}`)
  expect(revoked.status(), `撤销访客链接失败：${await revoked.text()}`).toBe(204)

  await page.setViewportSize({ width: 390, height: 844 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await expect(page.getByRole('button', { name: '🔑 重置访问' })).toHaveCount(0)
})
