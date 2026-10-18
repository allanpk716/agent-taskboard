import { expect, test, type APIRequestContext } from '@playwright/test'

const ADMIN = { username: 'admin', password: 'admin-pass-123' }

async function seedGuestTicket(request: APIRequestContext) {
  const login = await request.post('/api/v1/session', { data: ADMIN })
  expect(login.ok()).toBeTruthy()
  const created = await request.post('/api/v1/tickets', {
    data: {
      title: 'UD-2300 质控时报警 E-203',
      brief_md: '开机自检后进入质控界面，点击「开始」时弹出 E-203。',
      labels: ['机型:UD-2300'],
    },
  })
  expect(created.status(), `建单失败：${await created.text()}`).toBe(201)
  const number = (await created.json()).number as string
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '检验科-张三', valid_days: 30, can_edit_brief: false },
  })
  expect(link.status(), `创建访客链接失败：${await link.text()}`).toBe(201)
  return { number, linkKey: String((await link.json()).url).slice(3) }
}

test.describe('PC 布局', () => {
  for (const width of [1440, 390]) {
    test(`访客页在 ${width} 宽下居中显示并带版本号`, async ({ page, request }) => {
      const seed = await seedGuestTicket(request)
      await page.setViewportSize({ width, height: 900 })
      await page.goto(`/g/${seed.linkKey}`)

      await expect(page.locator('header')).toContainText(`工单板 · ${seed.number} · 管理员 请你反馈`)
      const title = page.getByRole('heading', { name: 'UD-2300 质控时报警 E-203' })
      await expect(title).toBeVisible()
      await expect(page.locator('[data-bubble="brief"]')).toContainText('开机自检后进入质控界面')
      await expect(page.getByPlaceholder(/写回复/)).toBeVisible()

      const titleBox = await title.boundingBox()
      expect(titleBox?.width).toBeLessThanOrEqual(720)
      if (titleBox) expect(Math.abs(titleBox.x + titleBox.width / 2 - width / 2)).toBeLessThan(2)

      const health = await request.get('/api/v1/health')
      expect(health.ok()).toBeTruthy()
      const version = (await health.json()).server_version as string
      await expect(page.locator('footer')).toHaveText(`工单板 v${version}`)

      const bannerBox = await page.locator('header').boundingBox()
      const titleBoxAgain = await title.boundingBox()
      const briefBox = await page.locator('[data-bubble="brief"]').boundingBox()
      const composerBox = await page.getByPlaceholder(/写回复/).boundingBox()
      expect(bannerBox!.y).toBeLessThan(titleBoxAgain!.y)
      expect(titleBoxAgain!.y).toBeLessThan(briefBox!.y)
      expect(briefBox!.y).toBeLessThan(composerBox!.y)

      await expect(page.getByRole('button', { name: /检查更新|执行更新/ })).toHaveCount(0)
    })
  }

  test('健康接口失败时访客页不显示版本号', async ({ page, request }) => {
    const seed = await seedGuestTicket(request)
    await page.route('**/api/v1/health', (route) => route.abort('connectionfailed'))
    await page.goto(`/g/${seed.linkKey}`)

    await expect(page.locator('header')).toContainText(`工单板 · ${seed.number}`)
    await expect(page.locator('footer')).toHaveCount(0)
  })
})
