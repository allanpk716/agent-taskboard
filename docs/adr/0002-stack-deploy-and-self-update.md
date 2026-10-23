# Go + SQLite 单二进制，静态前端独立更新，GitHub Release 与 UpdateHub 双发布源

状态补充：**部分被 [ADR 0009](0009-cloud-docker-deployment.md) 取代**。当前生产部署的 Windows/NSSM 形态、内网 VM 位置和容器内执行原地 exe 更换的部分以 ADR 0009 为准；单二进制、独立前端更新和 Release/UpdateHub 双发布源仍沿用本决策。

平台由本部门借助 Agent 长期维护，部门已有一个 Go + SQLite、由 NSSM 托管的内网服务（UpdateHub），可以照着做。因此后端选 Go 单二进制，数据库用 SQLite（WAL 模式），附件存在数据目录下的文件里，以 NSSM 服务的形式部署在 UpdateHub 所在的 VM 上。

前端比后端改得频繁，所以前端做成静态应用（React + TypeScript + Vite），放在磁盘上的独立目录中，由同一个 Go 进程同源提供。更新前端只需替换这个目录，后端不用重启。网页只调用公开的 HTTP API。API 在 `/api/v1` 内只做增量修改，破坏性变更开 `/api/v2`。

后端更新会造成几秒钟停服，由前端兜底，不追求零停机：
- 访客在网页上的输入实时保存到浏览器本地存储，收到服务器的成功响应后才清除；
- 所有会产生新内容的请求都带 `Idempotency-Key`，前端可以放心地自动重试。

更新只由管理员手动触发，入口有管理页按钮、`taskboard update` 命令和管理 API。前端和后端各自独立版本、独立更新。

后端更新流程：
1. 校验下载的包；
2. 自动备份数据库后执行迁移；
3. 把正在运行的 exe 改名为 `.prev`，放入新的 exe；
4. 进程退出，由 NSSM 拉起新版本。

新版本起不来时，在服务器上执行 `taskboard rollback` 手动回滚。

发布：前端打 `web/v*` tag，后端打 `server/v*` tag，GitHub Actions 只构建一次，产物附 `manifest.json`（含 SHA256 和前端要求的最低后端版本）。固定的 `channel-stable` Release 里放一个 `latest.json`，记录前后端各自的最新版本。开发机上的脚本把同一份产物包装成 nupkg，转发到内网的 UpdateHub。

更新源可以配置多个，按顺序回退，默认先查 UpdateHub，再查 GitHub。GitHub 源不调用 `api.github.com`，只用 `github.com/.../releases/...` 下的固定地址，因此可以配置镜像前缀和代理。不管经由哪个镜像下载，都按 SHA256 校验。

## Considered Options

- **PostgreSQL**：一个部门的工单量用不上，还要多运维一个进程。SQLite 备份和迁移都只是拷文件。
- **服务端渲染并嵌入二进制**：只有一个产物，但改前端也要重启后端。
- **Docker 部署**：部门的 Docker 环境已经停用。
- **零停机（多实例切换）**：要引入反向代理，还要协调 SQLite 迁移，对 MVP 来说太重。用浏览器草稿加幂等重试，换来的用户感知效果接近无缝。
- **只用 GitHub Release**：国内访问 GitHub 不稳定。**只用 UpdateHub**：不能打 tag 自动发布，因为 GitHub Actions 的构建机访问不到内网。所以两个都要，产物只构建一次。

## Consequences

- 首次部署就启用 HTTPS，做法参照日报系统：
  - 用公网域名（如 `taskboard.urithub.com`），在阿里云 DNS 上把它解析到内网 IP；
  - Caddy（带 alidns 插件）用 NSSM 托管，通过 DNS-01 方式向 Let's Encrypt 申请证书，自动签发、自动续期；
  - Caddy 反代到只监听 `127.0.0.1` 的 taskboard 服务；
  - 阿里云 AccessKey 用这个服务单独的 RAM 子账号，只授予 DNS 权限。

  这样访客链接的密钥（Bearer token）不会明文传输，浏览器端也不用导入证书。前提是 VM 能访问公网，签发和续期证书都要用到。
- 新版本起不来时，网页上的按钮也就用不了，回滚只能在服务器上执行命令行。
- 「Agent 访问协议原型」已定的协议补充一条：所有会产生新内容的请求都支持 `Idempotency-Key`。
