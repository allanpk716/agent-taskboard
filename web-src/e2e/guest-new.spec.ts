// 单独新建访客（#339/#343）：从访客名单浮动「＋ → 新建访客」进入单页，
// 填名字时实时提示重名/相似访客（“就是 TA”直达详情）；名字之下渐进展开
// “可能是 TA 的老工单”勾选区（默认不勾、可展开搜索），勾了已认领链接时
// 选择访问码；建立后的结果页能复制主页文案、展示一次性新码并进入详情。
import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { ADMIN, createTicket, login, loginAdmin } from './helpers'

const RUN = String(Date.now() % 1_000_000)

async function seedExistingGuest(
  request: APIRequestContext,
  name: string,
): Promise<number> {
  await loginAdmin(request)
  const ticket = await request.post('/api/v1/tickets', {
    data: { title: `${name}的血常规复查`, brief_md: '请补充结果。', labels: ['机型:UD-2300'] },
  })
  expect(ticket.status(), `建单失败：${await ticket.text()}`).toBe(201)
  const number = ((await ticket.json()) as { number: string }).number
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '补全结果', new_guest_name: name, valid_days: 30 },
  })
  expect(link.status(), `创建访客链接失败：${await link.text()}`).toBe(201)
  return ((await link.json()) as { guest_id: number }).guest_id
}

let seedSeq = 0

/** 造一条无主老链接：直接把链接从建访客时的归属里摘出来（同 guest-assign-access）。 */
async function createLegacyLink(
  request: APIRequestContext,
  ticket: string,
  note: string,
): Promise<{ id: number; url: string }> {
  const res = await request.post(`/api/v1/tickets/${ticket}/guest-links`, {
    data: { note, new_guest_name: note, valid_days: 30 },
  })
  expect(res.status(), `创建老链接失败：${await res.text()}`).toBe(201)
  const body = (await res.json()) as { id: number; guest_id: number; url: string }
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
    return { id: body.id, url: body.url }
  } finally {
    db.close()
  }
}

async function claimLegacy(
  request: APIRequestContext,
  link: { url: string },
  name: string,
): Promise<{ device_token: string; access_code: string }> {
  const res = await request.post('/api/v1/guest-links/claim', {
    data: { author_name: name },
    headers: { Authorization: `Bearer ${link.url.slice('/g/'.length)}` },
  })
  expect(res.status(), `认领老链接失败：${await res.text()}`).toBe(201)
  return (await res.json()) as { device_token: string; access_code: string }
}

async function openGuestHomeWithCode(page: Page, homeUrl: string, code: string) {
  await page.goto(homeUrl)
  await page.locator('#guest-home-code').fill(code)
  await page.getByRole('button', { name: '进入主页' }).click()
}

test('从名单浮动加号新建访客，结果页复制主页文案并进入详情', async ({ page, request }) => {
  await loginAdmin(request)
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)
  await page.goto('/guests')

  // 名单浮动「＋ → 新建访客」进入。
  await page.getByRole('button', { name: '＋' }).click()
  await page.getByRole('link', { name: /新建访客/ }).click()
  await expect(page).toHaveURL(/\/guests\/new$/)

  const name = `检验科-李四${RUN}`
  await page.getByLabel('访客名字').fill(name)
  await page.getByRole('button', { name: '建立访客' }).click()

  // 结果页：未认领、名下没有链接；复制主页文案与 GuestDetailPage 同一套。
  const result = page.getByRole('region', { name: '新建访客结果' })
  await expect(result).toContainText(name)
  await expect(result).toContainText('未认领')
  await expect(result).toContainText('0 条访客链接')
  await result.getByRole('button', { name: '复制主页文案' }).click()
  await expect(page.locator('#toast')).toContainText('主页文案已复制')
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  expect(copied).toContain(name)
  expect(copied).toMatch(/\/h\/tbh_/)

  await result.getByRole('link', { name: '去访客详情' }).click()
  await expect(page).toHaveURL(new RegExp(`/guests/\\d+$`))
  await expect(page.locator('.guest-profile')).toContainText(name)
})

