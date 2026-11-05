# 部署、发布与更新：实施参考

开发和部署时使用的具体约定。决策的理由见 `docs/adr/`：[0009](adr/0009-cloud-docker-deployment.md)（迁到云服务器 Docker）、[0010](adr/0010-cloud-release-and-deploy.md)（发布与部署）、[0011](adr/0011-desktop-client-standalone-distribution.md)（桌面客户端独立分发）。本仓库是公开的，**这里不写服务器 IP、账号和密钥**。

## 组成

| 部件 | 形态 |
|---|---|
| 后端 | Go 单二进制，镜像里是 `linux/amd64` 版本，只监听 `127.0.0.1:<port>` |
| 数据库 | SQLite，WAL 模式，放在数据目录下 |
| 附件 | 本地数据目录或阿里云 OSS 上的对象，数据库只记元数据 |
| 前端 | React + TypeScript + Vite 构建的静态文件，和后端打进同一个镜像，由后端同源提供 |
| 运行 | Docker 镜像 + host 网络，数据在宿主机数据卷，配置文件只读挂载，以 `taskboard` 用户（UID 65532）运行 |
| HTTPS | 宿主机 Caddy 整站反代到后端 |
| 桌面客户端 | 独立发布在 GitHub Release，成员自己下载，不经过服务端 |

生产只有云端容器这一种形态（ADR 0010）。原来的 Windows/NSSM 原地更新形态不再维护，它的部署步骤见本文件的 git 历史。

服务器上的部署目录（示例为 `/opt/taskboard/`）：

```
/opt/taskboard/
├── docker-compose.yml        # 由 deploy/cloud/docker-compose.yml 复制，image 写死版本号
├── config.toml               # 只在服务器上维护，属主 root:65532，权限 640
├── deploy.env                # 部署脚本读取的镜像仓库地址等，只在服务器上维护
├── oss.env                   # 仅 OSS 后端需要；root:root，权限 600
└── deploy.sh                 # 每次部署前从对应 tag 上传
```

数据卷 `taskboard-data` 挂在容器的 `/app/data`：

```
/app/data/
├── taskboard.db
├── guest-link-key.bin        # 访客链接加密密钥，见「备份与恢复」
├── attachments/
└── backups/                  # 备份目标，见「备份与恢复」
    └── attachments/          # 附件镜像
```

## API 约定（补充协议原型）

- 前缀为 `/api/v1`，只做增量修改：可以加字段、加接口，不删除、不改名。破坏性变更开 `/api/v2`，两版并存一段时间。
- 桌面客户端的版本不受管理员控制（ADR 0011），它用到的接口（会话、通知流、通知已读、客户端凭证）尤其要保持向后兼容：旧客户端必须照样能用。
- 所有会产生新内容的请求（回复、建单、反馈提交、附件上传）都接受 `Idempotency-Key` 请求头。服务端按 key 去重，key 重复时返回首次请求的结果；入口附件上传是唯一例外：key 必填，用来把同一份草稿的多个附件归属到之后的一次提交，不做单响应快照。
- `GET /api/v1/health` 返回 `server_version` 和 `web_version`。前后端合并发布后两者相同；CI 冒烟和部署脚本都用它核对版本。

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
daily_limit         = 200
```

- 图片由网页在上传前压缩：长边缩到 2048px，转为 JPEG（质量 85%），去掉 EXIF。本来就不到 1MB 的 PNG 截图保持原样。平台不保存原图。服务端只检查上限，不压缩。Agent 通过 API 上传图片前需要自行压缩。
- 插图在正文中随宽度缩放，最宽显示 800px，点击查看大图。图片、PDF、`.txt` 和 `.log` 可以在线预览（`inline`），其他格式一律返回 `Content-Disposition: attachment`。HTML、SVG 只能下载。所有附件响应都带 `X-Content-Type-Options: nosniff`。
- 附件地址使用与工单相同的凭证（成员登录态、访客链接密钥、Agent token）。访客链接撤销或过期后，通过它拿到的附件地址也随之失效。
- 正文中的 UNC 路径（`\\服务器\共享\...`）显示为带“复制路径”按钮的文本块，不做成链接。

讨论过程见工单 #25「附件和插图在网页上的显示与访问控制」和地图 #1 的 Notes。

## 附件存储后端与迁移

附件同一时刻只用一个后端：默认 `local`（数据目录下的普通文件），或 `oss`（阿里云 OSS）。数据库设置表记录实际后端；启动时发现它和 `[attachments] backend` 不一致会拒绝启动，避免只改配置却漏迁数据。OSS 的连接参数在 `[attachments.oss]`，AccessKey 只通过 `OSS_ACCESS_KEY_ID` / `OSS_ACCESS_KEY_SECRET` 环境变量注入。

切换后端必须停服执行：

```bash
taskboard migrate-attachments -to oss -config <配置文件>          # 本地迁到 OSS
taskboard migrate-attachments -to local -config <配置文件>        # OSS 迁回本地
```

容器部署先用 `docker compose stop taskboard` 停服，再用一次性容器执行同样命令，最后改配置并 `docker compose up -d --wait`。迁移命令不改配置文件；成功后按提示把 `[attachments] backend` 改成目标后端再启动。

迁移行为：

- 以库内标记判断源端，不支持从一套 OSS 直接迁到另一套 OSS；先迁回本地再调整参数。
- 遍历数据库里的所有附件记录（含待用附件），4 路并发复制，边复制边计算 CRC64，再和目标端比对；每处理 50 个文件输出一次进度。
- 目标端已有同名对象且大小、CRC64 都一致时跳过，因此中断后重跑就是续跑。源端数据始终保留不删。
- `-dry-run` 只输出数量、总大小和缺失清单，不写对象、不改标记；源端有对象但数据库没有记录的附件也会在报告里列出。
- 源端缺失或复制失败时不切换标记并以非零退出；确认缺失数据已无法找回后，可加 `-accept-missing` 按缺失清单切换。
- 迁回本地前会按附件总大小加 10% 余量检查数据目录所在分区的剩余空间，不足时在复制前拒绝。

### OSS 部署配置

生产配置：

```toml
[attachments]
backend = 'local'                 # 先保持 local；迁移完成后再改成 'oss'

