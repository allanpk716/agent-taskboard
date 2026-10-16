package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/labels"
	"github.com/allanpk716/agent-taskboard/internal/markdownview"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// decodeJSON 把请求体解码进 out；不是合法 JSON 时写出 400 并返回 false。
func decodeJSON(w http.ResponseWriter, r *http.Request, out any, want string) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(out); err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "请求体不是合法的 JSON", "请求体应为 "+want)
		return false
	}
	return true
}

// handleTicketsRoot 处理工单集合：
//
//	POST /api/v1/tickets  建单（不用模板版；列表在 #37、模板建单在 #34）
func (a *API) handleTicketsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		a.createTicket(w, r)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
	}
}

// handleTicketRef 处理单张工单：
//
//	GET /api/v1/tickets/{编号或序号}[?format=markdown]
//
// 工单引用接受 YYYYMMDD-NNN 工单编号或全局序号（可带 # 前缀）。
func (a *API) handleTicketRef(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.readTicket(w, r)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
	}
}

// createTicketRequest 是 POST /api/v1/tickets 的请求体。
// assignee（建单时指派与 share_text）预留给 #36，本工单先忽略。
type createTicketRequest struct {
	Kind     string   `json:"kind"`
	Title    string   `json:"title"`
	BriefMD  string   `json:"brief_md"`
	Labels   []string `json:"labels"`
	Assignee string   `json:"assignee"`
}

// createTicketResponse 是建单成功的响应（201）。
type createTicketResponse struct {
	Number string `json:"number"`
	Seq    int64  `json:"seq"`
	URL    string `json:"url"`
}

func (a *API) createTicket(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	var req createTicketRequest
	if !decodeJSON(w, r, &req, `{"kind": "task|report", "title": "…", "brief_md": "…", "labels": ["维度:值"]}`) {
		return
	}
	t, err := a.tickets.Create(r.Context(), tickets.CreateInput{
		Kind:    req.Kind,
		Title:   req.Title,
		BriefMD: req.BriefMD,
		Labels:  req.Labels,
		Creator: viewer,
	})
	if err != nil {
		a.writeTicketError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, createTicketResponse{
		Number: t.Number,
		Seq:    t.Seq,
		URL:    "/t/" + t.Number,
	})
}

func (a *API) readTicket(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	ref := strings.TrimPrefix(r.URL.Path, "/api/v1/tickets/")
	if strings.Contains(ref, "/") { // /timeline 等子资源由后续工单实现
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口", "读工单用 /api/v1/tickets/{编号或序号}")
		return
	}
	full, err := a.tickets.ByRef(r.Context(), ref)
	if err != nil {
		a.writeTicketError(w, r, err)
		return
	}
	if r.URL.Query().Get("format") == "markdown" {
		md := markdownview.Ticket(ticketMarkdownInput(full, viewer))
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(md))
		return
	}
	writeJSON(w, http.StatusOK, newTicketView(full, viewer))
}

// writeTicketError 把工单模块的错误映射成 HTTP 错误。
func (a *API) writeTicketError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tickets.ErrTitleRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeTitleRequired, "标题必填", "")
	case errors.Is(err, tickets.ErrInvalidKind):
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidKind, "kind 必须是 task（任务）或 report（反馈）", "不传时默认 task；用模板创建时类型由模板决定")
	case errors.Is(err, tickets.ErrDimensionLabelRequired):
		hint := "标签至少要有一个 维度:值 形式的维度标签"
		if dims, derr := a.tickets.Labels().DimensionNames(r.Context()); derr == nil && len(dims) > 0 {
			if values, verr := a.tickets.Labels().DimensionValues(r.Context(), dims[0]); verr == nil && len(values) > 0 {
				hint += "，如 " + dims[0] + ":" + values[0]
			}
		}
		WriteError(w, http.StatusUnprocessableEntity, CodeDimensionRequired,
			"成员创建工单时至少要打一个维度标签", hint)
	case isUnknownLabelValue(err):
		e := err.(*labels.UnknownValueError)
		extra := map[string]any{"suggestions": e.Suggestions}
		WriteErrorExtra(w, http.StatusUnprocessableEntity, CodeUnknownLabelValue,
			e.Error(), "改用字典中已有的值；完整字典见 GET /api/v1/labels（#31 提供）", extra)
	case errors.Is(err, tickets.ErrNotFound):
		WriteError(w, http.StatusNotFound, CodeTicketNotFound, "没有这张工单",
			"用工单编号（YYYYMMDD-NNN）或全局序号引用；编号按北京时间分配")
	default:
		panic(err)
	}
}

