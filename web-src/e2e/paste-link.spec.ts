import { expect, test } from '@playwright/test'
import { parsePasteLink } from '../src/member/pasteLink'
import { ADMIN, createTicket, login } from './helpers'

test('识别带域名的完整工单链接', () => {
  expect(parsePasteLink('https://taskboard.example.com/t/20261015-007')).toEqual({
    kind: 'path',
    path: '/t/20261015-007',
  })
})

test('识别不同域名、IP、http 与无协议写法', () => {
  expect(parsePasteLink('http://127.0.0.1:8901/t/1234')).toEqual({ kind: 'path', path: '/t/1234' })
  expect(parsePasteLink('taskboard.internal.local/drafts/dft_89#reply')).toEqual({
    kind: 'path',
    path: '/drafts/dft_89#reply',
  })
  expect(parsePasteLink('/t/20261015-007?list=inbox')).toEqual({
    kind: 'path',
    path: '/t/20261015-007?list=inbox',
  })
})

test('识别夹在中文句子里且带句末标点的链接', () => {
  expect(parsePasteLink('地址：https://agent.example.com/t/20261015-007?list=inbox。请处理。')).toEqual({
    kind: 'path',
    path: '/t/20261015-007?list=inbox',
  })
  expect(parsePasteLink('（链接 “http://example.com/t/1234?x=1”）')).toEqual({
    kind: 'path',
    path: '/t/1234?x=1',
  })
})

test('识别草稿链接并保留查询参数和 hash', () => {
  expect(parsePasteLink('请确认草稿 https://example.com/drafts/dft_42?from=agent#draft，谢谢')).toEqual({
    kind: 'path',
    path: '/drafts/dft_42?from=agent#draft',
  })
})

test('识别纯工单编号和井号序号', () => {
  expect(parsePasteLink('先处理 20261015-007')).toEqual({ kind: 'path', path: '/t/20261015-007' })
  expect(parsePasteLink('再看 #1234')).toEqual({ kind: 'path', path: '/t/1234' })
})

test('拒绝访客链接，且按出现位置优先返回拒绝', () => {
  expect(parsePasteLink('https://guest.example.com/g/tbg_secret-key')).toEqual({ kind: 'guest-link' })
  expect(parsePasteLink('/g/tbg_first https://example.com/t/20261015-007')).toEqual({ kind: 'guest-link' })
})

test('识别不到时返回无匹配', () => {
  expect(parsePasteLink('今天没有可打开的链接')).toEqual({ kind: 'none' })
  expect(parsePasteLink('')).toEqual({ kind: 'none' })
})

test('从剪贴板粘贴工单链接后在应用内打开工单', async ({ page, request }) => {
  const number = await createTicket(request, { title: '粘贴链接打开的工单', assignee: '管理员' })
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)
  await page.evaluate(
    (text) => navigator.clipboard.writeText(text),
    `请处理 https://messages.example.com/t/${number}?list=inbox。谢谢`,
  )

  const pasteButton = page.getByRole('button', { name: '📋 粘贴链接' })
  await expect(pasteButton).toBeVisible()
  await pasteButton.click()
  await expect(page).toHaveURL(new RegExp(`/t/${number}\\?list=inbox$`))
  await expect(page.getByRole('heading', { name: '粘贴链接打开的工单' })).toBeVisible()
})

test('剪贴板是访客链接时拒绝打开并展开手动输入', async ({ page }) => {
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  await login(page, ADMIN)
  await page.setViewportSize({ width: 1200, height: 800 })
  await expect(page.getByRole('button', { name: '📋 粘贴链接' })).toBeVisible()
  await page.evaluate(
    (text) => navigator.clipboard.writeText(text),
    'https://guest.example.com/g/tbg_do-not-claim',
  )

  await page.getByRole('button', { name: '📋 粘贴链接' }).click()
  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByText('这是访客链接，成员打开会把它认领掉')).toBeVisible()
  await expect(page.getByLabel('粘贴的链接')).toBeVisible()
  await expect(page.getByRole('button', { name: '打开', exact: true })).toBeVisible()
})