[attachments.oss]
region            = 'cn-hangzhou'
bucket            = 'urithub-files'
prefix            = 'agent-taskboard/prod/' # 系统根前缀，不能为空
internal_endpoint = 'oss-cn-hangzhou-internal.aliyuncs.com'
signing_host      = ''                       # 空 = 桶默认外网域名
```

服务端 API 一律走 `internal_endpoint`，同地域内网不占 ECS 公网出口；浏览器拿到的签名地址使用桶默认外网域名，只有以后开自定义域名或传输加速时才改 `signing_host`。所有对象都在根前缀下：`<prefix>attachments/`、`<prefix>archive-packages/`、`<prefix>backups/`。

`/opt/taskboard/oss.env` 只放两行凭证，属主 `root:root`，权限 `600`；`deploy/cloud/docker-compose.yml` 里的 `env_file` 会在启动时把它注入 taskboard 容器。凭证不进 `config.toml`、数据库和备份：

```dotenv
OSS_ACCESS_KEY_ID=<为 taskboard 单独生成的 AK ID>
OSS_ACCESS_KEY_SECRET=<为 taskboard 单独生成的 AK Secret>
```

桶 `urithub-files` 的配置要求：

- 杭州、标准存储、LRS、私有，开启「阻止公共访问」和 SSE-OSS。
- 不开版本控制，不配 CORS，不配 Referer 防盗链。
- 生命周期只配两条，且只作用于本系统根前缀：未完成的分片上传 1 天后删除；`<prefix>archive-packages/` 下的对象 8 天后删除。
- 前缀桶策略：允许 taskboard 的 AK `HeadBucket` 和操作 `<prefix>*`，拒绝它访问根前缀外的对象。生产和演练用不同前缀，生产是 `agent-taskboard/prod/`，演练是 `agent-taskboard/rehearsal/`。这条策略是账号内的第二道护栏；程序自身也会拒绝空前缀和前缀外删除。

**已知 AK 风险**：当前无法创建专用 RAM 用户，只能用现有账号级权限的 RAM 用户为 taskboard 单独生成一把 AK。AK 一旦泄露，影响面可能覆盖整个账号的 OSS；桶策略只能约束这把 AK 从 OSS 授权层访问的路径。以后能建专用 RAM 用户时，换 AK 即可，程序和桶前缀不用改。

### 出口流量清单

| 流量 | 切换 OSS 后走哪条路 | 是否占用 ECS 出口 | 依据 |
|---|---|---|---|
| 附件、插图下载 | 302 → OSS 签名地址 | 否（仅几百字节 302） | #297 |
| 附件上传 | 经服务器入方向 → 内网写 OSS | 否（入方向） | #298 |
| 归档包、瘦身包 | OSS 后端 302；本地后端可续传 | OSS 后端否 | #300、#301 |
| 前端资源 | 服务器直出，Caddy zstd/gzip，hash 资源长缓存 | 是（发版后约 209KB/人） | #301 |
| `/api.md`、SSE | 服务器直出 | 是（量小） | #295 |
| 备份 | 写 NAS 与内网 OSS | 否 | #300 |
| 客户端安装包与更新 | GitHub Releases | 否 | #301 |

桌面客户端的 `TASKBOARD_CLIENT_UPDATE_BASE` 只能指向 GitHub Releases 或同样承担下载流量的镜像源，**不能配置成 taskboard 本站**。不设置时客户端默认使用 GitHub；如果企业内网必须镜像，镜像地址也必须直接提供 Release 文件，不能让本站代理 exe 下载。

### 分两次上线、演练与维护窗口

1. **先发代码，不改后端**：发布带 OSS 支持的镜像，`backend = 'local'` 至少平稳运行 3 天，并确认经过一次每日对账。这个阶段验证新版本本身，附件行为不变。
2. **切换前一两天演练**：在生产机上用最新备份另起独立实例。实例使用独立 Docker volume、只监听本机，并把 OSS 前缀改成 `agent-taskboard/rehearsal/`；先执行 `-to oss`，再执行 `-to local`，核对双向迁移输出。演练完成后停止实例，用 `ossutil rm -r oss://urithub-files/agent-taskboard/rehearsal/` 删除演练前缀，并清掉演练 volume。
3. **维护窗口**（下班后，约 15 分钟）：执行 `deploy/cloud/oss-cutover-checklist.sh`。脚本逐项确认后执行手动备份 → 停服 → `migrate-attachments -to oss -dry-run` → 正式迁移 → 提示把配置改成 `backend = 'oss'` → 启动。正式迁移必须满足失败数 0、缺失数 0，脚本不自动改 `config.toml`。

