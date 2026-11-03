# 附件直接文件操作盘点（只读调研）

## 1. 结论

- 附件文件本体只有一处「主存储路径」定义：`data_dir/attachments/<数字id>`，由 `attachments.Manager.Path()` 派生；DB 只存元数据，**不存路径、不存哈希**，只存 `size`（路径隐含 = id）。
- 读写入口集中在 `internal/attachments`（上传写、清理删）、`internal/httpapi/attachments_download.go`（下载 `os.ReadFile` 整文件进内存）、`internal/archive`（打包流式读、导入流式写、移除删）、`internal/ops`（备份镜像/恢复流式拷贝）。
- 上传（成员 / Agent token / 访客链接 / 匿名入口）全部 `io.ReadAll` 整文件进内存后 `os.WriteFile`；下载同样整文件进内存。
- 访客签名短地址（`guest_url.go`）**已是死代码**：#162 已取消，JSON/Markdown 都返回普通路径，访客靠 `tb_guest_device` cookie / `tbgd_` Bearer 鉴权（docs/api.md:1022-1028，测试 skip 于 attachments_download_test.go:183-184）。
- 下载有完整鉴权（与读工单一致）+ 被拒/成功访问记录；上传不写访问记录；备份/归档/存储统计走管理员鉴权，无访问记录。
- 测试大量直接断言本地目录文件存在/内容（`s.DataDir()/attachments/<id>`），是换存储后端时的主要测试改动面。

## 2. 清单

### 2.1 上传（成员、Agent、访客链接、匿名入口、草稿）

| 位置 | 文件操作 | 权限 / 访问记录 |
|---|---|---|
| `internal/attachments/attachments.go:172-179` | 启动时 `os.MkdirAll(data_dir/attachments)` | 无（服务装配） |
| `internal/attachments/attachments.go:184-188` | `Dir()`/`Path(id)` 只派生路径，供下载/删除/统计/备份/归档复用 | — |
| `internal/httpapi/attachments.go:27-92` | 成员/Agent(Bearer)/访客链接上传入口；`ParseMultipartForm`（8MB 内存缓冲，超出落 Go 临时文件）+ `io.ReadAll` **整文件进内存** | 有：`viewerFromRequest` + `tickets.ForViewer` + `capsOf.Upload`（关单访客 409）；幂等键包装；上传不写访问记录 |
| `internal/attachments/attachments.go:205-266` | `Upload`：先插 DB 行 → `os.WriteFile(Path(id))` → commit 失败 `os.Remove` 回滚 | 业务校验（文件名/单文件/整单配额，配额来自 DB `SUM(size)`） |
| `internal/httpapi/intake_uploads.go:55-112` | 匿名反馈入口上传；`io.ReadAll` 整文件进内存 | 活跃入口 + `Idempotency-Key` + 入口/IP 限流；无访问记录 |
| `internal/attachments/attachments.go:268-314` | `UploadToIntake`：同样先 DB 行再 `os.WriteFile`，commit 失败删除 | 入口待提交配额（DB 口径） |
| `internal/httpapi/attachments.go:63-91` | multipart `FormFile("file")` 读取（不落应用目录，超出缓冲由标准库临时文件承载） | — |
| `web-src/src/member/memberApi.ts:799-812`、`web-src/src/guest/upload.ts:14-33` | 前端构造 multipart（图片先压缩），无本地文件操作 | — |
| 草稿附件：上传仍走上面两条接口；`internal/drafts/drafts.go:210-211,608-617`、`internal/attachments/drafts.go:12-83` | 草稿保存/更新只改 `draft_id`（**纯 DB**），不碰文件 | 成员/Agent 草稿权限；上传者归属校验 |

### 2.2 下载与内联插图 URL

