# 浏览器后台推送在国内内网环境下是否可用

> 调研工单：[#21](https://github.com/allanpk716/agent-taskboard/issues/21)（地图 [#17 成员通知](https://github.com/allanpk716/agent-taskboard/issues/17)）
> 调研日期：2026-10-16

## 结论

- **网页关闭后靠 Web Push 送达，在国内内网环境下不可靠，不能替代桌面客户端。**
  - Chrome 桌面版走 FCM，国内被墙，不挂代理基本收不到推送。
  - Edge（Windows 桌面）走 WNS（`*.notify.windows.com`），一般能连通，可以用。
  - Firefox 走 Mozilla autopush，国内能否连通没有可靠的公开测量，需要实测。
  - 即使推送服务能连通，桌面浏览器进程必须还在运行才能收到推送；用户把浏览器整个关掉，就收不到了。
- **网页打开期间，用 SSE + Notification API 弹通知是可行的**，可以作为桌面客户端的补充。但它依赖标签页不被浏览器休眠或丢弃，只能算“尽力送达”。
- 建议：桌面托盘客户端仍是主要的送达渠道；网页只在打开期间用 SSE 刷新铃铛并弹系统通知。Web Push 先不做；以后要做，也只把 Edge 当作可用的浏览器，并且不承诺送达。

## 1. 各浏览器用哪个推送服务

推送服务由浏览器决定，网站无法指定。网站从 `PushManager.subscribe()` 拿到 `PushSubscription.endpoint`，endpoint 的域名就是该浏览器厂商的推送服务（[MDN Push API](https://developer.mozilla.org/en-US/docs/Web/API/Push_API)）。

| 浏览器（桌面） | 推送服务 | 服务器发送到的 endpoint 域名 | 浏览器保持的长连接 |
|---|---|---|---|
| Chrome（以及大多数 Chromium 系浏览器） | FCM（Firebase Cloud Messaging） | `fcm.googleapis.com` | `mtalk.google.com:5228`（GCM/MCS 长连接，端口也可能用 443、5229、5230）（[Chromium gcm_client_impl.cc](https://chromium.googlesource.com/experimental/chromium/src/+/8e3ee1dae7a589cfd2c4e19597a7b076f4588801/google_apis/gcm/gcm_client_impl.cc)、[GCM 端口说明](https://groups.google.com/g/gcm-for-chrome-feedback/c/RCOOQJoDsPQ)） |
| Edge（Windows） | WNS（Windows Push Notification Service） | `*.notify.windows.com`，例如 `sg2p.notify.windows.com` | 由 Windows 的 WNS 通道维持 |
| Firefox | Mozilla Push Service（autopush-rs） | `updates.push.services.mozilla.com` | `wss://push.services.mozilla.com/`（[autopush 文档](https://autopush.readthedocs.io/en/latest/)、[autopush-rs](https://github.com/mozilla-services/autopush-rs)） |

注意：Edge 在 Android 上拿到的 endpoint 是 FCM，只有 Windows 桌面上才是 WNS（[Microsoft Q&A](https://learn.microsoft.com/en-us/answers/questions/2366491/chromium-edge-for-android-gets-google-endpoint-ins?forum=microsoftedge-all)）。微软建议服务端校验 endpoint 时只认 `notify.windows.com` 这个域，不要依赖子域名，子域名会变（[WNS 概述](https://github.com/MicrosoftDocs/windows-dev-docs/blob/docs/hub/apps/develop/notifications/push-notifications/wns-overview.md)）。

## 2. 国内网络与公司内网下的连通性

### Chrome / FCM：不可用

- GreatFire 把 `fcm.googleapis.com`（http 和 https）标为 **Blocked**，自 2017-06-23 起就有干扰记录，最近一次测试是 2026-09-25（[GreatFire: fcm.googleapis.com](https://en.greatfire.org/fcm.googleapis.com)）。`google.com` 域整体 “Mostly blocked”。
- 第三方推送厂商也有同样的说法：基于 FCM 的 Web Push 在中国大陆不可用（[Pushy](https://support.pushy.me/hc/en-us/articles/360043422932-Does-Pushy-work-in-China)），腾讯云也说明 FCM 通道不支持中国大陆（[腾讯云](https://www.tencentcloud.com/document/product/1024/30719)）。
- 两头都会断：浏览器连不上 FCM，就拿不到订阅、收不到消息；服务器连不上 FCM，就发不出去。即使服务器配了代理能发出去，同事电脑上的 Chrome 不挂代理也收不到。
- 公司内网的防火墙或代理通常也不放行 5228 端口；Chrome 经代理连 `mtalk.google.com` 时还有误判代理失效的已知问题（[Chromium 41353410](https://issues.chromium.org/issues/41353410)）。

### Edge / WNS：大概率可用

- WNS 是 Windows 自身通知基础设施的一部分，国内的 Windows 应用通知都依赖它，GreatFire 上没有它被封锁的记录（[GreatFire: notify.windows.com](https://en.greatfire.org/notify.windows.com) 显示未测试）。
- 公司防火墙需要放行 `*.notify.windows.com` 和 `*.wns.windows.com`（[Microsoft Q&A: WNS](https://learn.microsoft.com/en-us/answers/questions/2282530/wns-sometimes-not-received)）。
- WNS 有过整体故障（2024 年 7 月，[MicrosoftEdge/DevTools#262](https://github.com/MicrosoftEdge/DevTools/issues/262)），并且不提供端到端送达确认。

### Firefox / autopush：未知，需要实测

- GreatFire 没有测过 `push.services.mozilla.com` 和 `updates.push.services.mozilla.com`（[GreatFire](https://en.greatfire.org/updates.push.services.mozilla.com)），`mozilla.com` 域整体为 “Mixed”。没有找到可信的公开测量。
- 北京谋智火狐已停止运营，Firefox 账户服务在中国于 2025-09-29 终止，但浏览器本身照常可用（[CyberInsider](https://cyberinsider.com/mozilla-announces-imminent-end-of-firefox-account-services-in-china/)）。推送服务不受这件事直接影响，但能否连通仍需实测。

### 关于本机测试

本次在开发机上用 `curl` 测了上述域名，都返回了 HTTP 响应，但解析出的 IP 全在 `198.18.0.0/15`，说明这台机器走的是透明代理（fake-IP）。**这个结果不能代表同事电脑的直连情况**，未作为证据。真要验证，需在一台不挂代理的内网电脑上，分别用 Chrome、Edge、Firefox 打开一个 Web Push 测试页订阅并发一条推送。

## 3. 服务器（内网 VM，可访问公网）发送推送的要求

- **网页必须是安全上下文**：Service Worker 和 Push API 只在 HTTPS（或 localhost）下可用。按 ADR 0002，平台用公网域名 + Let's Encrypt 证书，域名解析到内网 IP，这一条满足，浏览器不会因为“内网 IP”而拒绝。
- **VAPID（[RFC 8292](https://www.rfc-editor.org/rfc/rfc8292)）**：
  - 生成一对 P-256 密钥，用 ES256 签 JWT；
  - JWT 的 `aud` 是 endpoint 的 origin（例如 `https://sg2p.notify.windows.com`），`exp` 不超过请求时刻后 24 小时，`sub` 填 `mailto:` 或 `https:` 联系地址；
  - 请求头 `Authorization: vapid t=<JWT>, k=<公钥>`；
  - 订阅时，网页把同一个公钥作为 `applicationServerKey` 传入，之后只有用对应私钥签名的请求才会被接受。签名错误、过期或 `aud` 不对时，推送服务可能返回 401 或 403。
  - Edge 的 WNS 只接受带 VAPID 的 Web Push（[web-push-php#136](https://github.com/web-push-libs/web-push-php/issues/136)）。
- **负载加密**：按 RFC 8291 用 `aes128gcm` 加密，请求要带 `TTL` 头（RFC 8030）。Go 生态有现成的库（如 `github.com/SherClockHolmes/webpush-go`），不用自己实现。
- **出站网络**：VM 要能直连各推送服务的 endpoint 域名（HTTPS 443）。WNS 一般可以；FCM 需要代理；Mozilla 需要实测。服务端不要对 endpoint 做过严的域名白名单，至少要放行 `*.notify.windows.com`。也要注意 SSRF 过滤别误伤推送域名（[docs.plus#398](https://github.com/docs-plus/docs.plus/issues/398)）。
- **时间同步**：`exp` 依赖服务器时钟，VM 要开 NTP。
- **订阅失效**：推送服务返回 404 或 410 时，删除这条订阅。
- endpoint 本身是一个能力 URL，谁拿到都能发消息，要当作秘密保存（[MDN Push API](https://developer.mozilla.org/en-US/docs/Web/API/Push_API)）。

## 4. 浏览器必须在运行

在 Windows、macOS、Linux 桌面上，浏览器进程完全退出后，Service Worker 不会被唤醒，推送要等浏览器下次启动才会到（[web.dev Push FAQ](https://web.dev/articles/push-notifications-faq)、[Chromium 开发者讨论](https://groups.google.com/a/chromium.org/g/chromium-dev/c/jqrtJCPMb-k)）。浏览器不需要开着窗口，只要后台进程还在就行（Chrome 的“关闭后继续运行后台应用”、Edge 的启动加速等设置会影响这一点）。Android 和 ChromeOS 没有这个限制，但它们不在本项目范围内。

所以，即使 Edge + WNS 能连通，“网页关闭后也能收到”也取决于用户的浏览器设置，平台无法保证。

## 5. 网页打开期间：Notification API + SSE

可行，限制如下：

- **SSE 本身**：经 Caddy 反代时，`Content-Type: text/event-stream` 的响应会立即刷新，不会被缓冲（[Caddy reverse_proxy](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy)）。按 ADR 0002 走 HTTPS，浏览器和 Caddy 之间是 HTTP/2，多个标签页共享连接，避开了 HTTP/1.1 下每个域名最多 6 条 SSE 连接的限制（[MDN EventSource](https://developer.mozilla.org/en-US/docs/Web/API/EventSource)）。`EventSource` 断线会自动重连，可以带 `Last-Event-ID` 补发。
- **后台标签页**：Chrome 的计时器节流只影响 `setTimeout`/`setInterval`，不影响 SSE 收消息（[Chrome 88 timer throttling](https://developer.chrome.com/blog/timer-throttling-in-chrome-88)）。真正的风险是标签页休眠或丢弃：
  - Edge “睡眠标签页”默认在标签页闲置 2 小时后让它休眠。官方列出的豁免条件包括当前可见、播放音频、公司内网站点、用户加入“始终保持活动”的站点等，其中没有“有 SSE 连接”（[Edge 性能功能](https://support.microsoft.com/en-US/edge/learn-about-performance-features-in-microsoft-edge)、[SleepingTabsEnabled 策略](https://learn.microsoft.com/en-us/deployedge/microsoft-edge-policies/sleepingtabsenabled)）。我们用的是公网域名，不会被 Edge 当作内网站点。
  - Chrome 的“节省内存”模式也会丢弃闲置标签页，可以把站点加入 `chrome://settings/performance` 的“始终保持这些网站的活动状态”。
  - 对策：让用户或 IT 把平台域名加入白名单（Edge 有组策略 `SleepingTabsBlockedForUrls`）；页面重新可见时（`visibilitychange`）主动拉一次未读通知补齐。
- **系统通知**：`Notification.requestPermission()` 必须在用户点击后调用；Windows 的“专注助手/勿扰”会压住通知。可以通过 Service Worker 的 `registration.showNotification()` 弹通知，这样点击后可以在 Service Worker 里打开工单页面，且不需要 Push API。

## 6. 对“网页能否补充或替代桌面客户端”的意见

| 场景 | 网页能否胜任 |
|---|---|
| 平台网页开着（前台或后台未休眠） | 能。SSE + 系统通知，体验接近客户端 |
| 网页关了，浏览器还在后台运行，用 Edge | 理论上能（Web Push via WNS），但依赖浏览器设置，无法保证 |
| 网页关了，用 Chrome | 不能（FCM 被墙） |
| 浏览器完全退出 | 不能 |

因此网页只能**补充**桌面客户端，不能**替代**。“成员能不能及时知道有工单要处理”仍应由托盘客户端兜底。
