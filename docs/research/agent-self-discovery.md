# Agent 如何凭一个 URL 自己学会用这个系统（#54 调研）

调研日期：2026-10-17。问题来源：issue #54（地图 #52 的用例 2「Agent 交接」）。

## 结论先行

1. **主入口用一份服务端公开提供的、短小的 Markdown 说明书**：`GET /agent.md`（`text/markdown`，
   无需凭证），同一内容再挂一个 `/llms.txt` 别名照顾约定俗成的发现路径。完整端点参考
   （即 `docs/api.md`）作为第二层，同样以原始 Markdown 公开提供（如 `/api.md`）。
2. **交接文案本身要能独立成立**：即使 Agent 一次网页都没读，文案里也要有绝对地址的说明书 URL、
   工单的 API 地址、凭证从哪来、第一条该跑的 `curl`。说明书是「深入」，不是「唯一线索」。
3. **修好「URL 指向 SPA 空壳」这个现存的坑**：现在交接文案（`share_text`）给的是
   `https://…/t/<编号>`，对 Agent 来说那是一个只有 `<div id="root">` 的 HTML 空壳（见下文
   「本仓库现状」）。至少要让这个空壳里有一行纯文本指向 `/agent.md`，最好对
   `Accept: text/markdown` 直接回 Markdown 指引。
4. **API 根自描述 + 401 提示指路** 作为廉价的兜底：`GET /api/v1` 返回能力清单与文档链接（现在是 404），
   401 的 `hint` 指向 Agent token 的来历和 `/agent.md`（现在说「先调用 POST /api/v1/session 登录」，
   会把 Agent 引向找人要密码）。
5. **暂不做 MCP server、暂不手写 OpenAPI**：MCP 需要每个同事在自己的 Agent 里装配置，
   违背「一个 URL」的前提；OpenAPI 对这套以 Markdown + curl 为主的 Agent 工作流增益小、维护成本高。
   两者都可以在 v0.2 之后再评估。

## 1. 业界做法与取舍

| 做法 | 是什么 | 适用对象 | 对本系统的取舍 |
|---|---|---|---|
| `/llms.txt` | 站点根（或任意路径下）的一份 Markdown：H1 名称（唯一必需）+ 引用块摘要 + 若干 H2「文件清单」链接；页面另有同 URL 加 `.md` 的纯 Markdown 版本。作者明确说主要用于推理时而非训练[^llmstxt] | 读网站的 Agent | 格式轻、与我们的「Markdown 优先」一致；但它只是**提案**，没有 Agent 会在拿到 `/t/…` 链接时自动去探测 `/llms.txt`——交接文案仍需显式给出地址。适合作为别名，不必作为唯一名字 |
| AGENTS.md | 「给编码 Agent 的 README」，放在**仓库根**，Codex、Jules、Cursor、Copilot 等支持，现由 Linux Foundation 下的 Agentic AI Foundation 托管[^agentsmd] | 在仓库里工作的 Agent | 是**仓库**约定，不是 Web 服务约定；同事的 Agent 不会 clone 本仓库。只借鉴它的写法（短、祈使句、可执行命令），不借鉴它的位置 |
| OpenAPI | 语言无关的 HTTP API 机器可读描述，当前版本 3.2.1（2026-09-10）[^oas] | 代码生成器、API 网关、函数调用框架 | 机器精确，但对「通用编码 Agent 读一遍就会用」不是必需：规范文档本身就有 34 万字符，这类 JSON/YAML 交给 WebFetch 会被截断/摘要。本系统的语义难点（等待方、交付/验收、幂等）在 schema 里表达不出来，仍得靠文字 |
| API 根自描述 / HATEOAS | Fielding：「REST API 应当只凭初始 URI 和标准媒体类型就能进入」[^fielding]；即根端点返回下一步链接 | 任何能发 HTTP 的客户端 | 非常便宜，适合作为兜底：Agent 无论从哪一个 `/api/v1/...` 撞进来都能找到路。本系统已经在部分响应里这样做（`GET /api/v1/session` 带 `inbox` 链接，收件箱项带 `read_new`/`read_full`，工单 Markdown 末尾有「下一步」） |
| Markdown 内容协商 | 客户端发 `Accept: text/markdown`，服务端回 Markdown；Cloudflare 2026-02 推出 Markdown for Agents 在边缘做这件事[^cf] | 发这个头的 Agent | Claude Code 的 WebFetch **确实**发送偏好 Markdown 的 `Accept` 头[^cc-tools]；但 curl 默认 `Accept: */*`，Codex 等不一定发[^cf]。所以只能做增强，不能作唯一路径 |
| MCP server | Agent 通过 MCP 协议调用工具；HTTP 传输的授权走 OAuth 2.1 + Protected Resource Metadata，stdio 传输从环境变量取凭证[^mcp-auth] | 已配置该 server 的 Agent | 体验最好（工具即接口），但前提是使用者先在自己的 Agent 客户端里**安装/登记**这个 server，且我们要实现 OAuth 2.1 资源服务器那一套或分发本地 stdio 程序。对「把一段文字发给同事」的用例，安装门槛本身就是失败点。v0.2 不做 |

