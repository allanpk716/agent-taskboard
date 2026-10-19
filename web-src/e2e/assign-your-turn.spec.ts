import { expect, test } from '@playwright/test'
import { ADMIN, createTicket, login, loginAdmin } from './helpers'

test('改派下拉排除当前指派人与停用成员', async ({ page, request }) => {
  await loginAdmin(request)
  const suffix = Date.now()
  const createMember = async (name: string) => {
    const res = await request.post('/api/v1/members', {
      data: { username: `assign-${suffix}-${name}`, name },
    })
    expect(res.status(), await res.text()).toBe(201)
    const body = await res.json()
    return body.member.id as number
  }
  const deactivatedID = await createMember('停用候选人')
  await createMember('可用候选人')
  await request.post(`/api/v1/members/${deactivatedID}/deactivate`)
  const number = await createTicket(request, { title: `改派下拉工单 ${suffix}`, assignee: '王工' })

  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await page.getByRole('button', { name: '👤 改派' }).click()
  const names = await page.locator('#as-sel option').allTextContents()
  expect(names).not.toContain('王工')
  expect(names).not.toContain('停用候选人')
  expect(names).toContain('可用候选人')
  const reactivated = await request.post(`/api/v1/members/${deactivatedID}/reactivate`)
  expect(reactivated.ok(), await reactivated.text()).toBeTruthy()
})

test('轮到本人时详情与列表显示轮到你', async ({ page, request }) => {
  const suffix = Date.now()
  const mine = await createTicket(request, { title: `自派自轮到你 ${suffix}`, assignee: '管理员' })
  const other = await createTicket(request, { title: `等待别人 ${suffix}`, assignee: '王工' })
  await login(page, ADMIN)

  await page.goto(`/t/${mine}`)
  await expect(page.locator('.dhead .badge.myturn')).toContainText('现在轮到你（')
  await expect(page.locator('.kv', { hasText: '等待方' })).toContainText('轮到你 ·')
  await expect(page.locator('.badge.myturn')).toContainText('现在轮到你')

  await page.goto('/tickets')
  const mineCard = page.locator('.tcard', { hasText: mine })
  await expect(mineCard).toContainText('轮到你 ·')
  await expect(mineCard).not.toContainText('等待 管理员')
  const otherCard = page.locator('.tcard', { hasText: other })
  await expect(otherCard).toContainText('等待 王工')
})
