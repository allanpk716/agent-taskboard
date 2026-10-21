import { expect, test, type APIRequestContext, type Locator } from '@playwright/test'
import { ADMIN, createTicket, login, openGuestPage } from './helpers'

const UNC_IN_TABLE = '\\\\fake-nas\\share\\交接资料.xlsx'
const BARE_UNC = '\\\\fake-nas\\share\\现场 日志'
const NESTED_BARE_UNC = '\\\\fake-nas\\share\\缩进子列表 资料'
const SECOND_BARE_UNC = '\\\\fake-nas\\share\\第二条路径'
const INLINE_UNC_WITH_SPACE = '\\\\fake-nas\\共享 文件\\交接报告'
const BRIEF = [
  '## 需要跟进的资料',
  '',
  '| 资料 | 路径 |',
  '| --- | --- |',
  `| 交接报告 | \`${UNC_IN_TABLE}\` |`,
  `| 现场日志 | ${BARE_UNC} |`,
  '',
  '```powershell',
  'Get-Content .\\\\fake-nas\\\\share\\\\run.log -Tail 100',
  '```',
  '',
  '处理顺序：',
  '1. 先核对交接报告',
  '   - 注意试剂批次',
  `    - 交接资料：${NESTED_BARE_UNC}`,
  '   - 记录核对人',
  '2. 再联系值班同事',
  '',
  '单个换行也保留。',
  '第二行仍在同一段落。',
  '',
  '[外部说明](https://example.com/gfm) 和 https://example.com/auto 都要打开。',
  '自动链接：<https://example.com/angle>',
  '[危险链接](javascript:alert(1)) 不可点击。',
  '<script>window.markdownXss = true</script>',
  '<img src=x onerror="window.markdownXss = true">',
  '',
  `两个路径：${NESTED_BARE_UNC} 和 ${SECOND_BARE_UNC}`,
  `反引号 UNC：\`${INLINE_UNC_WITH_SPACE}\`；普通行内代码：\`copy \\\\fake-nas\\share now\``,
].join('\n')

async function upload(request: APIRequestContext, number: string) {
  const res = await request.post(`/api/v1/tickets/${number}/attachments`, {
    multipart: {
      file: { name: '交接报告.txt', mimeType: 'text/plain', buffer: Buffer.from('report') },
    },
  })
  expect(res.status(), `上传附件失败：${await res.text()}`).toBe(201)
  return (await res.json()) as { id: string; url: string }
}

async function seed(request: APIRequestContext) {
  const number = await createTicket(request, {
    title: '按完整 GFM 渲染工单正文',
    brief: '占位',
  })
  const att = await upload(request, number)
  const brief = await request.patch(`/api/v1/tickets/${number}`, {
    data: {
      brief_md: `${BRIEF}\n\n[附件资料](att:${att.id})`,
      base_revision: 1,
    },
  })
  expect(brief.status(), `修改说明失败：${await brief.text()}`).toBe(200)
  const reply = await request.post(`/api/v1/tickets/${number}/replies`, {
    data: {
      body_md: [
        '## 回复里的表格',
        '',
        '| 项目 | 状态 |',
        '| --- | --- |',
        '| 检查 | 已完成 |',
        '',
        '```go',
        'fmt.Println("done")',
        '```',
      ].join('\n'),
    },
  })
  expect(reply.status(), `发回复失败：${await reply.text()}`).toBe(201)
  const link = await request.post(`/api/v1/tickets/${number}/guest-links`, {
    data: { note: '检验科-张三', valid_days: 30, can_edit_brief: false },
  })
  expect(link.status(), `创建访客链接失败：${await link.text()}`).toBe(201)
  const linkKey = String((await link.json()).url).slice(3)
  return { number, linkKey, attachmentUrl: att.url }
}

