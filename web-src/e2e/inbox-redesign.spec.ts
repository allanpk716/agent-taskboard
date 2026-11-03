import { expect, test, type APIRequestContext } from '@playwright/test'
import { createTicket, login, loginAdmin, loginWithRequest } from './helpers'
import { inboxPhrase, type InboxActor } from '../src/member/inboxText'

const member = (name: string): InboxActor => ({ type: 'member', name, member: { id: 1, name } })
const guest = (name: string): InboxActor => ({ type: 'guest', name })
const agent = (memberName: string): InboxActor => ({
  type: 'member_agent',
  name: `${memberName} 的 Agent · 笔记本`,
  member: { id: 1, name: memberName },
  agent: { id: 2, name: '笔记本' },
})

function phraseCase(reasonCode: string, trigger: InboxActor, me = '陈明') {
  return inboxPhrase(
    {
      reason_code: reasonCode,
      trigger,
      role: 'assignee',
      counterpart: { type: 'member' },
    },
    me,
  )
}

test('收件箱一句话函数覆盖全部原因', () => {
  expect(phraseCase('new', member('张三'))).toBe('张三 派给你')
  expect(phraseCase('delivered', member('李四'))).toBe('李四 交付了，等你验收')
  expect(phraseCase('assignee_replied', member('张三'))).toBe('张三 回复了，问你一个问题')
  expect(phraseCase('sent_back', member('李四'))).toBe('李四 退回了，要你修改')
  expect(phraseCase('creator_replied', member('张三'))).toBe('张三 回复了，问你一个问题')
  expect(phraseCase('agent_replied', agent('陈明'))).toBe('你的 Agent 回复了')
  expect(phraseCase('agent_replied', agent('张三'))).toBe('张三 的 Agent 回复了')
  expect(phraseCase('delivered', agent('陈明'))).toBe('你的 Agent 交付了，等你验收')
  expect(phraseCase('guest_replied', guest('王五'))).toBe('访客 王五 回复了')
  expect(phraseCase('guest_completed', guest('王五'))).toBe('访客 王五 补全了说明')
  expect(phraseCase('guest_revised', guest('王五'))).toBe('访客 王五 修订了说明')
  expect(phraseCase('guest_not_completed', guest('王五'))).toBe('访客 王五 还没填说明')
  expect(phraseCase('pending_triage', member('赵六'))).toBe('赵六 新建，等你初筛')
})

async function createFreshMember(request: APIRequestContext, name: string) {
  await loginAdmin(request)
  const suffix = Date.now()
  const res = await request.post('/api/v1/members', {
    data: { username: `inbox310-${suffix}`, name },
  })
  expect(res.status(), await res.text()).toBe(201)
  const body = (await res.json()) as {
    member: { id: number }
    initial_password: string
    member_username?: string
  }
  const username = ((body as { member?: { username?: string } }).member?.username ??
    `inbox310-${suffix}`) as string
  const triager = await request.post(`/api/v1/members/${body.member.id}/triager`, {
    data: { triager: true },
  })
  expect(triager.ok(), await triager.text()).toBeTruthy()
  return {
    id: body.member.id,
    name,
    credential: { username, password: body.initial_password },
  }
}

