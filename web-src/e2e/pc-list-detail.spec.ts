import { expect, test } from '@playwright/test'
import { ADMIN, createTicket, login } from './helpers'

test('wide viewport keeps the filtered list while switching detail rows', async ({ page, request }) => {
  const first = await createTicket(request, { title: '宽屏列表的第一张工单', assignee: '管理员' })
  const second = await createTicket(request, {
    title: '宽屏列表的第二张工单',
    labels: ['机型:US-1680'],
    assignee: '管理员',
  })
  for (let i = 0; i < 8; i++) {
    await createTicket(request, { title: `宽屏列表滚动填充 ${i}`, assignee: '管理员' })
  }
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)
  await page.goto('/tickets')
  await page.getByRole('button', { name: '指派给我' }).click()
  await expect(page).toHaveURL(/assignee=me/)
  await expect(page.locator('.tcard', { hasText: second })).toBeVisible()

  await page.locator('.tcard', { hasText: first }).click()
  await expect(page).toHaveURL(new RegExp(`/t/${first}\\?status=open&assignee=me$`))
  await expect(page.getByRole('heading', { name: '宽屏列表的第一张工单' })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: second })).toBeVisible()

  const listScroll = page.getByTestId('ticket-list-scroll')
  await listScroll.evaluate((el) => (el.scrollTop = 120))
  const scrollBeforeClick = await listScroll.evaluate((el) => el.scrollTop)
  await page.locator('.tcard', { hasText: second }).evaluate((el) => (el as HTMLElement).click())
  await expect(page).toHaveURL(new RegExp(`/t/${second}\\?status=open&assignee=me$`))
  expect(await listScroll.evaluate((el) => el.scrollTop)).toBe(scrollBeforeClick)

  await page.goBack()
  await expect(page.getByRole('heading', { name: '宽屏列表的第一张工单' })).toBeVisible()
  await page.goForward()
  await expect(page.getByRole('heading', { name: '宽屏列表的第二张工单' })).toBeVisible({ timeout: 15_000 })
})

test('detail URLs restore filters and remember an inbox source', async ({ page, request }) => {
  const ticket = await createTicket(request, {
    title: '可转发的筛选工单',
    labels: ['机型:US-1680'],
    assignee: '王工',
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)
  await page.goto(`/t/${ticket}?status=open&q=${encodeURIComponent('可转发的筛选工单')}`)
  await expect(page.getByRole('heading', { name: '可转发的筛选工单' })).toBeVisible()
  await expect(page.getByTestId('ticket-list-pane')).toContainText('工单')
  await expect(page.getByRole('button', { name: '指派给我' })).toBeVisible()
  await page.reload()
  await expect(page.getByRole('button', { name: '指派给我' })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: ticket })).toHaveCount(1)

  const inboxTicket = await createTicket(request, { title: '从收件箱打开的工单', assignee: '管理员' })
  await page.goto('/')
  await page.locator('.tcard', { hasText: inboxTicket }).click()
  await expect(page).toHaveURL(new RegExp(`/t/${inboxTicket}\\?list=inbox$`))
  await expect(page.getByTestId('inbox-list-pane')).toContainText('从收件箱打开的工单')
  await expect(page.getByTestId('inbox-list-pane').locator('.tcard', { hasText: ticket })).toHaveCount(0)
  await expect(page.getByTestId('inbox-list-pane')).toContainText('等我处理')
})

test('narrow viewport shows one pane and restores filters on back', async ({ page, request }) => {
  const ticket = await createTicket(request, { title: '窄屏筛选的工单', labels: ['机型:US-1680'] })
  await page.setViewportSize({ width: 900, height: 800 })
  await login(page, ADMIN)
  await page.goto('/tickets')
  await page.getByRole('button', { name: '已关闭' }).click()
  await page.getByRole('button', { name: '进行中' }).click()
  await expect(page.locator('.tcard', { hasText: ticket })).toBeVisible()
  await page.locator('.tcard', { hasText: ticket }).evaluate((el) => (el as HTMLElement).click())
  await expect(page.getByTestId('ticket-list-pane')).toHaveCount(0)
  await expect(page.getByRole('heading', { name: '窄屏筛选的工单' })).toBeVisible()
  await page.goBack()
  await expect(page).toHaveURL(/\/tickets\?status=open/)
  await expect(page.locator('.tcard', { hasText: ticket })).toBeVisible()
})

test('medium viewport also shows list and detail together', async ({ page, request }) => {
  const ticket = await createTicket(request, { title: '中屏同屏的工单' })
  await page.setViewportSize({ width: 1100, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/t/${ticket}?status=open`)
  await expect(page.getByTestId('ticket-list-pane')).toBeVisible()
  await expect(page.getByRole('heading', { name: '中屏同屏的工单' })).toBeVisible()
  const list = page.getByTestId('ticket-list-pane')
  expect(await list.evaluate((el) => getComputedStyle(el).width)).toBe('320px')
})

test('mobile keeps detail alone and returns to the filtered list', async ({ page, request }) => {
  const ticket = await createTicket(request, { title: '手机筛选的工单' })
  await page.setViewportSize({ width: 390, height: 844 })
  await login(page, ADMIN)
  await page.goto('/tickets')
  await page.locator('.tcard', { hasText: ticket }).click()
  await expect(page).toHaveURL(new RegExp(`/t/${ticket}\\?status=open$`))
  await expect(page.getByRole('heading', { name: '手机筛选的工单' })).toBeVisible()
  await expect(page.locator('.b-title')).toHaveText('工单')
  await page.goBack()
  await expect(page.locator('.tcard', { hasText: ticket })).toBeVisible()
})

test('detail writes refresh the selected row in the wide list', async ({ page, request }) => {
  const ticket = await createTicket(request, { title: '写操作后要刷新的行', labels: ['机型:UD-2300'] })
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)
  await page.goto(`/t/${ticket}?status=open`)
  const row = page.locator('.tcard', { hasText: ticket })
  await expect(row).toContainText('机型:UD-2300')

  await page.getByRole('button', { name: '🏷 标签', exact: true }).click()
  await page.getByLabel('标签输入').fill('机型:US-1680')
  await page.getByRole('button', { name: '添加', exact: true }).click()
  await expect(page.locator('.modal .chip', { hasText: '机型:US-1680' })).toBeVisible()
  await page.getByRole('button', { name: '完成' }).click()
  await expect(row.locator('.chip', { hasText: '机型:US-1680' })).toBeVisible()
})
