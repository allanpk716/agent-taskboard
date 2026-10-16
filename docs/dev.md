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

- **Go 黑盒测试**：`tbtest.Start(t)` 在临时 `data_dir` 上启动真实服务（随机回环端口），从外部发 HTTP 请求；`s.Clock()` 拿可注入的假时钟（北京时间）；`s.CreateAdmin` / `s.Login` 是造数据辅助，`s.IssueAgentToken` / `s.AgentClient` 造成员 Agent 的凭证，
`s.CreateGuestLink` / `s.GuestClient`（密钥直接作 Bearer）造访客链接与访客凭证（#32），`internal/server/intakes_test.go` 里的 `createIntake` / `submitToIntake` 造反馈入口与匿名提交（#35），`s.DoWithHeaders` 发带自定义请求头的请求（如 `Idempotency-Key`，此时不预读响应体，可自己逐字节比较）；`s.Store()` 暴露数据库，供「第 1000 张编号」这类测试直连造数（断言外部行为仍走 HTTP）。见 `internal/tbtest/harness.go` 和 `internal/server/*_test.go` 里的用例；原型场景的移植范例见 `internal/server/scenarios36_test.go`（member-handoff 的 4 个场景）与 `member_handoff_test.go`（等待方推导表逐行）。
- **CLI 测试**：`cmd/taskboard/main_test.go` 编译真实二进制后走 `create-admin` → `serve` → HTTP 登录。
- **Playwright**：对“真实服务 + 构建好的前端”跑用例，见 `web-src/e2e/`。

## 发布

前后端独立打 tag 自动发布（细节和演练步骤见 `docs/deploy-and-release.md` 的「发布」）：

- `server/vX.Y.Z` → `.github/workflows/release-server.yml`：构建 Windows 与 `linux/amd64` 后端，附 `manifest.json`，更新 `channel-stable` 的 `latest.json`。
- `web/vX.Y.Z` → `.github/workflows/release-web.yml`：构建前端 zip，同样附 `manifest.json` 并更新 `latest.json`；前端要求的最低后端版本写在 `web-src/server-compat.json` 的 `min_server`，开始依赖新后端接口时记得改它。
- 本地工具：`go run ./cmd/release manifest|latest`（生成清单、读改写 latest.json）；`go run ./cmd/publish-updatehub`（转发到内网 UpdateHub，需要环境变量 `UPDATEHUB_BASE_URL` 和 `UPDATEHUB_TOKEN`，可选 `GITHUB_TOKEN`、`GITHUB_MIRROR`）。

不碰 GitHub 和 UpdateHub 的演练命令在 `docs/deploy-and-release.md` 的「演练」小节；`internal/release`、`internal/updatehub` 和两个 `cmd/` 的测试就是发布逻辑的回归底座。
