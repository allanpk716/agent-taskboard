package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
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

// handleTicketRef 处理单张工单及其子资源：
//
//	GET    /api/v1/tickets/{编号或序号}[?format=markdown]
//	PATCH  /api/v1/tickets/{编号或序号}      修订标题/说明（#30；访客 #32）
//	GET    …/timeline[?after=N]            增量读取（#30；访客看不到标签事件）
//	POST   …/replies                       回复（#30；访客 #32）
//	POST   …/close  …/reopen               关闭与重开（#30，仅成员）
//	POST   …/labels                        打标签 / 摘标签（#31，仅成员）
//	POST/GET …/guest-links[/… ]            访客链接的创建/列表/撤销/延期（#32，仅成员）
//	（attachments 在 #33、assign 在 #36）
//
// 工单引用接受 YYYYMMDD-NNN 工单编号或全局序号（可带 # 前缀）。
func (a *API) handleTicketRef(w http.ResponseWriter, r *http.Request) {
	segs := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/tickets/"), "/")
	ref := segs[0]
	if len(segs) == 1 {
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
	sub, rest := segs[1], segs[2:]
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
	case "guest-links":
		a.handleGuestLinks(w, r, ref, rest)
	case "attachments":
		a.handleTicketAttachments(w, r, ref)
	default:
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口",
			"读工单用 /api/v1/tickets/{编号或序号}；回复、关闭、时间线等子资源见 docs/api.md")
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
	// 建单仅限成员（含成员 Agent）；访客建单走反馈入口（#35）。
	viewer, ok := a.memberFromRequest(w, r)
	if !ok {
		return
	}
	var req createTicketRequest
	if !decodeJSON(w, r, &req, `{"kind": "task|report", "title": "…", "brief_md": "…", "labels": ["维度:值"]}`) {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		t, err := a.tickets.Create(r.Context(), tickets.CreateInput{
			Kind:    req.Kind,
			Title:   req.Title,
			BriefMD: req.BriefMD,
			Labels:  req.Labels,
			Creator: viewer,
		})
		if err != nil {
			a.writeTicketError(w, r, "", err)
			return
		}
		writeJSON(w, http.StatusCreated, createTicketResponse{
			Number: t.Number,
			Seq:    t.Seq,
			URL:    "/t/" + t.Number,
		})
	})
}

// readTicket 处理 GET /api/v1/tickets/{id}[?format=markdown]。访客链接只能
// 访问它指向的那张工单（其他一律 404，防枚举）；访客看不到 labels 字段与
// 标签事件（事件序号因此跳号，已接受）。
func (a *API) readTicket(w http.ResponseWriter, r *http.Request, ref string) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	full, ok := a.loadTicketForViewer(w, r, viewer, ref)
	if !ok {
		return
	}
	atts, usage, err := a.attachmentProjection(r.Context(), full, viewer)
	if err != nil {
		panic(err)
	}
	if r.URL.Query().Get("format") == "markdown" {
		md := markdownview.Ticket(ticketMarkdownInput(full, viewer, atts, usage))
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(md))
		return
	}
	v := newTicketView(full, viewer)
	v.Attachments, v.Usage = atts, usage
	writeJSON(w, http.StatusOK, v)
}

// loadTicketForViewer 读取 ref 指向的工单并套上访客作用域：访客链接指向
// 其他工单时写 404 ticket_not_found（与「不存在」不可区分）。
func (a *API) loadTicketForViewer(w http.ResponseWriter, r *http.Request, viewer identity.Viewer, ref string) (tickets.Full, bool) {
	full, err := a.tickets.ByRef(r.Context(), ref)
	if err != nil {
		a.writeTicketError(w, r, ref, err)
		return tickets.Full{}, false
	}
	if viewer.Kind == identity.ViewerGuest && viewer.GuestLink.TicketSeq != full.Seq {
		WriteError(w, http.StatusNotFound, CodeTicketNotFound, "没有这张工单",
			"访客链接只能访问它所指向的那张工单")
		return tickets.Full{}, false
	}
	return full, true
}

