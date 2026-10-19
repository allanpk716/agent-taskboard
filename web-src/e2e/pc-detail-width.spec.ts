import { expect, test } from '@playwright/test'
import { ADMIN, createTicket, login, loginAdmin } from './helpers'

test('wide detail fills the pane from the list edge to the viewport edge', async ({
  page,
  request,
}) => {
  const number = await createTicket(request, { title: '宽屏铺满详情的工单', assignee: '管理员' })

  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 1920, height: 1080 },
  ]) {
    await page.setViewportSize(viewport)
    await login(page, ADMIN)
    await page.goto(`/t/${number}?status=open&assignee=me`)
    await expect(page.getByRole('heading', { name: '宽屏铺满详情的工单' })).toBeVisible()

    const list = await page.getByTestId('ticket-list-pane').boundingBox()
    const info = await page.locator('.dinfo').boundingBox()
    const conversation = await page.locator('.detail-main').boundingBox()
    const composer = await page.locator('.composer textarea').boundingBox()
    const head = await page.locator('.dhead').boundingBox()

    expect(conversation!.x).toBeCloseTo(list!.x + list!.width, 1)
    expect(conversation!.x + conversation!.width).toBeCloseTo(info!.x, 1)
    expect(conversation!.height).toBeGreaterThanOrEqual(
      viewport.height - head!.height - 4,
    )
    expect(conversation!.y + conversation!.height).toBeCloseTo(viewport.height, 1)
    expect(viewport.height - (composer!.y + composer!.height)).toBeLessThanOrEqual(120)

    await page.context().clearCookies()
  }
})

test('wide detail keeps the composer at the bottom while conversation scrolls', async ({
  page,
  request,
}) => {
  const number = await createTicket(request, { title: '宽屏长对话工单', assignee: '管理员' })
  for (let i = 1; i <= 18; i++) {
    const reply = await request.post(`/api/v1/tickets/${number}/replies`, {
      data: { body_md: `第 ${i} 条长对话回复：问题仍在处理中，这一条用于撑开对话流。` },
    })
    expect(reply.status(), `第 ${i} 条回复失败：${await reply.text()}`).toBe(201)
  }

  for (const viewport of [
    { width: 1440, height: 900 },
    { width: 1920, height: 1080 },
  ]) {
    await page.setViewportSize(viewport)
    await login(page, ADMIN)
    await page.goto(`/t/${number}?status=open&assignee=me`)
    await expect(page.getByRole('heading', { name: '宽屏长对话工单' })).toBeVisible()
    const composer = await page.locator('.composer textarea').boundingBox()
    const metrics = await page.locator('.conv').evaluate((el) => ({
      scrollHeight: el.scrollHeight,
      clientHeight: el.clientHeight,
    }))

    expect(composer!.y).toBeGreaterThanOrEqual(0)
    expect(composer!.y + composer!.height).toBeLessThanOrEqual(viewport.height)
    expect(viewport.height - (composer!.y + composer!.height)).toBeLessThanOrEqual(120)
    expect(metrics.scrollHeight).toBeGreaterThan(metrics.clientHeight)
    expect(
      await page.evaluate(
        () => document.scrollingElement!.scrollHeight <= window.innerHeight + 1,
      ),
    ).toBe(true)

    await page.context().clearCookies()
  }
})

test('member template brief collapses empty fields until expanded', async ({ page, request }) => {
  await loginAdmin(request)
  const created = await request.post('/api/v1/tickets', {
    data: {
      title: '折叠未填写字段的工单',
      template: 'bug-report',
      fields: { '哪台仪器？': 'UD-2300' },
    },
  })
  expect(created.status(), `建模板工单失败：${await created.text()}`).toBe(201)
  const number = (await created.json()).number as string

  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}?status=open`)
  const brief = page.locator('[data-bubble="brief"]')
  await expect(brief.getByRole('button', { name: /另有 \d+ 项未填写 ▸/ })).toBeVisible()
  await expect(brief.locator('.f')).toHaveCount(1)
  await expect(brief).not.toContainText('（未填写）')

  await brief.getByRole('button', { name: /另有 \d+ 项未填写 ▸/ }).click()
  await expect(brief).toContainText('（未填写）')
  await expect(brief.locator('.f').first()).toContainText('哪台仪器？')

  await page.setViewportSize({ width: 390, height: 844 })
  await page.reload()
  await expect(brief.getByRole('button', { name: /另有 \d+ 项未填写 ▸/ })).toBeVisible()
  await expect(brief).not.toContainText('（未填写）')
  await brief.getByRole('button', { name: /另有 \d+ 项未填写 ▸/ }).click()
  await expect(brief).toContainText('（未填写）')
})