| 位置 | 文件操作 | 权限 / 访问记录 |
|---|---|---|
| `internal/httpapi/session.go:119` | 路由 `/api/v1/attachments/` → 下载 | — |
| `internal/httpapi/attachments.go:168-196` | 生成 `attachments[].url = /api/v1/attachments/att_N/<filename>`；成员与访客同路径 | 工单读取权限先行；待用附件不列入 |
| `internal/httpapi/drafts.go:267-276,348` | 草稿附件视图生成同款 URL | 草稿本人可见 |
| `internal/markdownview/ticket.go:87-113` | `?format=markdown` 附件小节直接嵌 URL | 随工单读取权限 |
| `internal/httpapi/attachments_download.go:20-111` | GET/HEAD：**`os.ReadFile(Path(id))` 整文件进内存**后写出；设 Content-Type/Disposition/nosniff/Length | 有：`viewerFromRequest`（会话 cookie / Agent token / 访客设备 cookie 或 `tbgd_` Bearer，viewer.go:19-92,117+）→ `tickets.ForViewer`；不可见记 `RecordDenied`；GET 成功记 `RecordDownload`（HEAD 不记）；pending 仅草稿本人可下（62-68）；URL 文件名必须与 DB 一致（69-73） |
| `internal/attachments/attachments.go:451-463` | `IsInline`：图片/PDF/txt/log inline，其余强制 attachment（含 HTML/SVG） | 下载安全策略核心 |
| `web-src/src/shared/markdown.tsx:80-104,170-217` | 前端插图：`AuthorizedImage` 用 `fetch(att.url)`（cookie 自动带）→ blob objectURL 渲染 `<img>`；`att:` 链接替换为 `att.url` | 依赖同源凭证；不做本地存储 |
| `internal/attachments/guest_url.go:73-118` | HMAC 短时效签名 URL 生成/校验（**当前无任何调用方**） | 原设计给 `<img>` 免 Bearer；#162 取消 |
| `internal/accessrecords/accessrecords.go:129-149` | `RecordDownload` 每次一条；`RecordGuestLinkDownload`（无调用方） | 访问记录写入 `access_records` 表 |

### 2.3 认领 / 删除 / 清理 / 墓碑

| 位置 | 文件操作 | 权限 / 访问记录 |
|---|---|---|
| `internal/attachments/claim.go:36-131` `ClaimTx`、`:134-205` `ClaimIntakeTx`、`:207-241` `ValidateBriefIDsTx` | 回复/说明/入口提交认领：**纯 DB 更新**（state/attached_to/ticket_seq），不碰文件 | 上传者归属 + 工单权限；调用点 `internal/tickets/writes.go:148,352`、`draft_writes_tx.go:33,170`、`intakes.go:499` |
| `internal/attachments/drafts.go:98-125` | `DeleteDraftTx` 删 DB 行并返回 id | 草稿属主 |
| `internal/attachments/drafts.go:127-131` + `internal/drafts/drafts.go:625-650` | 草稿丢弃：事务提交后 `os.Remove` 逐个删文件 | HTTP 层仅成员网页会话可 DELETE（httpapi/drafts.go:103-112）；无访问记录 |
| `internal/attachments/claim.go:247-308` | 24h 未引用 pending 清理：DB 删行成功后 `os.Remove` 文件（删失败留孤儿下轮重试）；`internal/server/server.go:226-238` 每 10 分钟调度 | 后台任务，无访问记录 |
| `internal/archive/package.go:780-814` | 归档生成时丢弃未发出草稿：DB 删行后 `atts.RemoveFiles`（`os.Remove`） | 管理员操作链路 |
| `internal/archive/remove.go:190-213,216-245` | 移除归档：事务提交后对每个附件 **主存储 + 备份镜像**两处 `os.Stat` + `os.Remove`，统计释放字节与失败数；包文件保留 | 管理员 + `saved:true` 确认；写 `archive_operations`（非 access_records） |
| `internal/archive/remove.go:29-105` | 墓碑（`archived_tickets`）为纯 DB 行，读取带成员/访客可见性判定，无文件操作 | 成员按快照/通知/管理员；访客仅原链接 |
| `internal/archive/package.go:286-346` | 归档包文件 `os.Open`/过期 `os.Remove`/`os.ReadDir` 清单 | 包下载见 2.5 |

### 2.4 存储统计

