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
  3. **访客**：访客链接 `/g/<密钥>` 的密钥直接作 Bearer（前缀 `tbg_`，见「访客链接（#32）」）。
- 请求同时带 Bearer 和 cookie 时按 Bearer 解析，且解析失败不回退到 cookie：凭证坏了应当报错（401），而不是悄悄当作本人。Bearer 先按 Agent token 解析、再按访客链接密钥解析。
- 401 错误码：`unauthenticated`（未登录/会话过期）、`invalid_token`（Agent token 或访客链接密钥未知）、`link_revoked`（访客链接已撤销）、`link_expired`（访客链接已过期）。访客链接的撤销与过期即时生效：每个请求都实时查库（密钥只存哈希）。
- 撤销 Agent token 立即生效：每次请求都实时查库校验（token 只存哈希，无状态 JWT 不可用）。

## 会话与身份

| 方法与路径 | 说明 |
|---|---|
| `POST /api/v1/session` | 登录：`{"username","password"}` 换会话 cookie。错误 `invalid_credentials`(401)、`invalid_json`(400)。 |
| `GET /api/v1/session` | 当前凭证的身份（任何有效凭证；匿名 401 `unauthenticated`）。 |
| `DELETE /api/v1/session` | 登出（幂等 204），清除会话 cookie。 |

`GET /api/v1/session` 与登录成功响应的结构（成员）：

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
- `inbox` 是成员收件箱地址 `GET /api/v1/inbox`（#36 起已实现，见「等待方与收件箱」）。

凭证是访客链接密钥时（#32），`GET /api/v1/session` 返回链接指向的工单（贴一条链接就能干活，
spec 用户故事 84），没有 `inbox`：

```json
{
  "viewer": {
    "kind": "guest",
    "member": null,
    "agent": null,
    "link_note": "检验科-张三",
    "link_expires_at": "2026-11-15T09:00:00+08:00",
    "can_edit_brief": false
  },
  "ticket": {"number": "20261016-001", "seq": 1, "title": "……", "api": "/api/v1/tickets/20261016-001"}
}
```

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

## 工单（#29：建单与读取；#30：回复、修订、关闭与时间线）

工单引用 `{id}` 接受两种写法：工单编号 `YYYYMMDD-NNN`（北京时间每天从 001 起，
超 999 自动变 4 位）或全局序号（可带 `#` 前缀，URL 里需写成 `%23`）。网页和 Agent
都用 `/t/<编号>` 引用工单（成员网页路由在后续工单实现）。工单不能删除。

| 方法与路径 | 说明 |
|---|---|
| `POST /api/v1/tickets` | 建单（成员/成员 Agent；访客 403 `members_only`，访客建单走反馈入口 #35）。 |
| `GET /api/v1/tickets/{id}` | 读一张工单；`?format=markdown` 返回给 Agent 读的 Markdown。 |
| `PATCH /api/v1/tickets/{id}` | 修订标题/说明（创建者一方；持「可填写说明」链接的访客；乐观锁）。 |
| `POST /api/v1/tickets/{id}/replies` | 回复（成员、成员 Agent、访客）；`deliver: true` 仅指派人一方（#36）。 |
| `POST /api/v1/tickets/{id}/close` | 关闭（任何成员；访客 403 `members_only`）。 |
| `POST /api/v1/tickets/{id}/reopen` | 重新开启（任何成员；访客 403 `members_only`）。 |
| `POST /api/v1/tickets/{id}/assign` | 改派（#36，仅成员；记 `assigned` 事件，附改派版 `share_text`）。 |
| `GET /api/v1/tickets/{id}/timeline?after=N` | 时间线增量读取。 |
| `POST /api/v1/tickets/{id}/attachments` | 上传一个待用附件（multipart；见「附件（#33）」）。 |
| `GET /api/v1/attachments/{att_N}/{filename}` | 附件下载（与工单相同的凭证，或访客链接的签名地址）。 |
| `GET /api/v1/inbox` | 收件箱（#36，仅成员）：轮到我的开启工单 + 原因 + `read_new`。 |

（labels 在 #31；guest-links 见下。）

- 建单请求体 `{"kind", "title", "brief_md", "labels", "assignee?"}`（不用模板）或
  `{"template", "title", "fields", "labels", "assignee?"}`（用模板，见下面的「模板（#34）」）：
  - `kind` 可省（默认 `task`），只能是 `task`（任务）或 `report`（反馈）；用模板创建时由模板决定（请求里的 `kind` 被忽略）。
  - `title` 必填；`brief_md` 是不用模板时的一整块说明（可空）；模板工单的说明由 `fields` 按字段拼成。
  - `labels` 至少要有一个 `维度:值` 形式的维度标签；且每个标签（含不带维度的）
    必须是字典中在用的标准值（种子维度：产品线、系统、机型、部门、算法、立项、子项目；
    初始值见 `internal/labels/seeds.go`）。别名与 allow_new 见下面的「标签、字典与别名（#31）」。
    用模板创建时，模板的预设标签与维度字段选中的值会自动并进来（预设标签、维度字段值
    都要满足同样的字典校验；「不确定」不打标签）。
  - `assignee`（#36）可写成员 id 或姓名；写成成员 Agent（署名写法或 Agent 名字）
    返回 `422 assignee_must_be_member`（成员 Agent 不能被指派，hint 列出可选成员）；
    查无此人返回 `422 unknown_member`。建单时指派会在 `created` 事件后记一条
    `assigned{from: null, to}` 事件。
