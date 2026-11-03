# OSS 签名地址在浏览器中的下载与显示行为调研

## 1. 结论

1. **默认域名不应作为本项目的前端访问方案。**
   - 通过 OSS Bucket 默认域名 / 传输加速域名命中策略时，OSS 会追加 `x-oss-force-download: true` 和 `Content-Disposition: attachment`，标准浏览器直接打开会下载而不是预览。
   - 老规则按地域、Bucket 创建时间和 MIME 类型生效：`text/html` 很早即被强制；图片规则覆盖 `image/jpeg`、`image/png`、`image/webp`、`image/svg+xml`、BMP/TIFF/ICO/HEIC 等常见图片 MIME。PDF、视频不在早期显式 MIME 清单里，若类型正确、无 `attachment` 元数据，浏览器可 inline 预览/播放。
   - 但 **2022-10-09 之后首次开通 OSS 的用户新建 Bucket，默认域名访问“全部”文件类型都会强制下载**；并且 **2025-03-20 之后开通 OSS 的账号，在中国内地 Bucket 上用默认公网 Endpoint 调 GetObject 等数据 API 会直接返回 400 `PublicEndpointForbidden`**。因此对一个现在新开的部门账号，默认域名的签名 GET 可能根本不可用，而不只是被强制下载。
   - 直接用 `<video>` / `<audio>` 加载时，官方提醒媒体元素可能优先播放并忽略 `attachment` 下载建议，但这属于元素/浏览器行为，不能作为统一承诺。

2. **签名 URL 可以覆盖部分响应头，但不能覆盖 `Content-Type`，也不能任意加 `nosniff`。**
   - 签名 GET 支持 `response-content-disposition`、`response-cache-control`，另支持 `response-content-language`、`response-expires`、`response-content-encoding`；这些参数必须包含在签名中，匿名 GET 不允许自定义响应头，且只有成功返回 200 时才覆盖。
   - `response-content-type` 当前明确不支持，官方错误文档说明“出于安全考虑，GET请求时，不支持指定 response-content-type”。因此必须在上传 / CopyObject 替换元数据时写对对象 `Content-Type`。
   - `X-Content-Type-Options: nosniff` 不是 `response-*` 覆盖参数。OSS 有对象级 `x-oss-persistent-headers` 机制可持久返回非 `x-oss-meta-` 自定义响应头，但官方同时禁止与保留的标准 HTTP 头冲突；`X-Content-Type-Options` 是否被 OSS 判定为保留头未给出完整清单，需真实 Bucket 验证。若用 CDN，可在 CDN 出站响应头确定性地添加该头。
   - 对默认域名安全策略，官方解决方案是改用自定义域名；官方没有承诺用 `response-content-disposition=inline` 能绕过 `x-oss-force-download`，不应依赖。

3. **有效期：新项目应按 V4 设计，上限 7 天；V1 理论更长但已进入下线流程。**
   - V4 使用长期 AK：`x-oss-expires` 为 1–604,800 秒（7 天）；使用 STS 临时凭证最大 43,200 秒（12 小时）。控制台 / ossbrowser 上限为 32,400 秒（9 小时）。
   - V1 历史上用绝对 Unix `Expires`，理论上可到时间戳表示上限；但 2025-03-01 起新 UID 逐步不可用，2025-09-01 起新 Bucket 逐步不可用，因此本项目不应选 V1。
   - 签名是确定性 HMAC 计算结果：只要 AK、方法、对象、host、规范化 query/header、URL 编码和过期参数完全相同，URL 就稳定。V4 若做“时间桶”，需要同时固定 `x-oss-date` 与由它推得的 `x-oss-expires` /绝对截止时间；只固定 `Expires` 而每次生成新的 `x-oss-date` 仍会得到不同 URL。
   - 完全相同的签名 URL 配合 `Cache-Control` / ETag / Last-Modified 可以被浏览器缓存和条件请求复用。但这会绕过应用端每次下载鉴权和访问记录；若 `max-age` 长于签名剩余寿命，浏览器甚至可在签名过期后继续使用本地缓存。

