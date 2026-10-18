import { test, expect, type APIRequestContext, type Browser, type Page } from '@playwright/test'
import { ADMIN, login, loginAdmin } from './helpers'

// 访客落地页与自由格式（#66）：持「可填写说明」链接的访客第一次打开链接
// 先看到落地页（谁请你补充哪张反馈的说明、共几项、几项必填、可以传照片日志、
// 写到一半关掉也不丢），「开始填写」进分组填写页，「先不填，直接回复」直接
// 进对话页；补全过说明（时间线上已有访客的修订事件）或链接没有「可填写
// 说明」权限时，直接进对话页。无模板的「自由格式」工单显示成一组「情况
// 说明」。对真实服务 + 构建好的前端跑用例，造数据全部通过 HTTP API 完成。

interface Seed {
  number: string
  linkKey: string
}

/** 用「请人反馈」的方式建单（POST /api/v1/tickets 带 guest_link，与向导同一
 *  接口）：默认 Bug 反馈模板；free 为真时是一张不用模板的反馈工单。 */
async function requestFeedback(
  request: APIRequestContext,
  opts: { title: string; free?: boolean; note?: string },
): Promise<Seed> {
  await loginAdmin(request)
  const res = await request.post('/api/v1/tickets', {
    data: {
      title: opts.title,
      labels: [],
      guest_link: { note: opts.note ?? '检验科-张三', valid_days: 30, can_edit_brief: true },
      ...(opts.free ? { kind: 'report', brief_md: '' } : { template: 'bug-report', fields: {} }),
    },
  })
  expect(res.status(), `请人反馈建单失败：${await res.text()}`).toBe(201)
  const body = await res.json()
  return { number: body.number, linkKey: String(body.guest_link.url).slice(3) }
}

/** 画一张小 PNG（页面里用 canvas 生成）。 */
function smallPng(page: Page, width = 600, height = 400): Promise<Uint8Array> {
  return page.evaluate(async ({ width, height }) => {
    const c = document.createElement('canvas')
    c.width = width
    c.height = height
    const ctx = c.getContext('2d')!
    ctx.fillStyle = '#e8f4e8'
    ctx.fillRect(0, 0, width, height)
    ctx.fillStyle = '#333'
    ctx.font = '28px sans-serif'
    ctx.fillText('E-203 报警截图', 40, 200)
    const blob = await new Promise<Blob | null>((r) => c.toBlob(r, 'image/png')); if (!blob) throw new Error('toBlob 失败')
    return new Uint8Array(await blob.arrayBuffer())
  }, { width, height })
}

/** 填完 Bug 反馈模板的 6 个必填项（维度「不确定」+ 第二组四个「不清楚」+
 *  单选「每次」）；手机要先「下一组」，桌面双栏全部展开直接填。 */
async function fillBugRequired(page: Page, mobile: boolean, model = '不确定') {
  await page.getByRole('combobox', { name: '哪台仪器？' }).click()
  await page.getByRole('option', { name: model }).click()
  if (mobile) {
    await page.getByRole('button', { name: '下一组：发生了什么 ›' }).click()
  }
  for (const q of ['什么时候发生的？', '你做了哪些操作？', '实际看到了什么？', '你期望看到什么？']) {
    await page.locator(`.cf[data-field="${q}"]`).getByRole('button', { name: '不清楚', exact: true }).click()
  }
  if (mobile) {
    await page.locator('.grp').nth(2).locator('.grp-hd').click()
  }
  await page.locator('.cf[data-field="出现得频繁吗？"]').getByRole('radio', { name: '每次' }).check()
}