- 建单响应 `201`：`{number, seq, url, warnings, assignee, waiting_on, share_text}`
  （`url` 即 `/t/<编号>`）。`warnings` 是用模板建单时「缺必填」的逐项提醒
  `{field, message}[]`——工单照常创建，只在响应里提醒（spec 用户故事 6）；不用模板或
  必填全填时为空数组。`assignee` 为成员或 null；`share_text` 只在指派给**他人**时非空，
  可直接转发：`李工：给你派了一张工单「整理对照表」，让你的 Agent 读一下
  /t/20261016-001（它也会出现在你的收件箱里）`。
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
    `role`：本人是指派人 → `assignee`（自派自时优先算指派人），否则创建者一方 → `creator`，
    其他成员 → `member`，访客 → `guest`（#36）。`your_turn` = 等待方轮到的成员是不是本人
    （成员按成员 id，访客看等待方是不是访客）。
    访客：`member`/`agent` 为 `null`，另带 `link_note`/`link_expires_at`；能力表：`reply`/`upload` 需工单开启，
    `edit_brief` 需开启且链接带「可填写说明」，其余恒 false。
  - `waiting_on` 由时间线推导（规则见下「等待方与收件箱（#36）」），不单独存储。
  - 访客视角的可见性过滤（#32）：响应里没有 `labels` 字段，时间线跳过标签事件
   （事件序号会跳号，这是已接受的）；访客访问其他工单一律 404 `ticket_not_found`
   （防枚举）。`?format=markdown` 同样过滤（无标签行、无标签事件）。
- `?format=markdown` 返回 `text/markdown`：头部信息（含指派人，#36）、说明、附件（占位）、
  时间线和「下一步」；署名按 ADR 0003：成员 `陈某`，成员 Agent `陈某 的 Agent · 名字`。
  时间线里回复单独成节（`### [seq] 时间 · 作者 回复|**交付**` + 正文），
  说明修订/关闭/重开等事件写成 `- [seq] 时间 · 作者 事件文案`（如
  `修订说明（第 2 版）`、`关闭工单（重复：与 20261016-001 重复）`、
  `指派给 李工` / `改派：李工 → 赵工`）；标签事件写成「打上标签 X」「摘掉标签 Y」。
- 末尾「下一步（给 Agent 的操作指引）」（#36，照原型 `guidance()`）按
  角色×轮次生成，代码块可直接复制执行：已关闭（可重开时给 reopen 请求）；
  指派人且轮到我（退回/提问/访客回复时先读 [seq]；提问、交文件、
  `deliver: true` 交付三种请求，交付后不关闭工单）；发起人且轮到我
  （已交付 → 验收通过 `close{done}` / 不通过普通回复两种；否则答复）；
  访客且轮到我（回复 + `author_name`）；其余成员查 `GET /api/v1/inbox`
  或增量时间线。

### 回复（#30）

- 请求体 `{"body_md": "…", "attachment_ids?": ["att_N", …], "author_name?": "…", "via_agent?": false}`；
  空白正文且无附件引用返回 `422 empty_reply`（有附件即可通过）。
- `attachment_ids` 与正文里的 `att:` 引用（`![插图](att:att_N)` 或 `[文件](att:att_N)`）
  合并去重后在同一事务里认领（两步上传的第二步）：每个必须存在、待用（pending）、
  且是**本次凭证上传**的——同一成员（本人或其任意 Agent）算同一方，访客按链接算——
  否则 `422 attachment_not_usable`。认领后附件挂到「回复 N」上，回复事件带
  `attachment_ids`。插图（正文插图语法引用的图片文件，按扩展名判定）与附件
  （`attachment_ids` 与其余引用）分开计数，超过每帖上限返回
  `413 attachment_count_exceeded`（见「附件（#33）」）。
- `author_name` 与 `via_agent` 是访客的自报信息（#32，仅访客凭证采纳）：平台原样存下、
  不经核实，回复的作者结构带 `link_note`（链接备注）与这两项；成员的自报不生效
  （署名由凭证决定，ADR 0003）。
- 成员（含成员 Agent）在已关闭的工单上仍可回复——那不会重开工单；重新开启是显式
  动作。访客链接在关闭的工单上只读：回复返回 `409 ticket_closed`，重新开启后恢复。
  `deliver: true`（#36）是「做完了，请验收」的标记，只限指派人一方（本人或其
  Agent，且工单需开启），其他人用返回 `403 deliver_assignee_only`（hint：去掉
  deliver 作为普通回复；该你做就请发起人改派）。**交付不改变工单状态**，由
  发起人验收：通过就 `close{done}`，不通过就普通回复写明问题，工单自动退回
  指派人（等待方推导）。
- 成功 `201`：`{reply_no, timeline_seq, deliver, author, waiting_on}`；
  `author` 是展示名（`陈工` / `陈工 的 Agent · 笔记本`），
  `waiting_on` 结构同 GET（由时间线推导，见下）。
- 时间线记一条 `reply` 事件（`reply_no`、`body_md`）。

### 修订标题/说明（#30；#34 起支持按字段修订）

