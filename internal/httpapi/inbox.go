// 收件箱接口（#36）：GET /api/v1/inbox，仅限成员（访客 403 members_only，
// 决议 4：访客调成员专属接口统一这个码）。
package httpapi

import (
	"net/http"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// handleInbox 处理 GET /api/v1/inbox：列出「现在轮到我」的开启工单，每项带
// 原因（reason）和增量读取地址（read_new）；同一成员名下的所有 Agent 共用
// 一份。未指派、轮到本部门的工单进入有效初筛人的收件箱。
func (a *API) handleInbox(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "收件箱用 GET 读取")
		return
	}
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	if viewer.Kind != identity.ViewerMember {
		WriteError(w, http.StatusForbidden, CodeMembersOnly, "收件箱只对成员开放",
			"访客没有收件箱；用 GET /api/v1/session 找到链接指向的工单，再读它的 waiting_on")
		return
	}
	items, err := a.tickets.Inbox(r.Context(), viewer)
	if err != nil {
		panic(err)
	}
	views := make([]inboxItemView, 0, len(items))
	for _, it := range items {
		views = append(views, newInboxItemView(it))
	}
	writeJSON(w, http.StatusOK, inboxEnvelope{
		Member: viewer.Member.Name,
		Count:  len(views),
		Items:  views,
	})
}

// handleWaitingCounterparts 处理 GET /api/v1/inbox/waiting-on-counterparts：
// 列出我作为创建者或指派人、当前轮到往来对象的开启工单。count 是这份
// 参考清单自己的数量，不是收件箱数量。
func (a *API) handleWaitingCounterparts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "在等他用 GET 读取")
		return
	}
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	if viewer.Kind != identity.ViewerMember {
		WriteError(w, http.StatusForbidden, CodeMembersOnly, "在等他只对成员开放",
			"访客没有按往来对象分组的收件箱视图")
		return
	}
	items, err := a.tickets.WaitingCounterparts(r.Context(), viewer)
	if err != nil {
		panic(err)
	}
	views := make([]inboxItemView, 0, len(items))
	for _, it := range items {
		views = append(views, newInboxItemView(it))
	}
	writeJSON(w, http.StatusOK, inboxEnvelope{
		Member: viewer.Member.Name,
		Count:  len(views),
		Items:  views,
	})
}

// inboxItemView 是收件箱项的对外结构。
type inboxItemView struct {
	Number      string               `json:"number"`
	Title       string               `json:"title"`
	Role        string               `json:"role"` // assignee | creator | triager
	Reason      string               `json:"reason"`
	ReasonCode  string               `json:"reason_code"`
	SinceSeq    *int64               `json:"since_seq"` // 推导依据的回复事件 seq；无相关回复时为 null
	ReadNew     string               `json:"read_new"`  // 增量读取地址（after = 我在该单最后一条事件）
	ReadFull    string               `json:"read_full"` // 全文 Markdown 地址
	Kind        string               `json:"kind"`
	Creator     string               `json:"creator"`
	Assignee    *authorView          `json:"assignee"`
	Labels      []string             `json:"labels"`
	WaitingOn   string               `json:"waiting_on"`
	Who         string               `json:"who"`
	UpdatedAt   time.Time            `json:"updated_at"`
	Trigger     inboxActorView       `json:"trigger"`
	LastReplyAt *time.Time           `json:"last_reply_at"`
	CreatedAt   time.Time            `json:"created_at"`
	Counterpart inboxCounterpartView `json:"counterpart"`
	Unread      bool                 `json:"unread"`
}

type inboxActorView struct {
	Type   string     `json:"type"`
	Name   string     `json:"name"`
	Member *memberRef `json:"member,omitempty"`
	Agent  *agentRef  `json:"agent,omitempty"`
}

type inboxCounterpartView struct {
	Type   string     `json:"type"`
	Name   *string    `json:"name,omitempty"`
	Member *memberRef `json:"member,omitempty"`
}

func newInboxItemView(it tickets.InboxItem) inboxItemView {
	v := inboxItemView{
		Number: it.Number, Title: it.Title, Role: it.Role, Reason: it.Reason,
		ReasonCode: it.ReasonCode,
		ReadNew:    it.ReadNew, ReadFull: it.ReadFull,
		Kind: it.Kind, Creator: it.Creator,
		Labels: it.Labels, WaitingOn: it.Waiting, Who: it.Who, UpdatedAt: it.UpdatedAt,
		Trigger:     newInboxActorView(it.Trigger),
		LastReplyAt: it.LastReplyAt,
		CreatedAt:   it.CreatedAt,
		Counterpart: newInboxCounterpartView(it.Counterpart),
		Unread:      it.Unread,
	}
	if it.SinceSeq != 0 {
		s := it.SinceSeq
		v.SinceSeq = &s
	}
	if it.AssigneeID != 0 {
		assignee := newAuthorView(tickets.Author{
			MemberID: it.AssigneeID, MemberName: it.AssigneeName, MemberDeactivated: it.AssigneeDeactivated,
		}, true)
		v.Assignee = &assignee
	}
	return v
}

func newInboxActorView(actor tickets.InboxActor) inboxActorView {
	v := inboxActorView{Type: actor.Type, Name: actor.Name}
	if actor.Member != nil {
		v.Member = &memberRef{ID: actor.Member.MemberID, Name: actor.Member.MemberName}
		if actor.Member.MemberDeactivated {
			deactivated := true
			v.Member.Deactivated = &deactivated
		}
	}
	if actor.AgentID != 0 {
		v.Agent = &agentRef{ID: actor.AgentID, Name: actor.Agent}
	}
	return v
}

func newInboxCounterpartView(c tickets.InboxCounterpart) inboxCounterpartView {
	v := inboxCounterpartView{Type: c.Type}
	if c.Name != "" {
		name := c.Name
		v.Name = &name
	}
	if c.MemberID != 0 {
		v.Member = &memberRef{ID: c.MemberID, Name: c.Name}
	}
	return v
}

// inboxEnvelope 是收件箱响应。
type inboxEnvelope struct {
	Member string          `json:"member"`
	Count  int             `json:"count"`
	Items  []inboxItemView `json:"items"`
}
