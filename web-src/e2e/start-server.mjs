// 启动“真实服务 + 构建好的前端”供 Playwright 使用：
// 临时 data_dir + 临时 config.toml，先 create-admin 再 serve。
// 第二位成员（王工，非管理员）通过 #42 的成员账号接口创建，初始密码由服务器
// 生成、写进临时文件供用例读取（只出现一次，服务端不再回显）。
// 进程随 Playwright 结束被清理（SIGINT/SIGTERM）。
import { spawn, execFileSync } from 'node:child_process'
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'

const port = Number(process.env.E2E_PORT || 8901)
const repoRoot = resolve(import.meta.dirname, '..', '..')
const bin = process.env.E2E_BIN || join(repoRoot, 'bin', process.platform === 'win32' ? 'taskboard.exe' : 'taskboard')
const webDir = process.env.E2E_WEB_DIR || join(repoRoot, 'web')
const admin = { username: 'admin', password: 'admin-pass-123', name: '管理员' }
// 成员网页 e2e 需要第二位成员（指派、收件箱流转、非管理员视角）。
// 用 #42 的成员账号接口创建（普通成员）；密码由服务器生成。
const worker = { username: 'wang', name: '王工' }
// 初始密码按端口放在临时文件里：服务被复用时（Playwright reuseExistingServer），
// 首次创建时写下的密码仍然有效，用例照常能读到。
const membersFile = join(tmpdir(), `tb-e2e-members-${port}.json`)

const dir = mkdtempSync(join(tmpdir(), 'tb-e2e-'))
const tomlPath = (p) => p.replaceAll('\\', '/')
const configPath = join(dir, 'config.toml')
writeFileSync(
  configPath,
  `data_dir = '${tomlPath(join(dir, 'data'))}'\nport = ${port}\nweb_dir = '${tomlPath(webDir)}'\n`,
)

// 创建管理员（复用已有服务时登录名可能已存在，忽略失败）。
try {
  execFileSync(bin, [
    'create-admin',
    '-config',
    configPath,
    '-username',
    admin.username,
    '-password',
    admin.password,
    '-name',
    admin.name,
  ], { stdio: 'inherit' })
} catch {
  console.log(`[e2e] ${admin.username} 已存在（复用服务），跳过创建`)
}

// 先清掉旧密码文件：它只在本进程创建成员成功后重新出现，用例等它出现即可，
// 不会读到上一次运行（或另一台服务）的旧密码。
try {
  rmSync(membersFile, { force: true })
} catch {}

const child = spawn(bin, ['serve', '-config', configPath], { stdio: 'inherit' })
const base = `http://127.0.0.1:${port}`
const cleanup = () => {
  child.kill()
  try {
    rmSync(dir, { recursive: true, force: true })
  } catch {}
}
process.on('SIGINT', () => {
  cleanup()
  process.exit(0)
})
process.on('SIGTERM', () => {
  cleanup()
  process.exit(0)
})
child.on('exit', (code) => {
  try {
    rmSync(dir, { recursive: true, force: true })
  } catch {}
  process.exit(code ?? 0)
})

// 等服务起来后，用管理员会话把第二位成员建成普通成员（#42 接口）。
async function createWorker() {
  for (let i = 0; i < 60; i++) {
    try {
      const up = await fetch(`${base}/api/v1/session`, { method: 'POST', body: '{}' })
      // 空 body 是合法 JSON（缺字段）→ 401 invalid_credentials；服务应答即活。
      if (up.status === 400 || up.status === 401) break
    } catch {}
    await new Promise((r) => setTimeout(r, 500))
  }
  const login = await fetch(`${base}/api/v1/session`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: admin.username, password: admin.password }),
  })
  if (!login.ok) throw new Error(`[e2e] 管理员登录失败：${login.status}`)
  const cookie = (login.headers.get('set-cookie') ?? '').split(';')[0]
  const res = await fetch(`${base}/api/v1/members`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Cookie: cookie },
    body: JSON.stringify({ username: worker.username, name: worker.name }),
  })
  if (res.status === 201) {
    const { initial_password } = await res.json()
    writeFileSync(membersFile, JSON.stringify({ ...worker, password: initial_password }))
    console.log(`[e2e] 已创建普通成员 ${worker.username}，初始密码写入 ${membersFile}`)
  } else if (res.status === 409) {
    console.log(`[e2e] ${worker.username} 已存在（复用服务），沿用 ${membersFile} 里的密码`)
  } else {
    throw new Error(`[e2e] 创建成员 ${worker.username} 失败：${res.status} ${await res.text()}`)
  }
}
createWorker().catch((err) => {
  console.error(String(err))
  child.kill()
  process.exit(1)
})

// 保持本进程存活，直到 serve 退出。
setInterval(() => {}, 1 << 30)
