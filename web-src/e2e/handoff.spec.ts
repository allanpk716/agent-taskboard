import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, createTicket, login, markDraftIntroSeen, wang, loginWithRequest } from './helpers'

// Agent 接入、工单指令与交接文案：只从浏览器可见行为与剪贴板断言。

async function issueToken(request: APIRequestContext, who: { username: string; password: string }, name: string) {
  const session = await loginWithRequest(request, who)
  expect(session.ok()).toBeTruthy()
  const res = await request.post('/api/v1/me/agent-tokens', { data: { name } })
  expect(res.ok(), await res.text()).toBeTruthy()
  await markDraftIntroSeen(request)
  return (await res.json()) as { id: number; token: string; onboarding_text: string }
}

async function createMemberWithoutAgentToken(
  request: APIRequestContext,
): Promise<{ memberID: number; credentials: { username: string; password: string } }> {
  const session = await loginWithRequest(request, ADMIN)
  expect(session.ok()).toBeTruthy()
  const username = `handoff-notoken-${Date.now().toString(36)}`
  const created = await request.post('/api/v1/members', {
    data: { username, name: '无token成员' },
  })
  expect(created.ok(), await created.text()).toBeTruthy()
  const body = (await created.json()) as { member: { id: number }, initial_password: string }
  return {
    memberID: body.member.id,
    credentials: { username, password: body.initial_password },
  }
}

test('发起人看到两个按钮，分别复制工单指令和中性交接文案', async ({ page, request }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const number = await createTicket(request, { title: '要交给王工Agent的工单', assignee: '王工' })
  await issueToken(request, ADMIN, '发起人交接')
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.locator('.actrow').getByRole('button', { name: '🤖 复制给我的 Agent' }).click()
  let dialog = page.locator('.modal')
  await expect(dialog).toContainText('🤖 复制给我的 Agent')
  await expect(dialog.locator('[data-handoff-preview]')).toContainText(
    '1. 凭证：用环境变量 TASKBOARD_TOKEN，请求头 Authorization: Bearer $TASKBOARD_TOKEN',
  )
  let preview = ((await dialog.locator('[data-handoff-preview]').textContent()) ?? '').trim()
  await dialog.getByRole('button', { name: '复制工单指令' }).click()
  await expect(dialog.getByRole('button', { name: '已复制' })).toBeVisible()
  expect((await page.evaluate(() => navigator.clipboard.readText())).replace(/\r\n/g, '\n')).toBe(preview)
  expect(preview).not.toMatch(/tbg_|tbk_|Authorization: Bearer tbag_/)
  await dialog.getByRole('button', { name: '关闭' }).click()

  await page.locator('.actrow').getByRole('button', { name: '📨 复制给对方的 Agent' }).click()
  dialog = page.locator('.modal')
  await expect(dialog).toContainText('📨 复制给对方的 Agent')
  await expect(dialog.getByRole('radio', { name: '成员' })).toBeChecked()
  await expect(dialog.locator('[data-handoff-preview]')).toContainText('请让你的 Agent 处理工单')
  await expect(dialog.locator('[data-handoff-preview]')).not.toContainText('给你派了一张工单')
  await expect(dialog.locator('[data-handoff-preview]')).toContainText('下面这段交给你的 Agent')
  preview = ((await dialog.locator('[data-handoff-preview]').textContent()) ?? '').trim()
  await dialog.getByRole('button', { name: '复制交接文案' }).click()
  await expect(dialog.getByRole('button', { name: '已复制' })).toBeVisible()
  expect((await page.evaluate(() => navigator.clipboard.readText())).replace(/\r\n/g, '\n')).toBe(preview)
  expect(preview).not.toMatch(/tbg_|tbk_|Authorization: Bearer tbag_/)
})

test('指派给自己也常驻两个按钮', async ({ page, request }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const number = await createTicket(request, { title: '指派人自己的Agent工单', assignee: '王工' })
  await login(page, await wang())
  await page.goto(`/t/${number}`)

  await expect(page.locator('.actrow').getByRole('button', { name: '🤖 复制给我的 Agent' })).toBeVisible()
  await expect(page.locator('.actrow').getByRole('button', { name: '📨 复制给对方的 Agent' })).toBeVisible()
  await page.locator('.actrow').getByRole('button', { name: '🤖 复制给我的 Agent' }).click()
  const dialog = page.locator('.modal')
  await expect(dialog.locator('[data-handoff-preview]')).toContainText('请处理 agent-taskboard 上的工单')
  await expect(dialog.locator('[data-handoff-preview]')).not.toContainText('给你派了一张工单')
  await expect(dialog.getByRole('radio')).toHaveCount(0)
})

test('没有Agent token时顶部两步引导但不阻止复制', async ({ page, request }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const { memberID, credentials: worker } = await createMemberWithoutAgentToken(request)
  const number = await createTicket(request, { title: '还没签发token的成员工单', assignee: String(memberID) })
  await login(page, worker)
  await page.goto(`/t/${number}`)
  await page.locator('.actrow').getByRole('button', { name: '🤖 复制给我的 Agent' }).click()

  const guide = page.locator('.handoff-onboarding')
  await expect(guide).toContainText('你的 Agent 还没接入，第一次需要两步')
  await expect(guide).toContainText('生成 token，把接入文案贴给你的 Agent')
  await expect(page.getByRole('button', { name: '复制工单指令' })).toBeEnabled()
  await guide.getByRole('link', { name: '「我的 → Agent」' }).click()
  await expect(page).toHaveURL(/\/me$/)

  await issueToken(request, worker, '手机交接')
  await page.goto(`/t/${number}`)
  await page.locator('.actrow').getByRole('button', { name: '🤖 复制给我的 Agent' }).click()
  await expect(page.locator('.handoff-onboarding')).toHaveCount(0)
})

