import { test, expect, type APIRequestContext, type Page } from '@playwright/test'

// 成员网页（#41）：收件箱、列表、详情、建单。对真实服务 + 构建好的前端跑用例，
// 造数据（成员、工单、回复、访客链接）全部通过 HTTP API 完成；成员由
// e2e/start-server.mjs 预先创建（admin / 管理员 与 wang / 王工）。

export const ADMIN = { username: 'admin', password: 'admin-pass-123' }
export const WANG = { username: 'wang', password: 'wang-pass-123' }

export async function login(page: Page, who: { username: string; password: string }) {
  await page.goto('/login')
  await page.getByLabel('用户名').fill(who.username)
  await page.getByLabel('密码').fill(who.password)
  await page.getByRole('button', { name: '登录' }).click()
  await page.waitForURL((u) => !u.pathname.endsWith('/login'))
}

export async function loginAdmin(request: APIRequestContext) {
  const res = await request.post('/api/v1/session', { data: ADMIN })
  expect(res.ok(), '管理员登录失败').toBeTruthy()
}

/** 用管理员会话建一张自由格式工单（可指派），返回编号。 */
export async function createTicket(
  request: APIRequestContext,
  opts: { title: string; labels?: string[]; assignee?: string } = { title: '测试工单' },
): Promise<string> {
  await loginAdmin(request)
  const res = await request.post('/api/v1/tickets', {
    data: {
      title: opts.title,
      brief_md: `${opts.title} 的说明。`,
      labels: opts.labels ?? ['机型:UD-2300'],
      ...(opts.assignee ? { assignee: opts.assignee } : {}),
    },
  })
  expect(res.status(), `建单失败：${await res.text()}`).toBe(201)
  return (await res.json()).number
}

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
  await expect(page).toHaveURL(new RegExp(`/t/${number}$`))
})

// 2. 工单列表：按状态、指派、操作方（本人/Agent）筛选，按关键词搜索。
test('工单列表按状态、指派与本人/Agent筛选，支持关键词', async ({ page, request }) => {
  // 造三张：A 成员建的开启未指派；B 成员 Agent 建的开启指派给我；C 已关闭。
  const a = await createTicket(request, { title: '成员本人创建的工单', labels: ['机型:UD-2300'] })
  const b = await createTicketViaAgent(request, '成员 Agent 创建的工单', '管理员')
  const c = await createTicket(request, { title: '随后被关闭的工单', labels: ['机型:US-1680'] })
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
  await expect(page.locator('.tcard', { hasText: b })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: a })).toHaveCount(0)
  await grp('操作方').getByRole('button', { name: '本人' }).click()
  await expect(page.locator('.tcard', { hasText: a })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: b })).toHaveCount(0)
  await grp('操作方').getByRole('button', { name: '全部' }).click()

  // 指派 = 指派给我：只剩 B
  await grp('指派').getByRole('button', { name: '指派给我' }).click()
  await expect(page.locator('.tcard', { hasText: b })).toBeVisible()
  await expect(page.locator('.tcard', { hasText: a })).toHaveCount(0)
  await grp('指派').getByRole('button', { name: '全部' }).click()

  // 关键词：状态切回全部后，只命中标题含关键词的那张
  await grp('状态').getByRole('button', { name: '全部' }).click()
  await page.getByPlaceholder(/搜索/).fill('随后被关闭')
  await page.getByPlaceholder(/搜索/).press('Enter')
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
  const tokenRes = await request.post('/api/v1/me/agent-tokens', { data: { name: `agent-${title}` } })
  expect(tokenRes.ok()).toBeTruthy()
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
  await page.getByText('自由格式').click()
  await page.getByLabel('标题').fill('给王工的派活工单')
  await page.getByLabel('说明').fill('把对照表整理一份，周五前交。')
  await page.getByLabel('标签输入').fill('机型:SL-1000')
  await page.getByRole('button', { name: '添加', exact: true }).click()
  await page.getByLabel('指派给').selectOption('王工')
  await page.getByRole('button', { name: '创建工单' }).click()
  await expect(page.locator('.donet')).toContainText('已创建')
  const shareText = (await page.locator('.linkbox').textContent()) ?? ''
  const number = shareText.match(/\/t\/(\d{8}-\d+)/)![1]

  // B（王工）登录：收件箱出现「新指派给你」
  await login(page, WANG)
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

// 4. 详情页创建访客链接并复制（验收标准 2）。
test('详情页创建访客链接并复制', async ({ page, request }) => {
  const number = await createTicket(request, { title: '要发访客链接的工单' })
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)
  await page.goto(`/t/${number}`)

  await page.getByRole('button', { name: '访客链接' }).click()
  await page.getByLabel('备注').fill('检验科-张三')
  await page.getByRole('button', { name: '创建', exact: true }).click()

  // 链接只出现一次：显示在链接框里，复制后剪贴板内容一致
  const linkText = await page.locator('.linkbox').textContent()
  expect(linkText).toMatch(/^\/g\/tbg_/)
  await page.getByRole('button', { name: '复制链接' }).click()
  await expect(page.getByText('已复制')).toBeVisible()
  const clip = await page.evaluate(() => navigator.clipboard.readText())
  expect(clip).toMatch(/^\/g\/tbg_/)
  expect(clip.trim()).toBe(linkText!.trim())

  // 创建事件记入时间线；复制出的链接真的能打开（访客页显示工单标题）
  await page.getByRole('button', { name: '关闭', exact: true }).click()
  await expect(page.locator('.conv .event').last()).toContainText('创建了访客链接「检验科-张三」')
  await page.goto(clip)
  await expect(page.getByRole('heading', { name: '要发访客链接的工单' })).toBeVisible()
})

// 5. 时间线按「本人 / Agent」筛选后结果正确（验收标准 3）。
test('时间线按本人/Agent筛选后结果正确', async ({ page, request }) => {
  const number = await createTicket(request, { title: '有本人与Agent回复的工单', assignee: '管理员' })
  await loginAdmin(request)
  const tokenRes = await request.post('/api/v1/me/agent-tokens', { data: { name: '时间线筛选' } })
  const token: string = (await tokenRes.json()).token
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
  expect(alias.ok(), await alias.text()).toBeTruthy()
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

  // 指派给王工：出现 share_text
  await page.locator('.actrow').getByRole('button', { name: /指派|改派/ }).click()
  await page.getByLabel('指派给').selectOption('王工')
  await page.getByRole('button', { name: /确认指派|确认改派/ }).click()
  await expect(page.locator('.linkbox')).toContainText('转给你了')
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
//    完成页显示可复制的 share_text。
test('建单（自由格式）：缺维度标签被拦，指派后显示 share_text', async ({ page }) => {
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '建单' }).click()

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

  // 完成页：编号、share_text 与复制按钮
  await expect(page.locator('.donet')).toContainText('已创建')
  const share = page.locator('.linkbox')
  await expect(share).toContainText('王工')
  await expect(share).toContainText('让你的 Agent')
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

  // 第一步：选模板「Bug 反馈」
  await page.locator('.tplpick', { hasText: 'Bug 反馈' }).click()
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
  // 暂存的附件建单后已上传并写进说明
  await page.getByRole('button', { name: '打开工单' }).click()
  await expect(page.locator('[data-bubble="brief"]')).toContainText('BM-10vet')
  await expect(page.getByText('（未填写）').first()).toBeVisible()
  await expect(page.locator('[data-bubble="brief"]')).toContainText('现场日志.log')
})
