// 访客主页 HTTP 接口（#329）：主页密钥认领/输码，设备凭证与主页 Agent
// 凭证读取名下有效工单列表。列表不写访问记录。
package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

type guestHomeTicketView struct {
	Number       string `json:"number"`
	Title        string `json:"title"`
	Status       string `json:"status"`
	API          string `json:"api"`
	WaitingOn    bool   `json:"waiting_on_you"`
	Reason       string `json:"reason,omitempty"`
	ReadOnly     bool   `json:"read_only"`
	ExpiresAt    string `json:"expires_at"`
	CanEditBrief bool   `json:"can_edit_brief"`
}

type guestHomeView struct {
	State                 string                `json:"state"`
	GuestName             string                `json:"guest_name"`
	Tickets               []guestHomeTicketView `json:"tickets"`
	GuestAgentInstruction string                `json:"guest_agent_instruction"`
}

func (a *API) handleGuestHome(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
			"读取访客主页用 GET")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if token, ok := bearerToken(r); ok && identity.IsGuestHomeKey(token) && !strings.Contains(token, ":") {
		home, err := a.ident.GuestHomeForKey(r.Context(), token)
		if err != nil {
			var status *identity.HomeStatusError
			if errors.As(err, &status) &&
				(status.Status == identity.GuestLinkClaimed || status.Status == identity.GuestLinkAccessLocked) {
				state := "claimed"
				if status.Status == identity.GuestLinkAccessLocked {
					state = "access_code_locked"
				}
				writeJSON(w, http.StatusOK, a.newGuestHomeView(status.Home.Name, state, nil, r))
				return
			}
			a.writeGuestHomeStatusError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, a.newGuestHomeView(home.Name, "unclaimed", nil, r))
		return
	}

	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	if viewer.Kind != identity.ViewerGuest || viewer.Guest == nil {
		WriteError(w, http.StatusForbidden, CodeMembersOnly, "访客主页只能由访客凭证读取", "")
		return
	}
	items, err := a.tickets.GuestHomeTickets(r.Context(), viewer.Guest.ID)
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusOK, a.newGuestHomeView(viewer.Guest.Name, "claimed", items, r))
}

func (a *API) handleGuestHomeClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
			"认领访客主页用 POST")
		return
	}
	home, ok := a.guestHomeForClaim(w, r)
	if !ok {
		return
	}
	var req struct {
		AuthorName string `json:"author_name"`
	}
	if !decodeJSON(w, r, &req, `{"author_name": "检验科 张三"}`) {
		return
	}
	result, err := a.tickets.ClaimGuestHome(r.Context(), home, req.AuthorName)
	if err != nil {
		a.writeGuestClaimError(w, err)
		return
	}
	a.setGuestDeviceCookie(w, result)
	writeJSON(w, http.StatusCreated, map[string]any{
		"device_token": result.DeviceToken, "access_code": result.AccessCode,
	})
}

func (a *API) handleGuestHomeEnter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
			"凭访问码进入访客主页用 POST")
		return
	}
	home, ok := a.guestHomeForClaim(w, r)
	if !ok {
		return
	}
	var req struct {
		AccessCode string `json:"access_code"`
	}
	if !decodeJSON(w, r, &req, `{"access_code": "123456"}`) {
		return
	}
	result, err := a.tickets.EnterGuestHome(r.Context(), home, req.AccessCode)
	if err != nil {
		a.writeGuestClaimError(w, err)
		return
	}
	a.setGuestDeviceCookie(w, result)
	writeJSON(w, http.StatusCreated, map[string]any{"device_token": result.DeviceToken})
}

func (a *API) guestHomeForClaim(w http.ResponseWriter, r *http.Request) (identity.GuestHome, bool) {
	token, ok := bearerToken(r)
	if !ok || !identity.IsGuestHomeKey(token) {
		WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "未登录",
			"访客主页认领和输码用主页地址里的 tbh_ 密钥作为 Bearer 凭证")
		return identity.GuestHome{}, false
	}
	home, err := a.ident.GuestHomeForKey(r.Context(), token)
	if err == nil {
		return home, true
	}
	var status *identity.HomeStatusError
	if errors.As(err, &status) {
		if status.Status == identity.GuestLinkClaimed || status.Status == identity.GuestLinkAccessLocked {
			return status.Home, true
		}
	}
	a.writeGuestHomeStatusError(w, err)
	return identity.GuestHome{}, false
}

