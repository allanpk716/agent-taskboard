import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, createTicket, login } from './helpers'

const GUEST_NAME = '检验科-张三'

interface GuestLinkResponse {
  id: number
  url: string
  guest_id: number
  guest_name: string
}

async function createGuestLink(
  request: APIRequestContext,
  number: string,
  note: string,
  guest: number | string = GUEST_NAME,
): Promise<GuestLinkResponse> {
  const res = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data:
      typeof guest === 'number'
        ? { note, guest_id: guest, valid_days: 30 }
        : { note, new_guest_name: guest, valid_days: 30 },
  })
  expect(res.status(), `创建访客链接失败：${await res.text()}`).toBe(201)
  return (await res.json()) as GuestLinkResponse
}

async function claimGuestLink(
  request: APIRequestContext,
  linkKey: string,
): Promise<{ device_token: string; access_code: string }> {
  const res = await request.post('/api/v1/guest-links/claim', {
    data: { author_name: '检验科 张三' },
    headers: { Authorization: `Bearer ${linkKey}` },
  })
  expect(res.status(), `访客链接认领失败：${await res.text()}`).toBe(201)
  return (await res.json()) as { device_token: string; access_code: string }
}

async function guestHomeURL(
  request: APIRequestContext,
  guestID: number,
): Promise<string> {
  const res = await request.post(`/api/v1/guests/${guestID}/home-share-text`)
  expect(res.status(), `复制访客主页文案失败：${await res.text()}`).toBe(200)
  return ((await res.json()) as { home_url: string }).home_url
}

