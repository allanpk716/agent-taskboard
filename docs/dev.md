# 开发指南

后端是 Go 单二进制 `taskboard`（模块在仓库根），前端是 `web-src/` 下的 React + TypeScript + Vite 应用，构建产物放到仓库根的 `web/`（不入库），由后端同源托管。

## 后端

```bash
go build ./...                 # 编译
go test ./...                  # 全部测试（含 API 黑盒测试和 CLI 端到端测试）
go test ./internal/server/ -v  # 只跑 API 黑盒测试
```

本地起一个服务（先复制 `config.example.toml` 为 `config.toml`）：

```bash
go run ./cmd/taskboard serve -config config.toml
# 首次使用前创建管理员：
go run ./cmd/taskboard create-admin -config config.toml -username admin -password <密码> -name 管理员
```

交叉构建（发布产物，同时支持 Windows 与 linux/amd64，纯 Go SQLite 驱动无需 CGO）：

```bash
go build -o bin/taskboard.exe ./cmd/taskboard
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/taskboard-linux-amd64 ./cmd/taskboard
```

## 前端

```bash
cd web-src
npm install
npm run dev     # Vite 开发服务器，/api 代理到 127.0.0.1:8080（后端另起）
npm run build   # 产物输出到 ../web/
```

## 端到端（Playwright）

一条命令完成：编译后端二进制到 `bin/`、构建前端到 `web/`、在临时 `data_dir` 上启动真实服务（先 `create-admin`），再对 `http://127.0.0.1:8901` 跑浏览器用例：

```bash
cd web-src
npm run e2e
```

- 管理员账号：`admin` / `admin-pass-123`（见 `web-src/e2e/start-server.mjs`）。
- 端口可用 `E2E_PORT` 环境变量改；已有服务在跑时 Playwright 会复用。
- HTML 报告在仓库根 `playwright-report/`（不入库）。

## 测试底座（后续每张工单都要用）

- **Go 黑盒测试**：`tbtest.Start(t)` 在临时 `data_dir` 上启动真实服务（随机回环端口），从外部发 HTTP 请求；`s.Clock()` 拿可注入的假时钟（北京时间）；`s.CreateAdmin` / `s.Login` 是造数据辅助，`s.IssueAgentToken` / `s.AgentClient` 造成员 Agent 的凭证；`s.Store()` 暴露数据库，供「第 1000 张编号」这类测试直连造数（断言外部行为仍走 HTTP）。见 `internal/tbtest/harness.go` 和 `internal/server/*_test.go` 里的用例。
- **CLI 测试**：`cmd/taskboard/main_test.go` 编译真实二进制后走 `create-admin` → `serve` → HTTP 登录。
- **Playwright**：对“真实服务 + 构建好的前端”跑用例，见 `web-src/e2e/`。