func (a *API) writeGuestHomeStatusError(w http.ResponseWriter, err error) {
	var status *identity.HomeStatusError
	if errors.As(err, &status) {
		switch status.Status {
		case identity.GuestLinkAccessLocked:
			WriteError(w, http.StatusForbidden, CodeAccessCodeLocked, "访问码已锁定",
				"请联系能管理这位访客的成员重置访问码")
		default:
			WriteError(w, http.StatusUnauthorized, CodeLinkClaimed, "访客主页已被认领",
				"换设备请在主页输入访问码；如果不是你认领的，请联系邀请你的成员")
		}
		return
	}
	if errors.Is(err, identity.ErrInvalidCredentials) {
		WriteError(w, http.StatusUnauthorized, CodeInvalidToken, "凭证无效或已撤销", "")
		return
	}
	panic(err)
}

func (a *API) writeGuestClaimError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tickets.ErrClaimNameRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeClaimNameRequired, err.Error(),
			"请填写你的称呼")
	case errors.Is(err, tickets.ErrLinkAlreadyClaimed):
		WriteError(w, http.StatusConflict, CodeLinkClaimed, err.Error(), "")
	case errors.Is(err, tickets.ErrHomeAlreadyClaimed):
		WriteError(w, http.StatusConflict, CodeLinkClaimed, err.Error(), "")
	case errors.Is(err, tickets.ErrAccessCodeRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeAccessCodeRequired, err.Error(),
			"访问码是认领主页时显示的 6 位数字")
	case errors.Is(err, tickets.ErrAccessCodeInvalid):
		WriteError(w, http.StatusUnauthorized, CodeAccessCodeInvalid, err.Error(),
			"请核对访问码；连续输错 5 次会锁定")
	case errors.Is(err, tickets.ErrAccessCodeLocked):
		WriteError(w, http.StatusForbidden, CodeAccessCodeLocked, err.Error(),
			"请联系能管理这位访客的成员重置访问码")
	default:
		panic(err)
	}
}

func (a *API) setGuestDeviceCookie(w http.ResponseWriter, result tickets.GuestClaimResult) {
	http.SetCookie(w, &http.Cookie{
		Name: GuestDeviceCookieName, Value: result.DeviceToken, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Expires: result.ClaimedAt.Add(365 * 24 * time.Hour),
	})
}

func (a *API) newGuestHomeView(guestName, state string, items []tickets.GuestHomeTicket, r *http.Request) guestHomeView {
	views := make([]guestHomeTicketView, 0, len(items))
	for _, item := range items {
		views = append(views, guestHomeTicketView{
			Number: item.Number, Title: item.Title, Status: item.Status, API: item.API,
			WaitingOn: item.WaitingOn, Reason: item.Reason, ReadOnly: item.ReadOnly,
			ExpiresAt: item.ExpiresAt.Format(time.RFC3339Nano), CanEditBrief: item.CanEditBrief,
		})
	}
	return guestHomeView{
		State: state, GuestName: guestName, Tickets: views,
		GuestAgentInstruction: a.guestHomeAgentInstruction(r),
	}
}

func (a *API) guestHomeAgentInstruction(r *http.Request) string {
	base := a.requestBaseURL(r)
	return `请处理我在 agent-taskboard 访客主页上的全部工单。
1. 凭证：主页地址 ` + guestHomeURLPlaceholder + `（只给你自己的 Agent，不要转发）。
   访问码：<请填入你的 6 位访问码>
   主页地址 /h/ 后面是主页密钥；请求头写 Authorization: Bearer tbh_<主页密钥>:<访问码>。读不到访问码或返回 401/403 就停下来找我。
2. 不熟悉平台先读说明书：` + base + `/agent.md
3. 先列工单：curl -H "Authorization: Bearer tbh_<主页密钥>:<访问码>" "` + base + `/api/v1/guest-home"
4. 对返回的每张工单，按 api 字段逐张读取，照工单末尾「下一步」处理；一次可以批量处理多张，但不要把不同工单的回复写串。
5. 你写的回复和说明修订会直接发出（访客 Agent 不走成员草稿流程）；署名如实写明由 Agent 写成。
6. 写完用一两句话告诉我每张工单做了什么。`
}

func (a *API) guestHomePath(key string, r *http.Request) string {
	if key == "" {
		return ""
	}
	return a.absURL("/h/" + key)
}
