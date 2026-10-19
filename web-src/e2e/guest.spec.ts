import { test, expect, type APIRequestContext, type Page } from '@playwright/test'

// 访客网页（#38）：工单对话页。对真实服务 + 构建好的前端跑用例，
// 造数据（工单、回复、访客链接）全部通过 HTTP API 完成。

const ADMIN = { username: 'admin', password: 'admin-pass-123' }

interface Seed {
  number: string
  linkKey: string
  linkId: number
}

async function loginAdmin(request: APIRequestContext) {
  const res = await request.post('/api/v1/session', { data: ADMIN })
  expect(res.ok(), '管理员登录失败').toBeTruthy()
}

// 造一张 Bug 反馈模板工单（部分字段未填）+ 一条访客链接；
// 可选：成员 Agent 回复一条（对话里出现带机器人角标的气泡）、关闭工单。
async function seedGuestTicket(
  request: APIRequestContext,
  opts: { agentReply?: boolean; close?: boolean } = {},
): Promise<Seed> {
  await loginAdmin(request)
  const created = await request.post('/api/v1/tickets', {
    data: {
      template: 'bug-report',
      title: 'UD-2300 质控时报警 E-203，无法开始测试',
      fields: {
        // 「序列号和软件版本是多少？」「正常的例子」留空 → 说明气泡显示（未填写）
        '哪台仪器？': 'UD-2300',
        '什么时候发生的？': '10月15日上午 9:10 左右',
        '你做了哪些操作？': '1. 开机自检完成\n2. 进入质控界面\n3. 点击「开始」',
        '出错的例子': '屏幕弹出「E-203 试剂余量不足」，但试剂是早上刚换的新瓶；本应正常开始测试。',
        '出现得频繁吗？': '每次',
      },
    },
  })
  expect(created.status(), `建单失败：${await created.text()}`).toBe(201)
  const number: string = (await created.json()).number

  if (opts.agentReply) {
    const tokenRes = await request.post('/api/v1/me/agent-tokens', { data: { name: '初筛' } })
    expect(tokenRes.ok()).toBeTruthy()
    const token: string = (await tokenRes.json()).token
    const reply = await request.post(`/api/v1/tickets/${number}/replies`, {
      data: { body_md: '感谢反馈，已转给仪器组。请补充软件版本：在「设置 → 关于本机」里，例如 `V3.2.1`。' },
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(reply.status(), `成员 Agent 回复失败：${await reply.text()}`).toBe(201)
  }
  if (opts.close) {
    const closed = await request.post(`/api/v1/tickets/${number}/close`, { data: { reason: 'done' } })
    expect(closed.ok(), `关闭失败：${await closed.text()}`).toBeTruthy()
  }

  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '检验科-张三', valid_days: 30, can_edit_brief: false },
  })
  expect(link.status(), `创建访客链接失败：${await link.text()}`).toBe(201)
  const linkBody = await link.json()
  return { number, linkId: linkBody.id as number, linkKey: String(linkBody.url).slice(3) /* 去掉 /g/ */ }
}

// 在页面里画一张大图并编码成 JPEG，再在 SOI 后插入一段带 EXIF 的 APP1，
// 得到「带 EXIF 的大 JPEG」（用于验证前端上传前压缩、去 EXIF）。
function jpegWithExif(page: Page, width = 3000, height = 2000): Promise<Uint8Array> {
  return page.evaluate(async ({ width, height }) => {
    const c = document.createElement('canvas')
    c.width = width
    c.height = height
    const ctx = c.getContext('2d')!
    const g = ctx.createLinearGradient(0, 0, width, height)
    g.addColorStop(0, '#d0e7ff')
    g.addColorStop(1, '#ffd9d0')
    ctx.fillStyle = g
    ctx.fillRect(0, 0, width, height)
    ctx.fillStyle = '#333'
    ctx.font = '48px sans-serif'
    for (let i = 0; i < 24; i++) ctx.fillText(`E-203 报警截图 ${i}`, 40 + (i % 4) * 700, 120 + Math.floor(i / 4) * 300)
    const blob = await new Promise<Blob | null>((r) => c.toBlob(r, "image/jpeg", 0.9)); if (!blob) throw new Error("toBlob 失败")
    const bytes = new Uint8Array(await blob.arrayBuffer())
    // APP1 = FFE1 + 长度 + "Exif\0\0" + TIFF 头（0 个目录项）
    const exif = new Uint8Array([0x45, 0x78, 0x69, 0x66, 0x00, 0x00, 0x4d, 0x4d, 0x00, 0x2a, 0x00, 0x00, 0x00, 0x08, 0x00, 0x00, 0x00, 0x00])
    const seg = new Uint8Array(4 + exif.length)
    seg[0] = 0xff
    seg[1] = 0xe1
    seg[2] = (exif.length + 2) >> 8
    seg[3] = (exif.length + 2) & 0xff
    seg.set(exif, 4)
    const out = new Uint8Array(bytes.length + seg.length)
    out.set(bytes.subarray(0, 2), 0)
    out.set(seg, 2)
    out.set(bytes.subarray(2), 2 + seg.length)
    return out
  }, { width, height })
}

