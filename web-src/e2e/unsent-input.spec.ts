// 成员端未提交的输入（#262）：建单表单、工单回复框、说明修订框里没提交的
// 内容实时写进本机 localStorage，刷新后自动填回；提交成功（2xx）后清除。
import { expect, test } from '@playwright/test'
import { ADMIN, createTicket, login } from './helpers'

test('建单表单未提交的内容刷新后恢复，建单成功后清除', async ({ page }) => {
  await login(page, ADMIN)
  await page.goto('/new')
  await page.getByRole('button', { name: /我自己填/ }).click()
  await page.locator('.tplpick', { hasText: '自由格式' }).click()

  await page.getByLabel('标题').fill('未提交的建单标题')
  await page.getByLabel('说明').fill('说明写了一半。')
  await page.getByLabel('标签输入').fill('机型:UD-2300')
  await page.getByRole('button', { name: '添加', exact: true }).click()
  // 添加标签要先经服务端 resolve，等输入框清空、标签上墙后再刷新。
  await expect(page.getByLabel('标签输入')).toHaveValue('')
  await expect(page.locator('.lblrow')).toContainText('机型:UD-2300')
  await page.getByLabel('指派给').selectOption('王工')

  // 刷新后回到同一张表单：文字内容自动填回（暂存附件是本机文件对象，无法恢复）。
  await page.reload()
  await page.getByRole('button', { name: /我自己填/ }).click()
  await page.locator('.tplpick', { hasText: '自由格式' }).click()
  await expect(page.getByLabel('标题')).toHaveValue('未提交的建单标题')
  await expect(page.getByLabel('说明')).toHaveValue('说明写了一半。')
  await expect(page.locator('.lblrow')).toContainText('机型:UD-2300')
  await expect(page.getByLabel('指派给')).toHaveValue('王工')

  // 建单成功后本机保存的内容清除。
  await page.getByRole('button', { name: '创建工单' }).click()
  await expect(page.locator('.donet')).toContainText('已创建')
  await page.goto('/new')
  await page.getByRole('button', { name: /我自己填/ }).click()
  await page.locator('.tplpick', { hasText: '自由格式' }).click()
  await expect(page.getByLabel('标题')).toHaveValue('')
  await expect(page.getByLabel('说明')).toHaveValue('')
})

test('工单回复未提交的内容刷新后恢复，发送成功后清除', async ({ page, request }) => {
  const number = await createTicket(request, { title: '未提交回复的工单', assignee: '管理员' })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.getByPlaceholder(/写回复/).fill('回复写了一半。')
  await page.reload()
  await expect(page.getByPlaceholder(/写回复/)).toHaveValue('回复写了一半。')

  await page.getByRole('button', { name: '发送', exact: true }).click()
  await expect(page.locator('[data-bubble="reply"]').last()).toContainText('回复写了一半。')
  await page.reload()
  await expect(page.getByPlaceholder(/写回复/)).toHaveValue('')
  const keys = await page.evaluate(() => Object.keys(localStorage).filter((k) => k.includes(':reply:')))
  expect(keys).toEqual([])
})

test('说明修订未提交的内容刷新后恢复，保存成功后清除', async ({ page, request }) => {
  const number = await createTicket(request, { title: '未提交说明修订的工单' })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.getByRole('button', { name: '修改说明' }).click()
  await page.locator('#be-markdown').fill('说明改到一半。')
  await page.reload()
  await page.getByRole('button', { name: '修改说明' }).click()
  await expect(page.locator('#be-markdown')).toHaveValue('说明改到一半。')

  await page.getByRole('button', { name: '保存修改' }).click()
  await expect(page.locator('.brief-edit-dialog')).toHaveCount(0)
  await expect(page.locator('[data-bubble="brief"]')).toContainText('说明改到一半。')
  await page.reload()
  await page.getByRole('button', { name: '修改说明' }).click()
  // 重新打开时显示的是已保存的说明本身，本机不再留有未提交的修订。
  await expect(page.locator('#be-markdown')).toHaveValue('说明改到一半。')
  const keys = await page.evaluate(() => Object.keys(localStorage).filter((k) => k.includes(':brief:')))
  expect(keys).toEqual([])
})

test('说明修订恢复后沿用旧版本，远端已改时保存提示冲突', async ({ page, request }) => {
  const number = await createTicket(request, { title: '说明修订冲突的工单' })
  // 用创建者的直发 Agent 从 API 修改说明，模拟另一个编辑方推进版本。
  const tokenRes = await request.post('/api/v1/me/agent-tokens', {
    data: { name: `说明修订冲突-${Date.now()}`, draft_mode: 'direct' },
  })
  expect(tokenRes.status(), '签发 Agent 失败').toBe(201)
  const token = ((await tokenRes.json()) as { token: string }).token

  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await page.getByRole('button', { name: '修改说明' }).click()
  await page.locator('#be-markdown').fill('本机改到一半的说明。')
  await expect
    .poll(async () => page.evaluate(() => Object.keys(localStorage).filter((k) => k.includes(':brief:')).length))
    .toBe(1)

  const revised = await request.patch(`/api/v1/tickets/${number}`, {
    data: { brief_md: '另一方改过后的说明。', base_revision: 1 },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(revised.status(), `另一方修改说明失败：${await revised.text()}`).toBe(200)

  await page.reload()
  await page.getByRole('button', { name: '修改说明' }).click()
  await expect(page.locator('#be-markdown')).toHaveValue('本机改到一半的说明。')
  await expect(page.locator('.brief-edit-dialog')).toContainText(
    '说明在你上次编辑后已被修改过，下面是你本机保存的修订；直接保存会提示冲突，请先复制好你的内容。',
  )

  await page.getByRole('button', { name: '保存修改' }).click()
  await expect(page.locator('.brief-edit-dialog .errbox', { hasText: 'revision_conflict' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.locator('.brief-edit-dialog')).toHaveCount(0)
  await expect(page.locator('[data-bubble="brief"]')).toContainText('另一方改过后的说明。')
  await expect(page.locator('[data-bubble="brief"]')).not.toContainText('本机改到一半的说明。')
})

test('点取消丢弃本机保存的说明修订，重新打开显示原说明', async ({ page, request }) => {
  const number = await createTicket(request, { title: '取消说明修订的工单' })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.getByRole('button', { name: '修改说明' }).click()
  await page.locator('#be-markdown').fill('改了但决定取消。')
  await expect
    .poll(async () => page.evaluate(() => Object.keys(localStorage).filter((k) => k.includes(':brief:')).length))
    .toBe(1)
  await page.getByRole('button', { name: '取消', exact: true }).click()
  await expect(page.locator('.brief-edit-dialog')).toHaveCount(0)

  await page.getByRole('button', { name: '修改说明' }).click()
  await expect(page.locator('#be-markdown')).toHaveValue('取消说明修订的工单 的说明。')
  const keys = await page.evaluate(() => Object.keys(localStorage).filter((k) => k.includes(':brief:')))
  expect(keys).toEqual([])
})