- 请求体 `{"title?": "…", "brief_md?": "…", "fields?": {…}, "base_revision": N}`，
  只需传改动的部分。`brief_md` 只用于非模板工单；模板工单用 `fields` 传**改动的字段**
  （键是小节标题（问句）原文，未传的字段保持不变），两类修订按工单类型互斥：
  用错时分别返回 `422 fields_without_template` / `422 brief_md_with_template`。
- `base_revision` 必填（`422 base_revision_required`），不等于当前版本时返回
  `409 revision_conflict`（附 `current_revision`，重读合并后再提交）。
  只有 title 或什么都没改的修订同样推进版本号。
- 权限：创建者本人及其任意 Agent（其他成员 `403 not_brief_owner`，用回复补充内容）；
  持「可填写说明」链接的访客（工单需开启；无权限的链接 `403 link_cannot_edit_brief`，
  关闭后 `409 ticket_closed`）；反馈入口的提交者视同创建者，靠提交响应里那条带
  「可填写说明」的访客链接行使这项权限（入口在 #35 落地）。修订不再做必填校验，
  但标题改成空白返回 `422 title_required`。访客修订可自报 `author_name`/`via_agent`
  （记在修订事件的作者上）。
- 成功 `200`：`{"revision": N}`；每次修订记一条 `brief_revised` 事件（带 `revision`）。
  字段修订的取值仍做结构校验：键必须是模板（创建时版本）里的字段（`422 unknown_field`）、
  单选值必须在选项里（`422 invalid_field_option`）、维度值必须是字典里在用的值
  （`422 unknown_label_value`；「不确定」除外）；但**不做必填校验**，可以把字段清空。
  正文里的 `att:` 引用同样认领（挂到「说明」上）：只认领其中待用的，已挂在别处的
  可以重复引用；待用的必须是本次凭证上传的（`422 attachment_not_usable`）。

### 关闭与重新开启（#30）

- 关闭请求体 `{"reason": "done|wontfix|duplicate", "duplicate_of?": "编号或序号"}`；
  任何成员（含成员 Agent）都可关闭。缺失/非法 reason → `422 invalid_close_reason`；
  `duplicate` 必须带 `duplicate_of`（`422 duplicate_of_required`；写成其他类型 →
  `422 invalid_duplicate_of`），且必须指向存在的工单（否则 `404 ticket_not_found`）；
  重复关闭 → `409 already_closed`。
- 成功 `200`：`{status: "closed", close_reason, duplicate_of?}`；记一条 `closed` 事件
  （`reason`，重复时 `duplicate_of` 为原工单编号）。
- 重开 `POST …/reopen` 任何成员可用；未关闭时 `409 not_closed`。成功 `200`：
  `{status: "open", waiting_on}`，记一条 `reopened` 事件；重开后可继续回复，
  轮次按最后一条相关回复推导。

### 时间线增量读取（#30）

- `GET /api/v1/tickets/{id}/timeline?after=N` 返回
  `{events, cursor, waiting_on, your_turn, status}`：`events` 只含 `seq > N` 的事件
  （省略 after 即全量），`cursor` 是该单最后一条事件的 `seq`，客户端存下来
  下次作 `after`。追平后再取：空列表、cursor 不变。
- `after` 不是非负整数 → `422 invalid_cursor`。
- 事件结构：`{seq, change, at, actor, type}` + 按类型出现的附加字段
  （`reply` 带 `reply_no`/`body_md`/`deliver`，`brief_revised` 带 `revision`，
  `closed` 带 `reason`/`duplicate_of`，`assigned` 带 `from`/`to`（from 为 null 表示
  创建时指派，否则是改派），`guest_link_created`/`guest_link_revoked` 带
  `note`，`guest_link_extended` 带 `note` 与 `expires_at`）。时间线只追加：每条都记录
  作者与时间，旧事件永不改动。访客可见性过滤（跳过标签事件）在 #32 已生效；
  访客的 `cursor` 是它可见的最后一条事件 `seq`（跳过的序号不会当作增量重发）。
- `waiting_on` 的推导见下「等待方与收件箱（#36）」。

## 指派与改派（#36）

- `POST /api/v1/tickets/{id}/assign {"assignee": "成员 id 或姓名"}`（仅成员；
  访客 403 `members_only`）。改派给当前指派人 → `422 already_assigned`；
  写成成员 Agent → `422 assignee_must_be_member`；查无此人 → `422 unknown_member`
  （hint 列出可选成员）。
- 成功 `200`：`{assignee, waiting_on, share_text}`；`share_text` 只在改派给**他人**时
  非空：`赵工：工单「整理对照表」转给你了，让你的 Agent 读一下 /t/20261016-001`。
- 记一条 `assigned{from, to}` 事件（文案「指派给 X」/「改派：X → Y」）并推进变更序号。
  改派后，原指派人的回复不再参与等待方推导，工单随推导从原指派人的收件箱移到
  新指派人的收件箱。

## 等待方与收件箱（#36）

等待方与收件箱原因都从时间线推导，不存会漂移的状态；唯一真相在时间线
（实现：`tickets.WaitingOnOf`，#37 的查询同样复用）。规则照 prototype/member-handoff
的 `turnOf`：

