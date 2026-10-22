import { test, expect, type APIRequestContext } from '@playwright/test'
import { ADMIN, claimGuestLink, createTicket, login, loginAdmin, wang } from './helpers'

// 成员网页（#41）：收件箱、列表、详情、建单。对真实服务 + 构建好的前端跑用例，
// 造数据（成员、工单、回复、访客链接）全部通过 HTTP API 完成；成员由
// e2e/start-server.mjs 预先创建（admin / 管理员 由命令行，wang / 王工 由
// #42 的成员账号接口创建，见 helpers.ts）。

// 1. 登录后进入成员网页：顶栏、底部标签栏（收件箱/工单/＋建单/工具/我的），
//    收件箱按原因显示卡片（新指派给你）。
test('登录后进入成员网页，收件箱按原因显示卡片', async ({ page, request }) => {
  const number = await createTicket(request, { title: '收件箱原因卡片的工单', assignee: '管理员' })
  await login(page, ADMIN)

  // 顶栏 + 底部标签栏
  await expect(page.getByRole('heading', { name: '收件箱' })).toBeVisible()
  await expect(page.locator('.b-me')).toContainText('管理员')
  for (const tab of ['收件箱', '工单', '建单', '工具', '我的']) {
    await expect(page.locator('.b-tabs').getByRole('link', { name: tab })).toBeVisible()
  }

  // 卡片：原因色条 + 编号 + 标题；点击进入详情
  const card = page.locator('.tcard', { hasText: number })
  await expect(card).toBeVisible()
  await expect(card.locator('.badge.reason')).toHaveText('新指派给你')
  await expect(card).toContainText('收件箱原因卡片的工单')
  await card.click()
  await expect(page).toHaveURL(new RegExp(`/t/${number}(\\?|$)`))
})

// 2. 工单列表：按状态、指派、操作方（本人/Agent）筛选，按关键词搜索。
test('工单列表按状态、指派与本人/Agent筛选，支持关键词', async ({ page, request }) => {
  // 造三张：A 成员建的开启未指派；B 成员 Agent 建的开启指派给我；C 已关闭。
  const a = await createTicket(request, { title: '成员本人创建的工单', labels: ['机型:UD-2300'] })
  const b = await createTicketViaAgent(request, '成员 Agent 创建的工单', '管理员')
  const closedTitle = `随后被关闭的工单 ${Date.now()}`
  const c = await createTicket(request, { title: closedTitle, labels: ['机型:US-1680'] })
  const closed = await request.post(`/api/v1/tickets/${c}/close`, { data: { reason: 'done' } })
  expect(closed.ok()).toBeTruthy()

  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '工单' }).click()
  const grp = (name: string) => page.getByRole('group', { name })

  // 默认开启：A、B 在，C 不在；卡片带编号、标题、发起人、指派、标签
  const cardA = page.locator('.tcard', { hasText: a })
  const cardB = page.locator('.tcard', { hasText: b })
  await expect(cardA).toBeVisible()
  await expect(cardB).toBeVisible()
  await expect(page.locator('.tcard', { hasText: c })).toHaveCount(0)
  await expect(cardB).toContainText('指派 管理员')
  await expect(cardA).toContainText('未指派')
  await expect(cardA.locator('.chip')).toContainText('机型:UD-2300')

  // 状态 = 已关闭：只剩 C
  await grp('状态').getByRole('button', { name: '已关闭' }).click()
  await expect(page.locator('.tcard', { hasText: c })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: c }).locator('.badge.closed')).toContainText('完成')
  await expect(page.locator('.tcard', { hasText: a })).toHaveCount(0)
  await grp('状态').getByRole('button', { name: '开启' }).click()

  // 操作方 = Agent：只剩 B（创建者署名是成员 Agent）；本人只剩 A
  await grp('操作方').getByRole('button', { name: 'Agent' }).click()
  await expect(page).toHaveURL(/actor_kind=agent/)
  await expect(page.locator('.tcard', { hasText: b })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: a })).toHaveCount(0)
  await grp('操作方').getByRole('button', { name: '本人' }).click()
  await expect(page).toHaveURL(/actor_kind=member/)
  await expect(page.locator('.tcard', { hasText: a })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: b })).toHaveCount(0)
  await grp('操作方').getByRole('button', { name: '全部' }).click()

  // 指派 = 指派给我：只剩 B
  await grp('指派').getByRole('button', { name: '指派给我' }).click()
  await expect(page).toHaveURL(/assignee=me/)
  await expect(page.locator('.tcard', { hasText: b })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: a })).toHaveCount(0)
  await grp('指派').getByRole('button', { name: '全部' }).click()

  // 关键词：状态切回全部后，只命中标题含关键词的那张
  await grp('状态').getByRole('button', { name: '全部' }).click()
  await page.getByPlaceholder(/搜索/).fill(closedTitle)
  await page.getByPlaceholder(/搜索/).press('Enter')
  await expect(page).toHaveURL(/q=/)
  await page.reload()
  await expect(page.locator('.tcard', { hasText: c })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: a })).toHaveCount(0)
})

