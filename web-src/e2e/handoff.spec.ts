import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, createTicket, login, wang } from './helpers'

// #110「交给 Agent」：只从浏览器可见行为与剪贴板断言。

async function issueToken(request: APIRequestContext, who: { username: string; password: string }, name: string) {
  const session = await request.post('/api/v1/session', { data: who })
  expect(session.ok()).toBeTruthy()
  const res = await request.post('/api/v1/me/agent-tokens', { data: { name } })
  expect(res.ok(), await res.text()).toBeTruthy()
}

test('发起人打开交给Agent：默认含人话行，可切换并复制', async ({ page, request }) => {
  const number = await createTicket(request, { title: '要交给王工Agent的工单', assignee: '王工' })
  await issueToken(request, ADMIN, '发起人交接')
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.locator('.actrow').getByRole('button', { name: '交给 Agent' }).click()
  const dialog = page.locator('.modal')
  await expect(dialog).toContainText('发给王工（含人话行）')
  await expect(dialog).toContainText('只要 Agent 段')
  await expect(dialog.locator('[data-handoff-preview]')).toContainText('给你派了一张工单')
  await expect(dialog).toContainText('文案不含凭证，对方的 Agent 用它主人自己签的 token')

  await dialog.getByText('只要 Agent 段').click()
  await expect(dialog.locator('[data-handoff-preview]')).not.toContainText('给你派了一张工单')
  const preview = ((await dialog.locator('[data-handoff-preview]').textContent()) ?? '').trim()
  await dialog.getByRole('button', { name: '复制交接文案' }).click()
  await expect(dialog.getByRole('button', { name: '已复制' })).toBeVisible()
  expect((await page.evaluate(() => navigator.clipboard.readText())).replace(/\r\n/g, '\n')).toBe(preview)
  expect(preview).toContain('/agent.md')
  expect(preview).not.toMatch(/tbg_|tbk_|Authorization: Bearer [A-Za-z0-9_-]{20,}/)
})

test('指派人本人打开交给Agent：只有Agent段且没有单选', async ({ page, request }) => {
  const number = await createTicket(request, { title: '指派人自己的Agent工单', assignee: '王工' })
  await login(page, await wang())
  await page.goto(`/t/${number}`)

  await page.locator('.actrow').getByRole('button', { name: '交给 Agent' }).click()
  const dialog = page.locator('.modal')
  await expect(dialog).toContainText('请处理 agent-taskboard 上的工单')
  await expect(dialog.locator('[data-handoff-preview]')).not.toContainText('给你派了一张工单')
  await expect(dialog.getByRole('radio')).toHaveCount(0)
})

test('没有Agent token时提示去签发，签发后提示消失', async ({ page, request }) => {
  const number = await createTicket(request, { title: '还没签发token的成员工单' })
  await login(page, await wang())
  await page.goto(`/t/${number}`)

  await page.locator('.actrow').getByRole('button', { name: '交给 Agent' }).click()
  await expect(page.locator('.modal')).toContainText('你还没签发 Agent token')
  await page.getByRole('link', { name: '去「我的 → Agent」' }).click()
  await expect(page).toHaveURL(/\/me$/)

  await issueToken(request, await wang(), '手机交接')
  await page.goto(`/t/${number}`)
  await page.locator('.actrow').getByRole('button', { name: '交给 Agent' }).click()
  await expect(page.locator('.modal')).not.toContainText('你还没签发 Agent token')
})

