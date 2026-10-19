import { expect, test, type APIRequestContext } from '@playwright/test'
import { loginAdmin } from './helpers'

async function createGuestLink(request: APIRequestContext, title: string) {
  await loginAdmin(request)
  const ticket = await request.post('/api/v1/tickets', {
    data: { title, brief_md: '认领前不可见的说明', labels: ['机型:UD-2300'] },
  })
  expect(ticket.status()).toBe(201)
  const number = ((await ticket.json()) as { number: string }).number
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '检验科-张三', valid_days: 30 },
  })
  expect(link.status()).toBe(201)
  const body = (await link.json()) as { id: number; url: string; expires_at: string }
  return { number, id: body.id, linkKey: body.url.slice(3), expiresAt: body.expires_at }
}

test('guest claims a link before any ticket content is shown', async ({ page, request, browser }) => {
  const seed = await createGuestLink(request, '认领后才可见的工单标题')
  await page.goto(`/g/${seed.linkKey}`)

  const claim = page.locator('.claim-card')
  await expect(claim).toBeVisible()
  await expect(claim).toContainText('管理员 邀请你处理一张工单')
  await expect(claim).toContainText('链接备注：检验科-张三')
  await expect(claim).toContainText('有效期至：')
  await expect(claim).toContainText('🔒 这张工单只有你和处理人能看到')
  await expect(page.getByRole('heading', { name: '认领后才可见的工单标题' })).toHaveCount(0)
  await expect(page.locator('[data-bubble="brief"]')).toHaveCount(0)

  await page.locator('#guest-claim-name').fill('检验科 张三')
  await page.getByRole('button', { name: '是我，打开工单' }).click()
  await expect(page.getByRole('heading', { name: '认领后才可见的工单标题' })).toBeVisible()
  await expect(page.locator('.privacy-note.persistent')).toContainText(
    '🔒 只有你和处理人（管理员）能看到这张工单。',
  )

  const other = await browser.newPage()
  await other.goto(`/g/${seed.linkKey}`)
  await expect(other.getByRole('heading', { name: '这条链接已被认领' })).toBeVisible()
  await other.close()
})