```
已关闭                        → 无等待方（party=none）
尚无回复 或 最后是访客回复     → 指派人（未指派 → department）
任务 且 创建者是成员 且 创建者≠指派人（“有对手”）：
    指派人一方回复             → 创建者（原因：已交付待验收 若带 deliver，否则 指派人回复了）
    创建者一方回复             → 指派人（原因：此前有交付则 发起人退回修改，否则 发起人回复了）
    第三位成员回复             → 不改变轮次（不参与相关回复集）
其余情况（反馈、自派自）成员回复 → 访客
成员 Agent 的回复算其所属成员一方；改派后按当前指派人重算，原指派人的回复不再参与
```

- `waiting_on = {party, member, text, reason}`；`text` 示例：`李工（指派人）`、
  `王工（发起人）`、`访客`、`本部门（未指派）`、`无（已关闭）`。未指派时原因只写
  「尚未指派」或「访客回复了」（ticket-query 原型的修正：不能写「新指派给你」）。
- 已接受边界：任务同时发了访客链接时，指派人的回复一律算作交给创建者；
  重新开启后按最后一条相关回复推导，Markdown「下一步」提示重开者回复说明原因。

### GET /api/v1/inbox（仅成员）

- 访客 403 `members_only`（决议 4），匿名 401。
- 列出「现在轮到我」的**开启**工单，按工单序号；同一成员名下所有 Agent 共用一份
  （Agent 定时轮询它即可，不靠人口头转告）；未指派的工单不出现在任何人的收件箱里
  （要用查询接口找，#37）。响应：`{member, count, items}`，每项：
  - `{number, title, role(assignee|creator), reason, since_seq, read_new, read_full}`。
  - `reason` 文案：新指派给你 / 已交付，待你验收 / 指派人回复了，待你答复 /
    发起人退回修改 / 发起人回复了 / 访客回复了。
  - `since_seq` 是推导所依据的那条回复的事件 seq（无相关回复时为 null，即新指派）。
  - `read_new` = `/api/v1/tickets/<编号>/timeline?after=<我在该单最后一条任意事件的 seq>`
    （不只回复——要读的是「上次我看过之后别人做了什么」）；`read_full` =
    `/api/v1/tickets/<编号>?format=markdown`。

## 幂等（#30）

对所有会产生新内容的请求（建单、回复、说明修订——成员与访客凭证都一样；
反馈入口提交在 #35 起支持；附件上传随后续工单接入）支持 `Idempotency-Key` 请求头：

- 记录按（key、凭证、端点）隔离：同一 key 换了凭证（另一成员、另一个 Agent）
  或换了端点，都是新请求。
- 首次成功响应（2xx）存快照（含状态码与响应体）；重放返回首次结果，逐字节
  相同，不产生新内容。失败的请求不存快照，修正后重试照常执行。
- 同名请求头留空视为没带；幂等表在数据库里（迁移 `0004`）。


## 模板（#34）

成员用 Markdown 维护模板；成员和 Agent 都按模板的字段建单、填写。任何成员
（含成员 Agent）都能新建和修改模板；每次修改产生一个新版本并记录修改人；
模板只能停用、不能删除；用模板建的工单保存创建时那个版本的字段定义，
模板以后修改不影响已有工单。

### 模板语法（来自 #9，格式错误保存失败并报出行号）

```markdown
---
名称: Bug 反馈
类型: 反馈
预设标签: [待初筛]
---
## 哪台仪器？ {维度:机型 必填}
在仪器背面铭牌上可以看到型号；不确定可以选“不确定”。
## 你做了哪些操作？ {必填}
按顺序写，一步一行。
> 示例：1. 开机自检完成 2. 进入质控界面 3. 点击“开始”
```

- YAML 头（首尾 `---`）：键只认 `名称`、`类型`（任务/反馈）、`预设标签`（`[a, b]`），其余键报错。
- 每个 `##` 小节是一个字段：标题写成问句（**问句原文就是 `fields` 的键**），小节正文是
  引导说明，`> 示例：` 起的引用行是占位示例。
- 花括号标记写在标题行末尾：`{必填}`、`{选填}`、`{单选: A | B}`、`{维度:X 必填}`
  （可与 `必填`/`选填` 组合）；不写时默认多行文本、选填。
- 小节标题为「给 Agent 的说明」的一段不是字段，是一段说明；会出现在模板的填写指南里，
  也会出现在按该模板建的工单 Markdown 的「下一步」里（spec 用户故事 35）。
- 字段标题重复、标记无法识别、YAML 头缺键等格式错误：保存失败（422
  `template_format_error`），message 里带行号（如「第 3 行：类型只能是 任务 或 反馈」）。

### 端点

| 方法与路径 | 说明 |
|---|---|
| `GET /api/v1/templates` | 模板列表（含已停用，各项带 `active`），成员与成员 Agent 可读。 |
| `POST /api/v1/templates` | 新建：`{"key": "check", "markdown": "模板原文"}`，成功 201。 |
| `GET /api/v1/templates/{key}` | 读当前版本：字段定义、`agent_note`、模板原文 `markdown`、`version`、`updated_by`。 |
| `GET /api/v1/templates/{key}?format=markdown` | 给 Agent 的填写指南（`text/markdown`）：建单请求示例、逐字段问句/引导/示例/候选值、「给 Agent 的说明」。 |
| `PATCH /api/v1/templates/{key}` | 修改：`{"markdown": "新原文"}`，产生新版本（版本号 +1，记录修改人）。 |
| `POST /api/v1/templates/{key}/deactivate` | 停用（幂等 200；模板不能删除，停用后不能再用它建单，读取仍可见）。 |

