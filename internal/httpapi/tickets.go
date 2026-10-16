package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/labels"
	"github.com/allanpk716/agent-taskboard/internal/markdownview"
	"github.com/allanpk716/agent-taskboard/internal/templates"
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

// handleTicketRef 处理单张工单及其子资源：
//
//	GET    /api/v1/tickets/{编号或序号}[?format=markdown]
//	PATCH  /api/v1/tickets/{编号或序号}      修订标题/说明（#30）
//	GET    …/timeline[?after=N]            增量读取（#30）
//	POST   …/replies                       回复（#30）
//	POST   …/close  …/reopen               关闭与重开（#30）
//	POST   …/labels                        打标签 / 摘标签（#31）
//	（attachments 在 #33、guest-links 在 #32、assign 在 #36）
//
// 工单引用接受 YYYYMMDD-NNN 工单编号或全局序号（可带 # 前缀）。
func (a *API) handleTicketRef(w http.ResponseWriter, r *http.Request) {
	ref, sub, hasSub := ticketRefAndSub(r.URL.Path)
	if !hasSub {
		switch r.Method {
		case http.MethodGet:
			a.readTicket(w, r, ref)
		case http.MethodPatch:
			a.reviseTicket(w, r, ref)
		default:
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		}
		return
	}
	switch sub {
	case "timeline":
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
			return
		}
		a.ticketTimeline(w, r, ref)
	case "replies":
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
			return
		}
		a.replyTicket(w, r, ref)
	case "close":
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
			return
		}
		a.closeTicket(w, r, ref)
	case "reopen":
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
			return
		}
		a.reopenTicket(w, r, ref)
	case "labels":
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "打标签用 POST")
			return
		}
		a.setTicketLabels(w, r, ref)
	default:
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口",
			"读工单用 /api/v1/tickets/{编号或序号}；回复、关闭、时间线等子资源见 docs/api.md")
	}
}

// createTicketRequest 是 POST /api/v1/tickets 的请求体。
// 用模板创建时带 template 与 fields（类型由模板决定、缺必填只 warnings）；
// assignee（建单时指派与 share_text）预留给 #36，本工单先忽略。
type createTicketRequest struct {
	Kind     string            `json:"kind"`
	Template string            `json:"template"`
	Title    string            `json:"title"`
	BriefMD  string            `json:"brief_md"`
	Fields   map[string]string `json:"fields"`
	Labels   []string          `json:"labels"`
	Assignee string            `json:"assignee"`
}

// createTicketResponse 是建单成功的响应（201）。用模板建单缺必填项时
// 工单照常创建，warnings 逐项列出（选「不确定」算已填）。
type createTicketResponse struct {
	Number   string                  `json:"number"`
	Seq      int64                   `json:"seq"`
	URL      string                  `json:"url"`
	Warnings []tickets.FieldWarning `json:"warnings"`
}

func (a *API) createTicket(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	var req createTicketRequest
	if !decodeJSON(w, r, &req, `{"template": "bug-report", "title": "…", "fields": {"问句？": "…"}, "labels": []}`) {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		t, warnings, err := a.tickets.Create(r.Context(), tickets.CreateInput{
			Kind:        req.Kind,
			Title:       req.Title,
			BriefMD:     req.BriefMD,
			Labels:      req.Labels,
			TemplateKey: req.Template,
			Fields:      req.Fields,
			Creator:     viewer,
		})
		if err != nil {
			a.writeTicketError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, createTicketResponse{
			Number:   t.Number,
			Seq:      t.Seq,
			URL:      "/t/" + t.Number,
			Warnings: warnings,
		})
	})
}