| 位置 | 文件操作 | 权限 |
|---|---|---|
| `internal/httpapi/storage.go:12-58` | `/admin/storage` 组装 | 仅管理员网页会话；无访问记录 |
| `internal/storage/storage.go:61-75` | 汇总三类占用 | — |
| `internal/storage/storage.go:174-215` `DirBytes` | `filepath.WalkDir` **递归列举**附件目录/备份目录并累计文件 size（互斥嵌套目录，跳过符号链接） | 目录级真实占用（含孤儿文件） |
| `internal/storage/storage.go:151-172` | 按工单 `SUM(size)`（DB 口径，含待用附件） | 用于可归档月份 |
| `internal/storage/storage.go:217-227` | `os.Stat` 三个 DB 文件 | — |

### 2.5 归档导出打包 / 导入恢复

| 位置 | 文件操作 | 权限 |
|---|---|---|
| `internal/archive/package.go:107-225` | 生成 zip：`os.MkdirAll`+`CreateTemp` 暂存 → `os.Rename` 原子落 `data_dir/archive-packages/`，失败 `os.Remove` | 管理员；写 `archive_operations` |
| `internal/archive/package.go:581-604` + `:628-646` | 打包附件：`os.Open(attachmentsDir/<id>)` **流式**读入 zip；边拷边算 SHA-256，写入字节数必须等于 DB `size`，否则整体失败 | 管理员 |
| `internal/httpapi/archive.go:159-186` | 包下载：`os.Open` + `io.Copy` 流式响应 | 管理员网页会话 |
| `internal/httpapi/archive_import.go:95-140` + `internal/archive/archive.go:88-95` | 导入包上传：流式写到 `data_dir/archive-packages/.import-*.zip.tmp`（≤1GB），不进内存 | 管理员 |
| `internal/archive/import.go:145-195` | 导入：**先写附件文件再提交 DB 事务**，任一步失败 `removeImportFiles`（`os.Remove`）回滚已写文件 | 管理员 |
| `internal/archive/import.go:276-287,424-435` | 导入前对每个 zip 条目校验清单 SHA-256 与大小 | — |
| `internal/archive/import.go:930-980` | `writeImportAttachments`：`os.OpenFile(O_CREATE|O_TRUNC)` **流式**写 `<attachmentsDir>/<id>`，并按 zip 条目大小复核 | — |

### 2.6 备份镜像与恢复

| 位置 | 文件操作 | 权限 |
|---|---|---|
| `internal/ops/backup.go:474-509` `syncMirror` | `os.ReadDir` 主附件目录 → 目标 `attachments/` 下「存在且同大小则跳过」，否则 `copyFile`；**同步不删除**（孤儿保留） | 备份调度/管理员触发；结果写 `backup_records` |
| `internal/ops/backup.go:583-604` `copyFile` | `os.Open` → 写 `.part` → `os.Rename`（流式，不留半截文件） | — |
| `internal/ops/backup.go:230,301` | 两种备份路径（目标可达/本机 only）都会做镜像 | — |
| `internal/ops/restore.go:42-148` | 恢复：换 DB + 从备份文件旁 `attachments/` 镜像 `os.Stat`/`copyFile` 补齐主存储缺失附件；两边都缺只列入 `MissingAttachments` 不中止 | CLI（要求服务停止）；无访问记录 |
| `internal/ops/restore.go:151-168` | `SELECT id FROM attachments` 列出应有文件名（=id） | — |
| `internal/archive/remove.go:221-226` | 移除归档时同步删备份镜像副本 | 见 2.3 |

### 2.7 数据库表结构（附件相关字段）

| 位置 | 字段要点 |
|---|---|
| `internal/store/migrations/0010_attachments.sql:5-23` | `attachments`：id、ticket_seq、filename、**size**、state(pending/attached)、attached_to、uploader_member_id/agent_id/link_id、uploaded_at、claimed_at；**无路径列、无哈希列**；`timeline_events.attachment_ids` 存引用 JSON |
| `0024_intake_attachments.sql:10-37` | ticket_seq 改可空；新增 `uploader_intake_id`、`intake_upload_key` + 索引 |
| `0031_draft_attachment_reservations.sql:3-4` | 新增 `draft_id`（草稿占用，豁免 24h 清理） |
| `0019_access_records.sql:4-21` | 下载/被拒记录含 `attachment_id/ref/filename` 快照、actor 快照、action(view/download/denied) |
| `0036_create_drafts.sql:3-50` | 草稿表重建时暂存/恢复 `attachments.draft_id` |

