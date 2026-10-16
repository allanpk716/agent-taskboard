// 启动“真实服务 + 构建好的前端”供 Playwright 使用：
// 临时 data_dir + 临时 config.toml，先 create-admin 再 serve。
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

const dir = mkdtempSync(join(tmpdir(), 'tb-e2e-'))
const tomlPath = (p) => p.replaceAll('\\', '/')
const configPath = join(dir, 'config.toml')
writeFileSync(
  configPath,
  `data_dir = '${tomlPath(join(dir, 'data'))}'\nport = ${port}\nweb_dir = '${tomlPath(webDir)}'\n`,
)

// 创建管理员。复用已有服务时登录名可能已存在，忽略失败。
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
  console.log('[e2e] 管理员已存在（复用服务），跳过创建')
}

const child = spawn(bin, ['serve', '-config', configPath], { stdio: 'inherit' })
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

// 保持本进程存活，直到 serve 退出。
setInterval(() => {}, 1 << 30)
