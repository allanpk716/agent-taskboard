import { test, expect } from '@playwright/test'
import { ADMIN, login } from './helpers'

// 管理页系统状态面板（#45）：管理员在「我的」标签下看到备份状态（从未成功
// 备份超过 36 小时时标红、未配置备份目标时提示「备份未离开本机」）和前后端
// 版本。普通成员看不到该面板。

test('管理员在管理面板看到备份状态与版本', async ({ page }) => {
  await login(page, ADMIN)
  await page.goto('/me')

  const backupCard = page.getByTestId('backup-card')
  await expect(backupCard).toBeVisible()

  // e2e 环境没有配置备份目标，也从未备份：从未成功 + 标红 + 未离开本机。
  await expect(page.getByTestId('backup-last-success')).toHaveText('从未成功备份')
  await expect(page.getByTestId('backup-overdue')).toBeVisible()
  await expect(page.getByTestId('backup-local-only')).toContainText('备份未离开本机')

  // 版本卡片：没有配置更新源，检查失败但仍展示当前版本。
  const updateCard = page.getByTestId('update-card')
  await expect(updateCard).toBeVisible()
  await expect(page.getByTestId('update-error')).toContainText('更新源')
  await expect(page.getByTestId('server-version')).toContainText('未检查更新')
  await expect(page.getByTestId('web-version')).toBeVisible()
  await expect(page.getByTestId('update-btn')).toBeDisabled()
})

// 普通成员看不到面板：e2e 环境的两个账号都是 create-admin 造的（都是管理员），
// 这个场景由 internal/server 的黑盒测试覆盖（非管理员 403 admin_required）
// 和 MePage 只在 me.is_admin 时渲染 SystemPanel 保证。
