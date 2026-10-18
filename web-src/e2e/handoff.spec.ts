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
