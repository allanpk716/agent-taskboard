import { createServer, type Server } from 'node:http'
import { createHash } from 'node:crypto'
import { test, expect, type APIRequestContext, type Page } from '@playwright/test'
import { login, wang } from './helpers'

const verifier = 'e2e-client-verifier-with-enough-characters-for-pkce-1234567890'

function challenge(): string {
  return createHash('sha256').update(verifier).digest('base64url')
}

async function listenCallback(): Promise<{ server: Server; port: number; codes: string[] }> {
  const codes: string[] = []
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? '/cb', 'http://127.0.0.1')
    if (url.pathname === '/cb') codes.push(url.searchParams.get('code') ?? '')
    res.setHeader('content-type', 'text/html; charset=utf-8')
    res.end('<!doctype html><title>已连接</title><body>已连接这台电脑</body>')
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  return { server, port: (server.address() as { port: number }).port, codes }
}

function connectURL(port: number, hostname: string): string {
  return `/client/connect?port=${port}&state=e2e-state&code_challenge=${challenge()}&hostname=${encodeURIComponent(hostname)}`
}

async function confirmAndExchange(page: Page, request: APIRequestContext, hostname: string): Promise<string> {
  const cb = await listenCallback()
  try {
    await page.goto(connectURL(cb.port, hostname))
    await page.getByRole('button', { name: '连接' }).click()
    await expect(page.getByText('已连接这台电脑')).toBeVisible()
    expect(cb.codes).toHaveLength(1)
    const res = await request.post('/api/v1/client/token', {
      data: { code: cb.codes[0], code_verifier: verifier },
    })
    expect(res.status(), await res.text()).toBe(201)
    return ((await res.json()) as { token: string }).token
  } finally {
    await new Promise<void>((resolve) => cb.server.close(() => resolve()))
  }
}

test('已登录成员在确认页连接本机客户端', async ({ page, request }) => {
  await login(page, await wang())
  const token = await confirmAndExchange(page, request, 'E2E-WORKSTATION')
  expect(token).toMatch(/^tbc_/)
})

test('确认页取消不签发客户端code', async ({ page }) => {
  await login(page, await wang())
  const cb = await listenCallback()
  try {
    await page.goto(connectURL(cb.port, 'E2E-CANCEL'))
    await page.getByRole('button', { name: '取消' }).click()
    await expect(page).toHaveURL(/\/$/)
    expect(cb.codes).toHaveLength(0)
  } finally {
    await new Promise<void>((resolve) => cb.server.close(() => resolve()))
  }
})

test('未登录打开确认页，登录后回到确认页完成连接', async ({ page, request }) => {
  const cb = await listenCallback()
  try {
    await page.goto(connectURL(cb.port, 'E2E-LOGIN'))
    await expect(page).toHaveURL(/\/login/)
    const who = await wang()
    await page.getByLabel('用户名').fill(who.username)
    await page.getByLabel('密码').fill(who.password)
    await page.getByLabel('验证码').fill('TB123')
    await page.getByRole('button', { name: '登录' }).click()
    await expect(page.getByRole('heading', { name: '连接这台电脑' })).toBeVisible()
    await page.getByRole('button', { name: '连接' }).click()
    await expect(page.getByText('已连接这台电脑')).toBeVisible()
    expect(cb.codes).toHaveLength(1)
    const exchange = await request.post('/api/v1/client/token', {
      data: { code: cb.codes[0], code_verifier: verifier },
    })
    expect(exchange.status()).toBe(201)
  } finally {
    await new Promise<void>((resolve) => cb.server.close(() => resolve()))
  }
})

test('我的页面显示已连接电脑并支持撤销', async ({ page, request }) => {
  const who = await wang()
  await login(page, who)
  const token = await confirmAndExchange(page, request, 'E2E-ME-LIST')
  const used = await request.get('/api/v1/session', {
    headers: { Authorization: `Bearer ${token}`, 'User-Agent': 'taskboard-client/2.0.1' },
  })
  expect(used.ok()).toBeTruthy()

  await page.goto('/me')
  const row = page.locator('.mini-tbl tr', { hasText: 'E2E-ME-LIST' })
  await expect(row).toContainText('taskboard-client 2.0.1')
  await row.getByRole('button', { name: '撤销' }).click()
  await page.locator('.modal').getByRole('button', { name: '撤销', exact: true }).click()
  await expect(page.getByText('已撤销这台电脑的凭证')).toBeVisible()
  await expect(page.locator('.mini-tbl tr', { hasText: 'E2E-ME-LIST' })).toHaveCount(0)
  const revoked = await request.get('/api/v1/session', {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(revoked.status()).toBe(401)
})

test('我的页面显示已代管的桌面客户端', async ({ page }) => {
  test.skip(!process.env.E2E_CLIENT_VERSION, '这一轮服务端未预置代管客户端')
  await login(page, await wang())
  await page.goto('/me')
  const card = page.getByTestId('client-card')
  await expect(card).toBeVisible()
  await expect(card).toContainText('首次运行如遇 SmartScreen')
  await expect(card).toContainText('更多信息')
  await expect(card).toContainText('仍要运行')
  await expect(card).toContainText('杀毒软件')
  await expect(page.getByTestId('client-download')).toHaveText(
    `下载 Windows 客户端 ${process.env.E2E_CLIENT_VERSION}`,
  )
})

test('我的页面在未代管时提示管理员尚未发布客户端', async ({ page }) => {
  test.skip(!!process.env.E2E_CLIENT_VERSION, '这一轮服务端已预置代管客户端')
  await login(page, await wang())
  await page.goto('/me')
  await expect(page.getByTestId('client-unavailable')).toHaveText('管理员尚未发布客户端。')
})
