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
)

// ErrorBody 是统一错误格式 {error:{code, message, hint?}} 的内层。
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

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
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: ErrorBody{Code: code, Message: message, Hint: hint}})
}

func writeAPIError(w http.ResponseWriter, err *Error) {
	WriteError(w, err.Status, err.Code, err.Message, err.Hint)
}
