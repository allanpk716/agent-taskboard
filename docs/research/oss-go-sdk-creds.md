# Go 接入阿里云 OSS 的调研结论

> 查阅日期：2026-11-03。本文只做调研，未修改仓库文件。

## 1. 结论

### SDK 选择

1. **新接入应选 `github.com/aliyun/alibabacloud-oss-go-sdk-v2`（OSS Go SDK V2），不建议选旧版 `aliyun-oss-go-sdk`。**
   - 官方 V1 文档明确推荐 V2，称其为“major architectural improvements”，并提示迁移到 V2。
   - 维护状态：V2 最新 release 为 `v1.6.1`（2026-10-16），仓库最近提交同日；旧版最新 release 为 `v3.0.2`（2023-12-28），仓库最近提交为 2024-07-05。旧版未在官方文档中标为立即停用，但明显不是当前重点。

2. **V2 覆盖本项目需要的全部对象操作。**
   - 流式上传：`PutObject.Body` 是 `io.Reader`，简单上传最大 5 GiB；`Uploader.UploadFrom` 可自动选择简单/分片上传，最大 48.8 TiB。
   - 流式下载：`GetObject` 返回 `io.ReadCloser`，支持 Range；`Downloader.DownloadFile` 支持分片并发、CRC64、进度、失败重连和断点续传。
   - 分片上传：提供 `InitiateMultipartUpload` / `UploadPart` / `CompleteMultipartUpload` / `AbortMultipartUpload` 等接口；分片最大 5 GiB，对象最大 48.8 TiB。
   - 预签名：`Client.Presign` 支持 GET/PUT/HEAD/分片相关请求；默认有效期 15 分钟，V4 签名最长 7 天，可为 GET 覆盖 `response-content-disposition`。
   - 列举：`ListObjectsV2` 支持前缀、分页、`startAfter`；V2 提供 `NewListObjectsV2Paginator` 自动翻页。
   - 批量删除：`DeleteMultipleObjects` 支持一次传入对象列表；OSS API 单次最多 1000 个对象。
   - 取大小：`HeadObject` 返回完整元数据；`GetObjectMeta` 更轻量，明确包含 `ContentLength`、ETag、LastModified 等。

### S3 兼容接口是否值得用

3. **`aws-sdk-go-v2` 指向 OSS 对本项目所需的核心 API 可行，但不建议作为第一实现。**
   - OSS 官方声明兼容 S3 的 Bucket/Object/Multipart API，包括 Get/Put/Head/List/DeleteObjects 和 Multipart 操作。
   - 限制不少：OSS 只支持虚拟主机风格请求；ACL、存储类型、ETag 语义与 S3 不完全一致；V4 签名还要求 `x-oss-content-sha256: UNSIGNED-PAYLOAD` 且不能用 chunked transfer，官方特别提示 Go v2 的 Manager 上传大文件需注意。
   - AWS SDK 默认凭证链不会自动理解阿里云 ECS 实例 RAM 角色，需要额外适配阿里云 credentials provider 或退回 AK/STS。
   - 为将来 MinIO 留口子有价值，但更稳妥做法是**在业务层定义很小的 AttachmentStore 接口**，当前用 OSS V2 实现，未来再增加 S3/MinIO adapter；不要为了让未来可能的多后端而牺牲当前 OSS 凭证、域名和签名细节。

### 凭证与权限

4. **ECS 实例 RAM 角色可用，且是本项目生产环境首选。**
   - OSS Go SDK V2 官方凭证文档支持 `ECSRAMRole`：底层是 STS Token，通过实例元数据自动刷新，不需要在应用中保存 AK。
   - ECS 与 OSS 同地域时，服务端 API 请求应使用内网 Endpoint，避免公网流量并降低延迟。

5. **RAM 子账号/RAM 角色最小权限可以限定到单个 bucket 和前缀。**
   - 对象读写删除类操作授权到 `acs:oss:*:*:<bucket>/attachments/*`。
   - `ListObjects`、`ListMultipartUploads` 的资源是 bucket 本身，需要单独在 bucket 资源上授权，并用 `Condition` 的 `oss:Prefix` 限制为 `attachments` / `attachments/*`。
   - 基本动作可收敛为：`oss:PutObject`、`oss:GetObject`、`oss:DeleteObject`、`oss:AbortMultipartUpload`、`oss:ListParts`、`oss:ListMultipartUploads`、`oss:ListObjects`。分片初始化、上传、完成在 OSS 权限模型中都归入 `oss:PutObject`。