4. **自定义域名 / CDN：中国内地场景基本意味着域名必须 ICP 备案，并要显式配置 HTTPS。**
   - Bucket 位于中国内地时，绑定到 OSS 的自定义域名必须完成 ICP 备案；Bucket 在中国香港、新加坡等非中国内地节点则官方说明无需 ICP。
   - CDN 域名若加速区域包含中国内地，也必须完成 ICP 备案；仅中国内地以外加速不在此要求内。
   - OSS 默认域名自带 HTTPS；自定义域名直连 OSS 时需在 OSS 控制台做证书托管。走 CDN 时证书配置在 CDN 控制台。证书可购买、申请免费证书或上传第三方证书；OSS 证书托管支持 RSA/ECC，公钥和私钥各不超过 20 KiB。

5. **`<img>`、`fetch`、302、CORS、凭证和防盗链的行为不同。**
   - 普通 `<img src>` 不设置 `crossorigin` 时通常可直接跨域显示图片，不需要 OSS CORS；但请求不是 CORS 请求，画布读取像素会被污染。设置 `crossorigin`、要读画布、或用 `fetch().blob()` 时必须配置 CORS。
   - 直接 `fetch(签名URL)`：OSS 需允许前端 Origin 和 GET/HEAD；如 JS 要读 `ETag`、`Content-Disposition` 等响应头，还要配置 Expose Headers。走 CDN 时要在 CDN 配置 CORS 或明确透传 OSS CORS 头。
   - 现有 `AuthorizedImage` 是先请求应用同源 URL，再由 302 跳到 OSS。`fetch` 默认 `redirect: follow`、`credentials: "same-origin"`：应用请求会带会话 Cookie，跳到 OSS 后不会带应用 Cookie；签名本身完成 OSS 授权。若调用方手工加了 `Authorization`，现代 Fetch 规范会在跨 Origin 重定向时移除该头。
   - **关键风险：Fetch 标准规定跨 Origin 重定向后请求的 redirect-taint 不是 same-origin 时，序列化 `Origin` 为 `null`。**因此应用同源 URL 302 到 OSS 时，OSS 看到的常是 `Origin: null`，只配置精确应用域名会 CORS 失败。`AllowedOrigin: *` 可能匹配 null origin，但授权面过宽且不能配合凭证模式；更稳的方案是让前端拿到最终签名 URL 后直接 fetch，或在 CDN 边缘专门处理 CORS。
   - OSS Referer 防盗链对匿名访问和签名 URL 都生效；带 `Authorization` 的 AK API 调用不受限。浏览器默认 Referrer-Policy 通常对 HTTPS 跨域只发送应用 Origin，但页面/链接若为 `no-referrer`、`same-origin`，或媒体 Range 请求无 Referer，会被视为空 Referer。需要白名单匹配实际协议和域名，或允许空 Referer；走 CDN 时还必须在 CDN 层配置防盗链，否则可被边缘缓存绕过。

## 2. 依据（官方文档，均于 2026-11-03 查阅）

