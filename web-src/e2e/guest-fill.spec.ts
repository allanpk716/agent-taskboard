import { test, expect, type APIRequestContext, type Locator, type Page } from '@playwright/test'

// 访客分组填写页（#65）：持「可填写说明」链接的访客第一次打开链接先看到
// 落地页（#66），「开始填写」进入分组清单式填写页；补全过说明后再打开链接
// 直接进对话页，从说明卡片的「✏ 修改说明」进入同一张分组页继续改。对真实
// 服务 + 构建好的前端跑用例，造数据（工单、访客链接、模板）全部通过 HTTP
// API 完成。

const ADMIN = { username: 'admin', password: 'admin-pass-123' }

interface FillSeed {
  number: string
  linkKey: string
}

async function loginAdmin(request: APIRequestContext) {
  const res = await request.post('/api/v1/session', { data: ADMIN })
  expect(res.ok(), '管理员登录失败').toBeTruthy()
}

/** 造一张 Bug 反馈模板工单（分组：哪台仪器 / 发生了什么 / 影响多大，必填 6 项全部留空）
 *  + 一条带「可填写说明」权限的访客链接。 */
async function seedFillTicket(request: APIRequestContext): Promise<FillSeed> {
  await loginAdmin(request)
  const created = await request.post('/api/v1/tickets', {
    data: {
      template: 'bug-report',
      title: 'UD-2300 质控时报警 E-203',
      fields: {},
      labels: ['机型:UD-2300'],
    },
  })
  expect(created.status(), `建单失败：${await created.text()}`).toBe(201)
  const number: string = (await created.json()).number
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '检验科-张三', valid_days: 30, can_edit_brief: true },
  })
  expect(link.status(), `创建访客链接失败：${await link.text()}`).toBe(201)
  return { number, linkKey: String((await link.json()).url).slice(3) }
}

// 1.（手机视口）分组一次只展开一组：组头有序号/组名/副标题/必填进度，
//    「下一组」按钮可用，填写后必填进度条与组完成态（✓）变化。
test.describe('手机视口', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('分组一次展开一组，「下一组」可用，进度条随填写变化，组填完显示 ✓', async ({ page, request }) => {
    const seed = await seedFillTicket(request)
    await page.goto(`/g/${seed.linkKey}`)

    // 第一次打开链接先看到落地页（#66），「开始填写」进入分组填写页
    await page.getByRole('button', { name: '开始填写' }).click()

    const groups = page.locator('.grp')
    await expect(groups).toHaveCount(3)

    // 顶部吸顶：标题 + 必填进度（全部留空：0/6）
    await expect(page.locator('.fill-sticky')).toContainText('UD-2300 质控时报警 E-203')
    await expect(page.locator('.fill-sticky .fill-prog')).toHaveText(/必填 0\/6/)

    // 第一组默认展开：组头有序号、组名、副标题和「必填 0/1」
    const g1 = groups.nth(0)
    await expect(g1.locator('.grp-num')).toHaveText('1')
    await expect(g1.locator('.grp-name')).toHaveText('哪台仪器')
    await expect(g1.locator('.grp-sub')).toHaveText('型号和版本')
    await expect(g1.locator('.grp-req')).toHaveText('必填 0/1')
    await expect(g1.locator('.cf', { hasText: '哪台仪器？' })).toBeVisible()

    // 其余组收起（没有字段内容）；组头仍可见
    const g2 = groups.nth(1)
    await expect(g2.locator('.grp-name')).toHaveText('发生了什么')
    await expect(g2.locator('.cf')).toHaveCount(0)
    await expect(groups.nth(2).locator('.cf')).toHaveCount(0)

    // 填第一组的必填维度字段：组完成 → 组头数字变绿色 ✓，进度条 1/6
    await g1.getByRole('combobox', { name: '哪台仪器？' }).click()
    await g1.getByRole('option', { name: 'UD-2300' }).click()
    await expect(g1.locator('.grp-num')).toHaveText('✓')
    await expect(g1.locator('.grp-num')).toHaveClass(/ok/)
    await expect(g1.locator('.grp-req')).toHaveText('必填 1/1')
    await expect(page.locator('.fill-sticky .fill-prog')).toHaveText(/必填 1\/6/)

    // 组末的「下一组：发生了什么 ›」：展开第二组并收起第一组
    await g1.getByRole('button', { name: '下一组：发生了什么 ›' }).click()
    await expect(g2.locator('.cf', { hasText: '什么时候发生的？' })).toBeVisible()
    await expect(g1.locator('.cf')).toHaveCount(0)

    // 点已展开组的组头可以收起，再点第二组之外的第一组组头：一次仍只展开一组
    await g2.locator('.grp-hd').click()
    await expect(g2.locator('.cf')).toHaveCount(0)
    await g1.locator('.grp-hd').click()
    await expect(g1.locator('.cf', { hasText: '哪台仪器？' })).toBeVisible()
    await expect(g2.locator('.cf')).toHaveCount(0)
  })
})

