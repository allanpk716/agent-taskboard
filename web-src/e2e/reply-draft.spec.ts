import { test, expect } from '@playwright/test'
import type { APIRequestContext } from '@playwright/test'
import { createTicket, login, loginAdmin, markDraftIntroSeen, wang } from './helpers'

async function issueRequiredAgent(request: APIRequestContext): Promise<string> {
  const member = await wang()
  const login = await request.post('/api/v1/session', { data: member })
  expect(login.status(), `王工登录失败：${await login.text()}`).toBe(200)
  const res = await request.post('/api/v1/me/agent-tokens', {
    data: { name: `笔记本-${Date.now()}`, draft_mode: 'required' },
  })
  expect(res.status(), `签发 Agent 失败：${await res.text()}`).toBe(201)
  await markDraftIntroSeen(request)
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

async function replyDraft(
  request: APIRequestContext,
  token: string,
  number: string,
  body = '这是 Agent 起草的回复。',
): Promise<{ id: string; version: number; preview_url: string }> {
  const drafted = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: body },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(drafted.status(), `Agent 回复失败：${await drafted.text()}`).toBe(202)
  return ((await drafted.json()) as { draft: { id: string; version: number; preview_url: string } }).draft
}

test('Agent 回复存成草稿，成员确认后正式发出', async ({ page, request }) => {
  const number = await createTicket(request, { title: 'Agent 起草回复', assignee: '王工' })
  const token = await issueRequiredAgent(request)
  await replyDraft(request, token, number)

  await login(page, await wang())
  await page.goto(`/t/${number}#draft`)
  await expect(page.getByLabel('回复草稿')).toContainText('草稿 · 未发出 · 只有你能看到')
  await expect(page.getByLabel('回复草稿')).toContainText('这是 Agent 起草的回复。')
  await expect(page.locator('.draft-lock')).toContainText('你在这张工单上有一份草稿')
  await page.getByRole('button', { name: '发出', exact: true }).click()
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
  await markDraftIntroSeen(request)
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

test('成员直接修改回复草稿并发出后署名注明修改', async ({ page, request }) => {
  const number = await createTicket(request, { title: '成员手改草稿', assignee: '王工' })
  const token = await issueRequiredAgent(request)
  await replyDraft(request, token, number)

  await login(page, await wang())
  await page.goto(`/t/${number}#draft`)
  await page.getByRole('button', { name: '直接修改' }).click()
  await page.getByLabel('草稿内容').fill('这是成员修改后的回复。')
  await page.getByRole('button', { name: '保存草稿' }).click()
  await expect(page.getByLabel('回复草稿')).toContainText('第 2 版')
  await page.getByRole('button', { name: '发出', exact: true }).click()
  await expect(page.getByLabel('回复草稿')).toHaveCount(0)
  await expect(page.locator('[data-bubble="reply"]')).toContainText('这是成员修改后的回复。')
  await expect(page.locator('[data-bubble="reply"] .draft-confirm')).toHaveText('经王工修改并确认')
})

test('成员手改后 Agent 用旧版本改草稿得到 409', async ({ page, request }) => {
  const number = await createTicket(request, { title: '草稿版本冲突', assignee: '王工' })
  const token = await issueRequiredAgent(request)
  const draft = await replyDraft(request, token, number)

  await login(page, await wang())
  await page.goto(`/t/${number}#draft`)
  await page.getByRole('button', { name: '直接修改' }).click()
  await page.getByLabel('草稿内容').fill('成员先改了一版。')
  await page.getByRole('button', { name: '保存草稿' }).click()
  await expect(page.getByLabel('回复草稿')).toContainText('第 2 版')

  const stale = await request.put(`/api/v1/drafts/${draft.id}`, {
    data: { base_version: draft.version, request: { body_md: 'Agent 晚到的版本。' } },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(stale.status()).toBe(409)
  expect(((await stale.json()) as { error: { code: string } }).error.code).toBe('draft_version_conflict')
})

test('复制修改指令时代入工单和草稿信息', async ({ page, request }) => {
  const title = '复制草稿修改指令'
  const number = await createTicket(request, { title, assignee: '王工' })
  const token = await issueRequiredAgent(request)
  const draft = await replyDraft(request, token, number)

  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, await wang())
  await page.goto(`/t/${number}#draft`)
  await page.getByRole('button', { name: '复制修改指令' }).click()
  await expect(page.locator('.toast, #toast')).toContainText('修改指令已复制')
  const base = new URL(page.url()).origin
  const expected = `请修改我在 agent-taskboard 工单 ${number}「${title}」上的回复草稿 ${draft.id}（当前第 ${draft.version} 版）。
1. 凭证：用环境变量 TASKBOARD_TOKEN；读不到或返回 401 就停下来告诉我，不要用密码登录。
2. 先读当前草稿：curl -H "Authorization: Bearer $TASKBOARD_TOKEN" "${base}/api/v1/drafts/${draft.id}"
   我可能亲手改过，以最新一版为准，不要把我的改动改回去。需要上下文时再读工单：${base}/api/v1/tickets/${number}?format=markdown
3. 按我下面的意见修改，然后整份写回：PUT ${base}/api/v1/drafts/${draft.id}，请求体 {"base_version": <刚读到的 version>, "request": {…完整内容…}}。
   返回 409 draft_version_conflict，说明你读完之后草稿又被改过；重新读，在最新版上改。
4. 改完把查看地址 ${base}${draft.preview_url} 发给我，说明这一版改了什么。发出或丢弃由我在网页上做，你做不了。

我的修改意见：`
  await expect
    .poll(async () => page.evaluate(() => navigator.clipboard.readText().then((s) => s.replace(/\r\n/g, '\n'))))
    .toBe(expected)
  await expect(page.locator('.instruction-preview')).toContainText(
    `请修改我在 agent-taskboard 工单 ${number}「${title}」上的回复草稿 ${draft.id}`,
  )
})

test('草稿写成后工单有新变化时确认后仍可发出', async ({ page, request }) => {
  const number = await createTicket(request, { title: '草稿后的新变化', assignee: '王工' })
  const token = await issueRequiredAgent(request)
  await replyDraft(request, token, number)
  await loginAdmin(request)
  const changed = await request.post(`/api/v1/tickets/${number}/replies`, { data: { body_md: '创建者补充了一条回复。' } })
  expect(changed.status(), `创建者回复失败：${await changed.text()}`).toBe(201)

  await login(page, await wang())
  await page.goto(`/t/${number}#draft`)
  await expect(page.locator('.draft-new-events')).toContainText(/草稿写成（.*）之后的新变化/)
  await page.getByRole('button', { name: '发出', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: /草稿写成后工单有 1 条新变化/ })
  await expect(dialog).toContainText('创建者补充了一条回复。')
  await dialog.getByRole('button', { name: '仍然发出' }).click()
  await expect(page.getByLabel('回复草稿')).toHaveCount(0)
  await expect(page.locator('[data-bubble="reply"]')).toHaveCount(2)
})

test('工单已关闭后发出按钮禁用并显示原因', async ({ page, request }) => {
  const number = await createTicket(request, { title: '关闭后的草稿', assignee: '王工' })
  const token = await issueRequiredAgent(request)
  await replyDraft(request, token, number)
  await loginAdmin(request)
  const closed = await request.post(`/api/v1/tickets/${number}/close`, { data: { reason: 'done' } })
  expect(closed.status(), `关闭失败：${await closed.text()}`).toBe(200)

  await login(page, await wang())
  await page.goto(`/t/${number}#draft`)
  await expect(page.locator('.draft-blocked')).toContainText('工单已关闭，不能发出回复草稿')
  await expect(page.getByRole('button', { name: '发出', exact: true })).toBeDisabled()
})

test('成员切换作为交付发出后工单进入交付状态', async ({ page, request }) => {
  const number = await createTicket(request, { title: '交付草稿', assignee: '王工' })
  const token = await issueRequiredAgent(request)
  await replyDraft(request, token, number, '这份回复会作为交付。')

  await login(page, await wang())
  await page.goto(`/t/${number}#draft`)
  await page.getByRole('button', { name: '作为交付发出' }).click()
  await page.getByRole('button', { name: '发出', exact: true }).click()
  await expect(page.getByLabel('回复草稿')).toHaveCount(0)
  await expect(page.locator('[data-bubble="reply"] .deliver-tag')).toHaveText('交付')
  await expect(page.locator('.tc-top')).toContainText('等待')
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

  await card.getByRole('button', { name: '发出', exact: true }).click()
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