- 新建/修改请求带 `Idempotency-Key` 时同样去重。
- `key` 是模板的稳定标识（出现在工单的 `template` 字段里），不能含空格或 `/`，
  且不能复用已存在的 key（409 `template_key_taken`）。
- 字段定义结构：`{key, title, guide, example, required, type, options[], dimension?}`；
  `type` 为 `text | radio | dimension`。**维度字段的 `options` 是读取时从标签字典现取的
  候选值 + 「不确定」**（字典会变，不缓存在模板里）。
- 错误码：`422 template_format_error`（带行号）、`422 template_key_required`、
  `422 invalid_template_key`、`422 markdown_required`、`409 template_key_taken`、
  `404 template_not_found`。

### 按模板建单与修订

- `POST /api/v1/tickets {"template", "title", "fields", "labels"}`：
  - `fields` 的键是模板小节标题（问句）原文；值是填写的文字。
  - 缺必填项**不拦截**：工单照常创建，响应 `warnings` 逐项列出；维度字段选「不确定」
    算已填、且不打该维度的标签。
  - 结构错误会拦：未知字段键 `422 unknown_field`、单选值不在选项里
    `422 invalid_field_option`、维度值不在字典里 `422 unknown_label_value`（附 suggestions）。
  - 模板的预设标签自动打上；维度字段选中的值同时成为标签并以文字写进说明
    （访客看不到标签）；类型由模板决定。
  - 未知模板 404 `template_not_found`；已停用模板 422 `template_inactive`。
- `GET /api/v1/tickets/{id}` 的模板工单：`template` 为模板 key；`brief.fields` 为字段值
  map，`brief.markdown` 为按字段拼好的说明（`### 问句` + 内容，只拼已填字段）；
  `brief.template` 同 `template`；`brief.template_fields` 是创建时版本的字段定义
  （与模板接口同形，含未填字段，按模板顺序），访客网页据此按问句渲染说明气泡、
  对未填字段显示「（未填写）」（#38）；非模板工单没有这个字段。
- `PATCH /api/v1/tickets/{id} {"fields": {改动的字段}, "base_revision": N}`：
  只改动传入的字段，其余保持不变；每次修订记一条 `brief_revised` 事件（见上）。
- 预置三份模板（spec「初始数据」）：`bug-report`（Bug 反馈，预设 `待初筛`）、
  `assign-task`（分配任务）、`question`（问题咨询）；模板是运行时数据，上线后可随时修改。


## 标签、字典与别名（#31）

标签可以带维度（`维度:值`，如 `机型:UD-2300`）或不带维度（如 `待初筛`）；同一维度可以打多个值。
成员和成员 Agent 都能打标签；访客 403 `members_only`（决议 4：访客调仅成员的接口统一这个码）。

| 方法与路径 | 说明 |
|---|---|
| `POST /api/v1/tickets/{id}/labels` | 打标签 / 摘标签：`{"add":[…],"remove":[…],"allow_new":false}`。 |
| `GET /api/v1/labels[?dimension=]` | 字典列表：每个值带 `aliases`，另列出不带维度的标签。 |
| `GET /api/v1/labels/resolve?q=&dimension=` | 俗称换标准值，注明匹配方式 `exact` / `alias` / `fuzzy`。 |
| `POST /api/v1/labels/aliases` | 添加别名（任何成员）；冲突 409。 |
| `PATCH /api/v1/labels/aliases` | 改别名的指向（**管理员**）。 |
| `POST /api/v1/labels/dimensions` | 添加新维度（**管理员**；新维度只能由管理员加）。 |
| `POST /api/v1/labels/rename` | 改名（**管理员**），改写历史工单上的标签。 |
| `POST /api/v1/labels/merge` | 合并（**管理员**），改写历史工单、别名改指向、from 停用。 |
| `POST /api/v1/labels/deactivate` | 停用（**管理员**，幂等），历史工单保留。 |

### 打标签 / 摘标签（POST /api/v1/tickets/{id}/labels）

- `add` / `remove` 里的写法先解析再落库：
  1. **exact**：与字典值规范化后相同（忽略大小写、空格、连字符、下划线）；
  2. **alias**：被同维度恰一个在用值认领（裸写法则要求全字典恰一个），多义不换；
  3. 都不是且未带 `allow_new` → 422 `unknown_label_value` + `suggestions`（同范围里规范化后互相包含的近似值，最多 5 个）。
- `allow_new: true` 时新建未知值（响应的 `created_dictionary_values` 回显）；维度必须已存在，
  否则 422 `unknown_dimension`（新维度只能由管理员加）。不带维度的标签同样可以新建。
- 响应 200：`{labels, changed: {added, removed, alias_resolved}, created_dictionary_values}`，
  `alias_resolved` 注明「由别名 X 换成标准值」。
- 每次（净）增删记一条 `label_added` / `label_removed` 时间线事件（带 `label` 字段），
  并推进全局变更序号；打已在单上的标签、摘不在单上的标签都是幂等成功，不记事件。
