// Package httpapi 实现 /api/v1 下的 HTTP 接口。
package httpapi

import (
	"encoding/json"
	"net/http"
)

// 错误码。/api/v1 内只做增量修改：只增不改；后续工单的错误码在使用处再加。
const (
	CodeUnauthenticated    = "unauthenticated"      // 未登录或凭证失效
	CodeInvalidCredentials = "invalid_credentials"  // 用户名或密码不正确
	CodeCaptchaRequired    = "captcha_required"     // 登录请求缺验证码标识或答案
	CodeCaptchaInvalid     = "captcha_invalid"      // 验证码答案错误
	CodeCaptchaExpired     = "captcha_expired"      // 验证码已过期
	CodeCaptchaUsed        = "captcha_used"         // 验证码已被使用
	CodeLoginLocked        = "login_locked"         // 连续密码失败后账号来源组合已锁定
	CodeLoginRateLimited   = "login_rate_limited"   // 登录来源每分钟请求过多
	CodeLoginLockRequired  = "login_lock_required"  // 解锁请求缺登录名或来源 IP
	CodeLoginLockNotFound  = "login_lock_not_found" // 解锁目标不存在或已过期
	CodeInvalidJSON        = "invalid_json"         // 请求体不是合法 JSON
	CodeInvalidUTF8        = "invalid_utf8"         // 请求体不是合法的 UTF-8
	CodeInvalidToken       = "invalid_token"        // Agent token 无效或已撤销
	CodeWebSessionRequired = "web_session_required" // 只接受网页会话发起的请求（ADR 0003）
	CodeNameRequired       = "name_required"        // Agent 名字为空
	CodeAgentNameTaken     = "agent_name_taken"     // 同名未撤销的 Agent 已存在
	CodeAgentTokenNotFound = "agent_token_not_found"
	CodeNotFound           = "not_found"
	CodeMethodNotAllowed   = "method_not_allowed"
	CodeInternal           = "internal_error"
	CodeTitleRequired      = "title_required"           // 建单时标题为空
	CodeInvalidKind        = "invalid_kind"             // kind 不是 task 或 report
	CodeDimensionRequired  = "dimension_label_required" // 一个维度标签都没打
	CodeUnknownLabelValue  = "unknown_label_value"      // 标签值不在字典中
	CodeTicketNotFound     = "ticket_not_found"         // 引用的工单不存在

	// #30：回复、说明修订、关闭与时间线。
	CodeEmptyReply           = "empty_reply"            // 回复既无正文也无附件
	CodeNotBriefOwner        = "not_brief_owner"        // 只有创建者能修订说明
	CodeBaseRevisionRequired = "base_revision_required" // 修订说明没带 base_revision
	CodeRevisionConflict     = "revision_conflict"      // base_revision 乐观锁冲突
	CodeInvalidCloseReason   = "invalid_close_reason"   // 关闭原因非法
	CodeInvalidDuplicateOf   = "invalid_duplicate_of"   // duplicate_of 不是工单引用
	CodeDuplicateOfRequired  = "duplicate_of_required"  // 重复关闭时缺 duplicate_of
	CodeAlreadyClosed        = "already_closed"         // 工单已关闭，不能再关
	CodeNotClosed            = "not_closed"             // 工单没关闭，谈不上重开
	CodeDeliverAssigneeOnly  = "deliver_assignee_only"  // 交付标记只限指派人（#36）
	CodeInvalidCursor        = "invalid_cursor"         // after 等游标参数不是整数

	// #36：成员间交接与收件箱。
	CodeAssigneeMustBeMember = "assignee_must_be_member" // 指派对象是成员 Agent
	CodeInvalidAssignee      = "invalid_assignee"        // assignee 类型或数字 id 非法
	CodeUnknownMember        = "unknown_member"          // 指派对象不是成员
	CodeAlreadyAssigned      = "already_assigned"        // 改派给当前指派人
	CodeAssigneeDeactivated  = "assignee_deactivated"    // 指派对象的账号已停用（#42 评审修正）

	// #158：协作者。
	CodeCollaboratorNotAllowed  = "collaborator_not_allowed"
	CodeAlreadyCollaborator     = "already_collaborator"
	CodeNotCollaborator         = "not_collaborator"
	CodeCollaboratorDeactivated = "collaborator_deactivated"

	// #31：标签、字典与别名。
	CodeMembersOnly        = "members_only"         // 仅成员可用（访客 403）
	CodeAdminRequired      = "admin_required"       // 标签治理仅管理员
	CodeUnknownDimension   = "unknown_dimension"    // 维度不存在（新维度只能由管理员加）
	CodeWildcardNotAllowed = "wildcard_not_allowed" // 维度:* 不能打到工单上
	CodeLabelDeactivated   = "label_deactivated"    // 标签值已停用，不能再新打
	CodeAliasTaken         = "alias_taken"          // 写法已被同维度另一标准值占用
	CodeAliasExists        = "alias_exists"         // 别名已挂在这个标准值上
	CodeAliasRequired      = "alias_required"       // 别名为空
	CodeAliasNotFound      = "alias_not_found"      // 改指向时别名不存在
	CodeDimensionTaken     = "dimension_taken"      // 维度已存在
	CodeValueTaken         = "value_taken"          // 改名/合并目标已存在
	CodeSameLabel          = "same_label"           // 两个标签是同一个
	CodeDimensionMismatch  = "dimension_mismatch"   // 改名/合并两端维度不一致
	CodeQRequired          = "q_required"           // resolve 缺少 q
	CodeLabelRequired      = "label_required"       // 请求里的标签为空
	CodeInvalidLabelAny    = "invalid_label_any"    // label_any 没有写成 维度:值

	// #34：模板（维护、读取与按模板建单）。
	CodeTemplateFormatError   = "template_format_error"   // 模板格式错误（message 带行号）
	CodeTemplateKeyRequired   = "template_key_required"   // 新建模板时 key 为空
	CodeInvalidTemplateKey    = "invalid_template_key"    // key 里有不适合 URL 的字符
	CodeMarkdownRequired      = "markdown_required"       // 没写模板内容
	CodeTemplateKeyTaken      = "template_key_taken"      // 模板 key 已存在（不能复用）
	CodeTemplateNotFound      = "template_not_found"      // 引用的模板不存在
	CodeTemplateInactive      = "template_inactive"       // 用已停用的模板建单
	CodeUnknownField          = "unknown_field"           // fields 里有模板没有的字段键
	CodeInvalidFieldOption    = "invalid_field_option"    // 单选/维度字段的值不在选项里
	CodeFieldsWithoutTemplate = "fields_without_template" // 非模板工单用 fields 修订
	CodeBriefMDWithTemplate   = "brief_md_with_template"  // 模板工单用 brief_md 修订

	// #32：访客链接与访客视角。
	CodeLinkRevoked                = "link_revoked"                  // 访客链接已被撤销（401，即时生效）
	CodeLinkExpired                = "link_expired"                  // 访客链接已过期（401，即时生效）
	CodeLinkUnclaimed              = "link_unclaimed"                // 访客链接尚未认领（401）
	CodeLinkClaimed                = "link_claimed"                  // 访客链接已被认领，链接密钥不再可用
	CodeClaimNameRequired          = "claim_name_required"           // 认领访客链接时称呼为空
	CodeAccessCodeRequired         = "access_code_required"          // 凭访问码进入时访问码为空
	CodeAccessCodeInvalid          = "access_code_invalid"           // 访问码不正确
	CodeAccessCodeLocked           = "access_code_locked"            // 连续输错后访问码已锁定
	CodeNoteRequired               = "note_required"                 // 创建访客链接缺备注
	CodeInvalidValidDays           = "invalid_valid_days"            // 有效期不是 7/30/90 或与 expires_at 并存
	CodeInvalidExpiresAt           = "invalid_expires_at"            // expires_at 不是合法的将来时刻
	CodeGuestLinkNotFound          = "guest_link_not_found"          // 这个工单下没有这条访客链接
	CodeLinkNotExtendable          = "link_not_extendable"           // 已撤销或已过期的链接不能延期
	CodeShareTextUnavailable       = "share_text_unavailable"        // 老访客链接没有可恢复密钥，不能直接复制转发文案
	CodeGuestLinkCipherUnavailable = "guest_link_cipher_unavailable" // 服务端加密密钥写不出来（503），新建链接/换新地址暂不可用
	CodeLinkCannotEditBrief        = "link_cannot_edit_brief"        // 链接没有「可填写说明」权限
	CodeTicketClosed               = "ticket_closed"                 // 工单已关闭，访客链接只读；成员回复草稿保存/发出同样使用

	// #211：草稿。
	CodeInvalidDraftMode     = "invalid_draft_mode"     // draft_mode 不是 required/direct
	CodeDraftPending         = "draft_pending"          // 同一方已有待确认草稿
	CodeDraftNotFound        = "draft_not_found"        // 草稿不存在、已丢弃或不属于当前一方
	CodeDraftVersionConflict = "draft_version_conflict" // PUT/发出基于的草稿版本过期
	CodeDraftAlreadySent     = "draft_already_sent"     // 草稿已发出，不能再修改或发出
	CodeBaseVersionRequired  = "base_version_required"  // PUT 草稿缺 base_version
	CodeTicketChanged        = "ticket_changed"         // 草稿写成后工单有新事件且未确认

	// #33：附件。
	CodeAttachmentTooLarge      = "attachment_too_large"      // 单个文件超限（413）
	CodeAttachmentCountExceeded = "attachment_count_exceeded" // 每帖附件/插图个数超限（413）
	CodeTicketQuotaExceeded     = "ticket_quota_exceeded"     // 整张工单合计超限（413）
	CodeAttachmentNotUsable     = "attachment_not_usable"     // 引用的附件不可用（422）
	CodeAttachmentNotFound      = "attachment_not_found"      // 附件不存在（404）
	CodeFileRequired            = "file_required"             // multipart 缺文件字段
	CodeInvalidFilename         = "invalid_filename"          // 文件名缺失或非法
	CodeAttachmentURLExpired    = "attachment_url_expired"    // 附件签名地址过期（401）

	// #35：反馈入口。
	CodeIntakeNotFound             = "intake_not_found"         // 入口不存在或已停用（含轮换后的旧 slug）
	CodeSlugRequired               = "slug_required"            // 创建/轮换入口时没写 slug
	CodeInvalidSlug                = "invalid_slug"             // slug 里有不适合 URL 的字符
	CodeSlugTaken                  = "slug_taken"               // slug 已被另一个入口占用
	CodeTemplateNotReport          = "template_not_report"      // 入口只能绑定反馈类型的模板
	CodeMissingFields              = "missing_fields"           // 入口提交缺必填项（严格校验，逐项提示）
	CodeIdempotencyKeyRequired     = "idempotency_key_required" // 入口附件上传缺提交归属键
	CodeIntakeUploadRateLimited    = "intake_upload_rate_limited"
	CodeIntakePendingQuotaExceeded = "intake_pending_quota_exceeded"
	CodeIntakePaused               = "intake_paused"

	// #45：更新。
	CodeUpdateSourceUnreachable    = "update_source_unreachable"    // 所有更新源都连不上（502）
	CodeUpdateFailed               = "update_failed"                // 更新本身的失败（哈希不符、备份失败等）
	CodeContainerUpdateUnsupported = "container_update_unsupported" // 容器部署不支持原地更新（409）
	// #42：成员账号管理（网页上创建与停用，仅管理员）。
	CodeUsernameRequired     = "username_required"      // 创建成员时登录名为空
	CodeInvalidUsername      = "invalid_username"       // 登录名里有不适合的字符
	CodeInvalidName          = "invalid_name"           // 显示名里含替换字符
	CodeUsernameTaken        = "username_taken"         // 登录名已存在（409）
	CodeMemberNotFound       = "member_not_found"       // 没有这位成员（404）
	CodeMemberDeactivated    = "member_deactivated"     // 账号已停用（登录 401）
	CodeMemberInactive       = "member_inactive"        // 重置密码的目标账号已停用（422）
	CodePasswordUnchanged    = "password_unchanged"     // 新密码与旧密码相同（422）
	CodeCannotDeactivateSelf = "cannot_deactivate_self" // 不能停用自己的账号

	// 幂等（评审修正）：同键并发重放。
	CodeIdempotencyInProgress = "idempotency_in_progress" // 同一个 Idempotency-Key 的请求正在执行（409）

	// #37：查询与计数。
	CodeUnknownParameter = "unknown_parameter"  // 不认识的查询参数（附可用参数列表）
	CodeInvalidStatus    = "invalid_status"     // status 只能是 open/closed/all
	CodeInvalidSort      = "invalid_sort"       // 不支持的排序键
	CodeInvalidLimit     = "invalid_limit"      // limit 不是 1–200 的整数
	CodeInvalidPageToken = "invalid_page_token" // page_token 无效
	CodeInvalidTime      = "invalid_time"       // 时间筛选格式不对
	CodeInvalidInclude   = "invalid_include"    // include 只支持 brief
	CodeInvalidWaitingOn = "invalid_waiting_on" // waiting_on 只能是五者之一
	CodeGroupByRequired  = "group_by_required"  // 计数缺 group_by
	CodeInvalidGroupBy   = "invalid_group_by"   // 不能按这个维度分组
	CodeInvalidActorKind = "invalid_actor_kind" // actor_kind 只能是 member/agent

	// #78：成员标通知已读。
	CodeInvalidReadRequest     = "invalid_read_request"   // 请求体不是 ids/ticket/up_to 三选一
	CodeAgentCannotMarkRead    = "agent_cannot_mark_read" // 成员 Agent 不能替主人消费未读
	CodeTooManyNotificationIDs = "too_many_notification_ids"
	CodeInvalidAfter           = "invalid_after"
	CodeInvalidBefore          = "invalid_before"
	CodeInvalidLastEventID     = "invalid_last_event_id"

	// #234：管理页存储统计。
	CodeInvalidClosedMonths = "invalid_closed_months" // closed_months 不是 1–120 的整数（400）
	CodeTicketNotArchivable = "ticket_not_archivable" // 选择里有不可归档工单（400）
	CodeArchiveNotFound     = "archive_not_found"     // 归档包不存在或已过期（404）

	// #80：桌面客户端凭证。
	CodeInvalidClientPort        = "invalid_client_port"
	CodeInvalidClientConnect     = "invalid_client_connect"
	CodeInvalidClientCode        = "invalid_client_code"
	CodeClientCredentialNotFound = "client_credential_not_found"
	CodeClientScope              = "client_scope"
	CodeAgentNotAllowed          = "agent_not_allowed"
	CodeClientNotAvailable       = "client_not_available"
	CodePasswordTooShort         = "password_too_short"
	CodeInvalidOldPassword       = "invalid_old_password"
	CodeCannotResetSelf          = "cannot_reset_self"
)