**截至 2026 年的走向**：没有一个「让任意 Agent 凭 URL 自学 Web 服务」的正式标准。
事实上的组合是「Markdown 优先（`llms.txt` / `.md` 页面 / `Accept: text/markdown`）+ 已有的 HTTP API」，
MCP 用于长期、需安装的集成；AGENTS.md 是仓库内约定。我们的做法与这个主流一致，且不押注任何单一发现机制。

## 2. 真实 Agent 拿到 URL 后实际怎么读

**Claude Code（WebFetch 工具）**——官方文档[^cc-tools]：

- WebFetch 接收 URL + 一个「要提取什么」的 prompt；HTML 先转成 Markdown，然后「对大多数抓取，用一次单独的模型调用
  对内容执行 prompt，Claude 收到的是那次调用的结果而不是原始页面」。官方原话：**「这使 WebFetch 天生有损」**，
  并建议「用 Bash 里的 `curl` 拿未经处理的页面」。
- 每次最多读 100,000 字符；更长的页面（v2.1.290+）会告知还剩多少、可用 `offset` 续读。
- 发送 `User-Agent: Claude-User…` 和**偏好 Markdown 的 `Accept` 头**，以便支持内容协商的服务器直接回 Markdown。
- **不能自定义请求头**（工具只有 url/prompt/offset 参数），因此**凭证保护的页面读不到**——这直接决定文档必须公开。
- 拒绝 `localhost` 和不带点的主机名（裸内网名）；跨主机重定向不自动跟随。
- 抓取前把**主机名**发给 `api.anthropic.com` 做域名安全检查；网络挡住该地址时 WebFetch 失败，可用 `skipWebFetchPreflight: true` 关闭[^cc-data][^cc-net]。
  `taskboard.urithub.com` 是带点的正式域名，不受「裸内网名」限制；但域名检查是否放行该主机未见文档说明，需在演练中实测。
- 结论：对 Claude Code，**短、结构清楚的纯 Markdown** 最可靠；真正要执行的 API 调用它会用 Bash `curl`（能带 `Authorization` 头）。

**Claude API 的服务端 web_fetch 工具**（claude.ai、托管 Agent 等）[^api-webfetch]：在 Anthropic 服务器上抓取，
不执行 JavaScript（「不支持动态 JS 渲染的网站」），只允许抓取对话中出现过的 URL，拒绝私有地址，
拒绝看起来含凭证的 URL（除非凭证出现在用户消息里）。**内网服务对这类 Agent 不可达**——
本系统只服务内网编码 Agent（跑在同事电脑上），这一类不在目标内。

**Codex CLI**[^codex-sec][^codex-cfg]：「默认情况下 Agent 在关闭网络访问的情况下运行」，
需要网络的命令要用户批准（或配置 `sandbox_workspace_write.network_access = true`）。
内置 `web_search` 默认是 `cached`（OpenAI 维护的索引，不访问外网），配置参考里**没有**独立的抓取 URL 工具。
所以 Codex 读我们的文档和调 API 都走 shell `curl`，第一次会弹审批；而 `curl` 拿到的是原始字节，
对 Markdown/纯文本最友好，对 SPA HTML 最不友好。