- `remove` 解析不到的写法 422 `unknown_label_value`；解析得到但不在单上则幂等成功
  （决议：摘不存在的标签是幂等成功，不是 422）。停用值可以摘（工单上可能还带着）。
- `维度:*` 是查询用的通配，不能上工单：422 `wildcard_not_allowed`。
- 已停用的值不能再新打：422 `label_deactivated`（建单同样）。
- 注意：建单（POST /api/v1/tickets）的 `labels` 仍是严格校验（必须是字典里的标准值，
  不做别名替换），别名只在打标签 / resolve / 筛选（#37）里生效。

### 字典列表与 resolve

- `GET /api/v1/labels`：`{dimensions: [{name, values: [{value, label, aliases}]}], plain: [不带维度的标签]}`；
  指定 `dimension` 时只列该维度并省略 `plain`；未知维度 422 `unknown_dimension`。停用的值不列出。
- `GET /api/v1/labels/resolve`：`{q, matches: [{label, via, alias?}]}`，按 exact → alias → fuzzy
  排序（同级按字典顺序），`alias` 字段是命中的那个别名；fuzzy 要求规范化后 ≥2 字符且互相包含。
  `q` 可以是裸写法、别名或 `维度:值`；`dimension` 参数限定范围。无命中时 `hint` 指向
  allow_new / 加别名。缺 `q` → 422 `q_required`；未知维度 → 422 `unknown_dimension`。

### 别名（POST /api/v1/labels/aliases）

- 请求体 `{"label": "维度:值", "alias": "俗称"}`（任何成员，含成员 Agent）；
  成功 201 `{label, aliases}`。别名只能挂在 `维度:值` 标准值上，不能挂在不带维度的标签上。
- 冲突按规范化比较（忽略大小写、空格、连字符）：
  - 已被同维度另一个标准值（或它的别名）占用 → 409 `alias_taken`；
  - 已挂在这个值上（或与标准值本身写法相同）→ 409 `alias_exists`。
- 同一个别名可以被不同维度的值分别认领（跨维度不冲突）；但裸写法只有唯一认领时才会自动换。

### 治理（仅管理员；非管理员 403 `admin_required`）

- `POST /api/v1/labels/dimensions {"name"}`：新维度；重复 409 `dimension_taken`，空名 422 `name_required`。
- `POST /api/v1/labels/rename {"label", "to"}`：两端维度必须一致（或都是不带维度），目标已存在
  409 `value_taken`（提示改用合并）。字典、别名（FK 级联）与历史工单上的标签一并改。
- `POST /api/v1/labels/merge {"from", "into"}`：历史工单上的 `from` 改写成 `into`（同单两值去重），
  `from` 的别名改指向 `into`，`from` 停用（字典不物理删除）。响应 `{from, into, tickets_updated}`。
  时间线事件只追加，保留当时的原文（决议 7）。
- `POST /api/v1/labels/deactivate {"label"}`：幂等；历史工单保留该值，建单与打标签被拒
  （422 `label_deactivated`），字典与 resolve 不再列出。
- `PATCH /api/v1/labels/aliases {"alias", "label"}`：把该维度里已有的别名改指向另一个标准值；
  别名不存在 404 `alias_not_found`。

## 访客链接（#32）

成员把一张工单交给其他部门：创建访客链接后，对方（人或 Agent）只凭这条链接就能
查看和回复。链接地址 `/g/<密钥>`，密钥（前缀 `tbg_`）直接作 `Authorization: Bearer`。
一张工单可以有多条链接，每条都有成员写的备注（如「检验科-张三」）；任何成员
（含成员 Agent）都能为任意工单创建、撤销、延期。

| 方法与路径 | 说明 |
|---|---|
| `POST /api/v1/tickets/{id}/guest-links` | 创建：`{"note", "valid_days"?, "expires_at"?, "can_edit_brief"}`。 |
| `GET /api/v1/tickets/{id}/guest-links` | 列表（任何成员；**不回显密钥**）。 |
| `DELETE /api/v1/tickets/{id}/guest-links/{链接id}` | 撤销（立即生效，幂等 204）。 |
| `POST /api/v1/tickets/{id}/guest-links/{链接id}/extend` | 延期：`{"valid_days"|"expires_at"}`（二选一，必填其一）。 |

- 创建：`note` 必填（422 `note_required`）；有效期 `valid_days: 7|30|90` 或
  `expires_at`（`YYYY-MM-DD` 按北京时间当天 00:00 起算，或 RFC3339 时刻）**二选一**，
  都不填时默认 30 天，**没有永久有效的选项**（显式 `valid_days: 0`、非法天数、
  `expires_at` 不晚于现在或格式错误分别返回 422 `invalid_valid_days` /
  `invalid_expires_at`）。`can_edit_brief` 授予「可填写说明」权限（默认 false）。
- 创建成功 `201`：`{id, url: "/g/<密钥>", note, expires_at, can_edit_brief}`。
  完整密钥只在创建响应里出现一次（库中只存 SHA-256 哈希）；列表项是
  `{id, note, expires_at, revoked_at?, can_edit_brief, created_by, created_at}`。
- 创建、撤销、延期各记一条时间线事件（`guest_link_created` / `guest_link_revoked` /
  `guest_link_extended`，都带 `note`；延期另带新 `expires_at`），并推进全局变更序号。
  这些事件对访客也可见（访客看不到的只有标签事件）。