```bash
sudo /opt/taskboard/oss-cutover-checklist.sh \
  --base-url https://taskboard.example.com \
  --header 'Cookie: <管理员会话 Cookie>'
```

脚本会自动检查 health、附件 302 的 `Cache-Control: no-store`、HTML 附件强制下载、归档包签名地址、OSS 备份对象、Caddy zstd、桶默认域名 200、根前缀外签名地址 403。上传、插图、Lightbox、访客、统计和 `curl -L` 需要按提示人工确认。第二天再看每日对账结果，孤儿数应为 0。

**回滚**：

- dry-run 或正式迁移失败时数据库标记仍是 local：把配置保持 `backend = 'local'`，直接启动旧服务。
- 改成 OSS 后启动失败，优先检查 `oss.env`、桶策略和内网 endpoint；修不好就停服执行 `migrate-attachments -to local`，成功后把配置改回 local 再启动。
- 已经稳定运行几天后要回退，同样执行停服 → `-to local`（命令会先检查磁盘空间）→ 改配置 → 启动，并按清单重新验证。

### 非当前后端残留与清理

- 迁移到 OSS 成功后，本地 `/app/data/attachments/` 原样保留 30 天。删除前确认最近备份完整、OSS 侧抽样下载一致，再执行：

```bash
docker compose run --rm --no-deps --entrypoint /bin/sh taskboard \
  -c 'find /app/data/attachments -mindepth 1 -maxdepth 1 -type f -regextype posix-extended -regex ".*/[0-9]+" -delete'
```

- 迁回本地后，OSS 侧对象同样先保留 30 天。确认数据库、本地附件和备份可用后，先用 `ossutil ls -r oss://urithub-files/agent-taskboard/prod/` 核对清单，再删除整个系统根前缀。不要在桶上按空前缀清理，避免影响其他系统。
- 归档和瘦身包在 OSS 后端下的本地权威副本仍由程序按现有规则清理；不需要手工删除。

### 传输加速、计费与监控