func isUnknownLabelValue(err error) bool {
	var e *labels.UnknownValueError
	return errors.As(err, &e)
}

// memberRef 是 viewer/作者里成员的对外结构。
type memberRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// agentRef 是具名 Agent 的对外结构。
type agentRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// authorView 是作者结构 {member, agent}：本人操作 agent 为 null。
type authorView struct {
	Member memberRef `json:"member"`
	Agent  *agentRef `json:"agent"`
}

func newAuthorView(a tickets.Author) authorView {
	v := authorView{Member: memberRef{ID: a.MemberID, Name: a.MemberName}}
	if a.AgentID != 0 {
		v.Agent = &agentRef{ID: a.AgentID, Name: a.AgentName}
	}
	return v
}

type waitingOnView struct {
	Party  string  `json:"party"`
	Member *string `json:"member"`
	Text   string  `json:"text"`
	Reason string  `json:"reason"`
}

type briefView struct {
	Fields   *map[string]string `json:"fields"`
	Markdown string             `json:"markdown"`
	Revision int64              `json:"revision"`
	Template *string            `json:"template"`
}

type canView struct {
	Reply            bool `json:"reply"`
	Upload           bool `json:"upload"`
	EditBrief        bool `json:"edit_brief"`
	Label            bool `json:"label"`
	Close            bool `json:"close"`
	Reopen           bool `json:"reopen"`
	ManageGuestLinks bool `json:"manage_guest_links"`
	Deliver          bool `json:"deliver"`
	Assign           bool `json:"assign"`
}

type ticketViewerView struct {
	Kind     string    `json:"kind"`
	Member   memberRef `json:"member"`
	Agent    *agentRef `json:"agent"`
	Role     string    `json:"role"`
	YourTurn bool      `json:"your_turn"`
	Can      canView   `json:"can"`
}

type eventView struct {
	Seq    int64      `json:"seq"`
	Change int64      `json:"change"`
	At     time.Time  `json:"at"`
	Actor  authorView `json:"actor"`
	Type   string     `json:"type"`
}

type ticketView struct {
	Number      string           `json:"number"`
	Seq         int64            `json:"seq"`
	URL         string           `json:"url"`
	Kind        string           `json:"kind"`
	Title       string           `json:"title"`
	Status      string           `json:"status"`
	CloseReason *string          `json:"close_reason"`
	Template    *string          `json:"template"`
	Creator     authorView       `json:"creator"`
	Assignee    *authorView      `json:"assignee"`
	WaitingOn   waitingOnView    `json:"waiting_on"`
	Labels      []string         `json:"labels"`
	Brief       briefView        `json:"brief"`
	Viewer      ticketViewerView `json:"viewer"`
	Timeline    []eventView      `json:"timeline"`
}

