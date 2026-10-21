import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, createTicket, login, wang } from './helpers'

// Agent 接入、工单指令与交接文案：只从浏览器可见行为与剪贴板断言。

async function issueToken(request: APIRequestContext, who: { username: string; password: string }, name: string) {
  const session = await request.post('/api/v1/session', { data: who })
  expect(session.ok()).toBeTruthy()
  const res = await request.post('/api/v1/me/agent-tokens', { data: { name } })
  expect(res.ok(), await res.text()).toBeTruthy()
  return (await res.json()) as { id: number; token: string; onboarding_text: string }
}

test('发起人看到两个按钮，分别复制工单指令和交接文案', async ({ page, request }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const number = await createTicket(request, { title: '要交给王工Agent的工单', assignee: '王工' })
  await issueToken(request, ADMIN, '发起人交接')
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.locator('.actrow').getByRole('button', { name: '🤖 让我的 Agent 处理' }).click()
  let dialog = page.locator('.modal')
  await expect(dialog).toContainText('🤖 让我的 Agent 处理')
  await expect(dialog.locator('[data-handoff-preview]')).toContainText(
    '1. 凭证：用环境变量 TASKBOARD_TOKEN，请求头 Authorization: Bearer $TASKBOARD_TOKEN',
  )
  let preview = ((await dialog.locator('[data-handoff-preview]').textContent()) ?? '').trim()
  await dialog.getByRole('button', { name: '复制工单指令' }).click()
  await expect(dialog.getByRole('button', { name: '已复制' })).toBeVisible()
  expect((await page.evaluate(() => navigator.clipboard.readText())).replace(/\r\n/g, '\n')).toBe(preview)
  expect(preview).not.toMatch(/tbg_|tbk_|Authorization: Bearer tbag_/)
  await dialog.getByRole('button', { name: '关闭' }).click()

  await page.locator('.actrow').getByRole('button', { name: '📨 发给 王工' }).click()
  dialog = page.locator('.modal')
  await expect(dialog).toContainText('📨 发给 王工')
  await expect(dialog.locator('[data-handoff-preview]')).toContainText('给你派了一张工单')
  await expect(dialog.locator('[data-handoff-preview]')).toContainText('下面这段交给你的 Agent')
  preview = ((await dialog.locator('[data-handoff-preview]').textContent()) ?? '').trim()
  await dialog.getByRole('button', { name: '复制交接文案' }).click()
  await expect(dialog.getByRole('button', { name: '已复制' })).toBeVisible()
  expect((await page.evaluate(() => navigator.clipboard.readText())).replace(/\r\n/g, '\n')).toBe(preview)
  expect(preview).not.toMatch(/tbg_|tbk_|Authorization: Bearer tbag_/)
})

test('指派人只看到让我的Agent处理', async ({ page, request }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const number = await createTicket(request, { title: '指派人自己的Agent工单', assignee: '王工' })
  await login(page, await wang())
  await page.goto(`/t/${number}`)

  await expect(page.locator('.actrow').getByRole('button', { name: '让我的 Agent 处理' })).toBeVisible()
  await expect(page.locator('.actrow').getByRole('button', { name: /发给 王工/ })).toHaveCount(0)
  await page.locator('.actrow').getByRole('button', { name: '🤖 让我的 Agent 处理' }).click()
  const dialog = page.locator('.modal')
  await expect(dialog.locator('[data-handoff-preview]')).toContainText('请处理 agent-taskboard 上的工单')
  await expect(dialog.locator('[data-handoff-preview]')).not.toContainText('给你派了一张工单')
  await expect(dialog.getByRole('radio')).toHaveCount(0)
})