- 传输加速是纯运维开关，本次不开。需要国际访问附件时，在 OSS 控制台开启传输加速，把 `signing_host` 改成加速域名（或把自定义域名 CNAME 到加速域名），重启 taskboard；`internal_endpoint` 继续使用杭州内网域名。费用按境外加速流量约 1.25 元/GB 计算，外网流出另计。切换后用真实附件从目标地区实测，并重新核对账单。
- ECS 公网带宽保持固定带宽，设计仍按 1M 出口预算。按流量计费只作为备选：它能避免固定峰值限制，但费用随附件和页面流量放大，切换前必须同时在费用中心设置预算和流量告警，并评估对同机其他服务的影响。
- 运维与费用监控是本 spec 的已知缺口：代码不做 OSS 费用告警。上线前只在阿里云控制台配置 OSS 费用、外网流出流量和账户余额告警；对象存储异常如何接入系统通知留作后续工单。

真实 OSS 冒烟任务在 `.github/workflows/oss-smoke.yml`，只有 `workflow_dispatch` 手动触发。所需 secret 任一缺失时任务输出警告并成功跳过；全部配置后才运行带 `oss_smoke` build tag 的 Go 测试，覆盖桶探测、上传、签名下载、删除、前缀外 403 和前缀外删除被拒。默认 `go test ./...` 不编译该测试。

## 前端：访客输入不丢失

- 草稿以“访客链接 + 工单”为键，实时写入 `localStorage`，覆盖回复、说明和反馈表单，也包括已上传附件的 `att:` 引用。
- 只有收到服务器的 2xx 响应后才清除草稿。
- 请求失败或连不上服务时，显示“服务更新中，内容已保存”，然后带同一个 `Idempotency-Key` 自动重试。部署重启容器的几秒钟停服就靠这一条兜底。
- 缓存策略：`index.html` 设为 `no-cache`；带哈希的资源文件长期缓存。

## 发布

仓库里的实现：`.github/workflows/release-server.yml` 和 `release-client.yml`（CI）；`cmd/release`（manifest、latest 和 check-exe 子命令，CI 与本地演练共用）；`scripts/docker-smoke.ps1`（本地镜像冒烟）。

### 平台：`server/vX.Y.Z`

前后端共用一个版本号，只打 `server/vX.Y.Z` tag（ADR 0010）。`web/v*` tag 和 `release-web` 流水线已停用。`release-server` 流水线依次执行：

1. `go vet`、`go test`；
2. 用 `APP_VERSION=X.Y.Z` 构建镜像：一次构建前端和后端，前端的 `version.json` 写入同一个版本号；
3. **冒烟门禁**：在构建机上用刚构建的镜像启动容器，检查：
   - `GET /` 返回 200 且是 HTML；
   - 一条前端路由（如 `/login`）返回 200 且是同一份 HTML；
   - 首页引用的一个静态资源返回 200；
   - `GET /api/v1/health` 的 `server_version` 和 `web_version` 都等于 `X.Y.Z`。

   任何一项失败，流水线就此停止，镜像不推送；
4. 推送冒烟验过的同一份镜像（不重新构建）：`ghcr.io/allanpk716/agent-taskboard:X.Y.Z` 和 `:latest`。阿里云 ACR 是可选的第二个仓库（暂缓开通，ADR 0010），配置了 GitHub Secrets（`ACR_REGISTRY`、`ACR_NAMESPACE`、`ACR_USERNAME`、`ACR_PASSWORD`）就同时推送 `X.Y.Z` 和 `latest`；
   未配置 `ACR_REGISTRY` 时跳过 ACR 并在流水线日志给出 warning，发布不因此失败；
5. 发布 Release（附后端二进制和 `manifest.json`），并读改写 `channel-stable` 的 `latest.json`，只更新 server 条目。

本地验证镜像（端口默认 8932，避开 8901/8911-8914）：

```powershell
.\scripts\docker-smoke.ps1
```

脚本构建带 `e2e_fixed_captcha` build tag 的本地测试镜像（验证码固定为 `TB123`），启动容器，执行和 CI 相同的首页、路由、静态资源、版本检查，再检查带验证码登录、进程非 root，并在重建容器后验证数据仍保留。发布镜像不带该 build tag，验证码仍是随机实现。脚本结束时会删除冒烟容器、数据卷和镜像。

### 桌面客户端：`client/vX.Y.Z`

客户端独立发布（ADR 0011）。`release-client` 流水线在 Ubuntu 上交叉构建 Windows/amd64 的 `taskboard-client.exe`，用 `release check-exe` 核对产物平台，生成 `manifest.json` 并发布 `client/vX.Y.Z` Release。之后它再做两件事：

