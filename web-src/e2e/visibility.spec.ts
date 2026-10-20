import { expect, test } from '@playwright/test'
import { createTicket, login, wang } from './helpers'

test('成员打开可见范围外的工单显示统一404提示', async ({ page, request }) => {
  const number = await createTicket(request, { title: '管理员保留的敏感工单' })
  await login(page, await wang())
  await page.goto(`/t/${number}`)

  await expect(page.locator('.ecard')).toHaveText('找不到这张工单，或你没有权限查看')
  await expect(page.getByRole('heading', { name: '管理员保留的敏感工单' })).toHaveCount(0)
})