test('没有Agent token时顶部两步引导但不阻止复制', async ({ page, request }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const number = await createTicket(request, { title: '还没签发token的成员工单', assignee: '王工' })
  const worker = await wang()
  await login(page, worker)
  await page.goto(`/t/${number}`)
  await page.locator('.actrow').getByRole('button', { name: '🤖 让我的 Agent 处理' }).click()

  const guide = page.locator('.handoff-onboarding')
  await expect(guide).toContainText('你的 Agent 还没接入，第一次需要两步')
  await expect(guide).toContainText('生成 token，把接入文案贴给你的 Agent')
  await expect(page.getByRole('button', { name: '复制工单指令' })).toBeEnabled()
  await guide.getByRole('link', { name: '「我的 → Agent」' }).click()
  await expect(page).toHaveURL(/\/me$/)

  await issueToken(request, worker, '手机交接')
  await page.goto(`/t/${number}`)
  await page.locator('.actrow').getByRole('button', { name: '🤖 让我的 Agent 处理' }).click()
  await expect(page.locator('.handoff-onboarding')).toHaveCount(0)
})

test('手机菜单可以打开两个Agent入口', async ({ page, request }) => {
  const number = await createTicket(request, { title: '手机抽屉打开的交接工单', assignee: '王工' })
  await page.setViewportSize({ width: 375, height: 720 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.getByRole('button', { name: '更多操作' }).click()
  await page.getByRole('menuitem', { name: '🤖 让我的 Agent 处理' }).click()
  await expect(page.locator('.mask')).toBeVisible()
  await expect(page.locator('.modal')).toContainText('复制工单指令')
  await page.getByRole('button', { name: '关闭', exact: true }).click()

  await page.getByRole('button', { name: '更多操作' }).click()
  await page.getByRole('menuitem', { name: '📨 发给 王工' }).click()
  await expect(page.locator('.modal')).toContainText('复制交接文案')
})

test('改派确认后直接弹出发给指派人的交接文案', async ({ page, request }) => {
  const number = await createTicket(request, { title: '改派后交接的工单' })
  await issueToken(request, ADMIN, '改派交接')
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.locator('.actrow').getByRole('button', { name: /指派|改派/ }).click()
  await page.getByLabel('指派给').selectOption('王工')
  await page.getByRole('button', { name: /确认指派|确认改派/ }).click()
  const dialog = page.locator('.modal')
  await expect(dialog).toContainText('📨 发给 王工')
  await expect(dialog.locator('[data-handoff-preview]')).toContainText('转给你了')
  await expect(dialog.locator('[data-handoff-preview]')).toContainText('请处理 agent-taskboard 上的工单')
  await expect(dialog.getByRole('button', { name: '复制交接文案' })).toBeVisible()
})

test('建单指派给同事后完成区域有交接入口', async ({ page }) => {
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '建单' }).click()
  await page.getByRole('button', { name: /我自己填/ }).click()
  await page.getByText('自由格式').click()
  await page.getByLabel('标题').fill('建单后交给王工Agent')
  await page.getByLabel(/说明/).fill('完成后把清单发回来。')
  await page.getByLabel('标签输入').fill('1680')
  await page.getByRole('button', { name: '添加', exact: true }).click()
  await expect(page.locator('.lblrow .chip').filter({ hasText: '机型:US-1680' })).toBeVisible()
  await page.getByLabel('指派给').selectOption('王工')
  await page.getByRole('button', { name: '创建工单' }).click()

  await expect(page.locator('.donet')).toContainText('已创建')
  const done = page.locator('.bpage')
  await expect(done).toContainText('📨 发给 王工')
  await expect(done.locator('[data-handoff-preview]')).toContainText('给你派了一张工单')
  await expect(done.locator('[data-handoff-preview]')).toContainText('请处理 agent-taskboard 上的工单')
  await expect(done.getByRole('button', { name: '复制交接文案' })).toBeVisible()
})