test('手机视口操作行常驻两个Agent按钮', async ({ page, request }) => {
  const number = await createTicket(request, { title: '手机操作行的交接工单', assignee: '王工' })
  await page.setViewportSize({ width: 390, height: 844 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  const agentButton = page.locator('.actrow').getByRole('button', { name: '🤖 复制给我的 Agent' })
  const peerButton = page.locator('.actrow').getByRole('button', { name: '📨 复制给对方的 Agent' })
  await expect(agentButton).toBeVisible()
  await expect(peerButton).toBeVisible()
  await agentButton.click()
  await expect(page.locator('.mask')).toBeVisible()
  await expect(page.locator('.modal')).toContainText('复制工单指令')
  await page.getByRole('button', { name: '关闭', exact: true }).click()

  await peerButton.click()
  await expect(page.locator('.modal')).toContainText('📨 复制给对方的 Agent')
  await expect(page.locator('.modal').getByRole('radio', { name: '成员' })).toBeChecked()
  await expect(page.locator('.modal')).toContainText('复制交接文案')
})

test('未指派与指派给自己时两个按钮都可见', async ({ page, request }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const unassigned = await createTicket(request, { title: '未指派也有两个按钮的工单' })
  await login(page, ADMIN)
  await page.goto(`/t/${unassigned}`)
  await expect(page.locator('.actrow').getByRole('button', { name: '🤖 复制给我的 Agent' })).toBeVisible()
  await expect(page.locator('.actrow').getByRole('button', { name: '📨 复制给对方的 Agent' })).toBeVisible()

  const mine = await createTicket(request, { title: '指派给我也有两个按钮的工单', assignee: '管理员' })
  await page.goto(`/t/${mine}`)
  await expect(page.locator('.actrow').getByRole('button', { name: '🤖 复制给我的 Agent' })).toBeVisible()
  await expect(page.locator('.actrow').getByRole('button', { name: '📨 复制给对方的 Agent' })).toBeVisible()

  await page.locator('.actrow').getByRole('button', { name: '📨 复制给对方的 Agent' }).click()
  const dialog = page.locator('.modal')
  await expect(dialog.getByRole('radio', { name: '成员' })).toBeChecked()
  const preview = dialog.locator('[data-handoff-preview]')
  await expect(preview).toContainText('请让你的 Agent 处理工单')
  await expect(preview).not.toContainText('管理员')
  await expect(preview).not.toContainText('给你派了')
})

test('复制给对方的Agent访客分支没有链接时能新建并显示转发文案', async ({ page, request }) => {
  const number = await createTicket(request, { title: '访客分支新建链接的工单' })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.locator('.actrow').getByRole('button', { name: '📨 复制给对方的 Agent' }).click()
  const dialog = page.locator('.modal')
  await dialog.getByRole('radio', { name: '访客' }).check()
  await expect(dialog.locator('.peer-create')).toBeVisible()
  await dialog.getByLabel('备注').fill('检验科-张三')
  await dialog.getByLabel('有效期').selectOption('7')
  await dialog.getByLabel('可填写说明').check()
  await dialog.getByRole('button', { name: '创建并显示转发文案' }).click()

  const preview = dialog.locator('[data-handoff-preview]')
  await expect(preview).toContainText('检验科-张三：想请你帮忙反馈「访客分支新建链接的工单」')
  await expect(preview).toContainText('/g/')
  await expect(dialog.getByRole('button', { name: '复制转发文案' })).toBeVisible()

  const links = await (await request.get(`/api/v1/tickets/${number}/guest-links`)).json()
  expect(links.links).toHaveLength(1)
  expect(links.links[0].can_edit_brief).toBe(true)
  expect(preview).toContainText(links.links[0].note)
})

test('不能管理访客链接时对方是访客选项置灰', async ({ page, request }) => {
  const number = await createTicket(request, { title: '无链接管理权限的工单' })
  await page.route(`**/api/v1/tickets/${number}`, async (route) => {
    const response = await route.fetch()
    const body = await response.json()
    body.viewer.can.manage_guest_links = false
    await route.fulfill({ response, json: body })
  })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.locator('.actrow').getByRole('button', { name: '📨 复制给对方的 Agent' }).click()
  const dialog = page.locator('.modal')
  await expect(dialog.getByRole('radio', { name: '访客' })).toBeDisabled()
  await expect(dialog.getByRole('radio', { name: '成员' })).toBeEnabled()
  await expect(dialog.locator('[data-guest-link-denied]')).toContainText(
    '你不能管理这张工单的访客链接，请找创建者或指派人',
  )
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
  const session = await loginWithRequest(request, ADMIN)
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

  await page.locator('.actrow').getByRole('button', { name: '🤖 复制给我的 Agent' }).click()
  const dialog = page.locator('.modal')
  await expect(dialog.locator('[data-handoff-preview]')).toContainText('请处理 agent-taskboard 上的工单')
  await expect(dialog.getByRole('radio')).toHaveCount(0)
})

test('我的页面显示接入文案并支持换新', async ({ page, request }) => {
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '我的' }).click()
  // 常驻的安装技能文字：含技能地址，不含 token（补装/更新技能用）。
  const skillButton = page.getByRole('button', { name: '复制安装技能文字' })
  await expect(skillButton).toBeVisible()
  await skillButton.click()
  const skillText = await page.evaluate(() => navigator.clipboard.readText())
  expect(skillText).toContain('/skills/agent-taskboard/SKILL.md')
  expect(skillText).not.toMatch(/tbag_/)
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