| 主题 | 文档与关键内容 |
|---|---|
| 默认域名强制下载规则 | [如何配置访问OSS文件时的预览行为？](https://help.aliyun.com/zh/oss/how-to-ensure-an-object-is-previewed-when-you-access-the-object)：官方给出“OSS域名强制下载规则速查表”，说明默认域名/传输加速域名按时间、地域、Bucket 创建时间和 MIME 追加 `x-oss-force-download: true` 与 `Content-Disposition: attachment`；2022-10-09 后首次开通 OSS 用户新建 Bucket 为“全部”类型；自定义域名可规避。 |
| 新账号默认公网 Endpoint 禁用 | [OSS Update Notice: Policy Change in Calling Data API Operations via the Default Public Domain Name](https://www.alibabacloud.com/notice/oss_update_notice_policy_change_in_calling_data_api_operations_via_the_default_public_domain_name_45a?_p_lc=1)：2025-03-20 后开通 OSS 的用户，中国内地 Bucket 经默认公网域名调用 GetObject 等数据 API 会被阻断。另见 [0048-00000401](https://www.alibabacloud.com/help/en/oss/user-guide/0048-00000401)。 |
| GetObject 响应头覆盖 | [GetObject](https://help.aliyun.com/zh/oss/developer-reference/getobject)：列出 `response-cache-control`、`response-content-disposition` 等参数；说明匿名 GET 不能自定义响应头，且仅 200 成功时返回覆盖值。 |
| 覆盖参数必须签名 | [0017-00000901](https://help.aliyun.com/zh/oss/user-guide/0017-00000901)：官方列出支持的响应头定制参数，并要求请求携带签名。 |
| Content-Type 不能覆盖 | [0017-00000902](https://help.aliyun.com/zh/oss/user-guide/0017-00000902)：原文“出于安全考虑，GET请求时，不支持指定 response-content-type”。 |
| 对象级自定义响应头 | [HeadObject](https://help.aliyun.com/zh/oss/developer-reference/headobject)：说明 PutObject 可用 `x-oss-persistent-headers` 保存非 `x-oss-meta-` 自定义头并在响应中返回；[OSS HTTP 400 errors](https://www.alibabacloud.com/help/en/oss/user-guide/http-400-error-code) 说明该机制不能指定与标准 HTTP 头冲突的保留头。 |
| 预签名 URL 与有效期 | [使用预签名URL下载或预览文件](https://help.aliyun.com/zh/oss/user-guide/how-to-obtain-the-url-of-a-single-object-or-the-urls-of-multiple-objects)：V4 最大 604,800 秒，STS 最大 43,200 秒；V1 为 timestamp limit；控制台 32,400 秒。 |
| V4 签名参数与确定性 | [签名版本4（推荐）](https://help.aliyun.com/zh/oss/developer-reference/add-signatures-to-urls)：`x-oss-date + x-oss-expires` 决定有效期，并完整描述 CanonicalRequest / HMAC-SHA256 计算。 |
| V1 下线 | [Notice on the discontinuation of the OSS V1 signature algorithm](https://www.alibabacloud.com/en/notice/notice_on_the_discontinuation_of_the_oss_v1_signature_algorithm_40f)、[V1签名升级为V4签名指引](https://help.aliyun.com/zh/oss/developer-reference/guidelines-for-upgrading-v1-signatures-to-v4-signatures)：2025-03-01 新 UID、2025-09-01 新 Bucket 逐步不可用 V1。 |
| 自定义域名与 ICP | [通过自定义域名访问OSS](https://help.aliyun.com/zh/oss/user-guide/access-buckets-via-custom-domain-names)：中国内地 Bucket 的自定义域名必须 ICP 备案；非中国内地无需；自定义域名不添加默认域名的强制下载头。 |
| HTTPS 证书 | [通过HTTPS协议访问OSS](https://help.aliyun.com/zh/oss/user-guide/access-oss-by-https-protocol)：默认域名原生 HTTPS；直连自定义域名在 OSS 证书托管，CDN 域名在 CDN 控制台配置 HTTPS 证书；证书来源和大小/算法限制见该页。 |
| CDN 与 ICP / HTTPS / 回源 | [通过CDN加速访问OSS](https://help.aliyun.com/zh/oss/user-guide/cdn-acceleration)：加速区域包含中国内地必须 ICP；添加域名后配置 HTTPS 证书；私有 Bucket 回源与签名参数的冲突也有说明。 |
| OSS CORS | [跨域设置](https://help.aliyun.com/zh/oss/user-guide/configure-cross-origin-resource-sharing)、[PutBucketCors](https://help.aliyun.com/zh/oss/developer-reference/putbucketcors)：默认不开启 CORS；需匹配 Origin、Method、Header；`ExposeHeader` 控制 JS 可读响应头；多来源/通配符建议 `Vary: Origin`；CDN 场景要在 CDN 配置或透传。 |
| `<img>` 是否需要 CORS | [MDN `<img>`](https://developer.mozilla.org/en-US/docs/Web/HTML/Reference/Elements/img)：未设置 `crossorigin` 时发送 non-CORS 请求，可显示但画布污染；设置后必须由服务器返回匹配的 CORS 头。 |
| 302 后 `Origin: null` | [Fetch Standard](https://fetch.spec.whatwg.org/)：`Origin` 用于 CORS 请求；“redirect-taint 不是 same-origin 时序列化为 null”；默认 redirect mode 为 follow。 |
| fetch 凭证 | [MDN Request: credentials](https://developer.mozilla.org/en-US/docs/Web/API/Request/credentials)：默认 `same-origin`，只对同源请求发送/接受凭证；`include` 才跨源携带。 |
| Referer 策略 | [MDN Referrer-Policy](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Referrer-Policy)：默认 `strict-origin-when-cross-origin`，跨源 HTTPS 请求通常只发送 Origin；`no-referrer` / `same-origin` 不发送跨源 Referer。 |
| OSS 防盗链 | [防盗链](https://help.aliyun.com/zh/oss/user-guide/hotlink-protection)：按空 Referer、黑名单、白名单顺序校验；协议参与匹配；签名 URL 访问会执行防盗链；CDN 需同步配置。 |
| 浏览器缓存 | [MDN HTTP caching](https://developer.mozilla.org/en-US/docs/Web/HTTP/Guides/Caching)：HTTP cache 会将响应与请求关联并在后续请求复用，支持新鲜度与 ETag/Last-Modified 条件验证。 |

## 3. 对本项目的含义

- **把自定义域名当作 OSS 前置条件，而不是优化项。**若部门使用 2025-03-20 后新开的 OSS 账号并选中国内地 Bucket，默认公网签名 URL 会被 `PublicEndpointForbidden` 拒绝；即使账号较老，也可能命中强制下载规则。需要先完成域名 ICP 备案、DNS CNAME、OSS 绑定和 HTTPS 证书。
- **服务端上传/管理仍可用内网 Endpoint，签名 URL 必须按最终访问域名生成。**V4 签名覆盖 host / canonical request；不能先按内网 host 签名后简单替换域名。自定义域名本身只用于公网访问，不能替代 ECS 到 OSS 的内网 Endpoint。
- **新实现应使用 V4 签名，短期 TTL 更符合工单权限模型。**若仅为减少 URL 抖动做 5–10 分钟时间桶，可以接受；若为了浏览器缓存做小时级稳定 URL，会削弱“每次访问重新鉴权、写访问记录、撤销即时生效”的语义。
- **上传时必须写正确的对象元数据。**图片、PDF、文本要写可靠 `Content-Type`；HTML、SVG 及未知/高风险类型应持久设置或按请求覆盖 `Content-Disposition: attachment`。不能寄望 GET 时改 `response-content-type`。
- **现有 `fetch(att.url) -> blob -> object URL` 与“同源接口 302 到 OSS”组合存在 CORS 陷阱。**只给 OSS 配置精确应用 Origin 很可能失败，因为跳转后的 `Origin` 是 `null`。应优先设计为前端获取最终签名 URL 后直接 fetch；如坚持 302，需要实测并在 OSS/CDN 使用能匹配 null 的策略，或用 CDN 出站 CORS 头，但要评估放大规模。
- **CORS 与防盗链要按访问路径分层配置。**直连 OSS 时配置 Bucket CORS；走 CDN 时配置 CDN CORS / 透传。防盗链方面，现有附件 `<a rel="noreferrer">` 若未来直接指向或跳到 OSS，会带来空 Referer，OSS 需允许空 Referer 或干脆不用 Referer 防盗链，改以短时签名和权限检查为主。
- **审计模型会变化。**302 前的应用请求可记录一次授权下载，但后续 OSS 访问、浏览器缓存命中、CDN 命中不会再进入应用访问记录。若需要每次真实读取审计，需要引入 OSS/CDN 实时日志或禁用长缓存。
- **安全头策略要补位。**OSS 原路径不再统一由 Go HTTP handler 输出 `X-Content-Type-Options: nosniff`；应在对象持久头或 CDN 出站响应头中补齐，并保持未知类型 `application/octet-stream` + attachment。

## 4. 不确定点与需真实账号验证

- 需确认部门 OSS 账号开通时间、Bucket 地域和创建时间，才能判断其默认域名到底命中“强制下载”还是直接 `PublicEndpointForbidden`。
- `response-content-disposition=inline` 能否覆盖某些旧 Bucket 的默认域名强制下载策略，官方未承诺；官方明确建议自定义域名，不能把覆盖参数当作绕过方案。
- `x-oss-persistent-headers` 是否接受 `X-Content-Type-Options: nosniff` 未在官方文档给出完整保留头清单，需在真实 Bucket 验证；CDN 添加该头是确定可行的替代路径。
- “302 后 `Origin: null` + OSS `AllowedOrigin: *`”在 Chrome/Safari/Firefox 与当前 OSS/CDN 行为需实测；不同 CDN 对 null Origin、通配符和缓存 `Vary: Origin` 的处理也可能不同。
- 浏览器对 PDF、特殊视频编码、`<video>` Range 请求和 `Content-Disposition` 的处理有差异，需用实际附件类型和目标浏览器验证。
- 若采用 CDN，还需按实际域名验证 ICP 状态、证书托管、回源 Host/SNI、私有 Bucket 回源、缓存 Key 是否包含签名查询参数，以及访问日志能否满足审计要求。