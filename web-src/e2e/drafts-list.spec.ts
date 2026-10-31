import { test, expect } from '@playwright/test'
import type { APIRequestContext } from '@playwright/test'
import { createTicket, login, loginAdmin, markDraftIntroSeen, wang, loginWithRequest } from './helpers'

test.use({ viewport: { width: 1280, height: 800 } })

async function issueRequiredAgent(request: APIRequestContext): Promise<string> {
  const member = await wang()
  const session = await loginWithRequest(request, member)
  expect(session.status(), `王工登录失败：${await session.text()}`).toBe(200)
  const res = await request.post('/api/v1/me/agent-tokens', {
    data: { name: `草稿列表-${Date.now()}`, draft_mode: 'required' },
  })
  expect(res.status(), `签发 Agent 失败：${await res.text()}`).toBe(201)
  await markDraftIntroSeen(request)
  return ((await res.json()) as { token: string }).token
}

// discardMyDrafts 丢弃当前登录成员的全部待确认草稿。草稿徽章和「N 份待确认」
// 是全局计数，断言它们之前先清场，免得被别的 spec 留下的草稿干扰（#255）。
async function discardMyDrafts(request: APIRequestContext): Promise<void> {
  const res = await request.get('/api/v1/drafts')
  expect(res.status(), `读取草稿失败：${await res.text()}`).toBe(200)
  for (const draft of ((await res.json()) as { drafts: { id: string }[] }).drafts) {
    expect((await request.delete(`/api/v1/drafts/${draft.id}`)).status()).toBe(204)
  }
}

async function draftReply(
  request: APIRequestContext,
  token: string,
  number: string,
  body: string,
): Promise<void> {
  const res = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: body },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(res.status(), `Agent 写草稿失败：${await res.text()}`).toBe(202)
}

test('桌面「我的草稿」列出待确认草稿并跳到工单草稿处', async ({ page, request }) => {
  const first = await createTicket(request, { title: '草稿列表第一张', assignee: '王工' })
  const second = await createTicket(request, { title: '草稿列表第二张', assignee: '王工' })
  const token = await issueRequiredAgent(request)
  await discardMyDrafts(request)
  await draftReply(request, token, first, '第一张工单的草稿。')
  await draftReply(request, token, second, '第二张工单的草稿。')

  await login(page, await wang())
  const entry = page.getByRole('link', { name: /✎ 草稿/ })
  await expect(entry).toContainText('2')
  await entry.click()
  await expect(page.getByRole('heading', { name: '我的草稿' })).toBeVisible()
  await expect(page.getByText('只有你能看到 · 2 份待确认')).toBeVisible()
  const ticketDrafts = page.locator('section[aria-label="工单上的草稿"]')
  await expect(ticketDrafts).toContainText('草稿列表第一张')
  await expect(ticketDrafts).toContainText('草稿列表第二张')
  await expect(ticketDrafts).toContainText('回复草稿')
  await expect(page.locator('section[aria-label="建单草稿"]')).toBeVisible()

  await page.getByRole('link', { name: `打开 草稿列表第一张` }).click()
  await expect(page).toHaveURL(new RegExp(`/t/${first}#draft$`))
  await expect(page.getByLabel('回复草稿')).toContainText('第一张工单的草稿。')
})

test('草稿写成后工单有新回复时列表提示新变化', async ({ page, request }) => {
  const number = await createTicket(request, { title: '草稿后的新变化', assignee: '王工' })
  const token = await issueRequiredAgent(request)
  await draftReply(request, token, number, '草稿写成时的内容。')
  await loginAdmin(request)
  const changed = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: '草稿写成后来了一条新回复。' },
  })
  expect(changed.status(), `成员回复失败：${await changed.text()}`).toBe(201)

  await login(page, await wang())
  await page.getByRole('link', { name: /✎ 草稿/ }).click()
  const row = page.locator('.draft-row', { hasText: '草稿后的新变化' })
  await expect(row).toContainText('工单有 1 条新变化')
})

test('发出和丢弃草稿后徽章与列表更新', async ({ page, request }) => {
  const wangUser = await wang()
  const wangLogin = await loginWithRequest(request, wangUser)
  expect(wangLogin.status()).toBe(200)
  await discardMyDrafts(request)

  const sendNumber = await createTicket(request, { title: '列表发出草稿', assignee: '王工' })
  const discardNumber = await createTicket(request, { title: '列表丢弃草稿', assignee: '王工' })
  const token = await issueRequiredAgent(request)
  await draftReply(request, token, sendNumber, '这份会发出。')
  await draftReply(request, token, discardNumber, '这份会丢弃。')

  await login(page, wangUser)
  const entry = page.getByRole('link', { name: /✎ 草稿/ })
  await expect(entry).toContainText('2')
  await entry.click()
  await page.getByRole('link', { name: '打开 列表发出草稿' }).click()
  await page.getByLabel('回复草稿').getByRole('button', { name: '发出', exact: true }).click()
  await expect(page.locator('.draft-card')).toHaveCount(0)
  await entry.click()
  await expect(page.locator('.draft-row')).toHaveCount(1)
  await page.getByRole('link', { name: '打开 列表丢弃草稿' }).click()
  await page.getByLabel('回复草稿').getByRole('button', { name: '丢弃' }).click()
  await page.getByRole('dialog', { name: '丢弃草稿？' }).getByRole('button', { name: '丢弃' }).click()
  await expect(page.locator('.draft-card')).toHaveCount(0)
  await entry.click()
  await expect(entry).not.toContainText('1')
  await expect(page.locator('.draft-row')).toHaveCount(0)

  await loginWithRequest(request, wangUser)
  const tokens = await request.get('/api/v1/me/agent-tokens')
  expect(tokens.status()).toBe(200)
  for (const token of ((await tokens.json()) as { tokens: { id: number; revoked_at?: string | null }[] }).tokens) {
    if (!token.revoked_at) {
      const revoked = await request.delete(`/api/v1/me/agent-tokens/${token.id}`)
      expect(revoked.status()).toBe(204)
    }
  }
})

test.describe('手机视口', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('「我的」页提供草稿入口', async ({ page, request }) => {
    const number = await createTicket(request, { title: '手机草稿入口', assignee: '王工' })
    const token = await issueRequiredAgent(request)
    await draftReply(request, token, number, '手机上待确认。')

    await login(page, await wang())
    const inboxBanner = page.getByRole('link', { name: /你有 \d+ 份草稿待处理/ })
    await inboxBanner.click()
    await expect(page.locator('h1', { hasText: '我的草稿' })).toBeVisible()
    await page.goBack()
    const meTab = page.getByRole('link', { name: /我的/ })
    await expect(meTab.locator('.dot')).toHaveCSS('background-color', 'rgb(217, 119, 6)')
    await meTab.click()
    await page.getByRole('link', { name: /我的草稿（\d+）/ }).click()
    await expect(page.locator('h1', { hasText: '我的草稿' })).toBeVisible()
    await expect(page.locator('section[aria-label="工单上的草稿"]')).toContainText('手机草稿入口')

    await discardMyDrafts(request)
    const tokens = await request.get('/api/v1/me/agent-tokens')
    expect(tokens.status()).toBe(200)
    for (const token of ((await tokens.json()) as { tokens: { id: number; revoked_at?: string | null }[] }).tokens) {
      if (!token.revoked_at) {
        expect((await request.delete(`/api/v1/me/agent-tokens/${token.id}`)).status()).toBe(204)
      }
    }
  })
})
