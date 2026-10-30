import { test, expect } from '@playwright/test'
import { ADMIN, login, loginWithRequest } from './helpers'

// 管理页系统状态面板（#45）：管理员在「管理」页（成员账号、标签治理之外）
// 看到备份状态（从未成功备份超过 36 小时时标红、未配置备份目标时提示
// 「备份未离开本机」）和前后端版本。

test('管理员在管理页看到备份状态与版本', async ({ page }) => {
  await login(page, ADMIN)
  await page.goto('/admin')

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

test('容器模式下管理页提示升级镜像', async ({ page }) => {
  test.skip(!process.env.E2E_CONTAINER, '需要以容器模式启动 e2e 服务')
  await login(page, ADMIN)
  await page.goto('/admin')

  await expect(page.getByTestId('update-card')).toBeVisible()
  await expect(page.getByTestId('update-error')).toHaveText('容器部署，请升级镜像')
  await expect(page.getByTestId('update-btn')).toBeDisabled()
})

test('管理员在管理页切换成员的初筛人身份', async ({ page }) => {
  await login(page, ADMIN)
  await page.goto('/admin')

  const row = page.locator(`tr[data-member="wang"]`)
  await expect(row).toBeVisible()
  const toggle = row.getByRole('checkbox', { name: '初筛人' })
  const initial = await toggle.isChecked()
  await toggle.click()

  await expect(toggle).toBeChecked({ checked: !initial })
  const body = (await page.evaluate(() => fetch('/api/v1/members').then((r) => r.json()))) as {
    members: { username: string; triager: boolean }[]
  }
  expect(body.members.find((m) => m.username === 'wang')?.triager).toBe(!initial)

  await toggle.click()
  await expect(toggle).toBeChecked({ checked: initial })
})

test('管理员在管理页授予与撤销成员的管理员身份', async ({ page }) => {
  await login(page, ADMIN)
  // 先复位夹具状态（wang 是普通成员、无人是初筛人），让用例可以独立重跑。
  await page.evaluate(async () => {
    const body = (await fetch('/api/v1/members').then((r) => r.json())) as {
      members: { id: number; username: string; triager: boolean; is_admin: boolean }[]
    }
    for (const m of body.members) {
      if (m.triager) {
        await fetch(`/api/v1/members/${m.id}/triager`, { method: 'POST', body: '{"triager":false}' })
      }
      if (m.is_admin && m.username !== 'admin') {
        await fetch(`/api/v1/members/${m.id}/admin`, { method: 'POST', body: '{"admin":false}' })
      }
    }
  })
  await page.goto('/admin')

  // 成员表有表头；表格上方常驻说明两列身份的含义；没有初筛人时提示
  // 由全体在职管理员兜底。
  const panel = page.locator('.mcard', { hasText: '创建、停用和恢复成员账号' })
  await expect(panel.getByRole('columnheader', { name: '姓名' })).toBeVisible()
  await expect(panel.getByRole('columnheader', { name: '状态' })).toBeVisible()
  await expect(panel.getByRole('columnheader', { name: /初筛人/ })).toBeVisible()
  await expect(panel.getByRole('columnheader', { name: '管理员' })).toBeVisible()
  await expect(panel.getByRole('columnheader', { name: '操作' })).toBeVisible()
  await expect(panel).toContainText(
    '初筛人：没人负责的工单（新反馈、未指派的任务）由初筛人接收通知，负责检查信息、打标签、指派给合适的人；Agent 初筛不起作用时由他们兜底。',
  )
  await expect(panel).toContainText('管理员：能管理成员和标签，并能看到全部工单。')
  await expect(panel).toContainText('当前未指定初筛人，由全体在职管理员（管理员）充当。')

  // 授予：先弹确认框，写明授予后的权限；确认后列表勾上。
  const row = page.locator('tr[data-member="wang"]')
  const adminToggle = row.getByRole('checkbox', { name: '管理员' })
  await expect(adminToggle).not.toBeChecked()
  await adminToggle.click()
  const grantDialog = page.locator('.modal')
  await expect(grantDialog).toContainText('授予后，王工 将能看到全部工单，并能管理成员和标签。')
  await grantDialog.getByRole('button', { name: '设为管理员' }).click()
  await expect(adminToggle).toBeChecked()
  await expect(page.locator('#toast')).toContainText('✓ 已设 王工 为管理员')
  const granted = (await page.evaluate(() => fetch('/api/v1/members').then((r) => r.json()))) as {
    members: { username: string; is_admin: boolean }[]
  }
  expect(granted.members.find((m) => m.username === 'wang')?.is_admin).toBe(true)

  // 撤销：不弹框，直接生效。
  await adminToggle.click()
  await expect(page.locator('.modal')).toHaveCount(0)
  await expect(adminToggle).not.toBeChecked()
  await expect(page.locator('#toast')).toContainText('✓ 已撤销 王工 的管理员身份')
  const revoked = (await page.evaluate(() => fetch('/api/v1/members').then((r) => r.json()))) as {
    members: { username: string; is_admin: boolean }[]
  }
  expect(revoked.members.find((m) => m.username === 'wang')?.is_admin).toBe(false)

  // 自己那一行的管理员勾选框禁用，并带提示。
  const selfToggle = page.locator('tr[data-member="admin"]').getByRole('checkbox', { name: '管理员' })
  await expect(selfToggle).toBeChecked()
  await expect(selfToggle).toBeDisabled()
  await expect(selfToggle).toHaveAttribute('title', '不能撤销自己的管理员身份，请另一位管理员来撤销')
})

test('管理员编辑反馈入口的部门联系方式', async ({ page, request }) => {
  const loginRes = await loginWithRequest(request, ADMIN)
  expect(loginRes.ok()).toBeTruthy()
  const cookie = (loginRes.headers()['set-cookie'] ?? '').split(';')[0]
  const slug = `contact-${Date.now() % 1_000_000}`
  const created = await request.post('/api/v1/intakes', {
    data: { slug, name: '部门 Bug 反馈', template: 'bug-report' },
    headers: { Cookie: cookie },
  })
  expect(created.status(), `创建入口失败：${await created.text()}`).toBe(201)

  await login(page, ADMIN)
  await page.goto('/admin')
  await page.getByRole('tab', { name: '反馈入口' }).click()
  const row = page.locator(`tr[data-intake="${slug}"]`)
  await expect(row).toBeVisible()
  await row.getByRole('button', { name: '编辑联系方式' }).click()
  await page.locator('#intake-contact').fill('IT 服务台 400-123-4567')
  await page.getByRole('button', { name: '保存', exact: true }).click()
  await expect(row).toContainText('IT 服务台 400-123-4567')
})

test('管理员停用后可显示并恢复已停用成员', async ({ page, request }) => {
  const loginRes = await loginWithRequest(request, ADMIN)
  expect(loginRes.ok()).toBeTruthy()
  const cookie = (loginRes.headers()['set-cookie'] ?? '').split(';')[0]
  const username = `inactive-${Date.now() % 1_000_000}`
  const created = await request.post('/api/v1/members', {
    data: { username, name: '待恢复工' },
    headers: { Cookie: cookie },
  })
  expect(created.ok(), `创建成员失败：${await created.text()}`).toBeTruthy()

  await login(page, ADMIN)
  await page.goto('/admin')
  const row = page.locator(`tr[data-member="${username}"]`)
  await expect(row).toBeVisible()
  const toggle = page.getByRole('checkbox', { name: /显示已停用/ })
  await expect(toggle).not.toBeChecked()
  await expect(toggle).toHaveAccessibleName(/显示已停用（0）/)

  await row.getByRole('button', { name: '停用' }).click()
  const deactivateDialog = page.locator('.modal')
  await expect(deactivateDialog).toContainText(
    '停用后 TA 将不能登录，名下全部 Agent token 会被撤销（恢复后也不会复活），所有登录会被注销；已有的指派保留',
  )
  await deactivateDialog.getByRole('button', { name: '停用', exact: true }).click()

  await expect(row).toHaveCount(0)
  await expect(toggle).toHaveAccessibleName(/显示已停用（1）/)
  await toggle.click()
  await expect(row).toBeVisible()
  await expect(row).toHaveClass(/inactive/)
  await expect(row.locator('.badge.closed')).toHaveText('已停用')
  await expect(row.getByRole('button', { name: '停用' })).toHaveCount(0)
  await expect(row.getByRole('button', { name: '恢复' })).toHaveCount(1)

  await row.getByRole('button', { name: '恢复' }).click()
  const reactivateDialog = page.locator('.modal')
  await expect(reactivateDialog).toContainText(
    '恢复后 TA 可以用原来的密码登录；Agent token 不会恢复，需要 TA 重新签发',
  )
  await reactivateDialog.getByRole('button', { name: '恢复', exact: true }).click()
  await expect(row.locator('.badge.open')).toBeVisible()
  await expect(row.getByRole('button', { name: '恢复' })).toHaveCount(0)
  await expect(page.locator('tr[data-member="admin"]').getByRole('button', { name: '停用' })).toHaveCount(0)
})

test('管理员重置成员密码后可用新密码登录', async ({ page, request }) => {
  const loginRes = await loginWithRequest(request, ADMIN)
  expect(loginRes.ok()).toBeTruthy()
  const cookie = (loginRes.headers()['set-cookie'] ?? '').split(';')[0]
  const username = `reset-${Date.now() % 1_000_000}`
  const created = await request.post('/api/v1/members', {
    data: { username, name: '待重置工' },
    headers: { Cookie: cookie },
  })
  expect(created.ok(), `创建成员失败：${await created.text()}`).toBeTruthy()

  await login(page, ADMIN)
  await page.goto('/admin')
  const row = page.locator(`tr[data-member="${username}"]`)
  await expect(row).toBeVisible()
  const adminRow = page.locator('tr[data-member="admin"]')
  await expect(adminRow).toBeVisible()
  await expect(adminRow.getByRole('button', { name: '重置密码' })).toHaveCount(0)
  await expect(adminRow.getByRole('button', { name: '停用' })).toHaveCount(0)
  await expect(adminRow).toContainText('改自己的密码请去『我的』')
  await row.getByRole('button', { name: '重置密码' }).click()
  const confirmDialog = page.locator('.modal')
  await expect(confirmDialog).toContainText('将生成新的初始密码，并让 TA 在所有浏览器上退出登录')
  await confirmDialog.getByRole('button', { name: '重置密码', exact: true }).click()

  const passwordDialog = page.locator('.modal')
  await expect(passwordDialog).toContainText('初始密码只显示这一次')
  await expect(passwordDialog).toContainText('请登录后在『我的』里改密码')
  const password = (await passwordDialog.locator('[data-initial-password]').textContent()) ?? ''
  expect(password).not.toBe('')
  await passwordDialog.getByRole('button', { name: '复制' }).click()
  await passwordDialog.getByRole('button', { name: '关闭' }).click()

  await login(page, { username, password })
  await expect(page.getByText('待重置工')).toBeVisible()
})

// 普通成员看不到面板：e2e 环境里 wang 是普通成员但管理页入口对非管理员隐藏、
// 后端 403 admin_required，这个场景由 internal/server 的黑盒测试覆盖
// （Test管理员可检查更新）与 AdminPage 的 is_admin 判断保证。
