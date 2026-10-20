# 部署、发布与更新：实施参考

开发和部署时使用的具体约定。决策的理由见 `docs/adr/0002-stack-deploy-and-self-update.md`，讨论过程见工单 #7「自建服务的技术栈与部署形态」。本仓库是公开的，**这里不写内网 IP、账号和密钥**。

## 组成

| 部件 | 形态 |
|---|---|
| 后端 | Go 单二进制 `taskboard.exe`（同时产出 `linux/amd64` 版本），只监听 `127.0.0.1:<port>` |
| 数据库 | SQLite，WAL 模式，放在数据目录下 |
| 附件 | 数据目录下的普通文件，数据库只记元数据 |
| 前端 | React + TypeScript + Vite 构建的静态文件，放在独立的 `web/` 目录，由后端同源提供 |
| HTTPS | Caddy（带 alidns 插件）反代到后端，NSSM 托管 |
| 进程托管 | NSSM，taskboard 和 caddy 各注册一个 Windows 服务 |

程序和数据分开存放：程序装在 C:（VM 的本地盘，空间小）；数据目录由 `config.toml` 的 `data_dir` 指定，放在 D:（空间大，是 NAS 上的 qcow2）。建议的目录结构：

```
C:\<安装目录>\
├── taskboard.exe
├── taskboard.exe.prev        # 上一版，供 rollback 使用
├── config.toml
├── web\                      # 当前前端（切换目标）
└── web-<version>\            # 已解压的各版前端

D:\taskboard-data\            # data_dir
├── taskboard.db
├── attachments\
├── client\                   # 服务端代管的桌面客户端
│   ├── client.json           # 当前和上一版客户端的指针
│   └── taskboard-client-<version>.exe
└── backups\                  # 只在没有配置备份目标时使用，见「备份与恢复」
```

```toml
data_dir = 'D:\taskboard-data'
```

## API 约定（补充协议原型）

- 前缀为 `/api/v1`，只做增量修改：可以加字段、加接口，不删除、不改名。破坏性变更开 `/api/v2`，两版并存一段时间。
- 所有会产生新内容的请求（回复、建单、反馈提交、附件上传）都接受 `Idempotency-Key` 请求头。服务端按 key 去重，key 重复时返回首次请求的结果；入口附件上传是唯一例外：key 必填，用来把同一份草稿的多个附件归属到之后的一次提交，不做单响应快照。
- 管理员专用的更新接口：检查更新、执行更新，供管理页按钮和管理员的 Agent 调用。

## 附件上限与显示

附件有上限，以控制存储和备份的体积。大文件改为外部文件引用：放到 NAS 等共享位置，把路径写在工单里。

| 规则 | 默认值 | 超限时 |
|---|---|---|
| 单个文件 | 5MB | 413 `attachment_too_large` |
| 每条说明或回复的附件 | 5 个 | 413 `attachment_count_exceeded` |
| 每条说明或回复的插图 | 10 张 | 413 `attachment_count_exceeded` |
| 整张工单合计 | 50MB | 413 `ticket_quota_exceeded` |

```toml
[attachments]
max_file_mb         = 5
max_files_per_post  = 5
max_images_per_post = 10
max_ticket_mb       = 50

[intake]
upload_rate_per_min = 20
pending_quota_mb    = 200
```

- 图片由网页在上传前压缩：长边缩到 2048px，转为 JPEG（质量 85%），去掉 EXIF。本来就不到 1MB 的 PNG 截图保持原样。平台不保存原图。服务端只检查上限，不压缩。Agent 通过 API 上传图片前需要自行压缩。
- 插图在正文中随宽度缩放，最宽显示 800px，点击查看大图。图片、PDF、`.txt` 和 `.log` 可以在线预览（`inline`），其他格式一律返回 `Content-Disposition: attachment`。HTML、SVG 只能下载。所有附件响应都带 `X-Content-Type-Options: nosniff`。
- 附件地址使用与工单相同的凭证（成员登录态、访客链接密钥、Agent token）。访客链接撤销或过期后，通过它拿到的附件地址也随之失效。
- 正文中的 UNC 路径（`\\服务器\共享\...`）显示为带“复制路径”按钮的文本块，不做成链接。

讨论过程见工单 #25「附件和插图在网页上的显示与访问控制」和地图 #1 的 Notes。

## 前端：访客输入不丢失