test('访客级重置访问码展示影响预览并复制重置文案', async ({ page, request }) => {
  const number = await createTicket(request, { title: '血球仪 BH-7600CS 开机报错 E203' })
  const link = await createGuestLink(request, number, '补全说明')
  const other = await createTicket(request, { title: '同一访客的第二张工单' })
  await createGuestLink(request, other, '追问日志', link.guest_id)

  const claim = await claimGuestLink(request, link.url.slice(3))
  const home = await guestHomeURL(request, link.guest_id)
  const enter = await request.post('/api/v1/guest-home/enter', {
    data: { access_code: claim.access_code },
    headers: { Authorization: `Bearer ${home.slice(3)}` },
  })
  expect(enter.status(), '凭访问码进入主页失败').toBe(201)

  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.setViewportSize({ width: 1280, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await page.getByRole('region', { name: '工单操作' }).getByRole('button', { name: '🔑 重置访问' }).click()

  const wizard = page.getByRole('dialog', { name: '🔑 重置访问' })
  await wizard.getByRole('button', { name: '重置访问码' }).click()
  await wizard.getByRole('button', { name: '下一步：确认' }).click()
  await expect(wizard.getByTestId('reset-access-preview')).toContainText('将作废 2 台设备')
  await expect(wizard.getByTestId('reset-access-preview')).toContainText('涉及 2 张工单')
  await expect(wizard).toContainText('连续输错造成的锁定会解除')
  await wizard.getByRole('button', { name: '确认重置访问码' }).click()

  const result = page.getByRole('dialog', { name: '重置访问码完成' })
  const code = result.locator('.reset-code')
  await expect(code).toHaveText(/^\d{6}$/)
  const newCode = await code.textContent()
  expect(newCode).not.toBe(claim.access_code)
  await expect(result).toContainText('新访问码只显示这一次')
  await expect(result).toContainText('访客主页')
  await result.getByRole('button', { name: '📋 一键复制' }).click()
  await expect(result.locator('.reset-copy-button')).toHaveText('✓ 已复制')
  await expect(page.locator('#toast')).toHaveText('已复制，可直接粘贴给对方')
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  const base = page.url().split('/t/')[0]
  expect(copied).toContain(`${GUEST_NAME}：你的访问码已重置。`)
  expect(copied).toContain(`你的工单主页：${base}${home}`)
  expect(copied).toContain(`新访问码：${newCode}`)
  expect(copied).toContain('访问码请勿转发；如果你让 Agent 处理过你的工单，也请把新访问码告诉它。')

  const oldDevice = await request.get(`/api/v1/tickets/${number}`, {
    headers: { Authorization: `Bearer ${claim.device_token}` },
  })
  expect(oldDevice.status(), '重置后旧设备应失效').toBe(401)
  const renewed = await request.post('/api/v1/guest-home/enter', {
    data: { access_code: newCode },
    headers: { Authorization: `Bearer ${home.slice(3)}` },
  })
  expect(renewed.status(), '新访问码应能进入主页').toBe(201)
})

test('重置链接只换地址，访问码与设备保持不变', async ({ page, request }) => {
  const number = await createTicket(request, { title: '重置链接的工单' })
  const link = await createGuestLink(request, number, '补全说明')
  const claim = await claimGuestLink(request, link.url.slice(3))

  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.setViewportSize({ width: 1280, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await page.getByRole('region', { name: '工单操作' }).getByRole('button', { name: '🔑 重置访问' }).click()

  const wizard = page.getByRole('dialog', { name: '🔑 重置访问' })
  await wizard.getByRole('button', { name: '重置链接' }).click()
  await wizard.getByRole('button', { name: '下一步：确认' }).click()
  await expect(wizard).toContainText('旧链接地址立即失效，无法再打开')
  await expect(wizard).toContainText('访问码、已认领设备和备注都保持不变')
  await wizard.getByRole('button', { name: '确认重置链接' }).click()

  const result = page.getByRole('dialog', { name: '重置链接完成' })
  await expect(result.locator('.reset-code')).toHaveCount(0)
  const newLink = await result.locator('[data-testid="reset-access-link"]').textContent()
  expect(newLink).toBeTruthy()
  expect(newLink).not.toContain(link.url.slice(3))
  await result.getByRole('button', { name: '📋 一键复制' }).click()
  await expect(page.locator('#toast')).toHaveText('已复制，可直接粘贴给对方')

  const oldURL = await request.get('/api/v1/guest-link', {
    headers: { Authorization: `Bearer ${link.url.slice(3)}` },
  })
  expect(oldURL.status(), '旧链接地址应失效').toBe(401)
  const device = await request.get(`/api/v1/tickets/${number}`, {
    headers: { Authorization: `Bearer ${claim.device_token}` },
  })
  expect(device.status(), '重置链接后原设备应仍可信').toBe(200)
  const renewed = await request.post('/api/v1/guest-links/enter', {
    data: { access_code: claim.access_code },
    headers: { Authorization: `Bearer ${new URL(newLink!).pathname.slice(3)}` },
  })
  expect(renewed.status(), '原访问码应仍然可用').toBe(201)
})

test('重置主页地址只作废旧主页，链接与设备不受影响', async ({ page, request }) => {
  const number = await createTicket(request, { title: '主页地址单独重置' })
  const link = await createGuestLink(request, number, '补全说明')
  const claim = await claimGuestLink(request, link.url.slice(3))
  const oldHome = await guestHomeURL(request, link.guest_id)

  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await page.setViewportSize({ width: 1280, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await page.getByRole('region', { name: '工单操作' }).getByRole('button', { name: '🔑 重置访问' }).click()

  const wizard = page.getByRole('dialog', { name: '🔑 重置访问' })
  await wizard.getByRole('button', { name: '重置主页地址' }).click()
  await wizard.getByRole('button', { name: '下一步：确认' }).click()
  await expect(wizard).toContainText('旧主页地址立即失效，无法再打开')
  await expect(wizard).toContainText('访问码、已登录设备和名下链接都不受影响')
  await wizard.getByRole('button', { name: '确认重置主页地址' }).click()

  const result = page.getByRole('dialog', { name: '重置主页地址完成' })
  await expect(result.locator('.reset-code')).toHaveCount(0)
  const newHome = await result.locator('[data-testid="reset-access-link"]').textContent()
  expect(new URL(newHome!).pathname).toMatch(/^\/h\/tbh_/)
  expect(new URL(newHome!).pathname).not.toBe(oldHome)
  await result.getByRole('button', { name: '📋 一键复制' }).click()
  await expect(page.locator('#toast')).toHaveText('已复制，可直接粘贴给对方')

  const oldKey = await request.get('/api/v1/guest-home', {
    headers: { Authorization: `Bearer ${oldHome.slice(3)}` },
  })
  expect(oldKey.status(), '旧主页地址应失效').toBe(401)
  const newKey = await request.get('/api/v1/guest-home', {
    headers: { Authorization: `Bearer ${new URL(newHome!).pathname.slice(3)}` },
  })
  expect(newKey.status(), '新主页地址应能打开').toBe(200)
  const device = await request.get(`/api/v1/tickets/${number}`, {
    headers: { Authorization: `Bearer ${claim.device_token}` },
  })
  expect(device.status(), '重置主页后原设备应仍可信').toBe(200)
  const renewed = await request.post('/api/v1/guest-home/enter', {
    data: { access_code: claim.access_code },
    headers: { Authorization: `Bearer ${new URL(newHome!).pathname.slice(3)}` },
  })
  expect(renewed.status(), '原访问码应仍然可用').toBe(201)
})

test('多条有效链接时先选链接，手机上也能完成重置访问码', async ({ page, request }) => {
  const number = await createTicket(request, { title: '多链接的重置访问' })
  const first = await createGuestLink(request, number, '检验科-张三')
  await createGuestLink(request, number, '设备科-李四', '设备科-李四')
  const oldCode = (await claimGuestLink(request, first.url.slice(3))).access_code

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
  await expect(wizard.getByTestId('reset-access-preview')).toContainText('将作废 1 台设备')
  await expect(wizard.getByTestId('reset-access-preview')).toContainText('涉及 1 张工单')
  await wizard.getByRole('button', { name: '确认重置访问码' }).click()

  const result = page.getByRole('dialog', { name: '重置访问码完成' })
  const newCode = await result.locator('.reset-code').textContent()
  expect(newCode).toMatch(/^\d{6}$/)
  expect(newCode).not.toBe(oldCode)
  await result.getByRole('button', { name: '📋 一键复制' }).click()
  await expect(page.locator('#toast')).toHaveText('已复制，可直接粘贴给对方')
})

test('撤销链接立即生效', async ({ page, request }) => {
  const number = await createTicket(request, { title: '撤销链接的工单' })
  const link = await createGuestLink(request, number, '补全说明')
  const claim = await claimGuestLink(request, link.url.slice(3))
  expect(claim.access_code).toMatch(/^\d{6}$/)

  await page.setViewportSize({ width: 1280, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await page.getByRole('region', { name: '工单操作' }).getByRole('button', { name: '🔑 重置访问' }).click()
  const wizard = page.getByRole('dialog', { name: '🔑 重置访问' })
  await wizard.getByRole('button', { name: '撤销链接' }).click()
  await wizard.getByRole('button', { name: '下一步：确认' }).click()
  await expect(wizard).toContainText('对方再打开会看到链接失效页')
  await expect(wizard).toContainText('撤销后不能恢复')
  await wizard.getByRole('button', { name: '确认撤销链接' }).click()
  await expect(page.locator('#toast')).toHaveText('已撤销，对方再打开会看到链接失效页')
  await expect(page.getByRole('dialog')).toHaveCount(0)
})

test('未认领链接不能重置访问码，可以重置链接或主页地址', async ({ page, request }) => {
  const number = await createTicket(request, { title: '未认领链接的重置访问' })
  await createGuestLink(request, number, '设备科-李四')

  await page.setViewportSize({ width: 1280, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await page.getByRole('region', { name: '工单操作' }).getByRole('button', { name: '🔑 重置访问' }).click()
  const wizard = page.getByRole('dialog', { name: '🔑 重置访问' })
  await expect(wizard.getByRole('button', { name: '重置访问码' })).toBeDisabled()
  await expect(wizard).toContainText('这条链接还没有被访客认领，尚没有访问码')
  await expect(wizard.getByRole('button', { name: '重置链接' })).toContainText('只换新地址')
  await expect(wizard.getByRole('button', { name: '重置主页地址' })).toBeEnabled()
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
