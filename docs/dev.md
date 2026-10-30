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

注意：`serve` 启动后会把 `web/` 转成指向某个版本目录的 junction（Windows）/
符号链接（Linux），自己不再持有文件。Git Bash 里的 `rm -rf web/` 会**顺着链接
删掉目标目录里的构建产物**——想删链接本身用 `cmd //c rmdir web`（或先停服务
再处理）。

## 端到端（Playwright）

一条命令完成：编译后端二进制到 `bin/`、构建前端到 `web/`、在临时 `data_dir` 上启动真实服务（先 `create-admin`），再对 `http://127.0.0.1:8901` 跑浏览器用例：

```bash
cd web-src
npm run e2e
```

- 管理员账号：`admin` / `admin-pass-123`（见 `web-src/e2e/start-server.mjs`）。
- 后端编译为独立的 `bin/taskboard-e2e(.exe)`，不会覆盖开发用 `bin/taskboard(.exe)`。
- 端口可用 `E2E_PORT` 环境变量改；已有服务在跑时 Playwright 会复用。
- 第二轮固定使用 8915 端口并以 `E2E_CONTAINER=1` 启动，只执行依赖容器模式的用例。
- HTML 报告在仓库根 `playwright-report/`（不入库）。

## 测试底座（后续每张工单都要用）

- **Go 黑盒测试**：`tbtest.Start(t)` 在临时 `data_dir` 上启动真实服务（随机回环端口），从外部发 HTTP 请求；`s.Clock()` 拿可注入的假时钟（北京时间）；`s.CreateAdmin` / `s.Login` 是造数据辅助，`s.IssueAgentToken` / `s.AgentClient` 造成员 Agent 的凭证，
`s.CreateGuestLink` 造访客链接，`s.ForgetGuestLinkKey(id)` 把链接构造成没有加密密文的老链接，`s.GuestClient` 先用链接密钥走认领接口、再复用返回的 `tbgd_` 设备凭证作 Bearer；`s.GuestCookieClient` 模拟只带 `tb_guest_device` cookie 的访客浏览器（认领、访问码进入与入口提交测试里可从响应头取 cookie），`internal/server/intakes_test.go` 里的 `createIntake` / `submitToIntake` 造反馈入口与匿名提交（#35），`s.DoWithHeaders` 发带自定义请求头的请求（如 `Idempotency-Key`，此时不预读响应体，可自己逐字节比较），`s.OpenSSE` 打开并解析通知 SSE（可用假时钟推进 25 秒保活），`s.IssueClientCredential` 走完整 PKCE 流程签发桌面客户端凭证，`s.ClientCredentialClient` 以该凭证发请求（#74）；`s.Store()` 暴露数据库，供「第 1000 张编号」这类测试直连造数（断言外部行为仍走 HTTP），`tbtest.WithAttachments(maxFileMB, maxFilesPerPost, maxImagesPerPost, maxTicketMB)` 调小附件上限（#33，测试 413 不用打 5MB 载荷）、`s.Attachments()` 暴露附件模块（直接触发 24 小时清除任务：`CleanupUnreferenced`，服务里由周期任务调用）、`s.Archives()` 暴露归档模块（直接触发暂存包到期清除任务：`CleanupExpired`，服务里由周期任务调用）、`s.Idempotency()` 暴露幂等模块（同理直接触发过期记录清除：`PurgeExpired`），`tbtest.WithBackup(config.Backup)` 指定备份目标（#43：临时目录充当目标，或「文件下的子路径」模拟连不上的目标）、`s.Backups()` 暴露备份模块（直接做一次备份或触发调度检查，服务里由周期任务调用）。见 `internal/tbtest/harness.go` 和 `internal/server/*_test.go` 里的用例；原型场景的移植范例见 `internal/server/scenarios36_test.go`（member-handoff 的 4 个场景）、`member_handoff_test.go`（等待方推导表逐行）与 `internal/server/ticket_query_scenarios_test.go`（ticket-query 的 5 个场景 32 步，#37；`ticket_query_test.go` 是配套的世界 fixture 与逐项断言）。备份与恢复的接缝是 `internal/ops`：备份命名/镜像/轮换/.bad/调度在 `internal/ops/*_test.go`（注入时钟模拟多天）。
- `tbtest.WithContainer(publicURL, clientImageDir)` 让黑盒服务以容器模式装配（验证管理页提示和镜像客户端装载）；`tbtest.WithCaptchaSource(source, answer)` 注入固定验证码；`tbtest.WithGuestLinkCipherUnavailable()` 构造服务端加密密钥不可用的降级模式。Go 侧测试底座默认验证码是 `AB23`；Playwright 二进制用 `e2e_fixed_captcha` build tag 编译，把网页登录验证码固定为 `TB123`，发布二进制不携带该 tag。
- **CLI 测试**：`cmd/taskboard/main_test.go` 编译真实二进制后走 `create-admin` → `serve` → HTTP 登录；`cmd/taskboard/backup_cli_test.go` 走 `backup` / `restore` 的备份与恢复全流程（数据写入 → 备份 → 损坏 → 恢复 → 工单数一致、附件能打开）；`cmd/taskboard/update_cli_test.go` 走 `update` / `rollback` 的更新全流程（#45）：本地 HTTP 服务伪装 GitHub/UpdateHub 源，更新操作只针对临时安装目录里的替身 exe，绝不替换测试进程自己的二进制。
- **更新与前端切换（#45）**：源回退、镜像前缀、SHA1/SHA256 双校验在 `internal/updater/*_test.go`（假源是本地 HTTP 服务，不碰真实 GitHub/UpdateHub）；前端原子切换在 `internal/webapp/switch_test.go`（直接打服务层：并发重建 `web/` 目录链接期间，逐个请求与「页面 + 哈希资源」要么全旧要么全新）。
- **Playwright**：对“真实服务 + 构建好的前端”跑用例（含两份 Bug 反馈模板的
  访客填写行为），见 `web-src/e2e/`。

## 发布

平台和客户端分别打 tag 自动发布（细节和演练步骤见 `docs/deploy-and-release.md` 的「发布」）：

- `server/vX.Y.Z` → `.github/workflows/release-server.yml`：一次构建前端、Windows 与 `linux/amd64` 后端和容器镜像，附 `manifest.json`，更新 `channel-stable` 的 `latest.json`。
- `client/vX.Y.Z` → `.github/workflows/release-client.yml`：在 Ubuntu 上显式交叉构建 Windows/amd64 客户端，附 `manifest.json` 并更新 `latest.json`。
- `server/vX.Y.Z` 通过镜像冒烟门禁后推送 GHCR 与阿里云 ACR；本地镜像验证用 `.\scripts\docker-smoke.ps1`。
- 本地工具：`go run ./cmd/release manifest|latest`（生成清单、读改写 latest.json）；`go run ./cmd/publish-updatehub`（转发到内网 UpdateHub，需要环境变量 `UPDATEHUB_BASE_URL` 和 `UPDATEHUB_TOKEN`，可选 `GITHUB_TOKEN`、`GITHUB_MIRROR`）。

不碰 GitHub 和 UpdateHub 的演练命令在 `docs/deploy-and-release.md` 的「演练」小节；`internal/release`、`internal/updatehub` 和两个 `cmd/` 的测试就是发布逻辑的回归底座。
