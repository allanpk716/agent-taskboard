// PC 分栏视图（宽屏 / 中屏）下列表常驻、只换右侧工单：详情页必须随工单重挂。
// 否则它内部的回复编辑器、草稿卡片和没关的弹窗会把上一张工单的状态带到下一张，
// 浏览器后退/前进只换地址里的工单号时也一样。
import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, createTicket, login, loginWithRequest, markDraftIntroSeen, wang } from './helpers'

const WIDE = { width: 1440, height: 900 }

async function issueRequiredAgent(request: APIRequestContext): Promise<string> {
  const member = await wang()
  const logged = await loginWithRequest(request, member)
  expect(logged.status(), `王工登录失败：${await logged.text()}`).toBe(200)
  const issued = await request.post('/api/v1/me/agent-tokens', {
    data: { name: `分栏草稿-${Date.now()}`, draft_mode: 'required' },
  })
  expect(issued.status(), `签发 Agent 失败：${await issued.text()}`).toBe(201)
  await markDraftIntroSeen(request)
  return ((await issued.json()) as { token: string }).token
}

test('分栏视图切工单后回复编辑器是当前工单自己的内容', async ({ page, request }) => {
  await createTicket(request, { title: '分栏编辑器 A', assignee: '管理员' })
  const b = await createTicket(request, { title: '分栏编辑器 B', assignee: '管理员' })
  await page.setViewportSize(WIDE)
  await login(page, ADMIN)
  await page.goto('/tickets?status=open&assignee=me')

  const composer = page.locator('.composer textarea')
  await page.locator('.tcard', { hasText: '分栏编辑器 A' }).click()
  await expect(page.getByRole('heading', { name: '分栏编辑器 A' })).toBeVisible()
  await composer.fill('写给 A 的回复')
  await expect(composer).toHaveValue('写给 A 的回复')

  await page.locator('.tcard', { hasText: '分栏编辑器 B' }).click()
  await expect(page).toHaveURL(new RegExp(`/t/${b}`))
  await expect(page.getByRole('heading', { name: '分栏编辑器 B' })).toBeVisible()
  await expect(composer).toHaveValue('')

  // 切回 A：A 的未提交回复还在（由本机的未提交输入恢复），没有因为切单丢掉。
  await page.locator('.tcard', { hasText: '分栏编辑器 A' }).click()
  await expect(page.getByRole('heading', { name: '分栏编辑器 A' })).toBeVisible()
  await expect(composer).toHaveValue('写给 A 的回复')
})

test('Agent 改出新一版草稿后成员直接修改看到的是最新版', async ({ page, request }) => {
  const number = await createTicket(request, { title: '分栏草稿卡片', assignee: '王工' })
  const token = await issueRequiredAgent(request)
  const drafted = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: 'Agent 的第一版。' },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(drafted.status(), `Agent 回复失败：${await drafted.text()}`).toBe(202)
  const draft = ((await drafted.json()) as { draft: { id: string; version: number } }).draft

  await page.setViewportSize(WIDE)
  await login(page, await wang())
  await page.goto('/tickets?status=open&assignee=me')
  await page.locator('.tcard', { hasText: '分栏草稿卡片' }).click()
  const card = page.getByLabel('回复草稿')
  await expect(card).toContainText('第 1 版')
  await expect(card).toContainText('Agent 的第一版。')

  // 成员停留期间 Agent 改出第 2 版；成员打个标签会触发详情重新加载。
  const second = await request.put(`/api/v1/drafts/${draft.id}`, {
    data: { base_version: draft.version, request: { body_md: 'Agent 的第二版。' } },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(second.ok(), `Agent 改草稿失败：${await second.text()}`).toBeTruthy()
  await page.getByRole('region', { name: '工单操作' }).getByRole('button', { name: '🏷 标签' }).click()
  await page.getByLabel('标签输入').fill('待初筛')
  await page.getByLabel('标签输入').press('Enter')
  await expect(page.getByRole('dialog', { name: `打标签 · ${number}` })).toContainText('待初筛')
  await page.getByRole('button', { name: '完成' }).click()
  await expect(card).toContainText('第 2 版')

  await page.getByRole('button', { name: '直接修改' }).click()
  await expect(page.getByLabel('草稿内容')).toHaveValue('Agent 的第二版。')
  await page.getByRole('button', { name: '保存草稿' }).click()
  await expect(card).toContainText('第 3 版')
  await expect(card).toContainText('Agent 的第二版。')
  await expect(card).not.toContainText('Agent 的第一版。')
})

test('分栏视图里浏览器后退换工单时打开的弹窗随之关闭', async ({ page, request }) => {
  const b = await createTicket(request, { title: '分栏弹窗 B', assignee: '管理员' })
  const a = await createTicket(request, { title: '分栏弹窗 A', assignee: '管理员' })
  await page.setViewportSize(WIDE)
  await login(page, ADMIN)
  await page.goto('/tickets?status=open&assignee=me')
  await page.locator('.tcard', { hasText: '分栏弹窗 B' }).click()
  await expect(page.getByRole('heading', { name: '分栏弹窗 B' })).toBeVisible()
  await page.locator('.tcard', { hasText: '分栏弹窗 A' }).click()
  await expect(page.getByRole('heading', { name: '分栏弹窗 A' })).toBeVisible()

  await page.getByRole('button', { name: '✔ 关闭' }).click()
  const dialog = page.getByRole('dialog', { name: `关闭工单 · ${a}` })
  await expect(dialog).toBeVisible()

  // 后退只换地址里的工单号：弹窗不能留着落到 B 上。弹窗标题带实时工单号，
  // 换单后会变成「关闭工单 · B」，所以按「关闭工单」匹配而不是按 A 的标题。
  await page.goBack()
  await expect(page).toHaveURL(new RegExp(`/t/${b}`))
  await expect(page.getByRole('heading', { name: '分栏弹窗 B' })).toBeVisible()
  await expect(page.getByRole('dialog', { name: /关闭工单/ })).toHaveCount(0)
  await expect(page.getByRole('button', { name: '✔ 关闭' })).toBeVisible()
})
