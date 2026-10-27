// 新版本提示（#262）：主屏应用切回前台时页面不重新加载，用 health 的
// web_version 与页面加载时的基准比较。这里用 page.route 改写 health 响应模拟
// 版本变化，再派发 visibilitychange 验证横幅与底部版本号的行为。
import { expect, test, type Page } from '@playwright/test'
import { ADMIN, login } from './helpers'

async function mockHealth(page: Page, server: string, web: string) {
  await page.route('**/api/v1/health', (route) =>
    route.fulfill({ json: { status: 'ok', server_version: server, web_version: web } }),
  )
}

async function becomeVisible(page: Page) {
  await page.evaluate(() => {
    Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
    document.dispatchEvent(new Event('visibilitychange'))
  })
}

test('web_version 变化时提示新版本：关闭后再触发仍出现，点击后刷新', async ({ page }) => {
  await mockHealth(page, '1.0.0', '20261027')
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)

  // 底部同时显示服务器版本和当前加载的网页版本。
  const footer = page.locator('.pc-side-foot small')
  await expect(footer).toHaveText('服务器 v1.0.0 · 网页 v20261027')

  // 服务器上换了新网页版本：切回前台后出现横幅。
  await mockHealth(page, '1.0.0', '20261028')
  await becomeVisible(page)
  const banner = page.locator('.version-banner')
  await expect(banner).toBeVisible()
  await expect(banner).toContainText('已有新版本，点此刷新')

  // 关闭后横幅消失；再切回前台仍检测到新版本时再次出现。
  await page.getByRole('button', { name: '关闭新版本提示' }).click()
  await expect(banner).toBeHidden()
  await becomeVisible(page)
  await expect(banner).toBeVisible()

  // 点击刷新：页面重新加载，基准变成新版本，横幅不再出现。
  await page.getByRole('button', { name: '已有新版本，点此刷新' }).click()
  await expect(footer).toHaveText('服务器 v1.0.0 · 网页 v20261028')
  await expect(banner).toHaveCount(0)
})

test('只有 server_version 变化时不提示，底部服务器版本原地上移', async ({ page }) => {
  await mockHealth(page, '1.0.0', '20261027')
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)

  const footer = page.locator('.pc-side-foot small')
  await expect(footer).toHaveText('服务器 v1.0.0 · 网页 v20261027')

  await mockHealth(page, '1.1.0', '20261027')
  await becomeVisible(page)
  await expect(footer).toHaveText('服务器 v1.1.0 · 网页 v20261027')
  await expect(page.locator('.version-banner')).toHaveCount(0)
})

test('「我的」页底部同时显示服务器与网页版本', async ({ page }) => {
  await mockHealth(page, '1.0.0', '20261027')
  await login(page, ADMIN)
  await page.goto('/me')
  await expect(page.getByTestId('member-version')).toHaveText('服务器 v1.0.0 · 网页 v20261027')
})