test('电脑端收件箱卡片显示一句话、发起人、完整标题和最后回复时间', async ({
  page,
  request,
}) => {
  const viewer = await createFreshMember(request, '赵收件')
  await loginAdmin(request)
  const longTitle =
    '生产线 3 号机型在高温环境下连续运行 48 小时后出现间歇性通讯中断，需要排查固件看门狗和 CAN 总线重连逻辑并给出修复方案'
  const assigned = await createTicket(request, {
    title: longTitle,
    labels: ['机型:UD-2300'],
    assignee: viewer.name,
  })
  const asked = await createTicket(request, {
    title: '更新内部部署手册',
    labels: ['机型:UD-2300'],
    assignee: viewer.name,
  })
  const reply = await request.post(`/api/v1/tickets/${asked}/replies`, {
    data: { body_md: '这份手册要覆盖新的部署路径吗？' },
  })
  expect(reply.status(), `发起人回复失败：${await reply.text()}`).toBe(201)

  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, viewer.credential)
  await page.goto('/')
  await expect(page.getByRole('tab', { name: '按时间' })).toHaveAttribute('aria-selected', 'true')

  const assignedCard = page.locator('.pc-row', { hasText: assigned })
  await expect(assignedCard.locator('.inbox-phrase')).toHaveText('管理员 派给你')
  await expect(assignedCard.locator('.pc-row-title')).toHaveText(longTitle)
  await expect(assignedCard.locator('.inbox-time')).toHaveText(/^创建于 /)
  await expect(assignedCard.locator('.inbox-time')).toHaveAttribute('title', /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
  await expect(assignedCard.locator('.pc-row-title')).toHaveClass(/unread/)
  await expect(assignedCard.locator('.unread-dot')).toHaveCount(1)
  await expect(assignedCard.locator('.pc-row-meta')).toContainText(assigned)
  await expect(assignedCard.locator('.pc-row-meta')).toContainText('任务')
  await expect(assignedCard.locator('.pc-row-meta')).toContainText('发起人 管理员')
  await expect(assignedCard.locator('.chip', { hasText: '机型:UD-2300' })).toBeVisible()
  await expect(assignedCard).not.toContainText('我是指派人')

  const askedCard = page.locator('.pc-row', { hasText: asked })
  await expect(askedCard.locator('.inbox-phrase')).toHaveText('管理员 回复了，问你一个问题')
  await expect(askedCard.locator('.inbox-time')).toHaveText(/分钟前|刚刚/)
  await expect(askedCard.locator('.inbox-time')).toHaveAttribute('title', /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
  await expect(askedCard.locator('.pc-row-meta')).toContainText(asked)
  await expect(askedCard.locator('.pc-row-meta')).toContainText('发起人 管理员')
  await page.screenshot({ path: 'test-results/inbox-redesign-desktop.png', fullPage: true })
})

test('电脑端按人分组显示在等他并记住视图选择', async ({ page, request }) => {
  const viewer = await createFreshMember(request, '赵分组')
  await loginAdmin(request)
  const asked = await createTicket(request, {
    title: '按人分组的提问工单',
    assignee: viewer.name,
  })
  const askedReply = await request.post(`/api/v1/tickets/${asked}/replies`, {
    data: { body_md: '请确认这份规范是否适用。' },
  })
  expect(askedReply.status(), await askedReply.text()).toBe(201)
  const pending = await createTicket(request, { title: '等待初筛的未指派工单' })
  for (let i = 0; i < 8; i++) {
    await createTicket(request, { title: `撑长列表用于检查吸顶 ${i}` })
  }

  await loginWithRequest(request, viewer.credential)
  const waitingRes = await request.post('/api/v1/tickets', {
    data: {
      title: '等管理员处理的参考工单',
      brief_md: '这张单当前轮到管理员。',
      labels: ['机型:UD-2300'],
      assignee: '管理员',
    },
  })
  expect(waitingRes.status(), `建在等他工单失败：${await waitingRes.text()}`).toBe(201)
  const waiting = ((await waitingRes.json()) as { number: string }).number
  await loginAdmin(request)
  const waitingReply = await request.post(`/api/v1/tickets/${waiting}/replies`, {
    data: { body_md: '我正在处理这张参考工单。' },
  })
  expect(waitingReply.status(), await waitingReply.text()).toBe(201)
  await loginWithRequest(request, viewer.credential)
  const turnToAdmin = await request.post(`/api/v1/tickets/${waiting}/replies`, {
    data: { body_md: '收到，等你交付。' },
  })
  expect(turnToAdmin.status(), `发起人回复失败：${await turnToAdmin.text()}`).toBe(201)
  const selfRes = await request.post('/api/v1/tickets', {
    data: {
      title: '我自己的分组工单',
      brief_md: '自派自事务。',
      labels: ['机型:UD-2300'],
      assignee: viewer.name,
    },
  })
  expect(selfRes.status(), `建自派自工单失败：${await selfRes.text()}`).toBe(201)
  const inboxBefore = ((await (
    await request.get('/api/v1/inbox')
  ).json()) as { count: number }).count

  await page.setViewportSize({ width: 1440, height: 900 })
  await login(page, viewer.credential)
  await page.goto('/')
  await expect(page.locator('.pc-list-head')).toContainText(`${inboxBefore} 张`)
  await page.getByRole('tab', { name: '按人' }).click()
  await expect(page.getByRole('tab', { name: '按人' })).toHaveAttribute('aria-selected', 'true')

  const groups = page.locator('.person-group')
  await expect(groups.locator('.group-name', { hasText: '我自己' })).toBeVisible()
  await expect(groups.locator('.group-name', { hasText: '管理员' })).toBeVisible()
  await expect(groups.locator('.group-name', { hasText: '待初筛' })).toBeVisible()
  const adminGroup = page.locator('.person-group[aria-label="管理员"]')
  await expect(adminGroup.locator('.group-counts')).toHaveText('轮到我 1 · 等他 1')
  await expect(adminGroup.locator('.tcard', { hasText: asked }).locator('.inbox-phrase')).toHaveText(
    '管理员 回复了，问你一个问题',
  )

  const toggle = adminGroup.getByRole('button', { name: /在等他 1 张/ })
  await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await expect(adminGroup.locator('.waiting-card', { hasText: waiting })).toBeHidden()
  await toggle.click()
  await expect(toggle).toHaveAttribute('aria-expanded', 'true')
  const waitingCard = adminGroup.locator('.waiting-card', { hasText: '等管理员处理的参考工单' })
  await expect(waitingCard).toBeVisible()
  await expect(waitingCard).toHaveClass(/waiting/)
  await expect(waitingCard.locator('.inbox-phrase')).toHaveText('等 管理员 处理')
  await expect(page.locator('.pc-list-head')).toContainText(`${inboxBefore} 张`)

  const scroll = page.locator('.pc-list-scroll')
  const adminHead = await adminGroup.locator('.group-head').elementHandle()
  await scroll.evaluate((element, head) => {
    element.scrollTop += head.getBoundingClientRect().top - element.getBoundingClientRect().top
  }, adminHead)
  const stickyTop = await adminGroup.locator('.group-head').boundingBox()
  const scrollTop = await scroll.boundingBox()
  expect(Math.abs((stickyTop?.y ?? 0) - (scrollTop?.y ?? 0))).toBeLessThanOrEqual(1)

  await page.reload()
  await expect(page.getByRole('tab', { name: '按人' })).toHaveAttribute('aria-selected', 'true')
  await page.getByRole('tab', { name: '按时间' }).click()
  await expect(page.locator('.tcard', { hasText: pending })).toBeVisible()
  await expect(page.locator('.waiting-card', { hasText: '等管理员处理的参考工单' })).toHaveCount(0)
  await page.screenshot({ path: 'test-results/inbox-redesign-person-desktop.png', fullPage: true })
})

test('手机视口显示收件箱卡片并支持按人展开在等他', async ({ page, request }) => {
  const viewer = await createFreshMember(request, '赵手机')
  await loginAdmin(request)
  const longTitle =
    '手机端也要完整显示这条很长的生产线通讯中断标题，包括固件看门狗和 CAN 总线重连逻辑，不允许截断或省略'
  const assigned = await createTicket(request, {
    title: longTitle,
    assignee: viewer.name,
  })
  const reply = await request.post(`/api/v1/tickets/${assigned}/replies`, {
    data: { body_md: '请确认手机上的显示。' },
  })
  expect(reply.status(), await reply.text()).toBe(201)

  await loginWithRequest(request, viewer.credential)
  const waitingRes = await request.post('/api/v1/tickets', {
    data: {
      title: '手机端在等管理员的工单',
      brief_md: '手机端参考卡。',
      labels: ['机型:UD-2300'],
      assignee: '管理员',
    },
  })
  expect(waitingRes.status(), await waitingRes.text()).toBe(201)
  const waiting = ((await waitingRes.json()) as { number: string }).number
  await loginAdmin(request)
  const adminReply = await request.post(`/api/v1/tickets/${waiting}/replies`, {
    data: { body_md: '我处理中。' },
  })
  expect(adminReply.status(), await adminReply.text()).toBe(201)
  await loginWithRequest(request, viewer.credential)
  const viewerReply = await request.post(`/api/v1/tickets/${waiting}/replies`, {
    data: { body_md: '等你交付。' },
  })
  expect(viewerReply.status(), await viewerReply.text()).toBe(201)

  await page.setViewportSize({ width: 390, height: 844 })
  await login(page, viewer.credential)
  await page.goto('/')
  const card = page.locator('.tcard', { hasText: longTitle })
  await expect(card.locator('.inbox-phrase')).toHaveText('管理员 回复了，问你一个问题')
  await expect(card.locator('.tc-title')).toHaveText(longTitle)
  await expect(card.locator('.tc-meta')).toContainText(assigned)
  await expect(card.locator('.tc-meta')).toContainText('任务')
  await expect(card.locator('.tc-meta')).toContainText('发起人 管理员')
  await expect(card.locator('.inbox-time')).toHaveText(/分钟前|刚刚/)
  await expect(card.locator('.inbox-time')).toHaveAttribute('title', /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
  await expect(card).not.toContainText('我是指派人')

  await page.getByRole('tab', { name: '按人' }).click()
  const adminGroup = page.locator('.person-group[aria-label="管理员"]')
  await expect(adminGroup.locator('.group-counts')).toHaveText('轮到我 1 · 等他 1')
  const toggle = adminGroup.getByRole('button', { name: /在等他 1 张/ })
  await expect(adminGroup.locator('.waiting-card', { hasText: '手机端在等管理员的工单' })).toBeHidden()
  await toggle.click()
  const waitingCard = adminGroup.locator('.waiting-card', { hasText: '手机端在等管理员的工单' })
  await expect(waitingCard).toBeVisible()
  await expect(waitingCard.locator('.inbox-phrase')).toHaveText('等 管理员 处理')
  await page.screenshot({ path: 'test-results/inbox-redesign-mobile.png', fullPage: true })
})