- 草稿以“访客链接 + 工单”为键，实时写入 `localStorage`，覆盖回复、说明和反馈表单，也包括已上传附件的 `att:` 引用。
- 只有收到服务器的 2xx 响应后才清除草稿。
- 请求失败或连不上服务时，显示“服务更新中，内容已保存”，然后带同一个 `Idempotency-Key` 自动重试。
- 缓存策略：`index.html` 设为 `no-cache`；带哈希的资源文件长期缓存。

## 更新

只由管理员手动触发，有三个入口：管理页按钮、`taskboard update` 命令、管理 API（`GET`/`POST /api/v1/admin/update`），背后是同一套逻辑（`internal/updater`）。服务不会自动安装更新，只在管理页提示有新版本。命令行先看一眼：`taskboard update -check` 只查不装，`taskboard version` 显示当前前后端版本（后端版本是发布流水线注入二进制的；前端版本在 `web/version.json` 里，手动部署的首个前端没有它，视为未知）。

1. 按配置的更新源顺序检查，用第一个连得上的源，并记录实际用的是哪个（源全不可用时明确报错，不会去连没配置的地址）。
2. 前端、后端和桌面客户端分别对比当前版本和最新版本，版本互相独立。客户端尚未发布只是该组件「不可用」，不影响前后端检查和更新。前端的 manifest 写着它需要的最低后端版本 `min_server`；后端版本不够时，先升级后端（最新的后端也不够时拒绝这次前端更新，等发布满足要求的后端）。
3. **后端**：
   1. 下载新版本（按当前平台取 `taskboard.exe` 或 `taskboard-linux-amd64`），按 manifest 的 SHA256 校验，不符就拒绝；
   2. 做一次更新前备份（见「备份与恢复」），备份失败就中止更新，不替换 exe；
   3. 把正在运行的 exe 改名为 `taskboard.exe.prev`，放入新的 exe（正在运行的 exe 只改名不覆盖，Windows 也允许）；
   4. 管理页/管理 API 触发时：写完响应后进程退出，由 NSSM 拉起新版本；命令行触发时：提示执行 `nssm restart <服务名>`；
  5. 新版本启动时执行迁移（serve 打开库时自动做），再做健康检查：对自身的 `GET /api/v1/health` 自检一次，通过则打印版本，不通过只警告不退出（是否重启交给 NSSM；新版本起不来时回滚见下）。迁移 `0026_ticket_reads.sql` 会记录当时的全局变更序号作为工单未读基线：上线前的历史工单全部视为已读，不回填「成员 × 工单」；基线之后的新事件按 #135 的未读规则计算。
4. **前端**：下载 zip，校验 SHA256，解压到 `web-<version>/`（连同写入 `version.json` 版本标记），再原子地切换 `web/`（重建目录链接），服务不用重启。刚换下的版本目录再保留两代：翻到一半的旧页面还能取到旧哈希资源，不会半新半旧。UpdateHub 源取到的是 nupkg：先按 feed 里的 SHA1 校验整包，解包后再按里面的 manifest 的 SHA256 校验产物。
5. **桌面客户端**：下载 `taskboard-client.exe`，按 manifest 的 SHA256 和大小校验并确认是 Windows/amd64 PE 后，按版本存入 `data_dir/client/taskboard-client-<version>.exe`，再原子切换 `client.json` 的当前/上一版指针；更早版本尽力清理。成员从「我的」页面下载，客户端自动更新时调 `GET /api/v1/client/latest` 与下载接口。所有组件都先下载并校验，之后才开始替换磁盘文件；客户端组件失败只记录在该组件结果里，不阻断前后端更新。
6. **回滚**：在服务器上执行 `taskboard rollback`（服务必须已停止，配置的端口上仍在监听时拒绝执行），换回 `.prev` 版本和最近一次更新前的备份（先恢复库再换 exe，两步都完成后才启动服务；更新之后新产生的数据会回滚掉）。新版本起不来时网页上的按钮不可用，只能这样回滚。如果新 exe 连运行都运行不了（`taskboard rollback` 本身也执行不了），就手动处理：服务停着时把 `taskboard.exe` 改名挪开，复制 `taskboard.exe.prev` 为 `taskboard.exe`，再 `nssm start`。新版本没启动过就没跑过迁移，库不用恢复。更新器替换前会检查新 exe 能否在本机运行，挡住了这类产物（2026-10-17 的 server/v0.1.4 在 Ubuntu 上漏设 GOOS，`taskboard.exe` 其实是 Linux 二进制，生产因此中断约 2 分钟，就是这样手动换回的）；流水线构建后也会用 `release check-exe` 核对产物平台。

配置示意：

