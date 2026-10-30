import { test, expect, type Route } from '@playwright/test'
import { ADMIN, login } from './helpers'

// #235：统计页支持按月整批与逐张选择，预览会丢弃的草稿，再生成并下载
// 归档包。接口数据用路由模拟，保持用例只验证管理页行为，不依赖真实时钟。

const candidates = {
  database_bytes: 123456,
  attachments_bytes: 456789,
  backups_bytes: 789012,
  closed_months: 3,
  cutoff: '2026-07-16T09:00:00+08:00',
  archivable: [
    {
      month: '2026-05',
      ticket_count: 2,
      attachment_bytes: 25,
      tickets: [
        { number: '20260510-001', title: '五月第一张', attachment_bytes: 25 },
        { number: '20260520-001', title: '五月第二张', attachment_bytes: 0 },
      ],
    },
  ],
}

test('管理员选择可归档工单后预览生成并下载归档包', async ({ page }) => {
  const previewRequests: Array<Record<string, unknown>> = []
  const generateRequests: Array<Record<string, unknown>> = []
  const created = {
    name: 'archive-20261030-abcdef.zip',
    ticket_count: 1,
    attachment_bytes: 25,
    generated_at: '2026-10-30T10:00:00+08:00',
    expires_at: '2026-11-06T10:00:00+08:00',
    ticket_numbers: ['20260510-001'],
    download_url: '/api/v1/admin/archive/packages/archive-20261030-abcdef.zip/download',
    operator_name: '管理员',
  }

  await page.route('**/api/v1/admin/storage*', (route) => route.fulfill({ json: candidates }))
  await page.route('**/api/v1/admin/archive/preview', async (route) => {
    previewRequests.push(route.request().postDataJSON() as Record<string, unknown>)
    await route.fulfill({
      json: {
        ticket_count: 1,
        attachment_bytes: 25,
        tickets: [
          { number: '20260510-001', title: '五月第一张', closed_at: '2026-05-10T10:00:00+08:00', attachment_bytes: 25 },
        ],
        drafts: [
          {
            id: 'dft_1',
            ticket_number: '20260510-001',
            kind: 'reply',
            member_name: '陈工',
            agent_name: '待确认稿',
            version: 1,
            body_md: '归档前会丢弃这份草稿',
            attachment_ids: [],
          },
        ],
      },
    })
  })
  const packageRoute = async (route: Route) => {
    const path = new URL(route.request().url()).pathname
    if (path.endsWith('/download')) {
      await route.fulfill({
        status: 200,
        contentType: 'application/zip',
        headers: { 'content-disposition': 'attachment; filename="archive-20261030-abcdef.zip"' },
        body: 'mock archive package',
      })
      return
    }
    if (route.request().method() === 'POST') {
      generateRequests.push(route.request().postDataJSON() as Record<string, unknown>)
      await route.fulfill({ status: 201, json: created })
      return
    }
    await route.fulfill({ json: { count: 1, packages: [created] } })
  }
  await page.route('**/api/v1/admin/archive/packages**', packageRoute)

  await login(page, ADMIN)
  await page.goto('/admin')

  await page.getByTestId('archive-month-2026-05').check()
  await expect(page.getByTestId('archive-ticket-20260510-001')).toBeChecked()
  await expect(page.getByTestId('archive-ticket-20260520-001')).toBeChecked()
  await page.getByTestId('archive-ticket-20260520-001').uncheck()

  await page.getByRole('button', { name: '预览归档' }).click()
  const preview = page.getByTestId('archive-preview')
  await expect(preview).toContainText('将归档 1 张工单，附件 25 B')
  await expect(preview).toContainText('归档前会丢弃这份草稿')
  await expect(preview).toContainText('20260510-001')
  await expect(preview).toContainText('五月第一张')
  await page.getByRole('button', { name: '生成归档包' }).click()

  const pkg = page.getByTestId('archive-package-archive-20261030-abcdef.zip')
  await expect(pkg).toContainText('archive-20261030-abcdef.zip')
  await expect(pkg).toContainText('1 张工单 · 25 B')
  await expect(pkg).toContainText('2026-11-06 10:00')
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.getByTestId('archive-download-archive-20261030-abcdef.zip').click(),
  ])
  expect(download.suggestedFilename()).toBe('archive-20261030-abcdef.zip')

  expect(previewRequests).toEqual([
    { closed_months: 3, ticket_numbers: ['20260510-001'] },
  ])
  expect(generateRequests).toEqual([
    { closed_months: 3, ticket_numbers: ['20260510-001'] },
  ])
})
