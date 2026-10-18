import { expect, test } from '@playwright/test'
import { ADMIN, login, loginAdmin } from './helpers'

test('指派给已停用成员的工单显示停用徽标', async ({ page, request }) => {
  const title = `停用成员徽标工单 ${Date.now()}`
  await loginAdmin(request)
  const suffix = `${Date.now()}`
  const created = await request.post('/api/v1/members', {
    data: { username: `offline-${suffix}`, name: `停用成员${suffix}` },
  })
  expect(created.status(), await created.text()).toBe(201)
  const createdBody = await created.json()
  const assignee = createdBody.member.name
  const memberID = createdBody.member.id
  const password = createdBody.initial_password

  const ticket = await request.post('/api/v1/tickets', {
    data: {
      title,
      brief_md: `${title} 的说明。`,
      labels: ['机型:UD-2300'],
      assignee,
    },
  })
  expect(ticket.status(), await ticket.text()).toBe(201)
  const number = (await ticket.json()).number

  const memberLogin = await request.post('/api/v1/session', { data: { username: `offline-${suffix}`, password } })
  expect(memberLogin.ok(), await memberLogin.text()).toBeTruthy()
  const reply = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: '我先跟进这张工单。' },
  })
  expect(reply.status(), await reply.text()).toBe(201)
  const label = await request.post(`/api/v1/tickets/${number}/labels`, { data: { add: ['待初筛'] } })
  expect(label.status(), await label.text()).toBe(200)

  await loginAdmin(request)
  const deactivated = await request.post(`/api/v1/members/${memberID}/deactivate`)
  expect(deactivated.ok(), await deactivated.text()).toBeTruthy()

  try {
    await login(page, ADMIN)
    await page.goto(`/t/${number}`)
    await expect(page.locator('.kv', { hasText: '指派人' }).locator('.deactivated-badge')).toHaveText('已停用')
    await expect(page.locator('.bubble', { hasText: '我先跟进这张工单。' }).locator('.who .deactivated-badge')).toBeVisible()
    await expect(page.locator('.event', { hasText: '打上了标签' }).locator('.deactivated-badge')).toBeVisible()
  } finally {
    const reactivated = await request.post(`/api/v1/members/${memberID}/reactivate`)
    expect(reactivated.ok(), await reactivated.text()).toBeTruthy()
  }
})
