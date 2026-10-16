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
