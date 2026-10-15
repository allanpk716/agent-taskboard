# 薄自建，不以现成工单系统为后端

平台的核心差异是**访客链接**：针对单张工单、免登录即可查看和回复、支持过期与撤销，访客的 Agent 也能通过 HTTP API 操作。调研发现，没有现成系统原生支持这一点。最接近的 Redmine 和 Gitea/Forgejo 都需要外挂一层“访客网关”，而这层网关正是平台的主体。因此 MVP 采用薄自建：单一服务，访客链接作为一等概念进入数据模型。外部 Agent 只面对一套 HTTP API，成员和访客的区别只在凭证。平台由本部门借助 Agent 维护，我们判断维护一份精简代码库，比跟进上游升级并维护适配层更可控。

## Considered Options

- **Redmine + 访客网关**：工单、CommonMark、附件、REST 和统计都现成。但需要多部署一套 Ruby 应用；访客回复会显示成网关的代理用户；Agent 要面对两套 API，若由网关统一包装，网关本身就接近自建了。如果公司已有可复用的 Redmine 实例，这会是首选，但目前没有。
- **Gitea/Forgejo + 访客网关**：问题与上一条相同，而且统计能力更弱。
- **osTicket**：有工单级免登录链接，但 API 只能建单，正文是 HTML，因此排除。

借鉴而不采用：Vikunja 的“分享链接换 token 走 API”机制。

## Consequences

- HTTP API 是唯一的事实来源。将来提供给外部 Agent 的 CLI、MCP server 和配套技能，都是对这套 API 的薄封装，所以 API 的设计要能一对一映射为 CLI 子命令或 MCP tool。
