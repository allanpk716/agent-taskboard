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

建议的目录结构：

```
<安装目录>/
├── taskboard.exe
├── taskboard.exe.prev        # 上一版，供 rollback 使用
├── config.toml
├── web/                      # 当前前端（切换目标）
├── web-<version>/            # 已解压的各版前端
└── data/
    ├── taskboard.db
    ├── attachments/
    └── backups/              # 更新前自动备份的数据库
```

## API 约定（补充协议原型）

- 前缀为 `/api/v1`，只做增量修改：可以加字段、加接口，不删除、不改名。破坏性变更开 `/api/v2`，两版并存一段时间。
- 所有会产生新内容的请求（回复、建单、反馈提交、附件上传）都接受 `Idempotency-Key` 请求头。服务端按 key 去重，key 重复时返回首次请求的结果。
- 管理员专用的更新接口：检查更新、执行更新，供管理页按钮和管理员的 Agent 调用。

## 前端：访客输入不丢失

- 草稿以“访客链接 + 工单”为键，实时写入 `localStorage`，覆盖回复、说明和反馈表单，也包括已上传附件的 `att:` 引用。
- 只有收到服务器的 2xx 响应后才清除草稿。
- 请求失败或连不上服务时，显示“服务更新中，内容已保存”，然后带同一个 `Idempotency-Key` 自动重试。
- 缓存策略：`index.html` 设为 `no-cache`；带哈希的资源文件长期缓存。

## 更新

只由管理员手动触发，有三个入口：管理页按钮、`taskboard update` 命令、管理 API，背后是同一套逻辑。服务不会自动安装更新，只在管理页提示有新版本。

1. 按配置的更新源顺序检查，用第一个连得上的源，并记录实际用的是哪个。
2. 前端和后端分别对比当前版本和最新版本。前端的 manifest 写着它需要的最低后端版本 `min_server`；后端版本不够时，先升级后端。
3. **后端**：
   1. 下载新版本，校验哈希；
   2. 备份数据库到 `data/backups/`；
   3. 把正在运行的 exe 改名为 `taskboard.exe.prev`，放入新的 exe；
   4. 进程退出，由 NSSM 拉起新版本；
   5. 新版本启动时执行迁移，再做健康检查。
4. **前端**：下载新版本，校验哈希，解压到 `web-<version>/`，再原子地切换 `web/`，服务不用重启。
5. **回滚**：在服务器上执行 `taskboard rollback`，换回 `.prev` 版本和更新前的数据库备份。新版本起不来时网页上的按钮不可用，只能这样回滚。

配置示意：

```toml
[update]
sources = ["updatehub", "github"]   # 按顺序回退

[update.updatehub]
base_url = "https://<updatehub 地址>"
channel  = "stable"

[update.github]
repo   = "allanpk716/agent-taskboard"
mirror = ["https://<镜像前缀>/"]    # 按顺序尝试，最后直连
proxy  = ""                         # http:// 或 socks5://；为空时读取 HTTPS_PROXY 环境变量
```

## 发布

### GitHub Release（唯一构建来源）

- 后端打 `server/vX.Y.Z` tag，前端打 `web/vX.Y.Z` tag，各自触发 GitHub Actions 构建并发布 Release。
- 每个 Release 都附带构建产物和 `manifest.json`：

  ```json
  { "component": "web", "version": "1.4.0", "min_server": "1.2.0",
    "files": [{ "name": "taskboard-web-1.4.0.zip", "sha256": "..." }] }
  ```

- 固定 tag `channel-stable` 下的 Release 里放一个 `latest.json`，记录前后端各自的最新版本。CI 每次发布后自动更新它。前后端分开打 tag 后，`releases/latest` 只指向最近发布的那一个，所以不能依赖它。
- 客户端**不调用 `api.github.com`**，只使用固定地址：
  - `https://github.com/<repo>/releases/download/channel-stable/latest.json`
  - `https://github.com/<repo>/releases/download/<tag>/<file>`

  因此可以直接在地址前面加镜像前缀。不管经由哪个镜像下载，都按 `manifest.json` 里的 SHA256 校验。

### UpdateHub（内网，转发同一份产物）

UpdateHub 是部门已有的内网更新服务，实现了 Velopack 协议。GitHub Actions 的构建机访问不到内网，所以由开发机上的 `publish-updatehub` 脚本完成：下载指定 Release 的产物，包装成 nupkg，上传到 UpdateHub。

- 应用名：`agent-taskboard-server`、`agent-taskboard-web`；通道：`stable`。
- 第一次上传时自动建应用。appId 只能用字母、数字和连字符；feed 里的 `PackageId` 必须和 appId 一致，不区分大小写。
- 上传：`POST /api/apps/{appId}/channels/{channel}/releases`，带 `Authorization: Bearer <token>`，multipart 一次上传 nupkg 和 `releases.win.json`，可附 `notes`，单次不超过 200MB。token 只放在开发机的环境变量里。
- 查询：`GET /{appId}/{channel}/releases.win.json`，不需要认证。返回格式为 `{"Assets":[{PackageId,Version,Type,FileName,SHA1,Size}]}`，再按 `FileName` 下载。
- 只有 `.nupkg` 能登记成版本。nupkg 就是一个 zip，里面加一个 `.nuspec`。我们自己生成，不依赖 .NET 的 `vpk` 工具。原始的 `manifest.json` 一起打进包里。
- 走这个源时，按 feed 里的 SHA1 校验，解包后再按 manifest 里的 SHA256 校验一次。

## 首次部署（手动）

1. 把 taskboard 安装到 UpdateHub 所在的 VM 上，用 NSSM 注册服务，监听 `127.0.0.1` 上的一个新端口。
2. HTTPS 照搬日报系统的做法：
   - 使用**带 alidns 插件**的 caddy.exe，官方版本不带这个插件；
   - 在阿里云 RAM 里给这个服务单独建一个子账号，只授予 `AliyunDNSFullAccess`；
   - 在阿里云 DNS 上把 `taskboard.urithub.com` 的 A 记录指向 VM 的**内网 IP**；
   - 编写 Caddyfile：域名、`tls { dns alidns {...} }`、`reverse_proxy 127.0.0.1:<port>`。用 `icacls` 限制文件权限，因为里面有密钥；
   - 用 NSSM 注册 caddy 服务，启动参数用 `caddy run`，**不要用 `caddy start`**；在防火墙上开放 80 和 443 端口；
   - 日志里出现 `certificate obtained successfully` 后，用 `curl` 验证返回 200。
3. 前提是 VM 能访问公网，签发和续期证书要连 Let's Encrypt 和阿里云 API。公司软路由已有 `urithub.com` 的直连规则。
4. 创建管理员账号。

## 尚未决定

备份的频率、存放位置、保留多少份，以及恢复演练怎么做，见工单「数据库与附件的定期备份和恢复」。
