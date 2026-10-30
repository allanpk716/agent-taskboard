import { test, expect } from '@playwright/test'
import { readFile } from 'node:fs/promises'
import { ADMIN, ageClosedTicket, createTicket, login } from './helpers'

// #236/#237：管理员确认已保存后移除归档包；旧工单地址显示墓碑页，随后导入已保存的包恢复。
test('管理员归档移除后导入恢复工单', async ({ page, request }) => {
  const title = `e2e 归档移除 ${Date.now()}`
  const number = await createTicket(request, { title })
  const closed = await request.post(`/api/v1/tickets/${number}/close`, { data: { reason: 'done' } })
  expect(closed.status(), `关闭工单失败：${await closed.text()}`).toBe(200)
  ageClosedTicket(number, new Date(Date.now() - 120 * 24 * 3600 * 1000).toISOString())

  await login(page, ADMIN)
  await page.goto('/admin')
  await expect(page.getByTestId('storage-months-table')).toContainText(number)
  await page.getByTestId(`archive-ticket-${number}`).check()
  await page.getByRole('button', { name: '预览归档' }).click()
  await expect(page.getByTestId('archive-preview')).toContainText(number)
  await page.getByRole('button', { name: '生成归档包' }).click()
  const packageRow = page.locator('[data-testid^="archive-package-"]').first()
  await expect(packageRow).toContainText(number)
  const packageTestId = await packageRow.getAttribute('data-testid')
  const created = packageTestId?.replace(/^archive-package-/, '') ?? ''
  expect(created).toContain('.zip')
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.getByTestId(`archive-download-${created}`).click(),
  ])
  const archivePackage = await readFile(await download.path())

  const row = page.getByTestId(`archive-package-${created}`)
  await expect(row).toContainText(number)
  await page.getByTestId(`archive-saved-${created}`).check()
  await page.getByTestId(`archive-remove-${created}`).click()
  await expect(page.getByTestId('archive-released')).toContainText('已释放')
  await expect(row).toContainText('已从服务器移除 1 张工单')
  await expect(page.getByTestId(`archive-remove-${created}`)).toBeDisabled()

  await page.goto(`/t/${number}`)
  const tombstone = page.getByTestId('ticket-tombstone')
  await expect(tombstone).toContainText('这张工单已归档')
  await expect(tombstone).toContainText(number)
  await expect(tombstone).toContainText(title)
  await expect(tombstone).toContainText('管理员')
  await expect(tombstone).toContainText(created)

  await page.goto('/admin')
  await page.getByTestId('archive-import-file').setInputFiles({
    name: created,
    mimeType: 'application/zip',
    buffer: archivePackage,
  })
  await page.getByTestId('archive-import-inspect').click()
  const importRow = page.getByTestId(`archive-import-ticket-${number}`)
  await expect(importRow).toContainText(number)
  await expect(importRow).toContainText('可导入')
  await page.getByTestId(`archive-import-select-${number}`).check()
  await page.getByTestId('archive-import-submit').click()
  await expect(page.getByTestId('archive-import-result')).toContainText('已导入 1 张工单')
  await expect(importRow).toHaveCount(0)

  const restored = await request.get(`/api/v1/tickets/${number}`)
  expect(restored.status(), `导入后读取工单失败：${await restored.text()}`).toBe(200)
  await page.goto(`/t/${number}?imported=${Date.now()}`)
  await expect(page.getByTestId('ticket-tombstone')).toHaveCount(0)
  await expect(page.locator('main')).toContainText(title)
  await expect(page.locator('main')).toContainText('已关闭')
})
