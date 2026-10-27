import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, login, wang } from './helpers'

const CAPTCHA_ANSWER = 'TB123'

async function wrongLogin(request: APIRequestContext, username: string, password: string) {
  const captcha = await request.get('/api/v1/login-captcha')
  expect(captcha.ok()).toBeTruthy()
  const { id } = (await captcha.json()) as { id: string }
  return request.post('/api/v1/session', {
    data: { username, password, captcha_id: id, captcha_answer: CAPTCHA_ANSWER },
  })
}

test('登录页显示锁定提示且管理员可解锁', async ({ page, request }) => {
  const member = await wang()
  for (let i = 0; i < 10; i++) {
    const res = await wrongLogin(request, member.username, 'wrong-pass-10')
    expect(res.status(), `第 ${i + 1} 次失败`).toBe(i === 9 ? 403 : 401)
  }

  await page.goto('/login')
  await page.getByLabel('用户名').fill(member.username)
  await page.getByLabel('密码').fill(member.password)
  await page.getByLabel('验证码').fill(CAPTCHA_ANSWER)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page.getByRole('alert')).toContainText('已锁定，约 15 分钟后再试或联系管理员')

  await login(page, ADMIN)
  await page.goto('/admin')
  const row = page.locator('tr[data-lock="wang"]')
  await expect(row).toContainText('wang')
  await expect(row).toContainText('127.0.0.1')
  await expect(row).toContainText('15 分钟')
  await row.getByRole('button', { name: '解锁' }).click()
  await expect(row).toHaveCount(0)

  await page.context().clearCookies({ name: 'tb_session' })
  await page.goto('/login')
  await page.getByLabel('用户名').fill(member.username)
  await page.getByLabel('密码').fill(member.password)
  await page.getByLabel('验证码').fill(CAPTCHA_ANSWER)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page).toHaveURL((url) => !url.pathname.endsWith('/login'))
})
