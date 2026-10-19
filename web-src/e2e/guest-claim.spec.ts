import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, login, loginAdmin } from './helpers'

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
  const code = page.locator('[data-access-code]')
  await expect(code).toHaveText(/^\d{6}$/)
  await expect(page.locator('.claim-card')).toContainText('请截图保存访问码')
  const accessCode = await code.textContent()
  await page.getByRole('button', { name: '我已保存，打开工单' }).click()
  await expect(page.getByRole('heading', { name: '认领后才可见的工单标题' })).toBeVisible()
  await expect(page.locator('.privacy-note.persistent')).toContainText(
    '🔒 只有你和处理人（管理员）能看到这张工单。',
  )
  await page.reload()
  await expect(page.getByRole('heading', { name: '认领后才可见的工单标题' })).toBeVisible()
  await expect(page.locator('[data-access-code]')).toHaveCount(0)

  const other = await browser.newPage()
  await other.goto(`/g/${seed.linkKey}`)
  await expect(other.getByRole('heading', { name: '输入访问码' })).toBeVisible()
  await other.locator('#guest-access-code').fill(accessCode ?? '')
  await other.getByRole('button', { name: '凭访问码进入' }).click()
  await expect(other.getByRole('heading', { name: '认领后才可见的工单标题' })).toBeVisible()
  await other.close()

  for (let i = 0; i < 5; i++) {
    const wrong = await request.post('/api/v1/guest-links/enter', {
      data: { access_code: '000000' },
      headers: { Authorization: `Bearer ${seed.linkKey}` },
    })
    expect(wrong.status(), await wrong.text()).toBe(i === 4 ? 403 : 401)
  }
  await login(page, ADMIN)
  await page.goto(`/t/${seed.number}`)
  await page.getByRole('button', { name: '访客链接' }).click()
  const row = page.locator('tr', { hasText: '检验科-张三' })
  await expect(row.locator('.mini')).toContainText('输码已锁定')
  await row.getByRole('button', { name: '重置访问码' }).click()
  await expect(page.locator('[data-reset-code]')).toHaveText(/^\d{6}$/)
  await expect(page.locator('.okbox')).toContainText('新访问码只显示这一次')
})
