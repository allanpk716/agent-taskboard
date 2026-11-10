// 部门访客名单（#328）：成员和成员 Agent 都能列出并按名字搜索，
// 建访客链接时据此选择已有访客；访客建立本身发生在建链接时（选或新建）。
package httpapi

import (
	"net/http"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// guestView 是访客名单的列表项。
type guestView struct {
	ID                 int64      `json:"id"`
	Name               string     `json:"name"`
	ClaimedAt          *time.Time `json:"claimed_at,omitempty"`
	ClaimName          string     `json:"claim_name,omitempty"`
	AccessCodeLockedAt *time.Time `json:"access_code_locked_at,omitempty"`
	LinkCount          int        `json:"link_count"`
	CreatedBy          authorView `json:"created_by"`
	CreatedAt          time.Time  `json:"created_at"`
}

// handleGuests 处理 GET /api/v1/guests?q=<名字>：列出部门访客名单。
func (a *API) handleGuests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
			"列出部门访客用 GET /api/v1/guests?q=<名字>")
		return
	}
	if _, ok := a.memberFromRequestFor(w, r, "访客不能查看部门访客名单"); !ok {
		return
	}
	guests, err := a.tickets.Guests(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		panic(err)
	}
	out := make([]guestView, 0, len(guests))
	for _, g := range guests {
		out = append(out, newGuestView(g))
	}
	writeJSON(w, http.StatusOK, map[string]any{"guests": out})
}

func newGuestView(g tickets.GuestInfo) guestView {
	return guestView{
		ID: g.ID, Name: g.Name, ClaimedAt: g.ClaimedAt, ClaimName: g.ClaimName,
		AccessCodeLockedAt: g.AccessCodeLockedAt, LinkCount: g.LinkCount,
		CreatedBy: newAuthorView(g.CreatedBy, false), CreatedAt: g.CreatedAt,
	}
}
