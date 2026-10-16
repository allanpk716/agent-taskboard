// 访客链接的 HTTP 侧（#32）：创建、列表、撤销与延期。任何成员（含成员 Agent）
// 都能操作；访客 403 members_only。密钥只在创建响应里出现一次。
package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// guestLinkRequest 是 POST …/guest-links 与 …/guest-links/{id}/extend 的请求体：
// valid_days 与 expires_at 二选一（都不填时创建默认 30 天，延期必填其一）。
// ValidDays 用指针区分「没写」与「写了 0」——写了 0 或其他非法值都是 422
// （没有永久有效的选项）。
type guestLinkRequest struct {
	Note         string `json:"note"`
	ValidDays    *int   `json:"valid_days"`
	ExpiresAt    string `json:"expires_at"`
	CanEditBrief bool   `json:"can_edit_brief"`
}

// guestLinkView 是访客链接的对外结构（列表项不含密钥）。
type guestLinkView struct {
	ID           int64      `json:"id"`
	URL          string     `json:"url,omitempty"` // 仅创建响应：/g/<密钥>，只出现一次
	Note         string     `json:"note"`
	ExpiresAt    string     `json:"expires_at"`
	RevokedAt    *string    `json:"revoked_at,omitempty"`
	CanEditBrief bool       `json:"can_edit_brief"`
	CreatedBy    authorView `json:"created_by"`
	CreatedAt    string     `json:"created_at"`
}

// handleGuestLinks 处理 …/guest-links 与 …/guest-links/{id}[/extend]：
//
//	POST   /api/v1/tickets/{id}/guest-links            创建（任何成员）
//	GET    /api/v1/tickets/{id}/guest-links            列表（任何成员；不含密钥）
//	DELETE /api/v1/tickets/{id}/guest-links/{链接id}    撤销（立即生效，幂等 204）
//	POST   /api/v1/tickets/{id}/guest-links/{链接id}/extend  延期（已失效的不能延期）
func (a *API) handleGuestLinks(w http.ResponseWriter, r *http.Request, ref string, rest []string) {
	if len(rest) == 0 {
		switch r.Method {
		case http.MethodPost:
			a.createGuestLink(w, r, ref)
		case http.MethodGet:
			a.listGuestLinks(w, r, ref)
		default:
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		}
		return
	}
	id, ok := guestLinkID(w, rest[0])
	if !ok {
		return
	}
	if len(rest) == 1 {
		if r.Method != http.MethodDelete {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "撤销访客链接用 DELETE")
			return
		}
		a.revokeGuestLink(w, r, ref, id)
		return
	}
	if len(rest) == 2 && rest[1] == "extend" {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "延期访客链接用 POST")
			return
		}
		a.extendGuestLink(w, r, ref, id)
		return
	}
	WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口",
		"访客链接接口：POST …/guest-links、GET …/guest-links、DELETE …/guest-links/{id}、POST …/guest-links/{id}/extend")
}

func (a *API) createGuestLink(w http.ResponseWriter, r *http.Request, ref string) {
	viewer, ok := a.memberFromRequest(w, r)
	if !ok {
		return
	}
	var req guestLinkRequest
	if !decodeJSON(w, r, &req, `{"note": "检验科-张三", "valid_days": 30, "expires_at": "2026-12-01", "can_edit_brief": false}`) {
		return
	}
	spec, ok := parseGuestLinkSpec(w, req)
	if !ok {
		return
	}
	link, err := a.tickets.CreateGuestLink(r.Context(), viewer, ref, spec)
	if err != nil {
		a.writeTicketError(w, r, ref, err)
		return
	}
	writeJSON(w, http.StatusCreated, guestLinkView{
		ID: link.ID, URL: "/g/" + link.Key, Note: link.Note,
		ExpiresAt: link.ExpiresAt.Format(time.RFC3339Nano), CanEditBrief: link.CanEditBrief,
	})
}