// 1. 用请人反馈的方式建单后，第一次打开链接显示落地页：成员名、标题、
//    问题数、必填数正确，称呼预填链接备注。
test.describe('落地页', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('第一次打开链接显示落地页，称呼预填链接备注', async ({ page, request }) => {
    const seed = await requestFeedback(request, { title: 'UD-2300 质控时报警 E-203' })
    await page.goto(`/g/${seed.linkKey}`)

    const landing = page.locator('.guest-landing')
    await expect(landing).toBeVisible()
    await expect(landing.locator('.landing-lead')).toHaveText('管理员 请你补充这张反馈的说明')
    await expect(landing.getByRole('heading', { name: 'UD-2300 质控时报警 E-203' })).toBeVisible()
    await expect(landing.locator('.landing-info')).toContainText('9 项（6 项必填）')
    await expect(landing.locator('.landing-info')).toContainText('可以上传照片、截图和日志')
    await expect(landing.locator('.landing-info')).toContainText('内容保存在这台设备上')
    await expect(landing.getByLabel('你的称呼（管理员会看到）')).toHaveValue('检验科-张三')
    await expect(landing.getByRole('button', { name: '开始填写' })).toBeVisible()
    await expect(landing.getByRole('button', { name: '先不填，直接回复 →' })).toBeVisible()
  })

  // 2. 「先不填，直接回复 →」直接进对话页，能回复。
  test('「先不填，直接回复」直接进对话页，能回复', async ({ page, request }) => {
    const seed = await requestFeedback(request, { title: '先不填直接回复的工单' })
    await page.goto(`/g/${seed.linkKey}`)
    await page.getByRole('button', { name: '先不填，直接回复 →' }).click()

    // 对话页：说明卡片 + 回复框；不再有落地页
    await expect(page.locator('[data-bubble="brief"]')).toBeVisible()
    await expect(page.locator('.guest-landing')).toHaveCount(0)
    await page.getByLabel('署名').fill('检验科 张三')
    await page.getByPlaceholder(/写回复/).fill('我先口头说个大概：仪器今天上午报 E-203。')
    await page.getByRole('button', { name: '发送' }).click()
    const reply = page.locator('[data-bubble="reply"]').last()
    await expect(reply).toContainText('E-203', { timeout: 15000 })
    await expect(reply).toContainText('张三')
  })

  // 3. 开始填写 → 提交后，再次打开链接直接进对话页。
  test('开始填写提交后，再次打开链接直接进对话页', async ({ page, request }) => {
    const seed = await requestFeedback(request, { title: '再次打开直接进对话' })
    await page.goto(`/g/${seed.linkKey}`)
    await page.getByRole('button', { name: '开始填写' }).click()
    await expect(page.locator('.grp').first()).toBeVisible()

    // 落地页上的称呼带进填写页的署名（预填链接备注）
    await expect(page.getByLabel('署名')).toHaveValue('检验科-张三')

    await fillBugRequired(page, true)
    await page.getByRole('button', { name: /^提交给管理员/ }).click()
    await expect(page.locator('.fill-done')).toContainText('已提交给管理员')
    await page.getByRole('button', { name: '查看对话' }).click()
    await expect(page.locator('.event', { hasText: '修订了说明' })).toContainText('检验科-张三', { timeout: 15000 })

    // 再次打开链接：直接进对话页（说明卡片 + ✏ 修改说明），不再显示落地页
    await page.goto(`/g/${seed.linkKey}`)
    await expect(page.locator('.guest-landing')).toHaveCount(0)
    await expect(page.locator('[data-bubble="brief"]')).toBeVisible()
    await expect(page.getByRole('button', { name: '✏ 修改说明' })).toBeVisible()
  })

  // 4. 没有「可填写说明」权限的链接直接进对话页。
  test('没有「可填写说明」权限的链接直接进对话页', async ({ page, request }) => {
    await loginAdmin(request)
    const created = await request.post('/api/v1/tickets', {
      data: { title: '只读链接的工单', brief_md: '说明一整块。', labels: ['机型:UD-2300'] },
    })
    expect(created.status(), `建单失败：${await created.text()}`).toBe(201)
    const number: string = (await created.json()).number
    const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
      data: { note: '检验科-张三', valid_days: 30, can_edit_brief: false },
    })
    expect(link.ok(), `创建访客链接失败：${await link.text()}`).toBeTruthy()
    const linkKey = String((await link.json()).url).slice(3)

    await page.goto(`/g/${linkKey}`)
    await expect(page.locator('.guest-landing')).toHaveCount(0)
    await expect(page.locator('[data-bubble="brief"]')).toBeVisible()
    await expect(page.getByRole('button', { name: '✏ 修改说明' })).toHaveCount(0)
  })
})

