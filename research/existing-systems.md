# 现成系统能否覆盖：访客链接 + Agent API + 附件 + 内网自托管

> 研究工单：[#2](https://github.com/allanpk716/agent-taskboard/issues/2)（父地图 [#1](https://github.com/allanpk716/agent-taskboard/issues/1)）
> 调研日期：2026-10-15。术语以仓库根目录 `GLOSSARY.md` 为准（工单、说明、回复、附件、成员、访客、访客链接、外部文件引用）。

## 结论先行

- **没有任何一个现成系统原生提供"单张工单的访客链接 + 免登录回复 + 免登录上传附件 + 过期/撤销"**。这正是预期中最大的缺口，也被证实是普遍缺口。
- 主流系统给无账号者的路径只有两种：**邮件往返**（GitLab Service Desk、Zammad、FreeScout、OpenProject）或**项目级匿名权限**（Redmine、OpenProject、Taiga 只读、Plane 公开看板只读）。前者需要内网邮件服务、而且访客的 Agent 无法走 HTTP API；后者粒度是"项目"而不是"工单"，也没有过期。
- 唯一原生带"工单级免登录访问链接"的是 **osTicket**（邮件里的 access link / 工单号+邮箱查询），但它的 API 只能建单，Agent 读、回复、打标签、统计全部做不到，且正文是 HTML，直接出局。
- 最接近的两个候选：
  1. **Redmine**（或其分支 OpenProject）：成员侧功能、Markdown（CommonMark）、附件、REST/JSON API、标签式字段、统计都齐，**缺的只是访客链接**，需要外挂一个"访客网关"。
  2. **Gitea / Forgejo**：轻量、Markdown 原生、API 完整（含 issue/评论附件），**同样缺访客链接**，也需要外挂网关；统计接口弱于 GitLab。
  （GitLab 功能和 API 最全，但太重，且 Service Desk 只走邮件，也不解决访客链接。）
- **诚实判断**：任何候选都要自己写"访客网关"（签发/校验/撤销 token、访客查看页、回复与上传表单、访客 API），而这部分恰好是本平台最核心、最有差异的那一块。剩下的"成员登录 + 工单 CRUD + 附件 + 标签 + 统计"本身体量不大。**薄自建大概率不比"现成系统 + 网关"更贵**，而且省掉了两套数据模型之间的映射、权限对齐和一个重量级依赖的运维。建议地图 #1 的"自建还是采用现成系统"倾向**薄自建**；如果团队更看重"成员侧开箱即用的成熟 UI"，备选方案是 **Redmine + 自写访客网关**。

## 需求拆解（评估维度）

| 编号 | 需求 |
|---|---|
| R1 | 内网自托管、开源或可买断（不依赖公网 SaaS） |
| R2 | 成员登录后创建工单，说明为 Markdown，可插图 |
| R3 | 附件，单文件 50MB 量级（超过走外部文件引用，即 NAS 路径文本） |
| R4 | **单张工单**的访客链接：免登录查看、回复、上传附件，可过期、可撤销 |
| R5 | 完整 API：Agent 读取（最好 Markdown/JSON）、回复、上传附件、打标签、查询统计 |
| R6 | 访客的 Agent 能凭访客链接走 API |

图例：✅ 满足 / 🟡 部分满足 / ❌ 不满足。

## 总览矩阵

| 系统 | R1 自托管 | R2 Markdown | R3 附件 50MB | R4 工单级访客链接 | R5 Agent API | R6 访客 Agent 走 API |
|---|---|---|---|---|---|---|
| GitLab（Issues + Service Desk） | ✅ | ✅ | ✅（可配置） | ❌（仅邮件） | ✅ 最全 | ❌ |
| Gitea / Forgejo | ✅ 轻量 | ✅ | ✅（默认 100MB） | ❌ | ✅ | ❌ |
| Redmine | ✅ | ✅（CommonMark） | ✅（可配置） | 🟡 项目级匿名，无过期 | ✅ | 🟡 匿名可读，写入受限 |
| OpenProject | ✅ | ✅ | ✅ | 🟡 项目级匿名，评论权限未必可授予 | ✅（API v3, HAL+JSON） | ❌/🟡 |
| Zammad | ✅ | ❌（HTML/纯文本） | ✅ | ❌（仅邮件，官方 wontfix） | ✅ | ❌ |
| osTicket | ✅ | ❌（HTML） | ✅ | ✅/🟡 邮件里的 access link，有撤销/过期之实无其名 | ❌ 只能建单 | ❌ |
| FreeScout | ✅ | ❌（HTML） | ✅ | 🟡 付费 End-User Portal，按邮箱魔法链接登录 | 🟡 付费 API 模块 | ❌ |
| Plane（社区版） | ✅ | ❌（富文本 HTML） | ✅ | ❌ 公开看板只读，评论需注册 | 🟡 | ❌ |
| Taiga | ✅ | ✅ | ✅ | ❌ 匿名只可读 | ✅ | ❌ |
| Jira Service Management DC | 🟡 已停售新客户 | 🟡 wiki 标记 | ✅ | ❌（免登录门户只能建单） | ✅ | ❌ |
| Peppermint | ❌ 仓库已归档 | ✅ | ✅ | ❌ | ❌ 无正式外部 API | ❌ |
| Vikunja（任务管理，非工单） | ✅ | ❌（HTML 编辑器） | ✅ | 🟡 **项目级**分享链接，读/写/管理三档，可设密码，无过期 | ✅ 分享链接可换 JWT 走 API | 🟡 有，但粒度是项目 |
| Libredesk | ✅ | ❌ | ✅ | ❌（邮件/聊天组件） | ✅ | ❌ |

## 逐个候选

### GitLab（Issues + Service Desk）

- R1 ✅ 自托管社区版即可。
- R2 ✅ Issue 说明和评论都是 GitLab Flavored Markdown，API 返回原始 Markdown。
- R3 ✅ 通过 `POST /projects/:id/uploads` 上传，返回可嵌入 Markdown 的链接；附件上限由管理员配置。
- R4 ❌ 无账号者只能通过 Service Desk / External Participants **以邮件参与**："users without a GitLab account that can interact with an issue or Service Desk ticket only by email"；每张工单最多 10 个外部参与者；文档未说明可通过邮件上传附件。依据：[Service Desk](https://docs.gitlab.com/user/project/service_desk/)、[External participants](https://docs.gitlab.com/user/project/service_desk/external_participants/)。Service Desk 还需要配置收信邮箱（内网需有邮件服务器）。
- R5 ✅ Issues、Notes、Labels、Uploads API 齐全；统计有 [Issues statistics API](https://docs.gitlab.com/api/issues_statistics/)（按标签等过滤返回 all/opened/closed 计数）。
- R6 ❌ 外部参与者没有 HTTP 凭证。
- 需补：访客网关（见文末）。绕法是"每张工单一个项目 + 有过期时间的项目访问令牌"，代价过高。整体也偏重（运维成本远超需求）。

### Gitea / Forgejo

- R1 ✅ 单二进制，非常轻量。
- R2 ✅ Markdown 原生，API 返回原文。
- R3 ✅ `[attachment] MAX_SIZE` 默认 100MB（仅作用于 issue/PR 附件）。依据：[Config Cheat Sheet](https://docs.gitea.com/administration/config-cheat-sheet/)。注意旧版本 REST 上传不校验大小的缺陷 [go-gitea/gitea#35512](https://github.com/go-gitea/gitea/issues/35512)（已关联修复 PR）。
- R4 ❌ 评论、上传都必须登录，没有任何匿名写入或分享链接机制。
- R5 ✅ issue、评论、标签、`/issues/{index}/assets` 与 `/issues/comments/{id}/assets` 附件接口齐全（以实例 `/api/swagger` 为准）；统计只能靠列表接口的过滤和计数头，没有专门统计接口。
- R6 ❌
- 需补：访客网关；统计查询可能要额外封装。另一个问题是数据模型绑定"仓库"，部门工单要硬套成 repo/issue。

### Redmine

- R1 ✅
- R2 ✅ 5.0 起支持 CommonMark Markdown 文本格式。
- R3 ✅ REST 先 `POST /uploads.json` 拿 token，再在建单/更新时引用；超限返回 422。依据：[Rest_api](https://www.redmine.org/projects/redmine/wiki/Rest_api)。上限在管理设置里调整。
- R4 🟡 只有内置的 **Anonymous 角色**：对公开项目可授予 Add notes / Add issues 等权限，但该角色的权限对整个实例生效（也可以把 Anonymous 当成员加到单个项目上），**粒度是项目而不是工单，没有凭证、过期和撤销**。匿名上传附件还有已知问题（需要给 Anonymous 角色 Add issues 权限才不弹认证框，见 [Defect #30176](https://www.redmine.org/issues/30176)）。依据：[RedmineRoles](https://www.redmine.org/projects/redmine/wiki/RedmineRoles)。Planio 也明确建议匿名只给只读权限（[链接](https://plan.io/make-redmine-projects-public-on-planio/)）。
- R5 ✅ JSON REST（`issues.json?include=journals,attachments`），API key 通过 `X-Redmine-API-Key` 传递；标签可用分类、自定义字段或 tracker 表达（原生没有自由标签）；统计可用带过滤条件的查询读 `total_count`。
- R6 🟡 匿名可走 REST 读取公开项目，但没有工单级凭证。
- 需补：访客网关（服务账号代访客写入，回复里标注访客身份）；标签语义需约定。**是成员侧最成熟、和本需求贴合度最高的候选之一。**

### OpenProject

- R1 ✅（Redmine 系分支，现代化 UI）。
- R2 ✅ Markdown（CKEditor 存 Markdown）。
- R3 ✅
- R4 🟡 公开项目 + Anonymous 角色可读；文档提到权限可带 `public: true` 或 `require: :member`，**"Add comments" 能否真正授予匿名用户文档未说明，需实测**。依据：[Roles and permissions](https://www.openproject.org/docs/system-admin-guide/users-permissions/roles-permissions/)、[Permissions 开发概念](https://www.openproject.org/docs/development/concepts/permissions/)。另可通过回复通知邮件来评论（同样是邮件路径）。
- R5 ✅ [API v3](https://www.openproject.org/docs/api/endpoints/work-packages/)（HAL+JSON，说明字段带 `raw` Markdown）。
- R6 ❌/🟡
- 需补：同 Redmine。比 Redmine 重，概念更偏项目管理（甘特图、工期等），对"工单"来说冗余。

### Zammad

- R1 ✅
- R2 ❌ 回复（article）的 `content_type` 只有 `text/html` 与 `text/plain`，插图以 data URI 嵌入 HTML。依据：[Ticket articles API](https://docs.zammad.org/en/latest/api/ticket/articles.html)。
- R3 ✅ API 附件为 base64。
- R4 ❌ 官方明确不支持免登录查看工单："No. There is no possibility to do that…"，按 works as designed / wontfix 关闭（[zammad#603](https://github.com/zammad/zammad/issues/603)）；客户只能回邮件，或者用邮箱找回密码后登录。核心模型要求每张工单绑定一个客户账号。
- R5 ✅ 工单、article、tag、报表 API 都有。
- R6 ❌
- 结论：定位是对外客服，以邮件为中心，与"Agent 优先、Markdown"方向相反。

### osTicket

- R1 ✅
- R2 ❌ 富文本 HTML。
- R3 ✅ 客户端附件可配置。
- R4 ✅/🟡 **唯一原生带工单级免登录访问的候选**：通知邮件里带指向单张工单的 access link，"regardless of whether or not he or she is currently registered"；也可以凭"工单号 + 邮箱"查询，系统再把链接发到邮箱。依据：[osTicket Wiki: Managing End Users](https://github.com/osTicket/osTicket/wiki/Managing-End-Users)、[User Settings](https://docs.osticket.com/en/latest/Admin/Settings/Users.html)。但链接通过邮件分发，没有显式的过期或撤销管理，也有已知缺陷（[osTicket#4510](https://github.com/osTicket/osTicket/issues/4510)）。
- R5 ❌ 官方 API："For now, only ticket creation is supported"（[API Docs](https://docs.osticket.com/en/latest/Developer%20Documentation/API%20Docs.html)）。Agent 不能读、回复、打标签或统计。
- R6 ❌
- 结论：访客侧最接近，Agent 侧完全不行，出局。

### FreeScout

- R1 ✅（Laravel，轻量）。
- R2 ❌ HTML 邮件式会话。
- R4 🟡 付费模块 [End-User Portal](https://freescout.net/module/end-user-portal/)：客户凭邮箱魔法链接登录，看到的是"自己邮箱名下的所有会话"，不是"一张工单一个链接"；该模块有过认证绕过漏洞 CVE-2023-52268（1.0.65 前）。
- R5 🟡 需要付费的 [API & Webhooks 模块](https://freescout.net/module/api-webhooks/)（[API 文档](https://api-docs.freescout.net/)）；也有社区开源替代实现。
- R6 ❌
- 结论：以邮件为中心，不适合。

### Plane（社区版）

- R1 ✅
- R2 ❌ 富文本编辑器，存储为 HTML。
- R4 ❌ "Publish" 把项目发布为公开页面，可免登录**查看**；开启评论后访客需要**注册账号**才能评论。依据：[Publish projects](https://docs.plane.so/core-concepts/deploy)。匿名访问需求仍是开放的 feature request（[makeplane/plane#6347](https://github.com/makeplane/plane/issues/6347)）。
- R5 🟡 有 REST API（`X-API-Key`），覆盖 work item 等资源（[API 介绍](https://developers.plane.so/api-reference/introduction)）；部分高级能力（如 Intake 表单）偏商业版。
- R6 ❌

### Taiga

- R2 ✅ Markdown；R5 ✅ [REST API](https://docs.taiga.io/api.html)，附件 multipart 上传需 Bearer token。
- R4 ❌ External User 角色对匿名用户**只授予查看**，评论等写权限只对已登录的非成员生效。
- 结论：敏捷看板定位，访客写入不可行。

### Jira Service Management Data Center

- R1 🟡 Atlassian 已于 **2026-03-30 停止向新客户销售 Data Center**，2029-03-28 EOL（[官方公告](https://www.atlassian.com/licensing/data-center-end-of-life)）。新部门无法采购。
- R4 ❌ "login-free portal" 只允许免登录**建单**和浏览知识库；查看和跟进自己的请求仍需客户账号（客户不占 license）。依据：[Setting up login-free portal access](https://confluence.atlassian.com/spaces/SERVICEDESKSERVER/pages/1289420880/Setting+up+login-free+portal+access)。
- 结论：采购上不可行，直接排除。

### Peppermint

- Markdown 和附件都有，但**主仓库已于 2026-07-17 归档**（[Peppermint-Lab/peppermint](https://github.com/Peppermint-Lab/peppermint)），没有正式的外部 API（[#141](https://github.com/Peppermint-Lab/peppermint/issues/141)）。排除。

### 额外发现：Vikunja（轻量"分享链接"类工具里最接近的）

- 支持**无账号的 link share**，权限分 Read / Read & Write / Admin，可设密码；分享链接可通过 `/api/v1/shares/:share/auth` 换取 JWT 后调用 API，留下的评论用分享名标识。依据：[Sharing & Teams](https://vikunja.io/help/sharing-and-teams/)。**这是唯一现成的"凭链接走 API"机制**，满足 R6 的形态。
- 但：粒度是**项目**而非任务（只能一张工单开一个项目来硬套）；创建时没有过期选项（只有会话 JWT 的 TTL）；链接不可编辑；近期出现多起链接分享相关的安全公告（例如删除分享后 JWT 仍有效 72 小时、只读分享可拿到高权限分享的哈希，分别在 2.3.0 / 2.6.0 修复，另有一条尚未修复），见 [GHSA-96q5-xm3p-7m84](https://github.com/go-vikunja/vikunja/security/advisories/GHSA-96q5-xm3p-7m84)、[GHSA-8hp8-9fhr-pfm9](https://github.com/go-vikunja/vikunja/security/advisories/GHSA-8hp8-9fhr-pfm9)。描述是 HTML 编辑器，不是 Markdown。
- 结论：设计思路值得借鉴（分享链接 → 换短期 token → 走同一套 API），但不宜直接采用。

### 其他简要排除

- **Libredesk**：单二进制、API 完善，但只有邮件/聊天/WhatsApp 渠道，没有工单级访客链接（[libredesk](https://github.com/abhinavxd/libredesk)）。
- **Outline / Docmost / Nextcloud 共享链接 / HedgeDoc** 一类"分享链接 + 文档"工具：要么分享链接只读（Outline），要么能匿名编辑但没有回复、附件、标签、状态这些工单结构（HedgeDoc），要么只有文件上传没有工单（Nextcloud 文件投递链接）。都不是工单系统。

## 共同缺口：访客网关需要做什么

无论选哪个现成系统，都要外挂一个服务，至少包含：

1. 成员为某张工单签发访客链接（随机 token、过期时间、撤销），token 存在网关自己的库里（现成系统没有这张表）。
2. 访客网页：渲染工单说明和回复（Markdown 渲染、图片代理）、回复表单、附件上传（50MB 量级，需要流式转发）、外部文件引用的展示。
3. 访客 API：同一 token 可以按 JSON/Markdown 读取，也可以回复和上传（满足 R6）。
4. 用服务账号代写入后端，并在回复里标注"访客：xxx"，因为后端系统不知道访客是谁。
5. 权限隔离：确保 token 只能触达那一张工单（包括附件下载 URL 也要经网关代理，否则会绕过）。

这几项就是本平台的核心差异功能，工作量与后端选谁基本无关。

## 自建 vs 现成 + 网关（诚实评估）

- **现成 + 网关**：成员侧 UI、权限、附件存储、API、备份方案开箱即用；代价是要维护两套系统和一层映射（工单类型"任务/反馈"、标签、访客身份都要映射到后端概念），还要处理后端的 HTML/Markdown 差异、附件代理、后端升级带来的 API 漂移。Redmine 方案的成员 UI 是现成的，但较老旧。
- **薄自建**：数据模型就是 `GLOSSARY.md` 里那几个名词（工单、说明、回复、附件、外部文件引用、成员、访客链接），外加标签和统计查询；Markdown 原生存储，API 和访客 API 是同一套。主要成本在成员侧 Web UI（列表、筛选、编辑器、上传）。由于平台是 Agent 优先，成员 UI 可以做得很朴素。
- 判断：由于"访客网关"在两条路线里都要写，且它占了差异化工作量的大头，**薄自建的边际成本只是"成员 UI + 工单 CRUD + 统计"，大概率低于引入并长期运维一个重量级系统再做映射的成本**。如果决策者更看重成员侧立即可用，建议的备选是 **Redmine（CommonMark）+ 访客网关**，其次是 **Gitea/Forgejo + 访客网关**。
