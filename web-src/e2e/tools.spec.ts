import { test, expect } from '@playwright/test'
import { ADMIN, createTicket, login, wang } from './helpers'

// 成员网页（#42）：模板维护、标签字典与别名、个人设置（Agent token）、
// 管理页（成员账号、标签治理）。对真实服务 + 构建好的前端跑用例；
// 造数据走 HTTP API。王工是普通成员（start-server.mjs 用 #42 的接口创建）。
// RUN 后缀让模板 key、Agent 名、别名、账号名、字典值每次运行都唯一：
// 服务被复用（Playwright reuseExistingServer）或用例次序变化时互不干扰。
const RUN = String(Date.now() % 1_000_000)

// 1.（验收）保存格式错误的模板时看到行号；修正后保存，产生新版本。
test('模板维护：格式错误报行号，修正后保存产生新版本', async ({ page }) => {
  await login(page, ADMIN)
  await page.goto('/templates')

  // 列表里有预置模板
  await expect(page.locator('.tcard', { hasText: 'Bug 反馈' })).toBeVisible()

  // 新建：先写一处格式错误（第 3 行类型不合法）
  await page.getByRole('button', { name: '＋ 新建模板' }).click()
  await page.locator('#tpl-key').fill(`site-visit-${RUN}`)
  const src = page.getByLabel('模板源码')
  await src.fill(
    ['---', '名称: 现场巡检', '类型: 巡检', '预设标签: []', '---', '## 巡检了哪台仪器？ {维度:机型 必填}', '写铭牌上的型号。'].join(
      '\n',
    ),
  )
  await page.getByRole('button', { name: '创建模板' }).click()

  // 保存被拒绝，错误框里报出行号（第 3 行）
  const err = page.locator('.errbox')
  await expect(err).toContainText('template_format_error')
  await expect(err).toContainText('第 3 行')

  // 修正第 3 行后创建成功（v1），解析出的字段出现
  await src.fill(
    ['---', '名称: 现场巡检', '类型: 任务', '预设标签: []', '---', '## 巡检了哪台仪器？ {维度:机型 必填}', '写铭牌上的型号。'].join(
      '\n',
    ),
  )
  await page.getByRole('button', { name: '创建模板' }).click()
  await expect(page).toHaveURL(new RegExp(`/templates/site-visit-${RUN}$`))
  await expect(page.locator('.sec-t', { hasText: '现场巡检' })).toContainText('v1')
  await expect(page.locator('.fldlist')).toContainText('巡检了哪台仪器？')

  // 修改内容再保存 → v2；历史版本列出 v2、v1
  await src.fill(
    [
      '---',
      '名称: 现场巡检',
      '类型: 任务',
      '预设标签: []',
      '---',
      '## 巡检了哪台仪器？ {维度:机型 必填}',
      '写铭牌上的型号。',
      '## 有什么异常？ {必填}',
      '按时间顺序写。',
    ].join('\n'),
  )
  await page.getByRole('button', { name: '保存（产生新版本）' }).click()
  await expect(page.locator('.sec-t', { hasText: '现场巡检' })).toContainText('v2')
  await expect(page.locator('.verrow')).toHaveCount(2)
  await expect(page.locator('.verrow').first()).toContainText('v2')
  await expect(page.locator('.verrow').last()).toContainText('v1')

  // 停用：徽标变「已停用」，列表里保留
  await page.getByRole('button', { name: '停用模板' }).click()
  await page.locator('.modal').getByRole('button', { name: '停用', exact: true }).click()
  await expect(page.locator('.sec-t', { hasText: '现场巡检' }).locator('.badge.closed')).toBeVisible()
  await page.goto('/templates')
  const card = page.locator('.tcard', { hasText: '现场巡检' })
  await expect(card).toBeVisible()
  await expect(card.locator('.badge.closed')).toBeVisible()
})

// 2. 工具页：标签字典按维度浏览、resolve 解析、添加别名（写别名能匹配标准值）。
test('工具页：字典浏览、resolve 解析与添加别名', async ({ page }) => {
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '工具' }).click()

  // 维度卡片：机型下有种子值，别名区显示「（无别名）」
  await expect(page.locator('.mcard .sec-t', { hasText: '机型' })).toBeVisible()
  await expect(page.locator('table.mini-tbl .chip', { hasText: '机型:UD-2300' })).toBeVisible()

  // resolve：写型号的一部分 → fuzzy 命中标准值
  await page.getByLabel('解析输入').fill('2300')
  await page.getByRole('button', { name: '解析' }).click()
  // 解析结果表在 DOM 里排在字典浏览表之前（ResolveCard 先渲染）；两表都有同一 chip，
  // 用 .first() 明确断言解析结果的那个，避免 strict mode 命中两个元素。
  await expect(page.locator('.mini-tbl .chip', { hasText: '机型:UD-2300' }).first()).toBeVisible()
  await expect(page.locator('.mini-tbl').first()).toContainText('fuzzy')

  // 添加别名（每次运行唯一），之后 resolve 写别名 → alias 命中
  const alias = `1680x${RUN}`
  await page.getByLabel('别名维度').selectOption('机型')
  await page.getByLabel('标准值').selectOption('US-1680')
  await page.getByLabel('别名输入').fill(alias)
  await page.getByRole('button', { name: '添加', exact: true }).click()
  await expect(page.locator('.aliasbox', { hasText: alias }).first()).toBeVisible()

  await page.getByLabel('解析输入').fill(alias)
  await page.getByRole('button', { name: '解析' }).click()
  await expect(page.locator('.mini-tbl').first()).toContainText(`「${alias}」→ 标准值`)
})