**共同点**：所有 Agent 都不执行我们的 React 前端。凡是交给 Agent 的 URL，服务端直接返回的内容就是它能看到的全部。

## 3. 文档公开还是凭证后可读

**建议公开（无需凭证）**。理由：

- WebFetch 不能带 `Authorization` 头（上节），凭证门槛会让 Claude Code 最顺手的路径直接失败；
  Agent 也需要先读文档才知道「凭证怎么用」——鸡生蛋问题。
- 文档只描述**接口契约**，不含任何工单数据；同样的内容已经在公开的 GitHub 仓库 `docs/api.md` 里（仓库可见性为 PUBLIC）。
- 服务只在内网（`taskboard.urithub.com` 走 HTTPS，仅内网解析/可达），公开范围本来就只是内网。
- 数据接口仍然全部要凭证；`GET /api/v1/health` 已经是公开端点，先例存在。

注意事项：公开文档里**不要**出现任何真实 token、访客密钥、成员名单或工单内容；示例用占位符。

## 4. 本仓库现状评估

### 4.1 服务端目前没有任何文档端点

- `internal/server/server.go:74-75`：`/api/` 交给 API，其余全部交给前端静态目录。
- `internal/httpapi/session.go:40-60`：注册的路由里没有文档类端点；`/api/v1/` 兜底返回 `404 not_found`
  「没有这个接口」，`GET /api/v1` 本身也是 404。
- `internal/webapp/static.go:217` 起的 `resolve`：找不到文件就**回退到 `index.html`**（SPA 路由）。
  因此 `https://taskboard.urithub.com/t/20261016-001` 和 `/g/<密钥>` 对任何 Agent 返回的都是
  `web-src/index.html` 那个只有 `<title>部门工单平台</title>` 和空 `<div id="root">` 的页面。
- 现有交接文案 `share_text`（`internal/httpapi/tickets.go:199-202`）：
  `李工：给你派了一张工单「…」，让你的 Agent 读一下 https://…/t/<编号>（它也会出现在你的收件箱里）`。
  **按今天的实现，Agent 照做只会读到空壳**，既不知道 API 在 `/api/v1`，也不知道要 `tbag_` token，
  更不知道 `?format=markdown`。这是用例 2 的头号阻断点。
- 401 的 `hint` 是「先调用 POST /api/v1/session 登录」（`internal/httpapi/viewer.go:43`、`session.go:148`），
  会诱导 Agent 向人要用户名密码；应改为指向 Agent token（个人设置里签发）与 `/agent.md`。

### 4.2 `docs/api.md` 适不适合直接给 Agent 读

约 3.35 万字符（58KB），能装进 WebFetch 的 10 万字符单次上限，但：

- **它是给实现者的参考，不是给使用者的入门**：按工单号（#29、#36…）组织，夹杂「orchestrator 决议第 N 条」「评审 P2-7」
  「迁移 0013」「NSSM」等内部语境；管理员端点（备份、更新、标签治理、成员管理）与 Agent 日常无关却占大量篇幅。
  经 WebFetch 小模型摘要后，关键约束很容易丢。
- **缺起手流程**：没有「第 1 步验证凭证 → 第 2 步读工单 → 第 3 步回复/交付 → 第 4 步之后查收件箱」这条主线。
- **凭证说明缺「从哪来」**：写了 `tbag_` 是 `POST /api/v1/me/agent-tokens` 签发，但没写「这一步只能由人在网页
  个人设置里做，Agent 不能自己签发（403 `web_session_required`），拿到后放进环境变量」。
- **地址都是相对路径**，没有基地址（`https://taskboard.urithub.com`）；Agent 从文档无法知道该打哪台主机。
- **等待方/收件箱语义写得全但偏实现**：规则用伪代码给出，缺「作为指派人，你的工作循环是什么」的白话版本
  （读 → 干活 → `deliver: true` 交付 → 等发起人验收；被退回就看 `read_new`）。
- **幂等写了，但没写「Agent 应该总是带 `Idempotency-Key`」**这样的行为建议。
- 优点可直接复用：统一错误结构带 `hint`；`GET /api/v1/tickets/{id}?format=markdown` 末尾已有按角色×轮次
  生成、可复制执行的「下一步」——这正是 Agent 最需要的东西，说明书应把 Agent 尽快导到这里。

