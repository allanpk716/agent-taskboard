import { test, expect, type APIRequestContext, type Locator, type Page } from '@playwright/test'
import { openGuestPage } from './helpers'

// 访客网页 #39：反馈入口提交页（/i/<slug>）与「补全说明」表单。
// 对真实服务 + 构建好的前端跑用例，造数据（入口、别名、工单、访客链接）
// 全部通过 HTTP API 完成。

const ADMIN = { username: 'admin', password: 'admin-pass-123' }

async function loginAdmin(request: APIRequestContext) {
  const res = await request.post('/api/v1/session', { data: ADMIN })
  expect(res.ok(), '管理员登录失败').toBeTruthy()
}

// 造一个反馈入口（绑定预置 bug-report 模板），可选先给 UD-2300 挂一个别名。
async function seedIntake(
  request: APIRequestContext,
  opts: { alias?: boolean; contact?: string } = {},
): Promise<string> {
  await loginAdmin(request)
  if (opts.alias) {
    const res = await request.post('/api/v1/labels/aliases', {
      data: { label: '机型:UD-2300', alias: '2300血球仪' },
    })
    expect(res.ok(), `挂别名失败：${await res.text()}`).toBeTruthy()
  }
  const created = await request.post('/api/v1/intakes', {
    data: {
      slug: `bug-${Date.now()}`,
      name: '部门 Bug 反馈',
      template: 'bug-report',
      department_contact: opts.contact ?? 'IT 服务台 400-123-4567',
    },
  })
  expect(created.status(), `创建入口失败：${await created.text()}`).toBe(201)
  return (await created.json()).slug as string
}

// 造一张 Bug 反馈模板工单 + 一条带「可填写说明」权限的访客链接。
async function seedEditableTicket(request: APIRequestContext): Promise<{ number: string; linkKey: string }> {
  await loginAdmin(request)
  const created = await request.post('/api/v1/tickets', {
    data: {
      template: 'bug-report',
      title: 'UD-2300 质控时报警 E-203，无法开始测试',
      fields: {
        '哪台仪器？': 'UD-2300',
        '什么时候发生的？': '10月15日上午 9:10 左右',
        '你做了哪些操作？': '1. 开机自检完成\n2. 进入质控界面\n3. 点击「开始」',
        '出错的例子': '屏幕弹出「E-203 试剂余量不足」；应正常开始质控测试。',
        '出现得频繁吗？': '每次',
      },
      // 序列号/软件版本、猜测等留空 → 补全说明表单里有可改的字段
    },
  })
  expect(created.status(), `建单失败：${await created.text()}`).toBe(201)
  const number: string = (await created.json()).number
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '检验科-张三', valid_days: 30, can_edit_brief: true },
  })
  expect(link.ok(), `创建访客链接失败：${await link.text()}`).toBeTruthy()
  return { number, linkKey: String((await link.json()).url).slice(3) }
}

/** 在维度下拉框里输入并选中一个候选值（按标准值或别名过滤后点击）。 */
async function pickDimension(page: Page, fieldTitle: string, query: string, optionText: string) {
  const combo = page.locator('.fld', { hasText: fieldTitle }).getByRole('combobox')
  await combo.click()
  await combo.fill(query)
  const option = page.getByRole('option', { name: optionText })
  await expect(option).toBeVisible()
  await option.click()
  await expect(combo).toHaveValue(optionText)
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
    ctx.fillStyle = '#d33'
    ctx.font = '30px sans-serif'
    ctx.fillText('E-203 试剂余量不足', 20, 200)
    const blob = await new Promise<Blob | null>((r) => c.toBlob(r, 'image/png')); if (!blob) throw new Error('toBlob 失败')
    return new Uint8Array(await blob.arrayBuffer())
  }, { width, height })
}

/** 往输入框里「粘贴」一个文件（模拟粘贴截图）。 */
async function pasteFile(target: Locator, bytes: Uint8Array, name: string, type: string) {
  await target.evaluate((ta, payload) => {
    const { bytes, name, type } = payload as { bytes: Uint8Array; name: string; type: string }
    const dt = new DataTransfer()
    dt.items.add(new File([new Uint8Array(bytes)], name, { type }))
    ta.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }))
  }, { bytes, name, type })
}

