import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import { ADMIN, createTicket, login, loginAdmin, loginWithRequest, markDraftIntroSeen, openGuestPage, wang } from './helpers'

const VIEWPORTS = [
  { name: 'mobile', width: 390, height: 844 },
  { name: 'narrow', width: 800, height: 800 },
  { name: 'medium', width: 1100, height: 800 },
  { name: 'wide', width: 1440, height: 900 },
] as const

function longBody(marker: string): string {
  return Array.from({ length: 10 }, (_, i) => `${marker}：第 ${i + 1} 段内容，用来保证这条回复在常见屏幕高度下需要滚动才能看到。`).join('\n\n')
}

async function addReply(
  request: APIRequestContext,
  number: string,
  body: string,
  token?: string,
): Promise<void> {
  const res = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: body },
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
  })
  expect(res.status(), `造回复失败：${await res.text()}`).toBe(201)
}

async function directAgentToken(request: APIRequestContext): Promise<string> {
  const issued = await request.post('/api/v1/me/agent-tokens', {
    data: { name: `最新回复-${Date.now()}`, draft_mode: 'direct' },
  })
  expect(issued.status(), `签发 Agent 失败：${await issued.text()}`).toBe(201)
  return ((await issued.json()) as { token: string }).token
}

async function requiredAgentToken(request: APIRequestContext): Promise<string> {
  const loginRes = await loginWithRequest(request, await wang())
  expect(loginRes.status(), `王工登录失败：${await loginRes.text()}`).toBe(200)
  const issued = await request.post('/api/v1/me/agent-tokens', {
    data: { name: `回复草稿-${Date.now()}`, draft_mode: 'required' },
  })
  expect(issued.status(), `签发 Agent 失败：${await issued.text()}`).toBe(201)
  await markDraftIntroSeen(request)
  return ((await issued.json()) as { token: string }).token
}

async function createGuestLink(
  request: APIRequestContext,
  number: string,
): Promise<string> {
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '检验科-张三', valid_days: 30 },
  })
  expect(link.status(), `创建访客链接失败：${await link.text()}`).toBe(201)
  return String(((await link.json()) as { url: string }).url).slice(3)
}

function replyLocator(page: Page, marker: string) {
  return page.locator('[data-bubble="reply"]').filter({ hasText: marker }).last()
}

async function expectReplyStartVisible(page: Page, marker: string, top: number) {
  const viewportHeight = page.viewportSize()?.height ?? 0
  await expect
    .poll(async () => (await replyLocator(page, marker).boundingBox())?.y ?? Number.MAX_SAFE_INTEGER)
    .toBeLessThan(viewportHeight - 160)
  const box = await replyLocator(page, marker).boundingBox()
  expect(box, `没找到「${marker}」回复`).toBeTruthy()
  expect(box!.y).toBeGreaterThanOrEqual(top - 1)
  // 目标开头进入视口，而不是只露出底部或滚到回复框。
  expect(box!.y).toBeLessThan(viewportHeight - 160)
}

async function waitIntersection(page: Page) {
  await page.evaluate(() => new Promise(requestAnimationFrame))
}

test('成员详情四档布局都能从顶部跳到最新回复开头', async ({ page, request }) => {
  const number = await createTicket(request, { title: '四档布局跳最新回复', assignee: '管理员' })
  for (let i = 1; i <= 16; i++) {
    await addReply(request, number, longBody(`普通回复 ${i}`))
  }
  await addReply(request, number, longBody('真正的最后一条回复'))
  await login(page, ADMIN)

  for (const viewport of VIEWPORTS) {
    await page.setViewportSize(viewport)
    await page.goto(`/t/${number}`)
    await expect(page.getByRole('heading', { name: '四档布局跳最新回复' })).toBeVisible()

    const usesConversationScroll = viewport.name === 'medium' || viewport.name === 'wide'
    expect(
      usesConversationScroll
        ? await page.locator('.pc-detail .conv').evaluate((el) => el.scrollTop)
        : await page.evaluate(() => window.scrollY),
    ).toBe(0)

    const button = page.getByRole('button', { name: '跳到最新回复' })
    await expect(button).toBeVisible()
    await button.focus()
    await page.keyboard.press('Enter')
    await expect(button).toBeHidden()
    const top = usesConversationScroll
      ? ((await page.locator('.pc-detail .conv').boundingBox())?.y ?? 0) + 8
      : 12
    await expectReplyStartVisible(page, '真正的最后一条回复', top)

    if (usesConversationScroll) {
      await page.locator('.pc-detail .conv').evaluate((el) => el.scrollTo({ top: 0 }))
    } else {
      await page.evaluate(() => window.scrollTo({ top: 0 }))
    }
    await expect(button).toBeVisible()
  }
})

