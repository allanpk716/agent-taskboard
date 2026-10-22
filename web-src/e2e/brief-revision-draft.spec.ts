import { expect, test, type APIRequestContext } from '@playwright/test'
import { claimGuestLink, login, loginAdmin, markDraftIntroSeen, wang } from './helpers'

async function issueRequiredAgent(request: APIRequestContext): Promise<string> {
  await request.post('/api/v1/session', { data: await wang() })
  const res = await request.post('/api/v1/me/agent-tokens', {
    data: { name: `修订笔记本-${Date.now()}`, draft_mode: 'required' },
  })
  expect(res.status()).toBe(201)
  await markDraftIntroSeen(request)
  return ((await res.json()) as { token: string }).token
}

test('Agent 说明修订草稿按字段确认并保留起草过程', async ({ page, request }) => {
  const token = await issueRequiredAgent(request)
  const created = await request.post('/api/v1/tickets', {
    data: {
      title: 'Agent 修订说明',
      brief_md: 'Agent 修订说明 的说明。',
      labels: ['机型:UD-2300'],
    },
  })
  expect(created.status(), '王工建单失败').toBe(201)
  const number = ((await created.json()) as { number: string }).number
  const drafted = await request.patch(`/api/v1/tickets/${number}`, {
    data: { title: '新标题', brief_md: '修订后的说明。', base_revision: 1 },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(drafted.status()).toBe(202)

  await login(page, await wang())
  await page.goto(`/t/${number}#draft`)
  const card = page.getByLabel('说明修订草稿')
  await expect(card).toContainText('草稿 · 未发出 · 只有你能看到')
  await expect(card).toContainText('Agent 修订说明')
  await expect(card).toContainText('新标题')
  await expect(card).toContainText('修订后的说明。')
  await card.getByRole('button', { name: '发出修订', exact: true }).click()

  const brief = page.locator('[data-bubble="brief"]')
  await expect(brief).toContainText('修订后的说明。')
  await expect(brief.getByRole('button', { name: /v1/ })).toBeVisible()
  await expect(brief).toContainText('查看起草过程（1 版）')
  await brief.getByRole('button', { name: '查看起草过程（1 版）' }).click()
  await brief.getByRole('button', { name: 'v1', exact: true }).last().click()
  await expect(brief.locator('.brief-compare.drafting')).toContainText(/说明修订 #2 的起草过程/)

  await loginAdmin(request)
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '检验科-张三' },
  })
  expect(link.status()).toBe(201)
  const key = ((await link.json()) as { url: string }).url.replace('/g/', '')
  const guestToken = await claimGuestLink(request, key)
  const guestView = await request.get(`/api/v1/tickets/${number}`, {
    headers: { Authorization: `Bearer ${guestToken}` },
  })
  expect(guestView.status()).toBe(200)
  expect((await guestView.json()).timeline.some((e: { drafting_count?: number }) => e.drafting_count)).toBe(false)
})