```toml
[update]
sources = ["updatehub", "github"]   # 按顺序回退；留空（默认）不检查更新

[update.updatehub]
base_url = "https://<updatehub 地址>"
channel  = "stable"

[update.github]
repo   = "allanpk716/agent-taskboard"
mirror = ["https://<镜像前缀>/"]    # 按顺序尝试，最后直连
proxy  = ""                         # http:// 或 socks5://；为空时读取 HTTPS_PROXY 环境变量
```

## 发布

仓库里的实现：`.github/workflows/release-server.yml`、`release-web.yml` 和 `release-client.yml`（CI）；`cmd/release`（manifest、latest 和 check-exe 子命令，CI 与本地演练共用）；`cmd/publish-updatehub`（开发机转发脚本）；`web-src/server-compat.json`（前端声明的 `min_server`）。

### GitHub Release（唯一构建来源）

- 后端打 `server/vX.Y.Z` tag，前端打 `web/vX.Y.Z` tag，桌面客户端打 `client/vX.Y.Z` tag，各自触发 GitHub Actions 构建并发布 Release。
- 每个 Release 都附带构建产物和 `manifest.json`：

  ```json
  { "component": "web", "version": "1.4.0", "min_server": "1.2.0",
    "files": [{ "name": "taskboard-web-1.4.0.zip", "sha256": "..." }] }
  ```

- 固定 tag `channel-stable` 下的 Release 里放一个 `latest.json`，记录后端、前端、桌面客户端各自的最新版本。CI 每次发布后自动更新它。组件分开打 tag 后，`releases/latest` 只指向最近发布的那一个，所以不能依赖它。
- 客户端**不调用 `api.github.com`**，只使用固定地址：
  - `https://github.com/<repo>/releases/download/channel-stable/latest.json`
  - `https://github.com/<repo>/releases/download/<tag>/<file>`

  因此可以直接在地址前面加镜像前缀。不管经由哪个镜像下载，都按 `manifest.json` 里的 SHA256 校验。

CI 更新 `latest.json` 的方式是读改写：下载现有文件，用 `release latest` 只替换本组件的条目（另一组件原样保留），再 `--clobber` 上传。前后端同时打 tag 的极端情况下，两个工作流可能同时尝试创建 `channel-stable` Release，失败的那个重跑一次即可。

### 演练（不碰 GitHub 和 UpdateHub）

在开发机上就能把发布逻辑全部走一遍（不依赖 `act`）：

```bash
# 后端：构建产物 + 清单
mkdir -p dist
go build -trimpath -ldflags='-s -w' -o dist/taskboard.exe ./cmd/taskboard
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o dist/taskboard-linux-amd64 ./cmd/taskboard
go run ./cmd/release manifest -component server -version 0.1.0 -dir dist

# latest.json：先发后端再发前端，互不覆盖
go run ./cmd/release latest -component server -version 0.1.0 -tag server/v0.1.0 -repo OWNER/REPO -file latest.json
go run ./cmd/release latest -component web -version 0.2.0 -tag web/v0.2.0 -repo OWNER/REPO -file latest.json

# 桌面客户端：Ubuntu 上显式交叉构建 Windows/amd64（#81 的入口包）
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
  -ldflags='-s -w -H=windowsgui -X github.com/allanpk716/agent-taskboard/internal/version.Client=0.1.0' \
  -o dist/taskboard-client.exe ./cmd/taskboard-client
go run ./cmd/release check-exe -goos windows -goarch amd64 dist/taskboard-client.exe
go run ./cmd/release manifest -component client -version 0.1.0 -dir dist
go run ./cmd/release latest -component client -version 0.1.0 -tag client/v0.1.0 -repo OWNER/REPO -file latest.json

# 前端：构建 + 打包 + 清单（min_server 来自 web-src/server-compat.json）
cd web-src && npm ci && npm run build && cd ..
(cd web && zip -r ../dist/taskboard-web-0.2.0.zip .)
go run ./cmd/release manifest -component web -version 0.2.0 -min-server $(jq -r .min_server web-src/server-compat.json) -dir dist

# 转发脚本：-dry-run 只下载打包不上传；-download-base 指向任意提供固定下载地址的服务
python -m http.server 8931 --directory dist &
go run ./cmd/publish-updatehub -tag server/v0.1.0 \
  -download-base http://127.0.0.1:8931/ -dry-run
```

