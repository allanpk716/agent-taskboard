# 调研：附件之外 1M 出口带宽下的大流量

## 1. 结论

- SPA 单包无代码分割：首屏 JS 664KB + CSS 54KB + HTML ≈ **720KB 未压缩 ≈ 5.8s@125KB/s**；gzip 后 210KB≈1.7s。Go 服务端自身从不压缩；docs 推荐的宿主 Caddy 带 `encode zstd gzip`，但 `deploy/cloud` 无代理配置。
- 缓存头已到位：hash 资产 `immutable` 1 年，index.html no-cache→304；发版时每个活跃用户仍需重下 ~665KB（50 人 ≈33MB 集中在发版窗口）。
- **归档包下载是最大的可变出口**：≈所选附件总量之和（zip 对图片再压收益≈0），8.2s/MB（50MB≈6.8 分钟），且无 Range/断点续传。
- 备份不经 HTTP 下发（VACUUM INTO + 附件目录增量镜像到 UNC/本地）；桌面客户端安装包/更新**直连 GitHub**（exe 10MB），不占 ECS 出口，除非把更新源指回本站。
- Agent 文档 `/api.md` 112.7KB（raw 0.9s / gzip 0.33s），no-cache 无预压缩。
- 302 跟随：浏览器、桌面客户端（Go 默认 ≤10 跳，跨域剥 Authorization）都跟随；**curl 默认不跟随**，仓库所有示例 curl 均无 `-L`。

## 2. 清单

### 前端 SPA（构建产物实测：主工作区 `web/`，其 `web-src` 与本分支源码逐字节一致）

| 位置 | 做了什么 | 备注 |
|---|---|---|
| `web-src/vite.config.ts:6-7` | 仅 React 插件，无 compression 插件，产物 `../web` | 不生成 `.gz`/`.br` 预压缩文件 |
| `internal/webapp/static.go:181` | `http.ServeContent` 原样发文件 | Go 标准库不协商 gzip/br；自动支持 Range 与 304 |
| `internal/webapp/static.go:329-339` | `assets/`→`public,max-age=31536000,immutable`；其余 no-cache | hash 长缓存已到位；index.html 每次条件请求，很便宜 |
| `web-src/public/sw.js:1-10` | SW 故意不缓存任何请求 | 无缓存放大/陈旧问题 |
| 实测 `/Users/allan716/orca/agent-taskboard/web` | index.html 945B、CSS 54,393B、JS 664,321B（各含 icon/manifest 共 ~10KB 次要） | 首屏 raw **719.7KB≈5.8s**；gzip 210.6KB≈**1.7s**；brotli-5 195.6KB≈1.6s |
| `docs/deploy-and-release.md:176-190` | 推荐 Caddy `encode zstd gzip` 反代 | 照此部署首屏≈210KB；`deploy/cloud/docker-compose.yml` 直接 host 网络裸暴露 Go，无压缩 |

### 归档包导出/下载

| 位置 | 做了什么 | 备注 |
|---|---|---|
| `internal/archive/package.go:142,169-205,581-604` | `zip.NewWriter`（`zw.Create` 默认 Deflate）打包全部 JSON + 附件**原件**流 | 图片/已压缩附件几乎不缩；包≈附件总和 |
| `internal/archive/archive.go:86` | 暂存 `data_dir/archive-packages/` | 磁盘占用同包大小 |
| `internal/archive/package.go:17` | 包保留 7 天 | 期间可反复下载（重复出口流量） |
| `internal/httpapi/archive.go:159-185` | 管理员 GET 下载：200 + `io.Copy` | 无 Content-Length、无 Range/断点续传；**8.2s/MB**：50MB≈6.8min、500MB≈68min |
| `config.example.toml:23-25` | 单附件 5MB / 单工单 50MB 上限 | 包本身无独立上限，随所选工单数线性 |

### 备份

| 位置 | 做了什么 | 备注 |
|---|---|---|
| `internal/ops/backup.go:54,329-353,459-467` | `VACUUM INTO` 后复制到 `[backup].target`（UNC/本地） | **无 HTTP 下载端点**（`internal/httpapi/backups.go:17` 只回状态 JSON） |
| `internal/ops/backup.go:477-511` | 附件目录增量镜像到 `target/attachments` | target 是 NAS 时走内网；ECS 本地卷则不出公网 |

### 桌面客户端安装/更新

| 位置 | 做了什么 | 备注 |
|---|---|---|
| `web-src/src/member/MePage.tsx:188-191` | 「下载 Windows 客户端」直链 GitHub Release | 不占 ECS 出口；exe 实测 10,065,920B（client/v0.2.0 manifest）≈80s@1Mbps（GitHub 侧） |
| `internal/client/update.go:22,39-43` | 更新源默认 `github.com/...`，可用 `TASKBOARD_CLIENT_UPDATE_BASE` 覆盖 | 若指回本站即 ~10MB/次·人的出口流量 |
| `internal/client/update.go:63-99,117-135` | 每 6h 查 `latest.json`；用户确认后才下 manifest+exe；10min 超时 | 80s ≪ 超时，带宽不是问题 |

