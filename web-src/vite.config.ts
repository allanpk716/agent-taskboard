import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// 构建产物放到仓库根的 web/（即部署时的前端目录），开发时代理 /api 到本机后端。
export default defineConfig({
  plugins: [react()],
  build: { outDir: '../web', emptyOutDir: true },
  server: { proxy: { '/api': 'http://127.0.0.1:8080' } },
})
