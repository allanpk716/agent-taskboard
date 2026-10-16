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
