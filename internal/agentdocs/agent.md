# agent-taskboard Agent 说明书

agent-taskboard 是部门工单记录平台。Agent 可以创建和处理工单、回复、上传附件、查看标签和字典；平台只记录事实，不内置 AI。

## 凭证

- 成员 Agent 的 token 由主人通过「我的 → Agent」生成的接入文案交给 Agent，不在工单指令和交接文案中出现。Agent 首次接入时执行接入文案里的两条命令，用 `setx` 把平台地址和凭证存成 Windows 用户环境变量 `TASKBOARD_URL` / `TASKBOARD_TOKEN`；新变量要新开终端才生效，本会话可直接使用。
- 后续请求头写 `Authorization: Bearer $TASKBOARD_TOKEN`。读不到这个变量或请求返回 401 时，Agent 要停下来，请主人去「我的 → Agent」生成或换新 token，并重新粘贴接入文案；不要用密码登录，也绝不使用别人的 token。
- 访客链接本身就是访客凭证：把 `/g/<密钥>` 里的密钥作为 Bearer token 使用。
- 所有数据接口仍需凭证；本说明书不含任何数据。

## 起手流程

1. 先看工单的 Markdown 视图：`{{BASE}}/api/v1/tickets/<工单编号>?format=markdown`，请求头 `Authorization: Bearer $TASKBOARD_TOKEN`。
2. 照工单末尾「下一步（给 Agent 的操作指引）」执行。
3. 之后的待办看收件箱：`{{BASE}}/api/v1/inbox`。
4. 用变更序号增量轮询：列表接口的 `changed_after=<上次拿到的最大 change>`，只处理之后变化的工单。

## 等待方与收件箱

等待方表示当前轮到谁行动，由最后一条回复或访客修订说明推导，不能手动设置。访客还没补全说明时轮到访客，这张工单不会进入成员收件箱。收件箱是某位成员「现在轮到我」的开启工单清单；同一成员的所有成员 Agent 共用这份收件箱。看到自己一方在等待方中，或工单进入收件箱，就该继续处理。

## 可见范围与敏感信息

- 工单内容属于公司商业敏感信息。成员只能读取自己创建、当前被指派、被加入协作，以及未指派时由自己初筛的工单；管理员可以读取全部。成员 Agent 的读取范围与其主人相同，列表、搜索、计数和收件箱也只出现这些工单。
- 访问不到的工单会返回 404，与编号不存在无法区分；不要反复尝试或猜测编号。标签、维度和别名字典是全员共享的，不代表某张工单可见。
- 访客访问必须使用访客链接凭证；不要把工单内容、访客链接或访问码写入无关工单、通知或其他系统。

## 请求注意事项

- 所有写请求都带 `Idempotency-Key: <每次业务操作固定的随机值>`，重试不会重复建单或重复回复。
- Windows 下请求体必须是 UTF-8。把 JSON 写入文件后用 `curl --data-binary @body.json` 发送；PowerShell 用 `[Text.Encoding]::UTF8.GetBytes(...)` 作为 `-Body`。
- 附件先 `POST {{BASE}}/api/v1/tickets/<工单编号>/attachments` 上传（multipart 字段名 `file`），再用返回的 `att_N` 引用：图片嵌正文，普通文件写 `[名字](att:att_N)`；不属于某个字段的文件放进回复或说明修订的 `attachment_ids`，说明里的日志等整单文件不要拼进字段。

完整接口参考见 `{{BASE}}/api.md`。
