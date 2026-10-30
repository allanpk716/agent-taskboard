// e2e 用例共用的登录与造数据辅助（不是测试文件，各 spec 都从这里导入）。
import { readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { inflateRawSync } from 'node:zlib'
import { DatabaseSync } from 'node:sqlite'
import { expect, type APIRequestContext, type Page } from '@playwright/test'

export const ADMIN = { username: 'admin', password: 'admin-pass-123' }
const E2E_CAPTCHA_ANSWER = 'TB123'

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
  // 已登录时打开 /login 会直接跳回首页，换账号前先清掉会话 cookie。
  await page.context().clearCookies({ name: 'tb_session' })
  await page.goto('/login')
  await page.getByLabel('用户名').fill(who.username)
  await page.getByLabel('密码').fill(who.password)
  await page.getByLabel('验证码').fill(E2E_CAPTCHA_ANSWER)
  await page.getByRole('button', { name: '登录' }).click()
  await page.waitForURL((u) => !u.pathname.endsWith('/login'))
}

export async function loginAdmin(request: APIRequestContext) {
  const res = await loginWithRequest(request, ADMIN)
  expect(res.ok(), '管理员登录失败').toBeTruthy()
}

export async function loginWithRequest(
  request: APIRequestContext,
  who: { username: string; password: string },
) {
  const captcha = await request.get('/api/v1/login-captcha')
  expect(captcha.ok(), '获取验证码失败').toBeTruthy()
  const { id } = (await captcha.json()) as { id: string }
  return request.post('/api/v1/session', {
    data: { ...who, captcha_id: id, captcha_answer: E2E_CAPTCHA_ANSWER },
  })
}

export async function markDraftIntroSeen(request: APIRequestContext) {
  await request.post('/api/v1/me/notices/drafts_intro/seen')
}

export function ageClosedTicket(number: string, closedAt: string): void {
  const port = process.env.E2E_PORT || '8901'
  const info = JSON.parse(readFileSync(join(tmpdir(), `tb-e2e-server-${port}.json`), 'utf-8')) as {
    data_dir: string
  }
  const db = new DatabaseSync(join(info.data_dir, 'taskboard.db'))
  try {
    const ticket = db.prepare('SELECT seq FROM tickets WHERE number = ?').get(number) as { seq: number }
    if (!ticket) throw new Error(`e2e 造数找不到工单 ${number}`)
    db.prepare(`
      UPDATE timeline_events SET at = ?
      WHERE ticket_seq = ? AND type = 'closed' AND seq = (
        SELECT MAX(seq) FROM timeline_events WHERE ticket_seq = ? AND type = 'closed'
      )
    `).run(closedAt, ticket.seq, ticket.seq)
  } finally {
    db.close()
  }
}

export function readZipTextEntry(zipPath: string, wantedName: string): string {
  const zip = readFileSync(zipPath)
  let eocd = -1
  for (let i = zip.length - 22; i >= 0; i--) {
    if (zip.readUInt32LE(i) === 0x06054b50) {
      eocd = i
      break
    }
  }
  if (eocd < 0) throw new Error('e2e 下载的 zip 缺少中央目录')
  const count = zip.readUInt16LE(eocd + 10)
  let offset = zip.readUInt32LE(eocd + 16)
  for (let i = 0; i < count; i++) {
    if (zip.readUInt32LE(offset) !== 0x02014b50) throw new Error('e2e zip 中央目录损坏')
    const method = zip.readUInt16LE(offset + 10)
    const compressedSize = zip.readUInt32LE(offset + 20)
    const nameLength = zip.readUInt16LE(offset + 28)
    const extraLength = zip.readUInt16LE(offset + 30)
    const commentLength = zip.readUInt16LE(offset + 32)
    const localOffset = zip.readUInt32LE(offset + 42)
    const name = zip.subarray(offset + 46, offset + 46 + nameLength).toString('utf-8')
    if (name === wantedName) {
      const localNameLength = zip.readUInt16LE(localOffset + 26)
      const localExtraLength = zip.readUInt16LE(localOffset + 28)
      const dataStart = localOffset + 30 + localNameLength + localExtraLength
      const data = zip.subarray(dataStart, dataStart + compressedSize)
      return (method === 8 ? inflateRawSync(data) : data).toString('utf-8')
    }
    offset += 46 + nameLength + extraLength + commentLength
  }
  throw new Error(`e2e zip 缺少 ${wantedName}`)
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
