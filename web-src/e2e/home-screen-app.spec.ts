import { expect, test, type APIResponse } from '@playwright/test'

type ManifestIcon = {
  src: string
  sizes: string
  purpose?: string
}

type Manifest = {
  name: string
  short_name: string
  start_url: string
  scope: string
  display: string
  icons: ManifestIcon[]
}

async function expectImage(response: APIResponse, path: string) {
  expect(response.status(), `${path} 应返回 200`).toBe(200)
  expect(response.headers()['content-type'], `${path} 应是图片`).toMatch(/^image\//)
}

// #246：页面可添加为手机主屏应用（manifest、图标、不缓存的 service worker）。
test('手机主屏应用的安装声明和 service worker 可用', async ({ page, request }) => {
  const homeResponse = await request.get('/')
  expect(homeResponse.status(), '首页应返回 200').toBe(200)

  await page.goto('/')
  const manifestHref = await page.locator('link[rel="manifest"]').getAttribute('href')
  expect(manifestHref).toBe('/manifest.json')
  await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute(
    'content',
    '#2a6fdb',
  )
  const appleTouchIconHref = await page
    .locator('link[rel="apple-touch-icon"]')
    .getAttribute('href')
  expect(appleTouchIconHref).toBe('/apple-touch-icon-180.png')

  const manifestResponse = await request.get(manifestHref!)
  expect(manifestResponse.status(), 'manifest 应返回 200').toBe(200)
  expect(manifestResponse.headers()['content-type'], 'manifest 应是 JSON').toMatch(
    /^application\/json\b/,
  )
  expect(manifestResponse.headers()['cache-control']).toBe('no-cache')
  const manifest = (await manifestResponse.json()) as Manifest
  expect(manifest).toMatchObject({
    name: '部门工单平台',
    short_name: '工单',
    start_url: '/',
    scope: '/',
    display: 'standalone',
  })
  expect(manifest.icons.map((icon) => icon.sizes)).toEqual(
    expect.arrayContaining(['192x192', '512x512']),
  )
  expect(
    manifest.icons
      .filter((icon) => icon.purpose === 'maskable')
      .map((icon) => icon.sizes),
  ).toContain('512x512')

  for (const icon of manifest.icons) {
    await expectImage(await request.get(icon.src), icon.src)
  }
  await expectImage(await request.get(appleTouchIconHref!), appleTouchIconHref!)

  const registration = await page.evaluate(async () => {
    const ready = await navigator.serviceWorker.ready
    return { scope: ready.scope }
  })
  expect(new URL(registration.scope).pathname).toBe('/')

  // 未知路径会回退到 index.html（同样 200 + no-cache），所以还要看类型确认是脚本本身。
  const serviceWorkerResponse = await request.get('/service-worker.js')
  expect(serviceWorkerResponse.status(), 'service worker 应返回 200').toBe(200)
  expect(serviceWorkerResponse.headers()['content-type']).toMatch(/javascript/)
  expect(serviceWorkerResponse.headers()['cache-control']).toBe('no-cache')
})
