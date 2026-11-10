// 部门访客名单（#328）：成员和成员 Agent 都能列出并按名字搜索，
// 建访客链接时据此选择已有访客；访客建立本身发生在建链接时（选或新建）。
package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
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

// handleGuestsRef 处理 /api/v1/guests/{id}/home-share-text：成员和成员 Agent
// 可随时重新复制访客主页文案；响应不带访问码。
func (a *API) handleGuestsRef(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
			"复制访客主页文案用 POST /api/v1/guests/{id}/home-share-text")
		return
	}
	rest := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(rest) != 5 || rest[0] != "api" || rest[1] != "v1" || rest[2] != "guests" ||
		rest[3] == "" || rest[4] != "home-share-text" {
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口", "")
		return
	}
	id, err := strconv.ParseInt(rest[3], 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "访客 id 必须是名单里的数字 id", "")
		return
	}
	viewer, ok := a.memberFromRequestFor(w, r, "访客不能复制部门访客的主页文案")
	if !ok {
		return
	}
	share, err := a.tickets.GuestHomeShareForViewer(r.Context(), viewer, id)
	switch {
	case errors.Is(err, tickets.ErrGuestNotFound), errors.Is(err, tickets.ErrGuestDisabled):
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "没有这位访客或她已停用", "")
	case errors.Is(err, tickets.ErrGuestManagementNotAllowed):
		WriteError(w, http.StatusForbidden, CodeGuestManagementNotAllowed, err.Error(),
			"只有访客建立者、管理员或名下链接的管理人可以复制主页文案")
	case errors.Is(err, tickets.ErrGuestLinkCipherUnavailable):
		WriteError(w, http.StatusServiceUnavailable, CodeGuestLinkCipherUnavailable, err.Error(), "等服务端加密密钥可用后再复制")
	case errors.Is(err, tickets.ErrGuestShareTextUnavailable):
		WriteError(w, http.StatusConflict, CodeShareTextUnavailable, err.Error(), "主页密钥不可恢复，请联系管理员处理")
	case err != nil:
		panic(err)
	default:
		path := "/h/" + share.Key
		writeJSON(w, http.StatusOK, map[string]any{
			"home_url": path, "share_text": tickets.GuestHomeShareText(share.GuestName, a.absURL(path)),
		})
	}
}

func newGuestView(g tickets.GuestInfo) guestView {
	return guestView{
		ID: g.ID, Name: g.Name, ClaimedAt: g.ClaimedAt, ClaimName: g.ClaimName,
		AccessCodeLockedAt: g.AccessCodeLockedAt, LinkCount: g.LinkCount,
		CreatedBy: newAuthorView(g.CreatedBy, false), CreatedAt: g.CreatedAt,
	}
}
