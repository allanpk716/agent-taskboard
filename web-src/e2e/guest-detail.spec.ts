// 访客详情档案页（#338）：工单/访问/管理三个标签；工单标签里轮到访客的
// 置顶并注明原因，访问标签给访问码状态与已登录设备数，管理保留原有操作。
import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, login, loginAdmin } from './helpers'

interface Seed {
  guestID: number
  fillTicket: string
  repliedTicket: string
}

async function seedGuestArchive(request: APIRequestContext): Promise<Seed> {
  await loginAdmin(request)
  const first = await request.post('/api/v1/tickets', {
    data: { title: '等待访客补全说明', brief_md: '请补充现场情况。', labels: ['机型:UD-2300'] },
  })
  expect(first.status(), `建单失败：${await first.text()}`).toBe(201)
  const fillTicket = ((await first.json()) as { number: string }).number
  const link = await request.post(`/api/v1/tickets/${fillTicket}/guest-links`, {
    data: { note: '补全说明', new_guest_name: '检验科-张三', valid_days: 30, can_edit_brief: true },
  })
  expect(link.status(), `创建访客链接失败：${await link.text()}`).toBe(201)
  const linkBody = (await link.json()) as { guest_id: number; url: string }

  const second = await request.post('/api/v1/tickets', {
    data: {
      kind: 'report',
      title: '等待访客答复',
      brief_md: '打印日志待补充。',
      labels: ['机型:UD-2300'],
      guest_link: { note: '追问日志', guest_id: linkBody.guest_id, valid_days: 30 },
    },
  })
  expect(second.status(), `建反馈工单失败：${await second.text()}`).toBe(201)
  const repliedTicket = ((await second.json()) as { number: string }).number
  const reply = await request.post(`/api/v1/tickets/${repliedTicket}/replies`, {
    data: { body_md: '请把打印日志发一下。' },
  })
  expect(reply.status(), `成员回复失败：${await reply.text()}`).toBe(201)

  const claim = await request.post('/api/v1/guest-links/claim', {
    data: { author_name: '检验科 张三' },
    headers: { Authorization: `Bearer ${linkBody.url.slice(3)}` },
  })
  expect(claim.status(), `访客认领失败：${await claim.text()}`).toBe(201)
  return { guestID: linkBody.guest_id, fillTicket, repliedTicket }
}

test('访客详情档案页分三个标签，轮到访客的工单置顶', async ({ page, request }) => {
  const seed = await seedGuestArchive(request)
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)
  await page.goto(`/guests/${seed.guestID}`)

  // 档案头部 + 三个标签。
  await expect(page.locator('.guest-profile')).toContainText('检验科-张三')
  await expect(page.locator('.guest-profile')).toContainText('已认领')
  for (const name of ['工单', '访问', '管理']) {
    await expect(page.getByRole('tab', { name, exact: true })).toBeVisible()
  }

  // 工单标签：轮到访客的两张置顶（新的在前），并注明成员侧原因。
  const tickets = page.locator('.guest-ticket')
  await expect(tickets).toHaveCount(2)
  await expect(tickets.nth(0)).toContainText('等待访客答复')
  await expect(tickets.nth(0).locator('.guest-turn .badge')).toContainText('轮到访客')
  await expect(tickets.nth(0)).toContainText('已回复，等待访客答复')
  await expect(tickets.nth(1)).toContainText('等待访客补全说明')
  await expect(page.getByRole('tabpanel')).toContainText('只列你可见范围内')

  // 访问标签：访问码状态、已登录设备数、主页文案。
  await page.getByRole('tab', { name: '访问', exact: true }).click()
  const access = page.getByTestId('guest-panel-access')
  await expect(access).toBeVisible()
  await expect(access).toContainText('已认领')
  await expect(access).toContainText('已登录设备')
  await expect(access).toContainText('1 台')
  await expect(access).toContainText('这位访客名下所有访客链接')
  await access.getByRole('button', { name: '复制主页文案' }).click()
  await expect(page.locator('#toast')).toContainText('主页文案已复制')
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  expect(copied).toContain('检验科-张三')
  expect(copied).toMatch(/\/h\/tbh_/)
  await expect(access.getByRole('button', { name: '重置访问码' })).toBeVisible()
  await expect(access.getByRole('button', { name: '重置主页地址' })).toBeVisible()

  // 管理标签：改名、停用/恢复、合并入口都在。
  await page.getByRole('tab', { name: '管理', exact: true }).click()
  const manage = page.getByTestId('guest-panel-manage')
  await expect(manage).toBeVisible()
  await manage.getByRole('button', { name: '改名' }).click()
  await manage.getByLabel('访客新名字').fill('检验科-张三丰')
  await manage.getByRole('button', { name: '保存' }).click()
  await expect(page.locator('.guest-profile')).toContainText('检验科-张三丰')
  await manage.getByRole('button', { name: '停用', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: '停用', exact: true }).click()
  await expect(page.locator('.guest-profile')).toContainText('已停用')
  await manage.getByRole('button', { name: '恢复', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: '恢复', exact: true }).click()
  await expect(page.locator('.guest-profile')).not.toContainText('已停用')
  await manage.getByRole('button', { name: '合并到另一位访客' }).click()
  await expect(page.getByRole('dialog', { name: '合并到另一位访客' })).toBeVisible()
  await page
    .getByRole('dialog', { name: '合并到另一位访客' })
    .getByRole('button', { name: '取消' })
    .click()
})

test('访客详情访问标签里可以重置访问码', async ({ page, request }) => {
  const seed = await seedGuestArchive(request)
  await login(page, ADMIN)
  await page.goto(`/guests/${seed.guestID}`)
  await page.getByRole('tab', { name: '访问', exact: true }).click()
  await page.getByTestId('guest-panel-access').getByRole('button', { name: '重置访问码' }).click()

  const wizard = page.getByRole('dialog', { name: '🔐 重置访问码' })
  await expect(wizard).toContainText('作废 1 台设备，涉及 2 张工单')
  await wizard.getByRole('button', { name: '重置访问码' }).click()
  const result = page.getByRole('dialog', { name: '重置访问码完成' })
  const code = await result.locator('.reset-code').textContent()
  expect(code).toMatch(/^\d{6}$/)
})
