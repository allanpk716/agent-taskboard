// 成员列表接口（#41 成员网页用）：GET /api/v1/members 列出全部成员，
// 供建单/改派的下拉选择。只读；账号的创建与停用（写接口）在 #42。
// 仅成员可读：匿名 401，访客 403 members_only（决议 4）。
package httpapi

import (
	"net/http"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

func (a *API) handleMembers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "成员列表用 GET 读取；账号管理在后续版本加入")
		return
	}
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	if viewer.Kind != identity.ViewerMember {
		WriteError(w, http.StatusForbidden, CodeMembersOnly, "成员列表只对成员开放",
			"访客不能浏览成员名单")
		return
	}
	list, err := a.ident.ListMembers(r.Context())
	if err != nil {
		panic(err)
	}
	members := make([]memberRef, 0, len(list))
	for _, m := range list {
		members = append(members, memberRef{ID: m.ID, Name: m.Name})
	}
	writeJSON(w, http.StatusOK, struct {
		Members []memberRef `json:"members"`
	}{Members: members})
}