test('建单完成区域按是否指派显示未指派提示', async ({ page }) => {
  await login(page, ADMIN)
  const hint = '未指派的工单不会出现在任何人的收件箱里，要在工单列表里找到它。'

  const createFreeForm = async (assignee: string) => {
    await page.goto('/new')
    await page.getByRole('button', { name: /我自己填/ }).click()
    await page.getByText('自由格式').click()
    await page.getByLabel('标题').fill(`建单指派状态 ${Date.now()} ${assignee || '暂不指派'}`)
    await page.getByLabel(/说明/).fill('检查完成区域的指派提示。')
    await page.getByLabel('标签输入').fill('1680')
    await page.getByRole('button', { name: '添加', exact: true }).click()
    await expect(page.getByLabel('标签输入')).toHaveValue('')
    await page.getByLabel('指派给').selectOption(assignee)
    await page.getByRole('button', { name: '创建工单' }).click()
    await expect(page.locator('.donet')).toContainText('已创建')
  }

  await createFreeForm('管理员')
  await expect(page.locator('.bpage')).not.toContainText(hint)

  await createFreeForm('')
  await expect(page.locator('.bpage')).toContainText(hint)

  await createFreeForm('王工')
  const done = page.locator('.bpage')
  await expect(done).toContainText('📨 发给 王工')
  await expect(done.locator('[data-handoff-role="hint"]')).toContainText(
    '复制后发给王工，他再把后半段交给自己的 Agent',
  )
  await expect(done).not.toContainText(hint)
})

test('指派人按成员id判断而不是重名判断', async ({ page, request }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const session = await request.post('/api/v1/session', { data: ADMIN })
  const adminName = (await session.json()).viewer.member.name
  const suffix = Date.now().toString(36)
  const member = await request.post('/api/v1/members', {
    data: { username: `same-${suffix}`, name: adminName },
  })
  expect(member.ok(), await member.text()).toBeTruthy()
  const memberID = (await member.json()).member.id
  const number = await createTicket(request, { title: '按id判断指派人的工单', assignee: String(memberID) })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.locator('.actrow').getByRole('button', { name: '🤖 让我的 Agent 处理' }).click()
  const dialog = page.locator('.modal')
  await expect(dialog.locator('[data-handoff-preview]')).toContainText('请处理 agent-taskboard 上的工单')
  await expect(dialog.getByRole('radio')).toHaveCount(0)
})

test('我的页面显示接入文案并支持换新', async ({ page, request }) => {
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '我的' }).click()
  const agentName = `接入${Date.now().toString(36)}`
  await page.getByRole('button', { name: '＋ 生成新 token' }).click()
  await page.getByLabel('名字').fill(agentName)
  await page.getByRole('button', { name: '生成', exact: true }).click()

  const dialog = page.locator('.modal')
  const preview = dialog.locator('[data-onboarding-preview]')
  await expect(preview).toContainText('你是我（管理员）在 agent-taskboard 上的成员 Agent')
  await expect(preview).toContainText('setx TASKBOARD_URL')
  await expect(preview).toContainText('setx TASKBOARD_TOKEN')
  const onboarding = ((await preview.textContent()) ?? '').trim()
  await dialog.getByRole('button', { name: '复制接入文案' }).click()
  expect((await page.evaluate(() => navigator.clipboard.readText())).replace(/\r\n/g, '\n')).toBe(onboarding)
  await dialog.getByRole('button', { name: '关闭', exact: true }).click()

  const row = page.locator(`tr[data-token="${agentName}"]`)
  await row.getByRole('button', { name: '换新' }).click()
  await expect(page.locator('[data-onboarding-preview]')).toContainText(
    `你是我（管理员）在 agent-taskboard 上的成员 Agent「${agentName}」`,
  )
  const renewed = ((await page.locator('[data-onboarding-preview]').textContent()) ?? '').trim()
  expect(renewed).not.toBe(onboarding)
  const newToken = (renewed.match(/tbag_[A-Za-z0-9_-]+/g) ?? [])[0]
  const oldToken = (onboarding.match(/tbag_[A-Za-z0-9_-]+/g) ?? [])[0]
  const old = await request.get('/api/v1/session', { headers: { Authorization: `Bearer ${oldToken}` } })
  expect(old.status()).toBe(401)
  const fresh = await request.get('/api/v1/session', { headers: { Authorization: `Bearer ${newToken}` } })
  expect(fresh.status()).toBe(200)
})