/** 用管理员的成员 Agent token 建单（创建者署名是 Agent，actor_kind=agent 命中）。 */
async function createTicketViaAgent(
  request: APIRequestContext,
  title: string,
  assignee?: string,
): Promise<string> {
  await loginAdmin(request)
  const tokenRes = await request.post('/api/v1/me/agent-tokens', {
    data: { name: `agent-${title}`, draft_mode: 'direct' },
  })
  expect(tokenRes.ok()).toBeTruthy()
  await request.post('/api/v1/me/notices/drafts_intro/seen')
  const token: string = (await tokenRes.json()).token
  const res = await request.post('/api/v1/tickets', {
    data: {
      title,
      brief_md: `${title} 的说明。`,
      labels: ['机型:SL-1000'],
      ...(assignee ? { assignee } : {}),
    },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(res.status(), `Agent 建单失败：${await res.text()}`).toBe(201)
  return (await res.json()).number
}

// 3. 派活到验收（验收标准 1）：A 在网页上建单并指派给 B；B 在收件箱看到
//    「新指派给你」，打开后交付；A 在收件箱看到「已交付，待你验收」，验收通过
//    以「完成」关闭。全程走网页。
test('派活到验收：A建单指派B，B收件箱交付，A验收关闭', async ({ page }) => {
  // A（管理员）建单并指派给 B（王工），拿到 share_text 里的编号
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '建单' }).click()
  await page.getByRole('button', { name: /我自己填/ }).click()
  await page.getByText('自由格式').click()
  await page.getByLabel('标题').fill('给王工的派活工单')
  await page.getByLabel('说明').fill('把对照表整理一份，周五前交。')
  await page.getByLabel('标签输入').fill('机型:SL-1000')
  await page.getByRole('button', { name: '添加', exact: true }).click()
  // 添加要先等 /labels/resolve 返回才出 chip；不等就提交会被 dimension_label_required 拦下（#184）。
  await expect(page.locator('.lblrow .chip').filter({ hasText: '机型:SL-1000' })).toBeVisible()
  await page.getByLabel('指派给').selectOption('王工')
  await page.getByRole('button', { name: '创建工单' }).click()
  await expect(page.locator('.donet')).toContainText('已创建')
  const doneText = (await page.locator('.donet').textContent()) ?? ''
  const number = doneText.match(/(\d{8}-\d{3})/)![1]

  // B（王工）登录：收件箱出现「新指派给你」
  await login(page, await wang())
  const card = page.locator('.tcard', { hasText: number })
  await expect(card).toBeVisible()
  await expect(card.locator('.badge.reason')).toHaveText('新指派给你')
  await card.click()

  // 详情：写回复并标记为交付
  await expect(page.getByRole('heading', { name: '给王工的派活工单' })).toBeVisible()
  await page.getByPlaceholder(/写回复/).fill('任务完成，请验收：清单已整理好。')
  await page.getByRole('button', { name: '标记为交付' }).click()
  await page.getByRole('button', { name: '发送', exact: true }).click()
  const reply = page.locator('[data-bubble="reply"]').last()
  await expect(reply).toContainText('任务完成，请验收：清单已整理好。')
  await expect(reply.locator('.deliver-tag')).toHaveText('交付')

  // 换 A（管理员）登录：收件箱出现「已交付，待你验收」
  await page.locator('.b-tabs').getByRole('link', { name: '我的' }).click()
  await page.getByRole('button', { name: '登出' }).click()
  // 登出按钮在异步 POST 完成后自行跳转 /login；不等它就 goto 会被这次导航中止（ERR_ABORTED）。
  // 与 login.spec.ts 的写法保持一致。
  await expect(page).toHaveURL(/\/login$/)
  await login(page, ADMIN)
  const acceptCard = page.locator('.tcard', { hasText: number })
  await expect(acceptCard).toBeVisible()
  await expect(acceptCard.locator('.badge.reason')).toHaveText('已交付，待你验收')
  await acceptCard.click()

  // 验收通过，以「完成」关闭
  await expect(page.locator('.accept')).toContainText('已交付，等待你的验收')
  await page.getByRole('button', { name: '验收通过，关闭（完成）' }).click()
  await expect(page.locator('.badge.closed').first()).toContainText('已关闭 · 完成')
  await expect(page.locator('.composer.ro')).toContainText('工单已关闭')
  await expect(page.getByPlaceholder(/写回复/)).toHaveCount(0)
})