function smallPng(page: Page, width = 800, height = 600): Promise<Uint8Array> {
  return page.evaluate(async ({ width, height }) => {
    const c = document.createElement('canvas')
    c.width = width
    c.height = height
    const ctx = c.getContext('2d')!
    ctx.fillStyle = '#e8f4e8'
    ctx.fillRect(0, 0, width, height)
    ctx.strokeStyle = '#2a7'
    for (let i = 0; i < 30; i++) {
      ctx.beginPath()
      ctx.arc(100 + i * 24, 300 + Math.sin(i) * 120, 40, 0, Math.PI * 2)
      ctx.stroke()
    }
    const blob = await new Promise<Blob | null>((r) => c.toBlob(r, "image/png")); if (!blob) throw new Error("toBlob 失败")
    return new Uint8Array(await blob.arrayBuffer())
  }, { width, height })
}

// 往输入框里「粘贴」一个文件（模拟粘贴截图）。
async function pasteFile(page: Page, bytes: Uint8Array, name: string, type: string) {
  await page.locator('textarea').first().evaluate((ta, payload) => {
    const { bytes, name, type } = payload as { bytes: Uint8Array; name: string; type: string }
    const dt = new DataTransfer()
    dt.items.add(new File([new Uint8Array(bytes)], name, { type }))
    ta.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }))
  }, { bytes, name, type })
}

// 在字节数组里找一个字节串的首次出现（没有就 -1）。
function indexOfBytes(hay: Uint8Array, needle: Uint8Array): number {
  outer: for (let i = 0; i + needle.length <= hay.length; i++) {
    for (let j = 0; j < needle.length; j++) {
      if (hay[i + j] !== needle[j]) continue outer
    }
    return i
  }
  return -1
}

function bytesEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false
  return true
}

const EXIF_MAGIC = new TextEncoder().encode('Exif')

// 解析 JPEG 的 SOF 段拿宽高。
// 解析 JPEG 的 SOF 段拿宽高。
function jpegSize(buf: Uint8Array): { w: number; h: number } {
  expect(buf[0]).toBe(0xff)
  expect(buf[1]).toBe(0xd8)
  let i = 2
  while (i + 9 < buf.length) {
    if (buf[i] !== 0xff) throw new Error('不是合法的 JPEG')
    const marker = buf[i + 1]
    if (marker >= 0xc0 && marker <= 0xcf && marker !== 0xc4 && marker !== 0xc8 && marker !== 0xcc) {
      return { h: (buf[i + 5] << 8) | buf[i + 6], w: (buf[i + 7] << 8) | buf[i + 8] }
    }
    const len = (buf[i + 2] << 8) | buf[i + 3]
    i += 2 + len
  }
  throw new Error('没找到 SOF 段')
}