// 1.（手机视口）反馈入口：漏填必填被拦下，看到缺项汇总与字段标红；
//    补全后提交成功，看到编号和专属链接。
test.describe('手机视口', () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test('反馈入口漏填必填被拦下，补全后提交成功并显示编号与专属链接', async ({ page, request }) => {
  const slug = await seedIntake(request)
  await page.goto(`/i/${slug}`)

  await expect(page.getByRole('heading', { name: '部门 Bug 反馈' })).toBeVisible()
  await expect(page.getByText('🔒 只有你和负责处理这张反馈的成员能看到它。')).toBeVisible()
  await expect(page.getByText('只有处理的成员能下载')).toBeVisible()
  await expect(page.getByText('管理员')).toHaveCount(0)

  // 只填标题就提交：缺项汇总 + 各字段标红与提示
  await page.getByLabel('一句话标题').fill('UD-2300 质控时报警 E-203')
  await page.getByRole('button', { name: '提交反馈' }).click()
  const missbox = page.locator('.missbox')
  await expect(missbox).toBeVisible()
  await expect(missbox.getByText('还差 6 项必填')).toBeVisible()
  for (const q of ['哪台仪器？', '什么时候发生的？', '你做了哪些操作？', '出错的例子', '你的署名']) {
    await expect(missbox).toContainText(q)
  }
  const steps = page.locator('.fld', { hasText: '你做了哪些操作？' })
  await expect(steps).toHaveClass(/miss/)
  await expect(steps.locator('.err')).toContainText('从开机或进入某个界面开始')

  // 实时：填好一项后，这一项的红消失（汇总数字随之变化）
  await steps.getByRole('textbox').fill('1. 开机自检完成\n2. 点击「开始」')
  await expect(missbox.getByText('还差 5 项必填')).toBeVisible()
  await expect(steps).not.toHaveClass(/miss/)

  // 补全其余必填：维度下拉（含「不确定」）、时间、出错例子、单选、署名
  await pickDimension(page, '哪台仪器？', 'BH-7300', 'BH-7300')
  await page.locator('.fld', { hasText: '什么时候发生的？' }).getByRole('textbox').fill('今天上午 9 点左右')
  await page.locator('.fld', { hasText: '出错的例子' }).getByRole('textbox').fill('弹出「E-203 试剂余量不足」，试剂是新换的；应正常开始质控测试。')
  await page.locator('.fld', { hasText: '出现得频繁吗？' }).getByRole('radio', { name: '每次' }).check()
  await page.getByLabel('你的署名').fill('检验科 张三')

  await page.getByRole('button', { name: '提交反馈' }).click()

  // 提交成功：编号 + 专属链接 + 收藏提醒
  await expect(page.getByRole('heading', { name: /已提交/ })).toBeVisible({ timeout: 15000 })
  const linkbox = page.locator('.linkbox').first()
  await expect(linkbox).toContainText('/g/')
  const codebox = page.locator('[data-access-code]')
  await expect(codebox).toContainText(/^\d{6}$/)
  await expect(page.getByText('访问码只显示这一次')).toBeVisible()
  await expect(page.getByText('丢失访问码请联系本部门')).toBeVisible()
  await expect(page.getByText('IT 服务台 400-123-4567')).toBeVisible()
  const guestUrl = new URL(await linkbox.textContent() ?? '')
  expect(guestUrl.pathname.startsWith('/g/')).toBeTruthy()

  // 链接真实可用：能读到这张反馈（编号对得上，说明按问句渲染）
  await page.getByRole('link', { name: '打开这条反馈' }).click()
  await expect(page.getByRole('button', { name: '先不填，直接回复 →' })).toBeVisible()
  })
})