- 把同一个 exe 以固定文件名 `taskboard-client.exe` 上传到 `channel-stable` Release（`--clobber`）。网页上的下载按钮指向这个固定地址，始终下载最新版；
- 读改写 `channel-stable` 的 `latest.json`，只更新 client 条目。客户端检查更新时读的就是它。

### `channel-stable` 与固定地址

固定 tag `channel-stable` 下的 Release 放着 `latest.json`（记录 server 和 client 各自的最新版本）和最新的 `taskboard-client.exe`。组件分开打 tag 后，`releases/latest` 只指向最近发布的那一个，所以不能依赖它。

客户端**不调用 `api.github.com`**，只使用固定地址：

- `https://github.com/<repo>/releases/download/channel-stable/latest.json`
- `https://github.com/<repo>/releases/download/<tag>/<file>`

下载的产物都按对应 Release 的 `manifest.json` 里的 SHA256 校验。

CI 更新 `latest.json` 的方式是读改写：下载现有文件，用 `release latest` 只替换本组件的条目（另一组件原样保留），再 `--clobber` 上传。server 和 client 同时打 tag 的极端情况下，两个工作流可能同时尝试创建 `channel-stable` Release，失败的那个重跑一次即可。

### 演练（不碰 GitHub）

```bash
# latest.json：先发 server 再发 client，互不覆盖
go run ./cmd/release latest -component server -version 0.1.0 -tag server/v0.1.0 -repo OWNER/REPO -file latest.json
go run ./cmd/release latest -component client -version 0.1.0 -tag client/v0.1.0 -repo OWNER/REPO -file latest.json

# 桌面客户端：显式交叉构建 Windows/amd64
mkdir -p dist
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
  -ldflags='-s -w -H=windowsgui -X github.com/allanpk716/agent-taskboard/internal/version.Client=0.1.0' \
  -o dist/taskboard-client.exe ./cmd/taskboard-client
go run ./cmd/release check-exe -goos windows -goarch amd64 dist/taskboard-client.exe
go run ./cmd/release manifest -component client -version 0.1.0 -dir dist

# 平台镜像
docker build --build-arg APP_VERSION=0.0.0-local -t taskboard:local .
```

## 部署

### 首次部署

1. 在服务器上建部署目录，把 `deploy/cloud/docker-compose.yml` 复制进去，`image` 改成镜像仓库地址加版本号（目前是 `ghcr.io/allanpk716/agent-taskboard:X.Y.Z`，启用 ACR 后换成 ACR 的 VPC 地址）；
2. 从 `config.container.example.toml` 复制出 `config.toml`，改 `public_url`。缺少 `public_url` 时服务拒绝启动，避免对外链接依赖可被伪造的请求头。端口只由 `config.toml` 的 `port` 决定；
3. 写 `deploy.env`（格式见 `deploy/cloud/deploy.env.example`）；
4. 启动并创建管理员（GHCR 镜像是公开的，不用登录；启用 ACR 后先 `docker login`）：

```bash
docker compose up -d --wait
docker compose exec taskboard /app/taskboard create-admin \
  -config /app/config/config.toml -username <登录名> -password <初始密码> -name <显示名>
curl -fsS http://127.0.0.1:8080/api/v1/health
```

镜像 `HEALTHCHECK` 使用 `taskboard healthcheck -config /app/config/config.toml`，子命令读取配置里的 `port` 后请求本机 health 接口；端口不再来自环境变量。

5. 配置宿主机 Caddy（见下），确认公网地址能打开；
6. 手动执行一次备份并做恢复演练（见「备份与恢复」）。

镜像通过 `TASKBOARD_DEPLOY=container` 进入容器模式，该环境变量优先于配置文件里的 `[server] deploy`。管理页的在线更新按钮在容器模式下只提示「容器部署，请升级镜像」。

### 宿主机 Caddy

整站反代到后端。仓库示例是 `deploy/cloud/Caddyfile.example`，按生产域名复制到宿主机 Caddy 后先执行 `caddy validate`。`encode zstd gzip` 是**必配项**：OSS 只卸载附件和归档包流量，前端静态资源仍从这台服务器直出，不压缩会重新挤占 1M 出口预算。

