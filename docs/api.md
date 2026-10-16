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
- `inbox` 是成员收件箱地址（端点在后续工单实现）。

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
| `POST /api/v1/tickets/{id}/replies` | 回复（成员、成员 Agent、访客）。 |
| `POST /api/v1/tickets/{id}/close` | 关闭（任何成员；访客 403 `members_only`）。 |
| `POST /api/v1/tickets/{id}/reopen` | 重新开启（任何成员；访客 403 `members_only`）。 |
| `GET /api/v1/tickets/{id}/timeline?after=N` | 时间线增量读取。 |

（labels 在 #31、attachments 在 #33、assign 在 #36 加入；guest-links 见下。）

- 建单请求体 `{"kind", "title", "brief_md", "labels"}`（不用模板）或
  `{"template", "title", "fields", "labels"}`（用模板，见下面的「模板（#34）」）：
  - `kind` 可省（默认 `task`），只能是 `task`（任务）或 `report`（反馈）；用模板创建时由模板决定（请求里的 `kind` 被忽略）。
  - `title` 必填；`brief_md` 是不用模板时的一整块说明（可空）；模板工单的说明由 `fields` 按字段拼成。
  - `labels` 至少要有一个 `维度:值` 形式的维度标签；且每个标签（含不带维度的）
    必须是字典中在用的标准值（种子维度：产品线、系统、机型、部门、算法、立项、子项目；
    初始值见 `internal/labels/seeds.go`）。别名与 allow_new 见下面的「标签、字典与别名（#31）」。
    用模板创建时，模板的预设标签与维度字段选中的值会自动并进来（预设标签、维度字段值
    都要满足同样的字典校验；「不确定」不打标签）。
  - `assignee`（建单时指派与 `share_text`）预留给 #36，本版忽略。
- 建单响应 `201`：`{number, seq, url, warnings}`（`url` 即 `/t/<编号>`）。
  `warnings` 是用模板建单时「缺必填」的逐项提醒 `{field, message}[]`——工单照常创建，
  只在响应里提醒（spec 用户故事 6）；不用模板或必填全填时为空数组。
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
    访客：`member`/`agent` 为 `null`，另带 `link_note`/`link_expires_at`；`your_turn` 看
    等待方是不是访客；能力表：`reply`/`upload` 需工单开启，`edit_brief` 需开启且链接带
    「可填写说明」，其余恒 false。
  - `waiting_on` 由时间线推导（规则见下「时间线增量读取」；指派在 #36 加入）。
  - 访客视角的可见性过滤（#32）：响应里没有 `labels` 字段，时间线跳过标签事件
   （事件序号会跳号，这是已接受的）；访客访问其他工单一律 404 `ticket_not_found`
   （防枚举）。`?format=markdown` 同样过滤（无标签行、无标签事件）。
- `?format=markdown` 返回 `text/markdown`：头部信息、说明、附件（占位）、时间线和
  「下一步」；署名按 ADR 0003：成员 `陈某`，成员 Agent `陈某 的 Agent · 名字`。
  时间线里回复单独成节（`### [seq] 时间 · 作者 回复` + 正文），
  说明修订/关闭/重开等事件写成 `- [seq] 时间 · 作者 事件文案`（如
  `修订说明（第 2 版）`、`关闭工单（重复：与 20261016-001 重复）`）；
  标签事件写成「打上标签 X」「摘掉标签 Y」。
  时间线里回复单独成节（`### [seq] 时间 · 作者 回复` + 正文），
  说明修订/关闭/重开等事件写成 `- [seq] 时间 · 作者 事件文案`（如
  `修订说明（第 2 版）`、`关闭工单（重复：与 20261016-001 重复）`）。

### 回复（#30）

- 请求体 `{"body_md": "…", "author_name?": "…", "via_agent?": false}`；空白正文且无附件
  返回 `422 empty_reply`（附件引用在 #33 加入后，有附件即可通过）。
- `author_name` 与 `via_agent` 是访客的自报信息（#32，仅访客凭证采纳）：平台原样存下、
  不经核实，回复的作者结构带 `link_note`（链接备注）与这两项；成员的自报不生效
  （署名由凭证决定，ADR 0003）。
- 成员（含成员 Agent）在已关闭的工单上仍可回复——那不会重开工单；重新开启是显式
  动作。访客链接在关闭的工单上只读：回复返回 `409 ticket_closed`，重新开启后恢复。
  `deliver: true` 目前一律返回 `403 deliver_assignee_only`（指派与交付在 #36 加入）。
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
  关闭后 `409 ticket_closed`）。反馈入口提交者在 #35 加入。修订不再做必填校验，
  但标题改成空白返回 `422 title_required`。访客修订可自报 `author_name`/`via_agent`
  （记在修订事件的作者上）。
- 成功 `200`：`{"revision": N}`；每次修订记一条 `brief_revised` 事件（带 `revision`）。
  字段修订的取值仍做结构校验：键必须是模板（创建时版本）里的字段（`422 unknown_field`）、
  单选值必须在选项里（`422 invalid_field_option`）、维度值必须是字典里在用的值
  （`422 unknown_label_value`；「不确定」除外）；但**不做必填校验**，可以把字段清空。

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
  `closed` 带 `reason`/`duplicate_of`，`guest_link_created`/`guest_link_revoked` 带
  `note`，`guest_link_extended` 带 `note` 与 `expires_at`）。时间线只追加：每条都记录
  作者与时间，旧事件永不改动。访客可见性过滤（跳过标签事件）在 #32 已生效；
  访客的 `cursor` 是它可见的最后一条事件 `seq`（跳过的序号不会当作增量重发）。
- `waiting_on` 的推导（#36 指派加入前的规则）：已关闭 → `none`；任务单创建者一方
  回复或尚无回复 → `department`（原因「尚未指派」；访客回复后是「访客回复了」，
  访客在 #32 起）；反馈单成员回复 → `guest`。指派后按 prototype/member-handoff 的
  turnOf 规则推导（对手、交付、退回），收件箱在 #36。

## 幂等（#30）

对所有会产生新内容的请求（建单、回复、说明修订——成员与访客凭证都一样；
反馈提交与附件上传随后续工单接入）支持 `Idempotency-Key` 请求头：

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
  `brief.template` 同 `template`。
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
- 可见：标题、说明、全部回复、附件（#33）、指派人、状态变更和关闭原因、访客链接
  事件。不可见：`labels` 字段和标签事件（事件序号会跳号，已接受）。
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
