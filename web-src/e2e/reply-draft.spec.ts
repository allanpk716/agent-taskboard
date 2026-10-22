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
