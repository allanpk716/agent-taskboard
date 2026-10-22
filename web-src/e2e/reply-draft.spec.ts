import { test, expect } from '@playwright/test'
import type { APIRequestContext } from '@playwright/test'
import { createTicket, login, wang } from './helpers'

async function issueRequiredAgent(request: APIRequestContext): Promise<string> {
  const member = await wang()
  const login = await request.post('/api/v1/session', { data: member })
  expect(login.status(), `王工登录失败：${await login.text()}`).toBe(200)
  const res = await request.post('/api/v1/me/agent-tokens', {
    data: { name: `笔记本-${Date.now()}`, draft_mode: 'required' },
  })
  expect(res.status(), `签发 Agent 失败：${await res.text()}`).toBe(201)
  return ((await res.json()) as { token: string }).token
}

test('Agent 回复存成草稿，成员确认后正式发出', async ({ page, request }) => {
  const number = await createTicket(request, { title: 'Agent 起草回复', assignee: '王工' })
  const token = await issueRequiredAgent(request)
  const drafted = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: '这是 Agent 起草的回复。' },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(drafted.status(), `Agent 回复失败：${await drafted.text()}`).toBe(202)

  await login(page, await wang())
  await page.goto(`/t/${number}#draft`)
  await expect(page.getByLabel('回复草稿')).toContainText('草稿 · 未发出 · 只有你能看到')
  await expect(page.getByLabel('回复草稿')).toContainText('这是 Agent 起草的回复。')
  await expect(page.locator('.draft-lock')).toContainText('你在这张工单上有一份草稿')
  await page.getByRole('button', { name: '发出' }).click()
  await expect(page.getByLabel('回复草稿')).toHaveCount(0)
  await expect(page.locator('.draft-lock')).toHaveCount(0)
  await expect(page.locator('.draft-confirm')).toContainText('经王工确认')
})

test('成员丢弃回复草稿后工单不留痕迹', async ({ page, request }) => {
  const number = await createTicket(request, { title: '丢弃 Agent 草稿', assignee: '王工' })
  const token = await issueRequiredAgent(request)
  const drafted = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: '这份会被丢弃。' },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(drafted.status()).toBe(202)

  await login(page, await wang())
  await page.goto(`/t/${number}#draft`)
  await page.getByRole('button', { name: '丢弃' }).click()
  await page.getByRole('dialog', { name: '丢弃草稿？' }).getByRole('button', { name: '丢弃' }).click()
  await expect(page.getByLabel('回复草稿')).toHaveCount(0)
  await expect(page.locator('.draft-lock')).toHaveCount(0)
  const replies = await page.locator('[data-bubble="reply"]').count()
  expect(replies).toBe(0)
})

test('草稿通知出现在铃铛并定位到草稿处', async ({ page, request }) => {
  const number = await createTicket(request, { title: '草稿通知', assignee: '王工' })
  const session = await request.post('/api/v1/session', { data: await wang() })
  expect(session.status()).toBe(200)
  await login(page, await wang())
  const issued = await request.post('/api/v1/me/agent-tokens', {
    data: { name: `通知笔记本-${Date.now()}`, draft_mode: 'required' },
  })
  expect(issued.status(), `签发 Agent 失败：${await issued.text()}`).toBe(201)
  const token = ((await issued.json()) as { token: string }).token
  const drafted = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: '待确认的草稿。' },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(drafted.status()).toBe(202)

  const badge = page.locator('.bellbtn b')
  const unreadBefore = Number(await badge.textContent() ?? '0')
  await page.getByRole('button', { name: '通知' }).click()
  const row = page.locator('.nrow', { hasText: '写了回复草稿待确认' }).filter({ hasText: '通知笔记本-' })
  await expect(row).toBeVisible()
  await expect(badge).toHaveText(String(unreadBefore))
  await row.click()
  await expect(page).toHaveURL(new RegExp(`/t/${number}#draft$`))
  await expect(page.getByLabel('回复草稿')).toContainText('待确认的草稿。')
})
