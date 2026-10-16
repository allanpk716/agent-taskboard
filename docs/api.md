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