// 2. 维度下拉框按别名搜索：输入别名能选中对应的标准值（提交后标签是标准值）。
test('维度下拉框输入别名能选中对应的标准值', async ({ page, request }) => {
  const slug = await seedIntake(request, { alias: true })
  await page.goto(`/i/${slug}`)

  await pickDimension(page, '哪台仪器？', '2300血球仪', 'UD-2300')

  // 把必填补齐后提交，标签里应是标准值（别名解析发生在服务端建单）
  await page.getByLabel('一句话标题').fill('UD-2300 质控时报警 E-203')
  await page.locator('.fld', { hasText: '什么时候发生的？' }).getByRole('textbox').fill('今天上午 9 点左右')
  await page.locator('.fld', { hasText: '你做了哪些操作？' }).getByRole('textbox').fill('点「开始」就报警')
  await page.locator('.fld', { hasText: '出错的例子' }).getByRole('textbox').fill('E-203 报警，应正常开始')
  await page.locator('.fld', { hasText: '出现得频繁吗？' }).getByText('每次').check()
  await page.getByLabel('你的署名').fill('张三')
  await page.getByRole('button', { name: '提交反馈' }).click()

  await expect(page.getByRole('heading', { name: /已提交/ })).toBeVisible({ timeout: 15000 })
  const number = (await page.getByRole('heading', { name: /已提交/ }).textContent())!.match(/\d{8}-\d+/)![0]
  await loginAdmin(request)
  const ticket = await request.get(`/api/v1/tickets/${number}`)
  expect(ticket.ok()).toBeTruthy()
  expect((await ticket.json()).labels).toContain('机型:UD-2300')
})

// 3. 持「可填写说明」链接的访客：第一次打开先看落地页（#66），「开始填写」
//    进入分组填写页（#65）→ 改一个字段（含粘贴图片）→ 提交 → 确认页 → 回到
//    对话，时间线出现修订事件，插图挂在说明里。必填项在建单时已填齐，提交
//    不会被拦。
test('修改说明提交后回到对话，时间线出现修订事件', async ({ page, request }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const seed = await seedEditableTicket(request)
  await openGuestPage(page, request, seed.linkKey)

  await page.getByRole('button', { name: '开始填写' }).click()
  await expect(page.locator('.grp').first()).toContainText('哪台仪器')

  // 改一个字段：序列号和软件版本（原本未填）
  const ver = page.locator('.cf', { hasText: '序列号和软件版本是多少？' })
  await ver.locator('textarea').fill('序列号 7600CS-2023-0412，软件 V3.2.1')

  // 文本字段可以直接粘贴截图：引用单独挂在字段附件列表里
  const actual = page.locator('.cf', { hasText: '出错的例子' })
  const png = await smallPng(page)
  await pasteFile(actual.locator('textarea'), png, '报警截图.png', 'image/png')
  await expect(actual.locator('.staged-file')).toContainText('报警截图.png', { timeout: 15000 })
  await expect(actual.locator('textarea')).not.toHaveValue(/att:/)

  await page.getByRole('button', { name: /^提交给管理员/ }).click()
  await expect(page.locator('.fill-done')).toContainText('已提交给管理员')
  await page.getByRole('button', { name: '查看对话' }).click()

  // 回到对话：说明气泡里出现新值与插图，时间线出现修订事件
  await expect(page.locator('[data-bubble="brief"]')).toContainText('V3.2.1')
  await expect(page.locator('[data-bubble="brief"] img.inline-img')).toBeVisible({ timeout: 15000 })
  const event = page.locator('.event', { hasText: '修订了说明' })
  await expect(event).toBeVisible({ timeout: 15000 })
  await expect(event).toContainText('第 2 版')
})