// 3b. 自派自的任务（#128）：成员 Agent 通过 API 交付后，本人在收件箱看到
// 待验收，详情页显示「现在轮到你」和验收按钮；本人退回后按钮消失。
test('自派自任务：Agent交付后本人验收界面', async ({ page, request }) => {
  const number = await createTicket(request, {
    title: '自派自的工具清单',
    assignee: '管理员',
  })
  await loginAdmin(request)
  const tokenRes = await request.post('/api/v1/me/agent-tokens', {
    data: { name: `自派自验收-${number}`, draft_mode: 'direct' },
  })
  expect(tokenRes.ok(), await tokenRes.text()).toBeTruthy()
  await request.post('/api/v1/me/notices/drafts_intro/seen')
  const token: string = (await tokenRes.json()).token
  const delivered = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: '清单已完成，请验收。', deliver: true },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(delivered.status(), await delivered.text()).toBe(201)

  await login(page, ADMIN)
  const card = page.locator('.tcard', { hasText: number })
  await expect(card).toBeVisible()
  await expect(card.locator('.badge.reason')).toHaveText('已交付，待你验收')
  await card.click()
  await expect(page.locator('.badge.myturn')).toHaveText('● 现在轮到你（已交付，待你验收）')
  await expect(page.locator('.accept')).toContainText('已交付，等待你的验收')
  await expect(page.getByRole('button', { name: '验收通过，关闭（完成）' })).toBeVisible()

  const sentBack = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: '缺少异常处理，请补充后重新交付。' },
  })
  expect(sentBack.ok(), await sentBack.text()).toBeTruthy()
  await page.reload()
  await expect(page.locator('.badge.myturn')).toHaveText('● 现在轮到你（发起人退回修改）')
  await expect(page.locator('.accept')).toHaveCount(0)
})

