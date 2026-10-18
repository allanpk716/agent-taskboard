// 单张工单的写操作（#30）：回复、说明修订、关闭与重新开启，以及时间线增量读取。
package httpapi

import (
	"net/http"
	"strconv"

	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// replyRequest 是 POST …/replies 的请求体。attachment_ids 与正文 att: 引用在
// 同一事务里认领（#33）；author_name / via_agent 是访客的自报信息（#32）：只有
// 访客凭证会采纳，成员的署名由凭证决定，不能自报（ADR 0003）。
type replyRequest struct {
	BodyMD        string   `json:"body_md"`
	AttachmentIDs []string `json:"attachment_ids"`
	Deliver       bool     `json:"deliver"`
	AuthorName    string   `json:"author_name"`
	ViaAgent      bool     `json:"via_agent"`
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
			BodyMD:        req.BodyMD,
			AttachmentIDs: req.AttachmentIDs,
			Deliver:       req.Deliver,
			AuthorName:    req.AuthorName,
			ViaAgent:      req.ViaAgent,
		})
		if err != nil {
			a.writeTicketError(w, r, ref, err)
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
// 只传改动的字段）；base_revision 必带（乐观锁）；author_name / via_agent
// 是访客的自报署名（#32，仅访客凭证采纳）。
type reviseTicketRequest struct {
	Title        *string           `json:"title"`
	BriefMD      *string           `json:"brief_md"`
	Fields       map[string]string `json:"fields"`
	BaseRevision int64             `json:"base_revision"`
	AuthorName   string            `json:"author_name"`
	ViaAgent     bool              `json:"via_agent"`
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
			AuthorName:   req.AuthorName,
			ViaAgent:     req.ViaAgent,
		})
		if err != nil {
			a.writeTicketError(w, r, ref, err)
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
	// 关闭仅限成员；访客 403 members_only（决策：访客调成员专属接口统一这个码）。
	viewer, ok := a.memberFromRequest(w, r)
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
		a.writeTicketError(w, r, ref, err)
		return
	}
	view := closeTicketView{Status: t.Status, CloseReason: t.CloseReason}
	if t.DuplicateOfSeq != 0 {
		var dup tickets.Ticket
		if dup, err = a.tickets.BySeq(r.Context(), t.DuplicateOfSeq); err == nil {
			d := dup.Number
			view.DuplicateOf = &d
		} else {
			a.writeTicketError(w, r, ref, err)
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
	// 重新开启仅限成员；访客 403 members_only。
	viewer, ok := a.memberFromRequest(w, r)
	if !ok {
		return
	}
	full, err := a.tickets.Reopen(r.Context(), viewer, ref)
	if err != nil {
		a.writeTicketError(w, r, ref, err)
		return
	}
	writeJSON(w, http.StatusOK, reopenTicketView{
		Status:    full.Status,
		WaitingOn: newWaitingOnView(tickets.WaitingOnOf(full)),
	})
}

// ticketTimeline 处理 GET /api/v1/tickets/{id}/timeline?after=N（增量读取）。
// 返回 after 之后的事件、当前游标、等待方、是否轮到当前凭证和状态。
// 访客（#32）只能读链接指向的工单，且看不到标签事件（cursor 因此是访客可见的
// 最后一条事件 seq，跳过的序号不会当作增量重发）。
// actor_kind=member|agent（#37，orchestrator 决议 5）按「本人 / Agent」筛选
// 事件：member 是非 Agent 署名（成员本人或访客），agent 是成员 Agent；
// cursor 同样只数筛后仍可见的事件。
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
	actorKind := r.URL.Query().Get("actor_kind")
	if actorKind != "" && actorKind != "member" && actorKind != "agent" {
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidActorKind,
			"actor_kind 只能是 member（本人或访客真人署名）或 agent（成员 Agent 或访客自报由 Agent 写成）", "")
		return
	}
	full, ok := a.loadTicketForViewer(w, r, viewer, ref)
	if !ok {
		return
	}
	isGuest := viewer.Kind == identity.ViewerGuest
	visible := func(e tickets.Event) bool {
		if isGuest && !visibleToGuest(e) {
			return false
		}
		switch actorKind {
		case "member":
			return e.Actor.AgentID == 0 && !e.Actor.ViaAgent
		case "agent":
			return e.Actor.AgentID != 0 || e.Actor.ViaAgent
		}
		return true
	}
	waitingOn := tickets.WaitingOnOf(full)
	cursor := int64(0)
	for _, e := range full.Timeline {
		if !visible(e) {
			continue
		}
		cursor = e.Seq
	}
	view := timelineEnvelope{
		Events:    []eventView{},
		Cursor:    cursor,
		WaitingOn: newWaitingOnView(waitingOn),
		YourTurn:  yourTurnOf(viewer, waitingOn),
		Status:    full.Status,
	}
	for _, e := range full.Timeline {
		if e.Seq <= after || !visible(e) {
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

// assignTicketRequest 是 POST …/assign 的请求体：assignee 可写数字成员 id、
// 字符串成员 id 或姓名。
type assignTicketRequest struct {
	Assignee assigneeRef `json:"assignee"`
}

// assignTicketView 是改派成功（200）的响应；改派给他人时带一句可转发的
// share_text。
type assignTicketView struct {
	Assignee  *memberRef    `json:"assignee"`
	WaitingOn waitingOnView `json:"waiting_on"`
	ShareText *string       `json:"share_text"`
}

// assignTicket 处理 POST /api/v1/tickets/{id}/assign（改派，仅成员；访客 403
// members_only）。记一条 assigned{from,to} 事件；改派后原指派人的回复不再
// 参与等待方推导，工单随推导从原指派人的收件箱移到新指派人的收件箱。
func (a *API) assignTicket(w http.ResponseWriter, r *http.Request, ref string) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	if viewer.Kind != identity.ViewerMember {
		WriteError(w, http.StatusForbidden, CodeMembersOnly, "改派只对成员开放",
			"访客不能改派工单；如需转办请联系成员")
		return
	}
	var req assignTicketRequest
	if !decodeJSON(w, r, &req, `{"assignee": "成员 id 或姓名（id 也可写成数字，如 4）；空字符串取消指派"}`) {
		return
	}
	if req.Assignee.invalid {
		writeInvalidAssignee(w)
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		result, err := a.tickets.Assign(r.Context(), viewer, ref, req.Assignee.ref)
		if err != nil {
			a.writeTicketError(w, r, ref, err)
			return
		}
		view := assignTicketView{
			WaitingOn: newWaitingOnView(result.WaitingOn),
		}
		if result.AssigneeID != 0 {
			view.Assignee = &memberRef{ID: result.AssigneeID, Name: result.AssigneeName}
		}
		// 改派给自己不需要通知，share_text 为空。
		if result.AssigneeID != 0 && result.AssigneeID != viewer.Member.ID {
			share := a.reassignShareText(result.AssigneeName, result.Title, result.Number)
			view.ShareText = &share
		}
		writeJSON(w, http.StatusOK, view)
	})
}
