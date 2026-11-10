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

// handleGuestsRef 处理 /api/v1/guests/{id}/ 下的成员操作：
//
//	POST /api/v1/guests/{id}/home-share-text            重复复制主页文案（成员与成员 Agent）
//	GET  /api/v1/guests/{id}/reset-access-code/preview  重置访问码影响预览（仅网页成员）
//	POST /api/v1/guests/{id}/reset-access-code          访客级重置访问码（仅网页成员）
//	POST /api/v1/guests/{id}/reset-home-key             重置主页地址（仅网页成员）
func (a *API) handleGuestsRef(w http.ResponseWriter, r *http.Request) {
	rest := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(rest) < 5 || rest[0] != "api" || rest[1] != "v1" || rest[2] != "guests" || rest[3] == "" {
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口", "")
		return
	}
	id, err := strconv.ParseInt(rest[3], 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "访客 id 必须是名单里的数字 id", "")
		return
	}
	action := rest[4]
	preview := len(rest) == 6 && rest[5] == "preview"
	if len(rest) > 6 || (len(rest) == 6 && !preview) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口", "")
		return
	}
	switch {
	case action == "home-share-text" && len(rest) == 5 && r.Method == http.MethodPost:
		a.copyGuestHomeText(w, r, id)
	case action == "reset-access-code" && preview && r.Method == http.MethodGet:
		a.previewGuestAccessCodeReset(w, r, id)
	case action == "reset-access-code" && len(rest) == 5 && r.Method == http.MethodPost:
		a.resetGuestAccessCodeForGuest(w, r, id)
	case action == "reset-home-key" && len(rest) == 5 && r.Method == http.MethodPost:
		a.resetGuestHomeKey(w, r, id)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
			"访客操作接口：POST /api/v1/guests/{id}/home-share-text、"+
				"GET /api/v1/guests/{id}/reset-access-code/preview、"+
				"POST /api/v1/guests/{id}/reset-access-code、"+
				"POST /api/v1/guests/{id}/reset-home-key")
	}
}

func (a *API) copyGuestHomeText(w http.ResponseWriter, r *http.Request, id int64) {
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

func (a *API) previewGuestAccessCodeReset(w http.ResponseWriter, r *http.Request, id int64) {
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	preview, err := a.tickets.GuestAccessCodeResetPreviewForViewer(r.Context(), viewer, id)
	if !a.writeGuestResetError(w, err, "") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"guest_id": preview.GuestID, "guest_name": preview.GuestName,
		"device_count": preview.DeviceCount, "ticket_count": preview.TicketCount,
		"access_code_locked": preview.AccessCodeLocked,
	})
}

func (a *API) resetGuestAccessCodeForGuest(w http.ResponseWriter, r *http.Request, id int64) {
	w.Header().Set("Cache-Control", "no-store")
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		reset, err := a.tickets.ResetGuestAccessCodeForGuest(r.Context(), viewer, id)
		if !a.writeGuestResetError(w, err,
			"主页地址暂时不可恢复；请先重置主页地址，再重置访问码") {
			return
		}
		homePath := "/h/" + reset.HomeKey
		writeJSON(w, http.StatusOK, map[string]any{
			"guest_id":    reset.GuestID,
			"access_code": reset.AccessCode,
			"home_url":    homePath,
			"reset_text": tickets.GuestAccessCodeResetTextForGuest(
				reset.GuestName, a.absURL(homePath), reset.AccessCode),
		})
	})
}

func (a *API) resetGuestHomeKey(w http.ResponseWriter, r *http.Request, id int64) {
	w.Header().Set("Cache-Control", "no-store")
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		share, err := a.tickets.ResetGuestHomeKeyForViewer(r.Context(), viewer, id)
		if !a.writeGuestResetError(w, err, "") {
			return
		}
		path := "/h/" + share.Key
		writeJSON(w, http.StatusOK, map[string]any{
			"guest_id":   share.GuestID,
			"home_url":   path,
			"share_text": tickets.GuestHomeShareText(share.GuestName, a.absURL(path)),
		})
	})
}

// writeGuestResetError 统一映射访客级重置的管理与凭证错误；返回 false 表示
// 已写出响应。shareHint 传给 share_text_unavailable，两个接口的恢复建议不同。
func (a *API) writeGuestResetError(w http.ResponseWriter, err error, shareHint string) bool {
	switch {
	case errors.Is(err, tickets.ErrGuestNotFound), errors.Is(err, tickets.ErrGuestDisabled):
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "没有这位访客或她已停用", "")
	case errors.Is(err, tickets.ErrGuestManagementNotAllowed):
		WriteError(w, http.StatusForbidden, CodeGuestManagementNotAllowed, err.Error(),
			"只有访客建立者、管理员或名下链接的管理人可以重置")
	case errors.Is(err, tickets.ErrGuestNotClaimed):
		WriteError(w, http.StatusConflict, CodeGuestNotClaimed, err.Error(),
			"等访客第一次打开主页或链接认领后再重置")
	case errors.Is(err, tickets.ErrGuestLinkCipherUnavailable):
		WriteError(w, http.StatusServiceUnavailable, CodeGuestLinkCipherUnavailable,
			err.Error(), "等服务端加密密钥可用后再重置")
	case errors.Is(err, tickets.ErrGuestShareTextUnavailable):
		hint := shareHint
		if hint == "" {
			hint = "主页密钥不可恢复，请联系管理员处理"
		}
		WriteError(w, http.StatusConflict, CodeShareTextUnavailable, err.Error(), hint)
	case err == nil:
		return true
	default:
		panic(err)
	}
	return false
}

func newGuestView(g tickets.GuestInfo) guestView {
	return guestView{
		ID: g.ID, Name: g.Name, ClaimedAt: g.ClaimedAt, ClaimName: g.ClaimName,
		AccessCodeLockedAt: g.AccessCodeLockedAt, LinkCount: g.LinkCount,
		CreatedBy: newAuthorView(g.CreatedBy, false), CreatedAt: g.CreatedAt,
	}
}
