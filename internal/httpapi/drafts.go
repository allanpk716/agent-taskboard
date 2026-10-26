package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/drafts"
	"github.com/allanpk716/agent-taskboard/internal/template"
	"github.com/allanpk716/agent-taskboard/internal/templates"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

func (a *API) handleDrafts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	list, err := a.drafts.List(r.Context(), viewer)
	if err != nil {
		panic(err)
	}
	views := make([]draftView, 0, len(list))
	for _, d := range list {
		views = append(views, newDraftView(a, r, d))
	}
	writeJSON(w, http.StatusOK, struct {
		Count  int         `json:"count"`
		Drafts []draftView `json:"drafts"`
	}{Count: len(views), Drafts: views})
}

func (a *API) handleDraft(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/drafts/"), "/send")
	if strings.Contains(r.URL.Path, "/send") {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "发出草稿用 POST", "")
			return
		}
		a.sendDraft(w, r, strings.TrimPrefix(id, "/"))
		return
	}
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		d, err := a.drafts.ByViewer(r.Context(), viewer, id)
		if !a.writeDraftError(w, r, err) {
			return
		}
		writeJSON(w, http.StatusOK, newDraftView(a, r, d))
	case http.MethodPut:
		var req struct {
			BaseVersion *int64          `json:"base_version"`
			Request     json.RawMessage `json:"request"`
		}
		if !decodeJSON(w, r, &req, `{"base_version": 1, "request": {"body_md": "…"}}`) {
			return
		}
		if req.BaseVersion == nil {
			WriteError(w, http.StatusUnprocessableEntity, CodeBaseVersionRequired, "改草稿需要带上 base_version", "先 GET 草稿，取 version 作 base_version")
			return
		}
		existing, err := a.drafts.ByViewer(r.Context(), viewer, id)
		if !a.writeDraftError(w, r, err) {
			return
		}
		var d drafts.Draft
		if existing.Kind == drafts.KindCreate {
			var createReq drafts.CreateRequest
			if err := json.Unmarshal(req.Request, &createReq); err != nil {
				WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "请求体不是合法的 JSON", "")
				return
			}
			d, err = a.drafts.UpdateCreate(r.Context(), viewer, id, *req.BaseVersion, createReq)
		} else {
			var replyReq drafts.Request
			if err := json.Unmarshal(req.Request, &replyReq); err != nil {
				WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "请求体不是合法的 JSON", "")
				return
			}
			d, err = a.drafts.Update(r.Context(), viewer, id, *req.BaseVersion, replyReq)
		}
		if _, ok := a.writeDraftValidation(w, err); ok {
			return
		}
		if !a.writeDraftError(w, r, err) {
			return
		}
		writeJSON(w, http.StatusOK, newDraftView(a, r, d))
	case http.MethodDelete:
		if !viewer.IsWebSession() {
			WriteError(w, http.StatusForbidden, CodeWebSessionRequired, "丢弃草稿只限成员本人在网页上操作", "")
			return
		}
		err := a.drafts.Delete(r.Context(), viewer, id)
		if !a.writeDraftError(w, r, err) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
	}
}

