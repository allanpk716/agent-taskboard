import { test, expect } from '@playwright/test'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import {
  ADMIN,
  ageClosedTicket,
  createTicket,
  login,
  loginAdmin,
  readZipTextEntry,
} from './helpers'

// #235：真实后端 + 真实构建前端。关闭时间用测试专用直连 SQLite 快路径改早，
// 让服务真实执行选择校验、生成、暂存与下载。
test('管理员选择可归档工单后预览生成并下载归档包', async ({ page, request }) => {
  await loginAdmin(request)
  const title = `e2e 归档包 ${Date.now()}`
  const number = await createTicket(request, { title })
  const closed = await request.post(`/api/v1/tickets/${number}/close`, { data: { reason: 'done' } })
  expect(closed.status(), `关闭工单失败：${await closed.text()}`).toBe(200)
  ageClosedTicket(number, new Date(Date.now() - 120 * 24 * 3600 * 1000).toISOString())

  const storage = await request.get('/api/v1/admin/storage?closed_months=3')
  expect(storage.ok(), `读取存储统计失败：${await storage.text()}`).toBeTruthy()
  const candidates = (await storage.json()) as {
    archivable: Array<{ tickets: Array<{ number: string }> }>
  }
  expect(
    candidates.archivable.some((month) => month.tickets?.some((ticket) => ticket.number === number)),
    '造数后存储统计应列出可归档工单',
  ).toBeTruthy()

  await login(page, ADMIN)
  await page.goto('/admin')
  await page.getByTestId(`archive-ticket-${number}`).check()
  await page.getByRole('button', { name: '预览归档' }).click()
  const preview = page.getByTestId('archive-preview')
  await expect(preview).toContainText(`将归档 1 张工单`)
  await expect(preview).toContainText(number)
  await expect(preview).toContainText(title)
  await expect(preview).toContainText('这些工单上没有未发出的草稿。')

  await page.getByRole('button', { name: '生成归档包' }).click()
  let created: { name: string; ticket_numbers: string[] } | undefined
  for (let i = 0; i < 40 && !created; i++) {
    const listRes = await request.get('/api/v1/admin/archive/packages')
    expect(listRes.ok(), `读取归档包列表失败：${await listRes.text()}`).toBeTruthy()
    const list = (await listRes.json()) as {
      packages: Array<{ name: string; ticket_numbers: string[] }>
    }
    created = list.packages.find((pkg) => pkg.ticket_numbers.includes(number))
    if (!created) await new Promise((resolve) => setTimeout(resolve, 250))
  }
  expect(created, '生成后归档包列表应包含新包').toBeTruthy()

  const pkg = page.getByTestId(`archive-package-${created!.name}`)
  await expect(pkg).toContainText(created!.name)
  await expect(pkg).toContainText('1 张工单')
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.getByTestId(`archive-download-${created!.name}`).click(),
  ])
  expect(download.suggestedFilename()).toBe(created!.name)
  const zipPath = join(tmpdir(), `tb-e2e-${created!.name}`)
  await download.saveAs(zipPath)
  const manifest = JSON.parse(readZipTextEntry(zipPath, 'manifest.json')) as {
    format_version: number
    tickets: Array<{ number: string }>
  }
  expect(manifest.format_version).toBe(1)
  expect(manifest.tickets.map((ticket) => ticket.number)).toContain(number)
})
