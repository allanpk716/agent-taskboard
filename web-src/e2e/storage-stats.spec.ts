import { test, expect, type Page, type Response } from '@playwright/test'
import { ADMIN, login, wang } from './helpers'

// 管理页「存储」面板（#234）：管理员看到三类空间占用与按关闭月份汇总的
// 可归档工单；N 改动后重新请求。普通成员看不到管理页入口。

type StorageStats = {
  closed_months: number
  attachments_backend?: string
  slimmable_ticket_count: number
  slimmable_attachment_bytes: number
  archivable: { month: string; ticket_count: number; attachment_bytes: number }[]
}

function waitForStorageResponse(page: Page, closedMonths: number): Promise<Response> {
  return page.waitForResponse((response) => {
    if (!response.ok()) return false
    const url = new URL(response.url())
    return (
      url.pathname === '/api/v1/admin/storage' && url.searchParams.get('closed_months') === String(closedMonths)
    )
  })
}

async function expectArchivableMonths(page: Page, stats: StorageStats): Promise<void> {
  if (stats.archivable.length === 0) {
    await expect(page.getByTestId('storage-empty')).toHaveText(
      `没有已关闭满 ${stats.closed_months} 个月的工单`,
    )
    return
  }

  await expect(page.getByTestId('storage-months-table')).toBeVisible()
  for (const { month } of stats.archivable) {
    await expect(page.getByTestId(`storage-month-${month}`)).toBeVisible()
  }
  await expect(page.getByTestId('storage-empty')).toHaveCount(0)
}

test('管理员在管理页看到存储占用与可归档月份', async ({ page }) => {
  await login(page, ADMIN)

  // 全量 e2e 会共用同一个服务与数据目录；其他用例可能已经留下可归档工单。
  // 等页面自己发出的请求完成，再按这次响应的实际内容断言，避免依赖执行顺序。
  const defaultResponse = waitForStorageResponse(page, 3)
  await page.goto('/admin')
  const defaultStats = (await (await defaultResponse).json()) as StorageStats

  const card = page.getByTestId('storage-card')
  await expect(card).toBeVisible()

  // 三类占用都渲染成人类可读单位；整个 e2e 套件共用一个服务，
  // 附件目录里可能有别的用例上传的文件，不假设具体数值。
  const sizeRe = /^\d+(\.\d+)? (B|KB|MB|GB)$/
  await expect(page.getByTestId('storage-database')).toHaveText(sizeRe)
  await expect(page.getByTestId('storage-database')).not.toHaveText(/^0 B$/)
  await expect(page.getByTestId('storage-attachments')).toHaveText(sizeRe)
  await expect(page.getByTestId('storage-backups')).toHaveText(sizeRe)
  // 可瘦身汇总按这次响应的实际数值渲染，不依赖其他用例留下的数据。
  const sizePattern = '\\d+(\\.\\d+)? (B|KB|MB|GB)'
  await expect(page.getByTestId('storage-slimmable')).toHaveText(
    new RegExp(`^${defaultStats.slimmable_ticket_count} 张工单，附件共 ${sizePattern}$`),
  )

  expect(defaultStats.closed_months).toBe(3)
  await expectArchivableMonths(page, defaultStats)

  // 改 N → 重新请求，展示内容同样按实际响应断言。
  const input = page.getByTestId('storage-months-input')
  const months5Response = waitForStorageResponse(page, 5)
  await input.fill('5')
  const months5Stats = (await (await months5Response).json()) as StorageStats
  expect(months5Stats.closed_months).toBe(5)
  await expectArchivableMonths(page, months5Stats)
})

test('存储面板按月汇总并随 N 重新请求', async ({ page }) => {
  // 拦截管理 API：默认（N=3）回两个月份；closed_months=1 时只剩最新的一个月。
  await page.route('**/api/v1/admin/storage*', async (route) => {
    const n = new URL(route.request().url()).searchParams.get('closed_months')
    const archivable =
      n === '1'
        ? [{ month: '2026-08', ticket_count: 2, attachment_bytes: 2048 }]
        : [
            { month: '2026-08', ticket_count: 2, attachment_bytes: 2048 },
            { month: '2026-05', ticket_count: 4, attachment_bytes: 1024 },
          ]
    await route.fulfill({
      json: {
        database_bytes: 123456,
        attachments_bytes: 456789,
        attachments_backend: 'oss',
        backups_bytes: 789012,
        slimmable_ticket_count: n === '1' ? 1 : 2,
        slimmable_attachment_bytes: n === '1' ? 1024 : 4096,
        closed_months: n === '1' ? 1 : 3,
        cutoff: '2026-07-16T09:00:00+08:00',
        archivable,
      },
    })
  })
  await login(page, ADMIN)
  await page.goto('/admin')

  await expect(page.getByTestId('storage-card').getByText('附件（OSS）')).toBeVisible()
  await expect(page.getByTestId('storage-slimmable')).toHaveText('2 张工单，附件共 4 KB')
  await expect(page.getByTestId('storage-months-table')).toContainText('2026-08')
  await expect(page.getByTestId('storage-months-table')).toContainText('2026-05')
  await expect(page.getByTestId('storage-month-2026-05')).toContainText('4 张工单')
  await expect(page.getByTestId('storage-month-2026-05')).toContainText('1 KB')
  await expect(page.getByTestId('storage-month-2026-08')).toContainText('2 KB')

  await page.getByTestId('storage-months-input').fill('1')
  await expect(page.getByTestId('storage-slimmable')).toHaveText('1 张工单，附件共 1 KB')
  await expect(page.getByTestId('storage-month-2026-05')).toHaveCount(0)
  await expect(page.getByTestId('storage-month-2026-08')).toBeVisible()
})

test('普通成员看不到管理页与存储区块', async ({ page }) => {
  const member = await wang()
  await login(page, member)
  await page.goto('/')
  await expect(page.getByRole('link', { name: '管理' })).toHaveCount(0)

  await page.goto('/admin')
  await expect(page.getByText('管理功能只对管理员开放')).toBeVisible()
  await expect(page.getByTestId('storage-card')).toHaveCount(0)
})