// 3.（手机视口）「不清楚」能填好必填项；短文本出现追问提示，写长后消失。
test.describe('追问与不清楚', () => {
  test.use({ viewport: { width: 390, height: 844 } })
  test('「不清楚」填好必填项；短文本出现追问提示，写长后消失', async ({ page, request }) => {
  const seed = await seedFillTicket(request)
  await page.goto(`/g/${seed.linkKey}`)
  await page.getByRole('button', { name: '开始填写' }).click()

  // 短文本（序列号和软件版本，选填）：写短出现黄色追问提示，内容取字段示例
  const sn = page.locator('.cf[data-field="序列号和软件版本是多少？"]')
  await sn.locator('textarea').fill('V3.2.1')
  await expect(sn.locator('.nudge')).toBeVisible()
  await expect(sn.locator('.nudge')).toContainText('能再具体一点吗？比如：序列号 7600CS-2023-0412，软件 V3.2.1')

  // 写长（≥ 20 字）后提示消失
  await sn.locator('textarea').fill('序列号 7600CS-2023-0412，软件 V3.2.1，固件也是今天刚升的版本')
  await expect(sn.locator('.nudge')).toHaveCount(0)

  // 「不知道」的必填项：点「不清楚」快捷按钮填好，且不出现追问提示
  const g1 = page.locator('.grp').nth(0)
  await g1.getByRole('combobox', { name: '哪台仪器？' }).click()
  await g1.getByRole('option', { name: '不确定' }).click()
  await g1.getByRole('button', { name: '下一组：发生了什么 ›' }).click()
  const when = page.locator('.cf[data-field="什么时候发生的？"]')
  await when.locator('textarea').fill('早上')
  await expect(when.locator('.nudge')).toBeVisible()
  await when.getByRole('button', { name: '不清楚', exact: true }).click()
  await expect(when.locator('textarea')).toHaveValue('不清楚')
  await expect(when.locator('.nudge')).toHaveCount(0)

  // 必填进度计入「不清楚」：0/6 → 2/6
  await expect(page.locator('.fill-sticky .fill-prog')).toHaveText(/必填 2\/6/)
  })
})

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
    ctx.fillText('SN 7600CS-2023-0412 V3.2.1', 20, 200)
    const blob = await new Promise<Blob | null>((r) => c.toBlob(r, 'image/png')); if (!blob) throw new Error('toBlob 失败')
    return new Uint8Array(await blob.arrayBuffer())
  }, { width, height })
}