6. **STS 临时凭证可用，但不是本方案首选。**
   - `AssumeRole` Token 最小 900 秒、默认 3600 秒，最大受角色 `MaxSessionDuration` 限制；会话 Policy 是角色权限与会话 Policy 的交集。
   - 服务端长期运行用 ECS RAM Role 已自动获得临时凭证；给浏览器直传时，短时效预签名 PUT 通常比下发 STS Token 更小、更可控。

7. **不要把 AccessKey 写入普通配置文件或仓库。**
   - 官方明确警告 AK 泄露风险，且静态凭证“仅用于测试目的”的硬编码方式不应进入生产。
   - 若暂时无法用 ECS RAM Role，应使用最小权限 RAM 用户，由部署系统/KMS/环境变量注入，可轮换、可回收；不要放在 `config.toml`、`.env`、容器镜像或备份中。

### CI / 本地测试

8. **没有一个维持得很好的官方 OSS 全兼容模拟器。**
   - 阿里云官方历史仓库 `aliyun/oss-emulator` 支持基本 Put/Get/List/Copy/Delete/部分 multipart 和 HeadObject/GetObjectMeta/DeleteMultipleObjects，但最后一次提交在 2017 年，且不支持 Abort/ListParts/ListMultipartUploads 等清理接口，未声明支持新 V4 签名。
   - MinIO 是 S3 API 模拟的好选择，但不是 OSS 原生 SDK 模拟器：OSS V2 默认使用 `OSS4-HMAC-SHA256`，MinIO 实现 AWS SigV4/S3 API，二者不是同一原生协议。
   - 推荐 CI 策略：业务逻辑用本地/in-memory AttachmentStore fake；OSS adapter 用可注入的自定义 HTTP client 做 SDK 层协议测试；真实 OSS 做少量带 secret 门禁的 smoke test。若未来实现 S3 adapter，则 MinIO 很适合作为该 adapter 的 CI 后端。

## 2. 依据（官方链接，查阅日期 2026-11-03）

- **OSS Go SDK V2**  
  https://help.aliyun.com/zh/oss/developer-reference/manual-for-go-sdk-v2  
  说明 Go 1.18+、配置、凭证、内网 Endpoint、自定义 HTTP client、默认 V4 签名。

- **Go SDK V1 到 V2 迁移指南**  
  https://help.aliyun.com/zh/oss/developer-reference/migration-guide-in-go  
  原文要点：V2 使用新仓库，简化认证、重试、错误处理，并提供 Paginator、传输管理器、File-like 操作。

- **OSS Go SDK V1**  
  https://help.aliyun.com/zh/oss/developer-reference/go-sdk-v1  
  官方建议使用新版 V2，并提供 V1 到 V2 迁移指南。

- **V2 Releases / 官方仓库**  
  https://github.com/aliyun/alibabacloud-oss-go-sdk-v2/releases  
  最新 `v1.6.1`，发布于 2026-10-16。

- **旧版 SDK 官方仓库**  
  https://github.com/aliyun/aliyun-oss-go-sdk  
  最新版本 `v3.0.2`；仓库元数据显示最近 release 时间为 2023-12-28。

- **上传文件（Go SDK V2）**  
  https://help.aliyun.com/zh/oss/developer-reference/upload-file-for-go-sdk-v2  
  `PutObject` body 为 `io.Reader`，最大 5 GiB；`Uploader.UploadFrom` 最大 48.8 TiB，自动简单/分片上传。

- **下载文件（Go SDK V2）**  
  https://help.aliyun.com/zh/oss/developer-reference/download-file-for-go-sdk-v2  
  `GetObject` 流式返回 `io.ReadCloser`；Downloader 支持分片下载、CRC64、断点续传等。

- **分片上传（Go SDK V2）**  
  https://help.aliyun.com/zh/oss/developer-reference/v2-multipart-upload  
  覆盖分片初始化、上传、完成、取消和分片列表接口。

- **使用预签名 URL 下载（Go SDK V2）**  
  https://help.aliyun.com/zh/oss/developer-reference/v2-presign-download  
  默认有效期 15 分钟，V4 最长 7 天；示例包含 `ResponseContentDisposition` 强制指定下载文件名。

