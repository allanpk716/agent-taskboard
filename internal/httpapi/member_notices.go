package httpapi

import (
	"net/http"
	"strings"
)

// handleMemberNotice 标记成员侧一次性提示已读。
func (a *API) handleMemberNotice(w http.ResponseWriter, r *http.Request) {
	notice := strings.TrimPrefix(r.URL.Path, "/api/v1/me/notices/")
	// drafts_intro 是 #217 的旧提示键，保留兼容老网页；ADR 0017 之后由
	// drafts_direct_intro 驱动新提示。
	if r.Method != http.MethodPost || (notice != "drafts_intro/seen" && notice != "drafts_direct_intro/seen") {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	if err := a.ident.MarkWebNoticeSeen(r.Context(), viewer.Member, strings.TrimSuffix(notice, "/seen")); err != nil {
		panic(err)
	}
	w.WriteHeader(http.StatusNoContent)
}