// 1. 打开访客链接：状态徽标 + 说明气泡（问句/回答/未填写），发一条带粘贴图片的回复。
//    手机尺寸视口。
test.use({ viewport: { width: 390, height: 844 } })
test('打开访客链接看到徽标与说明气泡，回复并附带粘贴的图片', async ({ page, request }) => {
  const seed = await seedGuestTicket(request, { agentReply: true })
  await page.goto(`/g/${seed.linkKey}`)

  // 成员 Agent 回复过 → 轮到访客
  await expect(page.locator('.turn-badge')).toHaveText('● 现在轮到你回复')

  // 头部：顶栏（编号）、标题卡片（类型）、链接备注
  await expect(page.locator('header')).toContainText(seed.number)
  await expect(page.locator('.head > .meta')).toContainText('反馈')
  await expect(page.getByRole('heading', { name: 'UD-2300 质控时报警 E-203，无法开始测试' })).toBeVisible()
  await expect(page.locator('.link-meta')).toContainText('检验科-张三')

  // 说明气泡按字段显示问句与回答，未填写的显示（未填写）
  const brief = page.locator('[data-bubble="brief"]')
  await expect(brief).toBeVisible()
  await expect(brief.getByText('哪台仪器？')).toBeVisible()
  await expect(brief.getByText('UD-2300')).toBeVisible()
  await expect(brief.getByText('（未填写）').first()).toBeVisible()

  // 成员 Agent 的回复气泡：署名 + 机器人角标
  await expect(page.getByText('管理员 的 Agent · 初筛')).toBeVisible()
  await expect(page.locator('[data-bubble="reply"] .av .robot-badge').first()).toBeVisible()

  // 署名记在本机：刷新后仍在
  await page.getByLabel('署名').fill('张三')
  await page.reload()
  await expect(page.getByLabel('署名')).toHaveValue('张三')
  await expect(page.locator('.turn-badge')).toHaveText('● 现在轮到你回复')

  // 这条链接没与「可填写说明」权限：说明卡片上不显示 ✏ 修改说明（#39）
  await expect(page.getByRole('button', { name: '✏ 修改说明' })).toHaveCount(0)

  // 写回复 + 粘贴截图（上传完成后输入框里出现 att: 引用）
  await page.getByPlaceholder(/写回复/).fill('软件版本 V3.2.1，报警照片如下。')
  const jpeg = await jpegWithExif(page)
  await pasteFile(page, jpeg, '报警截图.jpg', 'image/jpeg')
  await expect(page.locator('textarea')).toHaveValue(/att:/, { timeout: 15000 })
  await page.getByRole('button', { name: '发送' }).click()

  // 回复出现在对话里：署名、正文、插图
  const myReply = page.locator('[data-bubble="reply"]').last()
  await expect(myReply.getByText('软件版本 V3.2.1，报警照片如下。')).toBeVisible()
  await expect(myReply.getByText('张三')).toBeVisible()
  const img = myReply.locator('img.inline-img')
  await expect(img).toBeVisible()

  // 点击查看大图
  await img.click()
  await expect(page.locator('.lightbox img')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.locator('.lightbox')).toBeHidden()
})

// 2. 工单关闭后只读；链接撤销/过期后整页提示。
test('工单关闭后只读；链接撤销或过期后显示整页提示', async ({ page, request }) => {
  const seed = await seedGuestTicket(request, { close: true })
  await page.goto(`/g/${seed.linkKey}`)

  await expect(page.locator('.turn-badge')).toHaveText('已关闭 · 完成')
  await expect(page.getByText(/工单已关闭，只能查看/)).toBeVisible()
  await expect(page.locator('textarea')).toHaveCount(0)

  // 撤销：整页提示原因（被撤销）
  const del = await request.delete(`/api/v1/tickets/${seed.number}/guest-links/${seed.linkId}`)
  expect(del.ok()).toBeTruthy()
  await page.reload()
  await expect(page.getByText('这条链接已失效')).toBeVisible()
  await expect(page.getByText(/撤销/)).toBeVisible()

  // 过期：整页提示原因（已到期）
  const short = await request.post(`/api/v1/tickets/${seed.number}/guest-links`, {
    data: { note: '短期', expires_at: new Date(Date.now() + 1500).toISOString() },
  })
  expect(short.ok(), await short.text()).toBeTruthy()
  const shortKey = String((await short.json()).url).slice(3)
  await page.waitForTimeout(2500)
  await page.goto(`/g/${shortKey}`)
  await expect(page.getByText('这条链接已失效')).toBeVisible()
  await expect(page.getByText(/到期/)).toBeVisible()
})

// 3. 服务中断时发送：草稿保存在本机；恢复后自动重试成功且只产生一条回复。
test('停掉服务后发送草稿仍在，恢复后自动重试成功且只产生一条回复', async ({ page, request }) => {
  const seed = await seedGuestTicket(request, {})
  await page.goto(`/g/${seed.linkKey}`)

  const body = '照片放在共享盘，请查收。'
  const unc = '\\\\nas01\\检验科\\报警截图.jpg'
  await page.getByPlaceholder(/写回复/).fill(`${body} ${unc}`)
  await page.getByLabel('署名').fill('张三')

  // 模拟服务不可用：拦下回复请求
  await page.route('**/api/v1/tickets/*/replies', (route) => route.abort('connectionfailed'))
  await page.getByRole('button', { name: '发送' }).click()
  await expect(page.getByText('服务更新中，内容已保存')).toBeVisible()

  // 刷新后草稿仍在（正文和署名）
  await page.reload()
  await expect(page.locator('textarea')).toHaveValue(`${body} ${unc}`)
  await expect(page.getByLabel('署名')).toHaveValue('张三')

  // 恢复服务：自动重试成功，回复出现；UNC 路径显示为带「复制路径」按钮的文本块
  await page.unroute('**/api/v1/tickets/*/replies')
  const myReply = page.locator('[data-bubble="reply"]').last()
  await expect(myReply.getByText(body)).toBeVisible({ timeout: 20000 })
  await expect(myReply.getByText(unc)).toBeVisible()
  await expect(myReply.getByRole('button', { name: '复制路径' })).toBeVisible()

  // 自动重试期间可能发了多次（同一个 Idempotency-Key），但只能有一条回复
  const res = await request.get(`/api/v1/tickets/${seed.number}`, {
    headers: { Authorization: `Bearer ${seed.linkKey}` },
  })
  expect(res.ok()).toBeTruthy()
  const ticket = await res.json()
  const same = ticket.timeline.filter((e: any) => e.type === 'reply' && e.body_md === `${body} ${unc}`)
  expect(same).toHaveLength(1)
})