### 2.8 现有测试依赖本地目录的部分

| 位置 | 依赖 |
|---|---|
| `internal/tbtest/harness.go:151-158` | 所有集成测试 dataDir=`t.TempDir()` |
| `internal/server/attachments_test.go:180-187` | 直接 `os.ReadFile(DataDir()/attachments/1)` 比对字节 |
| `internal/server/attachments_cleanup_test.go:90-93` | `os.Stat` 断言清理后文件删除/保留 |
| `internal/server/reply_draft_attachment_delete_test.go:28-41`、`reply_draft_attachments_test.go:47-50` | 草稿丢弃/替换后断言文件删除 |
| `internal/server/archive_remove_test.go:85-121` | 断言主存储 + 备份镜像两处删除、包文件保留 |
| `internal/server/archive_package_test.go:420,662,714,800` | 手工写附件文件制造「DB size 与实际不符」、ReadDir 检查包 |
| `internal/server/archive_import_test.go:191` | ReadDir 断言导入文件 |
| `internal/ops/backup_test.go:144-175`、`restore_test.go:54-66` | 直接写/删 attachments 文件驱动镜像与恢复 |
| `internal/storage/storage_test.go:23-52` | `DirBytes` 递归/排除语义 |
| `cmd/taskboard/backup_cli_test.go:261-264` | 遍历并清空 dataDir/attachments |

（验证：`go test ./internal/attachments ./internal/storage ./internal/ops ./internal/archive` 与 `go test ./internal/server` 全部通过，未改仓库文件。）

## 3. 不确定点

- 「访客签名地址已取消」依据代码调用搜索（`GuestURL/VerifyGuestURL/RecordGuestLinkDownload` 零调用方）、docs/api.md:1026 与测试 skip 注释；未查 git 历史（按任务约束）。
- multipart 超出 8MB 后落 Go 标准库临时文件，位置由 `os.TempDir()` 决定，属运行时行为推断，未实测落点。
- 归档包下载/管理操作是否需要计入 access_records：现有代码只写 `archive_operations`，属事实陈述；是否「应记」是设计判断。
- 前端仅审阅了附件 URL 渲染与上传调用，未运行前端构建（避免写仓库）。

## 4. 对存储后端设计的含义

- **接口面收敛点明确**：写（整字节）、删、流式读（打包/下载）、存在性+大小（备份/恢复/统计）、列举/目录字节数。下载是唯一需要「鉴权后转签名 URL」的路径，`Path()+os.ReadFile` 是替换点；上传两处 `WriteFile` 与删除四处 `Remove` 是写入面。
- **元数据缺口**：DB 无路径/哈希，`size` 是唯一完整性锚点（打包与镜像增量都只比大小）。引入 OSS 前建议补 `storage_backend`/`object_key`/`sha256`/`etag`，否则 302 后无法做内容校验与迁移对账。
- **顺序语义要保留**：上传是「DB 行→文件→commit，失败补偿删」；归档导入相反（文件→DB，失败删文件）；草稿/归档删除是「commit 后删文件」。对象存储的非原子性（跨服务无事务）需要显式补偿/对账任务。
- **302 签名下载与现有访问记录兼容**：鉴权仍在服务端，签发前写 `RecordDownload` 即可保持审计；但 Content-Disposition/nosniff/inline 白名单必须迁到 OSS 响应头参数（`response-content-disposition`/`response-content-type`），否则下载安全策略丢失。
- **本地目录耦合的旁路功能**：`DirBytes`（统计）、`syncMirror`（备份）、`restore`（补齐）、`archive remove`（删镜像）都按「目录里的普通文件=附件」假设工作；后端抽象需提供 `List/Stat/Delete` 或改为以 DB 为准的对账，否则 OSS 模式下这些功能会静默失真。
- **测试改造量大**：所有直接 `Stat/ReadFile(DataDir()/attachments/<id>)` 的断言应换成经存储接口或经下载 API 验证，可先行在测试注入 fake/本地后端保持行为等价。