func (a *API) sendDraft(w http.ResponseWriter, r *http.Request, id string) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	if !viewer.IsWebSession() {
		WriteError(w, http.StatusForbidden, CodeWebSessionRequired, "发出草稿只限成员本人在网页上操作", "")
		return
	}
	var req struct {
		Version           int64 `json:"version"`
		ConfirmThroughSeq int64 `json:"confirm_through_seq"`
		Deliver           *bool `json:"deliver"`
	}
	if !decodeJSON(w, r, &req, `{"version": 1}`) {
		return
	}
	if d, lookupErr := a.drafts.ByViewer(r.Context(), viewer, id); lookupErr == nil && d.Kind == drafts.KindBriefRevision {
		t, signature, drafting, err := a.drafts.SendBriefRevision(r.Context(), viewer, id, req.Version, req.ConfirmThroughSeq)
		if !a.writeDraftSendError(w, r, err) {
			return
		}
		writeJSON(w, http.StatusCreated, struct {
			Result    reviseTicketView   `json:"result"`
			Signature string             `json:"signature"`
			Drafting  []draftVersionView `json:"drafting"`
		}{Result: reviseTicketView{Revision: t.BriefRevision}, Signature: signature, Drafting: newDraftVersions(drafting)})
		return
	} else if lookupErr != nil && !errors.Is(lookupErr, drafts.ErrNotFound) {
		panic(lookupErr)
	}
	sent, err := a.drafts.Send(r.Context(), viewer, id, req.Version, req.ConfirmThroughSeq, req.Deliver)
	if !a.writeDraftSendError(w, r, err) {
		return
	}
	if sent.Create != nil {
		created := sent.Create
		full := created.Full
		waitingOn, err := a.tickets.WaitingOn(r.Context(), full)
		if err != nil {
			panic(err)
		}
		res := createTicketResponse{
			Number: full.Number, Seq: full.Seq, URL: "/t/" + full.Number,
			Warnings: created.Warnings, WaitingOn: newWaitingOnView(waitingOn),
		}
		if full.AssigneeID != 0 {
			res.Assignee = &memberRef{ID: full.AssigneeID, Name: full.AssigneeName}
			if full.AssigneeID != full.Creator.MemberID {
				share := newHandoffText(a.requestBaseURL(r)).share(false, full.AssigneeName, full.Number, full.Title)
				res.ShareText = &share
			}
		}
		if created.GuestLink != nil {
			link := created.GuestLink
			url := a.absURL("/g/" + link.Key)
			res.GuestLink = &createGuestLinkView{
				guestLinkBase: guestLinkBase{
					ID: link.ID, URL: url, Note: link.Note,
					ExpiresAt:    link.ExpiresAt.Format(time.RFC3339Nano),
					CanEditBrief: link.CanEditBrief,
				},
				ShareText: tickets.GuestShareText(link.Note, full.Title, url, link.ExpiresAt, false),
			}
		}
		writeJSON(w, http.StatusCreated, struct {
			Result    createTicketResponse `json:"result"`
			Signature string               `json:"signature"`
			Drafting  []draftVersionView   `json:"drafting"`
		}{Result: res, Signature: sent.Signature, Drafting: newDraftVersions(sent.Drafting)})
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Result    replyTicketView    `json:"result"`
		Signature string             `json:"signature"`
		Drafting  []draftVersionView `json:"drafting"`
	}{Result: replyTicketView{
		ReplyNo: sent.Reply.ReplyNo, TimelineSeq: sent.Reply.TimelineSeq, Deliver: sent.Reply.Deliver, Author: sent.Reply.AuthorName,
		WaitingOn: newWaitingOnView(sent.Reply.WaitingOn),
	}, Signature: sent.Signature, Drafting: newDraftVersions(sent.Drafting)})
}

func (a *API) writeDraftValidation(w http.ResponseWriter, err error) (error, bool) {
	var conflict *drafts.VersionConflictError
	var sent *drafts.AlreadySentError
	switch {
	case errors.As(err, &conflict):
		WriteErrorExtra(w, http.StatusConflict, CodeDraftVersionConflict, conflict.Error(), "重新读取草稿后基于最新版修改",
			map[string]any{"current_version": conflict.Current, "last_author": conflict.LastAuthor})
		return err, true
	case errors.As(err, &sent):
		WriteError(w, http.StatusConflict, CodeDraftAlreadySent, sent.Error(), "")
		return err, true
	}
	return err, false
}

func (a *API) writeDraftError(w http.ResponseWriter, r *http.Request, err error) bool {
	if _, ok := a.writeDraftValidation(w, err); ok {
		return false
	}
	if errors.Is(err, drafts.ErrNotFound) {
		WriteError(w, http.StatusNotFound, CodeDraftNotFound, "没有这份草稿", "它可能已被成员丢弃")
		return false
	}
	if err == nil {
		return true
	}
	a.writeTicketError(w, r, "", err)
	return false
}

