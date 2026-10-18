# agent-taskboard Agent 说明书

agent-taskboard 是部门工单记录平台。Agent 可以创建和处理工单、回复、上传附件、查看标签和字典；平台只记录事实，不内置 AI。

## 凭证

- 成员 Agent 用主人在网页「我的 → Agent」签发的 token，请求头写 `Authorization: Bearer <token>`。Agent 不能给自己签发 token；没有 token 就停下来请主人签发，不要用密码登录，也绝不使用别人的 token。
- 访客链接本身就是访客凭证：把 `/g/<密钥>` 里的密钥作为 Bearer token 使用。
- 所有数据接口仍需凭证；本说明书不含任何数据。

## 起手流程

1. 先看工单的 Markdown 视图：`{{BASE}}/api/v1/tickets/<工单编号>?format=markdown`。
2. 照工单末尾「下一步（给 Agent 的操作指引）」执行。
3. 之后的待办看收件箱：`{{BASE}}/api/v1/inbox`。
4. 用变更序号增量轮询：列表接口的 `changed_after=<上次拿到的最大 change>`，只处理之后变化的工单。

## 等待方与收件箱

等待方表示当前轮到谁行动，由最后一条回复或访客修订说明推导，不能手动设置。访客还没补全说明时轮到访客，这张工单不会进入成员收件箱。收件箱是某位成员「现在轮到我」的开启工单清单；同一成员的所有成员 Agent 共用这份收件箱。看到自己一方在等待方中，或工单进入收件箱，就该继续处理。

## 请求注意事项

- 所有写请求都带 `Idempotency-Key: <每次业务操作固定的随机值>`，重试不会重复建单或重复回复。
- Windows 下请求体必须是 UTF-8。把 JSON 写入文件后用 `curl --data-binary @body.json` 发送；PowerShell 用 `[Text.Encoding]::UTF8.GetBytes(...)` 作为 `-Body`。
- 附件先 `POST {{BASE}}/api/v1/tickets/<工单编号>/attachments` 上传（multipart 字段名 `file`），再用返回的 `att_N` 引用：图片嵌正文，普通文件写 `[名字](att:att_N)`；不属于某个字段的文件放进回复的 `attachment_ids`。

完整接口参考见 `{{BASE}}/api.md`。