// 4. 详情页创建访客链接并复制（验收标准 2）。
test('详情页创建访客链接并复制', async ({ page, request }) => {
  const number = await createTicket(request, { title: '要发访客链接的工单' })
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.getByRole('button', { name: '访客链接' }).click()
  await page.getByLabel('备注').fill('检验科-张三')
  await page.getByRole('button', { name: '创建', exact: true }).click()

  // 链接只出现一次：显示在链接框里，复制后剪贴板内容一致。显示与复制的都是
  // 绝对地址（服务端没配 public_url，用当前页面的 origin 拼出，评审 P2-7）。
  const linkText = await page.locator('.linkbox').textContent()
  expect(linkText).toMatch(/^https?:\/\/127\.0\.0\.1:\d+\/g\/tbg_/)
  await page.getByRole('button', { name: '复制链接' }).click()
  await expect(page.getByText('已复制')).toBeVisible()
  const clip = await page.evaluate(() => navigator.clipboard.readText())
  expect(clip).toMatch(/^https?:\/\/127\.0\.0\.1:\d+\/g\/tbg_/)
  expect(clip.trim()).toBe(linkText!.trim())

  // 创建事件记入时间线；复制出的链接真的能打开（访客页显示工单标题）
  await page.getByRole('button', { name: '关闭', exact: true }).click()
  await expect(page.locator('.conv .event').last()).toContainText('创建了访客链接「检验科-张三」')
  const key = new URL(clip, page.url()).pathname.slice(3)
  const token = await claimGuestLink(page.request, key)
  await page.addInitScript(({ key, token }) => {
    localStorage.setItem(`tb:guest-device:${key}`, token)
  }, { key, token })
  await page.goto(clip)
  await expect(page.getByRole('heading', { name: '要发访客链接的工单' })).toBeVisible()
})

test('反馈没有可用访客链接时提示并可发链接', async ({ page, request }) => {
  await loginAdmin(request)
  const created = await request.post('/api/v1/tickets', {
    data: {
      kind: 'report',
      title: '没有可用访客链接的反馈',
      labels: ['机型:UD-2300'],
      assignee: '管理员',
      guest_link: { note: '检验科-张三' },
    },
  })
  expect(created.status(), await created.text()).toBe(201)
  const body = (await created.json()) as { number: string; guest_link: { id: number } }
  const revoked = await request.delete(
    `/api/v1/tickets/${body.number}/guest-links/${body.guest_link.id}`,
  )
  expect(revoked.ok()).toBeTruthy()

  await login(page, ADMIN)
  await page.goto(`/t/${body.number}`)
  await expect(page.getByText('这张反馈没有可用的访客链接，访客看不到回复')).toBeVisible()

  await page.getByRole('button', { name: '发访客链接' }).click()
  await expect(page.locator('.modal')).toContainText(`访客链接 · ${body.number}`)
  await page.getByLabel('备注').fill('检验科-李四')
  await page.getByRole('button', { name: '创建', exact: true }).click()
  await expect(page.getByText('这张反馈没有可用的访客链接，访客看不到回复')).toHaveCount(0)
})

// 5. 时间线按「本人 / Agent」筛选后结果正确（验收标准 3）。
test('时间线按本人/Agent筛选后结果正确', async ({ page, request }) => {
  const number = await createTicket(request, { title: '有本人与Agent回复的工单', assignee: '管理员' })
  await loginAdmin(request)
  const tokenRes = await request.post('/api/v1/me/agent-tokens', {
    data: { name: '时间线筛选', draft_mode: 'direct' },
  })
  const token: string = (await tokenRes.json()).token
  await request.post('/api/v1/me/notices/drafts_intro/seen')
  const agentReply = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: '这是成员 Agent 的回复。' },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(agentReply.status()).toBe(201)
  const memberReply = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: '这是成员本人的回复。' },
  })
  expect(memberReply.status()).toBe(201)

  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  const grp = page.getByRole('group', { name: '操作方' })

  // 全部：两条都在
  await expect(page.getByText('这是成员 Agent 的回复。')).toBeVisible()
  await expect(page.getByText('这是成员本人的回复。')).toBeVisible()

  // 本人：只剩成员本人的回复；系统事件仍显示
  await grp.getByRole('button', { name: '本人' }).click()
  await expect(page.getByText('这是成员本人的回复。')).toBeVisible()
  await expect(page.getByText('这是成员 Agent 的回复。')).toBeHidden()

  // Agent：只剩成员 Agent 的回复
  await grp.getByRole('button', { name: 'Agent' }).click()
  await expect(page.getByText('这是成员 Agent 的回复。')).toBeVisible()
  await expect(page.getByText('这是成员本人的回复。')).toBeHidden()
})

