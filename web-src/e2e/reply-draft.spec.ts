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

async function uploadAgentAttachment(
  request: APIRequestContext,
  token: string,
  number: string,
): Promise<string> {
  const res = await request.post(`/api/v1/tickets/${number}/attachments`, {
    headers: { Authorization: `Bearer ${token}` },
    multipart: {
      file: {
        name: 'Agent结果.txt',
        mimeType: 'text/plain',
        buffer: Buffer.from('attachment from agent', 'utf8'),
      },
    },
  })
  expect(res.status(), `Agent 上传附件失败：${await res.text()}`).toBe(201)
  return ((await res.json()) as { id: string }).id
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

test('回复草稿显示附件并在发出后挂到回复', async ({ page, request }) => {
  const number = await createTicket(request, { title: '带附件的回复草稿', assignee: '王工' })
  const token = await issueRequiredAgent(request)
  const attachmentId = await uploadAgentAttachment(request, token, number)
  const drafted = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: '结果见附件。', attachment_ids: [attachmentId] },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(drafted.status(), `Agent 回复失败：${await drafted.text()}`).toBe(202)

  await login(page, await wang())
  await page.goto(`/t/${number}#draft`)
  const card = page.getByLabel('回复草稿')
  await expect(card).toContainText('Agent结果.txt')
  const link = card.locator('.att')
  await expect(link).toHaveAttribute('href', new RegExp(`/api/v1/attachments/${attachmentId}/Agent%E7%BB%93%E6%9E%9C\\.txt$`))
  const href = await link.getAttribute('href')
  if (!href) throw new Error('草稿附件缺少下载地址')
  const downloaded = await page.request.get(href)
  expect(downloaded.status(), `草稿附件下载失败：${await downloaded.text()}`).toBe(200)
  expect(await downloaded.text()).toBe('attachment from agent')

  await card.getByRole('button', { name: '发出' }).click()
  await expect(card).toHaveCount(0)
  await expect(page.locator('[data-bubble="reply"]')).toContainText('Agent结果.txt')
})

test('成员可以查看已发出回复的起草过程', async ({ page, request }) => {
  const number = await createTicket(request, { title: '查看起草过程', assignee: '王工' })
  const token = await issueRequiredAgent(request)
  const auth = { Authorization: `Bearer ${token}` }
  const drafted = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: '第一版结论。' },
    headers: auth,
  })
  expect(drafted.status(), `Agent 写草稿失败：${await drafted.text()}`).toBe(202)
  const draft = ((await drafted.json()) as { draft: { id: string; version: number } }).draft
  const uploaded = await request.post(`/api/v1/tickets/${number}/attachments`, {
    multipart: { file: { name: '旧版日志.log', mimeType: 'text/plain', buffer: Buffer.from('old log') } },
    headers: auth,
  })
  expect(uploaded.status(), `上传草稿附件失败：${await uploaded.text()}`).toBe(201)
  const attachment = ((await uploaded.json()) as { id: string }).id
  const second = await request.put(`/api/v1/drafts/${draft.id}`, {
    data: {
      base_version: draft.version,
      request: { body_md: '第二版补充了原因。', attachment_ids: [attachment] },
    },
    headers: auth,
  })
  expect(second.status(), `Agent 改草稿失败：${await second.text()}`).toBe(200)
  const version2 = ((await second.json()) as { version: number }).version

  await login(page, await wang())
  const third = await page.request.put(`/api/v1/drafts/${draft.id}`, {
    data: { base_version: version2, request: { body_md: '第三版由成员确认发出。' } },
  })
  expect(third.status(), `成员改草稿失败：${await third.text()}`).toBe(200)
  const version3 = ((await third.json()) as { version: number }).version
  const sent = await page.request.post(`/api/v1/drafts/${draft.id}/send`, {
    data: { version: version3 },
  })
  expect(sent.status(), `发出草稿失败：${await sent.text()}`).toBe(201)

  await page.goto(`/t/${number}`)
  const reply = page.locator('[data-bubble="reply"]').filter({ hasText: '第三版由成员确认发出。' })
  await expect(reply).toBeVisible()
  await expect(reply).toContainText('查看起草过程（3 版）')
  await reply.getByRole('button', { name: '查看起草过程（3 版）' }).click()
  await expect(reply.getByRole('group', { name: '起草过程版本' })).toBeVisible()
  await reply.getByRole('button', { name: 'v3' }).click()
  const compare = reply.locator('.brief-compare.drafting')
  await expect(compare).toBeVisible()
  await expect(compare).toContainText('💬 回复 #1 的起草过程')
  await expect(compare).toContainText('第 2 版 → 第 3 版')
  await expect(compare).toContainText('回复发出后不可修改；这里是发出前的草稿各版')
  await expect(compare.locator('.diff-context .object')).toHaveCSS('color', 'rgb(146, 64, 14)')
  await expect(compare.locator('.diff-table')).toContainText('第二版补充了原因。')
  await expect(compare.locator('.diff-table')).toContainText('第三版由成员确认发出。')
  await expect(compare.locator('.diff-table')).toContainText(`附件已清除：${attachment}`)
})