test('手机视口从详情顶栏菜单打开底部抽屉', async ({ page, request }) => {
  const number = await createTicket(request, { title: '手机抽屉打开的交接工单', assignee: '王工' })
  await page.setViewportSize({ width: 375, height: 720 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.getByRole('button', { name: '更多操作' }).click()
  await page.getByRole('menuitem', { name: '交给 Agent' }).click()
  await expect(page.locator('.mask')).toBeVisible()
  await expect(page.locator('.modal')).toContainText('发给王工（含人话行）')
  await expect(page.locator('.modal')).toContainText('复制交接文案')
})

test('改派确认后弹窗原地切换成交接', async ({ page, request }) => {
  const number = await createTicket(request, { title: '改派后交接的工单' })
  await issueToken(request, ADMIN, '改派交接')
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await page.route('**/api/v1/tickets/*/assign', async (route) => {
    const response = await route.fetch()
    const body = await response.json()
    body.share_text = `人话行也可以包含空行。\n\n不应反向拆出的 Agent 段\n\n${body.share_text}`
    await route.fulfill({ response, json: body })
  })

  await page.locator('.actrow').getByRole('button', { name: /指派|改派/ }).click()
  await page.getByLabel('指派给').selectOption('王工')
  await page.getByRole('button', { name: /确认指派|确认改派/ }).click()
  const dialog = page.locator('.modal')
  await expect(dialog).toContainText('发给王工（含人话行）')
  await expect(dialog.locator('[data-handoff-preview]')).toContainText('转给你了')
  await dialog.getByText('只要 Agent 段').click()
  await expect(dialog.locator('[data-handoff-preview]')).not.toContainText('不应反向拆出的 Agent 段')
  await expect(dialog.locator('[data-handoff-preview]')).toContainText('请处理 agent-taskboard 上的工单')
  await expect(dialog.getByRole('button', { name: '复制交接文案' })).toBeVisible()
  await expect(dialog).not.toContainText('复制 share_text')
})

test('建单指派给同事后完成区域有交接入口', async ({ page }) => {
  await login(page, ADMIN)
  await page.route('**/api/v1/tickets', async (route) => {
    if (route.request().method() !== 'POST') {
      await route.fallback()
      return
    }
    const response = await route.fetch()
    const body = await response.json()
    body.share_text = `人话行也可以包含空行。\n\n不应反向拆出的 Agent 段\n\n${body.share_text}`
    await route.fulfill({ response, json: body })
  })
  await page.locator('.b-tabs').getByRole('link', { name: '建单' }).click()
  await page.getByText('自由格式').click()
  await page.getByLabel('标题').fill('建单后交给王工Agent')
  await page.getByLabel(/说明/).fill('完成后把清单发回来。')
  await page.getByLabel('标签输入').fill('1680')
  await page.getByRole('button', { name: '添加', exact: true }).click()
  await page.getByLabel('指派给').selectOption('王工')
  await page.getByRole('button', { name: '创建工单' }).click()

  await expect(page.locator('.donet')).toContainText('已创建')
  const done = page.locator('.bpage')
  await expect(done).toContainText('发给王工（含人话行）')
  await expect(done.locator('[data-handoff-preview]')).toContainText('给你派了一张工单')
  await done.getByText('只要 Agent 段').click()
  await expect(done.locator('[data-handoff-preview]')).not.toContainText('不应反向拆出的 Agent 段')
  await expect(done.locator('[data-handoff-preview]')).toContainText('请处理 agent-taskboard 上的工单')
  await expect(done.getByRole('button', { name: '复制交接文案' })).toBeVisible()
  await expect(done).not.toContainText('share_text（可直接转发）')
})

test('指派人按成员id判断而不是重名判断', async ({ page, request }) => {
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

  await page.locator('.actrow').getByRole('button', { name: '交给 Agent' }).click()
  const dialog = page.locator('.modal')
  await expect(dialog).toContainText(`发给${adminName}（含人话行）`)
  await expect(dialog.getByRole('radio')).toHaveCount(2)
})

test('我的页面Agent区块使用新标题和副标题', async ({ page }) => {
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '我的' }).click()

  const section = page.locator('.mcard', { hasText: '为名下的 Agent 签发具名 token' })
  await expect(section.locator('.sec-t')).toHaveText('Agent')
  await expect(section.locator('.hint').first()).toContainText('为名下的 Agent 签发具名 token')
  await expect(section).toContainText('Agent 不能冒充本人，也不能签发 token')
  await expect(section).not.toContainText('我的成员 Agent（API token）')
})

test('签发token弹窗提示下一步配置Agent', async ({ page }) => {
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '我的' }).click()
  await page.getByRole('button', { name: '＋ 生成新 token' }).click()
  await page.getByLabel('名字').fill('下一步提示')
  await page.getByRole('button', { name: '生成', exact: true }).click()

  const dialog = page.locator('.modal')
  await expect(dialog.locator('[data-token-value]')).toBeVisible()
  const nextStep =
    '下一步：把这个 token 配进你的 Agent。以后接到工单，在工单上复制交接文案发给它就行——文案里不带 token。'
  await expect(dialog.getByText(nextStep)).toBeVisible()
})

test('改派给自己时确认后直接关闭弹窗', async ({ page, request }) => {
  const number = await createTicket(request, { title: '改派给自己的工单', assignee: '王工' })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.locator('.actrow').getByRole('button', { name: /指派|改派/ }).click()
  await page.getByLabel('指派给').selectOption('管理员')
  await page.getByRole('button', { name: /确认指派|确认改派/ }).click()
  await expect(page.locator('.modal')).toBeHidden()
  await expect(page.locator('.mmeta')).toContainText('管理员')
})
