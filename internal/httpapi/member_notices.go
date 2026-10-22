package httpapi

import (
	"net/http"
	"strings"
)

// handleMemberNotice 标记成员侧一次性提示已读。
func (a *API) handleMemberNotice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || strings.TrimPrefix(r.URL.Path, "/api/v1/me/notices/") != "drafts_intro/seen" {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	if err := a.ident.MarkWebNoticeSeen(r.Context(), viewer.Member, "drafts_intro"); err != nil {
		panic(err)
	}
	w.WriteHeader(http.StatusNoContent)
}