// 5. 自由格式工单显示一组「情况说明」：大文本框上方列提示点、有追问提示、
//    下面是附件区；提交后说明里能看到内容。
test.describe('自由格式', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('自由格式工单显示「情况说明」组，能上传附件，提交后说明里有内容', async ({ page, request }) => {
    const seed = await requestFeedback(request, { title: '生化仪结果漂移', free: true, note: '门诊-李四' })
    await page.goto(`/g/${seed.linkKey}`)

    // 落地页：写一段说明；称呼预填链接备注
    await expect(page.locator('.landing-info')).toContainText('写一段说明')
    await expect(page.getByLabel('你的称呼（管理员会看到）')).toHaveValue('门诊-李四')
    await page.getByRole('button', { name: '开始填写' }).click()

    // 一组「情况说明」：副标题 + 大文本框，上方列出可以按哪几点写
    const groups = page.locator('.grp')
    await expect(groups).toHaveCount(1)
    await expect(groups.nth(0).locator('.grp-name')).toHaveText('情况说明')
    await expect(groups.nth(0).locator('.grp-sub')).toHaveText('写一段，附上照片和日志')
    const tips = groups.nth(0).locator('.tips li')
    await expect(tips).toHaveCount(6)
    await expect(tips.nth(0)).toContainText('哪台设备')
    await expect(tips.nth(3)).toContainText('看到了什么')
    await expect(tips.nth(5)).toContainText('频繁')

    // 必填没写不能提交；写短了出现追问提示，写长后消失
    const submit = page.getByRole('button', { name: /^提交给管理员/ })
    await expect(submit).toBeDisabled()
    const box = groups.nth(0).locator('.cf:not(.area) textarea')
    await box.fill('今天报错')
    await expect(groups.nth(0).locator('.nudge')).toBeVisible()
    await expect(groups.nth(0).locator('.nudge')).toContainText(
      '能再具体一点吗？比如：哪台设备、什么时候、做了什么、看到什么、本来应该怎样、频繁吗',
    )
    await box.fill('BH-7600CS 今天上午 9 点做质控时报 E-203，重试两次都在同一步报警，平时每天都正常。')
    await expect(groups.nth(0).locator('.nudge')).toHaveCount(0)
    await expect(page.locator('.fill-prog')).toHaveText(/必填都写好了/)

    // 附件区在组末：上传图片显示在那里，不写进大文本框
    const png = await smallPng(page)
    await groups.nth(0).locator('.cf.area input[type=file]').setInputFiles({
      name: '报警照片.png',
      mimeType: 'image/png',
      buffer: Buffer.from(png),
    })
    await expect(groups.nth(0).locator('.cf.area .staged-file.ready')).toContainText('报警照片.png', { timeout: 15000 })
    await expect(box).not.toHaveValue(/att:/)

    await submit.click()
    await expect(page.locator('.fill-done')).toContainText('已提交给管理员')
    await page.getByRole('button', { name: '查看对话' }).click()

    // 对话页：说明里能看到内容，附件在说明底部；时间线有访客的修订事件
    const brief = page.locator('[data-bubble="brief"]')
    await expect(brief).toContainText('E-203')
    await expect(brief.locator('.atts .att')).toContainText('报警照片.png', { timeout: 15000 })
    const event = page.locator('.event', { hasText: '修订了说明' })
    await expect(event).toBeVisible({ timeout: 15000 })
    await expect(event).toContainText('门诊-李四')
    await expect(event).toContainText('第 2 版')
  })
})