async function assertMarkdown(root: Locator, attachmentUrl: string) {
  await expect(root.getByRole('heading', { level: 2, name: '需要跟进的资料' })).toBeVisible()
  const table = root.locator('table')
  await expect(table).toBeVisible()
  await expect(table.locator('thead')).toContainText('资料')
  await expect(table.locator('thead')).toContainText('路径')
  await expect(table.locator('tbody tr')).toHaveCount(2)
  await expect(root.locator('pre')).toContainText('Get-Content')
  await expect(root.locator('ol > li').filter({ hasText: '先核对交接报告' }).locator('ul > li')).toHaveCount(3)
  const paragraph = root
    .locator('p', { hasText: '单个换行也保留。' })
    .filter({ hasText: '第二行仍在同一段落。' })
  await expect(paragraph).toContainText('第二行仍在同一段落。')
  await expect(paragraph.locator('br')).toHaveCount(1)
  const external = root.getByRole('link', { name: '外部说明' })
  await expect(external).toHaveAttribute('target', '_blank')
  await expect(external).toHaveAttribute('rel', /noopener/)
  await expect(root.getByRole('link', { name: 'https://example.com/auto' })).toHaveAttribute('target', '_blank')
  await expect(root.getByRole('link', { name: 'https://example.com/angle' })).toHaveAttribute('target', '_blank')
  const att = root.getByRole('link', { name: '附件资料' })
  await expect(att).toHaveAttribute('href', attachmentUrl)
  await expect(root.getByRole('link', { name: '危险链接' })).toHaveCount(0)
  await expect(root.locator('script')).toHaveCount(0)
  await expect(root.locator('img')).toHaveCount(0)
  await expect(root.getByText('<script>window.markdownXss = true</script>')).toBeVisible()
  await expect(root.getByText('<img src=x onerror="window.markdownXss = true">')).toBeVisible()
  await expect(root.locator('.unc', { hasText: UNC_IN_TABLE }).getByRole('button', { name: '复制路径' })).toBeVisible()
  await expect(root.locator('.unc', { hasText: BARE_UNC }).getByRole('button', { name: '复制路径' })).toBeVisible()
  await expect(root.locator('.unc', { hasText: NESTED_BARE_UNC }).getByRole('button', { name: '复制路径' })).toHaveCount(2)
  await expect(root.locator('.unc', { hasText: SECOND_BARE_UNC }).getByRole('button', { name: '复制路径' })).toBeVisible()
  await expect(root.locator('.unc', { hasText: INLINE_UNC_WITH_SPACE }).getByRole('button', { name: '复制路径' })).toBeVisible()
  await expect(root.locator('code', { hasText: 'copy \\\\fake-nas\\share now' })).toBeVisible()
  await expect(root.locator('code', { hasText: 'copy \\\\fake-nas\\share now' }).locator('xpath=ancestor::span[@class="unc"][1]')).toHaveCount(0)
}

test('成员端和访客端都按完整 GFM 渲染说明与回复', async ({ page, browser, request }) => {
  const { number, linkKey, attachmentUrl } = await seed(request)

  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  const brief = page.locator('[data-bubble="brief"]')
  await assertMarkdown(brief, attachmentUrl)
  const reply = page.locator('[data-bubble="reply"]').first()
  await expect(reply.getByRole('heading', { level: 2, name: '回复里的表格' })).toBeVisible()
  await expect(reply.locator('tbody tr')).toHaveCount(1)
  await expect(reply.locator('pre')).toContainText('fmt.Println("done")')

  const guest = await browser.newPage({ viewport: { width: 390, height: 844 } })
  await openGuestPage(guest, request, linkKey)
  const guestBrief = guest.locator('[data-bubble="brief"]')
  await assertMarkdown(guestBrief, attachmentUrl)
  const guestReply = guest.locator('[data-bubble="reply"]').first()
  await expect(guestReply.locator('table')).toBeVisible()
  await expect(guestReply.locator('pre')).toBeVisible()
  await guest.close()
})
