import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, createTicket, login, loginAdmin, openGuestPage } from './helpers'

async function revise(request: APIRequestContext, number: string, revision: number, body: string) {
  const res = await request.patch(`/api/v1/tickets/${number}`, {
    data: { brief_md: body, base_revision: revision },
  })
  expect(res.status(), `修订第 ${revision} 版失败：${await res.text()}`).toBe(200)
}

async function upload(request: APIRequestContext, number: string, name: string, body: string) {
  const res = await request.post(`/api/v1/tickets/${number}/attachments`, {
    multipart: { file: { name, mimeType: 'text/plain', buffer: Buffer.from(body) } },
  })
  expect(res.status(), `上传 ${name} 失败：${await res.text()}`).toBe(201)
  return (await res.json()) as { id: string }
}

test('成员可原位对比任意两个说明版本', async ({ page, request }) => {
  const number = await createTicket(request, { title: '说明版本对比' })
  await revise(request, number, 1, '第一段新内容\n共同段落：第二版结尾在这里\n结尾')
  await revise(request, number, 2, '第一段新内容\n共同段落：第三版结尾在这里\n结尾')

  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  const brief = page.locator('[data-bubble="brief"]')
  await expect(brief.locator('.version-pill')).toHaveCount(3)
  await brief.getByRole('button', { name: 'v3' }).click()
  await expect(brief.locator('.diff-context')).toContainText('第 2 版 → 第 3 版')
  await expect(brief.locator('.diff-cell.old').filter({ hasText: '第二版结尾' })).toBeVisible()
  await expect(brief.locator('.diff-cell.new').filter({ hasText: '第三版结尾' })).toBeVisible()
  await expect(brief.locator('.diff-cell.old .inline-change')).toHaveText('二')
  await expect(brief.locator('.diff-cell.new .inline-change')).toHaveText('三')

  await brief.getByRole('button', { name: '✕ 退出对比' }).click()
  await page.reload()
  await brief.getByRole('button', { name: 'v1' }).click({ modifiers: ['Shift'] })
  await brief.getByRole('button', { name: 'v3' }).click({ modifiers: ['Shift'] })
  await expect(brief.locator('.diff-context')).toContainText('第 1 版 → 第 3 版')
  await expect(brief.locator('.diff-cell.old').filter({ hasText: '说明版本对比 的说明。' })).toBeVisible()
  await expect(brief.locator('.diff-cell.new').filter({ hasText: '第一段新内容' })).toBeVisible()

  await page.locator('.event', { hasText: '修订了说明（第 3 版）' })
    .getByRole('button', { name: '查看差异' }).click()
  await expect(brief.locator('.diff-context')).toContainText('第 2 版 → 第 3 版')
  await brief.getByRole('button', { name: '✕ 退出对比' }).click()
  await expect(brief.locator('.diff-context')).toHaveCount(0)
})

test('成员可查看第 1 版全文并下载后来移除的附件', async ({ page, request }) => {
  const number = await createTicket(request, { title: '旧版附件' })
  const old = await upload(request, number, '旧日志.log', 'old-log\n')
  let res = await request.patch(`/api/v1/tickets/${number}`, {
    data: { attachment_ids: [old.id], base_revision: 1 },
  })
  expect(res.status(), `挂附件失败：${await res.text()}`).toBe(200)
  res = await request.patch(`/api/v1/tickets/${number}`, {
    data: { brief_md: '新版没有附件。', attachment_ids: [], base_revision: 2 },
  })
  expect(res.status(), `移除附件失败：${await res.text()}`).toBe(200)

  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  const brief = page.locator('[data-bubble="brief"]')
  await page.locator('.event', { hasText: '创建了工单' })
    .getByRole('button', { name: '查看第 1 版' }).click()
  await expect(brief.locator('.diff-context')).toContainText('第 1 版全文')
  await expect(brief.locator('.version-full')).toContainText('旧版附件 的说明。')
  await brief.getByRole('button', { name: 'v2' }).click()
  await brief.getByRole('button', { name: '只看这一版全文' }).click()
  await expect(brief.locator('.version-full .att', { hasText: '旧日志.log' }))
    .toContainText('后来的版本中已移除')
  const href = await brief.locator('.version-full .att', { hasText: '旧日志.log' }).getAttribute('href')
  const downloaded = await request.get(href ?? '')
  expect(downloaded.status()).toBe(200)
  expect(await downloaded.text()).toContain('old-log')
})

test('上线前未存档的时间线版本不提供查看入口', async ({ page, request }) => {
  const number = await createTicket(request, { title: '上线前历史' })
  await revise(request, number, 1, '上线后的当前版')
  await login(page, ADMIN)
  const res = await request.get(`/api/v1/tickets/${number}`)
  expect(res.ok()).toBeTruthy()
  const ticket = (await res.json()) as { timeline: Array<Record<string, unknown>> }
  ticket.timeline = ticket.timeline.map((event) =>
    event.type === 'created' || event.type === 'brief_revised'
      ? { ...event, brief_version_saved: false }
      : event,
  )
  await page.route(`**/api/v1/tickets/${number}`, (route) =>
    route.fulfill({ json: ticket }),
  )
  await page.goto(`/t/${number}`)
  await expect(page.locator('.event', { hasText: '创建了工单' })).toContainText(/ · 此版本上线前未存档$/)
  await expect(page.locator('.event', { hasText: '修订了说明' })).toContainText(/ · 此版本上线前未存档$/)
  await expect(page.getByRole('button', { name: '查看第 1 版' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '查看差异' })).toHaveCount(0)
})

test('访客页能看到说明版本并对比', async ({ page, request }) => {
  const number = await createTicket(request, { title: '访客说明版本' })
  await revise(request, number, 1, '旧说明\n共同')
  await loginAdmin(request)
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '检验科-张三', valid_days: 30 },
  })
  expect(link.status(), `创建访客链接失败：${await link.text()}`).toBe(201)
  const linkKey = ((await link.json()) as { url: string }).url.replace('/g/', '')
  await openGuestPage(page, request, linkKey)
  const brief = page.locator('[data-bubble="brief"]')
  await expect(brief.locator('.version-pill')).toHaveCount(2)
  await brief.getByRole('button', { name: 'v2' }).click()
  await expect(brief.locator('.diff-context')).toContainText('第 1 版 → 第 2 版')
  await expect(brief.locator('.diff-cell.old').filter({ hasText: '访客说明版本 的说明。' })).toBeVisible()
})