### 服务端自身更新（方向为入口，不入出口账）

| 位置 | 做了什么 | 备注 |
|---|---|---|
| `internal/updater/sources.go:127-171` | 服务端从 GitHub/UpdateHub 拉 latest/manifest/产物并原地替换 | 入口流量：web zip 198KB≈1.6s、server linux 二进制 14.6MB≈117s（按 1Mbps 口径） |
| `internal/httpapi/update.go:44-49` | 容器模式禁用在线更新，改为换镜像 | 云部署走镜像仓库拉取（入口） |

### Agent 说明书等 Markdown

| 位置 | 做了什么 | 备注 |
|---|---|---|
| `internal/agentdocs/agentdocs.go:22-31,36-46` | `/agent.md`、`/llms.txt`、`/api.md`、`/skills/.../SKILL.md` 动态渲染 | no-cache、无压缩；**/api.md 112,708B≈0.9s**（gzip 41.5KB≈0.33s）；agent.md 10.7KB≈0.09s |
| `internal/webapp/agent_routes.go:80-110` | `/t/`、`/g/` 的 Agent Markdown 指南 | ~1.2KB，忽略 |
| `internal/httpapi/notifications_stream.go:44-95,118` | SSE 每 25s 发 `": ping"` | ≈1MB/月/桌面客户端（含 TCP 开销），可忽略但连接长驻 |

### 附件下载（背景对照）

| 位置 | 做了什么 | 备注 |
|---|---|---|
| `internal/httpapi/attachments_download.go:19-110` | 校验权限 + 访问记录后 `os.ReadFile` 整文件返回；有 Content-Length、无 Range | 5MB=41s、50MB=6.8min；302 改造点在校验与记录之后 |

### 302 跟随性

| 客户端 | 是否跟随 | 依据/备注 |
|---|---|---|
| 浏览器（导航/`<img>`/fetch/XHR） | 跟随 | GET 302 通用行为；SPA 插图与下载不受影响 |
| 桌面客户端 | 跟随 | `internal/client/client.go:174` 未设 `CheckRedirect`，Go 默认 ≤10 跳；**跨域重定向自动剥 Authorization**（OSS 签名在 query，不受影响） |
| Agent 的 curl | **不跟随** | `internal/httpapi/handoff.go:19,56`、`internal/webapp/agent_routes.go:87-107`、skill.md 所有示例均无 `-L`；agent.md 未给附件下载示例，但照抄模式的 `curl <附件URL>` 会拿到空 body 的 302 |
| wget / PowerShell IWR / Python requests | 跟随 | 通用工具默认行为（未在仓库内验证） |

## 3. 不确定点

- 前端产物实测自主工作区已构建的 `web/`（`web-src` 源码与本分支 diff 仅 node_modules/test-results/tsbuildinfo）；未在本仓库重新构建以免写文件。gzip 为默认级别、brotli 为 q5，与 Caddy zstd 略有差异。
- 线上是否真的挂了 Caddy：docs 模板带 `encode zstd gzip`，但仓库不含生产 Caddyfile，`deploy/cloud` 也未配置；若裸 Go 直出则全部未压缩。此项取决于运维实际部署。
- 客户端 exe 尺寸取自公开 GitHub manifest（client/v0.2.0），未本地构建复验。
- 归档包体量未实测（依赖生产数据量）；Deflate 对 JSON 收益明显、对 JPEG/zip ≈0。
- 1Mbps≈125KB/s 为理论上限，扣除 TCP/TLS 开销实际约 100–110KB/s，所有时间估算偏乐观。

## 4. 对存储后端设计的含义

- 附件 302 化后，出口大头依次变为：**归档包下载**（随附件总量线性、无断点续传，应同样考虑 OSS 直下或导出清单）、SPA 首屏/发版（无代理压缩时 720KB/人·次）、`/api.md`。
- 归档包生成目前直接 `os.Open` 本地附件目录（`package.go:589`）；附件迁 OSS 后需回源下载或改为「导出清单 + OSS 前缀」，否则每次生成产生一次 OSS→ECS 回源与双份磁盘暂存。备份镜像 `syncMirror` 同理只认本地目录，OSS 后端需要新的备份语义（版本化/生命周期），不能沿用目录镜像。
- 302 到 OSS 时：签名必须放 query（客户端 Authorization 在跨域跳转中会被剥掉）；`Content-Disposition`/`Content-Type`/`X-Content-Type-Options` 需编进签名 URL 参数，否则文件名与 inline 行为丢失；权限判定与访问记录保持在 302 之前完成（现有代码路径顺序已满足）。
- curl 型 Agent 兼容：skill/handoff 文案需补 `-L`，或提供 `?redirect=0` 之类返回 JSON 目标地址的兜底；HEAD 请求也要一并处理。
- 若前端继续由 ECS 直出，至少给 `index.html`/`assets/` 与 `/api.md` 加 gzip（Caddy `encode` 或预压缩 .gz/.br）：首屏 720KB→210KB、api.md 112KB→41KB；hash 长缓存与 no-cache 策略已就绪，无需改动。