// 3.（验收）签发 token，用它调用 API 时署名是这个 Agent；撤销后调用失败。
test('个人设置：签发 Agent token，署名正确，撤销后失效', async ({ page, request }) => {
  const number = await createTicket(request, { title: '验证 Agent token 的工单' })
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '我的' }).click()

  // 生成 token：只显示一次，形如 tbag_…
  const agentName = `巡检${RUN}`
  await page.getByRole('button', { name: '＋ 生成新 token' }).click()
  await page.locator('#tk-name').fill(agentName)
  await page.getByRole('button', { name: '生成', exact: true }).click()
  const tokenBox = page.locator('[data-token-value]')
  await expect(tokenBox).toBeVisible()
  const token = ((await tokenBox.textContent()) ?? '').trim()
  expect(token).toMatch(/^tbag_/)
  await page.locator('.modal').getByRole('button', { name: '关闭', exact: true }).click()

  // 列表里出现这个 Agent（使用中）
  await expect(page.locator(`tr[data-token="${agentName}"] .badge.open`)).toBeVisible()

  // 用它调 API：session 自报是这个具名 Agent；回复的署名是「管理员 的 Agent · 巡检」
  const agentHeaders = { Authorization: `Bearer ${token}` }
  const me = await request.get('/api/v1/session', { headers: agentHeaders })
  expect(me.status()).toBe(200)
  expect((await me.json()).viewer.agent.name).toBe(agentName)

  const reply = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: { body_md: 'Agent 通过 token 回复。' },
    headers: agentHeaders,
  })
  expect(reply.status()).toBe(201)

  // 网页上打开工单：回复气泡的署名是这个 Agent
  await page.goto(`/t/${number}`)
  const bubble = page.locator('[data-bubble="reply"]', { hasText: 'Agent 通过 token 回复。' })
  await expect(bubble).toBeVisible()
  await expect(bubble.locator('.who b')).toHaveText(`管理员 的 Agent · ${agentName}`)

  // 撤销：回到「我的」，确认后立即失效，再用它调 API 返回 401
  await page.goto('/me')
  await page.locator(`tr[data-token="${agentName}"]`).getByRole('button', { name: '撤销' }).click()
  await page.locator('.modal').getByRole('button', { name: '撤销', exact: true }).click()
  await expect(page.locator(`tr[data-token="${agentName}"] .badge.closed`)).toBeVisible()
  const failed = await request.get('/api/v1/session', { headers: agentHeaders })
  expect(failed.status()).toBe(401)
})

// 4.（验收）管理员合并两个标签值后，历史工单上的标签已改写。
// 用每次运行新建的字典值做合并（allow_new），不动种子值——其他用例还要用。
test('管理页：合并标签值后历史工单的标签已改写', async ({ page, request }) => {
  const from = `机型:E2EA${RUN}`
  const into = `机型:E2EB${RUN}`
  // 两张带 from 的旧工单；from/into 都用 allow_new 新建到字典
  const t1 = await createTicket(request, { title: '合并练习的旧工单一', labels: ['机型:UD-2300'] })
  const t2 = await createTicket(request, { title: '合并练习的旧工单二', labels: ['机型:UD-2300'] })
  for (const [n, label] of [
    [t1, from],
    [t2, from],
    [t1, into], // 目标值也要在字典里（挂在本单上无妨，改写后去重）
  ] as [string, string][]) {
    const res = await request.post(`/api/v1/tickets/${n}/labels`, {
      data: { add: [label], allow_new: true },
    })
    expect(res.ok(), `新建字典值 ${label}：${await res.text()}`).toBeTruthy()
  }

  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '我的' }).click()
  await page.getByRole('link', { name: '🛡 管理页' }).click()

  // 标签治理 → 机型 → 把 from 合并到 into
  await page.getByRole('tab', { name: '标签治理' }).click()
  await page.getByLabel('治理维度').selectOption('机型')
  await page.locator(`tr[data-label="${from}"]`).getByRole('button', { name: '合并到…' }).click()
  await page.getByLabel('合并目标').selectOption(into)
  await page.locator('.modal').getByRole('button', { name: '合并', exact: true }).click()
  await expect(page.locator('#toast')).toContainText(`已合并到 ${into}，2 张工单的标签已改写`)

  // 字典里不再列出 from（已停用）
  await expect(page.locator(`tr[data-label="${from}"]`)).toHaveCount(0)

  // 历史工单上的标签已改写：网页详情与 API 都只看到 into
  await page.goto(`/t/${t1}`)
  await page.getByText('标签 · 指派 · 访客链接').click()
  const meta = page.locator('.mmeta')
  await expect(meta.locator('.chip', { hasText: into })).toBeVisible()
  await expect(meta.locator('.chip', { hasText: from })).toHaveCount(0)
  for (const n of [t1, t2]) {
    const res = await request.get(`/api/v1/tickets/${n}`)
    expect(res.ok()).toBeTruthy()
    const labels: string[] = (await res.json()).labels
    expect(labels).toContain(into)
    expect(labels).not.toContain(from)
  }
})

