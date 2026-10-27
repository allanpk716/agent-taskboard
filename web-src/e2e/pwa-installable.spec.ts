import { expect, test, type APIResponse } from '@playwright/test'

type ManifestIcon = { src: string; sizes: string; purpose?: string }
type Manifest = {
  name: string
  short_name: string
  start_url: string
  display: string
  icons: ManifestIcon[]
}

// 后端对未知路径做 SPA 回退时也返回 200，所以不能只看状态码，
// 静态文件必须同时核对 Content-Type。
async function expectPng(response: APIResponse, path: string) {
  expect(response.status(), `${path} 应返回 200`).toBe(200)
  expect(
    response.headers()['content-type'],
    `${path} 应是 PNG，而不是 SPA 回退的 HTML`,
  ).toMatch(/^image\/png\b/)
}

// #246：平台可以添加为手机主屏应用（manifest、图标、service worker 齐全且不缓存）。
test('手机主屏应用的安装声明齐全', async ({ page, request }) => {
  const home = await request.get('/')
  expect(home.status(), '首页应返回 200').toBe(200)
  const html = await home.text()
  expect(html, '首页 HTML 应声明 manifest').toContain('<link rel="manifest" href="/manifest.json"')
  expect(html, '首页 HTML 应声明主题色').toContain('<meta name="theme-color" content="#2563eb"')
  expect(html, '首页 HTML 应声明 apple-touch-icon').toContain(
    '<link rel="apple-touch-icon" href="/apple-touch-icon.png"',
  )

  const manifestResponse = await request.get('/manifest.json')
  expect(manifestResponse.status(), 'manifest 应返回 200').toBe(200)
  expect(manifestResponse.headers()['content-type'], 'manifest 应是 JSON').toMatch(/json/)
  expect(manifestResponse.headers()['cache-control'], 'manifest 应 no-cache').toBe('no-cache')
  const manifest = (await manifestResponse.json()) as Manifest
  expect(manifest).toMatchObject({
    name: '部门工单平台',
    short_name: '工单',
    start_url: '/',
    display: 'standalone',
  })
  expect(manifest.icons.map((icon) => icon.sizes)).toEqual(
    expect.arrayContaining(['192x192', '512x512']),
  )

  await page.goto('/')
  const appleTouchIconHref = await page
    .locator('link[rel="apple-touch-icon"]')
    .getAttribute('href')
  expect(appleTouchIconHref).toBe('/apple-touch-icon.png')
  for (const icon of manifest.icons) {
    await expectPng(await request.get(icon.src), icon.src)
  }
  await expectPng(await request.get('/apple-touch-icon.png'), '/apple-touch-icon.png')

  const registration = await page.evaluate(async () => {
    const ready = await navigator.serviceWorker.ready
    return { scope: ready.scope }
  })
  expect(registration.scope).toBe(new URL('/', page.url()).href)

  const sw = await request.get('/sw.js')
  expect(sw.status(), 'service worker 应返回 200').toBe(200)
  expect(sw.headers()['content-type'], 'service worker 应是 JavaScript').toMatch(/javascript/)
  expect(sw.headers()['cache-control'], 'service worker 应 no-cache').toBe('no-cache')
})
