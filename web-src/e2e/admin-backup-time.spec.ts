import { test, expect } from '@playwright/test'
import { ADMIN, login } from './helpers'

// 备份时间按北京时间显示（评审 P2-14）：把浏览器时区强制到纽约，拦截备份
// 状态接口回一个已知的北京时间——本地时区会显示成前一天晚上，北京时间
// 渲染应保持「2026-10-16 09:30」。
test.use({ timezoneId: 'America/New_York' })

test('系统面板的备份时间按北京时间显示', async ({ page }) => {
  await page.route('**/api/v1/admin/backups', (route) =>
    route.fulfill({
      json: {
        local_only: true,
        last_success_at: '2026-10-16T09:30:00+08:00',
        last_local_fallback_at: '2026-10-16T10:00:00+08:00',
        target_unreachable: true,
        target_unreachable_error: 'NAS 共享无法连接',
        overdue: false,
        last_failure: null,
        records: [],
      },
    }),
  )
  await login(page, ADMIN)
  await page.goto('/admin')
  await expect(page.getByTestId('backup-last-success')).toHaveText('2026-10-16 09:30')
  await expect(page.getByTestId('backup-target-unreachable')).toHaveText(
    '备份目标不可达（NAS 共享无法连接）。最近一份只在本机：2026-10-16 10:00',
  )
})
