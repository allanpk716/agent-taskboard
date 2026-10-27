import { expect, test, type APIRequestContext } from '@playwright/test'
import { login, markDraftIntroSeen, wang, loginWithRequest } from './helpers'

async function issueRequiredAgent(request: APIRequestContext): Promise<string> {
  const member = await wang()
  const loginRes = await loginWithRequest(request, member)
  expect(loginRes.status(), `王工登录失败：${await loginRes.text()}`).toBe(200)
  const issued = await request.post('/api/v1/me/agent-tokens', {
    data: { name: `建单笔记本-${Date.now()}`, draft_mode: 'required' },
  })
  expect(issued.status(), `签发 Agent 失败：${await issued.text()}`).toBe(201)
  await markDraftIntroSeen(request)
  return ((await issued.json()) as { token: string }).token
}

async function createPreviewTemplate(request: APIRequestContext, key: string): Promise<void> {
  const created = await request.post('/api/v1/templates', {
    data: {
      key,
      markdown:
        '---\n名称: 预览检验申请\n类型: 任务\n---\n' +
        '## 要检查什么项目？ {必填}\n写项目名。\n' +
        '## 哪台仪器？ {维度:机型}\n选择仪器。\n' +
        '## 结果多急？ {单选: 今天 | 本周}\n',
    },
  })
  expect(created.status(), `建模板失败：${await created.text()}`).toBe(201)
}

test('Agent 建单草稿经通知确认后发出并展示访客文案', async ({ page, request }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  const who = await wang()
  await login(page, who)
  const token = await issueRequiredAgent(request)
  const markdown = '**请先确认这份草稿**\n- 检查标题\n- 检查指派'
  const drafted = await request.post('/api/v1/tickets', {
    data: {
      title: 'Agent 起草的建单',
      brief_md: markdown,
      labels: ['机型:UD-2300'],
      guest_link: { note: '检验科-张三', can_edit_brief: true },
    },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(drafted.status(), `Agent 建单失败：${await drafted.text()}`).toBe(202)
  const draft = ((await drafted.json()) as { draft: { id: string } }).draft

  await page.getByRole('button', { name: '通知' }).click()
  const row = page.locator('.nrow', { hasText: '写了建单草稿待确认：Agent 起草的建单' })
  await expect(row).toBeVisible()
  await row.click()
  await expect(page).toHaveURL(new RegExp(`/drafts/${draft.id}$`))
  await expect(page.locator('.create-draft-banner')).toContainText('草稿 · 未发布 · 只有你能看到')
  const draftCard = page.getByLabel('建单草稿')
  await expect(draftCard).toContainText('编号在发出时分配')
  const brief = draftCard.locator('> .md')
  await expect(brief.locator('strong')).toHaveText('请先确认这份草稿')
  await expect(brief.locator('li')).toHaveCount(2)
  await expect(brief).not.toContainText('**')

  await draftCard.getByRole('button', { name: '直接修改' }).click()
  await expect(draftCard.getByLabel('说明')).toHaveValue(markdown)
  await draftCard.getByRole('button', { name: '取消', exact: true }).click()
  await expect(brief.locator('strong')).toHaveText('请先确认这份草稿')

  await page.getByLabel('标签').fill('机型:US-1680')
  await page.getByRole('button', { name: '发出', exact: true }).click()
  const sentDialog = page.getByRole('dialog', { name: '建单草稿已发出' })
  await expect(sentDialog).toContainText('经王工修改并确认')
  const shareText = await sentDialog.locator('.linkbox').textContent()
  expect(shareText ?? '').toMatch(/\/g\/tbg_[A-Za-z0-9_-]+/)
  await sentDialog.getByRole('button', { name: '打开新工单' }).click()
  await expect(page).toHaveURL(/\/t\/\d{8}-\d+$/)
  await expect(page.locator('.dhead h1')).toHaveText('Agent 起草的建单')
  await expect(page.locator('.event .draft-confirm')).toHaveText('经王工修改并确认')
})

test('模板建单草稿预览显示字段正文并支持逐字段修改', async ({ page, request }) => {
  const who = await wang()
  const loginRes = await loginWithRequest(request, who)
  expect(loginRes.status(), `王工登录失败：${await loginRes.text()}`).toBe(200)
  // 模板 key 不能复用；服务实例在多次运行间复用，用时间戳保证可重复跑。
  const templateKey = `e2e-preview-check-${Date.now()}`
  await createPreviewTemplate(request, templateKey)
  const token = await issueRequiredAgent(request)

  const drafted = await request.post('/api/v1/tickets', {
    data: {
      template: templateKey,
      title: '模板建单草稿预览',
      fields: {
        '要检查什么项目？': '血常规五分类',
        '哪台仪器？': 'UD-2300',
        '结果多急？': '今天',
      },
    },
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(drafted.status(), `Agent 模板建单失败：${await drafted.text()}`).toBe(202)
  const draft = ((await drafted.json()) as { draft: { id: string } }).draft

  await login(page, who)
  try {
    await page.goto(`/drafts/${draft.id}`)
    const article = page.getByLabel('建单草稿')
    await expect(article).toContainText('要检查什么项目？')
    await expect(article).toContainText('血常规五分类')
    await expect(article).toContainText('哪台仪器？')
    await expect(article).toContainText('结果多急？')
    await expect(article).toContainText('今天')

    await page.getByRole('button', { name: '直接修改' }).click()
    // 模板草稿逐字段编辑，不再给一个改 brief_md 的大文本框。
    await expect(page.getByLabel('说明')).toHaveCount(0)
    await page.getByLabel('要检查什么项目？').fill('尿常规')
    await page.getByLabel('结果多急？').selectOption('本周')
    await page.getByRole('button', { name: '保存草稿' }).click()
    await expect(page.getByText('修改已保存为新一版')).toBeVisible()
    await expect(article).toContainText('尿常规')
    await expect(article).toContainText('本周')
    await expect(article).toContainText('第 2 版')
    await expect(article).not.toContainText('血常规五分类')
  } finally {
    // 草稿列表等用例对 pending 草稿数有精确断言，别把这份留在共享服务里。
    await request.delete(`/api/v1/drafts/${draft.id}`)
  }
})
