import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, login } from './helpers'

const RUN = Date.now().toString(36)

async function createMemberWithInitialPassword(
  request: APIRequestContext,
): Promise<{ username: string; password: string }> {
  const loginRes = await request.post('/api/v1/session', { data: ADMIN })
  expect(loginRes.ok()).toBeTruthy()
  const username = `pw${RUN}`
  const res = await request.post('/api/v1/members', { data: { username, name: '改密成员' } })
  expect(res.status(), await res.text()).toBe(201)
  const password = ((await res.json()) as { initial_password: string }).initial_password
  return { username, password }
}

test('我的页面自助修改密码', async ({ page, request }) => {
  const who = await createMemberWithInitialPassword(request)
  const newPassword = `${who.password}-new`
  await login(page, who)
  await page.locator('.b-tabs').getByRole('link', { name: '我的' }).click()

  await page.getByRole('button', { name: '修改密码' }).click()
  await page.getByLabel('旧密码').fill(who.password)
  await page.getByLabel('新密码', { exact: true }).fill(newPassword)
  await page.getByLabel('确认新密码').fill(`${newPassword}-different`)
  await page.getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByText('两次输入的新密码不一致')).toBeVisible()

  await page.getByLabel('确认新密码').fill(newPassword)
  await page.getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByText('已修改，其他浏览器上的登录已退出')).toBeVisible()
  await page.getByRole('button', { name: '完成', exact: true }).click()

  await page.locator('.btnrow').getByRole('button', { name: '登出' }).click()
  await page.waitForURL((u) => u.pathname.endsWith('/login'))
  await login(page, { username: who.username, password: newPassword })
  await expect(page.locator('.b-me')).toContainText('改密成员')
})