/** 完整跑通用例 1（#53）：成员向导生成链接 → 访客补全说明 → 成员在工单上
 *  看到说明和事件。成员页与访客页（独立上下文，不带成员会话）都用同一视口。 */
async function runFullUseCase(
  page: Page,
  browser: Browser,
  viewport: { width: number; height: number },
) {
  const mobile = viewport.width < 768
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)

  // 成员向导：收件箱卡片进入，两项必填（默认 Bug 反馈），生成链接
  await page.getByRole('button', { name: '＋ 请人反馈' }).click()
  await expect(page.getByRole('heading', { name: '请人反馈' })).toBeVisible()
  await page.getByLabel('请谁反馈').fill('检验科-张三')
  await page.getByLabel('什么事').fill('完整用例：UD-2300 质控报 E-203')
  await page.getByRole('button', { name: '生成链接' }).click()
  const done = page.locator('.donet')
  await expect(done).toContainText(/已创建/)
  const number = ((await done.locator('b').textContent()) ?? '').trim()
  expect(number).toMatch(/^\d{8}-\d{3,}$/)

  // 只复制链接；访客在独立上下文打开（同一视口）
  await page.getByRole('button', { name: '只复制链接' }).click()
  const linkUrl = await page.evaluate(() => navigator.clipboard.readText())
  expect(linkUrl).toMatch(/\/g\/tbg_/)

  const ctx = await browser.newContext({ viewport, baseURL: new URL(page.url()).origin })
  const guest = await ctx.newPage()
  await guest.goto(linkUrl)

  // 落地页（成员名 + 标题 + 称呼预填）→ 开始填写 → 填完必填 → 提交 → 确认页 → 查看对话
  const landing = guest.locator('.guest-landing')
  await expect(landing).toBeVisible()
  await expect(landing.locator('.landing-lead')).toHaveText('管理员 请你补充这张反馈的说明')
  await expect(landing.getByRole('heading', { name: '完整用例：UD-2300 质控报 E-203' })).toBeVisible()
  await expect(landing.getByLabel('你的称呼（管理员会看到）')).toHaveValue('检验科-张三')
  await guest.getByRole('button', { name: '开始填写' }).click()
  await expect(guest.locator('.grp').first()).toBeVisible()
  await fillBugRequired(guest, mobile, 'UD-2300')
  await guest.getByLabel('署名').fill('检验科 张三')
  await guest.getByRole('button', { name: /^提交给管理员/ }).click()
  await expect(guest.locator('.fill-done')).toContainText('已提交给管理员')
  await guest.getByRole('button', { name: '查看对话' }).click()

  // 访客对话页：说明卡片有内容，时间线有「修订了说明」事件
  await expect(guest.locator('[data-bubble="brief"]')).toContainText('UD-2300')
  await expect(guest.locator('.event', { hasText: '修订了说明' })).toContainText('检验科 张三', { timeout: 15000 })

  // 成员在工单上看到说明和事件
  await page.getByRole('button', { name: '打开工单' }).click()
  await expect(page.locator('[data-bubble="brief"]')).toContainText('UD-2300')
  await expect(page.locator('.event', { hasText: '修订了说明' })).toContainText('检验科 张三', { timeout: 15000 })
  await ctx.close()
}

// 6. 完整跑通用例 1：手机视口和桌面视口各跑一次。
test.describe('完整用例（手机视口）', () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test('成员向导 → 访客补全说明 → 成员看到说明和事件', async ({ page, browser }) => {
    await runFullUseCase(page, browser, { width: 390, height: 844 })
  })
})

test.describe('完整用例（桌面视口）', () => {
  test.use({ viewport: { width: 1280, height: 800 } })
  test('成员向导 → 访客补全说明 → 成员看到说明和事件', async ({ page, browser }) => {
    await runFullUseCase(page, browser, { width: 1280, height: 800 })
  })
})
