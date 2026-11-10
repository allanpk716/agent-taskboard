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

// guestView 是访客名单/详情共用的访客结构；#332 起带停用状态。
type guestView struct {
	ID                 int64      `json:"id"`
	Name               string     `json:"name"`
	DisabledAt         *time.Time `json:"disabled_at,omitempty"`
	ClaimedAt          *time.Time `json:"claimed_at,omitempty"`
	ClaimName          string     `json:"claim_name,omitempty"`
	AccessCodeLockedAt *time.Time `json:"access_code_locked_at,omitempty"`
	LinkCount          int        `json:"link_count"`
	CreatedBy          authorView `json:"created_by"`
	CreatedAt          time.Time  `json:"created_at"`
}

// handleGuestsRef 处理 /api/v1/guests/{id}[/<操作>]：
//
//	GET  /api/v1/guests/{id}                       访客详情（可见范围内的工单与链接）
//	POST /api/v1/guests/{id}/home-share-text        复制主页文案
//	POST /api/v1/guests/{id}/rename                 改名
//	POST /api/v1/guests/{id}/disable                停用（仅网页成员会话）
//	POST /api/v1/guests/{id}/enable                 恢复（仅网页成员会话）
func (a *API) handleGuestsRef(w http.ResponseWriter, r *http.Request) {
	rest := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if (len(rest) != 4 && len(rest) != 5) || rest[0] != "api" || rest[1] != "v1" || rest[2] != "guests" ||
		rest[3] == "" {
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口", "")
		return
	}
	id, err := strconv.ParseInt(rest[3], 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "访客 id 必须是名单里的数字 id", "")
		return
	}
	if len(rest) == 4 {
		a.guestDetail(w, r, id)
		return
	}
	switch rest[4] {
	case "home-share-text":
		a.copyGuestHomeShareText(w, r, id)
	case "rename":
		a.renameGuest(w, r, id)
	case "disable":
		a.setGuestDisabled(w, r, id, true)
	case "enable":
		a.setGuestDisabled(w, r, id, false)
	default:
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口", "")
	}
}

// guestDetail 处理 GET /api/v1/guests/{id}：任何成员（含成员 Agent）都能查看，
// 工单与链接按调用者可见范围过滤。
func (a *API) guestDetail(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
			"查看访客详情用 GET /api/v1/guests/{id}")
		return
	}
	viewer, ok := a.memberFromRequestFor(w, r, "访客不能查看部门访客详情")
	if !ok {
		return
	}
	detail, err := a.tickets.GuestDetail(r.Context(), viewer, id)
	switch {
	case errors.Is(err, tickets.ErrGuestNotFound):
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "没有这位访客", "")
	case err != nil:
		panic(err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"guest": newGuestView(detail.Guest), "tickets": newGuestDetailTicketViews(detail.Tickets),
		})
	}
}

type guestDetailTicketView struct {
	Number string          `json:"number"`
	Title  string          `json:"title"`
	Status string          `json:"status"`
	API    string          `json:"api"`
	Links  []guestLinkView `json:"links"`
}

func newGuestDetailTicketViews(items []tickets.GuestDetailTicket) []guestDetailTicketView {
	views := make([]guestDetailTicketView, 0, len(items))
	for _, item := range items {
		links := make([]guestLinkView, 0, len(item.Links))
		for _, l := range item.Links {
			links = append(links, newGuestLinkView(l))
		}
		views = append(views, guestDetailTicketView{
			Number: item.Number, Title: item.Title, Status: item.Status,
			API: "/api/v1/tickets/" + item.Number, Links: links,
		})
	}
	return views
}

// copyGuestHomeShareText 处理 POST /api/v1/guests/{id}/home-share-text：成员和
// 成员 Agent 可随时重新复制访客主页文案；响应不带访问码。
func (a *API) copyGuestHomeShareText(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
			"复制访客主页文案用 POST /api/v1/guests/{id}/home-share-text")
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

// renameGuest 处理 POST /api/v1/guests/{id}/rename：访客建立者、管理员或名下
// 任一链接的管理人可以改名，成员 Agent 同样可以代主人操作。
func (a *API) renameGuest(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
			"给访客改名用 POST /api/v1/guests/{id}/rename")
		return
	}
	viewer, ok := a.memberFromRequestFor(w, r, "访客不能给部门访客改名")
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req, `{"name": "检验科-张三"}`) {
		return
	}
	guest, err := a.tickets.RenameGuest(r.Context(), viewer, id, req.Name)
	switch {
	case errors.Is(err, tickets.ErrGuestNotFound):
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "没有这位访客", "")
	case errors.Is(err, tickets.ErrGuestNameRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeGuestNameRequired, err.Error(),
			"请填写去空白后的访客名字")
	case errors.Is(err, tickets.ErrGuestManagementNotAllowed):
		WriteError(w, http.StatusForbidden, CodeGuestManagementNotAllowed, err.Error(),
			"只有访客建立者、管理员或名下链接的管理人可以改名")
	case err != nil:
		panic(err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"guest": newGuestView(guest)})
	}
}

// setGuestDisabled 处理停用/恢复。停用等同名下链接全部失效但不改写各链接
// 的撤销与有效期字段；影响多张工单且难撤回，只接受网页成员会话。
func (a *API) setGuestDisabled(w http.ResponseWriter, r *http.Request, id int64, disable bool) {
	action, path := "停用", "disable"
	if !disable {
		action, path = "恢复", "enable"
	}
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法",
			action+"访客用 POST /api/v1/guests/{id}/"+path)
		return
	}
	viewer, ok := a.memberFromRequestFor(w, r, "访客不能"+action+"部门访客")
	if !ok {
		return
	}
	var err error
	if disable {
		err = a.tickets.DisableGuest(r.Context(), viewer, id)
	} else {
		err = a.tickets.EnableGuest(r.Context(), viewer, id)
	}
	switch {
	case errors.Is(err, tickets.ErrGuestNotFound):
		WriteError(w, http.StatusNotFound, CodeGuestNotFound, "没有这位访客", "")
	case errors.Is(err, tickets.ErrGuestAlreadyDisabled):
		WriteError(w, http.StatusConflict, CodeGuestAlreadyDisabled, err.Error(), "访客已停用，无需重复停用")
	case errors.Is(err, tickets.ErrGuestNotDisabled):
		WriteError(w, http.StatusConflict, CodeGuestNotDisabled, err.Error(), "这位访客当前是正常状态")
	case errors.Is(err, tickets.ErrGuestManagementNotAllowed):
		if !viewer.IsWebSession() {
			WriteError(w, http.StatusForbidden, CodeWebSessionRequired, "停用和恢复访客只接受网页成员会话",
				"成员 Agent 不能代替主人停用或恢复访客")
			return
		}
		WriteError(w, http.StatusForbidden, CodeGuestManagementNotAllowed, err.Error(),
			"只有访客建立者、管理员或名下链接的管理人可以在网页上停用或恢复")
	case err != nil:
		panic(err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func newGuestView(g tickets.GuestInfo) guestView {
	return guestView{
		ID: g.ID, Name: g.Name, DisabledAt: g.DisabledAt, ClaimedAt: g.ClaimedAt, ClaimName: g.ClaimName,
		AccessCodeLockedAt: g.AccessCodeLockedAt, LinkCount: g.LinkCount,
		CreatedBy: newAuthorView(g.CreatedBy, false), CreatedAt: g.CreatedAt,
	}
}
