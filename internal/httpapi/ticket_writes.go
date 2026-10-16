// 单张工单的写操作（#30）：回复、说明修订、关闭与重新开启，以及时间线增量读取。
package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// ticketRefAndSub 从路径 /api/v1/tickets/{ref}[/{子资源}] 里取出工单引用与子资源。
func ticketRefAndSub(path string) (ref, sub string, hasSub bool) {
	rest := strings.TrimPrefix(path, "/api/v1/tickets/")
	ref, sub, hasSub = strings.Cut(rest, "/")
	return ref, sub, hasSub
}

// replyRequest 是 POST …/replies 的请求体。attachment_ids 与访客自报的
// author_name/via_agent 分别在 #33/#32 加入。
type replyRequest struct {
	BodyMD  string `json:"body_md"`
	Deliver bool   `json:"deliver"`
}

// replyTicketView 是回复成功（201）的响应。
type replyTicketView struct {
	ReplyNo     int64         `json:"reply_no"`
	TimelineSeq int64         `json:"timeline_seq"`
	Deliver     bool          `json:"deliver"`
	Author      string        `json:"author"`
	WaitingOn   waitingOnView `json:"waiting_on"`
}

func (a *API) replyTicket(w http.ResponseWriter, r *http.Request, ref string) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	var req replyRequest
	if !decodeJSON(w, r, &req, `{"body_md": "…"}`) {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		result, err := a.tickets.Reply(r.Context(), viewer, ref, tickets.ReplyInput{
			BodyMD:  req.BodyMD,
			Deliver: req.Deliver,
		})
		if err != nil {
			a.writeTicketError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, replyTicketView{
			ReplyNo:     result.ReplyNo,
			TimelineSeq: result.TimelineSeq,
			Deliver:     req.Deliver,
			Author:      result.AuthorName,
			WaitingOn:   newWaitingOnView(result.WaitingOn),
		})
	})
}

// reviseTicketRequest 是 PATCH /api/v1/tickets/{id} 的请求体。
// 只需传改动的部分：title、brief_md（非模板工单）或 fields（模板工单，
// 只传改动的字段）；base_revision 必带（乐观锁）。
type reviseTicketRequest struct {
	Title        *string           `json:"title"`
	BriefMD      *string           `json:"brief_md"`
	Fields       map[string]string `json:"fields"`
	BaseRevision int64             `json:"base_revision"`
}

func (a *API) reviseTicket(w http.ResponseWriter, r *http.Request, ref string) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	var req reviseTicketRequest
	if !decodeJSON(w, r, &req, `{"title?": "…", "brief_md?": "…", "base_revision": 1}`) {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		t, err := a.tickets.Revise(r.Context(), viewer, ref, tickets.ReviseInput{
			Title:        req.Title,
			BriefMD:      req.BriefMD,
			Fields:       req.Fields,
			BaseRevision: req.BaseRevision,
		})
		if err != nil {
			a.writeTicketError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, reviseTicketView{Revision: t.BriefRevision})
	})
}

// reviseTicketView 是修订说明成功（200）的响应。
type reviseTicketView struct {
	Revision int64 `json:"revision"`
}

// closeTicketRequest 是 POST …/close 的请求体；duplicate_of 可写成
// 工单编号、序号（可带 #）或数字。
type closeTicketRequest struct {
	Reason      string `json:"reason"`
	DuplicateOf any    `json:"duplicate_of"`
}

// closeTicketView 是关闭成功（200）的响应。
type closeTicketView struct {
	Status      string  `json:"status"`
	CloseReason string  `json:"close_reason"`
	DuplicateOf *string `json:"duplicate_of"`
}

