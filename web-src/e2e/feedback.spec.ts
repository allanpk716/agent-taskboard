import { test, expect } from '@playwright/test'
import { ADMIN, login } from './helpers'

// 建单两屏流程（#195）：第一屏选谁来填写；「发链接请人填写」进入向导剩余
// 部分，一次建好工单和带「可填写说明」的访客链接。

test('入口与两屏：第一屏选择由谁填写，第二屏显示适用场景并可返回', async ({ page }) => {
  await login(page, ADMIN)

  await expect(
    page.getByText('要建一张工单？派任务、提需求、问问题，或者请部门外的人来填，都从这里开始。'),
  ).toBeVisible()
  await expect(page.getByRole('button', { name: '＋ 建单' })).toHaveCount(1)
  await page.getByRole('button', { name: '＋ 建单' }).click()

  await expect(page.locator('.bpage').getByRole('heading', { name: '建单' })).toBeVisible()
  await expect(page.getByText('先选这张工单的说明由谁来填写')).toBeVisible()
  const self = page.getByRole('button', { name: /我自己填/ })
  const guest = page.getByRole('button', { name: /发链接请人填写/ })
  await expect(self).toContainText('派任务、提需求、问问题：现在就把说明填好，再指派给同事')
  await expect(self).toContainText('下一步：选模板 →')
  await expect(guest).toContainText('给部门外的人发链接，由对方填写，对方不需要账号')
  await expect(guest).toContainText('下一步：选模板 →')

  await guest.click()
  await expect(page.getByRole('heading', { name: '选一个模板' })).toBeVisible()
  await expect(page.getByText('发链接请人填写')).toBeVisible()
  await expect(page.getByText('对方会按这个模板的引导来填写')).toBeVisible()
  const bug = page.getByRole('button', { name: /^Bug 反馈 反馈/ })
  await expect(bug).toContainText('反馈')
  await expect(bug).toContainText('适用：仪器或软件坏了、行为不对')
  const freeForm = page.locator('.tplpick', { hasText: '自由格式' })
  await expect(freeForm).toContainText('不用模板，对方自由填写，工单类型为反馈')

  await page.getByRole('button', { name: '← 建单' }).click()
  await expect(page.getByText('先选这张工单的说明由谁来填写')).toBeVisible()
  await self.click()
  await expect(page.getByText('我自己填')).toBeVisible()
  await expect(page.getByText('按你要做的事选，模板决定要填哪些内容')).toBeVisible()
})

test('发链接请人填写：选模板后填请谁填写，生成可用链接', async ({ page }) => {
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)

  await page.getByRole('button', { name: '＋ 建单' }).click()
  await page.getByRole('button', { name: /发链接请人填写/ }).click()
  await page.locator('.tplpick', { hasText: '分配任务' }).click()

  await expect(page.locator('.b-title')).toHaveText('发链接请人填写')
  await expect(page.getByText('发链接请人填写 · 分配任务')).toBeVisible()
  await expect(page.locator('.fr-tpl-group')).toHaveCount(0)
  await expect(page.getByLabel('请谁反馈')).toHaveCount(0)
  const gen = page.getByRole('button', { name: '生成链接' })
  await expect(gen).toBeDisabled()
  await page.getByLabel('请谁填写').fill('检验科-张三')
  await expect(gen).toBeDisabled()
  await page.getByLabel('什么事').fill('整理质控规则对照表')
  await expect(gen).toBeEnabled()
  await gen.click()

  const done = page.locator('.donet')
  await expect(done).toContainText(/已创建/)
  const number = (await done.locator('b').textContent()) ?? ''
  const box = page.locator('.linkbox')
  await expect(box).toContainText('检验科-张三')
  await expect(box).toContainText('整理质控规则对照表')
  await expect(box).toContainText('/g/')
  const res = await page.request.get(`/api/v1/tickets/${number}`)
  expect(res.ok()).toBeTruthy()
  const body = await res.json()
  expect(body.kind).toBe('task')
  expect(body.template).toBe('assign-task')
})

test('我自己填：选提需求后填写表单并建单成功', async ({ page }) => {
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '＋ 建单' }).click()
  await page.getByRole('button', { name: /我自己填/ }).click()
  const request = page.locator('.tplpick', { hasText: '提需求' })
  await expect(request).toContainText('任务')
  await request.click()

  await page.getByLabel('标题').fill('两屏流程自己填建单')
  await page
    .locator('.fld', { hasText: '为什么要做？有什么背景？' })
    .locator('textarea')
    .fill('质控规则分散，需要集中整理。')
  await page.getByLabel('标签输入').fill('机型:UD-2300')
  await page.getByRole('button', { name: '添加' }).click()
  await expect(page.locator('.lblrow .chip').filter({ hasText: '机型:UD-2300' })).toBeVisible()
  await page.getByRole('button', { name: '创建工单' }).click()
  await page.getByRole('button', { name: '仍然创建' }).click()
  await expect(page.locator('.donet')).toContainText('已创建')
})

test('旧「请人反馈」地址直接进入发链接请人填写的第二屏', async ({ page }) => {
  await login(page, ADMIN)
  await page.goto('/feedback')
  await expect(page.getByRole('heading', { name: '选一个模板' })).toBeVisible()
  await expect(page.locator('.b-title')).toHaveText('建单')
  await expect(page.getByText('发链接请人填写')).toBeVisible()
  await expect(page.getByText('对方会按这个模板的引导来填写')).toBeVisible()
  await expect(page.getByRole('button', { name: /我自己填/ })).toHaveCount(0)
  await page.locator('.tplpick', { hasText: '分配任务' }).click()
  await expect(page.locator('.b-title')).toHaveText('发链接请人填写')
})
