#!/usr/bin/env node
// 端到端测试入口：编译后端二进制、构建前端，然后跑 Playwright。
// 用法：cd web-src && npm run e2e
import { execFileSync, spawnSync } from 'node:child_process'
import { existsSync } from 'node:fs'
import { join, resolve } from 'node:path'

const repoRoot = resolve(import.meta.dirname, '..', '..')
const webSrc = resolve(import.meta.dirname, '..')
const run = (cmd, args, opts = {}) => {
  console.log(`[e2e] ${cmd} ${args.join(' ')}`)
  execFileSync(cmd, args, { stdio: 'inherit', shell: process.platform === 'win32', ...opts })
}

// 1. 编译后端到 bin/（Windows 与本机构建；linux/amd64 交叉构建见 docs/dev.md）。
const binPath = join(repoRoot, 'bin', `taskboard-e2e${process.platform === 'win32' ? '.exe' : ''}`)
run('go', ['build', '-tags', 'e2e_fixed_captcha', '-o', binPath, './cmd/taskboard'], { cwd: repoRoot })

// 2. 构建前端到 web/（已构建且不是最新也直接重建，成本可接受）。
run('npm', ['run', 'build'], { cwd: webSrc })
if (!existsSync(join(repoRoot, 'web', 'index.html'))) {
  console.error('[e2e] 前端构建后仍没有 web/index.html')
  process.exit(1)
}

// 3. 跑 Playwright。
const npx = 'npx'
const runPlaywright = (env, args = []) => {
  const res = spawnSync(npx, ['playwright', 'test', ...args], {
    stdio: 'inherit',
    cwd: webSrc,
    shell: process.platform === 'win32',
    env: { ...process.env, ...env },
  })
  return res.status ?? 1
}

const status = runPlaywright({})
if (status !== 0) process.exit(status)
// 容器提示第二轮：独立服务与端口，只跑需要容器部署配置的用例。
process.exit(runPlaywright({ E2E_CONTAINER: '1', E2E_PORT: '8915' }, ['--grep', '容器模式下管理页提示升级镜像']))