// 6. 详情页操作：打标签（别名换标准值、未知值给建议、允许新建）、改派、
//    关闭（不处理）与重新开启。
test('详情页打标签改派关闭与重开', async ({ page, request }) => {
  const number = await createTicket(request, { title: '详情页操作练习', labels: ['机型:UD-2300'] })
  // 预置别名（种子不发明别名，决议 1）：给 机型:US-1680 添「1680」
  await loginAdmin(request)
  const alias = await request.post('/api/v1/labels/aliases', {
    data: { label: '机型:US-1680', alias: '1680' },
  })
  // 别名可能已被早先的用例加上（alias_exists）——同样满足本用例的前置。
  expect(alias.ok() || (await alias.json()).error.code === 'alias_exists', await alias.text()).toBeTruthy()
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  // 打标签：写别名 1680（US-1680 的别名）→ 换成标准值
  await page.getByRole('button', { name: '标签' }).click()
  await page.getByLabel('标签输入').fill('1680')
  await page.getByRole('button', { name: '添加', exact: true }).click()
  await expect(page.locator('.modal .chip', { hasText: '机型:US-1680' })).toBeVisible()
  await expect(page.getByText(/别名/)).toBeVisible()

  // 未知值：字典里没有，给建议；勾「允许新建」后成功
  await page.getByLabel('标签输入').fill('机型:XYZ-999')
  await page.getByRole('button', { name: '添加', exact: true }).click()
  await expect(page.locator('.modal .errbox')).toContainText('unknown_label_value')
  await page.getByLabel(/允许新建/).check()
  await page.getByRole('button', { name: '添加', exact: true }).click()
  await expect(page.locator('.modal .chip', { hasText: '机型:XYZ-999' })).toBeVisible()

  // 摘掉一个标签
  await page.locator('.modal .chip', { hasText: '机型:UD-2300' }).locator('i').click()
  await expect(page.locator('.modal .chip', { hasText: '机型:UD-2300' })).toHaveCount(0)
  await page.getByRole('button', { name: '完成' }).click()
  // 展开元信息区，确认标签已同步到工单上
  await page.getByText('标签 · 指派 · 访客链接').click()
  await expect(page.locator('.mmeta .chip', { hasText: '机型:XYZ-999' })).toBeVisible()

  // 指派给王工：弹窗原地切换成交接文案
  await page.locator('.actrow').getByRole('button', { name: /指派|改派/ }).click()
  await page.getByLabel('指派给').selectOption('王工')
  await page.getByRole('button', { name: /确认指派|确认改派/ }).click()
  await expect(page.locator('.modal')).toContainText('📨 发给 王工')
  await expect(page.locator('[data-handoff-preview]')).toContainText('转给你了')
  await page.getByRole('button', { name: '关闭', exact: true }).click()
  await expect(page.locator('.mmeta')).toContainText('王工')

  // 以「不处理」关闭，再重新开启
  await page.locator('.actrow').getByRole('button', { name: /关闭/ }).click()
  await page.getByLabel('不处理').check()
  await page.locator('.modal').getByRole('button', { name: '关闭工单' }).click()
  await expect(page.locator('.badge.closed').first()).toContainText('已关闭 · 不处理')
  await page.locator('.actrow').getByRole('button', { name: /重开/ }).click()
  await page.locator('.modal').getByRole('button', { name: '重新开启' }).click()
  await expect(page.locator('.badge.myturn, .badge.waiting').first()).toBeVisible()
})

