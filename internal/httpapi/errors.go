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
	CodeInvalidJSON        = "invalid_json"         // 请求体不是合法 JSON
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
	CodeUnknownMember        = "unknown_member"          // 指派对象不是成员
	CodeAlreadyAssigned      = "already_assigned"        // 改派给当前指派人

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
	CodeLinkRevoked         = "link_revoked"           // 访客链接已被撤销（401，即时生效）
	CodeLinkExpired         = "link_expired"           // 访客链接已过期（401，即时生效）
	CodeNoteRequired        = "note_required"          // 创建访客链接缺备注
	CodeInvalidValidDays    = "invalid_valid_days"     // 有效期不是 7/30/90 或与 expires_at 并存
	CodeInvalidExpiresAt    = "invalid_expires_at"     // expires_at 不是合法的将来时刻
	CodeGuestLinkNotFound   = "guest_link_not_found"   // 这个工单下没有这条访客链接
	CodeLinkNotExtendable   = "link_not_extendable"    // 已撤销或已过期的链接不能延期
	CodeLinkCannotEditBrief = "link_cannot_edit_brief" // 链接没有「可填写说明」权限
	CodeTicketClosed        = "ticket_closed"          // 工单已关闭，访客链接只读

	// #35：反馈入口。
	CodeIntakeNotFound    = "intake_not_found"    // 入口不存在或已停用（含轮换后的旧 slug）
	CodeSlugRequired      = "slug_required"       // 创建/轮换入口时没写 slug
	CodeInvalidSlug       = "invalid_slug"        // slug 里有不适合 URL 的字符
	CodeSlugTaken         = "slug_taken"          // slug 已被另一个入口占用
	CodeTemplateNotReport = "template_not_report" // 入口只能绑定反馈类型的模板
	CodeMissingFields     = "missing_fields"      // 入口提交缺必填项（严格校验，逐项提示）
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
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// NewError 构造 API 错误，hint 可为空。
func NewError(status int, code, message, hint string) *Error {
	return &Error{Status: status, Code: code, Message: message, Hint: hint}
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
	WriteError(w, err.Status, err.Code, err.Message, err.Hint)
}
