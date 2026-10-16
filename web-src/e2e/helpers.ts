import type { APIRequestContext, Page } from '@playwright/test'
import { expect } from '@playwright/test'

// e2e 共享的账号与登录辅助（由 start-server.mjs 预先创建）。
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