转发头必须**覆盖**客户端自带的值，不能追加，否则请求方可以伪造来源 IP，影响登录限流和失败锁定的判定：

```caddyfile
taskboard.example.com {
    encode zstd gzip
    reverse_proxy 127.0.0.1:8080 {
        header_up X-Forwarded-For {remote_host}
        header_up X-Forwarded-Proto {scheme}
        header_up X-Real-IP {remote_host}
    }
}
```

改 Caddyfile 前先备份，改完用 `caddy validate` 校验，再 `caddy reload` 平滑生效，同机的其他站点不中断。

### 升级：一条命令

先把对应 tag 的部署脚本上传到服务器，再执行它：

```bash
# 开发机：取 tag 里的脚本，不用工作区里可能改过的版本
git show server/vX.Y.Z:deploy/cloud/deploy.sh > deploy.sh
# 上传到服务器的部署目录后，在服务器上：
sudo /opt/taskboard/deploy.sh X.Y.Z
```

`deploy.sh` 依次执行：

1. 读出 compose 里当前的版本号，作为回滚目标；
2. 读出 `[attachments] backend`；如果已是 `oss`，先确认 `oss.env` 存在；
3. 拉取新版本镜像；拉取失败就停止，不动线上。OSS 后端还会用新镜像执行一次 `migrate-attachments -to capability-probe` 能力探测：只有新 CLI 给出预期的 `-to` 参数错误才继续，避免写死版本号后漏掉 cherry-pick 或私有构建；
4. 在运行中的容器里执行 `taskboard backup -preupdate`，得到这次升级前备份的文件名；备份失败就停止，不动线上；
5. 把 compose 的 `image` 改成新版本，`docker compose up -d --wait`；
6. 经 `config.toml` 里的 `public_url` 核对：health 的 `server_version`、`web_version` 都等于新版本，首页返回 200 且是 HTML；
7. 第 5、6 步任何一步失败，就**自动回滚**：停掉容器，compose 切回旧版本，用旧镜像执行 `taskboard restore` 恢复第 4 步的备份，再启动并按旧版本核对一遍。回滚后以非零退出码结束，并打印失败原因。

脚本只改 compose 的 `image` 一行，不改 `config.toml` 和数据卷里的其他内容。自动回滚会丢掉升级开始以后新写入的数据，这段时间通常只有几分钟。

### 发版流程

发版 = 合并 → 打 `server/vX.Y.Z` tag → 等 CI 冒烟并推送镜像 → 执行部署脚本 → 用公网地址核对版本和首页。CI 失败或部署自动回滚时就停下排查，不要手动绕过。

## 桌面客户端分发

- **下载**：成员在网页「我的」页面点下载，拿到 `channel-stable` 下的最新 `taskboard-client.exe`。exe 免安装，首次运行时把自己装到用户目录，不需要管理员权限。它没有签名，首次运行要点「仍要运行」。
- **平台地址**：解析顺序为 `-server` 参数 > `TASKBOARD_SERVER_URL` 环境变量 > HKCU 里保存的地址 > 弹窗让成员填写。填写后先探测 `<地址>/api/v1/health`，确认是 taskboard 平台后才保存，然后走网页确认签发客户端凭证的流程。托盘菜单可以修改地址，改了以后原凭证作废，按新地址重新连接。
- **更新**：客户端定期读 `channel-stable/latest.json` 的 client 条目。有新版本时托盘弹出提示，成员点了才下载对应 `client/vX.Y.Z` 的 exe，按 `manifest.json` 的 SHA256 和大小校验后替换自己并重启。下载失败只提示，不影响通知。
- 0.1.4 及更早的客户端还会去服务端查更新，现在查不到，就停在原版本，通知照常可用。需要成员手动下载新版覆盖一次。

## 备份与恢复

讨论过程见工单 #14「数据库与附件的定期备份和恢复」。

**防范范围**：防范逻辑错误（误改、误删、迁移出错）。本地后端的备份目标目前在数据卷内，和主库在同一台云服务器上，**不防范服务器整机损坏**；每日磁盘快照是临时补救。OSS 后端下，库副本和访客链接密钥会在每次备份后上传一份到同配置的 OSS 根前缀下，作为异地备份；附件本体就在 OSS 上，附件镜像仍留在备份目标。

**执行方式**（实现：`internal/ops`，#43）：