`go test ./internal/updatehub/ ./cmd/publish-updatehub/` 里用本地假 HTTP 服务按 UpdateHub 上传接口的校验逻辑（Bearer、multipart、PackageId 与 appId 一致）完整走一遍上传，不需要真的连内网。

### UpdateHub（内网，转发同一份产物）

UpdateHub 是部门已有的内网更新服务，实现了 Velopack 协议。GitHub Actions 的构建机访问不到内网，所以由开发机上的 `publish-updatehub` 脚本完成：下载指定 Release 的产物，包装成 nupkg，上传到 UpdateHub。

- 应用名：`agent-taskboard-server`、`agent-taskboard-web`、`agent-taskboard-client`；通道：`stable`。
- 第一次上传时自动建应用。appId 只能用字母、数字和连字符；feed 里的 `PackageId` 必须和 appId 一致，不区分大小写。
- 上传：`POST /api/apps/{appId}/channels/{channel}/releases`，带 `Authorization: Bearer <token>`，multipart 一次上传 nupkg 和 `releases.win.json`，可附 `notes`，单次不超过 200MB。token 只放在开发机的环境变量里。
- 查询：`GET /{appId}/{channel}/releases.win.json`，不需要认证。返回格式为 `{"Assets":[{PackageId,Version,Type,FileName,SHA1,Size}]}`，再按 `FileName` 下载。
- 只有 `.nupkg` 能登记成版本。nupkg 就是一个 zip，里面加一个 `.nuspec`。我们自己生成，不依赖 .NET 的 `vpk` 工具。原始的 `manifest.json` 一起打进包里。
- 走这个源时，按 feed 里的 SHA1 校验，解包后再按 manifest 里的 SHA256 校验一次。

开发机上执行（地址和 token 只从环境变量读，不写入任何文件）：

```bash
# 需要：UPDATEHUB_BASE_URL、UPDATEHUB_TOKEN；可选：GITHUB_TOKEN、GITHUB_MIRROR（镜像前缀）
go run ./cmd/publish-updatehub -tag server/v1.2.3            # 或 web/v1.4.0
go run ./cmd/publish-updatehub -tag web/v1.4.0 -notes "修复访客链接过期提示"
go run ./cmd/publish-updatehub -tag client/v1.0.0 -notes "转发桌面客户端"
```

客户端发布后，管理员不需要逐台分发：在管理页看到客户端新版本后执行「立即更新」（或在服务器执行 `taskboard update`），服务端会把 exe 拉回、校验并代管；之后「我的」页面的下载按钮和 `/api/v1/client/latest` 都指向这份文件。

脚本流程：下载该 Release 的 `manifest.json` 和全部产物，逐一校验 SHA256；生成 `.nuspec`（id 与 appId 一致）后把产物和 `manifest.json` 一起打成 nupkg，布局为：

```
{appId}.nuspec        # id、version、title、authors、description
manifest.json         # Release 上的清单原样带入
payload/{name}        # 产物本身（后端是两个二进制，前端是 web zip）
```

然后生成 `releases.win.json`（`PackageId` 与 appId 一致，SHA1 和 Size 对应 nupkg）一起上传，最后读公开 feed 确认版本已登记。下载的产物、nupkg 和 feed 留在 `-out` 目录（默认 `publish-out/`，记得不要提交）便于复核。UpdateHub 会整份替换 feed，所以每次只写最新一条 Full 记录是预期行为。

## 首次部署（手动）

