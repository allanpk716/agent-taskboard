import { test, expect, type Page } from '@playwright/test'

const ADMIN = { username: 'admin', password: 'admin-pass-123' }

async function login(page: Page) {
  await page.goto('/login')
  await page.getByLabel('用户名').fill(ADMIN.username)
  await page.getByLabel('密码').fill(ADMIN.password)
  await page.getByRole('button', { name: '登录' }).click()
}

// 冒烟：登录后看到空壳页面。
test('登录后看到空壳页面', async ({ page }) => {
  await login(page)
  await expect(page.getByText('这里是空壳页面：收件箱、工单列表等将在后续版本加入。')).toBeVisible()
  await expect(page.getByText('管理员（admin）')).toBeVisible()
})

test('账号或密码错误时显示统一错误', async ({ page }) => {
  await page.goto('/login')
  await page.getByLabel('用户名').fill(ADMIN.username)
  await page.getByLabel('密码').fill('wrong-pass')
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page.getByRole('alert')).toHaveText(/用户名或密码不正确/)
  // 仍停留在登录页
  await expect(page).toHaveURL(/\/login$/)
})

test('登出后回到登录页，再访问首页被送回登录', async ({ page }) => {
  await login(page)
  await expect(page.getByText('这里是空壳页面：收件箱、工单列表等将在后续版本加入。')).toBeVisible()
  await page.getByRole('button', { name: '登出' }).click()
  await expect(page).toHaveURL(/\/login$/)

  // 未登录直接访问首页：被送到登录页。
  await page.goto('/')
  await expect(page).toHaveURL(/\/login$/)
})