// 4.（手机视口）给短字段（序列号和软件版本）上传图片：结果显示在该字段下，
//    引用写进字段值；提交后确认页 → 对话页，说明里能看到这张图。
test.describe('上传与提交', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('短字段上传图片显示在字段下，提交后说明里能看到', async ({ page, request }) => {
    const seed = await seedFillTicket(request)
    await page.goto(`/g/${seed.linkKey}`)
    await page.getByRole('button', { name: '开始填写' }).click()

    const g1 = page.locator('.grp').nth(0)
    await g1.getByRole('combobox', { name: '哪台仪器？' }).click()
    await g1.getByRole('option', { name: '不确定' }).click()

    // 第一组的短文本字段（序列号和软件版本）也有上传入口：上传结果显示在字段下
    const sn = page.locator('.cf[data-field="序列号和软件版本是多少？"]')
    const png = await smallPng(page)
    await sn.locator('input[type=file]').setInputFiles({
      name: '版本铭牌.png',
      mimeType: 'image/png',
      buffer: Buffer.from(png),
    })
    await expect(sn.locator('.staged-file')).toContainText('版本铭牌.png', { timeout: 15000 })
    await expect(sn.locator('textarea')).toHaveValue(/att:/, { timeout: 15000 })

    // 单个文件超过 5 MB：提示改为写 NAS 路径（服务端 413 的 hint）
    await sn.locator('input[type=file]').setInputFiles({
      name: '仪器录像.zip',
      mimeType: 'application/zip',
      buffer: Buffer.alloc(6 * 1024 * 1024, 1),
    })
    await expect(sn.locator('.staged-file.failed')).toContainText('NAS', { timeout: 15000 })

    // 填完其余必填：第二组 4 个必填都用「不清楚」，第三组单选「每次」
    await g1.getByRole('button', { name: '下一组：发生了什么 ›' }).click()
    for (const q of ['什么时候发生的？', '你做了哪些操作？', '实际看到了什么？', '你期望看到什么？']) {
      await page.locator(`.cf[data-field="${q}"]`).getByRole('button', { name: '不清楚', exact: true }).click()
    }
    await page.locator('.grp').nth(2).locator('.grp-hd').click()
    await page.locator('.cf[data-field="出现得频繁吗？"]').getByRole('radio', { name: '每次' }).check()
    await page.getByLabel('署名').fill('检验科 张三')

    await page.getByRole('button', { name: /^提交给管理员/ }).click()

    // 确认页：✓ 已提交给成员 + 收藏链接提醒 → 查看对话
    await expect(page.locator('.fill-done')).toContainText('已提交给管理员')
    await expect(page.getByText(/请收藏这个链接/)).toBeVisible()
    await page.getByRole('button', { name: '查看对话' }).click()

    // 对话页：说明里能看到这张图，时间线有一条访客的说明修订事件
    const brief = page.locator('[data-bubble="brief"]')
    await expect(brief.locator('img.inline-img')).toBeVisible({ timeout: 15000 })
    const event = page.locator('.event', { hasText: '修订了说明' })
    await expect(event).toBeVisible({ timeout: 15000 })
    await expect(event).toContainText('第 2 版')
    await expect(event).toContainText('检验科 张三')
  })

  // 5.（手机视口）必填没填完时提交按钮不能点，按钮下有提示；填完后提交成功。
  test('必填项没填完时提交按钮不能点，填完后能提交', async ({ page, request }) => {
    const seed = await seedFillTicket(request)
    await page.goto(`/g/${seed.linkKey}`)
    await page.getByRole('button', { name: '开始填写' }).click()

    const submit = page.getByRole('button', { name: /^提交给管理员/ })
    await expect(page.locator('.fill-prog')).toHaveText(/必填 0\/6/)
    await expect(submit).toBeDisabled()
    await expect(page.getByText('不知道的必填项写“不清楚”就行')).toBeVisible()

    // 填一项：进度变化，但还没填完，仍然不能点
    await page.getByRole('combobox', { name: '哪台仪器？' }).click()
    await page.getByRole('option', { name: '不确定' }).click()
    await expect(page.locator('.fill-prog')).toHaveText(/必填 1\/6/)
    await expect(submit).toBeDisabled()

    // 全部填完（不清楚 + 单选）→ 能点；提交后进确认页
    await page.getByRole('button', { name: '下一组：发生了什么 ›' }).click()
    for (const q of ['什么时候发生的？', '你做了哪些操作？', '实际看到了什么？', '你期望看到什么？']) {
      await page.locator(`.cf[data-field="${q}"]`).getByRole('button', { name: '不清楚', exact: true }).click()
    }
    await page.locator('.grp').nth(2).locator('.grp-hd').click()
    await page.locator('.cf[data-field="出现得频繁吗？"]').getByRole('radio', { name: '每次' }).check()
    await expect(page.locator('.fill-prog')).toHaveText(/必填都写好了/)
    await expect(submit).toBeEnabled()
    await submit.click()
    await expect(page.locator('.fill-done')).toContainText('已提交给管理员')
  })

  // 6.（手机视口）刷新页面后草稿还在（字段值和署名都保存在本机）。
  test('刷新页面后草稿还在', async ({ page, request }) => {
    const seed = await seedFillTicket(request)
    await page.goto(`/g/${seed.linkKey}`)
    await page.getByRole('button', { name: '开始填写' }).click()

    const sn = page.locator('.cf[data-field="序列号和软件版本是多少？"]')
    await sn.locator('textarea').fill('序列号 7600CS-2023-0412，软件 V3.2.1')
    await page.getByLabel('署名').fill('检验科 张三')

    await page.reload()
    await page.getByRole('button', { name: '开始填写' }).click()
    await expect(page.locator('.cf[data-field="序列号和软件版本是多少？"] textarea')).toHaveValue(
      '序列号 7600CS-2023-0412，软件 V3.2.1',
    )
    await expect(page.getByLabel('署名')).toHaveValue('检验科 张三')
  })
})