// writeTicketError 把工单模块的错误映射成 HTTP 错误。ref 是请求里的工单引用
// （错误 hint 里给 NAS 路径示例时用）。
func (a *API) writeTicketError(w http.ResponseWriter, r *http.Request, ref string, err error) {
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
	case errors.As(err, &conflict):
		WriteErrorExtra(w, http.StatusConflict, CodeRevisionConflict,
			conflict.Error(), "重新读取工单，合并修改后再提交",
			map[string]any{"current_revision": conflict.CurrentRevision})
	case errors.Is(err, tickets.ErrEmptyReply):
		WriteError(w, http.StatusUnprocessableEntity, CodeEmptyReply, err.Error(), "写正文，或在 attachment_ids / 正文里引用至少一个附件")
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
	case errors.Is(err, tickets.ErrNoteRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeNoteRequired, err.Error(), "备注写对方是谁，如「检验科-张三」；链接创建后凭备注区分多条链接")
	case errors.Is(err, tickets.ErrInvalidValidDays):
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidValidDays, err.Error(), "valid_days 只能是 7、30 或 90；或改用 expires_at 指定日期，两者二选一")
	case errors.Is(err, tickets.ErrInvalidExpiresAt):
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidExpiresAt, err.Error(), "写成日期（2026-12-01）或时刻（2026-12-01T18:00:00+08:00）")
	case errors.Is(err, tickets.ErrGuestLinkNotFound):
		WriteError(w, http.StatusNotFound, CodeGuestLinkNotFound, err.Error(), "链接 id 是创建响应里的数字 id；列表见 GET /api/v1/tickets/{id}/guest-links")
	case isLinkNotExtendable(err):
		WriteError(w, http.StatusConflict, CodeLinkNotExtendable, err.Error(),
			"需要对方继续参与时创建一条新的访客链接，并重新发给对方")
	case errors.Is(err, tickets.ErrLinkCannotEditBrief):
		WriteError(w, http.StatusForbidden, CodeLinkCannotEditBrief, err.Error(), "用回复补充内容；需要访客能改说明时由成员创建带「可填写说明」权限的链接")
	case errors.Is(err, tickets.ErrTicketClosed):
		WriteError(w, http.StatusConflict, CodeTicketClosed, err.Error(), "如需继续，请联系成员重新开启工单（成员回复不会重开工单）")
	case isAttachmentCountExceeded(err):
		e := err.(*attachments.CountExceededError)
		number := ref
		if full, ferr := a.tickets.ByRef(r.Context(), ref); ferr == nil {
			number = full.Number
		}
		WriteError(w, http.StatusRequestEntityTooLarge, CodeAttachmentCountExceeded,
			e.Error(), nasHint(number, ""))
	case errors.Is(err, attachments.ErrNotUsable):
		WriteError(w, http.StatusUnprocessableEntity, CodeAttachmentNotUsable, err.Error(),
			"先调上传接口拿到 att: 引用，再在自己发的回复或说明里引用它")
	default:
		panic(err)
	}
}

func isAttachmentCountExceeded(err error) bool {
	var e *attachments.CountExceededError
	return errors.As(err, &e)
}

func isUnknownLabelValue(err error) bool {
	var e *labels.UnknownValueError
	return errors.As(err, &e)
}