- taskboard 进程内置定时任务，用 `VACUUM INTO` 做 SQLite 在线备份，不直接复制库文件。更新前备份与定时备份用的是同一段代码（`ops.Backups.Preupdate`）。可以用 `taskboard backup` 手动触发，服务运行中也可以执行；加 `-preupdate` 时按更新前备份归类，并输出备份文件名，供部署脚本回滚时使用。
- 每次备份的顺序是：导出数据库 → 同步附件镜像 → 复制访客链接加密密钥 → 对副本执行 `PRAGMA integrity_check` → OSS 后端下上传库副本和密钥 → 结果写库 → 清点既有副本并轮换。
- 附件做增量镜像，同步本身不删除，且以数据库里的附件行为准（含待用附件）：
  镜像在目标下的 `attachments/` 目录，按文件名对齐主存储，已存在且同大小的跳过。
  归档移除是唯一会从镜像删除附件的场景：它在数据库事务提交并写墓碑后，删除这些
  工单的附件主文件与镜像；个别文件删除失败只记录告警日志，不把已成功的移除事务
  判为失败。暂存区里的归档包仍保留原 7 天有效期，到期后由清理任务删除。
- 访客链接加密密钥 `guest-link-key.bin` 随每次备份复制到备份目录（与库副本放在一起，不在轮换范围内，始终保留最新一份）。本机兜底副本同样带上这个文件；OSS 后端下还会上传到 `<prefix>backups/guest-link-key.bin`。附件镜像不上传。
- 每次备份还会顺手清点目标里既有的副本：发现损坏的（含人为损坏的）改名 `.bad`，不参与轮换。
- OSS 后端下，本地/备份目标轮换删掉某个库副本时，会同时删除 OSS 上的同名对象；删除失败只写日志，下一轮备份会按成功备份记录再补删。
- 只有写配置目标失败时才在本机 `data_dir/backups/` 写一份兜底副本；快照或完整性校验失败不兜底。

**频率与保留**：数据库每天 03:00（`daily_at`，北京时间）备份一次。保留 30 份每日备份、12 份每月备份（取每月 1 日那份）和 5 份更新前备份。附件只保留一份镜像，不分版本。文件名形如 `taskboard-YYYYMMDD-HHMMSS-<daily|monthly|preupdate>.db`。服务启动时若发现最近一个定时时刻还没备份过，会立刻补做一次。手动 `taskboard backup` 按当天的日期归入 daily（1 日归入 monthly），参与同类轮换。

归档移除只影响当前数据库和当前附件镜像；移除前生成的旧数据库备份里仍会包含这些
工单、访客链接与附件记录。这是备份恢复语义的自然结果，不需要手工清理；这些旧备份
会按上面的保留策略轮换，到期后自然过期。

**访客链接加密密钥文件（`guest-link-key.bin`）**：访客链接的密钥在数据库里用这个服务端密钥做 AEAD 加密保存（决策见 ADR 0012）；链接鉴权仍然只查数据库里的哈希，不依赖这个文件。文件由服务首次启动时在 `data_dir` 下自动生成，没有配置项，不要手工编辑。文件丢失或读不出来时服务照常启动：所有已存的链接密文都按**老链接**处理——访客手里的链接照常打开，但列表里 `share_text_available` 为假，`share-text` 返回 409，成员对每条链接「换新地址并复制」后就恢复可复制。内容损坏的文件会改名成 `guest-link-key.bin.corrupt-<时间戳>` 留存（不删除），然后生成新密钥照常使用。只有数据目录写不进去、新密钥也生成不了时，才进入无密钥降级模式：单建访客链接、发链接请人填写和反馈入口提交都照常创建老链接（不保存加密列），`share-text` 返回 409 并提示管理员检查服务端加密密钥，只有 `rotate-key` 返回 503 `guest_link_cipher_unavailable`；修好目录权限并重启即恢复。

```toml
[backup]
target         = '/app/data/backups'
daily_at       = "03:00"
keep_daily     = 30
keep_monthly   = 12
keep_preupdate = 5
```

**校验与告警**：