// ErrorBody 是统一错误格式 {error:{code, message, hint?}} 的内层；
// 附加字段（suggestions 等）经由 map 形式平铺进来。
type ErrorBody map[string]any

type errorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// Error 是带 HTTP 状态码的 API 错误。
type Error struct {
	Status  int
	Code    string
	Message string
	Hint    string
	Extra   map[string]any // 附加字段（如 suggestions），平铺在 error 对象里
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// NewError 构造 API 错误，hint 可为空。
func NewError(status int, code, message, hint string) *Error {
	return &Error{Status: status, Code: code, Message: message, Hint: hint}
}

// NewErrorExtra 构造带附加字段（如 suggestions）的 API 错误。
func NewErrorExtra(status int, code, message, hint string, extra map[string]any) *Error {
	return &Error{Status: status, Code: code, Message: message, Hint: hint, Extra: extra}
}

// WriteError 按统一格式写出一个错误响应。
func WriteError(w http.ResponseWriter, status int, code, message, hint string) {
	WriteErrorExtra(w, status, code, message, hint, nil)
}

// WriteErrorExtra 在统一错误格式上附加 extra 字段（如 unknown_label_value 的
// suggestions），它们与 code/message/hint 平铺在 error 对象里。
func WriteErrorExtra(w http.ResponseWriter, status int, code, message, hint string, extra map[string]any) {
	body := map[string]any{
		"code":    code,
		"message": message,
	}
	if hint != "" {
		body["hint"] = hint
	}
	for k, v := range extra {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: body})
}

func writeAPIError(w http.ResponseWriter, err *Error) {
	WriteErrorExtra(w, err.Status, err.Code, err.Message, err.Hint, err.Extra)
}