/** 造一个不分组的自定义模板 + 一张它的工单（必填 2 项留空）+ 可填写说明链接。 */
async function seedUngroupedTicket(request: APIRequestContext): Promise<FillSeed> {
  await loginAdmin(request)
  const tpl = await request.post('/api/v1/templates', {
    data: {
      key: 'device-note',
      markdown: [
        '---',
        '名称: 设备情况反馈',
        '类型: 反馈',
        '---',
        '## 设备的编号和版本？ {必填}',
        '在设备背面可以看到。',
        '> 示例：BH-7600CS，编号 2023-0412',
        '## 当时是怎么操作的？ {必填}',
        '按顺序写，一步一行。',
        '> 示例：1. 开机自检',
        '> 2. 进入质控界面',
        '> 3. 点击「开始」',
        '## 其他要补充的？',
      ].join('\n'),
    },
  })
  expect(tpl.status(), `建模板失败：${await tpl.text()}`).toBe(201)
  const created = await request.post('/api/v1/tickets', {
    data: {
      template: 'device-note',
      title: 'BH-7600CS 开机自检不过',
      fields: {},
      labels: ['机型:UD-2300'],
    },
  })
  expect(created.status(), `建单失败：${await created.text()}`).toBe(201)
  const number: string = (await created.json()).number
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '门诊-李四', valid_days: 30, can_edit_brief: true },
  })
  expect(link.status(), `创建访客链接失败：${await link.text()}`).toBe(201)
  return { number, linkKey: String((await link.json()).url).slice(3) }
}

// 7.（手机视口）不分组的模板工单显示成一组「填写说明」，多行示例默认折叠
//    （点「看完整示例」展开），可以正常提交。
test.describe('不分组模板', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('不分组的模板合成一组「填写说明」，可以正常提交', async ({ page, request }) => {
    const seed = await seedUngroupedTicket(request)
    await page.goto(`/g/${seed.linkKey}`)
    await page.getByRole('button', { name: '开始填写' }).click()

    // 合成一组：组名「填写说明」，全部字段都在这一组里（默认展开）
    const groups = page.locator('.grp')
    await expect(groups).toHaveCount(1)
    await expect(groups.nth(0).locator('.grp-name')).toHaveText('填写说明')
    await expect(groups.nth(0).locator('.cf:not(.area)')).toHaveCount(3)
    await expect(page.locator('.fill-prog')).toHaveText(/必填 0\/2/)

    // 整张工单的附件区在组末（手机）
    await expect(groups.nth(0).locator('.cf.area')).toContainText('照片、截图和日志')

    // 多行示例默认折叠：点「看完整示例」展开完整内容
    const steps = page.locator('.cf[data-field="当时是怎么操作的？"]')
    await expect(steps.locator('.ex')).toHaveCount(0)
    await steps.getByRole('button', { name: '看完整示例' }).click()
    await expect(steps.locator('.ex')).toContainText('1. 开机自检')
    await expect(steps.locator('.ex')).toContainText('3. 点击「开始」')

    // 单行示例不需要展开，直接显示
    await expect(page.locator('.cf[data-field="设备的编号和版本？"] .ex')).toContainText('BH-7600CS，编号 2023-0412')

    // 填完必填，提交正常
    await page.locator('.cf[data-field="设备的编号和版本？"] textarea').fill('BH-7600CS，编号 2023-0412')
    await steps.getByRole('button', { name: '不清楚', exact: true }).click()
    await page.getByRole('button', { name: /^提交给管理员/ }).click()
    await expect(page.locator('.fill-done')).toContainText('已提交给管理员')
    await page.getByRole('button', { name: '查看对话' }).click()
    await expect(page.locator('.event', { hasText: '修订了说明' })).toContainText('第 2 版', { timeout: 15000 })
  })
})

