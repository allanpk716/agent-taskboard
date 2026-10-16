import { defineConfig } from '@playwright/test'

const port = Number(process.env.E2E_PORT || 8901)
const baseURL = `http://127.0.0.1:${port}`

export default defineConfig({
  testDir: 'e2e',
  timeout: 30_000,
  fullyParallel: false, // 同一个服务实例，串行跑
  use: { baseURL },
  outputDir: 'test-results',
  reporter: [['list'], ['html', { open: 'never', outputFolder: '../playwright-report' }]],
  webServer: {
    command: 'node e2e/start-server.mjs',
    url: `${baseURL}/`,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
    env: { ...process.env, E2E_PORT: String(port) },
  },
})
