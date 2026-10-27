import { expect, test } from '@playwright/test'
import { ADMIN, claimGuestLink, createTicket, login } from './helpers'

test('wide detail keeps actions in the header and shows ordered ticket information', async ({
  page,
  request,
}) => {
  const number = await createTicket(request, {
    title: '宽屏信息栏的工单',
    labels: ['机型:UD-2300'],
    assignee: '王工',
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}?status=open`)
  await expect(page.getByRole('heading', { name: '宽屏信息栏的工单' })).toBeVisible()

  const actions = page.getByRole('region', { name: '工单操作' }).getByRole('button')
  await expect(actions).toHaveText([
    '🏷 标签',
    '👤 改派',
    '🤖 复制给我的 Agent',
    '📨 复制给对方的 Agent',
    '🔗 访客链接',
    '🤝 协作者',
    '✔ 关闭',
  ])

  await page.getByRole('region', { name: '工单操作' }).getByRole('button', { name: '访客链接' }).click()
  await page.getByLabel('备注').fill('检验科-张三')
  await page.getByLabel('有效期').selectOption('7')
  await page.getByLabel('可填写说明').check()
  await page.getByRole('button', { name: '创建', exact: true }).click()
  const url = (await page.locator('.linkbox').textContent()) ?? ''
  await page.getByRole('button', { name: '关闭', exact: true }).click()

  const info = page.getByRole('region', { name: '工单信息', exact: true })
  await expect(info).toBeVisible()
  expect(await info.evaluate((el) => el.getBoundingClientRect().width)).toBe(290)
  await expect(info.locator('.sec-t')).toHaveText(['状态', '协作者', '指派人', '发起人', '标签', '访客链接', '访问记录'])
  await expect(info).toContainText('任务')
  await expect(info).toContainText('等待 访客补全说明')
  await expect(info).toContainText('王工')
  await expect(info).toContainText('管理员')
  await expect(info).toContainText('机型:UD-2300')
  await expect(info).toContainText('检验科-张三')
  await expect(info).toContainText('可填写说明')
  await expect(info).toContainText(url)
  await expect(info).toContainText('剩余')
  await expect(info).toContainText('已使用 0 次')
  await expect(info).not.toContainText('复制给我的 Agent')

  const token = new URL(url).pathname.slice(3)
  const deviceToken = await claimGuestLink(request, token)
  const reply = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: '使用这条链接回复一次。' },
    headers: { Authorization: `Bearer ${deviceToken}` },
  })
  expect(reply.status(), `访客回复失败：${await reply.text()}`).toBe(201)
  await page.reload()
  await expect(page.getByRole('region', { name: '工单信息', exact: true })).toContainText('已使用 1 次')

  await page.getByRole('button', { name: '访客链接' }).click()
  await expect(page.locator('.modal')).toContainText(`访客链接 · ${number}`)
})

test('medium detail collapses ticket information into an expandable summary', async ({
  page,
  request,
}) => {
  const number = await createTicket(request, {
    title: '中屏信息摘要的工单',
    labels: ['机型:UD-2300'],
    assignee: '王工',
  })
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '检验科-张三', valid_days: 7 },
  })
  expect(link.status(), `创建访客链接失败：${await link.text()}`).toBe(201)

  await page.setViewportSize({ width: 1100, height: 800 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}?status=open`)
  await expect(page.getByRole('heading', { name: '中屏信息摘要的工单' })).toBeVisible()

  const summary = page.getByRole('region', { name: '工单信息摘要' })
  await expect(summary).toBeVisible()
  await expect(summary).toContainText('任务')
  await expect(summary).toContainText('等待 王工')
  await expect(summary).toContainText('指派 王工')
  await expect(summary).toContainText('机型:UD-2300')
  await expect(summary).toContainText('访客链接 1 条')
  await expect(page.getByRole('region', { name: '工单信息', exact: true })).toHaveCount(0)

  await summary.getByRole('button', { name: '全部信息 ▾' }).click()
  const info = page.getByRole('region', { name: '工单信息', exact: true })
  await expect(info).toBeVisible()
  await expect(info).toContainText('发起人')
  await expect(info).toContainText('检验科-张三')
  await expect(summary.getByRole('button', { name: '收起 ▴' })).toBeVisible()
})

test('mobile detail keeps the original metadata layout', async ({ page, request }) => {
  const number = await createTicket(request, {
    title: '手机详情保持原样的工单',
    labels: ['机型:UD-2300'],
    assignee: '王工',
  })

  await page.setViewportSize({ width: 390, height: 844 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}?status=open`)
  await expect(page.getByRole('heading', { name: '手机详情保持原样的工单' })).toBeVisible()

  await expect(page.getByText('标签 · 指派 · 访客链接')).toBeVisible()
  await expect(page.getByRole('region', { name: '工单信息摘要', exact: true })).toHaveCount(0)
  await expect(page.getByRole('region', { name: '工单信息', exact: true })).toHaveCount(0)
  await expect(page.getByRole('region', { name: '工单操作' }).getByRole('button', { name: '标签' })).toBeVisible()
})
