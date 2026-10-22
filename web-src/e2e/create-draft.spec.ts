import { expect, test, type APIRequestContext } from '@playwright/test'
import { login, markDraftIntroSeen, wang } from './helpers'

async function issueRequiredAgent(request: APIRequestContext): Promise<string> {
  const member = await wang()
  const loginRes = await request.post('/api/v1/session', { data: member })
  expect(loginRes.status(), `王工登录失败：${await loginRes.text()}`).toBe(200)
  const issued = await request.post('/api/v1/me/agent-tokens', {
    data: { name: `建单笔记本-${Date.now()}`, draft_mode: 'required' },
  })
  expect(issued.status(), `签发 Agent 失败：${await issued.text()}`).toBe(201)
  await markDraftIntroSeen(request)
  return ((await issued.json()) as { token: string }).token
}

test('Agent 建单草稿经通知确认后发出并展示访客文案', async ({ page, request }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  const who = await wang()
  await login(page, who)
  const token = await issueRequiredAgent(request)
  const drafted = await request.post('/api/v1/tickets', {
    data: {
      title: 'Agent 起草的建单',
      brief_md: '请确认这份草稿后再发出。',
      labels: ['机型:UD-2300'],
      guest_link: { note: '检验科-张三', can_edit_brief: true },
    },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(drafted.status(), `Agent 建单失败：${await drafted.text()}`).toBe(202)
  const draft = ((await drafted.json()) as { draft: { id: string } }).draft

  await page.getByRole('button', { name: '通知' }).click()
  const row = page.locator('.nrow', { hasText: '写了建单草稿待确认：Agent 起草的建单' })
  await expect(row).toBeVisible()
  await row.click()
  await expect(page).toHaveURL(new RegExp(`/drafts/${draft.id}$`))
  await expect(page.locator('.create-draft-banner')).toContainText('草稿 · 未发布 · 只有你能看到')
  await expect(page.getByLabel('建单草稿')).toContainText('编号在发出时分配')
  await expect(page.getByLabel('建单草稿')).toContainText('请确认这份草稿后再发出。')

  await page.getByLabel('标签').fill('机型:US-1680')
  await page.getByRole('button', { name: '发出', exact: true }).click()
  const sentDialog = page.getByRole('dialog', { name: '建单草稿已发出' })
  await expect(sentDialog).toContainText('经王工修改并确认')
  const shareText = await sentDialog.locator('.linkbox').textContent()
  expect(shareText ?? '').toMatch(/\/g\/tbg_[A-Za-z0-9_-]+/)
  await sentDialog.getByRole('button', { name: '打开新工单' }).click()
  await expect(page).toHaveURL(/\/t\/\d{8}-\d+$/)
  await expect(page.locator('.dhead h1')).toHaveText('Agent 起草的建单')
  await expect(page.locator('.event .draft-confirm')).toHaveText('经王工修改并确认')
})