// 4. 上传前压缩：长边 ≤2048、JPEG、无 EXIF；不到 1MB 的 PNG 原样保留。
test('粘贴的图片已压缩且不含 EXIF，小 PNG 保持原样', async ({ page, request }) => {
  const seed = await seedGuestTicket(request, {})
  await page.goto(`/g/${seed.linkKey}`)

  // 3000×2000、带 EXIF 的 JPEG → 压缩后 ≤2048、无 EXIF
  const jpeg = await jpegWithExif(page)
  await page.getByPlaceholder(/写回复/).fill('报警照片如下')
  await pasteFile(page, jpeg, '报警截图.jpg', 'image/jpeg')
  await expect(page.locator('textarea')).toHaveValue(/att:/, { timeout: 15000 })
  await page.getByRole('button', { name: '发送' }).click()
  await expect(page.locator('[data-bubble="reply"]').last().getByText('报警照片如下')).toBeVisible({ timeout: 15000 })

  const src1 = await page.locator('[data-bubble="reply"]').last().locator('img.inline-img').getAttribute('src')
  const res1 = await request.get(src1!)
  const buf1 = new Uint8Array(await (await res1.body()).buffer)
  expect(buf1[0]).toBe(0xff)
  expect(buf1[1]).toBe(0xd8) // JPEG
  expect(indexOfBytes(buf1.subarray(0, 8192), EXIF_MAGIC)).toBe(-1) // 无 EXIF
  const { w, h } = jpegSize(buf1)
  expect(Math.max(w, h)).toBeLessThanOrEqual(2048)
  expect(Math.max(w, h)).toBeGreaterThan(0)

  // 不到 1MB 的 PNG：原样上传（字节不变）
  const png = await smallPng(page)
  await page.getByPlaceholder(/写回复/).fill('示意图如下')
  await pasteFile(page, png, '示意图.png', 'image/png')
  await expect(page.locator('textarea')).toHaveValue(/att:/, { timeout: 15000 })
  await page.getByRole('button', { name: '发送' }).click()
  await expect(page.locator('[data-bubble="reply"]').last().getByText('示意图如下')).toBeVisible({ timeout: 15000 })

  const src2 = await page.locator('[data-bubble="reply"]').last().locator('img.inline-img').getAttribute('src')
  const res2 = await request.get(src2!)
  const buf2 = new Uint8Array(await (await res2.body()).buffer)
  expect(buf2[1]).toBe(0x50) // P（PNG 魔数 89 50 4E 47）
  expect(bytesEqual(buf2, png)).toBe(true) // 字节原样
})

// 5. 附件超限（413）：提示改用 NAS 路径，不产生回复。
test('附件超过单个文件上限时提示改用 NAS 路径', async ({ page, request }) => {
  const seed = await seedGuestTicket(request, {})
  await page.goto(`/g/${seed.linkKey}`)

  await page.getByPlaceholder(/写回复/).fill('录像放在 NAS 上')
  await page.setInputFiles('#guest-file-input', {
    name: '录像.zip',
    mimeType: 'application/zip',
    buffer: Buffer.alloc(6 * 1024 * 1024, 1), // 超过 5MB 的单个文件上限
  })
  await expect(page.locator('.toobig')).toContainText('录像.zip', { timeout: 10000 })
  await expect(page.locator('.toobig')).toContainText('NAS')

  // 没上传成附件：发送不携带它，也不应把它写进正文
  await expect(page.locator('textarea')).toHaveValue('录像放在 NAS 上')
})
