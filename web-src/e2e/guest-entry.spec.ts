import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, login, loginAdmin } from './helpers'

// #345：访客入口提到一级（手机「工单｜访客」分段、电脑侧栏一级入口），
// 名单改成通讯录式：搜索、最近联系横排、按部门分组、待处理数、浮动「＋」。
// 服务数据目录跨轮保留，用带轮次后缀的访客名避免定位到旧数据。
const RUN = String(Date.now() % 1_000_000)
let seedSeq = 0

async function seedGuestList(request: APIRequestContext) {
  await loginAdmin(request)
  // 每次造数用独立后缀：同一轮里两个用例各造一套，互不干扰。
  const run = `${RUN}${++seedSeq}`
  const zhangName = `检验科-张三${run}`
  const wangDept = `设备科-王五${run}`
  const wangNoDept = `王五${run}`

  const createTicket = async (title: string) => {
    const res = await request.post('/api/v1/tickets', {
      data: { title, brief_md: '请补充现场情况。', labels: ['机型:UD-2300'] },
    })
    expect(res.status(), `建单 ${title} 失败：${await res.text()}`).toBe(201)
    return ((await res.json()) as { number: string }).number
  }

  // ZHANG：一张待补全（待处理 1），且最近打开过（最近联系横排）。
  const zhangTicket = await createTicket(`待${zhangName}补全的说明`)
  const zhangLink = await request.post(`/api/v1/tickets/${zhangTicket}/guest-links`, {
    data: { note: '补全说明', new_guest_name: zhangName, valid_days: 30, can_edit_brief: true },
  })
  expect(zhangLink.status(), await zhangLink.text()).toBe(201)
  const zhang = (await zhangLink.json()) as { guest_id: number; url: string }
  const claim = await request.post('/api/v1/guest-links/claim', {
    data: { author_name: '张三' },
    headers: { Authorization: `Bearer ${zhang.url.slice('/g/'.length)}` },
  })
  expect(claim.status(), `认领访客链接失败：${await claim.text()}`).toBe(201)
  const device = ((await claim.json()) as { device_token: string }).device_token
  const viewed = await request.get(`/api/v1/tickets/${zhangTicket}`, {
    headers: { Authorization: `Bearer ${device}` },
  })
  expect(viewed.status(), '访客打开工单失败').toBe(200)

  // 有部门的 WANG_DEPT 与没有“-”的 WANG_NO_DEPT（归未分组），都不轮到访客。
  for (const name of [wangDept, wangNoDept]) {
    const number = await createTicket(`${name}的工单`)
    const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
      data: { note: `联系${name}`, new_guest_name: name, valid_days: 30 },
    })
    expect(link.status(), await link.text()).toBe(201)
  }
  return { run, zhang: zhangName, wangDept, wangNoDept }
}

test('手机：工单标签顶部分段切到通讯录式访客名单', async ({ page, request }) => {
  const seeded = await seedGuestList(request)
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '工单' }).click()
  await expect(page).toHaveURL(/\/tickets$/)

  const segment = page.getByRole('tablist', { name: '工单或访客' })
  await expect(segment).toBeVisible()
  await expect(segment.getByRole('tab', { name: '工单' })).toHaveAttribute('aria-selected', 'true')
  await segment.getByRole('tab', { name: '访客' }).click()
  await expect(page).toHaveURL(/\/guests$/)

  // 只看本轮造的三位访客，避免旧轮数据干扰分组断言。
  await page.getByLabel('搜索访客').fill(seeded.run)

  // 通讯录：最近联系横排 + 按部门分组（无“-”归未分组）+ 待处理数。
  const recent = page.getByRole('region', { name: '最近联系' })
  await expect(recent).toContainText('张三')
  await expect(recent).not.toContainText('王五')

  const zhangRow = page.getByRole('link', { name: new RegExp(seeded.zhang) })
  await expect(zhangRow).toBeVisible()
  await expect(zhangRow).toContainText('待处理')
  await expect(zhangRow).toContainText('1')
  await expect(page.getByRole('group', { name: '检验科' })).toContainText('张三')
  await expect(page.getByRole('group', { name: '设备科' })).toContainText('王五')
  await expect(page.getByRole('group', { name: '未分组' })).toContainText(seeded.wangNoDept)

  // 搜索：按名字或部门（部门是名字前缀）过滤。
  await page.getByLabel('搜索访客').fill(seeded.zhang)
  await expect(page.getByRole('link', { name: new RegExp(seeded.zhang) })).toBeVisible()
  await expect(page.getByRole('link', { name: new RegExp(seeded.wangDept) })).toHaveCount(0)
  await expect(page.getByRole('group', { name: '未分组' })).toHaveCount(0)

  // 浮动「＋」两项入口。
  await page.getByRole('button', { name: '＋' }).click()
  await expect(page.getByRole('link', { name: /新建访客/ })).toHaveAttribute('href', '/guests/new')
  await expect(page.getByRole('link', { name: /给访客发新工单/ })).toHaveAttribute('href', '/tickets/new')
})

test('电脑：侧边栏一级「访客」入口，工具页不再有访客管理', async ({ page, request }) => {
  const seeded = await seedGuestList(request)
  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, ADMIN)

  const sidebar = page.getByRole('navigation', { name: '侧栏' })
  await sidebar.getByRole('link', { name: '访客' }).click()
  await expect(page).toHaveURL(/\/guests$/)
  await page.getByLabel('搜索访客').fill(RUN)
  await expect(page.getByRole('link', { name: new RegExp(seeded.zhang) })).toBeVisible()

  await sidebar.getByRole('link', { name: '工具' }).click()
  await expect(page).toHaveURL(/\/tools$/)
  await expect(page.getByText('访客管理')).toHaveCount(0)
})