test('操作方筛选后跳到仍然看得见的最后一条回复', async ({ page, request }) => {
  const number = await createTicket(request, { title: '筛选后跳可见回复', assignee: '管理员' })
  const token = await directAgentToken(request)
  for (let i = 1; i <= 14; i++) {
    await addReply(request, number, longBody(`Agent 可见回复 ${i}`), token)
  }
  await addReply(request, number, longBody('被 Agent 筛掉的最后一条本人回复'))

  await page.setViewportSize({ width: 390, height: 844 })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  const button = page.getByRole('button', { name: '跳到最新回复' })
  await expect(button).toBeVisible()
  await page.getByRole('group', { name: '操作方' }).getByRole('button', { name: 'Agent' }).click()
  await expect(page.getByText('被 Agent 筛掉的最后一条本人回复')).toBeHidden()
  await expect(button).toBeVisible()

  await button.click()
  await expect(button).toBeHidden()
  await expectReplyStartVisible(page, 'Agent 可见回复 14', 12)
})

test('回复很少或没有回复时不显示最新回复按钮', async ({ page, request }) => {
  const noReply = await createTicket(request, { title: '没有回复的工单' })
  const shortReply = await createTicket(request, { title: '只有一条短回复的工单' })
  await addReply(request, shortReply, '这条短回复本来就在屏幕里。')

  await page.setViewportSize({ width: 390, height: 844 })
  await login(page, ADMIN)
  for (const number of [noReply, shortReply]) {
    await page.goto(`/t/${number}`)
    if (number === noReply) {
      await expect(page.getByRole('heading', { name: '没有回复的工单' })).toBeVisible()
    } else {
      await expect(page.getByText('这条短回复本来就在屏幕里。')).toBeVisible()
    }
    await waitIntersection(page)
    await expect(page.getByRole('button', { name: '跳到最新回复' })).toHaveCount(0)
  }
})

test('有回复草稿时仍然只跳到最后一条正式回复', async ({ page, request }) => {
  const number = await createTicket(request, { title: '有草稿也跳正式回复', assignee: '王工' })
  for (let i = 1; i <= 16; i++) {
    await addReply(request, number, longBody(`草稿前回复 ${i}`))
  }
  const token = await requiredAgentToken(request)
  const drafted = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: '这是待确认的回复草稿，不应成为按钮目标。' },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(drafted.status(), `造回复草稿失败：${await drafted.text()}`).toBe(202)

  await page.setViewportSize({ width: 390, height: 844 })
  await login(page, await wang())
  await page.goto(`/t/${number}`)
  await expect(page.getByLabel('回复草稿')).toContainText('这是待确认的回复草稿')
  const button = page.getByRole('button', { name: '跳到最新回复' })
  await expect(button).toBeVisible()
  await button.click()
  await expect(button).toBeHidden()
  await expectReplyStartVisible(page, '草稿前回复 16', 12)
})

test('访客手机与电脑对话都能跳到最新回复', async ({ page, request }) => {
  const number = await createTicket(request, { title: '访客长对话跳最新回复', brief: '这是一段用于撑开访客对话的说明。' })
  for (let i = 1; i <= 16; i++) {
    await addReply(request, number, longBody(`访客对话回复 ${i}`))
  }
  await addReply(request, number, longBody('访客真正的最后一条回复'))
  await loginAdmin(request)
  const linkKey = await createGuestLink(request, number)
  await page.setViewportSize({ width: 390, height: 844 })
  await openGuestPage(page, request, linkKey)
  await expect(page.getByRole('heading', { name: '访客长对话跳最新回复' })).toBeVisible()

  for (const viewport of [
    { width: 390, height: 844 },
    { width: 1280, height: 800 },
  ]) {
    if (viewport.width !== 390) {
      await page.setViewportSize(viewport)
      await page.goto(page.url())
      await expect(page.getByRole('heading', { name: '访客长对话跳最新回复' })).toBeVisible()
    }
    expect(await page.evaluate(() => window.scrollY)).toBe(0)
    const button = page.getByRole('button', { name: '跳到最新回复' })
    await expect(button).toBeVisible()

    if (viewport.width === 390) {
      const buttonBox = await button.boundingBox()
      const composerBox = await page.locator('.composer').boundingBox()
      expect(buttonBox!.y + buttonBox!.height).toBeLessThanOrEqual(composerBox!.y)
      expect(buttonBox!.height).toBeGreaterThanOrEqual(44)
    }

    await button.click()
    await expect(button).toBeHidden()
    const sticky = await page.locator('.privacy-note.persistent').boundingBox()
    await expectReplyStartVisible(page, '访客真正的最后一条回复', (sticky?.y ?? 0) + (sticky?.height ?? 0) + 2)

    await page.evaluate(() => window.scrollTo({ top: 0 }))
    await expect(button).toBeVisible()
  }
})

test('访客短对话不显示最新回复按钮', async ({ page, request }) => {
  const number = await createTicket(request, { title: '访客短对话' })
  await addReply(request, number, '这条访客短回复本来就在屏幕里。')
  await loginAdmin(request)
  const linkKey = await createGuestLink(request, number)
  await page.setViewportSize({ width: 390, height: 844 })
  await openGuestPage(page, request, linkKey)
  await expect(page.getByText('这条访客短回复本来就在屏幕里。')).toBeVisible()
  await waitIntersection(page)
  await expect(page.getByRole('button', { name: '跳到最新回复' })).toHaveCount(0)
})