1. 部署在**日报系统所在的那台 Windows 服务器**上（2026-10-17 首次部署时定下；具体机器、IP、账号不写进仓库）。没有放在 UpdateHub 所在的 VM 上：那台 VM 在 Proxmox 的内部网桥后面，靠宿主机端口映射对外，80/443 没有映射；公司按 MAC 控制上网，VM 不能直接接入办公网。选机器时先确认它在办公网上**直接有 IP**，80/443 可用。
2. 把 taskboard 装到 `C:\taskboard\`（`taskboard.exe`、`config.toml`、`web\`、`nssm.exe`），`data_dir` 设为 D: 上的目录，用 NSSM 注册服务，只监听 `127.0.0.1` 上的一个空闲端口。服务要以有备份共享写权限的账号运行（见「备份与恢复」）；NSSM 的退出动作保持默认的 Restart，更新时进程退出后才会被拉起新版本。服务器连不上 GitHub 时，首个版本在开发机构建后传上去即可，之后的更新走 UpdateHub。
3. HTTPS 照搬日报系统的做法（同一台机器上已有 Caddy 时，只在 Caddyfile 里加一个站点块）：
   - 使用**带 alidns 插件**的 caddy.exe，官方版本不带这个插件；
   - 在阿里云 RAM 里给这个服务单独建一个子账号，只授予 `AliyunDNSFullAccess`；
   - 在阿里云 DNS 上把 `taskboard.urithub.com` 的 A 记录指向这台服务器的**内网 IP**；
   - 编写 Caddyfile：域名、`tls { dns alidns {...} }`、`reverse_proxy 127.0.0.1:<port>`。用 `icacls` 限制文件权限，因为里面有密钥；
   - 用 NSSM 注册 caddy 服务，启动参数用 `caddy run`，**不要用 `caddy start`**；在防火墙上开放 80 和 443 端口；
   - 已有 Caddy 时：先备份 Caddyfile，追加站点后用 `caddy validate` 校验，再 `caddy reload` 平滑生效（同机的其他站点不中断）；
   - 日志里出现 `certificate obtained successfully` 后，用 `curl` 验证返回 200。
4. 前提是服务器能访问公网，签发和续期证书要连 Let's Encrypt 和阿里云 API。公司软路由已有 `urithub.com` 的直连规则。连 Let's Encrypt 时通时断是常态，Caddy 会自动重试，首次签发可能要等几分钟。
5. 在 `config.toml` 里配置 `[server] public_url = "https://taskboard.urithub.com"`：API 回给用户的访客链接与 share_text 用绝对地址，网页上「复制链接」拿到即可发送；留空时回相对路径，前端复制时用当前页面的地址拼绝对（评审 P2-7）。
6. 创建管理员账号：`taskboard create-admin -username <登录名> -name <显示名> -password <密码>`。成员账号由管理员在网页（或 `POST /api/v1/members`）创建。
7. 配置备份目标，手动执行一次 `taskboard backup`，确认管理页显示备份成功。然后做一次恢复演练（见「备份与恢复」）。

## 备份与恢复

讨论过程见工单 #14「数据库与附件的定期备份和恢复」。

**防范范围**：MVP 防两类风险，一是逻辑错误（误改、误删、迁移出错），二是 VM 损坏。**NAS 损坏是已知的单点风险**：数据盘（D: 的 qcow2）、Proxmox 每月的整机备份和 taskboard 的备份都在同一台 NAS 上，这类风险交给 NAS 自身的冗余处理，平台不做异地副本。

**执行方式**（实现：`internal/ops`，#43）：

- taskboard 进程内置定时任务，用 `VACUUM INTO` 做 SQLite 在线备份，不直接复制库文件。更新前备份与定时备份用的是同一段代码（`ops.Backups.Preupdate`，#45 的更新流程调用；快照或完整性校验失败时更新中止）。可以用 `taskboard backup` 手动触发（服务运行中也可以：在线备份）。
- 每次备份的顺序是：导出数据库 → 同步附件镜像 → 对副本执行 `PRAGMA integrity_check` → 结果写库 → 清点既有副本并轮换。
- 附件做增量镜像，只增不删（附件写入后不再修改，工单也不能删除）：镜像在目标下的 `attachments\` 目录，按文件名对齐主存储，已存在且同大小的跳过。
- 每次备份还会顺手清点目标里既有的副本：发现损坏的（含人为损坏的）改名 `.bad`，不参与轮换。
- 只有写配置目标失败时才在本机 `data_dir\backups\` 写一份兜底副本；快照或完整性校验失败不兜底。每个定时备份时段或一次更新前备份只兜底一次，之后每小时重试只试目标。兜底不做附件镜像，本机目录与目标目录各自按原类别和保留数轮换；目标恢复后不回传兜底副本，靠下一份完整备份覆盖。

**频率与保留**：数据库每天 03:00（`daily_at`，北京时间）备份一次。保留 30 份每日备份、12 份每月备份（取每月 1 日那份）和 5 份更新前备份。附件只保留一份镜像，不分版本。文件名形如 `taskboard-YYYYMMDD-HHMMSS-<daily|monthly|preupdate>.db`。服务启动时若发现最近一个定时时刻还没备份过（比如那个时刻服务没在跑），会立刻补做一次；同一时刻只补一次。手动 `taskboard backup` 按当天的日期归入 daily（1 日归入 monthly），参与同类轮换。

**备份目标**：

- 在 NAS 上新建一个专用共享（如 `taskboard-backup`），用 UNC 路径配置，不要放在数据目录下，否则会和数据一起丢失。
- 映射的盘符对 Windows 服务不可见，所以要用 UNC 路径，并在 NSSM 中让服务以有该共享写权限的账号运行（服务属性 → 登录）。
- NAS（群晖）是工作组账号，Windows 服务没法直接以 NAS 账号运行。做法是：在服务器上建一个与 NAS 账号**同名同密码**的本地账号，服务以它运行，访问共享时 Windows 自动用同名同密码通过认证。这个本地账号要设成密码永不过期（`wmic useraccount where "name='<账号>'" set PasswordExpires=false`），否则按服务器策略到期后服务登录失败、备份中断。
- TOML 里用单引号写 UNC：`target = '\\nas01\taskboard-backup'`，开头**两个**反斜杠。只写一个时 Windows 会把它当成当前盘根下的本地目录，备份留在本机，所以加载配置时会直接报错。
- 用管理员身份手动执行 `taskboard backup`（如 SSH 登录后）时，管理员会话没有 NAS 凭据，要先在同一个会话里 `net use \\<nas>\<共享> <密码> /user:<nas>\<账号> /persistent:no`，做完再 `net use ... /delete`。定时备份和更新前备份在服务进程里执行，用的是服务账号，不需要这一步。
- 没有配置备份目标时，备份存到 `data_dir\backups\`，管理页会提示“备份未离开本机”。

```toml
[backup]
target         = '\\<nas>\taskboard-backup'
daily_at       = "03:00"
keep_daily     = 30
keep_monthly   = 12
keep_preupdate = 5
```

**校验与告警**：

- 每次备份后对副本执行 `PRAGMA integrity_check`。结果写入数据库，包括时间、大小、是否通过和附件新增文件数。
- 校验不通过的副本改名为 `.bad` 保留，不算成功，也不参与轮换，所以不会因此淘汰旧的好副本。
- 备份目标连不上时记一次失败，之后每小时重试一次，直到成功。备份失败不影响服务运行。
- 管理页显示最近一次成功写到目标的时间，超过 36 小时标红；也可以通过 `GET /api/v1/admin/backups` 查询（响应里的 `local_only` 为 true 时提示“备份未离开本机”，`target_unreachable` 为 true 时显示「备份目标不可达（原因）。最近一份只在本机：时间」横幅）。不发邮件告警。
- 系统通知发给全体在职管理员：连续失败第一次告警，之后每 24 小时提醒一次；恢复后发恢复通知，并把这段失败期间的告警标为已读。状态从备份记录和通知表推算，服务重启后不会重复首告警。更新前备份失败不告警，更新流程会把结果报给操作人。
- 更新前备份兜底成功时更新继续，更新结果用 `preupdate_backup_local: true` 标明「只在本机」。

**恢复**：`taskboard restore <备份文件> [-config <配置>]` 依次执行以下步骤：

1. 确认服务已停止：配置的端口上仍有进程在监听时拒绝执行（Windows 下对运行中的库做热替换不安全，文件也被占用），提示先 `nssm stop <服务名>`；
2. 校验指定的备份文件（`.bad` 副本和校验不通过的文件直接拒绝），把当前库连同 `-wal`/`-shm` 另存为 `taskboard.db.pre-restore-<时间戳>`（原库不丢，需要时可以换回去）；
3. 把库换成指定的备份（复制，备份文件本身保留），并执行迁移（旧备份配新二进制时自动升级表结构）；
4. 从备份文件旁的镜像目录（`<备份所在目录>\attachments\`）补齐主存储里缺的附件文件；镜像里也缺的列出来提醒，不中止；
5. 提示启动服务：NSSM 下执行 `nssm start <服务名>`（或直接 `taskboard serve`）。服务不在 restore 进程里启停，避免和 NSSM 的托管状态打架。

**演练**：首次上线时演练一次：在另一个目录用备份启动一个实例，核对工单数和附件能否打开，把结果记在下面。之后每次大版本迁移前再演练一次。

| 日期 | 版本 | 备份文件 | 结果 |
|---|---|---|---|
| 2026-10-17 | 后端 0.1.3 / 前端 0.1.0 | `taskboard-20261017-110104-preupdate.db`（NAS 上，更新前由服务进程自动备份） | 通过。恢复到一个**不存在的**目录（模拟新机器），另起端口启动实例：3 张工单（均「完成」关闭）、3 个成员、访客修订的说明（第 2 版）与时间线完整。当时库里还没有附件，附件镜像补回 0 个，**附件打开未覆盖**，有了真实附件后再演练一次。演练中发现「恢复到不存在的数据目录直接失败」，已修复（0.1.3）。 |
