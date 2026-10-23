import { expect, request as playwrightRequest, test, type APIRequestContext, type Page } from '@playwright/test'
import { createHash } from 'node:crypto'
import { ADMIN, login, loginWithRequest, wang } from './helpers'

const viewports = [
  { name: 'PC 1440', width: 1440, height: 900 },
  { name: '手机 390', width: 390, height: 844 },
]

declare global {
  interface Window {
    __webBellNotifications: { title: string; options?: NotificationOptions }[]
  }
}

async function adminContext() {
  const admin = await playwrightRequest.newContext()
  const res = await loginWithRequest(admin, ADMIN)
  expect(res.ok(), await res.text()).toBeTruthy()
  return admin
}

async function createAssignedTicket(
  admin: APIRequestContext,
  title: string,
): Promise<string> {
  const res = await admin.post('/api/v1/tickets', {
    data: {
      title,
      brief_md: `${title} 的说明。`,
      labels: ['机型:UD-2300'],
      assignee: 'wang',
    },
  })
  expect(res.status(), await res.text()).toBe(201)
  return ((await res.json()) as { number: string }).number
}

async function clearNotifications(page: Page) {
  const res = await page.request.post('/api/v1/notifications/read', { data: { up_to: 999999999 } })
  expect(res.ok(), await res.text()).toBeTruthy()
}

const clientVerifier = 'web-bell-client-verifier-with-enough-pkce-characters-1234567890'
const baseURL = `http://127.0.0.1:${process.env.E2E_PORT || 8901}`

async function issueClientCredential(page: Page, hostname: string): Promise<string> {
  const challenge = createHash('sha256').update(clientVerifier).digest('base64url')
  const connect = await page.request.post('/api/v1/client/connect', {
    data: { port: 49161, state: 'web-bell-state', code_challenge: challenge, hostname },
  })
  expect(connect.status(), await connect.text()).toBe(201)
  const redirect = ((await connect.json()) as { redirect_url: string }).redirect_url
  const code = new URL(redirect).searchParams.get('code')
  expect(code).toBeTruthy()
  const exchange = await page.request.post('/api/v1/client/token', {
    data: { code, code_verifier: clientVerifier },
  })
  expect(exchange.status(), await exchange.text()).toBe(201)
  return ((await exchange.json()) as { token: string }).token
}

async function waitClientOnline(page: Page, online: boolean) {
  for (let i = 0; i < 60; i++) {
    const res = await page.request.get('/api/v1/notifications/unread')
    expect(res.ok()).toBeTruthy()
    if (((await res.json()) as { client_online: boolean }).client_online === online) return
    await new Promise((resolve) => setTimeout(resolve, 100))
  }
  throw new Error(`等待 client_online=${online} 超时`)
}

