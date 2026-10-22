import { test, expect, type APIRequestContext } from '@playwright/test'
import { login, ADMIN } from './helpers'

async function issueAgent(
  request: APIRequestContext,
  name: string,
  draftMode: 'required' | 'direct' = 'direct',
): Promise<string> {
  await request.post('/api/v1/session', { data: ADMIN })
  const res = await request.post('/api/v1/me/agent-tokens', {
    data: { name, draft_mode: draftMode },
  })
  expect(res.status(), `签发 Agent 失败：${await res.text()}`).toBe(201)
  await request.post('/api/v1/me/notices/drafts_intro/seen')
  return ((await res.json()) as { token: string }).token
}

test('Agent 表格显示草稿模式，切到可直接发前需要确认', async ({ page, request }) => {
  const run = Date.now()
  const token = await issueAgent(request, `模式开关${run}`, 'required')
  await login(page, ADMIN)
  await page.goto('/me')
  const row = page.locator(`tr[data-token="模式开关${run}"]`)
  await expect(row.getByText('必须走草稿')).toBeVisible()

  await row.getByRole('switch', { name: '草稿模式' }).click()
  const dialog = page.getByRole('dialog', { name: '改为可直接发？' })
  await expect(dialog).toContainText('它写的内容将立刻对外可见，署名里不注明「经你确认」')
  await dialog.getByRole('button', { name: '取消' }).click()
  await expect(dialog).toHaveCount(0)
  await expect(row.getByText('必须走草稿')).toBeVisible()
  let session = await request.get('/api/v1/session', {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(((await session.json()) as { viewer: { agent: { draft_mode: string } } }).viewer.agent.draft_mode).toBe('required')

  await row.getByRole('switch', { name: '草稿模式' }).click()
  await dialog.getByRole('button', { name: '改为可直接发' }).click()
  await expect(row.getByText('可直接发')).toBeVisible()
  session = await request.get('/api/v1/session', {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(((await session.json()) as { viewer: { agent: { draft_mode: string } } }).viewer.agent.draft_mode).toBe('direct')
})

test('签发 Agent 时默认必须走草稿', async ({ page, request }) => {
  const run = Date.now()
  await login(page, ADMIN)
  await page.goto('/me')
  await page.getByRole('button', { name: '＋ 生成新 token' }).click()
  await page.locator('#tk-name').fill(`必须草稿${run}`)
  await expect(page.getByRole('radio', { name: /必须走草稿（推荐）/ })).toBeChecked()
  await expect(page.getByRole('radio', { name: '可直接发' })).not.toBeChecked()
  await page.getByRole('button', { name: '生成', exact: true }).click()
  const onboarding = page.locator('[data-onboarding-preview]')
  await expect(onboarding).toContainText('setx TASKBOARD_TOKEN')
  const token = (((await onboarding.textContent()) ?? '').match(/tbag_[A-Za-z0-9_-]+/) ?? [])[0]
  const session = await request.get('/api/v1/session', {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(((await session.json()) as { viewer: { agent: { draft_mode: string } } }).viewer.agent.draft_mode).toBe('required')
})

test('新签发的Agent不触发草稿上线提示', async ({ page, request }) => {
  const suffix = Date.now().toString(36)
  await request.post('/api/v1/session', { data: ADMIN })
  const member = {
    username: `draft-notice-${suffix}`,
    password: `pass-${suffix}`,
  }
  const created = await request.post('/api/v1/members', {
    data: { username: member.username, name: '草稿提示成员' },
  })
  expect(created.ok(), await created.text()).toBeTruthy()
  const initialPassword = (((await created.json()) as { initial_password: string }).initial_password)
  await request.post('/api/v1/session', { data: { username: member.username, password: initialPassword } })
  const issued = await request.post('/api/v1/me/agent-tokens', {
    data: { name: `提示Agent${suffix}`, draft_mode: 'required' },
  })
  expect(issued.ok(), await issued.text()).toBeTruthy()

  await login(page, { username: member.username, password: initialPassword })
  const dialog = page.getByRole('dialog', { name: '工单板已更新' })
  await expect(dialog).toHaveCount(0)
  await page.reload()
  await expect(page.getByRole('dialog', { name: '工单板已更新' })).toHaveCount(0)
})

test('已停用的 Agent 不显示草稿开关', async ({ page, request }) => {
  const run = Date.now()
  const name = `已停用模式${run}`
  await issueAgent(request, name)
  const list = await request.get('/api/v1/me/agent-tokens')
  const id = ((await list.json()) as { tokens: { id: number; name: string }[] }).tokens.find((t) => t.name === name)?.id
  expect(id).toBeTruthy()
  const revoked = await request.delete(`/api/v1/me/agent-tokens/${id}`)
  expect(revoked.status(), `撤销 Agent 失败：${await revoked.text()}`).toBe(204)

  await login(page, ADMIN)
  await page.goto('/me')
  const row = page.locator(`tr[data-token="${name}"]`)
  await expect(row.getByText('—')).toBeVisible()
  await expect(row.getByRole('switch')).toHaveCount(0)
})
