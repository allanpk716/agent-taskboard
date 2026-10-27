import { expect, test } from '@playwright/test'
import { ADMIN, createTicket, login, wang } from './helpers'

test('wide viewport shows sidebar, version and centered main area', async ({ page, request }) => {
  const health = await (await request.get('/api/v1/health')).json()
  await createTicket(request, { title: '宽屏侧栏角标的工单', assignee: '管理员' })
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)

  const sidebar = page.getByRole('navigation', { name: '侧栏' })
  await expect(sidebar).toBeVisible()
  const nav = sidebar.getByRole('link').filter({ hasNotText: '工单板' })
  await expect(nav.nth(1)).toHaveText(/^📥收件箱[1-9]\d*$/)
  expect(await nav.evaluateAll((els) => els.map((e) => e.textContent))).toEqual([
    expect.stringMatching(/^✚＋ 建单$/),
    expect.stringMatching(/^📥收件箱[1-9]\d*$/),
    '✎✎ 草稿',
    '🗂工单',
    '🧰工具',
    '👤我的',
    '🛡管理',
  ])
  await expect(sidebar.getByRole('link', { name: '请人反馈' })).toHaveCount(0)
  await expect(sidebar.getByRole('link', { name: '建单（高级）' })).toHaveCount(0)
  await expect(sidebar).toContainText('管理员 · 管理员')
  await expect(sidebar).toContainText(`服务器 v${health.server_version}`)

  await sidebar.getByRole('link', { name: '工具' }).click()
  await expect(page).toHaveURL('/tools')
  await expect(page.getByText('标签字典与别名')).toBeVisible()
  const main = await page.getByRole('main').boundingBox()
  const centered = await page.locator('.pc-centered').boundingBox()
  expect(main!.width).toBeGreaterThan(900)
  expect(centered!.width).toBeLessThanOrEqual(960)

  await page.setViewportSize({ width: 900, height: 800 })
  await expect(page).toHaveURL('/tools')
  await expect(page.getByText('标签字典与别名')).toBeVisible()
  await expect(page.getByRole('navigation', { name: '侧栏' })).toBeVisible()
})

test('medium viewport shows the icon rail', async ({ page }) => {
  await page.setViewportSize({ width: 1100, height: 800 })
  await login(page, ADMIN)

  const sidebar = page.getByRole('navigation', { name: '侧栏' })
  await expect(sidebar).toBeVisible()
  await expect(sidebar.locator('.pc-nav-item.primary')).toContainText('建单')
  for (const name of ['收件箱', '工单', '工具', '我的', '管理']) {
    await expect(sidebar.locator('a').filter({ hasText: name }).first()).toBeVisible()
  }
  await expect(sidebar.getByRole('link', { name: '请人反馈' })).toHaveCount(0)
  await expect(sidebar.getByRole('link', { name: '建单（高级）' })).toHaveCount(0)
  await expect(sidebar).toContainText('服务器 v')
  await expect(page.getByRole('banner')).toHaveCount(0)
})

test('mobile keeps the phone shell and shows version on my page', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const member = await wang()
  await login(page, member)

  await expect(page.getByRole('heading', { name: '收件箱' })).toBeVisible()
  const tabs = page.getByRole('navigation', { name: '主导航' })
  await expect(tabs).toBeVisible()
  await expect(tabs.getByRole('link', { name: '管理' })).toHaveCount(0)
  await expect(tabs.getByRole('link', { name: '＋ 建单' })).toContainText('建单')
  await expect(tabs.getByText('请人反馈')).toHaveCount(0)
  await expect(tabs.getByText('建单（高级）')).toHaveCount(0)

  await tabs.getByRole('link', { name: '我的' }).click()
  await expect(page).toHaveURL('/me')
  await expect(page.getByTestId('member-version')).toContainText(/^服务器 v\d+\.\d+\.\d+(\s·\s网页 v.+)?$/)
  await expect(page.getByRole('navigation', { name: '侧栏' })).toHaveCount(0)
})