// 7. 建单（自由格式）：缺维度标签被拦下，补上后创建成功并指派，
//    完成页显示交接入口。
test('建单（自由格式）：缺维度标签被拦，指派后显示交接入口', async ({ page }) => {
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '建单' }).click()
  await page.getByRole('button', { name: /我自己填/ }).click()

  // 第一步：选「自由格式」
  await page.getByText('自由格式').click()

  // 标题 + 说明 + 一个不带维度的标签（先不足以提交）
  await page.getByLabel('标题').fill('给王工的紧急任务')
  await page.getByLabel(/说明/).fill('把对照表整理一份，周五前交。')
  await page.getByLabel('标签输入').fill('待初筛')
  await page.getByRole('button', { name: '添加', exact: true }).click()
  await expect(page.locator('.lblrow .chip')).toContainText('待初筛')

  // 只有普通标签：提交被 dimension_label_required 拦下
  await page.getByRole('button', { name: '创建工单' }).click()
  await expect(page.locator('.errbox')).toContainText('dimension_label_required')

  // 补一个维度标签（写别名 1680，自动换成标准值 机型:US-1680），
  // 指派给王工后创建成功
  await page.getByLabel('标签输入').fill('1680')
  await page.getByRole('button', { name: '添加', exact: true }).click()
  await expect(page.locator('.lblrow .chip').filter({ hasText: '机型:US-1680' })).toBeVisible()
  await page.getByLabel('指派给').selectOption('王工')
  await page.getByRole('button', { name: '创建工单' }).click()

  // 完成页：编号与交接文案
  await expect(page.locator('.donet')).toContainText('已创建')
  const share = page.locator('[data-handoff-preview]')
  await expect(share).toContainText('给你派了一张工单')
  await expect(share).toContainText('下面这段交给你的 Agent')
  const shareText = ((await share.textContent()) ?? '').trim()
  expect(shareText).toMatch(/\/t\/\d{8}-\d{3}/)

  // share_text 里的编号能打开
  const number = shareText.match(/\/t\/(\d{8}-\d+)/)![1]
  await page.getByRole('button', { name: '打开工单' }).click()
  await expect(page).toHaveURL(new RegExp(`/t/${number}$`))
  await expect(page.getByRole('heading', { name: '给王工的紧急任务' })).toBeVisible()
})

// 8. 建单（模板）：缺必填项弹出确认（只提醒不拦截），仍然创建后逐项提示。
test('建单（模板）：缺必填项弹出确认，仍创建后逐项提示', async ({ page }) => {
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '建单' }).click()
  await page.getByRole('button', { name: /我自己填/ }).click()

  // 第一步：选模板「Bug 反馈」
  const bugTemplate = page.getByRole('button', { name: /^Bug 反馈 反馈/ })
  await expect(bugTemplate).toContainText('适用：仪器或软件坏了、行为不对')
  await bugTemplate.click()
  await expect(page.getByText('哪台仪器？')).toBeVisible()

  // 只填标题和一个维度字段，其余必填留空；统一附件区暂存一个文件
  await page.getByLabel('标题').fill('BM-10vet 质控偏低')
  const dim = page.getByRole('combobox', { name: '哪台仪器？' })
  await dim.fill('BM-10vet')
  await dim.press('Enter') // 选第一个候选（可按别名搜索的下拉框，#39）
  await page.setInputFiles('input[aria-label="选择附件"]', {
    name: '现场日志.log',
    mimeType: 'text/plain',
    buffer: Buffer.from('E-203 试剂余量不足\n'),
  })
  await expect(page.locator('.staged-file')).toContainText('现场日志.log')

  // 维度标签：选中的机型值自动成为标签；但建单请求仍要显式维度标签吗？
  // 模板的维度字段值会自动并入标签，无需手填。
  await page.getByRole('button', { name: '创建工单' }).click()

  // 弹出「还有必填项没填」确认框，列出未填的必填字段
  const confirm = page.locator('.modal')
  await expect(confirm).toContainText('还有必填项没填')
  await expect(confirm.locator('.misslist li').first()).toBeVisible()
  await confirm.getByRole('button', { name: '仍然创建' }).click()

  // 创建成功；完成页提示必填未填的项（已按约定放行）
  await expect(page.locator('.donet')).toContainText('已创建')
  await expect(page.locator('.warnbox')).toContainText('必填未填')

  // 打开工单：说明气泡按字段显示，未填的显示（未填写），机型值同时进了标签；
  // 暂存的附件建单后挂到整单附件条，不进入任何字段
  await page.getByRole('button', { name: '打开工单' }).click()
  await expect(page.locator('[data-bubble="brief"]')).toContainText('BM-10vet')
  await page.locator('[data-bubble="brief"]').getByRole('button', { name: /另有 \d+ 项未填写 ▸/ }).click()
  await expect(page.getByText('（未填写）').first()).toBeVisible()
  await expect(page.locator('[data-bubble="brief"] .atts .att')).toHaveCount(1)
  await expect(page.locator('[data-bubble="brief"] .atts .att')).toContainText('现场日志.log')
})

