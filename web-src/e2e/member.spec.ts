import { test, expect, type APIRequestContext, type Page } from '@playwright/test'

// 成员网页（#41）：收件箱、列表、详情、建单。对真实服务 + 构建好的前端跑用例，
// 造数据（成员、工单、回复、访客链接）全部通过 HTTP API 完成；成员由
// e2e/start-server.mjs 预先创建（admin / 管理员 与 wang / 王工）。

export const ADMIN = { username: 'admin', password: 'admin-pass-123' }
export const WANG = { username: 'wang', password: 'wang-pass-123' }

export async function login(page: Page, who: { username: string; password: string }) {
  await page.goto('/login')
  await page.getByLabel('用户名').fill(who.username)
  await page.getByLabel('密码').fill(who.password)
  await page.getByRole('button', { name: '登录' }).click()
  await page.waitForURL((u) => !u.pathname.endsWith('/login'))
}

export async function loginAdmin(request: APIRequestContext) {
  const res = await request.post('/api/v1/session', { data: ADMIN })
  expect(res.ok(), '管理员登录失败').toBeTruthy()
}

/** 用管理员会话建一张自由格式工单（可指派），返回编号。 */
export async function createTicket(
  request: APIRequestContext,
  opts: { title: string; labels?: string[]; assignee?: string } = { title: '测试工单' },
): Promise<string> {
  await loginAdmin(request)
  const res = await request.post('/api/v1/tickets', {
    data: {
      title: opts.title,
      brief_md: `${opts.title} 的说明。`,
      labels: opts.labels ?? ['机型:UD-2300'],
      ...(opts.assignee ? { assignee: opts.assignee } : {}),
    },
  })
  expect(res.status(), `建单失败：${await res.text()}`).toBe(201)
  return (await res.json()).number
}

// 1. 登录后进入成员网页：顶栏、底部标签栏（收件箱/工单/＋建单/工具/我的），
//    收件箱按原因显示卡片（新指派给你）。
test('登录后进入成员网页，收件箱按原因显示卡片', async ({ page, request }) => {
  const number = await createTicket(request, { title: '收件箱原因卡片的工单', assignee: '管理员' })
  await login(page, ADMIN)

  // 顶栏 + 底部标签栏
  await expect(page.getByRole('heading', { name: '收件箱' })).toBeVisible()
  await expect(page.locator('.b-me')).toContainText('管理员')
  for (const tab of ['收件箱', '工单', '建单', '工具', '我的']) {
    await expect(page.locator('.b-tabs').getByRole('link', { name: tab })).toBeVisible()
  }

  // 卡片：原因色条 + 编号 + 标题；点击进入详情
  const card = page.locator('.tcard', { hasText: number })
  await expect(card).toBeVisible()
  await expect(card.locator('.badge.reason')).toHaveText('新指派给你')
  await expect(card).toContainText('收件箱原因卡片的工单')
  await card.click()
  await expect(page).toHaveURL(new RegExp(`/t/${number}$`))
})

// 2. 工单列表：按状态、指派、操作方（本人/Agent）筛选，按关键词搜索。
test('工单列表按状态、指派与本人/Agent筛选，支持关键词', async ({ page, request }) => {
  // 造三张：A 成员建的开启未指派；B 成员 Agent 建的开启指派给我；C 已关闭。
  const a = await createTicket(request, { title: '成员本人创建的工单', labels: ['机型:UD-2300'] })
  const b = await createTicketViaAgent(request, '成员 Agent 创建的工单', '管理员')
  const c = await createTicket(request, { title: '随后被关闭的工单', labels: ['机型:US-1680'] })
  const closed = await request.post(`/api/v1/tickets/${c}/close`, { data: { reason: 'done' } })
  expect(closed.ok()).toBeTruthy()

  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '工单' }).click()
  const grp = (name: string) => page.getByRole('group', { name })

  // 默认开启：A、B 在，C 不在；卡片带编号、标题、发起人、指派、标签
  const cardA = page.locator('.tcard', { hasText: a })
  const cardB = page.locator('.tcard', { hasText: b })
  await expect(cardA).toBeVisible()
  await expect(cardB).toBeVisible()
  await expect(page.locator('.tcard', { hasText: c })).toHaveCount(0)
  await expect(cardB).toContainText('指派 管理员')
  await expect(cardA).toContainText('未指派')
  await expect(cardA.locator('.chip')).toContainText('机型:UD-2300')

  // 状态 = 已关闭：只剩 C
  await grp('状态').getByRole('button', { name: '已关闭' }).click()
  await expect(page.locator('.tcard', { hasText: c })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: c }).locator('.badge.closed')).toContainText('完成')
  await expect(page.locator('.tcard', { hasText: a })).toHaveCount(0)
  await grp('状态').getByRole('button', { name: '开启' }).click()

  // 操作方 = Agent：只剩 B（创建者署名是成员 Agent）；本人只剩 A
  await grp('操作方').getByRole('button', { name: 'Agent' }).click()
  await expect(page.locator('.tcard', { hasText: b })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: a })).toHaveCount(0)
  await grp('操作方').getByRole('button', { name: '本人' }).click()
  await expect(page.locator('.tcard', { hasText: a })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: b })).toHaveCount(0)
  await grp('操作方').getByRole('button', { name: '全部' }).click()

  // 指派 = 指派给我：只剩 B
  await grp('指派').getByRole('button', { name: '指派给我' }).click()
  await expect(page.locator('.tcard', { hasText: b })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: a })).toHaveCount(0)
  await grp('指派').getByRole('button', { name: '全部' }).click()

  // 关键词：状态切回全部后，只命中标题含关键词的那张
  await grp('状态').getByRole('button', { name: '全部' }).click()
  await page.getByPlaceholder(/搜索/).fill('随后被关闭')
  await page.getByPlaceholder(/搜索/).press('Enter')
  await expect(page.locator('.tcard', { hasText: c })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: a })).toHaveCount(0)
})

/** 用管理员的成员 Agent token 建单（创建者署名是 Agent，actor_kind=agent 命中）。 */
async function createTicketViaAgent(
  request: APIRequestContext,
  title: string,
  assignee?: string,
): Promise<string> {
  await loginAdmin(request)
  const tokenRes = await request.post('/api/v1/me/agent-tokens', { data: { name: `agent-${title}` } })
  expect(tokenRes.ok()).toBeTruthy()
  const token: string = (await tokenRes.json()).token
  const res = await request.post('/api/v1/tickets', {
    data: {
      title,
      brief_md: `${title} 的说明。`,
      labels: ['机型:SL-1000'],
      ...(assignee ? { assignee } : {}),
    },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(res.status(), `Agent 建单失败：${await res.text()}`).toBe(201)
  return (await res.json()).number
}
