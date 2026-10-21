import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import { ADMIN, login, loginAdmin, openGuestPage } from './helpers'

async function seedGuestLink(
  request: APIRequestContext,
  opts: { canEditBrief?: boolean } = {},
): Promise<string> {
  await loginAdmin(request)
  const created = await request.post('/api/v1/tickets', {
    data: {
      title: '主区域布局检查工单',
      brief_md: '用于检查访客端根元素布局。',
      labels: ['机型:UD-2300'],
    },
  })
  expect(created.status(), `建访客工单失败：${await created.text()}`).toBe(201)
  const number = (await created.json()).number as string
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

async function expectPlainMain(page: Page): Promise<void> {
  const main = page.getByRole('main')
  const box = await main.boundingBox()
  expect(box?.width).toBeGreaterThan(900)
  await expect(main).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
  await expect(main).toHaveCSS('box-shadow', 'none')
}

test('PC member pages do not inherit the login card', async ({ page }) => {
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

test('mobile guest roots fill the viewport', async ({ page, request }) => {
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

test('login and client connect keep their centered card', async ({ page, request }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await request.post('/api/v1/session', { data: ADMIN })
  const cookies = (await request.storageState()).cookies
  await page.context().addCookies(cookies)

  for (const path of ['/login', '/client/connect?port=1&state=x&code_challenge=y&hostname=E2E']) {
    await page.goto(path)
    const main = page.locator('main.login')
    await expect(main).toBeVisible()
    await expect(main).toHaveCSS('max-width', '640px')
    await expect(main).toHaveCSS('background-color', 'rgb(255, 255, 255)')
    const box = await main.boundingBox()
    expect(Math.abs(box!.x + box!.width / 2 - 720)).toBeLessThan(2)
  }
})

test('feedback template back link matches the create-flow back style', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)

  await page.goto('/new')
  await page.getByRole('button', { name: /我自己填/ }).click()
  const pickStyle = await page.getByRole('button', { name: '← 建单' }).evaluate((el) => {
    const s = getComputedStyle(el)
    return [s.color, s.textDecorationLine, s.fontSize, s.backgroundColor, s.borderWidth] as const
  })
  await page.goto('/feedback')
  await page.locator('.tplpick').first().click()
  await expect(page.getByRole('link', { name: '← 换一个模板' })).toBeVisible()

  const feedback = page.getByRole('link', { name: '← 换一个模板' })
  await expect(feedback).toHaveCSS('color', pickStyle[0])
  await expect(feedback).toHaveCSS('text-decoration-line', pickStyle[1])
  await expect(feedback).toHaveCSS('font-size', pickStyle[2])
  await expect(feedback).toHaveCSS('background-color', pickStyle[3])
  await expect(feedback).toHaveCSS('border-width', pickStyle[4])
})