func (a *API) readTicket(w http.ResponseWriter, r *http.Request, ref string) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
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
	var conflict *tickets.RevisionConflictError
	switch {
	case errors.Is(err, tickets.ErrTitleRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeTitleRequired, "标题不能为空", "")
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
			e.Error(), "改用字典中已有的值，或带 allow_new: true 新建；完整字典见 GET /api/v1/labels", extra)
	case isLabelDeactivated(err):
		e := err.(*labels.LabelDeactivatedError)
		WriteError(w, http.StatusUnprocessableEntity, CodeLabelDeactivated, e.Error(),
			"这个值已停用，不能再新打；历史工单上仍保留")
	case errors.Is(err, tickets.ErrNotFound):
		WriteError(w, http.StatusNotFound, CodeTicketNotFound, "没有这张工单",
			"用工单编号（YYYYMMDD-NNN）或全局序号引用；编号按北京时间分配")
	case errors.Is(err, templates.ErrNotFound):
		WriteError(w, http.StatusNotFound, CodeTemplateNotFound, "没有这个模板",
			"模板列表见 GET /api/v1/templates")
	case errors.Is(err, tickets.ErrTemplateInactive):
		WriteError(w, http.StatusUnprocessableEntity, CodeTemplateInactive, err.Error(),
			"已停用的模板不能再用来建单；换用其他模板或新建一份")
	case isUnknownField(err):
		e := err.(*tickets.UnknownFieldError)
		WriteError(w, http.StatusUnprocessableEntity, CodeUnknownField, e.Error(),
			"fields 的键要用模板里的小节标题（问句）原文；字段定义见 GET /api/v1/templates/{key}")
	case isInvalidFieldOption(err):
		e := err.(*tickets.InvalidFieldOptionError)
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidFieldOption, e.Error(),
			"维度字段从字典候选值里选，选「不确定」也算已填")
	case errors.Is(err, tickets.ErrFieldsWithoutTemplate):
		WriteError(w, http.StatusUnprocessableEntity, CodeFieldsWithoutTemplate, err.Error(), "")
	case errors.Is(err, tickets.ErrBriefMDWithTemplate):
		WriteError(w, http.StatusUnprocessableEntity, CodeBriefMDWithTemplate, err.Error(),
			"fields 里只需传改动的字段，并带上 base_revision")
	case errors.As(err, &conflict):
		WriteErrorExtra(w, http.StatusConflict, CodeRevisionConflict,
			conflict.Error(), "重新读取工单，合并修改后再提交",
			map[string]any{"current_revision": conflict.CurrentRevision})
	case errors.Is(err, tickets.ErrEmptyReply):
		WriteError(w, http.StatusUnprocessableEntity, CodeEmptyReply, err.Error(), "写正文后重试；附件引用在 #33 提供")
	case errors.Is(err, tickets.ErrNotBriefOwner):
		WriteError(w, http.StatusForbidden, CodeNotBriefOwner, err.Error(), "只有创建者（本人或其 Agent）能修订说明；其他人用回复补充内容")
	case errors.Is(err, tickets.ErrBaseRevisionRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeBaseRevisionRequired, err.Error(), "先读取工单，取 brief.revision 作 base_revision")
	case errors.Is(err, tickets.ErrInvalidCloseReason):
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidCloseReason, err.Error(), "完成用 done；不处理用 wontfix；重复用 duplicate 并带 duplicate_of")
	case errors.Is(err, tickets.ErrDuplicateOfRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeDuplicateOfRequired, err.Error(), "写重复的那张工单的编号或序号")
	case errors.Is(err, tickets.ErrDuplicateNotFound):
		WriteError(w, http.StatusNotFound, CodeTicketNotFound, err.Error(), "duplicate_of 要指向一张存在的工单（编号或序号）")
	case errors.Is(err, tickets.ErrAlreadyClosed):
		WriteError(w, http.StatusConflict, CodeAlreadyClosed, err.Error(), "需要重新处理时先 POST …/reopen")
	case errors.Is(err, tickets.ErrNotClosed):
		WriteError(w, http.StatusConflict, CodeNotClosed, err.Error(), "")
	case errors.Is(err, tickets.ErrDeliverAssigneeOnly):
		WriteError(w, http.StatusForbidden, CodeDeliverAssigneeOnly, err.Error(), "去掉 deliver 作为普通回复；该你做就请发起人改派（指派在 #36 提供）")
	default:
		panic(err)
	}
}

func isUnknownField(err error) bool {
	var e *tickets.UnknownFieldError
	return errors.As(err, &e)
}

func isInvalidFieldOption(err error) bool {
	var e *tickets.InvalidFieldOptionError
	return errors.As(err, &e)
}

func isUnknownLabelValue(err error) bool {
	var e *labels.UnknownValueError
	return errors.As(err, &e)
}

func isLabelDeactivated(err error) bool {
	var e *labels.LabelDeactivatedError
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
	Label  string     `json:"label,omitempty"` // label_added / label_removed 事件带（#31）

	// 附加字段按事件类型出现（只追加）。
	ReplyNo     int64  `json:"reply_no,omitempty"`
	BodyMD      string `json:"body_md,omitempty"`
	Deliver     bool   `json:"deliver,omitempty"`
	Revision    int64  `json:"revision,omitempty"`
	Reason      string `json:"reason,omitempty"`
	DuplicateOf string `json:"duplicate_of,omitempty"`
}

// newEventView 把时间线事件转成对外结构。
func newEventView(e tickets.Event) eventView {
	return eventView{
		Seq: e.Seq, Change: e.Change, At: e.At, Actor: newAuthorView(e.Actor), Type: e.Type,
		Label: e.Label, ReplyNo: e.ReplyNo, BodyMD: e.BodyMD, Deliver: e.Deliver,
		Revision: e.Revision, Reason: e.Reason, DuplicateOf: e.DuplicateOf,
	}
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
	if full.TemplateKey != "" {
		key := full.TemplateKey
		v.Template = &key
		v.Brief.Template = &key
		// 读取同时返回 fields 和拼好的 brief_md（说明由创建时版本的字段定义拼出）。
		fields := full.Fields
		v.Brief.Fields = &fields
	}
	if full.CloseReason != "" {
		c := full.CloseReason
		v.CloseReason = &c
	}
	v.WaitingOn = newWaitingOnView(waitingOn)
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
		v.Timeline = append(v.Timeline, newEventView(e))
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
	if full.TemplateDef != nil {
		in.AgentNote = full.TemplateDef.AgentNote
		in.TemplateName = full.TemplateDef.Name
	}
	if full.CloseReason != "" {
		in.CloseReasonText = closeReasonText(full.CloseReason)
	}
	for _, e := range full.Timeline {
		in.Timeline = append(in.Timeline, markdownview.Event{
			Seq: e.Seq, At: e.At, AuthorName: e.Actor.DisplayName(), Type: e.Type,
			Label: e.Label, BodyMD: e.BodyMD, Deliver: e.Deliver, Revision: e.Revision,
			Reason: e.Reason, DuplicateOf: e.DuplicateOf,
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