// 4. 入口提交页的图片与附件：选择后立即上传、刷新后草稿恢复；提交时随字段认领，
//    建单说明和时间线都不再走「补传 + 说明修订」。
test('入口附件立即上传刷新不丢，提交后说明自带附件且无修订事件', async ({ page, request }) => {
  const slug = await seedIntake(request)
  await page.goto(`/i/${slug}`)

  await page.getByLabel('一句话标题').fill('BH-7300 质控时报警 E-203')
  await pickDimension(page, '哪台仪器？', '不确定', '不确定')
  await page.locator('.fld', { hasText: '什么时候发生的？' }).getByRole('textbox').fill('今天上午 9 点左右')
  await page.locator('.fld', { hasText: '你做了哪些操作？' }).getByRole('textbox').fill('点「开始」就报警')
  const actual = page.locator('.fld', { hasText: '出错的例子' })
  await actual.getByRole('textbox').fill('弹出「E-203 试剂余量不足」，应正常开始。截图如下：')
  await page.locator('.fld', { hasText: '出现得频繁吗？' }).getByRole('radio', { name: '每次' }).check()
  await page.getByLabel('你的署名').fill('检验科 张三')

  // 文本字段粘贴截图（暂存，提交后补传）；统一附件区放一个日志文件
  const png = await smallPng(page)
  await pasteFile(actual.getByRole('textbox'), png, '报警截图.png', 'image/png')
  await expect(actual.locator('.staged-file')).toContainText('报警截图.png')
  await page.setInputFiles('.drop input[type=file]', {
    name: '仪器日志.log',
    mimeType: 'text/plain',
    buffer: Buffer.from('2026-10-16 09:10 E-203 reagent low\n'),
  })
  await expect(page.locator('.drop').locator('~ .staged-files .staged-file')).toContainText('仪器日志.log')
  await expect(actual.locator('.staged-file.ready')).toContainText('报警截图.png')
  await expect(page.locator('.staged-file.ready').filter({ hasText: '仪器日志.log' })).toBeVisible()

  // 刷新后：字段文本仍无 att: 引用，两个已上传附件的元数据从草稿恢复。
  await page.reload()
  await expect(actual.getByRole('textbox')).toHaveValue(/应正常开始/)
  await expect(actual.getByRole('textbox')).not.toHaveValue(/att:/)
  await expect(actual.locator('.staged-file.ready')).toContainText('报警截图.png')
  await expect(page.locator('.staged-file.ready').filter({ hasText: '仪器日志.log' })).toBeVisible()

  await page.getByRole('button', { name: '提交反馈' }).click()
  await expect(page.getByRole('heading', { name: /已提交/ })).toBeVisible({ timeout: 20000 })

  // 打开专属链接：说明气泡里有插图，日志列在附件条里
  const guestPath = await page.getByRole('link', { name: '打开这条反馈' }).getAttribute('href')
  const guestKey = new URL(guestPath ?? '', page.url()).pathname.slice(3)
  const guestToken = (await page.evaluate((key) => localStorage.getItem(`tb:guest-device:${key}`), guestKey)) ?? ''
  expect(guestToken).toMatch(/^tbgd_/)
  await page.addInitScript(({ guestKey, guestToken }) => {
    localStorage.setItem(`tb:guest-device:${guestKey}`, guestToken)
  }, { guestKey, guestToken })
  await page.getByRole('link', { name: '打开这条反馈' }).click()
  await page.getByRole('button', { name: '先不填，直接回复 →' }).click()
  const brief = page.locator('[data-bubble="brief"]')
  await expect(brief).toContainText('试剂余量不足')
  await expect(brief.locator('img.inline-img')).toBeVisible()
  await expect(brief.locator('a.att-link, a')).toContainText('仪器日志.log')

  // 「不确定」不打机型标签；创建者是访客（带入口备注）
  const number = (await page.locator('header').first().textContent())!.match(/\d{8}-\d+/)![0]
  await loginAdmin(request)
  const res = await request.get(`/api/v1/tickets/${number}`)
  expect(res.ok()).toBeTruthy()
  const ticket = await res.json()
  expect(ticket.labels).toEqual(['待初筛'])
  expect(ticket.attachments).toHaveLength(2)
  expect(ticket.brief.revision).toBe(1)
  expect(ticket.timeline.some((e: { type: string }) => e.type === 'brief_revised')).toBe(false)
})