for (const viewport of viewports) {
  test.describe(`网页通知铃铛 ${viewport.name}`, () => {
    test.use({ viewport: { width: viewport.width, height: viewport.height } })

    test('未读角标正确，超过 99 显示 99+，首屏 30 条并支持加载更多', async ({ page }) => {
      const who = await wang()
      await login(page, who)
      await clearNotifications(page)
      const admin = await adminContext()
      for (let i = 0; i < 100; i++) {
        await createAssignedTicket(admin, `铃铛批量通知 ${String(i + 1).padStart(3, '0')}`)
      }
      await page.goto('/')
      await expect(page.locator('.bellbtn b')).toHaveText('99+')

      await page.getByRole('button', { name: '通知' }).click()
      await expect(page.locator('.nrow')).toHaveCount(30)
      await expect(page.getByRole('button', { name: '加载更多' })).toBeVisible()
      await page.getByRole('button', { name: '加载更多' }).click()
      await expect(page.locator('.nrow')).toHaveCount(60)
      await clearNotifications(page)
      await expect(page.locator('.bellbtn')).not.toContainText('99+')
    })

    test('抽屉支持遮罩、Esc 和关闭，未读筛选与空状态可用', async ({ page }) => {
      const who = await wang()
      await login(page, who)
      await clearNotifications(page)
      const admin = await adminContext()
      const number = await createAssignedTicket(admin, '铃铛抽屉通知')
      await page.goto('/')
      await expect(page.locator('.bellbtn b')).toHaveText('1')

      await page.getByRole('button', { name: '通知' }).click()
      await expect(page.getByRole('heading', { name: '通知' })).toBeVisible()
      await expect(page.locator('.nrow.unread .ntitle')).toContainText(`${number} 铃铛抽屉通知`)
      await expect(page.getByText('收件箱 = 轮到你处理；通知 = 发生了什么。')).toBeVisible()
      await page.locator('.notice-scrim').click({ position: { x: 10, y: 10 } })
      await expect(page.getByRole('heading', { name: '通知' })).toBeHidden()

      await page.getByRole('button', { name: '通知' }).click()
      await page.keyboard.press('Escape')
      await expect(page.getByRole('heading', { name: '通知' })).toBeHidden()

      await page.getByRole('button', { name: '通知' }).click()
      await page.getByRole('tab', { name: '未读' }).click()
      await expect(page.locator('.nrow')).toHaveCount(1)
      await page.getByRole('button', { name: '全部标为已读' }).click()
      await expect(page.locator('.bellbtn b')).toBeHidden()
      await expect(page.locator('.nrow')).toHaveCount(0)
      await expect(page.getByText('没有未读通知')).toBeVisible()
      await expect(page.locator('.nrow.unread .ndot')).toBeHidden()
      await page.keyboard.press('Escape')

      if (viewport.width === 390) {
        await page.getByRole('button', { name: '通知' }).click()
        const handle = page.locator('.sheet-handle')
        const box = await handle.boundingBox()
        expect(box).toBeTruthy()
        await handle.dispatchEvent('pointerdown', { pointerId: 1, clientY: box!.y + 8 })
        await handle.dispatchEvent('pointermove', { pointerId: 1, clientY: box!.y + 128 })
        await handle.dispatchEvent('pointerup', { pointerId: 1, clientY: box!.y + 128 })
        await expect(page.getByRole('heading', { name: '通知' })).toBeHidden()
      } else {
        await page.getByRole('button', { name: '通知' }).click()
        await page.getByRole('button', { name: '关闭通知' }).click()
        await expect(page.getByRole('heading', { name: '通知' })).toBeHidden()
      }
    })

    test('点击工单通知后 SPA 打开工单并按工单标已读', async ({ page }) => {
      const who = await wang()
      await login(page, who)
      await clearNotifications(page)
      const admin = await adminContext()
      const number = await createAssignedTicket(admin, '铃铛点击工单通知')
      await page.goto('/')
      await page.getByRole('button', { name: '通知' }).click()
      await page.locator('.nrow', { hasText: '铃铛点击工单通知' }).click()
      await expect(page).toHaveURL(new RegExp(`/t/${number.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`))
      await expect(page.getByRole('heading', { name: '铃铛点击工单通知' })).toBeVisible()
      const list = await page.request.get('/api/v1/notifications?limit=200')
      const body = (await list.json()) as { items: { ticket: { number: string }; read_at: string | null }[] }
      expect(body.items.find((n) => n.ticket?.number === number)?.read_at).not.toBeNull()
    })

    test('点击系统通知后打开管理备份，浏览器提醒开关可用', async ({ page }) => {
      const created = new Date().toISOString()
      const item = {
        id: 900001,
        ticket: null,
        url: '/admin#backup',
        kind: 'fyi',
        reason: 'backup_failing',
        text: '备份失败：测试目标不可达。',
        event: { seq: 0, type: 'system', actor: { kind: 'system', member_id: 0, agent_id: 0, name: '工单板' } },
        summary: '',
        created_at: created,
        read_at: null,
      }
      await page.addInitScript((mode: string) => {
        Object.defineProperty(window, 'Notification', {
          value: class {
            static permission = mode
            static requestPermission() {
              return Promise.resolve(mode)
            }
          },
        })
      }, viewport.width === 390 ? 'granted' : 'denied')
      await page.route('**/api/v1/notifications/unread', (route) =>
        route.fulfill({ json: { unread_count: 1, latest_id: 900001, client_online: false } }))
      await page.route('**/api/v1/notifications?*', (route) =>
        route.fulfill({ json: { items: [item], cursor: 900001, unread_count: 1, next: null } }))
      await page.route('**/api/v1/notifications/stream*', (route) =>
        route.fulfill({
          status: 200,
          headers: { 'content-type': 'text/event-stream' },
          body: `event: hello\ndata: ${JSON.stringify({ unread_count: 1, latest_id: 900001 })}\n\n`,
        }))
      await login(page, ADMIN)
      await page.goto('/')
      await expect(page.locator('.bellbtn b')).toHaveText('1')
      await page.getByRole('button', { name: '通知' }).click()
      await expect(page.locator('.nrow.system')).toContainText('🛡 备份失败：测试目标不可达。')
      await page.locator('.nrow.system').click()
      await expect(page).toHaveURL('/admin#backup')
      await expect(page.getByTestId('backup-card')).toBeVisible()

      await page.goto('/')
      await page.getByRole('button', { name: '通知' }).click()
      if (viewport.width === 390) {
        await page.context().grantPermissions(['notifications'])
        await page.getByRole('switch', { name: '在此浏览器弹出提醒' }).click({ force: true })
        await expect(page.getByRole('switch', { name: '在此浏览器弹出提醒' })).toHaveAttribute('aria-checked', 'true')
        await page.getByRole('switch', { name: '在此浏览器弹出提醒' }).click({ force: true })
        await expect(page.getByRole('switch', { name: '在此浏览器弹出提醒' })).toHaveAttribute('aria-checked', 'false')
      } else {
        await page.getByRole('switch', { name: '在此浏览器弹出提醒' }).click({ force: true })
        await expect(page.getByText('浏览器已拒绝通知权限，请在站点设置里开启')).toBeVisible()
        await expect(page.getByRole('switch', { name: '在此浏览器弹出提醒' })).toHaveAttribute('aria-checked', 'false')
      }
    })

    test('别人回复后 SSE 让角标实时加一', async ({ page }) => {
      const who = await wang()
      await login(page, who)
      await clearNotifications(page)
      const admin = await adminContext()
      const number = await createAssignedTicket(admin, '铃铛实时通知')
      await page.goto('/')
      await expect(page.locator('.bellbtn b')).toHaveText('1')
      const reply = await admin.post(`/api/v1/tickets/${number}/replies`, {
        data: { body_md: '有人回复，触发实时通知。' },
      })
      expect(reply.status(), await reply.text()).toBe(201)
      await expect(page.locator('.bellbtn b')).toHaveText('2')
    })

    test('页面内导航不会重建通知 SSE', async ({ page }) => {
      const who = await wang()
      await login(page, who)
      await clearNotifications(page)
      let streamRequests = 0
      page.on('request', (request) => {
        if (new URL(request.url()).pathname === '/api/v1/notifications/stream') streamRequests++
      })
      await page.goto('/')
      await expect(page.locator('.bellbtn')).toBeVisible()
      // EventSource 在铃铛渲染之后的 effect 里才发起，铃铛可见时请求不一定已经发出。
      await expect.poll(() => streamRequests).toBe(1)

      const navInPage = (to: string) =>
        page.evaluate((path) => {
          history.pushState(null, '', path)
          window.dispatchEvent(new Event('tb:nav'))
        }, to)
      await navInPage('/tickets')
      await expect(page).toHaveURL('/tickets')
      await navInPage('/')
      await expect(page).toHaveURL('/')
      await navInPage('/me')
      await expect(page).toHaveURL('/me')
      await navInPage('/')
      await expect(page).toHaveURL('/')
      expect(streamRequests).toBe(1)
    })

    test('客户端中途上线时浏览器提醒避让，断开后恢复', async ({ page }) => {
      await page.addInitScript(() => {
        Object.defineProperty(Document.prototype, 'hidden', {
          configurable: true,
          get: () => true,
        })
        localStorage.setItem('tb:browser-notifications', '1')
        class MockNotification {
          static permission = 'granted' as NotificationPermission
          static requestPermission() {
            return Promise.resolve('granted' as NotificationPermission)
          }
          onclick: (() => void) | null = null
          constructor(title: string, options?: NotificationOptions) {
            window.__webBellNotifications.push({ title, options })
          }
        }
        window.Notification = MockNotification as unknown as typeof Notification
        window.__webBellNotifications = []
      })
      const who = await wang()
      await login(page, who)
      await clearNotifications(page)
      await page.goto('/')
      await expect(page.locator('.bellbtn')).toBeVisible()

      const token = await issueClientCredential(page, `E2E-WEB-BELL-${viewport.width}`)
      const clientStream = await fetch(`${baseURL}/api/v1/notifications/stream`, {
        headers: { Authorization: `Bearer ${token}` },
      })
      expect(clientStream.ok, `client SSE status ${clientStream.status}`).toBeTruthy()
      try {
        await waitClientOnline(page, true)
        const admin = await adminContext()
        await createAssignedTicket(admin, '客户端在线不弹浏览器提醒')
        await expect(page.locator('.bellbtn b')).toHaveText('1')
        expect(await page.evaluate(() => window.__webBellNotifications)).toEqual([])

        await clientStream.body?.cancel()
        await waitClientOnline(page, false)
        await createAssignedTicket(admin, '客户端离线恢复浏览器提醒')
        await expect(page.locator('.bellbtn b')).toHaveText('2')
        await expect.poll(() => page.evaluate(() => window.__webBellNotifications)).toHaveLength(1)
        await clearNotifications(page)
      } finally {
        await clientStream.body?.cancel()
      }
    })
  })
}
