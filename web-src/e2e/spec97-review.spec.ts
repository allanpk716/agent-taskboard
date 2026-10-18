import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, createTicket, login } from './helpers'

async function createGuestReplyTicket(
  request: APIRequestContext,
  title: string,
  reply: string,
): Promise<string> {
  const number = await createTicket(request, { title, assignee: '管理员' })
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '检验科-张三', valid_days: 30 },
  })
  expect(link.ok(), `创建访客链接失败：${await link.text()}`).toBeTruthy()
  const key = String((await link.json()).url).slice(3)
  const sent = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: reply },
    headers: { Authorization: `Bearer ${key}` },
  })
  expect(sent.ok(), `访客回复失败：${await sent.text()}`).toBeTruthy()
  return number
}

test('mobile guest link dialog loads the existing list', async ({ page, request }) => {
  const number = await createTicket(request, { title: '手机访客链接的工单' })
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '检验科-李四', valid_days: 7 },
  })
  expect(link.ok()).toBeTruthy()

  await page.setViewportSize({ width: 390, height: 844 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}?status=open`)
  await page.getByRole('button', { name: '访客链接' }).click()
  await expect(page.locator('.modal')).toContainText('检验科-李四')
  await expect(page.locator('.modal')).not.toContainText('加载中…')
})

test('wide list keeps loaded rows and scroll across detail history', async ({ page, request }) => {
  const prefix = `稳定列表${Date.now()}`
  await createTicket(request, { title: `${prefix} 第一张` })
  await createTicket(request, { title: `${prefix} 第二张` })
  for (let i = 0; i < 54; i++) {
    await createTicket(request, { title: `${prefix} 填充 ${i}` })
  }

  await page.setViewportSize({ width: 1440, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/tickets?q=${encodeURIComponent(prefix)}`)
  await page.getByRole('button', { name: '加载更多' }).click()
  await expect(page.locator('.tcard', { hasText: `${prefix} 填充 50` })).toBeVisible()
  const rows = page.locator('.tcard')
  const firstNumber = (await rows.nth(0).getAttribute('href'))?.split('/')[2]?.split('?')[0]
  const secondNumber = (await rows.nth(1).getAttribute('href'))?.split('/')[2]?.split('?')[0]
  if (!firstNumber || !secondNumber) throw new Error('列表行缺少详情地址')
  const scroll = page.getByTestId('ticket-list-scroll')
  await scroll.evaluate((el) => (el.scrollTop = 180))

  await rows.nth(0).evaluate((el) => (el as HTMLElement).click())
  await expect(page).toHaveURL(new RegExp(`/t/${firstNumber}`))
  await expect(page.locator('.tcard', { hasText: `${prefix} 填充 50` })).toBeVisible()
  expect(await scroll.evaluate((el) => el.scrollTop)).toBe(180)

  await page.goBack()
  await expect(page.locator('.tcard', { hasText: `${prefix} 填充 50` })).toBeVisible()
  expect(await scroll.evaluate((el) => el.scrollTop)).toBe(180)
  await rows.nth(1).evaluate((el) => (el as HTMLElement).click())
  await expect(page).toHaveURL(new RegExp(`/t/${secondNumber}`))
  await expect(page.locator('.tcard', { hasText: `${prefix} 填充 50` })).toBeVisible()
  expect(await scroll.evaluate((el) => el.scrollTop)).toBe(180)
})

test('wide detail highlights tickets rather than inbox', async ({ page, request }) => {
  const number = await createTicket(request, { title: '侧栏高亮的工单' })
  await page.setViewportSize({ width: 1440, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}?status=open`)
  const sidebar = page.getByRole('navigation', { name: '侧栏' })
  await expect(sidebar.getByRole('link', { name: '工单', exact: true })).toHaveAttribute(
    'aria-current',
    'page',
  )
  await expect(sidebar.getByRole('link', { name: '收件箱', exact: true })).not.toHaveAttribute(
    'aria-current',
    'page',
  )
})

test('wide detail header omits the mobile metadata block', async ({ page, request }) => {
  const number = await createTicket(request, { title: '宽屏头部精简的工单' })
  await page.setViewportSize({ width: 1440, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}?status=open`)
  await expect(page.locator('.mmeta')).toHaveCount(0)
})