func (a *API) listGuestLinks(w http.ResponseWriter, r *http.Request, ref string) {
	if _, ok := a.memberFromRequest(w, r); !ok {
		return
	}
	links, err := a.tickets.GuestLinks(r.Context(), ref)
	if err != nil {
		a.writeTicketError(w, r, ref, err)
		return
	}
	out := []guestLinkView{}
	for _, l := range links {
		out = append(out, newGuestLinkView(l))
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": out})
}

func (a *API) revokeGuestLink(w http.ResponseWriter, r *http.Request, ref string, id int64) {
	viewer, ok := a.memberFromRequest(w, r)
	if !ok {
		return
	}
	if err := a.tickets.RevokeGuestLink(r.Context(), viewer, ref, id); err != nil {
		a.writeTicketError(w, r, ref, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) extendGuestLink(w http.ResponseWriter, r *http.Request, ref string, id int64) {
	viewer, ok := a.memberFromRequest(w, r)
	if !ok {
		return
	}
	var req guestLinkRequest
	if !decodeJSON(w, r, &req, `{"valid_days": 90} 或 {"expires_at": "2026-12-01"}`) {
		return
	}
	spec, ok := parseGuestLinkSpec(w, req)
	if !ok {
		return
	}
	if spec.ValidDays == 0 && spec.ExpiresAt == nil {
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidValidDays,
			"延期需要指明新的有效期", "带 valid_days: 7|30|90 或 expires_at；已撤销或已过期的链接不能延期")
		return
	}
	link, err := a.tickets.ExtendGuestLink(r.Context(), viewer, ref, id, spec)
	if err != nil {
		a.writeTicketError(w, r, ref, err)
		return
	}
	writeJSON(w, http.StatusOK, newGuestLinkView(link))
}

func newGuestLinkView(l tickets.GuestLinkInfo) guestLinkView {
	v := guestLinkView{
		ID: l.ID, Note: l.Note,
		ExpiresAt:    l.ExpiresAt.Format(time.RFC3339Nano),
		CanEditBrief: l.CanEditBrief,
		CreatedBy:    newAuthorView(l.CreatedBy),
		CreatedAt:    l.CreatedAt.Format(time.RFC3339Nano),
	}
	if l.RevokedAt != nil {
		t := l.RevokedAt.Format(time.RFC3339Nano)
		v.RevokedAt = &t
	}
	return v
}

// parseGuestLinkSpec 解析请求体里的有效期写法：valid_days（7/30/90）或
// expires_at（YYYY-MM-DD 或 RFC3339，北京时间；日期按当天 00:00 起算）。
// 语义校验（天数枚举、必须晚于现在）留给工单模块。
func parseGuestLinkSpec(w http.ResponseWriter, req guestLinkRequest) (tickets.GuestLinkSpec, bool) {
	spec := tickets.GuestLinkSpec{Note: req.Note, CanEditBrief: req.CanEditBrief}
	if req.ValidDays != nil {
		if *req.ValidDays == 0 {
			WriteError(w, http.StatusUnprocessableEntity, CodeInvalidValidDays,
				"valid_days 不能是 0：访客链接没有永久有效的选项", "不填时默认 30 天；或填 7、30、90，或改用 expires_at")
			return tickets.GuestLinkSpec{}, false
		}
		spec.ValidDays = *req.ValidDays
	}
	if raw := strings.TrimSpace(req.ExpiresAt); raw != "" {
		t, ok := parseExpiresAt(w, raw)
		if !ok {
			return tickets.GuestLinkSpec{}, false
		}
		spec.ExpiresAt = &t
	}
	return spec, true
}

// parseExpiresAt 把日期（YYYY-MM-DD，按北京时间当天 00:00）或时刻（RFC3339）
// 解析成时间。
func parseExpiresAt(w http.ResponseWriter, raw string) (time.Time, bool) {
	if t, err := time.ParseInLocation("2006-01-02", raw, clock.Beijing); err == nil {
		return t, true
	}
	if t, err := time.ParseInLocation(time.RFC3339, raw, clock.Beijing); err == nil {
		return t, true
	}
	WriteError(w, http.StatusUnprocessableEntity, CodeInvalidExpiresAt,
		"expires_at 应写成日期（2026-12-01）或时刻（2026-12-01T18:00:00+08:00）",
		"日期按北京时间当天 00:00 起算；访客链接没有永久有效的选项")
	return time.Time{}, false
}

// guestLinkID 解析路径里的链接 id。
func guestLinkID(w http.ResponseWriter, raw string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusNotFound, CodeGuestLinkNotFound, "没有这条访客链接",
			"链接 id 是创建响应里的数字 id；列表见 GET /api/v1/tickets/{id}/guest-links")
		return 0, false
	}
	return id, true
}