func (a *API) writeDraftSendError(w http.ResponseWriter, r *http.Request, err error) bool {
	if _, ok := a.writeDraftValidation(w, err); ok {
		return false
	}
	if changed, ok := err.(*drafts.TicketChangedError); ok {
		WriteErrorExtra(w, http.StatusConflict, CodeTicketChanged, changed.Error(), "确认工单新变化后带 confirm_through_seq 重发",
			map[string]any{"new_events": changed.NewEvents, "through_seq": changed.ThroughSeq})
		return false
	}
	return a.writeDraftError(w, r, err)
}

type draftView struct {
	ID          string                 `json:"id"`
	Kind        drafts.Kind            `json:"kind"`
	Status      string                 `json:"status"`
	Version     int64                  `json:"version"`
	Ticket      *draftTicketView       `json:"ticket"`
	Request     any                    `json:"request"`
	Attachments []attachmentView       `json:"attachments"`
	Author      authorView             `json:"author"`
	Versions    []draftVersionView     `json:"versions"`
	Base        draftBaseView          `json:"base"`
	NewEvents   int64                  `json:"new_events"`
	PreviewURL  string                 `json:"preview_url"`
	URL         string                 `json:"url"`
	Result      any                    `json:"result,omitempty"`
	Warnings    []tickets.FieldWarning `json:"warnings,omitempty"`
	// 模板建单草稿的服务端计算字段（只读，PUT 时忽略）：rendered_brief_md
	// 与发出时生成的工单说明同一条路径算出；template_fields 是当前模板版本的
	// 字段定义，供网页逐字段编辑。模板已不存在时退化为按字段键值原样拼出，
	// template_warning 说明原因。
	RenderedBriefMD string                   `json:"rendered_brief_md,omitempty"`
	TemplateFields  []draftTemplateFieldView `json:"template_fields,omitempty"`
	TemplateWarning string                   `json:"template_warning,omitempty"`
}

func draftAttachmentViews(atts []attachments.AttachmentInfo) []attachmentView {
	out := make([]attachmentView, 0, len(atts))
	for _, att := range atts {
		out = append(out, attachmentView{
			ID: att.Ref(), Filename: att.Filename, Size: att.Size,
			URL: attachmentBasePath(att.Ref()) + url.PathEscape(att.Filename),
		})
	}
	return out
}

type draftTicketView struct {
	Number string `json:"number"`
	Seq    int64  `json:"seq"`
	Title  string `json:"title"`
	URL    string `json:"url"`
}

type draftBaseView struct {
	TimelineSeq   int64 `json:"timeline_seq"`
	BriefRevision int64 `json:"brief_revision,omitempty"`
}

// draftTemplateFieldView 是模板建单草稿视图里的字段定义精简版：网页按它逐字段
// 编辑 fields；维度/单选字段连同候选值一起下发。
type draftTemplateFieldView struct {
	Key       string   `json:"key"`
	Title     string   `json:"title"`
	Required  bool     `json:"required"`
	Type      string   `json:"type"` // text | radio | dimension
	Options   []string `json:"options,omitempty"`
	Dimension string   `json:"dimension,omitempty"`
}

type draftVersionView struct {
	Version int64  `json:"version"`
	Author  string `json:"author"`
	At      string `json:"at"`
}

