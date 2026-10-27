import { test, expect, type Page } from '@playwright/test'

const ADMIN = { username: 'admin', password: 'admin-pass-123' }
const CAPTCHA_ANSWER = 'TB123'

async function login(page: Page) {
  await page.goto('/login')
  await page.getByLabel('用户名').fill(ADMIN.username)
  await page.getByLabel('密码').fill(ADMIN.password)
  await page.getByLabel('验证码').fill(CAPTCHA_ANSWER)
  await page.getByRole('button', { name: '登录' }).click()
}

// 冒烟：登录后进入成员网页（#41：收件箱是首页）。
test('登录后进入成员网页的收件箱', async ({ page }) => {
  await login(page)
  await expect(page.getByRole('heading', { name: '收件箱' })).toBeVisible()
  await expect(page.locator('.b-me')).toContainText('管理员')
  for (const tab of ['收件箱', '工单', '建单', '工具', '我的']) {
    await expect(page.locator('.b-tabs').getByRole('link', { name: tab })).toBeVisible()
  }
})

// iOS 主屏应用常以添加时所在的 /login 为启动地址：已登录再打开登录页应直接进站内。
test('已登录时打开登录页直接跳回首页', async ({ page }) => {
  await login(page)
  await expect(page.getByRole('heading', { name: '收件箱' })).toBeVisible()
  await page.goto('/login')
  await expect(page).toHaveURL((u) => u.pathname === '/')
  await expect(page.getByRole('heading', { name: '收件箱' })).toBeVisible()
})

test('登录页显示验证码，答错后提示并自动换一张', async ({ page }) => {
  await page.goto('/login')
  await expect(page.getByRole('img', { name: '验证码图片' })).toBeVisible()
  await page.getByRole('button', { name: '换一张' }).click()
  await expect(page.getByRole('img', { name: '验证码图片' })).toBeVisible()

  await page.getByLabel('用户名').fill(ADMIN.username)
  await page.getByLabel('密码').fill(ADMIN.password)
  await page.getByLabel('验证码').fill('wrong')
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page.getByRole('alert')).toHaveText(/验证码错误/)
  await page.getByLabel('验证码').fill(CAPTCHA_ANSWER)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page.getByRole('heading', { name: '收件箱' })).toBeVisible()
})

test('账号或密码错误时显示统一错误', async ({ page }) => {
  await page.goto('/login')
  await page.getByLabel('用户名').fill(ADMIN.username)
  await page.getByLabel('密码').fill('wrong-pass')
  await page.getByLabel('验证码').fill(CAPTCHA_ANSWER)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page.getByRole('alert')).toHaveText(/用户名或密码不正确/)
  // 仍停留在登录页
  await expect(page).toHaveURL(/\/login$/)
})

test('登出后回到登录页，再访问首页被送回登录', async ({ page }) => {
  await login(page)
  await expect(page.getByRole('heading', { name: '收件箱' })).toBeVisible()
  // 登出在「我的」页
  await page.locator('.b-tabs').getByRole('link', { name: '我的' }).click()
  await expect(page.getByRole('heading', { name: '我的 · 设置' })).toBeVisible()
  await page.getByRole('button', { name: '登出' }).click()
  await expect(page).toHaveURL(/\/login$/)

  // 未登录直接访问首页：被送到登录页。
  await page.goto('/')
  await expect(page).toHaveURL(/\/login$/)
})