// 5. 入口提交页的草稿与自动重试（与对话页同一套规则）：服务中断时提示
//    「服务更新中，内容已保存」；刷新后草稿仍在并自动重试，恢复后提交成功，
//    且只产生一张工单（同一个 Idempotency-Key）。
test('入口提交时服务中断草稿仍在，恢复后自动重试且只建一张工单', async ({ page, request }) => {
  const slug = await seedIntake(request)
  const title = `自动重试专用标题 ${Date.now()}`
  await page.goto(`/i/${slug}`)

  await page.getByLabel('一句话标题').fill(title)
  await pickDimension(page, '哪台仪器？', 'UD', 'UD-2300')
  await page.locator('.fld', { hasText: '什么时候发生的？' }).getByRole('textbox').fill('今天上午 9 点左右')
  await page.locator('.fld', { hasText: '你做了哪些操作？' }).getByRole('textbox').fill('点「开始」就报警')
  await page.locator('.fld', { hasText: '出错的例子' }).getByRole('textbox').fill('E-203 报警，应正常开始')
  await page.locator('.fld', { hasText: '出现得频繁吗？' }).getByRole('radio', { name: '每次' }).check()
  await page.getByLabel('你的署名').fill('张三')

  // 模拟服务不可用：拦下提交请求
  await page.route('**/api/v1/intakes/*/tickets', (route) => route.abort('connectionfailed'))
  await page.getByRole('button', { name: '提交反馈' }).click()
  await expect(page.getByText('服务更新中，内容已保存')).toBeVisible()

  // 刷新后草稿仍在（各字段值原样恢复）
  await page.reload()
  await expect(page.getByLabel('一句话标题')).toHaveValue(title)
  await expect(page.locator('.fld', { hasText: '出错的例子' }).getByRole('textbox')).toHaveValue('E-203 报警，应正常开始')
  await expect(page.getByLabel('你的署名')).toHaveValue('张三')

  // 恢复服务：自动重试成功，进入提交成功页
  await page.unroute('**/api/v1/intakes/*/tickets')
  await expect(page.getByRole('heading', { name: /已提交/ })).toBeVisible({ timeout: 20000 })

  // 重试期间发过多次（同一个 Idempotency-Key），但只能有一张同标题的工单
  await loginAdmin(request)
  const res = await request.get('/api/v1/tickets', { params: { status: 'all', q: title } })
  expect(res.ok()).toBeTruthy()
  expect((await res.json()).total).toBe(1)
})

// 6. 每日提交上限：当天已满 200 笔后，网页提交显示可理解的暂停提示，不进入自动重试。
test('入口达到每日上限后显示今日已暂停', async ({ page, request }) => {
  const slug = await seedIntake(request)
  const fields = {
    '哪台仪器？': 'UD-2300',
    '什么时候发生的？': '今天上午',
    '你做了哪些操作？': '点「开始」后报警',
    '出错的例子': '弹出 E-203；应正常开始。',
    '出现得频繁吗？': '每次',
  }
  for (let i = 0; i < 200; i++) {
    const res = await request.post(`/api/v1/intakes/${slug}/tickets`, {
      data: { title: `每日上限预置 ${i + 1}`, fields, author_name: '赵六' },
      headers: { 'X-Forwarded-For': `10.20.${Math.floor(i / 250)}.${(i % 250) + 1}` },
    })
    expect(res.status(), `第 ${i + 1} 笔预置提交失败：${await res.text()}`).toBe(201)
  }

  await page.goto(`/i/${slug}`)
  await page.getByLabel('一句话标题').fill('这天已经暂停的反馈')
  await pickDimension(page, '哪台仪器？', 'UD', 'UD-2300')
  await page.locator('.fld', { hasText: '什么时候发生的？' }).getByRole('textbox').fill('今天上午')
  await page.locator('.fld', { hasText: '你做了哪些操作？' }).getByRole('textbox').fill('点「开始」后报警')
  await page.locator('.fld', { hasText: '出错的例子' }).getByRole('textbox').fill('弹出 E-203；应正常开始。')
  await page.locator('.fld', { hasText: '出现得频繁吗？' }).getByText('每次').check()
  await page.getByLabel('你的署名').fill('张三')
  await page.getByRole('button', { name: '提交反馈' }).click()

  await expect(page.locator('.notice.err')).toHaveText('入口今日已暂停，请明天再试或联系部门。')
  await expect(page.getByRole('heading', { name: /已提交/ })).toHaveCount(0)
})
