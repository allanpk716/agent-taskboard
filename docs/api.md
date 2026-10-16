# HTTP API 约定与端点清单

`/api/v1` 之内的接口只做增量修改，破坏性变更开 `/api/v2`。本页记录通用约定，以及
spec 未定形状、由实现自拟的端点（orchestrator 决议第 8 条）；后续工单新增端点时在这里补一行。

## 通用约定

- 所有接口都在 `/api/v1` 之下；网页只调用公开 API。
- 错误一律写成 `{"error":{"code":"…","message":"…","hint":"…"}}`，`hint` 可选，告诉调用方下一步怎么做。
- 成功创建资源返回 `201`。
- 凭证三种，统一解析成一个 Viewer（`kind` 为 `member` / `guest`，成员带 `member` 与 `agent`，访客带 `guest_link`，见 spec「身份与凭证」模块）：
  1. **成员本人**：网页会话 cookie `tb_session`（HttpOnly + Secure + SameSite=Lax）。本人操作的唯一来源。
  2. **成员 Agent**：`Authorization: Bearer <Agent token>`，token 由 `POST /api/v1/me/agent-tokens` 签发（前缀 `tbag_`）。ADR 0003：用 token 的操作一律署名为「某成员 的 Agent · 名字」，永远不算本人。
  3. **访客**：访客链接 `/g/<密钥>` 的密钥直接作 Bearer（后续工单加入）。
- 请求同时带 Bearer 和 cookie 时按 Bearer 解析，且解析失败不回退到 cookie：凭证坏了应当报错（401），而不是悄悄当作本人。
- 401 错误码：`unauthenticated`（未登录/会话过期）、`invalid_token`（Agent token 无效或已撤销）。
- 撤销 Agent token 立即生效：每次请求都实时查库校验（token 只存哈希，无状态 JWT 不可用）。

## 会话与身份

| 方法与路径 | 说明 |
|---|---|
| `POST /api/v1/session` | 登录：`{"username","password"}` 换会话 cookie。错误 `invalid_credentials`(401)、`invalid_json`(400)。 |
| `GET /api/v1/session` | 当前凭证的身份（任何有效凭证；匿名 401 `unauthenticated`）。 |
| `DELETE /api/v1/session` | 登出（幂等 204），清除会话 cookie。 |

`GET /api/v1/session` 与登录成功响应的结构（访客形状后续工单加入）：

```json
{
  "viewer": {
    "kind": "member",
    "member": {"id": 1, "username": "chen", "name": "陈工", "is_admin": false},
    "agent": {"id": 3, "name": "笔记本"}
  },
  "inbox": "/api/v1/inbox"
}
```

- 网页会话（本人）：`viewer.agent` 为 `null`。
- Agent token：`viewer.agent` 为该具名 Agent `{id, name}`，`viewer.member` 是其所属成员。
- `inbox` 是成员收件箱地址（端点在后续工单实现）。

## Agent token（成员个人设置里的签发/撤销，ADR 0003）

| 方法与路径 | 说明 |
|---|---|
| `POST /api/v1/me/agent-tokens` | 签发一个具名 Agent token。**只接受网页会话**。 |
| `DELETE /api/v1/me/agent-tokens/{id}` | 撤销（立即生效，幂等 204）。**只接受网页会话**。 |

- 签发请求体 `{"name":"笔记本"}`；成功 `201`：

  ```json
  {"id": 3, "name": "笔记本", "token": "tbag_…", "created_at": "2026-10-16T09:00:00+08:00"}
  ```

  明文 `token` 只在签发响应里出现一次，之后任何接口都不回显（库中只存 SHA-256 哈希）。
- 撤销 Agent 后它的历史署名保留并标注「已停用」（时间线渲染见后续工单）；未撤销的同名不能重复，撤销后名字可复用。
- 错误码：
  - `401 unauthenticated` / `401 invalid_token`：凭证缺失或失效（撤销后的 token 再用就是 401）。
  - `403 web_session_required`：用 Agent token（或将来其他非网页会话凭证）发起签发/撤销。ADR 0003：Agent 不能给自己或别人签发凭证。
  - `422 name_required`：名字为空。
  - `409 agent_name_taken`：该成员名下已有同名、未撤销的 Agent。
  - `404 agent_token_not_found`：id 不存在、不属于当前成员，或路径不是数字（`/me/` 下只看自己的资源，防枚举）。

## 工单（#29：建单与读取，不用模板）

工单引用 `{id}` 接受两种写法：工单编号 `YYYYMMDD-NNN`（北京时间每天从 001 起，
超 999 自动变 4 位）或全局序号（可带 `#` 前缀，URL 里需写成 `%23`）。网页和 Agent
都用 `/t/<编号>` 引用工单（成员网页路由在后续工单实现）。工单不能删除。

| 方法与路径 | 说明 |
|---|---|
| `POST /api/v1/tickets` | 建单（成员/成员 Agent）。 |
| `GET /api/v1/tickets/{id}` | 读一张工单；`?format=markdown` 返回给 Agent 读的 Markdown。 |

- 建单请求体 `{"kind", "title", "brief_md", "labels"}`：
  - `kind` 可省（默认 `task`），只能是 `task`（任务）或 `report`（反馈）；用模板创建时由模板决定（#34）。
  - `title` 必填；`brief_md` 是不用模板时的一整块说明（可空）。
  - `labels` 至少要有一个 `维度:值` 形式的维度标签；且每个标签（含不带维度的）
    必须是字典中已有的值（种子维度：产品线、系统、机型、部门、算法、立项、子项目；
    初始值见 `internal/labels/seeds.go`）。完整的标签操作（allow_new、别名）在 #31。
  - `assignee`（建单时指派与 `share_text`）预留给 #36，本版忽略。
- 建单响应 `201`：`{number, seq, url}`（`url` 即 `/t/<编号>`）。
- 建单错误码：`422 title_required`、`422 invalid_kind`、
  `422 dimension_label_required`（hint 给出字典里的维度标签示例）、
  `422 unknown_label_value`（附 `suggestions`：同维度里规范化后相等或互相包含的值，
  规范化 = 忽略大小写、空格、连字符和下划线）。
- 工单编号与全局序号在同一事务内分配；建单同时写入一条 `created` 时间线事件，
  并推进全局变更序号（事件里的 `change` 字段，与事件写入同事务）。
- `GET` 响应（成员视角）：`number, seq, url, kind, title, status, close_reason,
  template, creator, assignee, waiting_on, labels, brief, viewer, timeline`；
  - 作者结构统一 `{member: {id, name}, agent: {id, name}|null}`，本人操作 `agent` 为 `null`；
    时间线事件带 `seq`（单内递增）、`change`、`at`、`actor`、`type`。
  - `viewer`：`{kind, member, agent, role(assignee|creator|member|guest), your_turn, can}`；
    `can` 是完整能力表 `reply/upload/edit_brief/label/close/reopen/manage_guest_links/deliver/assign`。
  - `waiting_on` 由时间线推导（尚无回复且未指派 → `department`/尚未指派；回复参与推导在 #30，指派在 #36）。
  - 访客视角的可见性过滤（无 `labels`、跳过标签事件）在 #32 加入。
- `?format=markdown` 返回 `text/markdown`：头部信息、说明、附件（占位）、时间线和
  「下一步」；署名按 ADR 0003：成员 `陈某`，成员 Agent `陈某 的 Agent · 名字`。