- 撤销立即生效：之后用这条密钥的任何请求 401 `link_revoked`。已撤销的再撤销是
  幂等 204，不重复记事件。
- 延期把有效期改到新时刻；**已撤销或已过期的链接不能延期**（409 `link_not_extendable`，
  hint 建议创建新链接）。工单关闭时链接变只读但可以延期（重开后恢复可回复）。
- 链接 id 不属于这张工单（或不存在的）→ 404 `guest_link_not_found`，路径不是数字也按
  404 处理。
- 过期即时生效：到期时刻含边界（`now >= expires_at` 即失效，401 `link_expired`）。

### 访客视角

- 凭证：`Authorization: Bearer tbg_…`。`GET /api/v1/session` 返回链接指向的工单
  （见上文「会话与身份」的访客形状）。
- 可见：标题、说明、全部回复、附件（下载地址是与该链接绑定的签名地址，见「附件（#33）」）、
  指派人、状态变更和关闭原因、访客链接事件。不可见：`labels` 字段和标签事件（事件序号会跳号，已接受）。
- 访问其他工单一律 404 `ticket_not_found`（读、时间线、回复、修订都一样，防枚举）。
- 回复：自报 `author_name` / `via_agent`；工单关闭后 409 `ticket_closed`，重开后恢复。
  作者结构：`member`/`agent` 为 `null`，带 `link_note`、`author_name`、`via_agent`
  （与成员 Agent 的平台认证署名在结构上区分）。Markdown 署名：
  `张三（经「检验科-张三」链接）`、`检验科-张三（访客）的 Agent（自报）`、
  不自报时 `访客（经「检验科-张三」链接）`。
- 修订标题/说明：只有持「可填写说明」权限链接的访客可以（`can.edit_brief`），
  乐观锁同样生效；无权限 403 `link_cannot_edit_brief`，工单关闭后 409 `ticket_closed`。

- 仅成员的接口（建单、标签全套、关闭、重开、访客链接管理）对访客统一
  403 `members_only`（决议 4）。

## 附件（#33）

两步上传（spec 用户故事 52）：先上传拿到 `att:` 引用与现成的 Markdown 片段，
再在回复或说明中引用（正文 `att:` 引用或 `attachment_ids`）。未被引用的附件也
计入配额，上传满 24 小时后由服务的周期任务清除（行与文件一并删除；备份镜像
只增不删，残留可接受）。文件本体放在 `data_dir\attachments\<id>`，数据库只存
元数据；服务端只检查上限，不压缩图片（压缩在前端做）。

### 上传（POST /api/v1/tickets/{id}/attachments）

- `multipart/form-data`，文件字段名 `file`；支持 `Idempotency-Key`（重试返回首次结果）。
- 成功 `201`：`{id: "att_N", filename, size(字节), state: "pending", markdown, note}`；
  `markdown` 是现成片段——图片（png/jpg/jpeg/gif/webp）用插图语法
  `![名字](att:att_N)`，其余用普通链接 `[名字](att:att_N)`。
- 权限：与回复相同（成员恒可，含工单已关闭时——不重开工单；访客链接需工单开启，
  关闭后 `409 ticket_closed`）。
- 文件名缺失/空白/超 255 字符 → `422 invalid_filename`；缺文件字段 → `422 file_required`。
  multipart 层会把带路径的文件名截成基本名。
- 上限（`config.toml [attachments]` 可调，默认即下表）：

  | 规则 | 默认值 | 超限时 |
  |---|---|---|
  | 单个文件 | 5MB | 413 `attachment_too_large` |
  | 每条说明或回复的附件 | 5 个 | 413 `attachment_count_exceeded` |
  | 每条说明或回复的插图 | 10 张 | 413 `attachment_count_exceeded` |
  | 整张工单合计（含待用） | 50MB | 413 `ticket_quota_exceeded`（附 `used_mb`、`ticket_limit_mb`） |

  三种 413 的 `hint` 都提示改用 NAS 路径（外部文件引用），如
  `\nas01\共享\工单61016-001\文件名`。
- 单工单 GET 的 `attachments` 只列已挂上的（`{id, filename, size, url, attached_to}`，
  `attached_to` 为「说明」或「回复 N」）；`attachment_usage`
  `{used_bytes, used_mb, ticket_limit_mb, file_limit_mb, max_files_per_post, max_images_per_post}`
  把待用的也计入。`?format=markdown` 附「## 附件」小节（链接 + 用量）。

### 下载（GET /api/v1/attachments/{att_N}/{filename}）

- 附件地址使用与工单相同的凭证：成员会话 cookie、成员 Agent token、访客链接密钥
  （Bearer），权限判定与读工单一致（访客取他单附件 404 `ticket_not_found`，防枚举）。
  只有待用（pending）状态不提供下载（404）。
- **访客的签名地址**（orchestrator 决议 9）：浏览器 `<img src>` 带不了 Bearer 头，
  所以访客读工单时（JSON 与 Markdown）拿到的 `url` 是与该链接绑定的短时效签名地址
  （`?link=<链接id>&exp=<unix>&sig=<HMAC>`，有效期 1 小时且不超过链接到期时刻）。
  签名绑（链接、附件、到期时刻），但链接的撤销与过期仍实时查库：链接失效后
  同一地址 401（`link_revoked` / `link_expired`）；地址本身过期（链接仍有效）401
  `attachment_url_expired`，重新读工单拿新地址；篡改签名 401 `invalid_token`；
  签名挪用到别的附件 401。没有不鉴权的永久地址。