test('输入相似名字提示已有访客，就是TA直达详情', async ({ page, request }) => {
  const name = `检验科-赵六${RUN}`
  const guestId = await seedExistingGuest(request, name)
  await login(page, ADMIN)
  await page.goto('/guests/new')

  // 换个部门前缀：去掉“部门-”后与已有访客相同，应提示。
  await page.getByLabel('访客名字').fill(`设备科-赵六${RUN}`)
  const hint = page.getByRole('region', { name: '相似访客提示' })
  await expect(hint).toContainText(name)
  await hint.getByRole('button', { name: '就是 TA' }).click()
  await expect(page).toHaveURL(new RegExp(`/guests/${guestId}$`))
  await expect(page.locator('.guest-profile')).toContainText(name)
})

test('新建访客勾选老工单并沿用访问码归入', async ({ page, request }) => {
  await loginAdmin(request)
  const run = `${RUN}${++seedSeq}`
  const name = `检验科-归入${run}`
  const sourceTicket = await createTicket(request, { title: `归入的已认领老工单${run}` })
  const source = await createLegacyLink(request, sourceTicket, name)
  const oldClaim = await claimLegacy(request, source, '张三')
  const viewed = await request.get(`/api/v1/tickets/${sourceTicket}`, {
    headers: { Authorization: `Bearer ${oldClaim.device_token}` },
  })
  expect(viewed.status(), '让来源链接记录最近使用失败').toBe(200)
  const plainTicket = await createTicket(request, { title: `归入的未认领老工单${run}` })
  await createLegacyLink(request, plainTicket, name)
  const otherTicket = await createTicket(request, { title: `无关的老工单${run}` })
  // 备注别带 run 数字：相近判定按“包含”，数字相同会被误标为可能同一个人。
  await createLegacyLink(request, otherTicket, '别的部门-老王')

  await login(page, ADMIN)
  await page.goto('/guests/new')
  await page.getByLabel('访客名字').fill(name)

  // 推荐默认不勾选；其余老工单默认收起，展开后可搜索。
  const picker = page.getByRole('region', { name: '可能是 TA 的老工单' })
  const sourceBox = picker.getByLabel(new RegExp(`归入的已认领老工单${run}`))
  const plainBox = picker.getByLabel(new RegExp(`归入的未认领老工单${run}`))
  await expect(sourceBox).not.toBeChecked()
  await expect(plainBox).not.toBeChecked()
  await expect(picker.getByLabel(new RegExp(`无关的老工单${run}`))).toHaveCount(0)
  await picker.getByRole('button', { name: '其他老链接' }).click()
  const otherBox = picker.getByLabel(new RegExp(`无关的老工单${run}`))
  await expect(otherBox).toBeVisible()
  await picker.getByLabel('搜索老链接').fill(`无关的老工单${run}`)
  await expect(sourceBox).toHaveCount(0)
  await expect(otherBox).toBeVisible()
  await picker.getByLabel('搜索老链接').fill('')

  await sourceBox.check()
  await plainBox.check()
  const card = page.getByTestId('guest-access-code-card')
  await expect(card).toBeVisible()
  await expect(card.locator(`[data-source-id="${source.id}"] input`)).toBeChecked()
  await expect(page.getByTestId('guest-assign-consequence')).toContainText(
    '的访问码和已登录设备会转给',
  )

  const summary = page.getByTestId('guest-new-summary')
  await expect(summary).toContainText(`新建访客『${name}』`)
  await expect(summary).toContainText('归入 2 张工单')
  await expect(summary).toContainText('沿用')
  await summary.getByRole('button', { name: '建立访客' }).click()

  const result = page.getByRole('region', { name: '新建访客结果' })
  await expect(result).toContainText(`已建立访客「${name}」`)
  await expect(result).toContainText('已归入 2 张工单')
  const detailHref = await result.getByRole('link', { name: '去访客详情' }).getAttribute('href')
  const guestId = Number(detailHref!.split('/').pop())

  const homeRes = await request.post(`/api/v1/guests/${guestId}/home-share-text`)
  expect(homeRes.status(), '读取访客主页失败').toBe(200)
  const home = (await homeRes.json()) as { home_url: string }
  const guestPage = await page.context().newPage()
  await openGuestHomeWithCode(guestPage, home.home_url, oldClaim.access_code)
  await expect(guestPage.getByRole('heading', { name })).toBeVisible()
  await expect(guestPage.locator('.guest-home-ticket')).toHaveCount(2)
})