- 每次备份后对副本执行 `PRAGMA integrity_check`。结果写入数据库，包括时间、大小、是否通过和附件新增文件数。
- 校验不通过的副本改名为 `.bad` 保留，不算成功，也不参与轮换。
- 备份目标连不上时记一次失败，之后每小时重试一次，直到成功。备份失败不影响服务运行。
- 管理页显示最近一次成功写到目标的时间，超过 36 小时标红；也可以通过 `GET /api/v1/admin/backups` 查询。不发邮件告警。
- 系统通知发给全体在职管理员：连续失败第一次告警，之后每 24 小时提醒一次；恢复后发恢复通知，并把这段失败期间的告警标为已读。

**恢复**：`taskboard restore <备份文件> -config <配置>` 要求服务已停止（配置的端口上仍有进程在监听时拒绝执行）。容器部署下先停容器，再用一次性容器执行：

```bash
docker compose stop taskboard
docker compose run --rm --no-deps taskboard restore /app/data/backups/<备份文件> -config /app/config/config.toml
docker compose up -d --wait
```

restore 依次：校验备份文件（`.bad` 和校验不通过的直接拒绝）；把当前库连同 `-wal`/`-shm` 另存为 `taskboard.db.pre-restore-<时间戳>`；把库换成备份并执行迁移；备份目录里有 `guest-link-key.bin` 时还原它（当前密钥先另存为 `guest-link-key.bin.pre-restore-<时间戳>`），恢复后的链接才能继续直接复制转发文案；从备份旁的 `attachments/` 镜像补齐主存储缺的附件，镜像里也缺的列出来提醒，不中止。数据库迁移只能前进，所以恢复时要用**与备份同版本或更新**的镜像执行。

恢复完成前会把库里的 `attachments_backend` 标记改写成当前配置的后端（OSS 时连同 bucket 和根前缀）。当前后端缺的附件先从备份镜像补；镜像里也缺、而当前后端也没有的附件会列入缺失清单。

OSS 后端下可以用异地备份直接恢复：

```bash
# 打印某个库备份 1 小时有效的签名下载地址
taskboard backup-url <备份名> -config /app/config/config.toml

# 先把库副本和密钥下载到 data_dir/backups/，再走同一个恢复流程
taskboard restore -from-oss <备份名> -config /app/config/config.toml

# 本机库还能打开时，latest 表示备份记录里最近一份成功写到目标的副本；
# 本机库也已经损坏时，请用具体备份名（可从备份告警或控制台对象列表取得）。
taskboard restore -from-oss latest -config /app/config/config.toml
```

用不含密钥文件的旧备份恢复时，`restore` 会明确提示；恢复后所有访客链接按老链接处理：链接照常可用，但要先「换新地址并复制」才能再次复制转发文案。

**演练**：首次上线时演练一次：在另一个目录用备份启动一个实例，核对工单数和附件能否打开，把结果记在下面。之后每次大版本迁移前再演练一次。

| 日期 | 版本 | 备份文件 | 结果 |
|---|---|---|---|
| 2026-10-17 | 后端 0.1.3 / 前端 0.1.0 | `taskboard-20261017-110104-preupdate.db`（NAS 上，更新前由服务进程自动备份） | 通过。恢复到一个**不存在的**目录（模拟新机器），另起端口启动实例：3 张工单（均「完成」关闭）、3 个成员、访客修订的说明（第 2 版）与时间线完整。当时库里还没有附件，附件镜像补回 0 个，**附件打开未覆盖**，有了真实附件后再演练一次。演练中发现「恢复到不存在的数据目录直接失败」，已修复（0.1.3）。 |
| 2026-10-20 | 后端 0.9.1 / 前端 0.9.0 | `taskboard-20261020-203334-preupdate.db`（NAS 上，2026-10-20 20:33 更新前自动备份） | 通过，**附件打开已覆盖**（#179）。直接从 NAS 上的备份和附件镜像 `taskboard restore` 到不存在的临时目录，临时配置不设备份目标和更新源（防止写 NAS 或自更新），另起端口启动：12 张工单（3 开 9 关），镜像补回附件 4 个，与生产 `data_dir\attachments` 的文件数一致。抽查 4 个附件（即全部附件）：图片 1 个（访客上传的 PNG）、普通文件 3 个（成员上传的日志、访客上传的日志、Agent 上传的 zip）。经临时实例的下载接口取回，都返回 200，Content-Type 正确，SHA-256 与生产磁盘上的文件逐一相同，zip 校验无错误。生产的 `data_dir` 只读、只算哈希，没有经生产接口下载，所以没有产生下载审计记录。演练后停掉临时实例，删掉临时目录，生产全程健康。 |