func newDraftView(a *API, r *http.Request, d drafts.Draft) draftView {
	author := authorView{Member: &memberRef{ID: d.MemberID, Name: d.MemberName}}
	if d.AgentID != 0 {
		author.Agent = &agentRef{ID: d.AgentID, Name: d.AgentName}
	}
	request := any(d.Request)
	var ticket *draftTicketView
	previewURL := d.TicketURL + "#draft"
	view := draftView{}
	if d.Kind == drafts.KindCreate {
		request = d.CreateRequest
		previewURL = "/drafts/" + d.ID
		// 只给 pending 草稿算预览：已发出的说明存的是发出那一刻的快照，
		// 模板之后改版不应让“预览”变得与已发出的工单说明不一致。
		if d.CreateRequest.Template != "" && d.Status == "pending" {
			view.RenderedBriefMD, view.TemplateFields, view.TemplateWarning =
				a.renderCreateTemplateBrief(r.Context(), d.CreateRequest)
		}
	}
	if d.TicketNumber != "" {
		ticket = &draftTicketView{Number: d.TicketNumber, Seq: d.TicketSeq, Title: d.TicketTitle, URL: d.TicketURL}
	}
	var result any
	if d.Status == "pending" {
		result = nil
	} else if d.Kind == drafts.KindCreate {
		result = map[string]any{"number": d.TicketNumber}
	} else if full, err := a.tickets.ByRef(r.Context(), d.TicketNumber); err == nil {
		for _, event := range full.Timeline {
			if event.Seq != d.SentTimelineSeq {
				continue
			}
			if event.Type == "reply" {
				result = map[string]any{"reply_no": event.ReplyNo, "timeline_seq": event.Seq, "deliver": event.Deliver}
			} else if event.Type == "brief_revised" {
				result = map[string]any{"revision": event.Revision, "timeline_seq": event.Seq}
			}
			break
		}
	}
	view.ID, view.Kind, view.Status, view.Version = d.ID, d.Kind, d.Status, d.Version
	view.Ticket, view.Request, view.Attachments = ticket, request, draftAttachmentViews(d.Attachments)
	view.Author, view.Versions = author, newDraftVersions(d.Versions)
	view.Base, view.NewEvents = draftBaseView{TimelineSeq: d.BaseTimelineSeq, BriefRevision: d.BaseBriefRevision}, d.NewEvents
	view.PreviewURL, view.URL, view.Result, view.Warnings = previewURL, "/api/v1/drafts/"+d.ID, result, d.Warnings
	return view
}

// renderCreateTemplateBrief 用发出时同一条路径渲染模板建单草稿的说明预览：
// 取模板当前版本的字段定义（发出时也是现取当前版本），交给 template.RenderBrief。
// 模板不存在时不报错：按已填字段的键值原样拼出（键作标题、按键排序），并给出
// 一条 warning——发出本会失败，但预览不能因此 500。
func (a *API) renderCreateTemplateBrief(ctx context.Context, req drafts.CreateRequest) (string, []draftTemplateFieldView, string) {
	tmpl, err := a.templates.Get(ctx, req.Template)
	if errors.Is(err, templates.ErrNotFound) {
		keys := make([]string, 0, len(req.Fields))
		for k, v := range req.Fields {
			if strings.TrimSpace(v) == "" {
				continue
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, "### "+k+"\n\n"+strings.TrimSpace(req.Fields[k]))
		}
		warning := "模板 " + req.Template + " 已不存在，预览按已填字段原样拼出；这份草稿无法直接发出，请先重建模板或另建草稿。"
		return strings.Join(parts, "\n\n"), nil, warning
	}
	if err != nil {
		panic(err)
	}
	fields := make([]draftTemplateFieldView, 0, len(tmpl.Fields))
	for _, f := range tmpl.Fields {
		fv := draftTemplateFieldView{
			Key: f.Key, Title: f.Title, Required: f.Required, Type: f.Type,
			Options: nonNil(f.Options), Dimension: f.Dimension,
		}
		if f.Type == template.TypeDimension {
			fv.Options, _ = a.dimensionOptions(ctx, f.Dimension)
		}
		fields = append(fields, fv)
	}
	return template.RenderBrief(req.Fields, tmpl.Fields), fields, ""
}

func newDraftVersions(vs []drafts.Version) []draftVersionView {
	out := make([]draftVersionView, 0, len(vs))
	for _, v := range vs {
		name := v.MemberName
		if v.AgentID != 0 {
			name += " 的 Agent · " + v.AgentName
		}
		out = append(out, draftVersionView{Version: v.Version, Author: name, At: v.At.Format("2006-01-02T15:04:05Z07:00")})
	}
	return out
}
