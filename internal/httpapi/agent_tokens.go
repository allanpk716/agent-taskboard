package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// handleAgentTokens 处理成员名下的 Agent token 集合：
//
//	GET   /api/v1/me/agent-tokens  列表（含已撤销；只接受网页会话，#42）
//	POST  /api/v1/me/agent-tokens  签发一个具名 token（只接受网页会话）
func (a *API) handleAgentTokens(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.listAgentTokens(w, r)
	case http.MethodPost:
		a.issueAgentToken(w, r)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
	}
}

// agentTokenRow 是 Agent token 列表的条目；明文 token 不回显（只在签发响应里
// 出现一次）。撤销的保留在列表里，页面标注「已停用」。
type agentTokenRow struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	CreatedAt string  `json:"created_at"`
	RevokedAt *string `json:"revoked_at"`
}

func (a *API) listAgentTokens(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	list, err := a.ident.ListAgentTokens(r.Context(), viewer.Member)
	if err != nil {
		panic(err)
	}
	rows := make([]agentTokenRow, 0, len(list))
	for _, tk := range list {
		row := agentTokenRow{ID: tk.ID, Name: tk.Name, CreatedAt: tk.CreatedAt.Format("2006-01-02T15:04:05Z07:00")}
		if tk.Revoked {
			at := tk.RevokedAt.Format("2006-01-02T15:04:05Z07:00")
			row.RevokedAt = &at
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, struct {
		Tokens []agentTokenRow `json:"tokens"`
	}{Tokens: rows})
}

// handleAgentToken 处理单个 Agent token：
//
//	DELETE /api/v1/me/agent-tokens/{id}  撤销（只接受网页会话），立即生效
func (a *API) handleAgentToken(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodDelete:
		a.revokeAgentToken(w, r)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
	}
}

type issueAgentTokenRequest struct {
	Name string `json:"name"`
}

func (a *API) issueAgentToken(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	var req issueAgentTokenRequest
	if !decodeJSON(w, r, &req, `{"name": "…"}`) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		WriteError(w, http.StatusUnprocessableEntity, CodeNameRequired, "Agent 名字不能为空", "给这个 Agent 起个名字，如「笔记本」")
		return
	}
	tok, err := a.ident.IssueAgentToken(r.Context(), viewer.Member, req.Name)
	if err != nil {
		if errors.Is(err, identity.ErrAgentNameTaken) {
			WriteError(w, http.StatusConflict, CodeAgentNameTaken, "该名字的 Agent 已存在", "换一个名字，或先撤销原有的同名 Agent")
			return
		}
		panic(err)
	}
	writeJSON(w, http.StatusCreated, tok)
}

func (a *API) revokeAgentToken(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/me/agent-tokens/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusNotFound, CodeAgentTokenNotFound, "没有这个 Agent token", "")
		return
	}
	if err := a.ident.RevokeAgentToken(r.Context(), viewer.Member, id); err != nil {
		if errors.Is(err, identity.ErrAgentTokenNotFound) {
			WriteError(w, http.StatusNotFound, CodeAgentTokenNotFound, "没有这个 Agent token", "")
			return
		}
		panic(err)
	}
	a.wakeMemberStreams(viewer.Member.ID)
	w.WriteHeader(http.StatusNoContent)
}
