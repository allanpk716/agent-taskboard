import { expect, test } from '@playwright/test'
import { ADMIN, createTicket, login } from './helpers'

async function addLabel(request: Parameters<typeof createTicket>[0], ticket: string, label: string) {
  const res = await request.post(`/api/v1/tickets/${ticket}/labels`, {
    data: { add: [label], allow_new: true },
  })
  expect(res.ok(), `加标签失败：${await res.text()}`).toBeTruthy()
}

async function addAlias(request: Parameters<typeof createTicket>[0], label: string, alias: string) {
  const res = await request.post('/api/v1/labels/aliases', { data: { label, alias } })
  expect(res.ok(), `加别名失败：${await res.text()}`).toBeTruthy()
}

test('label facet panel combines dimensions and restores its URL filters', async ({ page, request }) => {
  const both = await createTicket(request, { title: '同时符合两个维度的工单', labels: ['机型:UD-2300', '产品线:血液'] })
  const excluded = await createTicket(request, {
    title: '会被不含排除的工单',
    labels: ['机型:UD-1290', '产品线:尿液'],
  })
  await addLabel(request, excluded, '待初筛')

  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)
  await page.goto('/tickets')
  await page.getByRole('button', { name: '🏷 标签 ▾' }).click()
  await expect(page.getByRole('heading', { name: '标签筛选 · 同维度多选取或' })).toBeVisible()

  const panel = page.getByTestId('label-facet-panel')
  await panel.getByRole('checkbox', { name: 'UD-2300' }).check()
  await panel.getByRole('checkbox', { name: 'UD-1290' }).check()
  await panel.getByRole('checkbox', { name: '血液' }).check()
  await panel.locator('.label-facet-value', { hasText: '待初筛' }).getByRole('button', { name: '不含' }).click()
  await expect(panel.getByText('已选 4 个条件')).toBeVisible()
  await panel.getByRole('button', { name: /显示 \d+ 张/ }).click()

  await expect(page).toHaveURL(/label_any=/u)
  await expect(page).toHaveURL(/label=/u)
  await expect(page).toHaveURL(/no_label=/u)
  await expect(page.locator('.tcard', { hasText: both })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: excluded })).toHaveCount(0)
  await expect(page.getByTestId('ticket-list-pane')).toContainText('机型：UD-2300 或 UD-1290 ×')
  await expect(page.getByTestId('ticket-list-pane')).toContainText('产品线：血液 ×')
  await expect(page.getByTestId('ticket-list-pane')).toContainText('不含 待初筛 ×')
  await expect(page.getByTestId('ticket-list-pane')).toContainText(
    '机型是 UD-2300 或 UD-1290，且 产品线是 血液，且 不含 待初筛 · 共 1 张',
  )

  await page.reload()
  await expect(page.locator('.tcard', { hasText: both })).toBeVisible()
  await expect(page.getByTestId('ticket-list-pane')).toContainText('机型：UD-2300 或 UD-1290 ×')
})

test('label facets search, expand long groups, and link counts to other conditions', async ({ page, request }) => {
  const matching = await createTicket(request, {
    title: '分面计数命中的工单',
    labels: ['机型:UD-1290', '产品线:尿液'],
  })
  await createTicket(request, {
    title: '其他产品线的机型工单',
    labels: ['机型:UD-2300', '产品线:血液'],
  })
  await addAlias(request, '机型:UD-2300', '小红机')

  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)
  await page.goto('/tickets')
  await page.getByRole('button', { name: '🏷 标签 ▾' }).click()
  const panel = page.getByTestId('label-facet-panel')
  const machine = panel.locator('.label-facet-group', { hasText: '机型' })
  await expect(machine.getByRole('button', { name: '展开全部 14 个' })).toBeVisible()
  await machine.getByRole('button', { name: '展开全部 14 个' }).click()
  await expect(machine.getByRole('checkbox', { name: 'US-1680' })).toBeVisible()

  await panel.getByLabel('搜索标签').fill('小红机')
  await expect(panel.getByRole('checkbox', { name: 'UD-2300' })).toBeVisible()
  await expect(panel.getByRole('checkbox', { name: 'UD-1290' })).toHaveCount(0)
  await panel.getByLabel('搜索标签').fill('产品线')
  await expect(panel.locator('.label-facet-group', { hasText: '产品线' }).getByRole('checkbox', { name: '血液' })).toBeVisible()

  await panel.getByLabel('搜索标签').fill('')
  await panel.getByRole('checkbox', { name: '尿液' }).check()
  const ud1290 = panel.locator('.label-facet-value', { hasText: 'UD-1290' })
  const ud2300 = panel.locator('.label-facet-value', { hasText: 'UD-2300' })
  await expect(ud1290).toContainText('1')
  await expect(ud2300).toContainText('0')
  await expect(ud2300).toHaveClass(/zero/)
  await panel.getByRole('checkbox', { name: 'UD-1290' }).check()
  await panel.getByRole('button', { name: /显示 \d+ 张/ }).click()
  await expect(page.locator('.tcard', { hasText: matching })).toBeVisible()
})

test('mobile label facets use a full-screen bottom drawer', async ({ page, request }) => {
  const ticket = await createTicket(request, { title: '手机标签抽屉的工单', labels: ['机型:UD-2300'] })
  await page.setViewportSize({ width: 390, height: 844 })
  await login(page, ADMIN)
  await page.goto('/tickets')
  await page.getByRole('button', { name: '🏷 标签 ▾' }).click()
  const panel = page.getByTestId('label-facet-panel')
  await expect(panel).toBeVisible()
  const box = await panel.boundingBox()
  expect(box?.x).toBe(0)
  expect(box?.y).toBe(0)
  expect(box?.width).toBe(390)
  expect(box?.height).toBe(844)
  await panel.getByRole('checkbox', { name: 'UD-2300' }).check()
  await panel.getByRole('button', { name: /显示 \d+ 张/ }).click()
  await expect(page).toHaveURL(/label_any=/u)
  await expect(page.locator('.tcard', { hasText: ticket })).toBeVisible()
  await expect(page.getByLabel('已选标签条件')).toContainText('机型：UD-2300 ×')
})