test('more filters label input preserves panel labels', async ({ page, request }) => {
  const number = await createTicket(request, { title: '标签输入合并的工单', labels: ['机型:UD-2300', '待初筛'] })
  await page.setViewportSize({ width: 1440, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/tickets?status=open&label=${encodeURIComponent('待初筛')}`)
  await page.getByRole('button', { name: '更多筛选 ▾' }).click()
  await page.getByLabel('标签', { exact: true }).fill('机型:UD-2300')
  await page.getByLabel('标签', { exact: true }).blur()
  await expect(page.getByLabel('已选标签条件')).toContainText('待初筛 ×')
  await expect(page.getByLabel('已选标签条件')).toContainText('机型:UD-2300 ×')
  await expect(page.locator('.tcard', { hasText: number })).toBeVisible()
})

test('facet counts load per expanded group only', async ({ page, request }) => {
  await createTicket(request, { title: '分面懒加载的工单', labels: ['机型:UD-2300'] })
  await page.setViewportSize({ width: 1440, height: 800 })
  const requests: string[] = []
  page.on('request', (req) => {
    if (req.url().includes('/api/v1/tickets/counts?')) requests.push(req.url())
  })
  await login(page, ADMIN)
  await page.goto('/tickets')
  await page.getByRole('button', { name: '🏷 标签 ▾' }).click()
  await page.waitForTimeout(300)
  expect(requests).toHaveLength(0)

  await page
    .getByTestId('label-facet-panel')
    .locator('.label-facet-group', { hasText: '机型' })
    .getByRole('button', { name: /机型/ })
    .click()
  await expect(page.getByRole('checkbox', { name: 'UD-2300' })).toBeVisible()
  expect(requests.filter((url) => url.includes('group_by=%E6%9C%BA%E5%9E%8B'))).toHaveLength(1)
})

test('label chips fold only after an actual line overflows', async ({ page, request }) => {
  const number = await createTicket(request, {
    title: '短标签一行的工单',
    labels: ['机型:UD-2300'],
  })
  for (const label of ['甲', '乙', '丙', '丁']) {
    const res = await request.post(`/api/v1/tickets/${number}/labels`, {
      data: { add: [label], allow_new: true },
    })
    expect(res.ok(), `加标签 ${label} 失败：${await res.text()}`).toBeTruthy()
  }
  await page.setViewportSize({ width: 1440, height: 800 })
  await login(page, ADMIN)
  await page.goto('/tickets?status=open&label=甲&label=乙&label=丙&label=丁')
  const summary = page.getByLabel('已选标签条件')
  await expect(summary).toBeVisible()
  await expect(summary.getByRole('button', { name: /甲/u })).toBeVisible()
  await expect(summary.getByRole('button', { name: /丁/u })).toBeVisible()
  await expect(summary.getByRole('button', { name: /\+\d+/ })).toHaveCount(0)
})

test('inbox filters stay in the URL and in detail source links', async ({ page, request }) => {
  const guest = await createGuestReplyTicket(request, '收件箱筛选的访客回复', '访客已补充信息')
  const mine = await createTicket(request, { title: '收件箱筛选的新指派', assignee: '管理员' })

  await page.setViewportSize({ width: 1440, height: 800 })
  await login(page, ADMIN)
  await page.goto('/')
  await page.getByRole('button', { name: '访客新回复' }).click()
  await page.getByLabel('搜索').fill('访客回复')
  await page.getByLabel('搜索').press('Enter')
  await expect(page).toHaveURL(/inbox=guest/)
  await expect(page).toHaveURL(/q=/)
  await expect(page.locator('.tcard', { hasText: guest })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: mine })).toHaveCount(0)

  await page.locator('.tcard', { hasText: guest }).click()
  await expect(page).toHaveURL(new RegExp(`/t/${guest}\\?`))
  await expect(page).toHaveURL(/[?&]list=inbox/)
  await expect(page).toHaveURL(/[?&]inbox=guest/)
  await expect(page.getByTestId('inbox-list-pane').locator('.tcard', { hasText: mine })).toHaveCount(0)
})

test('mine inbox chip shows every turn and wide filter row has no feedback button', async ({
  page,
  request,
}) => {
  const guest = await createGuestReplyTicket(request, '等我处理的访客回复', '访客又回复了')
  const mine = await createTicket(request, { title: '等我处理的新指派', assignee: '管理员' })
  await page.setViewportSize({ width: 1440, height: 800 })
  await login(page, ADMIN)
  await page.goto('/?inbox=mine')
  await expect(page.locator('.tcard', { hasText: mine })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: guest })).toBeVisible()
  await expect(page.getByTestId('inbox-list-pane').getByRole('button', { name: '＋ 请人反馈' })).toHaveCount(0)
})

test('ticket rows do not claim unread state without list data', async ({ page, request }) => {
  await createTicket(request, { title: '没有未读数据的工单' })
  await page.setViewportSize({ width: 1440, height: 800 })
  await login(page, ADMIN)
  await page.goto('/tickets')
  await expect(page.locator('.unread-dot')).toHaveCount(0)
})