test('新建访客生成只显示一次的新访问码', async ({ page, request }) => {
  await loginAdmin(request)
  const run = `${RUN}${++seedSeq}`
  const name = `检验科-新码${run}`
  const claimedTicket = await createTicket(request, { title: `新码来源老工单${run}` })
  const claimedLink = await createLegacyLink(request, claimedTicket, name)
  await claimLegacy(request, claimedLink, '张三')

  await login(page, ADMIN)
  await page.goto('/guests/new')
  await page.getByLabel('访客名字').fill(name)
  const picker = page.getByRole('region', { name: '可能是 TA 的老工单' })
  await picker.getByLabel(new RegExp(`新码来源老工单${run}`)).check()
  await page.getByTestId('guest-access-code-card').getByRole('radio', { name: '生成新的访问码' }).check()
  await expect(page.getByTestId('guest-assign-consequence')).toContainText(
    '所选链接原来的访问码和设备都会作废',
  )
  const summary = page.getByTestId('guest-new-summary')
  await expect(summary).toContainText('归入 1 张工单')
  await expect(summary).toContainText('生成新的访问码')
  await summary.getByRole('button', { name: '建立访客' }).click()

  const result = page.getByRole('region', { name: '新建访客结果' })
  const code = await result.getByTestId('guest-new-access-code').textContent()
  expect(code).toMatch(/^\d{6}$/)
  await expect(result).toContainText('只显示这一次')
  const detailHref = await result.getByRole('link', { name: '去访客详情' }).getAttribute('href')
  const guestId = Number(detailHref!.split('/').pop())

  const homeRes = await request.post(`/api/v1/guests/${guestId}/home-share-text`)
  expect(homeRes.status(), '读取访客主页失败').toBe(200)
  const home = (await homeRes.json()) as { home_url: string }
  const guestPage = await page.context().newPage()
  await openGuestHomeWithCode(guestPage, home.home_url, code!)
  await expect(guestPage.getByRole('heading', { name })).toBeVisible()
  await expect(guestPage.locator('.guest-home-ticket')).toHaveCount(1)
})

test('归属失败时保留新访客并停在详情页提示失败', async ({ page, request }) => {
  await loginAdmin(request)
  const run = `${RUN}${++seedSeq}`
  const name = `检验科-归属失败${run}`
  const ticket = await createTicket(request, { title: `归属失败老工单${run}` })
  const link = await createLegacyLink(request, ticket, name)
  const grabber = await request.post('/api/v1/guests', { data: { name: `检验科-抢占${run}` } })
  expect(grabber.status(), '创建抢占访客失败').toBe(201)
  const grabberId = ((await grabber.json()) as { guest: { id: number } }).guest.id

  await login(page, ADMIN)
  await page.goto('/guests/new')
  await page.getByLabel('访客名字').fill(name)
  const picker = page.getByRole('region', { name: '可能是 TA 的老工单' })
  await picker.getByLabel(new RegExp(`归属失败老工单${run}`)).check()
  const summary = page.getByTestId('guest-new-summary')
  await expect(summary).toContainText('归入 1 张工单')

  // 提交前让这条链接被别处归属，整批归属应失败。
  const grab = await request.post(`/api/v1/guests/${grabberId}/assign-links`, {
    data: { link_ids: [link.id] },
  })
  expect(grab.status(), `抢占老链接失败：${await grab.text()}`).toBe(200)
  await summary.getByRole('button', { name: '建立访客' }).click()

  // 访客已建立、不回滚；页面停在详情并有可见失败提示。
  await page.waitForURL((u) => /^\/guests\/\d+$/.test(u.pathname))
  await expect(page.locator('#toast')).toContainText('归入老工单失败')
  await expect(page.locator('.guest-profile')).toContainText(name)
})
