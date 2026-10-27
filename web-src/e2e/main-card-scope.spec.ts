import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import { ADMIN, createTicket, login, loginAdmin, openGuestPage, loginWithRequest } from './helpers'

async function seedGuestLink(
  request: APIRequestContext,
  opts: { canEditBrief?: boolean } = {},
): Promise<string> {
  const number = await createTicket(request, {
    title: '主区域布局检查工单',
    brief: '用于检查访客端根元素布局。',
  })
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: {
      note: '检验科-张三',
      valid_days: 30,
      can_edit_brief: opts.canEditBrief ?? false,
    },
  })
  expect(link.status(), `建访客链接失败：${await link.text()}`).toBe(201)
  return String((await link.json()).url).slice(3)
}

async function seedIntakeSlug(request: APIRequestContext): Promise<string> {
  await loginAdmin(request)
  const created = await request.post('/api/v1/intakes', {
    data: {
      slug: `layout-${Date.now()}`,
      name: '布局检查反馈入口',
      template: 'bug-report',
      department_contact: 'IT 服务台',
    },
  })
  expect(created.status(), `建反馈入口失败：${await created.text()}`).toBe(201)
  return (await created.json()).slug as string
}

async function expectPlainMain(page: Page, minWidth = 900): Promise<void> {
  const main = page.getByRole('main')
  const box = await main.boundingBox()
  expect(box?.width).toBeGreaterThan(minWidth)
  await expect(main).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
  await expect(main).toHaveCSS('box-shadow', 'none')
}

test('PC 成员页不继承登录卡片', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)

  const checks: Array<[string, () => Promise<void>]> = [
    ['/new', () => expect(page.getByRole('heading', { name: '建单' })).toBeVisible()],
    ['/tools', () => expect(page.getByText('标签字典与别名')).toBeVisible()],
    ['/templates', () => expect(page.getByText('模板维护')).toBeVisible()],
    ['/templates/bug-report', () => expect(page.getByRole('textbox').first()).toBeVisible()],
    ['/me', () => expect(page.getByText('账号', { exact: true })).toBeVisible()],
    ['/admin', () => expect(page.getByRole('tablist', { name: '管理分区' })).toBeVisible()],
  ]
  for (const [path, ready] of checks) {
    await page.goto(path)
    await ready()
    await expectPlainMain(page)
  }

  await page.goto('/new')
  await page.getByRole('button', { name: /我自己填/ }).click()
  await expect(page.getByRole('heading', { name: '选一个模板' })).toBeVisible()
  await expectPlainMain(page)

  await page.goto('/feedback')
  await expect(page.getByRole('heading', { name: '选一个模板' })).toBeVisible()
  await expectPlainMain(page)
  await page.locator('.tplpick').first().click()
  await expect(page.getByLabel('请谁填写')).toBeVisible()
  await expectPlainMain(page)
})

test('PC 窄屏列表不变成 640px 卡片', async ({ page, request }) => {
  await createTicket(request, { title: '窄屏主区域布局检查工单' })
  await page.setViewportSize({ width: 1104, height: 900 })
  await login(page, ADMIN)
  await page.goto('/tickets')
  await expect(page.getByRole('link', { name: '窄屏主区域布局检查工单' })).toBeVisible()
  // 列表 + 详情布局下详情区本身是铺满右侧的白底（#137），只断言它不是登录页那张卡片。
  const main = page.getByRole('main')
  expect((await main.boundingBox())?.width).toBeGreaterThan(640)
  await expect(main).toHaveCSS('box-shadow', 'none')
  await expect(main).toHaveCSS('border-radius', '0px')
  await expect(main).toHaveCSS('margin-top', '0px')
})

test('1440 宽建单页没有多余的纵向滚动', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)
  await page.goto('/new')
  await expect(page.getByRole('heading', { name: '建单' })).toBeVisible()
  const overflow = await page.evaluate(() => {
    const el = document.documentElement
    return el.scrollHeight - el.clientHeight
  })
  expect(overflow).toBeLessThanOrEqual(1)
})

test('手机访客根元素铺满屏幕', async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const readonlyKey = await seedGuestLink(request)
  const editableKey = await seedGuestLink(request, { canEditBrief: true })
  const claimKey = await seedGuestLink(request)
  const slug = await seedIntakeSlug(request)

  for (const [name, open] of [
    ['guest link', () => openGuestPage(page, request, readonlyKey)],
    ['guest landing', () => openGuestPage(page, request, editableKey)],
    ['claim', () => page.goto(`/g/${claimKey}`)],
    ['intake', () => page.goto(`/i/${slug}`)],
  ] as const) {
    await open()
    const root = page.locator('.guest-root')
    await expect(root).toBeVisible()
    const styles = await root.evaluate((el) => {
      const s = getComputedStyle(el)
      return {
        margin: s.margin,
        paddingLeft: s.paddingLeft,
        paddingRight: s.paddingRight,
      }
    })
    expect(styles, name).toEqual({ margin: '0px', paddingLeft: '0px', paddingRight: '0px' })
  }
})

test('登录页和客户端连接页保留居中卡片', async ({ page, request }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await loginWithRequest(request, ADMIN)
  const cookies = (await request.storageState()).cookies

  // 登录页要未登录时看（已登录会跳回首页），客户端连接页要已登录时看。
  for (const path of ['/login', '/client/connect?port=1&state=x&code_challenge=y&hostname=E2E']) {
    if (path !== '/login') await page.context().addCookies(cookies)
    await page.goto(path)
    const main = page.locator('main.login')
    await expect(main).toBeVisible()
    await expect(main).toHaveCSS('max-width', '640px')
    await expect(main).toHaveCSS('background-color', 'rgb(255, 255, 255)')
    const box = await main.boundingBox()
    expect(Math.abs(box!.x + box!.width / 2 - 720)).toBeLessThan(2)
  }
})

test('反馈模板返回链接与建单返回样式一致', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)

  await page.goto('/new')
  await page.getByRole('button', { name: /我自己填/ }).click()
  const pickStyle = await page.getByRole('button', { name: '← 建单' }).evaluate((el) => {
    const s = getComputedStyle(el)
    return {
      color: s.color,
      textDecorationLine: s.textDecorationLine,
      fontSize: s.fontSize,
      backgroundColor: s.backgroundColor,
      borderWidth: s.borderWidth,
    }
  })
  await page.goto('/feedback')
  await page.locator('.tplpick').first().click()
  await expect(page.getByRole('link', { name: '← 换一个模板' })).toBeVisible()

  const feedback = page.getByRole('link', { name: '← 换一个模板' })
  await expect(feedback).toHaveCSS('color', pickStyle.color)
  await expect(feedback).toHaveCSS('text-decoration-line', pickStyle.textDecorationLine)
  await expect(feedback).toHaveCSS('font-size', pickStyle.fontSize)
  await expect(feedback).toHaveCSS('background-color', pickStyle.backgroundColor)
  await expect(feedback).toHaveCSS('border-width', pickStyle.borderWidth)
})
