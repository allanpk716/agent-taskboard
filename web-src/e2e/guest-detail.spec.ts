// 访客详情档案页（#338）：工单/访问/管理三个标签；工单标签里轮到访客的
// 置顶并注明原因，访问标签给访问码状态与已登录设备数，管理保留原有操作。
import { expect, test, type APIRequestContext } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { ADMIN, createTicket, login, loginAdmin } from './helpers'

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

async function createLegacyLink(
  request: APIRequestContext,
  ticket: string,
  note: string,
): Promise<number> {
  const res = await request.post(`/api/v1/tickets/${ticket}/guest-links`, {
    data: { note, new_guest_name: note, valid_days: 30 },
  })
  expect(res.status(), `创建老链接失败：${await res.text()}`).toBe(201)
  const body = (await res.json()) as { id: number; guest_id: number }
  const port = process.env.E2E_PORT || '8901'
  const info = JSON.parse(
    readFileSync(join(tmpdir(), `tb-e2e-server-${port}.json`), 'utf8'),
  ) as { data_dir: string }
  const db = new DatabaseSync(join(info.data_dir, 'taskboard.db'))
  try {
    db.prepare(`
      INSERT INTO guest_credential_holders (owner_type, owner_id, access_code_failures, created_at)
      VALUES ('guest_link', ?, 0, ?)
    `).run(body.id, new Date().toISOString())
    const holder = db.prepare(`
      SELECT id FROM guest_credential_holders WHERE owner_type = 'guest_link' AND owner_id = ?
    `).get(body.id) as { id: number }
    db.prepare(`
      UPDATE guest_links SET guest_id = NULL, credential_holder_id = ? WHERE id = ?
    `).run(holder.id, body.id)
    db.prepare(`DELETE FROM guest_credential_holders WHERE owner_type = 'guest' AND owner_id = ?`)
      .run(body.guest_id)
    db.prepare('DELETE FROM guests WHERE id = ?').run(body.guest_id)
    return body.id
  } finally {
    db.close()
  }
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

test('访客详情访问标签可按需显示并复制主页地址', async ({ page, request }) => {
  const seed = await seedGuestArchive(request)
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)
  await page.goto(`/guests/${seed.guestID}`)
  await page.getByRole('tab', { name: '访问', exact: true }).click()

  const access = page.getByTestId('guest-panel-access')
  await expect(access).not.toContainText('/h/')
  await access.getByRole('button', { name: '显示主页地址' }).click()
  const address = access.locator('[data-testid="guest-home-url"]')
  await expect(address).toContainText(/\/h\//)
  await access.getByRole('button', { name: '复制主页地址' }).click()
  await expect(page.locator('#toast')).toContainText('主页地址已复制')
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toMatch(/\/h\//)
})

test('访客详情提示老工单并可批量归属给已认领访客', async ({ page, request }) => {
  await loginAdmin(request)
  const targetTicket = await createTicket(request, { title: '张三的新工单' })
  const targetLink = await request.post(`/api/v1/tickets/${targetTicket}/guest-links`, {
    data: { note: '新联系', new_guest_name: '检验科-张三', valid_days: 30 },
  })
  expect(targetLink.status(), `创建目标访客失败：${await targetLink.text()}`).toBe(201)
  const target = (await targetLink.json()) as { guest_id: number; url: string }
  const claim = await request.post('/api/v1/guest-links/claim', {
    data: { author_name: '张三' },
    headers: { Authorization: `Bearer ${target.url.slice(3)}` },
  })
  expect(claim.status(), `目标访官认领失败：${await claim.text()}`).toBe(201)

  const firstTicket = await createTicket(request, { title: '张三的第一张老工单' })
  const secondTicket = await createTicket(request, { title: '现场设备的第二张老工单' })
  await createLegacyLink(request, firstTicket, '检验科-张三')
  await createLegacyLink(request, secondTicket, '设备科-张三')

  await login(page, ADMIN)
  await page.goto(`/guests/${target.guest_id}`)
  const notice = page.getByTestId('guest-possible-links')
  await expect(notice).toContainText('还有 2 条老工单可能是 TA 的 → 归进来')
  await notice.getByRole('button', { name: '归进来' }).click()
  await page.waitForURL((u) => u.pathname === `/guests/${target.guest_id}/assign`)

  const assignPage = page.getByTestId('guest-assign-page')
  await expect(assignPage).toContainText('把老工单归到访客')
  await expect(assignPage).toContainText('检验科-张三')
  await expect(assignPage).toContainText('保留 张三 的访问码')
  await expect(assignPage).toContainText('所选老链接原来的访问码和设备都会作废')
  const confirm = assignPage.getByRole('button', { name: '确认归到访客' })
  await expect(confirm).toBeDisabled()
  await assignPage.getByLabel(/张三的第一张老工单/).check()
  await assignPage.getByLabel(/现场设备的第二张老工单/).check()
  await expect(assignPage.getByRole('button', { name: '确认归到访客（2 条）' })).toBeEnabled()
  await assignPage.getByRole('button', { name: '确认归到访客（2 条）' }).click()

  await page.waitForURL((u) => u.pathname === `/guests/${target.guest_id}`)
  const tickets = page.getByTestId('guest-panel-tickets')
  await expect(tickets).toContainText('张三的第一张老工单')
  await expect(tickets).toContainText('现场设备的第二张老工单')
  await expect(page.locator('#toast')).toContainText('已归入 2 张工单')
})