- **列举文件（Go SDK V2）**  
  https://help.aliyun.com/zh/oss/developer-reference/v2-list-objects  
  `ListObjectsV2` 支持 prefix/maxKeys/startAfter，`ListObjectsV2Paginator` 自动翻页。

- **删除文件（Go SDK V2）**  
  https://help.aliyun.com/zh/oss/developer-reference/v2-delete-objects  
  `DeleteObject` 与 `DeleteMultipleObjects`；API 单次批量删除上限 1000 个对象。

- **获取文件元数据（Go SDK V2）**  
  https://help.aliyun.com/zh/oss/developer-reference/v2-query-object-metadata-versioning  
  `HeadObject` 获取完整元数据；`GetObjectMeta` 包含 `ContentLength` 等轻量元数据。

- **OSS 与 Amazon S3 的兼容性**  
  https://help.aliyun.com/zh/oss/developer-reference/compatibility-with-amazon-s3  
  列出兼容的 S3 API，同时说明只支持虚拟主机风格及 ACL、存储类型、ETag 等差异。

- **使用 AWS SDK 访问 OSS**  
  https://help.aliyun.com/zh/oss/developer-reference/use-aws-sdks-to-access-oss  
  说明 S3 兼容 Endpoint、阿里云凭证、V4 签名要求，以及 Go v2 Manager 大文件上传注意事项。

- **OSS Go SDK V2 配置访问凭证**  
  https://help.aliyun.com/zh/oss/developer-reference/configure-access-credentials-by-using-oss-sdk-for-go-v2  
  支持 RAM 用户 AK、STS、RAMRoleARN、ECSRAMRole、OIDC Role、CredentialsURI 等；明确静态 AK/硬编码风险。

- **基于 RAM Policy 控制 OSS 访问权限**  
  https://help.aliyun.com/zh/oss/user-guide/tutorial-use-ram-policies-to-control-access-to-oss  
  说明 OSS “目录”本质是 key 前缀；对象 Resource 可精确到 `bucket/Development/*`；列举需要在 bucket 资源上加 `oss:Prefix` 条件。

- **OSS RAM Policy 常见示例**  
  https://help.aliyun.com/zh/oss/common-examples-of-ram-policies  
  给出限定 bucket、前缀、动作的示例。

- **OSS 分片上传权限**  
  https://help.aliyun.com/zh/oss/user-guide/multipart-upload  
  官方表：Initiate/UploadPart/Complete 均需要 `oss:PutObject`；Abort 需要 `oss:AbortMultipartUpload`；ListParts/ListMultipartUploads 有对应 List 权限。

- **STS AssumeRole**  
  https://help.aliyun.com/zh/ram/api-assumerole  
  Token 最小 900 秒、默认 3600 秒；`Policy` 参数与角色权限取交集。

- **通过 Endpoint 和 Bucket 域名访问 OSS**  
  https://help.aliyun.com/zh/oss/user-guide/access-oss-via-bucket-domain-name  
  内网 Endpoint 适用于同地域阿里云产品，免流量费；并说明 2025-03-20 后部分新开通 OSS 用户在中国内地需要自定义域名访问数据 API。

- **官方历史 OSS Emulator**  
  https://github.com/aliyun/oss-emulator  
  官方仓库声明支持基础数据 API，但项目自 2017 年后未更新，接口范围有限。

- **MinIO S3 API Compatibility**  
  https://docs.min.io/aistor/developers/s3-api-compatibility  
  MinIO 支持 S3 Object/Multipart API，包括本项目核心操作，但这是 S3 兼容，不是 OSS 原生签名兼容。

## 3. 对本项目的含义

1. **SDK 路线：用 OSS Go SDK V2，同时在业务层留窄接口。**  
   当前 `attachments.Manager` 与本地路径、`os.ReadFile` 耦合。引入类似“保存、打开、删除、列举、签名下载 URL、取元数据”的存储接口后，本地实现仍是默认，OSS 只是一个可选 adapter。

2. **上传可继续由服务端鉴权和配额检查，再流式转发到 OSS。**  
   现有单文件默认 5 MB，用 `PutObject` 足够；若未来放大限制，再切 `Uploader`。上传时应显式设置 object key、Content-Type、Content-Disposition，避免依赖客户端浏览器行为。