// newTicketView 把工单投影 + 当前凭证组装成 GET 响应。
// 访客可见性过滤（无 labels、跳过标签事件）在访客链接工单 #32 加入。
func newTicketView(full tickets.Full, viewer identity.Viewer) ticketView {
	waitingOn := tickets.WaitingOnOf(full)
	v := ticketView{
		Number:  full.Number,
		Seq:     full.Seq,
		URL:     "/t/" + full.Number,
		Kind:    full.Kind,
		Title:   full.Title,
		Status:  full.Status,
		Creator: newAuthorView(full.Creator),
		Labels:  full.Labels,
		Brief:   briefView{Markdown: full.BriefMD, Revision: full.BriefRevision},
	}
	if full.CloseReason != "" {
		c := full.CloseReason
		v.CloseReason = &c
	}
	if waitingOn.Member != "" {
		m := waitingOn.Member
		v.WaitingOn = waitingOnView{Party: waitingOn.Party, Member: &m, Text: waitingOn.Text, Reason: waitingOn.Reason}
	} else {
		v.WaitingOn = waitingOnView{Party: waitingOn.Party, Text: waitingOn.Text, Reason: waitingOn.Reason}
	}
	tv := ticketViewerView{
		Kind:   string(viewer.Kind),
		Member: memberRef{ID: viewer.Member.ID, Name: viewer.Member.Name},
		Role:   roleOf(viewer, full),
	}
	if viewer.Agent != nil {
		tv.Agent = &agentRef{ID: viewer.Agent.ID, Name: viewer.Agent.Name}
	}
	tv.YourTurn = viewer.Kind == identity.ViewerMember && waitingOn.MemberID == viewer.Member.ID
	tv.Can = capsOf(viewer, full)
	v.Viewer = tv
	for _, e := range full.Timeline {
		v.Timeline = append(v.Timeline, eventView{
			Seq: e.Seq, Change: e.Change, At: e.At, Actor: newAuthorView(e.Actor), Type: e.Type,
		})
	}
	return v
}

// ticketMarkdownInput 把投影 + 当前凭证组装成 Markdown 视图的输入。
func ticketMarkdownInput(full tickets.Full, viewer identity.Viewer) markdownview.Input {
	waitingOn := tickets.WaitingOnOf(full)
	viewerName := viewer.Member.Name
	if viewer.Agent != nil {
		viewerName = viewer.Member.Name + " 的 Agent · " + viewer.Agent.Name
	}
	in := markdownview.Input{
		Number:          full.Number,
		Title:           full.Title,
		Kind:            full.Kind,
		Status:          full.Status,
		CreatorName:     full.Creator.DisplayName(),
		WaitingOnText:   waitingOn.Text,
		WaitingOnReason: waitingOn.Reason,
		Labels:          full.Labels,
		BriefMarkdown:   full.BriefMD,
		BriefRevision:   full.BriefRevision,
		ViewerName:      viewerName,
		ViewerRole:      roleOf(viewer, full),
		YourTurn:        viewer.Kind == identity.ViewerMember && waitingOn.MemberID == viewer.Member.ID,
		APIPath:         "/api/v1/tickets/" + full.Number,
	}
	if full.CloseReason != "" {
		in.CloseReasonText = closeReasonText(full.CloseReason)
	}
	for _, e := range full.Timeline {
		in.Timeline = append(in.Timeline, markdownview.Event{
			Seq: e.Seq, At: e.At, AuthorName: e.Actor.DisplayName(), Type: e.Type,
		})
	}
	return in
}

// roleOf 判定当前凭证在这张工单里的角色：指派人 / 发起人（创建者一方）/ 成员。
func roleOf(viewer identity.Viewer, full tickets.Full) string {
	if viewer.Kind == identity.ViewerGuest {
		return "guest"
	}
	if viewer.Member.ID == full.Creator.MemberID {
		return "creator" // 创建者本人及其任意 Agent 都算创建者一方
	}
	return "member" // assignee 在 #36 加入
}

// capsOf 判定当前凭证能做什么（spec「读取」里 viewer.can 的完整集合）。
// 成员：reply/upload/label/manage_guest_links/assign 恒 true；close 需开启；
// reopen 需已关闭；deliver 需开启且本人是指派人（#36 起）；edit_brief 需是创建者一方。
// 访客的能力表随访客链接工单 #32 加入。
func capsOf(viewer identity.Viewer, full tickets.Full) canView {
	if viewer.Kind != identity.ViewerMember {
		return canView{}
	}
	open := full.Status == "open"
	return canView{
		Reply:            true,
		Upload:           true,
		Label:            true,
		Close:            open,
		Reopen:           !open,
		ManageGuestLinks: true,
		Deliver:          false, // 需要本人是指派人；指派在 #36 加入
		Assign:           true,
		EditBrief:        viewer.Member.ID == full.Creator.MemberID,
	}
}

// closeReasonText 把关闭原因码翻成中文（Markdown 视图用）。
func closeReasonText(reason string) string {
	switch reason {
	case "done":
		return "完成"
	case "wontfix":
		return "不处理"
	case "duplicate":
		return "重复"
	}
	return reason
}
