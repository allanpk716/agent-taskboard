// 工单详情「归到访客」底部面板三步（#344）：选访客 → 顺带勾老工单（当前
// 链接锁定）→ 确认访问码；提交走批量归属接口，完成后链接区显示归属结果。
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

/** 建一位已认领访客：先在另一张工单上建链接并认领，链接留在名下。 */
async function createClaimedGuest(
  request: APIRequestContext,
  name: string,
): Promise<number> {
  const ticket = await createTicket(request, { title: `${name} 的认领工单` })
  const res = await request.post(`/api/v1/tickets/${ticket}/guest-links`, {
    data: { note: name, new_guest_name: name, valid_days: 30 },
  })
  expect(res.status(), `建立已认领访客失败：${await res.text()}`).toBe(201)
  const body = (await res.json()) as { guest_id: number; url: string }
  await claimLegacy(request, body, name)
  return body.guest_id
}

async function openAssignSheet(page: Page, ticket: string) {
  await page.goto(`/t/${ticket}`)
  await page.getByRole('button', { name: '访客链接' }).click()
  await page.getByRole('button', { name: '归到访客' }).click()
  return page.getByTestId('ticket-assign-guest-sheet')
}

test('工单详情归到访客面板：已认领访客顺带老工单并复制主页文案', async ({ page, request }) => {
  await loginAdmin(request)
  const run = `${RUN}${++seedSeq}`
  const guestName = `检验科-面板张${run}`
  await createClaimedGuest(request, guestName)
  const currentTicket = await createTicket(request, { title: `面板当前工单${run}` })
  const current = await createLegacyLink(request, currentTicket, `${guestName}的联系`)
  await claimLegacy(request, current, '面板张')
  const oldTicket = await createTicket(request, { title: `面板顺带老工单${run}` })
  const old = await createLegacyLink(request, oldTicket, guestName)
  await claimLegacy(request, old, '面板张')

  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)
  const sheet = await openAssignSheet(page, currentTicket)
  await expect(sheet).toContainText('1 · 归给谁')
  await sheet.getByLabel('搜索访客').fill(guestName)
  await sheet.locator('.guest-row', { hasText: guestName }).first().click()
  await sheet.getByRole('button', { name: '下一步' }).click()

  await expect(sheet).toContainText('2 · TA 可能还有这些老工单')
  const currentBox = sheet.getByLabel(new RegExp(`面板当前工单${run}`))
  await expect(currentBox).toBeChecked()
  await expect(currentBox).toBeDisabled()
  await sheet.getByLabel(new RegExp(`面板顺带老工单${run}`)).check()
  await sheet.getByRole('button', { name: '下一步' }).click()

  await expect(sheet).toContainText('3 · 确认归属和访问码')
  await expect(sheet).toContainText(/保留 面板张\d* 的访问码/)
  await expect(sheet).toContainText('所选老链接原来的访问码和设备都会作废')
  await sheet.getByRole('button', { name: '确认归属（2 条）' }).click()

  await expect(page.locator('#toast')).toContainText(`已归到「${guestName}」`)
  await expect(sheet).toHaveCount(0)
  const links = page.locator('.modal', { hasText: `访客链接 · ${currentTicket}` })
  await expect(links.locator('[data-guest-name]')).toHaveText(guestName)
  await links.getByRole('button', { name: '复制主页文案' }).first().click()
  await expect(page.locator('#toast')).toContainText('主页文案已复制')
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  expect(copied).toContain(guestName)
  expect(copied).toMatch(/\/h\/tbh_/)
})

test('工单详情归到访客面板：新建访客沿用当前链接访问码', async ({ page, request }) => {
  await loginAdmin(request)
  const run = `${RUN}${++seedSeq}`
  const guestName = `检验科-面板新客${run}`
  const currentTicket = await createTicket(request, { title: `面板新客当前工单${run}` })
  const current = await createLegacyLink(request, currentTicket, '新客当前联系')
  const oldClaim = await claimLegacy(request, current, '面板新客')

  await login(page, ADMIN)
  const sheet = await openAssignSheet(page, currentTicket)
  await sheet.getByLabel('新建访客名字').fill(guestName)
  await sheet.getByRole('button', { name: '新建并继续' }).click()
  await sheet.getByRole('button', { name: '下一步' }).click()

  const currentBox = sheet.getByLabel(new RegExp(`面板新客当前工单${run}`))
  await expect(currentBox).toBeChecked()
  await sheet.getByRole('button', { name: '下一步' }).click()

  await expect(sheet.getByTestId('guest-access-code-card')).toContainText(/沿用 .+ 的访问码/)
  await expect(sheet.locator('.guest-access-choice input[type="radio"]').first()).toBeChecked()
  await expect(sheet.getByTestId('guest-assign-consequence')).toContainText(
    '的访问码和已登录设备会转给',
  )
  await sheet.getByRole('button', { name: '确认归属（1 条）' }).click()
  await expect(page.locator('#toast')).toContainText(`已归到「${guestName}」`)
  await expect(sheet).toHaveCount(0)
  const links = page.locator('.modal', { hasText: `访客链接 · ${currentTicket}` })
  await expect(links.locator('[data-guest-name]')).toHaveText(guestName)

  // 沿用的访问码真的能进入新访客的主页。
  const listRes = await request.get(`/api/v1/guests?q=${encodeURIComponent(guestName)}`)
  expect(listRes.ok(), '读取新访客失败').toBeTruthy()
  const guests = ((await listRes.json()) as { guests: Array<{ id: number; link_count: number }> }).guests
  const guestID = guests.find((g) => g.link_count > 0)?.id
  expect(guestID, '面板应已新建访客并归属链接').toBeDefined()
  const homeText = await request.post(`/api/v1/guests/${guestID}/home-share-text`)
  expect(homeText.status(), '读取访客主页失败').toBe(200)
  const home = (await homeText.json()) as { home_url: string }
  const guestPage = await page.context().newPage()
  await guestPage.goto(home.home_url)
  await guestPage.locator('#guest-home-code').fill(oldClaim.access_code)
  await guestPage.getByRole('button', { name: '进入主页' }).click()
  await expect(guestPage.getByRole('heading', { name: guestName })).toBeVisible()
})