结论：`docs/api.md` 可以**原样**作为第二层参考公开（`/api.md`），但需要另写一份面向 Agent 的短说明书作为入口。

## 5. 推荐方案

**分层**（从交接文案出发，每一层都能独立让 Agent 前进一步）：

1. **交接文案**（人复制给同事）——自包含：说明书 URL、工单 API URL、凭证来源、第一条命令。
2. **`/agent.md`**（公开、`text/markdown`、目标 ≤ 1.5 万字符，绝对地址由 `[server] public_url` 生成；`/llms.txt` 为别名）。
3. **工单 Markdown**（`/api/v1/tickets/{id}?format=markdown`，需凭证）——已有，含「下一步」。
4. **`/api.md`**（公开，`docs/api.md` 原文嵌入二进制提供）——查细节用。
5. **兜底**：`GET /api/v1` 自描述 JSON（名称、版本、`docs` 链接、`session`/`inbox`/`tickets` 入口、凭证说明一句话）；
   401/404 的 `hint` 带 `/agent.md` 绝对地址；SPA 的 `index.html` 在 `#root` 里放一行会被 React 覆盖的纯文本
   「这是网页界面；AI Agent 请读 https://…/agent.md」并加 `<link rel="alternate" type="text/markdown" href="/agent.md">`；
   对 `/t/<编号>`、`/g/<密钥>` 的请求若 `Accept` 偏好 `text/markdown`（Claude Code 会这样发），直接回一段 Markdown 指引
   （这张工单的 API 地址 + 凭证说明 + 说明书链接），浏览器请求不受影响。

文档用服务端生成（Go 模板 + `public_url`），不放进前端 `web/` 目录：前端独立更新，而说明书描述的是后端接口，
应跟后端版本走；且需要插入基地址。

## 6. 最小内容清单

### `/agent.md`（面向 Agent 的说明书）

- [ ] 一句话：这是什么（部门工单平台），你（Agent）在这里能做什么、不能做什么（不能签发凭证、不能管理账号）。
- [ ] **基地址**（绝对 URL）与「所有接口在 `/api/v1`，用 `curl` 调，别用网页」。
- [ ] **凭证**：三种身份对照表——成员 Agent token（`tbag_`，由**人**在网页「个人设置 → Agent」签发，只显示一次，
      建议放环境变量如 `TASKBOARD_TOKEN`）；访客链接密钥（`tbg_`，就是 `/g/<密钥>` 里那段）；网页会话（Agent 不用）。
      统一用 `Authorization: Bearer …`；署名规则（「某成员 的 Agent · 名字」，永远不算本人）。
- [ ] **第 0 步**：`GET /api/v1/session` 验证身份（成员拿到 `inbox` 地址；访客拿到它对应的那张工单）。
- [ ] **处理一张工单的主线**（含可复制的 curl）：读 `?format=markdown` → 按末尾「下一步」行动 →
      回复 `POST …/replies {"body_md"}` → 做完 `deliver: true` 交付（交付不关单，等发起人验收）→ 发起人验收 `close {"reason":"done"}`。
- [ ] **等待方与收件箱的白话版**：「轮到谁」从时间线推导；`GET /api/v1/inbox` 列出轮到我的开启工单及原因；
      用每项的 `read_new` 增量读、`read_full` 读全文；同一成员的所有 Agent 共用一份收件箱，可定时轮询。
- [ ] **幂等**：所有产生新内容的 POST 都带 `Idempotency-Key`（重试安全，24 小时内重放返回首次结果）。
- [ ] **错误**：`{"error":{"code","message","hint"}}`，照 `hint` 做；常见码速查（401 `invalid_token` / `link_expired`、
      403 `members_only` / `deliver_assignee_only`、409 `revision_conflict`、422 `unknown_label_value` + `suggestions`）。
- [ ] **附件**两步上传一句话 + 指向 `/api.md` 的对应小节。
- [ ] **更多**：链接 `/api.md`（完整参考）、`GET /api/v1/templates/{key}?format=markdown`（按模板建单指南）。
- [ ] 写法：短句、祈使、每步一个可执行命令；不出现工单号/决议号等内部语境；顶部一句
      「若你的抓取工具会摘要页面，请用 `curl` 读本页原文」。

