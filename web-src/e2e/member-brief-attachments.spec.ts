import { expect, test, type APIRequestContext } from '@playwright/test'
import { ADMIN, createTicket, login, loginAdmin } from './helpers'

async function upload(request: APIRequestContext, number: string, name: string, body: string) {
  const res = await request.post(`/api/v1/tickets/${number}/attachments`, {
    multipart: { file: { name, mimeType: 'text/plain', buffer: Buffer.from(body) } },
  })
  expect(res.status(), `上传 ${name} 失败：${await res.text()}`).toBe(201)
  return (await res.json()) as { id: string }
}

test('建单暂存的附件进入整单附件条，不写进说明', async ({ page }) => {
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '建单' }).click()
  await page.getByRole('button', { name: /^Bug 反馈 report 预设 待初筛$/ }).click()

  await page.getByLabel('标题').fill('建单暂存整单附件')
  const dim = page.getByRole('combobox', { name: '哪台仪器？' })
  await dim.fill('BM-10vet')
  await dim.press('Enter')
  await page.setInputFiles('input[aria-label="选择附件"]', {
    name: '建单日志.log',
    mimeType: 'text/plain',
    buffer: Buffer.from('build log\n'),
  })
  await page.getByRole('button', { name: '创建工单' }).click()
  const confirm = page.locator('.modal')
  await confirm.getByRole('button', { name: '仍然创建' }).click()
  await expect(page.locator('.donet')).toContainText('已创建')

  await page.getByRole('button', { name: '打开工单' }).click()
  const brief = page.locator('[data-bubble="brief"]')
  await expect(brief.getByText('建单日志.log')).toBeVisible()
  await expect(brief.locator('.atts .att')).toHaveCount(1)
  await expect(brief.locator('.atts .att')).toContainText('建单日志.log')
  await expect(brief).not.toContainText('att:')
  await expect(brief).not.toContainText('[建单日志.log]')
})

test('建单时字段内附件留在字段下，不进整张工单的附件', async ({ page }) => {
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '建单' }).click()
  await page.getByRole('button', { name: /^Bug 反馈 report 预设 待初筛$/ }).click()

  await page.getByLabel('标题').fill('字段附件与整单附件各归各位')
  const dim = page.getByRole('combobox', { name: '哪台仪器？' })
  await dim.fill('BM-10vet')
  await dim.press('Enter')
  const field = page.locator('.fld').filter({ hasText: '什么时候发生的？' })
  await field.getByRole('button', { name: /粘贴或上传图片/ }).click()
  await page.locator('input[type="file"]').last().setInputFiles({
    name: '现场截图.png',
    mimeType: 'image/png',
    buffer: Buffer.from('fake image\n'),
  })
  await expect(field.locator('.staged-file')).toContainText('现场截图.png')

  await page.setInputFiles('input[aria-label="选择附件"]', {
    name: '现场日志.log',
    mimeType: 'text/plain',
    buffer: Buffer.from('build log\n'),
  })
  await page.getByRole('button', { name: '创建工单' }).click()
  const confirm = page.locator('.modal')
  await confirm.getByRole('button', { name: '仍然创建' }).click()
  await expect(page.locator('.donet')).toContainText('已创建')
  await page.getByRole('button', { name: '打开工单' }).click()

  const brief = page.locator('[data-bubble="brief"]')
  await expect(brief.locator('.f', { hasText: '什么时候发生的？' }).locator('img[alt="现场截图.png"]')).toBeVisible()
  await expect(brief.locator('.atts .att')).toHaveCount(1)
  await expect(brief.locator('.atts .att')).toContainText('现场日志.log')
  await expect(brief.locator('.atts .att')).not.toContainText('现场截图.png')
})

test('成员修改说明时整单附件可删可加，说明随之修订', async ({ page, request }) => {
  const number = await createTicket(request, { title: '成员修改整单附件' })
  const old = await upload(request, number, '旧日志.log', 'old\n')
  const res = await request.patch(`/api/v1/tickets/${number}`, {
    data: { attachment_ids: [old.id], base_revision: 1 },
  })
  expect(res.status(), `预挂附件失败：${await res.text()}`).toBe(200)

  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await expect(page.locator('[data-bubble="brief"] .atts')).toContainText('旧日志.log')
  await page.getByRole('button', { name: '修改说明' }).click()
  await page.getByRole('button', { name: '删除 旧日志.log' }).click()
  await page.setInputFiles('input[aria-label="添加附件"]', {
    name: '新日志.log',
    mimeType: 'text/plain',
    buffer: Buffer.from('new\n'),
  })
  await expect(page.locator('.brief-edit-dialog')).toContainText('新日志.log')
  await page.getByRole('button', { name: '保存修改' }).click()

  const brief = page.locator('[data-bubble="brief"]')
  await expect(brief.locator('.atts .att')).toHaveCount(1)
  await expect(brief.locator('.atts .att')).toContainText('新日志.log')
  await expect(brief).not.toContainText('旧日志.log')
  await expect(brief).toContainText('第 3 版')
})

test('成员修改模板说明时按字段键保存，其余字段保持不变', async ({ page, request }) => {
  await loginAdmin(request)
  const created = await request.post('/api/v1/tickets', {
    data: {
      title: '成员修改模板说明',
      template: 'bug-report',
      fields: {
        '哪台仪器？': 'UD-2300',
        '什么时候发生的？': '今天上午 9 点左右',
        '你的猜测或补充？': '可能是试剂问题',
      },
    },
  })
  expect(created.status(), `建模板工单失败：${await created.text()}`).toBe(201)
  const number = (await created.json()).number

  await login(page, ADMIN)
  await page.goto(`/t/${number}`)
  await page.getByRole('button', { name: '修改说明' }).click()
  const dim = page.getByRole('combobox', { name: '哪台仪器？' })
  await dim.fill('US-1680')
  await dim.press('Enter')
  await page.getByLabel('什么时候发生的？').fill('昨天下午 3 点左右')
  await page.getByRole('button', { name: '保存修改' }).click()
  await page.reload()

  const brief = page.locator('[data-bubble="brief"]')
  await expect(brief.locator('.f', { hasText: '哪台仪器？' }).locator('.v')).toHaveText('US-1680')
  await expect(brief.locator('.f', { hasText: '什么时候发生的？' }).locator('.v')).toHaveText('昨天下午 3 点左右')
  await expect(brief.locator('.f', { hasText: '你的猜测或补充？' }).locator('.v')).toHaveText('可能是试剂问题')
  await expect(brief).toContainText('第 2 版')
})

test('建单不带暂存附件时说明仍保持第一版', async ({ page }) => {
  await login(page, ADMIN)
  await page.locator('.b-tabs').getByRole('link', { name: '建单' }).click()
  await page.locator('.tplpick', { hasText: '自由格式' }).click()
  await page.getByLabel('标题').fill('不带附件建单')
  await page.getByLabel('说明').fill('说明保持原样。')
  await page.getByLabel('标签输入').fill('机型:UD-2300')
  await page.getByRole('button', { name: '添加', exact: true }).click()
  await page.getByRole('button', { name: '创建工单' }).click()
  await expect(page.locator('.donet')).toContainText('已创建')

  await page.getByRole('button', { name: '打开工单' }).click()
  const brief = page.locator('[data-bubble="brief"]')
  await expect(brief).toContainText('说明保持原样。')
  await expect(brief).toContainText('第 1 版')
  await expect(brief.locator('.atts')).toHaveCount(0)
})