// 9. 访客链接的「指定日期」：创建与延期都能选日期（当天有效，评审 P2-9）；
//    撤销后链接行不再提供延期。
test('访客链接支持指定日期创建与延期', async ({ page, request }) => {
  const number = await createTicket(request, { title: '指定日期访客链接的工单' })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await page.getByRole('button', { name: '访客链接' }).click()

  const pad = (n: number) => String(n).padStart(2, '0')
  const fmt = (iso: string) => `${parseInt(iso.slice(5, 7), 10)}月${parseInt(iso.slice(8, 10), 10)}日`
  const pickDate = new Date(Date.now() + 45 * 86400_000) // 今天 +45 天
  const pickStr = `${pickDate.getFullYear()}-${pad(pickDate.getMonth() + 1)}-${pad(pickDate.getDate())}`
  const pickLabel = `${fmt(pickStr)}为止` // 日期写法当天有效：展示为所选那天「为止」

  // 创建：有效期选「指定日期」，到期日按当天有效展示
  await page.getByLabel('备注').fill('外包-赵')
  await page.getByLabel('有效期').selectOption('date')
  await page.getByLabel('到期日期').fill(pickStr)
  await page.getByRole('button', { name: '创建', exact: true }).click()
  await expect(page.getByText('✓ 已创建（完整链接只在创建时出现一次）')).toBeVisible()
  const row = page.locator('.mini-tbl tr', { hasText: '外包-赵' })
  await expect(row).toContainText(pickLabel)

  // 延期到另一个日期（今天 +60 天）：行内日期输入 + 延期到该日
  const laterDate = new Date(Date.now() + 60 * 86400_000)
  const laterStr = `${laterDate.getFullYear()}-${pad(laterDate.getMonth() + 1)}-${pad(laterDate.getDate())}`
  const laterLabel = `${fmt(laterStr)}为止`
  await row.getByLabel('指定日期 外包-赵').fill(laterStr)
  await row.getByRole('button', { name: '延期到该日' }).click()
  await expect(page.getByText(`✓ 已延期到 ${laterStr}（当天有效）`)).toBeVisible()
  await expect(row).toContainText(laterLabel)

  // 撤销后不再提供延期（已失效，不能延期）
  await row.getByRole('button', { name: '撤销', exact: true }).click()
  await expect(row).toContainText('已失效，不能延期')
  await expect(row.getByRole('button', { name: '延期 30 天' })).toHaveCount(0)
  await expect(row.getByRole('button', { name: '延期到该日' })).toHaveCount(0)
})