### Agent 交接文案（人复制给同事；两种入口共用）

- [ ] 收件人 + 一句话任务（工单标题）。
- [ ] **说明书绝对地址**：`https://taskboard.urithub.com/agent.md`。
- [ ] **工单的 API 地址**（不只是网页 `/t/` 地址）：`https://…/api/v1/tickets/<编号>?format=markdown`，网页地址可并列给人看。
- [ ] **凭证从哪来**：成员——「用你自己的 Agent token（网页 个人设置 → Agent 签发）」；**绝不**把发起人的 token 写进文案。
      访客——访客链接本身就是凭证（`/g/<密钥>` 中 `tbg_…` 作 Bearer）。
- [ ] **第一条命令**：`curl -H "Authorization: Bearer $TASKBOARD_TOKEN" https://…/api/v1/tickets/<编号>?format=markdown`。
- [ ] 一句收尾：「照工单末尾的『下一步』做；以后轮到你时它会出现在 `GET /api/v1/inbox`」。
- [ ] 全文控制在 5～8 行，纯文本可直接粘贴进聊天工具和 Agent 对话框。

示意（措辞待 spec 定稿）：

```text
王梦龙：给你派了一张工单「整理对照表」，请让你的 AI Agent 处理：
1. 系统说明（先读）：https://taskboard.urithub.com/agent.md
2. 工单：https://taskboard.urithub.com/api/v1/tickets/20261016-001?format=markdown
3. 凭证：用你自己的 Agent token（网页「个人设置 → Agent」签发），请求头 Authorization: Bearer <token>
照工单末尾的「下一步」操作；之后轮到你时会出现在 /api/v1/inbox。
网页查看：https://taskboard.urithub.com/t/20261016-001
```

## 7. 未决与需演练验证

- Claude Code 的 WebFetch 域名安全检查对 `taskboard.urithub.com` 是否放行（文档只说发送主机名比对黑名单）——
  演练时实测；不行就在说明书/交接文案里写明「用 curl」。
- Codex 首次 `curl` 需要用户批准网络访问——交接文案可以提示同事批准，或在说明书里注明。
- 同事还没有 Agent token 时的体验：交接文案应让人一眼看出「先去个人设置签发一个」，这一步只能人做（ADR 0003）。

## 来源

[^llmstxt]: llms.txt 提案，https://llmstxt.org/
[^agentsmd]: AGENTS.md，https://agents.md/
[^oas]: OpenAPI Specification（latest，3.2.1），https://spec.openapis.org/oas/latest.html
[^fielding]: Roy Fielding, "REST APIs must be hypertext-driven"（2008），https://roy.gbiv.com/untangled/2008/rest-apis-must-be-hypertext-driven
[^cf]: Cloudflare Markdown for Agents（2026-02）的二手综述，含「Claude Code、OpenCode 发送 `Accept: text/markdown`，Codex 等不发」的第三方测试结论：https://alternativeto.net/news/2026/2/cloudflare-introduces-on-the-fly-html-to-markdown-conversion-for-ai-agents 、https://explainx.ai/blog/markdown-for-agents-content-negotiation-guide-2026 （二手来源；其中 Claude Code 一项已由 Claude Code 官方文档证实，其余 Agent 未核实）
[^cc-tools]: Claude Code Tools reference，「WebFetch tool behavior」一节，https://code.claude.com/docs/en/tools-reference
[^cc-data]: Claude Code Data usage，「WebFetch domain safety check」，https://code.claude.com/docs/en/data-usage
[^cc-net]: Claude Code Enterprise network configuration，https://code.claude.com/docs/en/network-config
[^api-webfetch]: Claude API Web fetch tool，https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-fetch-tool
[^codex-sec]: Codex「Agent approvals & security」，https://learn.chatgpt.com/docs/agent-approvals-security
[^codex-cfg]: Codex Configuration reference，https://learn.chatgpt.com/docs/config-file/config-reference
[^mcp-auth]: MCP 规范 2025-11-25，Authorization，https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization
