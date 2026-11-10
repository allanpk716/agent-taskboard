// 单独新建访客（#339）：从访客名单浮动「＋ → 新建访客」进入单页，
// 填名字时实时提示重名/相似访客（“就是 TA”直达详情），建立后的结果页
// 能复制主页文案并进入访客详情。“可能是 TA 的老工单”与摘要栏在 #343。
import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, login, loginAdmin } from './helpers'

const RUN = String(Date.now() % 1_000_000)

async function seedExistingGuest(
  request: APIRequestContext,
  name: string,
): Promise<number> {
  await loginAdmin(request)
  const ticket = await request.post('/api/v1/tickets', {
    data: { title: `${name}的血常规复查`, brief_md: '请补充结果。', labels: ['机型:UD-2300'] },
  })
  expect(ticket.status(), `建单失败：${await ticket.text()}`).toBe(201)
  const number = ((await ticket.json()) as { number: string }).number
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '补全结果', new_guest_name: name, valid_days: 30 },
  })
  expect(link.status(), `创建访客链接失败：${await link.text()}`).toBe(201)
  return ((await link.json()) as { guest_id: number }).guest_id
}

test('从名单浮动加号新建访客，结果页复制主页文案并进入详情', async ({ page, request }) => {
  await loginAdmin(request)
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)
  await page.goto('/guests')

  // 名单浮动「＋ → 新建访客」进入。
  await page.getByRole('button', { name: '＋' }).click()
  await page.getByRole('link', { name: /新建访客/ }).click()
  await expect(page).toHaveURL(/\/guests\/new$/)

  const name = `检验科-李四${RUN}`
  await page.getByLabel('访客名字').fill(name)
  await page.getByRole('button', { name: '建立访客' }).click()

  // 结果页：未认领、名下没有链接；复制主页文案与 GuestDetailPage 同一套。
  const result = page.getByRole('region', { name: '新建访客结果' })
  await expect(result).toContainText(name)
  await expect(result).toContainText('未认领')
  await expect(result).toContainText('0 条访客链接')
  await result.getByRole('button', { name: '复制主页文案' }).click()
  await expect(page.locator('#toast')).toContainText('主页文案已复制')
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  expect(copied).toContain(name)
  expect(copied).toMatch(/\/h\/tbh_/)

  await result.getByRole('link', { name: '去访客详情' }).click()
  await expect(page).toHaveURL(new RegExp(`/guests/\\d+$`))
  await expect(page.locator('.guest-profile')).toContainText(name)
})

test('输入相似名字提示已有访客，就是TA直达详情', async ({ page, request }) => {
  const name = `检验科-赵六${RUN}`
  const guestId = await seedExistingGuest(request, name)
  await login(page, ADMIN)
  await page.goto('/guests/new')

  // 换个部门前缀：去掉“部门-”后与已有访客相同，应提示。
  await page.getByLabel('访客名字').fill(`设备科-赵六${RUN}`)
  const hint = page.getByRole('region', { name: '相似访客提示' })
  await expect(hint).toContainText(name)
  await hint.getByRole('button', { name: '就是 TA' }).click()
  await expect(page).toHaveURL(new RegExp(`/guests/${guestId}$`))
  await expect(page.locator('.guest-profile')).toContainText(name)
})