// 5.（验收）非管理员看不到管理功能：UI 不显示，直接访问只有提示；接口 403。
test('非管理员看不到管理功能（UI 隐藏 + API 403）', async ({ page, request }) => {
  const WANG = await wang()
  const wangLogin = await request.post('/api/v1/session', { data: WANG })
  expect(wangLogin.ok(), `王工登录失败：${await wangLogin.text()}`).toBeTruthy()
  const wangCookie = (wangLogin.headers()['set-cookie'] ?? '').split(';')[0]
  const wangHeaders = { Cookie: wangCookie }

  await login(page, WANG)
  // 我的：没有「管理」入口；工具页也没有
  await page.locator('.b-tabs').getByRole('link', { name: '我的' }).click()
  await expect(page.getByRole('link', { name: /管理页/ })).toHaveCount(0)
  await page.locator('.b-tabs').getByRole('link', { name: '工具' }).click()
  await expect(page.getByRole('link', { name: /管理页/ })).toHaveCount(0)

  // 直接访问 /admin：只显示无权限提示，不出现成员账号 / 标签治理
  await page.goto('/admin')
  await expect(page.getByText('管理功能只对管理员开放')).toBeVisible()
  await expect(page.getByRole('tab', { name: '成员账号' })).toHaveCount(0)

  // API 双保险：成员账号与标签治理都 403 admin_required
  const createMember = await request.post('/api/v1/members', {
    data: { username: 'sneaky', name: '越权建号' },
    headers: wangHeaders,
  })
  expect(createMember.status()).toBe(403)
  expect((await createMember.json()).error.code).toBe('admin_required')

  const merge = await request.post('/api/v1/labels/merge', {
    data: { from: '机型:US-1680', into: '机型:SL-1000' },
    headers: wangHeaders,
  })
  expect(merge.status()).toBe(403)
  expect((await merge.json()).error.code).toBe('admin_required')

  // 非管理员能用成员功能（工具页的字典），但不能停用成员
  const deactivate = await request.post('/api/v1/members/1/deactivate', { headers: wangHeaders })
  expect(deactivate.status()).toBe(403)
})

// 6. 管理页成员账号：创建（初始密码只显示一次，新成员能登录）、停用（立即失效）。
test('管理页：创建成员账号并停用', async ({ page, request }) => {
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '我的' }).click()
  await page.getByRole('link', { name: '🛡 管理页' }).click()

  // 新建：填登录名与姓名 → 初始密码只显示一次
  const sun = `sun${RUN}`
  await page.getByRole('button', { name: '＋ 新建成员账号' }).click()
  await page.locator('#mm-username').fill(sun)
  await page.locator('#mm-name').fill('孙工')
  await page.getByRole('button', { name: '创建', exact: true }).click()
  const pwBox = page.locator('[data-initial-password]')
  await expect(pwBox).toBeVisible()
  const password = ((await pwBox.textContent()) ?? '').trim()
  expect(password.length).toBeGreaterThanOrEqual(8)
  await page.locator('.modal').getByRole('button', { name: '关闭', exact: true }).click()

  // 名单里出现孙工（使用中）
  await expect(page.locator(`tr[data-member="${sun}"] .badge.open`)).toBeVisible()

  // 新成员用初始密码能登录
  const sunLogin = await request.post('/api/v1/session', { data: { username: sun, password } })
  expect(sunLogin.ok(), '新成员用初始密码登录').toBeTruthy()
  const sunCookie = (sunLogin.headers()['set-cookie'] ?? '').split(';')[0]

  // 停用：立即失效（会话 401，再登录被拒 member_deactivated）
  await page.locator(`tr[data-member="${sun}"]`).getByRole('button', { name: '停用' }).click()
  await page.locator('.modal').getByRole('button', { name: '停用', exact: true }).click()
  await expect(page.locator(`tr[data-member="${sun}"] .badge.closed`)).toBeVisible()

  const alive = await request.get('/api/v1/members', { headers: { Cookie: sunCookie } })
  expect(alive.status()).toBe(401)
  const again = await request.post('/api/v1/session', { data: { username: sun, password } })
  expect(again.status()).toBe(401)
  expect((await again.json()).error.code).toBe('member_deactivated')

  // 管理员自己的行没有停用按钮
  await expect(page.locator('tr[data-member="admin"]').getByRole('button', { name: '停用' })).toHaveCount(0)

  // 系统状态（#45）已挂在管理页：备份状态卡 + 版本更新卡（细节见 admin.spec.ts）
  await expect(page.getByTestId('backup-card')).toBeVisible()
  await expect(page.getByTestId('update-card')).toBeVisible()
})
