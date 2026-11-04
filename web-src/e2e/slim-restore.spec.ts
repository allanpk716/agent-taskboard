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

// #285：管理员上传已保存的瘦身包，检查后按工单把附件恢复到原位。
test('管理员上传瘦身包检查并恢复附件', async ({ page, request }) => {
  await loginAdmin(request)
  const title = `e2e 瘦身恢复 ${Date.now()}`
  const number = await createTicket(request, { title })
  const uploaded = await request.post(`/api/v1/tickets/${number}/attachments`, {
    multipart: {
      file: { name: '恢复日志.log', mimeType: 'text/plain', buffer: Buffer.from('restore-e2e') },
    },
  })
  expect(uploaded.status(), `上传附件失败：${await uploaded.text()}`).toBe(201)
  const attachment = (await uploaded.json()) as { id: string }
  const revised = await request.patch(`/api/v1/tickets/${number}`, {
    data: {
      brief_md: `请查收 [恢复日志](att:${attachment.id})。`,
      attachment_ids: [attachment.id],
      base_revision: 1,
    },
  })
  expect(revised.status(), `挂说明附件失败：${await revised.text()}`).toBe(200)
  const closed = await request.post(`/api/v1/tickets/${number}/close`, { data: { reason: 'done' } })
  expect(closed.status(), `关闭工单失败：${await closed.text()}`).toBe(200)
  ageClosedTicket(number, new Date(Date.now() - 120 * 24 * 3600 * 1000).toISOString())

  await login(page, ADMIN)
  await page.goto('/admin')
  await page.getByTestId(`slim-ticket-${number}`).check()
  await page.getByRole('button', { name: '预览瘦身' }).click()
  await expect(page.getByTestId('slim-preview')).toContainText(number)
  await page.getByRole('button', { name: '生成瘦身包' }).click()
  let created: { name: string; ticket_numbers: string[] } | undefined
  for (let i = 0; i < 40 && !created; i++) {
    const listRes = await request.get('/api/v1/admin/slim/packages')
    expect(listRes.ok(), `读取瘦身包列表失败：${await listRes.text()}`).toBeTruthy()
    const list = (await listRes.json()) as { packages: Array<{ name: string; ticket_numbers: string[] }> }
    created = list.packages.find((pkg) => pkg.ticket_numbers.includes(number))
    if (!created) await new Promise((resolve) => setTimeout(resolve, 250))
  }
  expect(created, '生成后瘦身包列表应包含新包').toBeTruthy()
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.getByTestId(`slim-download-${created!.name}`).click(),
  ])
  const zipPath = join(tmpdir(), `tb-e2e-restore-${created!.name}`)
  await download.saveAs(zipPath)
  const manifest = JSON.parse(readZipTextEntry(zipPath, 'manifest.json')) as {
    files: Array<{ ticket_number: string; attachment_id: string }>
  }
  expect(manifest.files.map((file) => file.ticket_number)).toContain(number)
  expect(manifest.files.map((file) => file.attachment_id)).toContain(attachment.id)

  await page.getByTestId(`slim-saved-${created!.name}`).check()
  await page.getByTestId(`slim-remove-${created!.name}`).click()
  await expect(page.getByTestId('slim-remove-result')).toContainText('已移出 1 个附件')

  await page.getByTestId('slim-restore-file').setInputFiles(zipPath)
  await page.getByTestId('slim-restore-inspect').click()
  const row = page.getByTestId(`slim-restore-ticket-${number}`)
  await expect(row).toContainText(number)
  await expect(row).toContainText('可恢复')
  await page.getByTestId(`slim-restore-select-${number}`).check()
  await page.getByTestId('slim-restore-submit').click()
  await expect(page.getByTestId('slim-restore-result')).toContainText('已恢复 1 张工单')

  const restored = await request.get(`/api/v1/attachments/${attachment.id}/${encodeURIComponent('恢复日志.log')}`)
  expect(restored.status(), `恢复后下载附件失败：${await restored.text()}`).toBe(200)
  expect(await restored.text()).toBe('restore-e2e')

  // 共享 e2e 服务不能留下“已关闭满 3 个月”的工单；恢复关闭时间供其他用例筛选。
  ageClosedTicket(number, new Date().toISOString())
})