// 10. 更多筛选（评审 P2-11）：按标签（别名可用）与创建时间范围筛出目标工单。
test('工单列表更多筛选：标签（别名）与时间范围', async ({ page, request }) => {
  const a = await createTicket(request, { title: '要按标签筛中的工单', labels: ['机型:UD-2300'] })
  const b = await createTicket(request, { title: '不该出现的工单', labels: ['机型:US-1680'] })

  // 给 UD-2300 挂一个别名，筛别名也应命中（服务端解析成标准值）。
  await loginAdmin(request)
  const alias = await request.post('/api/v1/labels/aliases', {
    data: { label: '机型:UD-2300', alias: '优典两千三' },
  })
  expect(alias.ok(), await alias.text()).toBeTruthy()

  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '工单' }).click()
  await page.getByRole('button', { name: /更多筛选/ }).click()

  // 标签：填别名 → 只有 A（标准值 机型:UD-2300）
  await page.getByRole('combobox', { name: '标签' }).fill('优典两千三')
  await page.getByRole('combobox', { name: '标签' }).blur()
  await expect(page.locator('.tcard', { hasText: a })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: b })).toHaveCount(0)

  // 时间范围：创建于「明天起」→ 一张都没有
  const pad = (n: number) => String(n).padStart(2, '0')
  const tomorrow = new Date(Date.now() + 86400_000)
  await page.getByLabel('时间字段').selectOption('created')
  await page.getByLabel('从（含当天）').fill(`${tomorrow.getFullYear()}-${pad(tomorrow.getMonth() + 1)}-${pad(tomorrow.getDate())}`)
  await expect(page.getByText('没有符合条件的工单')).toBeVisible()
  await expect(page.locator('.tcard', { hasText: a })).toHaveCount(0)

  // 改回「今天起」→ A 又出现（时间条件与标签条件同时生效）
  const today = new Date()
  await page.getByLabel('从（含当天）').fill(`${today.getFullYear()}-${pad(today.getMonth() + 1)}-${pad(today.getDate())}`)
  await expect(page.locator('.tcard', { hasText: a })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: b })).toHaveCount(0)
})

// 11. 成员网页的幂等重试（评审 P2-13，ADR 0002）：服务中断时发送回复，
// 自动重试复用同一个 Idempotency-Key，恢复后只产生一条回复。
test('成员回复在服务中断时自动重试且只产生一条', async ({ page, request }) => {
  const number = await createTicket(request, { title: '自动重试的成员回复' })
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  // 模拟服务不可用：拦下回复请求
  await page.route('**/api/v1/tickets/*/replies', (route) => route.abort('connectionfailed'))
  await page.getByPlaceholder(/写回复/).fill('网络抖动时发的回复。')
  await page.getByRole('button', { name: /发送/ }).click()
  await expect(page.getByText('服务更新中，回复没有丢失，正在自动重试')).toBeVisible()

  // 恢复服务：自动重试成功，回复出现；库里只有这一条。
  await page.unroute('**/api/v1/tickets/*/replies')
  await expect(page.locator('[data-bubble="reply"]').getByText('网络抖动时发的回复。')).toBeVisible({ timeout: 20000 })
  const res = await request.get(`/api/v1/tickets/${number}`)
  expect(res.ok()).toBeTruthy()
  const ticket = await res.json()
  const same = ticket.timeline.filter((e: any) => e.type === 'reply' && e.body_md === '网络抖动时发的回复。')
  expect(same).toHaveLength(1)
})

// 12. 建单同样复用幂等键：中断后自动重试只建一张单。
test('建单在服务中断时自动重试且只建一张', async ({ page, request }) => {
  await login(page, ADMIN)
  await page.goto('/new')
  await page.getByRole('button', { name: /我自己填/ }).click()
  await page.locator('.tplpick', { hasText: '自由格式' }).click()
  await page.getByLabel('标题').fill('网络抖动时建的单')
  await page.getByLabel('说明', { exact: true }).fill('内容。')
  await page.getByLabel('标签输入').fill('机型:UD-2300')
  await page.getByLabel('标签输入').press('Enter')
  await expect(page.locator('.lblrow .chip').filter({ hasText: '机型:UD-2300' })).toBeVisible()

  // 模拟服务不可用：拦下建单请求
  await page.route('**/api/v1/tickets', (route) => route.abort('connectionfailed'))
  await page.getByRole('button', { name: /创建/ }).click()
  await expect(page.getByText('服务更新中，正在自动重试')).toBeVisible()

  // 恢复服务：自动重试成功；工单列表里只有这一张。
  await page.unroute('**/api/v1/tickets')
  await expect(page.getByText(/已创建/)).toBeVisible({ timeout: 20000 })
  await loginAdmin(request)
  const res = await request.get('/api/v1/tickets?q=' + encodeURIComponent('网络抖动时建的单') + '&status=all')
  expect(res.ok()).toBeTruthy()
  const body = await res.json()
  expect(body.items).toHaveLength(1)
})