/** 往输入框里「粘贴」一个文件（模拟粘贴截图）。 */
async function pasteFile(target: Locator, bytes: Uint8Array, name: string, type: string) {
  await target.evaluate((ta, payload) => {
    const { bytes, name, type } = payload as { bytes: Uint8Array; name: string; type: string }
    const dt = new DataTransfer()
    dt.items.add(new File([new Uint8Array(bytes)], name, { type }))
    ta.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }))
  }, { bytes, name, type })
}

// 8.（桌面视口）双栏：左边全部分组展开；右边依次是必填进度、逐字段清单、
//    附件区、署名和提交；点清单某一项会滚到对应字段；文本字段支持粘贴与拖放。
test.describe('桌面视口', () => {
  test.use({ viewport: { width: 1280, height: 800 } })

  test('双栏布局，点右栏清单某一项滚到对应字段', async ({ page, request }) => {
    const seed = await seedFillTicket(request)
    await page.goto(`/g/${seed.linkKey}`)
    await page.getByRole('button', { name: '开始填写' }).click()

    // 双栏：右栏有必填进度、逐字段清单（✓/○）、附件区、署名和提交按钮
    await expect(page.locator('.fill-twocol')).toBeVisible()
    const side = page.locator('.fill-side')
    await expect(side.locator('.fill-prog')).toHaveText(/必填 0\/6/)
    const check = side.locator('.fill-check .check-item')
    await expect(check).toHaveCount(9)
    await expect(check.first()).toContainText('○')
    await expect(side.locator('.cf.area')).toContainText('照片、截图和日志')
    await expect(side.getByLabel('署名')).toBeVisible()
    await expect(side.getByRole('button', { name: /^提交给管理员/ })).toBeVisible()

    // 左边全部分组展开（最后一组的字段直接可见）
    const groups = page.locator('.grp')
    await expect(groups.nth(2).locator('.cf', { hasText: '你的猜测或补充？' })).toBeVisible()

    // 页面滚回顶部后，最后一个字段在视口外；点清单里的一项滚到该字段
    const guess = page.locator('.cf[data-field="你的猜测或补充？"]')
    await page.evaluate(() => window.scrollTo(0, 0))
    await expect(guess).not.toBeInViewport()
    await check.filter({ hasText: '你的猜测或补充？' }).click()
    await expect(guess).toBeInViewport()

    // 填一个字段后：清单该项的 ○ 变成绿色 ✓，进度条 1/6
    await page.locator('.cf[data-field="什么时候发生的？"] textarea').fill('今天上午 9 点左右')
    await expect(side.locator('.fill-prog')).toHaveText(/必填 1\/6/)
    const item = check.filter({ hasText: '什么时候发生的？' })
    await expect(item).toHaveClass(/ok/)
    await expect(item.locator('.mark')).toHaveText('✓')

    // 桌面每个文本字段：粘贴截图、拖放文件、上传按钮都可用；结果显示在字段下
    const sn = page.locator('.cf[data-field="序列号和软件版本是多少？"]')
    const png = await smallPng(page)
    await pasteFile(sn.locator('textarea'), png, '粘贴的截图.png', 'image/png')
    await expect(sn.locator('.staged-file')).toContainText('粘贴的截图.png', { timeout: 15000 })
    await expect(sn.locator('textarea')).toHaveValue(/att:/, { timeout: 15000 })
    await sn.locator('textarea').evaluate(
      (ta, payload) => {
        const dt = new DataTransfer()
        dt.items.add(new File([new Uint8Array(payload.bytes)], '拖入的铭牌.png', { type: 'image/png' }))
        ta.dispatchEvent(new DragEvent('drop', { dataTransfer: dt, bubbles: true, cancelable: true }))
      },
      { bytes: Array.from(png) },
    )
    await expect(sn.locator('.staged-file').filter({ hasText: '拖入的铭牌.png' })).toBeVisible({ timeout: 15000 })

    // 右栏统一附件区也有拖放区（日志这类不属于某个字段的文件）
    await expect(side.locator('.cf.area .drop')).toBeVisible()
  })
})