3. **下载保持现有 API 路径，鉴权和访问记录完成后返回 302。**  
   不建议把 OSS 签名 URL 直接长期嵌在工单 JSON 中；否则链接在 TTL 内被转发后仍可访问，且访客链接撤销不能实时生效。302 前短时生成 URL，例如 1–5 分钟，并继续在 GET 时记录访问。

4. **预签名 URL 要覆盖下载语义。**  
   `GetObjectRequest.ResponseContentDisposition` 可强制 attachment/inline 和文件名；`response-content-type` 也可在 GetObject 中覆盖。危险类型继续强制 attachment。原响应中的 `X-Content-Type-Options: nosniff` 不是 GetObject 的标准覆盖参数，需要以 Content-Type/Content-Disposition 策略补足风险控制。

5. **需要区分“服务端 Endpoint”和“浏览器可访问 Endpoint”。**  
   ECS 上传、Head、删除应使用同地域内网 Endpoint；浏览器下载 302 必须是公网 Endpoint或已备案/配证书的自定义域名。若 SDK client 只配置内网 Endpoint，直接 Presign 得到的 URL 可能是浏览器不可达的内网域名，应使用两个签名/API 配置或专门的公开域名签名配置。

6. **1M ECS 带宽瓶颈只解决下载，不解决上传。**  
   302 后附件字节不再占 ECS 固定公网出口，但 OSS 外网流出流量仍按 OSS 计费。用户上传到后端仍经过 ECS 入口；若未来上传量明显增大，可另行评估浏览器预签名 PUT 直传，但这会改变当前两步上传、配额和认领流程。

7. **权限策略按 bucket + `attachments/` 前缀收敛。**  
   对象操作只授权 `bucket/attachments/*`；列举和 multipart 清理单独授权 bucket 资源并加 prefix 条件。不要授予 `AliyunOSSFullAccess`。

8. **失败清理必须纳入设计。**  
   分片上传失败或服务重启可能留下已计费碎片。应用应在错误路径 abort；同时配置生命周期清理未完成 multipart upload，批量删除 pending 附件时可分页调用 `DeleteMultipleObjects`（每批不超过 1000）。

9. **审计语义变化要写清楚。**  
   302 前的访问记录只能证明“授权并发起下载”，不能证明客户端从 OSS 完整取到文件，也拿不到实际传输字数。若需要完整审计，可另开 OSS 访问日志并设计关联 request/object key。

10. **CI 不应依赖真实 OSS 才能跑常规测试。**  
    用 fake store 测试权限、配额、认领、删除、URL 语义；用 SDK 自定义 HTTP client 或受控本地服务测试少量协议行为；真实 OSS smoke test 放到手动/带 secret 的 nightly job。

## 4. 不确定点与需真实账号验证项

- **新 OSS 账号域名策略**：2025-03-20 后新开通 OSS 用户在中国内地 Bucket 可能不能通过默认外网域名做数据 API，需要自定义域名。是否适用于该部门账号、域名是否已备案、HTTPS 证书如何管理，必须在真实账号验证。
- **实际价格与流量归属**：外网流出流量是确定计费项，但当前单价、资源包、闲忙时规则需以账号当前定价页和账单为准；本报告未做成本测算。
- **`oss-emulator` 与 V2 兼容性**：官方模拟器太老，虽然可能通过 V1 签名模式覆盖部分用例，但未验证其是否兼容 V2 SDK 的请求序列、V4 签名、ListObjectsV2 和上传管理器。
- **AWS SDK v2 细节**：官方提示 Manager/流式上传签名细节，但具体 `aws-sdk-go-v2` 版本、自定义 middleware、路径风格和预签名响应头组合仍需真实 OSS 冒烟验证。
- **ECS RAM Role 运行形态**：ECS 直接部署可用性明确；若服务跑在容器、systemd 代理或受限网络下，还需验证实例元数据访问、超时和凭证刷新。
- **RAM Policy 最终最小集**：以上动作是按官方文档推导的最小候选集；实际上传管理器、列举和清理路径可能多出少量权限，需要用真实子账号/角色做 deny-by-default 验证。
- **MinIO 留口子的必要性**：这是架构权衡而非官方能力问题。若没有明确的多云/私有化需求，先不要引入 S3 adapter；若有，接口设计必须把 OSS 特有的公开域名、凭证和响应头语义隔离在 adapter 内。