func (a *API) closeTicket(w http.ResponseWriter, r *http.Request, ref string) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	var req closeTicketRequest
	if !decodeJSON(w, r, &req, `{"reason": "done|wontfix|duplicate", "duplicate_of?": "编号或序号"}`) {
		return
	}
	duplicateOf, ok := duplicateRefOf(w, req.DuplicateOf)
	if !ok {
		return
	}
	t, err := a.tickets.Close(r.Context(), viewer, ref, tickets.CloseInput{
		Reason:      req.Reason,
		DuplicateOf: duplicateOf,
	})
	if err != nil {
		a.writeTicketError(w, r, err)
		return
	}
	view := closeTicketView{Status: t.Status, CloseReason: t.CloseReason}
	if t.DuplicateOfSeq != 0 {
		var dup tickets.Ticket
		if dup, err = a.tickets.BySeq(r.Context(), t.DuplicateOfSeq); err == nil {
			d := dup.Number
			view.DuplicateOf = &d
		} else {
			a.writeTicketError(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, view)
}

// reopenTicketView 是重新开启成功（200）的响应。
type reopenTicketView struct {
	Status    string        `json:"status"`
	WaitingOn waitingOnView `json:"waiting_on"`
}

func (a *API) reopenTicket(w http.ResponseWriter, r *http.Request, ref string) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	full, err := a.tickets.Reopen(r.Context(), viewer, ref)
	if err != nil {
		a.writeTicketError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, reopenTicketView{
		Status:    full.Status,
		WaitingOn: newWaitingOnView(tickets.WaitingOnOf(full)),
	})
}

// ticketTimeline 处理 GET /api/v1/tickets/{id}/timeline?after=N（增量读取）。
// 返回 after 之后的事件、当前游标、等待方、是否轮到当前凭证和状态。
func (a *API) ticketTimeline(w http.ResponseWriter, r *http.Request, ref string) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	after := int64(0)
	if raw := r.URL.Query().Get("after"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			WriteError(w, http.StatusUnprocessableEntity, CodeInvalidCursor,
				"after 必须是非负整数（单内事件序号）", "从上次响应的 cursor 继续；首次读取不带 after")
			return
		}
		after = n
	}
	full, err := a.tickets.ByRef(r.Context(), ref)
	if err != nil {
		a.writeTicketError(w, r, err)
		return
	}
	waitingOn := tickets.WaitingOnOf(full)
	cursor := int64(0)
	if n := len(full.Timeline); n > 0 {
		cursor = full.Timeline[n-1].Seq
	}
	view := timelineEnvelope{
		Events:    []eventView{},
		Cursor:    cursor,
		WaitingOn: newWaitingOnView(waitingOn),
		YourTurn:  viewer.Kind == identity.ViewerMember && waitingOn.MemberID == viewer.Member.ID,
		Status:    full.Status,
	}
	for _, e := range full.Timeline {
		if e.Seq <= after {
			continue
		}
		view.Events = append(view.Events, newEventView(e))
	}
	writeJSON(w, http.StatusOK, view)
}

// timelineEnvelope 是时间线接口的响应。
type timelineEnvelope struct {
	Events    []eventView   `json:"events"`
	Cursor    int64         `json:"cursor"`
	WaitingOn waitingOnView `json:"waiting_on"`
	YourTurn  bool          `json:"your_turn"`
	Status    string        `json:"status"`
}

// duplicateRefOf 把请求里的 duplicate_of（字符串或数字）规范成工单引用文本。
func duplicateRefOf(w http.ResponseWriter, v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", true
	case string:
		return x, true
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10), true
		}
	}
	WriteError(w, http.StatusUnprocessableEntity, CodeInvalidDuplicateOf,
		"duplicate_of 应写成工单编号或序号的字符串", `如 "20261016-002" 或 "2"`)
	return "", false
}

// newWaitingOnView 把推导结果转成对外结构（member 为 null 时省略）。
func newWaitingOnView(waitingOn tickets.WaitingOn) waitingOnView {
	v := waitingOnView{Party: waitingOn.Party, Text: waitingOn.Text, Reason: waitingOn.Reason}
	if waitingOn.Member != "" {
		m := waitingOn.Member
		v.Member = &m
	}
	return v
}
