import { expect, test } from '@playwright/test'
import { ADMIN, createTicket, login } from './helpers'

test('clicking a list row label chip toggles the URL filter without opening the row', async ({
  page,
  request,
}) => {
  const matching = await createTicket(request, {
    title: '列表标签筛选命中的工单',
    labels: ['机型:UD-1290'],
  })
  await createTicket(request, { title: '列表标签筛选不命中的工单', labels: ['机型:UD-2300'] })

  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)
  await page.goto('/tickets')
  const matchingRow = page.locator('.tcard', { hasText: matching })
  const chip = matchingRow.locator('.chip', { hasText: '机型:UD-1290' })
  await expect(chip).toHaveAttribute('title', '点击按此筛选')

  await chip.click()
  await expect(page).toHaveURL(/\/tickets\?.*label_any=/u)
  await expect(page.locator('.tcard', { hasText: matching })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: '列表标签筛选不命中的工单' })).toHaveCount(0)
  await expect(page.getByTestId('ticket-list-pane')).toContainText('机型：UD-1290 ×')
  await expect(page.getByRole('heading', { name: '列表标签筛选命中的工单' })).toHaveCount(0)

  await chip.click()
  await expect(page).toHaveURL(/\/tickets\?status=open$/)
  await expect(page.locator('.tcard', { hasText: '列表标签筛选不命中的工单' })).toBeVisible()
})

test('clicking an info-column label chip updates the source list and detail URL', async ({
  page,
  request,
}) => {
  const matching = await createTicket(request, {
    title: '详情标签筛选命中的工单',
    labels: ['机型:UD-1290', '待初筛'],
  })
  await createTicket(request, { title: '详情标签筛选不命中的工单', labels: ['机型:UD-2300'] })

  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)
  await page.goto(`/t/${matching}?status=open`)
  await expect(page.getByRole('heading', { name: '详情标签筛选命中的工单' })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: '详情标签筛选不命中的工单' })).toBeVisible()

  const info = page.getByRole('region', { name: '工单信息', exact: true })
  const chip = info.locator('.chip', { hasText: '待初筛' })
  await expect(chip).toHaveAttribute('title', '点击按此筛选')
  await chip.click()
  await expect(page).toHaveURL(new RegExp(`/t/${matching}\\?status=open&label=`))
  await expect(page.locator('.tcard', { hasText: '详情标签筛选不命中的工单' })).toHaveCount(0)
  await expect(page.getByRole('heading', { name: '详情标签筛选命中的工单' })).toBeVisible()
  await expect(page.getByTestId('ticket-list-pane')).toContainText('待初筛 ×')

  await chip.click()
  await expect(page).toHaveURL(new RegExp(`/t/${matching}\\?status=open$`))
  await expect(page.locator('.tcard', { hasText: '详情标签筛选不命中的工单' })).toBeVisible()
})

test('clicking a detail label chip on a narrow viewport returns to the filtered list', async ({
  page,
  request,
}) => {
  const matching = await createTicket(request, {
    title: '窄屏标签筛选命中的工单',
    labels: ['机型:UD-1290'],
  })
  await createTicket(request, { title: '窄屏标签筛选不命中的工单', labels: ['机型:UD-2300'] })

  await page.setViewportSize({ width: 900, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/t/${matching}?status=open`)
  await expect(page.getByRole('heading', { name: '窄屏标签筛选命中的工单' })).toBeVisible()
  await page.getByText('标签 · 指派 · 访客链接').click()
  await page.locator('.mmeta .chip', { hasText: '机型:UD-1290' }).click()

  await expect(page).toHaveURL(/\/tickets\?status=open&label_any=/)
  await expect(page.getByTestId('ticket-list-pane')).toBeVisible()
  await expect(page.locator('.tcard', { hasText: matching })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: '窄屏标签筛选不命中的工单' })).toHaveCount(0)
  await expect(page.getByRole('heading', { name: '窄屏标签筛选命中的工单' })).toHaveCount(0)
})
