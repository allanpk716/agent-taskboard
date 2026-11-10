import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, login, loginAdmin } from './helpers'

// #340：先选访客再建单。选访客（搜索、最近联系、相似提示、当场新建）→
// 选模板 → 填写（顶部固定“发给：某某”，可回去改）→ 完成页转发文案；
// 访客第一次收到工单时醒目提示文案里带了主页地址。
const RUN = String(Date.now() % 1_000_000)
let seedSeq = 0

async function seedGuest(request: APIRequestContext, name: string): Promise<number> {
  const res = await request.post('/api/v1/guests', { data: { name } })
  expect(res.status(), `新建访客 ${name} 失败：${await res.text()}`).toBe(201)
  return ((await res.json()) as { guest: { id: number } }).guest.id
}

/** 最近发过：只要成员建过链接，即使访客从未打开也会出现在向导第一步。 */
async function seedSentGuest(request: APIRequestContext, name: string): Promise<void> {
  const ticket = await request.post('/api/v1/tickets', {
    data: {
      title: `${name} 的未打开工单`,
      brief_md: '用于产生最近发送。',
      labels: ['机型:UD-2300'],
    },
  })
  expect(ticket.status(), `建单失败：${await ticket.text()}`).toBe(201)
  const number = ((await ticket.json()) as { number: string }).number
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '已发送未打开', new_guest_name: name, valid_days: 30 },
  })
  expect(link.status(), `给 ${name} 建链接失败：${await link.text()}`).toBe(201)
}

test('向导主流程：搜索选访客 → 模板 → 填写可改访客 → 完成页带主页提示', async ({
  page,
  request,
}) => {
  await loginAdmin(request)
  const run = `${RUN}${++seedSeq}`
  const zhang = `检验科-张三${run}`
  const li = `检验科-李四${run}`
  await seedGuest(request, zhang)
  await seedGuest(request, li)
  await seedSentGuest(request, `设备科-王五${run}`)
  await login(page, ADMIN)

  // 第一步：选访客。搜索过滤、最近发过（不要求访客已打开）、名下工单数与认领状态。
  await page.goto('/tickets/new')
  await expect(page.getByRole('heading', { name: '选访客' })).toBeVisible()
  await expect(page.getByRole('region', { name: '最近发过' })).toContainText('王五')
  await page.getByLabel('搜索访客').fill(zhang)
  const zhangRow = page.getByRole('button', { name: new RegExp(zhang) })
  await expect(zhangRow).toBeVisible()
  await expect(zhangRow).toContainText('未认领')
  await expect(zhangRow).toContainText('0 张工单')
  await expect(page.getByRole('button', { name: new RegExp(li) })).toHaveCount(0)
  zhangRow.click()

  // 第二步：模板；可回第一步重选访客。
  await expect(page.getByRole('heading', { name: '选一个模板' })).toBeVisible()
  await page.getByRole('button', { name: '← 选访客' }).click()
  await expect(page.getByRole('heading', { name: '选访客' })).toBeVisible()
  await page.getByLabel('搜索访客').fill(zhang)
  await page.getByRole('button', { name: new RegExp(zhang) }).click()
  await page.locator('.tplpick', { hasText: '分配任务' }).click()

  // 第三步：填写。顶部固定“发给：某某”，点它回第一步改人。
  await expect(page.getByTestId('guest-send-bar')).toContainText(`发给：${zhang}`)
  await page.getByRole('button', { name: '改访客' }).click()
  await expect(page.getByRole('heading', { name: '选访客' })).toBeVisible()
  await page.getByLabel('搜索访客').fill(zhang)
  await page.getByRole('button', { name: new RegExp(zhang) }).click()
  await page.locator('.tplpick', { hasText: '分配任务' }).click()
  await page.getByLabel('什么事').fill('向导主流程：整理质控规则对照表')
  await page.getByRole('button', { name: '生成链接' }).click()

  // 第四步：完成。首次收到工单 → 转发文案带主页地址并醒目提示。
  const done = page.locator('.donet')
  await expect(done).toContainText(/已创建/)
  const number = (await done.locator('b').textContent()) ?? ''
  const share = page.locator('.linkbox')
  await expect(share).toContainText(zhang)
  await expect(share).toContainText('/h/')
  const notice = page.getByRole('alert')
  await expect(notice).toContainText('第一次收到工单')
  await expect(notice).toContainText('主页地址')
  const res = await page.request.get(`/api/v1/tickets/${number}`)
  expect(res.ok()).toBeTruthy()
  expect(((await res.json()) as { template: string }).template).toBe('assign-task')
})

test('访客详情「给 TA 发新工单」带访客 id 进入并跳过选访客', async ({ page, request }) => {
  await loginAdmin(request)
  const run = `${RUN}${++seedSeq}`
  const name = `检验科-周七${run}`
  const id = await seedGuest(request, name)
  await login(page, ADMIN)

  await page.goto(`/guests/${id}`)
  await page.getByRole('link', { name: '给 TA 发新工单' }).click()
  await expect(page).toHaveURL(new RegExp(`/tickets/new\\?guest=${id}$`))
  await expect(page.getByRole('heading', { name: '选访客' })).toHaveCount(0)
  await expect(page.getByRole('heading', { name: '选一个模板' })).toBeVisible()
  await page.locator('.tplpick', { hasText: '自由格式' }).click()
  await expect(page.getByTestId('guest-send-bar')).toContainText(`发给：${name}`)
})

test('单独新建访客结果页可带新访客进入向导', async ({ page, request }) => {
  await loginAdmin(request)
  const name = `门诊科-赵八${RUN}${++seedSeq}`
  await login(page, ADMIN)

  await page.goto('/guests/new')
  await page.getByLabel('访客名字').fill(name)
  await page.getByRole('button', { name: '建立访客' }).click()
  await expect(page.getByRole('region', { name: '新建访客结果' })).toContainText(name)
  await page.getByRole('link', { name: '给 TA 发新工单' }).click()
  await expect(page).toHaveURL(/\/tickets\/new\?guest=\d+$/)
  await expect(page.getByRole('heading', { name: '选一个模板' })).toBeVisible()
})