- 响应头：图片、PDF、`.txt`、`.log` 以 `inline` 返回，其余（含 HTML、SVG）一律
  `Content-Disposition: attachment`（RFC 5987 编码文件名）；所有附件响应都带
  `X-Content-Type-Options: nosniff`。

- 仅成员的接口（建单、标签全套、关闭、重开、访客链接管理，以及 #35 起的反馈入口
  管理）对访客统一 403 `members_only`（决议 4）。

## 反馈入口（#35）

反馈入口是部门级的长期地址 `/i/<slug>`：内网任何人（或其 Agent）不用账号、不用
访客链接，凭入口地址就能自助提交一张反馈，提交成功当场拿到这张反馈的专属访客
链接（30 天、「可填写说明」权限）——之后完全按访客的方式参与（读、修订说明、
回复，密钥直接作 Bearer）。每个入口绑定一个**反馈类型**的模板，始终使用该模板的
**最新版本**；入口不能查看其他工单（匿名端点只读入口本身）。

### 端点

| 方法与路径 | 说明 |
|---|---|
| `POST /api/v1/intakes` | 创建（任何成员）：`{"slug", "name", "template"}`。 |
| `GET /api/v1/intakes` | 列表（任何成员，含已停用，各项带 `active`）。 |
| `GET /api/v1/intakes/{slug}` | **匿名**读取：入口信息 + 绑定模板的当前版本（维度字段带字典候选值和「不确定」）。 |
| `GET /api/v1/intakes/{slug}?format=markdown` | **匿名**读取给 Agent 的填写指南（`text/markdown`，与 `GET /api/v1/templates/{key}?format=markdown` 同一份）。 |
| `POST /api/v1/intakes/{slug}/tickets` | **匿名**提交：`{"title", "fields", "author_name?"}`。 |
| `POST /api/v1/intakes/{slug}/deactivate` | 停用（任何成员；幂等 200）。 |
| `POST /api/v1/intakes/{slug}/rotate` | 轮换 slug（任何成员）：`{"slug": "新 slug"}`；旧 slug 立即失效。 |

- 创建：`slug` 只用小写字母、数字和连字符（`422 slug_required` / `invalid_slug`），
  且不能与已有入口重复（`409 slug_taken`）；`name` 会显示在提交页上（`422 name_required`）；
  `template` 必须存在（`404 template_not_found`）、在用（`422 template_inactive`）且
  类型为反馈（`422 template_not_report`）。响应 `201` 是入口的管理视图
  `{slug, name, template, active, url: "/i/<slug>", created_by, created_at}`。
- 匿名读取：不存在或已停用的入口一律 `404 intake_not_found`（轮换后的旧 slug 同样，
  与「不存在」不可区分，防枚举）。响应 `{slug, name, url, kind, template}`，其中
  `template` 是当前版本 `{key, name, version, kind, preset_labels, agent_note, fields}`
  （字段结构与 `GET /api/v1/templates/{key}` 相同）。
- 提交（严格校验，与成员建单的 warnings 相反）：
  - 缺必填项直接拦下：`422 missing_fields`，`fields` 附加数组逐项给出
    `{field, hint, example?}`（引导说明、维度字段的「不确定」、单选字段的选项），
    `hint` 汇总逐项说明缺什么、怎么填。**「不确定」算已填**（不打该维度的标签）。
  - 结构错误与成员建单同码：空标题 `422 title_required`、未知字段键
    `422 unknown_field`、单选值不在选项里 `422 invalid_field_option`、维度值不在
    字典里 `422 unknown_label_value`（附 `suggestions`）。
  - 提交**不要求**维度标签（GLOSSARY：入口提交是「至少一个维度标签」的唯一例外）；
    模板预设标签与维度字段选中的值自动打上。
  - 成功 `201`：`{ticket: {number, seq, title, url}, guest_link: {url: "/g/<密钥>",
    note, expires_at, can_edit_brief: true}, next}`。密钥只在提交响应里出现一次。
    工单类型由模板决定（恒为 `report`）；编号与序号的分配同建单。
- 提交者**视同说明的创建者**：工单的创建者记录为访客（带链接备注
  `反馈入口「<名称>」提交人` 与自报的 `author_name`）；`viewer.role` 为 `creator`，
    用返回的链接即可读工单、修订说明（带「可填写说明」权限）和回复。时间线记
    `created`（访客作者）与 `guest_link_created` 两条事件；等待方推导照常
    （反馈：成员回复轮到访客，访客回复轮回本部门）。
- 停用与轮换：停用后匿名读写都按不存在处理；轮换把入口换到新 slug，旧 slug 立即
  失效（`404 intake_not_found`）。两者都不影响已提交的工单和提交者手里的访客链接
  （链接独立于入口存在，见「访客链接（#32）」）。
- 入口提交同样支持 `Idempotency-Key`（匿名凭证按同一 key 去重，重放返回首次结果）。
- 管理端点仅限成员；访客 403 `members_only`（hint 指向入口地址），匿名 401。

