import { test, expect } from '@playwright/test'
import { ADMIN, login, wang } from './helpers'

// 管理页「存储」面板（#234）：管理员看到三类空间占用与按关闭月份汇总的
// 可归档工单；N 改动后重新请求。普通成员看不到管理页入口。

test('管理员在管理页看到存储占用与可归档月份', async ({ page }) => {
  await login(page, ADMIN)
  await page.goto('/admin')

  const card = page.getByTestId('storage-card')
  await expect(card).toBeVisible()

  // 三类占用都渲染成人类可读单位；整个 e2e 套件共用一个服务，
  // 附件目录里可能有别的用例上传的文件，不假设具体数值。
  const sizeRe = /^\d+(\.\d+)? (B|KB|MB|GB)$/
  await expect(page.getByTestId('storage-database')).toHaveText(sizeRe)
  await expect(page.getByTestId('storage-database')).not.toHaveText(/^0 B$/)
  await expect(page.getByTestId('storage-attachments')).toHaveText(sizeRe)
  await expect(page.getByTestId('storage-backups')).toHaveText(sizeRe)

  // e2e 环境没有已关闭满 3 个月的工单：空态文案带当前 N。
  await expect(page.getByTestId('storage-empty')).toHaveText('没有已关闭满 3 个月的工单')

  // 改 N → 重新请求，空态文案随之更新。
  const input = page.getByTestId('storage-months-input')
  await input.fill('5')
  await expect(page.getByTestId('storage-empty')).toHaveText('没有已关闭满 5 个月的工单')
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
        backups_bytes: 789012,
        closed_months: n === '1' ? 1 : 3,
        cutoff: '2026-07-16T09:00:00+08:00',
        archivable,
      },
    })
  })
  await login(page, ADMIN)
  await page.goto('/admin')

  await expect(page.getByTestId('storage-months-table')).toContainText('2026-08')
  await expect(page.getByTestId('storage-months-table')).toContainText('2026-05')
  await expect(page.getByTestId('storage-month-2026-05')).toContainText('4 张工单')
  await expect(page.getByTestId('storage-month-2026-05')).toContainText('1 KB')
  await expect(page.getByTestId('storage-month-2026-08')).toContainText('2 KB')

  await page.getByTestId('storage-months-input').fill('1')
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
