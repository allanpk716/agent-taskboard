import { expect, test, type APIRequestContext } from '@playwright/test'
import { loginAdmin } from './helpers'

async function createTicketWithGuest(
  request: APIRequestContext,
  title: string,
  guestID: number,
) {
  const ticket = await request.post('/api/v1/tickets', {
    data: { title, brief_md: '请补充现场情况。', labels: ['机型:UD-2300'] },
  })
  expect(ticket.status(), await ticket.text()).toBe(201)
  const number = ((await ticket.json()) as { number: string }).number
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '跟进处理', guest_id: guestID, valid_days: 30 },
  })
  expect(link.status(), await link.text()).toBe(201)
  return number
}

test('guest claims home, sees multiple tickets and replies from it', async ({ page, request }) => {
  await loginAdmin(request)
  const firstTicket = await request.post('/api/v1/tickets', {
    data: { title: '第一张主页工单', brief_md: '请补充现场情况。', labels: ['机型:UD-2300'] },
  })
  expect(firstTicket.status()).toBe(201)
  const first = ((await firstTicket.json()) as { number: string }).number
  const firstLink = await request.post(`/api/v1/tickets/${first}/guest-links`, {
    data: { note: '检验科联系', new_guest_name: '检验科-张三', valid_days: 30 },
  })
  expect(firstLink.status(), await firstLink.text()).toBe(201)
  const guestID = ((await firstLink.json()) as { guest_id: number }).guest_id
  const second = await createTicketWithGuest(request, '第二张主页工单', guestID)
  const homeRes = await request.post(`/api/v1/guests/${guestID}/home-share-text`)
  expect(homeRes.status(), await homeRes.text()).toBe(200)
  const home = (await homeRes.json()) as { home_url: string }
  expect(home.home_url).toMatch(/^\/h\/tbh_/)

  await page.goto(home.home_url)
  await expect(page.getByRole('heading', { name: '检验科-张三' })).toBeVisible()
  await page.locator('#guest-home-name').fill('检验科 张三')
  await page.getByRole('button', { name: '是我，打开主页' }).click()
  const code = page.locator('[data-access-code]')
  await expect(code).toHaveText(/^\d{6}$/)
  await page.getByRole('button', { name: '我已保存，打开主页' }).click()

  const tickets = page.locator('.guest-home-ticket')
  await expect(tickets).toHaveCount(2)
  await expect(page.locator('[data-ticket-number="' + first + '"]')).toContainText('第一张主页工单')
  await expect(page.locator('[data-ticket-number="' + second + '"]')).toContainText('第二张主页工单')

  await page.locator('[data-ticket-number="' + first + '"]').click()
  await expect(page.getByRole('heading', { name: '第一张主页工单' })).toBeVisible()
  await expect(page.locator('[data-return-guest-home]')).toContainText('返回我的主页')
  await page.locator('textarea').fill('主页进入后回复：已核对现场记录。')
  await page.getByRole('button', { name: '发送' }).click()
  await expect(page.getByText('主页进入后回复：已核对现场记录。')).toBeVisible()
})
