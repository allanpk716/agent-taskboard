// #342：未认领访客批量归属时选择沿用旧访问码或生成只显示一次的新码。
import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { ADMIN, createTicket, login, loginAdmin } from './helpers'

const RUN = String(Date.now() % 1_000_000)
let seedSeq = 0

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

async function createUnclaimedGuest(
  request: APIRequestContext,
  name: string,
): Promise<number> {
  const res = await request.post('/api/v1/guests', { data: { name } })
  expect(res.status(), `创建目标访客失败：${await res.text()}`).toBe(201)
  return ((await res.json()) as { guest: { id: number } }).guest.id
}

async function openGuestHomeWithCode(page: Page, homeUrl: string, code: string) {
  await page.goto(homeUrl)
  await page.locator('#guest-home-code').fill(code)
  await page.getByRole('button', { name: '进入主页' }).click()
}

test('未认领访客批量归属可沿用最近使用的旧访问码', async ({ page, request }) => {
  await loginAdmin(request)
  const run = `${RUN}${++seedSeq}`
  const name = `检验科-沿用${run}`
  const guestID = await createUnclaimedGuest(request, name)
  const sourceTicket = await createTicket(request, { title: `沿用来源老工单${run}` })
  const source = await createLegacyLink(request, sourceTicket, name)
  const oldClaim = await claimLegacy(request, source, '张三')
  const viewed = await request.get(`/api/v1/tickets/${sourceTicket}`, {
    headers: { Authorization: `Bearer ${oldClaim.device_token}` },
  })
  expect(viewed.status(), '让来源链接记录最近使用失败').toBe(200)
  const unclaimedTicket = await createTicket(request, { title: `沿用的未认领老工单${run}` })
  await createLegacyLink(request, unclaimedTicket, name)

  await login(page, ADMIN)
  await page.goto(`/guests/${guestID}/assign`)
  const assignPage = page.getByTestId('guest-assign-page')
  await assignPage.getByLabel(new RegExp(`沿用来源老工单${run}`)).check()
  await assignPage.getByLabel(new RegExp(`沿用的未认领老工单${run}`)).check()
  await expect(assignPage.locator(`[data-source-id="${source.id}"] input`)).toBeChecked()
  await expect(assignPage.getByTestId('guest-assign-consequence')).toContainText(
    '的访问码和已登录设备会转给',
  )
  await assignPage.getByRole('button', { name: '确认归到访客（2 条）' }).click()
  await page.waitForURL((u) => u.pathname === `/guests/${guestID}`)

  const homeRes = await request.post(`/api/v1/guests/${guestID}/home-share-text`)
  expect(homeRes.status(), '读取访客主页失败').toBe(200)
  const home = (await homeRes.json()) as { home_url: string }
  const guestPage = await page.context().newPage()
  await openGuestHomeWithCode(guestPage, home.home_url, oldClaim.access_code)
  await expect(guestPage.getByRole('heading', { name })).toBeVisible()
  await expect(guestPage.locator('.guest-home-ticket')).toHaveCount(2)
})

test('未认领访客批量归属可生成只显示一次的新访问码', async ({ page, request }) => {
  await loginAdmin(request)
  const run = `${RUN}${++seedSeq}`
  const name = `检验科-新码${run}`
  const guestID = await createUnclaimedGuest(request, name)
  const claimedTicket = await createTicket(request, { title: `新码前的老工单${run}` })
  const claimed = await createLegacyLink(request, claimedTicket, name)
  await claimLegacy(request, claimed, '张三')
  const unclaimedTicket = await createTicket(request, { title: `新码批次的未认领老工单${run}` })
  await createLegacyLink(request, unclaimedTicket, name)

  await login(page, ADMIN)
  await page.goto(`/guests/${guestID}/assign`)
  const assignPage = page.getByTestId('guest-assign-page')
  await assignPage.getByLabel(new RegExp(`新码前的老工单${run}`)).check()
  await assignPage.getByLabel(new RegExp(`新码批次的未认领老工单${run}`)).check()
  await assignPage.getByRole('radio', { name: '生成新的访问码' }).check()
  await expect(assignPage.getByTestId('guest-assign-consequence')).toContainText(
    '所选链接原来的访问码和设备都会作废',
  )
  await assignPage.getByRole('button', { name: '确认归到访客（2 条）' }).click()

  const result = assignPage.getByRole('region', { name: '新访问码只显示一次' })
  const code = await result.locator('[data-testid="guest-assign-access-code"]').textContent()
  expect(code).toMatch(/^\d{6}$/)
  await expect(page).toHaveURL(new RegExp(`/guests/${guestID}/assign$`))
  await page.getByRole('button', { name: '我已保存，回访客详情' }).click()
  await page.waitForURL((u) => u.pathname === `/guests/${guestID}`)

  const homeRes = await request.post(`/api/v1/guests/${guestID}/home-share-text`)
  expect(homeRes.status(), '读取访客主页失败').toBe(200)
  const home = (await homeRes.json()) as { home_url: string }
  const guestPage = await page.context().newPage()
  await openGuestHomeWithCode(guestPage, home.home_url, code!)
  await expect(guestPage.getByRole('heading', { name })).toBeVisible()
  await expect(guestPage.locator('.guest-home-ticket')).toHaveCount(2)
})
