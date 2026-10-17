import { test, expect, type Browser } from '@playwright/test'
import { ADMIN, login } from './helpers'

// 「请人反馈」（#64）：收件箱卡片进入向导，一次建好工单和带「可填写说明」
// 权限的访客链接；完成页给出转发文案。对真实服务 + 构建好的前端跑用例。

// 1. 成员从收件箱卡片进入向导，填两项后生成链接；完成页显示编号和文案，
//    复制按钮可用；访客打开这条链接能看到工单（带「可填写说明」权限）。
test('请人反馈：向导生成链接，完成页可复制，访客能打开', async ({ page, browser }) => {
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)

  // 收件箱顶部的卡片「有人要反馈问题？」
  await expect(page.getByText('有人要反馈问题？')).toBeVisible()
  await page.getByRole('button', { name: '＋ 请人反馈' }).click()
  await expect(page.getByRole('heading', { name: '请人反馈' })).toBeVisible()

  // 两项必填没填完时「生成链接」不能点。
  const gen = page.getByRole('button', { name: '生成链接' })
  await expect(gen).toBeDisabled()
  await page.getByLabel('请谁反馈').fill('检验科-张三')
  await expect(gen).toBeDisabled()
  await page.getByLabel('什么事').fill('UD-2300 质控时报警 E-203')
  await expect(gen).toBeEnabled()

  // 模板选项：默认 Bug 反馈，下面有一句白话（分组未上线时取前三个字段的问句）。
  await expect(page.getByText('会问：').or(page.getByText(/哪台仪器/))).toBeVisible()

  // 更多选项默认折叠，摘要写「有效期 30 天 · 由我跟进」；展开改成 7 天。
  await expect(page.getByText('有效期 30 天 · 由我跟进')).toBeVisible()
  await page.getByText('更多选项').click()
  await page.getByRole('radio', { name: '7 天' }).check()
  await page.getByText('更多选项').click()

  await gen.click()

  // 完成页：工单编号 + 转发文案 + 复制按钮。
  const done = page.locator('.donet')
  await expect(done).toContainText(/已创建/)
  const number = (await done.locator('b').textContent()) ?? ''
  expect(number).toMatch(/^\d{8}-\d{3,}$/)
  const box = page.locator('.linkbox')
  await expect(box).toContainText('检验科-张三')
  await expect(box).toContainText('UD-2300 质控时报警 E-203')
  await expect(box).toContainText('/g/')

  await page.getByRole('button', { name: '复制文案' }).click()
  await expect
    .poll(() => page.evaluate(() => navigator.clipboard.readText()))
    .toContain('UD-2300 质控时报警 E-203')

  await page.getByRole('button', { name: '只复制链接' }).click()
  const linkUrl = await page.evaluate(() => navigator.clipboard.readText())
  expect(linkUrl).toMatch(/\/g\/tbg_/)

  // 打开工单 / 再请一个人 / 回收件箱都可达。
  await expect(page.getByRole('button', { name: '打开工单' })).toBeVisible()
  await expect(page.getByRole('button', { name: '再请一个人' })).toBeVisible()
  await expect(page.getByRole('button', { name: '回收件箱' })).toBeVisible()

  // 访客（不带成员会话）打开链接能看到工单；这条链接带「可填写说明」。
  const guest = await newGuestPage(browser, linkUrl)
  await expect(guest.getByRole('heading', { name: 'UD-2300 质控时报警 E-203' })).toBeVisible()
  await expect(guest.locator('.link-meta')).toContainText('检验科-张三')
  await expect(guest.getByRole('button', { name: '✏ 补全说明' })).toBeVisible()
  await guest.context().close()

  // 成员侧：工单确实存在（经 API 核对编号一致）。
  const res = await page.request.get(`/api/v1/tickets/${number}`)
  expect(res.ok()).toBeTruthy()
  const body = await res.json()
  expect(body.creator.member.name).toBe('管理员')
  const types = (body.timeline as { type: string }[]).map((e) => e.type)
  expect(types).toEqual(['created', 'assigned', 'guest_link_created']) // 跟进人默认本人
})

/** 不带成员会话的访客页（模拟对方点开链接）。 */
async function newGuestPage(browser: Browser, path: string) {
  const ctx = await browser.newContext({ baseURL: `http://127.0.0.1:${port()}` })
  const page = await ctx.newPage()
  await page.goto(path)
  return page
}

function port(): string {
  return process.env.E2E_PORT || '8901'
}

// 2. 向导底部有通往「建单（高级）」的链接；原建单页改了导航名，仍然能正常建单。
test('建单（高级）仍然能正常建单', async ({ page }) => {
  await login(page, ADMIN)

  // 底部标签改名「建单（高级）」；进入原建单页。
  await page.locator('.b-tabs').getByRole('link', { name: '建单（高级）' }).click()
  await expect(page.getByRole('heading', { name: '建单（高级）' })).toBeVisible()

  // 自由格式建一张（缺维度标签被拦 → 补上 → 创建成功）。
  await page.getByText('自由格式').click()
  await page.getByLabel('标题').fill('高级建单仍然可用')
  await page.getByRole('button', { name: '创建工单' }).click()
  await expect(page.locator('.errbox')).toContainText('dimension_label_required')
  await page.getByLabel('标签输入').fill('机型:UD-2300')
  await page.getByRole('button', { name: '添加' }).click()
  await page.getByRole('button', { name: '创建工单' }).click()
  await expect(page.locator('.donet')).toContainText('已创建')

  // 向导表单底部也有一条通往「建单（高级）」的链接。
  await page.goto('/')
  await page.getByRole('button', { name: '＋ 请人反馈' }).click()
  const adv = page.locator('.submitrow').getByRole('link', { name: '建单（高级）' })
  await expect(adv).toBeVisible()
  await adv.click()
  await expect(page.getByRole('heading', { name: '建单（高级）' })).toBeVisible()
})