func isLinkNotExtendable(err error) bool {
	var e *tickets.LinkNotExtendableError
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

// authorView 是作者结构 {member, agent}：本人操作 agent 为 null；访客作者
// （#32）member 与 agent 为 null，另带 link_note（链接备注）、author_name 与
// via_agent（都是自报、未经平台核实的信息，与成员 Agent 的平台认证署名区分）。
type authorView struct {
	Member     *memberRef `json:"member"`
	Agent      *agentRef  `json:"agent"`
	LinkNote   *string    `json:"link_note,omitempty"`
	AuthorName *string    `json:"author_name,omitempty"`
	ViaAgent   *bool      `json:"via_agent,omitempty"`
}

func newAuthorView(a tickets.Author) authorView {
	v := authorView{}
	if a.MemberID != 0 {
		v.Member = &memberRef{ID: a.MemberID, Name: a.MemberName}
		if a.AgentID != 0 {
			v.Agent = &agentRef{ID: a.AgentID, Name: a.AgentName}
		}
		return v
	}
	// 访客作者：带链接备注与自报信息。
	note := a.LinkNote
	v.LinkNote = &note
	if a.AuthorName != "" {
		name := a.AuthorName
		v.AuthorName = &name
	}
	via := a.ViaAgent
	v.ViaAgent = &via
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
	Kind     string     `json:"kind"`
	Member   *memberRef `json:"member"`
	Agent    *agentRef  `json:"agent"`
	Role     string     `json:"role"`
	YourTurn bool       `json:"your_turn"`
	Can      canView    `json:"can"`

	// 访客凭证（#32）：链接备注与到期时刻（can_edit_brief 即 can.edit_brief）。
	LinkNote      *string `json:"link_note,omitempty"`
	LinkExpiresAt *string `json:"link_expires_at,omitempty"`
}

type eventView struct {
	Seq    int64      `json:"seq"`
	Change int64      `json:"change"`
	At     time.Time  `json:"at"`
	Actor  authorView `json:"actor"`
	Type   string     `json:"type"`
	Label  string     `json:"label,omitempty"` // label_added / label_removed 事件带（#31）
	Note   string     `json:"note,omitempty"`  // guest_link_created / guest_link_revoked 的备注（#32）

	// 附加字段按事件类型出现（只追加）。
	ReplyNo     int64  `json:"reply_no,omitempty"`
	BodyMD      string `json:"body_md,omitempty"`
	Deliver     bool   `json:"deliver,omitempty"`
	Revision    int64  `json:"revision,omitempty"`
	Reason      string `json:"reason,omitempty"`
	DuplicateOf string `json:"duplicate_of,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"` // guest_link_extended 的新到期时刻（#32）

	AttachmentIDs []string `json:"attachment_ids,omitempty"` // reply 引用的附件（#33）
}

// newEventView 把时间线事件转成对外结构。
func newEventView(e tickets.Event) eventView {
	return eventView{
		Seq: e.Seq, Change: e.Change, At: e.At, Actor: newAuthorView(e.Actor), Type: e.Type,
		Label: e.Label, Note: e.Note, ReplyNo: e.ReplyNo, BodyMD: e.BodyMD, Deliver: e.Deliver,
		Revision: e.Revision, Reason: e.Reason, DuplicateOf: e.DuplicateOf, ExpiresAt: e.LinkExpires,
		AttachmentIDs: e.AttachmentIDs,
	}
}

// visibleToGuest 报告事件对访客是否可见：标签事件不可见（其余事件——回复、
// 状态变更、说明修订、访客链接、指派——都可见，spec「凭证与访问」）。
func visibleToGuest(e tickets.Event) bool {
	return e.Type != "label_added" && e.Type != "label_removed"
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
	Labels      *[]string        `json:"labels,omitempty"` // 访客视角无此字段（#32）
	Brief       briefView        `json:"brief"`
	Attachments []attachmentView `json:"attachments"`      // 已挂上的（#33）
	Usage       attachmentUsage  `json:"attachment_usage"` // 含待用附件（#33）
	Viewer      ticketViewerView `json:"viewer"`
	Timeline    []eventView      `json:"timeline"`
}

// attachmentView 是附件列表项（#33）：id 是 att: 引用，url 是下载地址
// （访客拿到的是与链接绑定的短时效签名地址）。
type attachmentView struct {
	ID         string `json:"id"`
	Filename   string `json:"filename"`
	Size       int64  `json:"size"` // 字节数
	URL        string `json:"url"`
	AttachedTo string `json:"attached_to"` // 「说明」或「回复 N」
}

// attachmentUsage 是附件用量回显（#33）：待用附件也计入 used。
type attachmentUsage struct {
	UsedBytes        int64   `json:"used_bytes"`
	UsedMB           float64 `json:"used_mb"`
	TicketLimitMB    int     `json:"ticket_limit_mb"`
	FileLimitMB      int     `json:"file_limit_mb"`
	MaxFilesPerPost  int     `json:"max_files_per_post"`
	MaxImagesPerPost int     `json:"max_images_per_post"`
}

// newTicketView 把工单投影 + 当前凭证组装成 GET 响应。访客可见性过滤（#32）：
// 无 labels 字段、时间线跳过标签事件（事件序号会跳号，这是已接受的）。
func newTicketView(full tickets.Full, viewer identity.Viewer) ticketView {
	waitingOn := tickets.WaitingOnOf(full)
	isGuest := viewer.Kind == identity.ViewerGuest
	v := ticketView{
		Number:      full.Number,
		Seq:         full.Seq,
		URL:         "/t/" + full.Number,
		Kind:        full.Kind,
		Title:       full.Title,
		Status:      full.Status,
		Creator:     newAuthorView(full.Creator),
		Brief:       briefView{Markdown: full.BriefMD, Revision: full.BriefRevision},
		Attachments: []attachmentView{},
	}
	if !isGuest {
		labels := full.Labels
		v.Labels = &labels
	}
	if full.CloseReason != "" {
		c := full.CloseReason
		v.CloseReason = &c
	}
	v.WaitingOn = newWaitingOnView(waitingOn)
	tv := ticketViewerView{Kind: string(viewer.Kind), Role: roleOf(viewer, full)}
	if isGuest {
		note := viewer.GuestLink.Note
		expiresAt := viewer.GuestLink.ExpiresAt.Format(time.RFC3339Nano)
		tv.LinkNote = &note
		tv.LinkExpiresAt = &expiresAt
		tv.YourTurn = yourTurnOf(viewer, waitingOn)
	} else {
		tv.Member = &memberRef{ID: viewer.Member.ID, Name: viewer.Member.Name}
		if viewer.Agent != nil {
			tv.Agent = &agentRef{ID: viewer.Agent.ID, Name: viewer.Agent.Name}
		}
	}
	tv.Can = capsOf(viewer, full)
	v.Viewer = tv
	for _, e := range full.Timeline {
		if isGuest && !visibleToGuest(e) {
			continue
		}
		v.Timeline = append(v.Timeline, newEventView(e))
	}
	return v
}

// ticketMarkdownInput 把投影 + 当前凭证组装成 Markdown 视图的输入。
// 访客（#32）：无标签行、时间线跳过标签事件，署名按访客写法；附件小节与
// 用量由 attachmentProjection 生成（#33），访客的下载链接是签名地址。
func ticketMarkdownInput(full tickets.Full, viewer identity.Viewer, atts []attachmentView, usage attachmentUsage) markdownview.Input {
	waitingOn := tickets.WaitingOnOf(full)
	isGuest := viewer.Kind == identity.ViewerGuest
	viewerName := viewer.Member.Name
	if isGuest {
		viewerName = "访客（经「" + viewer.GuestLink.Note + "」链接）"
	} else if viewer.Agent != nil {
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
		BriefMarkdown:   full.BriefMD,
		BriefRevision:   full.BriefRevision,
		ViewerName:      viewerName,
		ViewerRole:      roleOf(viewer, full),
		YourTurn:        yourTurnOf(viewer, waitingOn),
		APIPath:         "/api/v1/tickets/" + full.Number,
		Usage:           strconv.FormatFloat(usage.UsedMB, 'f', -1, 64) + " / " + strconv.Itoa(usage.TicketLimitMB) + " MB",
	}
	for _, att := range atts {
		in.Attachments = append(in.Attachments, markdownview.Attachment{
			Filename: att.Filename, URL: att.URL,
			SizeLabel: sizeLabel(att.Size), AttachedTo: att.AttachedTo,
		})
	}
	if !isGuest {
		labels := full.Labels
		in.Labels = &labels
	}
	if full.CloseReason != "" {
		in.CloseReasonText = closeReasonText(full.CloseReason)
	}
	for _, e := range full.Timeline {
		if isGuest && !visibleToGuest(e) {
			continue
		}
		in.Timeline = append(in.Timeline, markdownview.Event{
			Seq: e.Seq, At: e.At, AuthorName: e.Actor.DisplayName(), Type: e.Type,
			Label: e.Label, BodyMD: e.BodyMD, Deliver: e.Deliver, Revision: e.Revision,
			Reason: e.Reason, DuplicateOf: e.DuplicateOf, Note: e.Note, LinkExpiresAt: e.LinkExpires,
			AttachmentIDs: e.AttachmentIDs,
		})
	}
	return in
}

// yourTurnOf 判定是否轮到当前凭证：成员按推导出的成员，访客按等待方是不是访客。
func yourTurnOf(viewer identity.Viewer, waitingOn tickets.WaitingOn) bool {
	if viewer.Kind == identity.ViewerGuest {
		return waitingOn.Party == "guest"
	}
	return waitingOn.MemberID == viewer.Member.ID
}

// roleOf 判定当前凭证在这张工单里的角色：指派人 / 发起人（创建者一方）/ 成员 / 访客。
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
// 访客（#32）：reply/upload 需工单开启；edit_brief 需开启且链接带「可填写说明」；
// 其余恒 false（工单关闭后链接只读，重开后恢复）。
func capsOf(viewer identity.Viewer, full tickets.Full) canView {
	open := full.Status == "open"
	if viewer.Kind == identity.ViewerGuest {
		return canView{
			Reply:     open,
			Upload:    open,
			EditBrief: open && viewer.GuestLink.CanEditBrief,
		}
	}
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

// sizeLabel 把字节数写成人类可读的大小（Markdown 附件小节用）。
func sizeLabel(n int64) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " MB"
	case n >= 1<<10:
		return strconv.FormatFloat(float64(n)/(1<<10), 'f', 1, 64) + " KB"
	default:
		return strconv.FormatInt(n, 10) + " B"
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
