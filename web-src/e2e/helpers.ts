// e2e 用例共用的登录与造数据辅助（不是测试文件，各 spec 都从这里导入）。
import { readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { expect, type APIRequestContext, type Page } from '@playwright/test'

export const ADMIN = { username: 'admin', password: 'admin-pass-123' }

// 王工是普通成员（start-server.mjs 用 #42 的成员账号接口创建）：初始密码由
// 服务器生成、只出现一次，写在按端口命名的临时文件里；服务刚起来时可能还没
// 写好，这里等它出现（复用旧服务、文件不存在时回退到旧密码）。
let cachedWang: { username: string; password: string } | null = null
export async function wang(): Promise<{ username: string; password: string }> {
  if (cachedWang) return cachedWang
  const port = process.env.E2E_PORT || '8901'
  const file = join(tmpdir(), `tb-e2e-members-${port}.json`)
  for (let i = 0; i < 80; i++) {
    try {
      const parsed = JSON.parse(readFileSync(file, 'utf-8')) as { username: string; password: string }
      cachedWang = parsed
      return parsed
    } catch {}
    await new Promise((r) => setTimeout(r, 250))
  }
  console.warn(`[e2e] 没找到 ${file}，回退到旧密码（服务是旧代码启动的？）`)
  cachedWang = { username: 'wang', password: 'wang-pass-123' }
  return cachedWang
}

export async function login(page: Page, who: { username: string; password: string }) {
  await page.goto('/login')
  await page.getByLabel('用户名').fill(who.username)
  await page.getByLabel('密码').fill(who.password)
  await page.getByRole('button', { name: '登录' }).click()
  await page.waitForURL((u) => !u.pathname.endsWith('/login'))
}

export async function loginAdmin(request: APIRequestContext) {
  const res = await request.post('/api/v1/session', { data: ADMIN })
  expect(res.ok(), '管理员登录失败').toBeTruthy()
}

export async function markDraftIntroSeen(request: APIRequestContext) {
  await request.post('/api/v1/me/notices/drafts_intro/seen')
}

const guestTokens = new Map<string, string>()

export async function claimGuestLink(request: APIRequestContext, linkKey: string): Promise<string> {
  const cached = guestTokens.get(linkKey)
  if (cached) return cached
  const res = await request.post('/api/v1/guest-links/claim', {
    data: { author_name: '检验科 张三' },
    headers: { Authorization: `Bearer ${linkKey}` },
  })
  expect(res.status(), `访客链接认领失败：${await res.text()}`).toBe(201)
  const token = ((await res.json()) as { device_token: string }).device_token
  guestTokens.set(linkKey, token)
  return token
}

export async function openGuestPage(
  page: Page,
  _request: APIRequestContext,
  linkKey: string,
): Promise<void> {
  const token = await claimGuestLink(page.request, linkKey)
  await page.addInitScript(({ key, token }) => {
    localStorage.setItem(`tb:guest-device:${key}`, token)
  }, { key: linkKey, token })
  await page.goto(`/g/${linkKey}`)
}

/** 用管理员会话建一张自由格式工单（可指派），返回编号。 */
export async function createTicket(
  request: APIRequestContext,
  opts: { title: string; brief?: string; labels?: string[]; assignee?: string } = { title: '测试工单' },
): Promise<string> {
  await loginAdmin(request)
  const res = await request.post('/api/v1/tickets', {
    data: {
      title: opts.title,
      brief_md: opts.brief ?? `${opts.title} 的说明。`,
      labels: opts.labels ?? ['机型:UD-2300'],
      ...(opts.assignee ? { assignee: opts.assignee } : {}),
    },
  })
  expect(res.status(), `建单失败：${await res.text()}`).toBe(201)
  return (await res.json()).number
}
