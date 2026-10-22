package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/drafts"
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
			BaseVersion *int64         `json:"base_version"`
			Request     drafts.Request `json:"request"`
		}
		if !decodeJSON(w, r, &req, `{"base_version": 1, "request": {"body_md": "…"}}`) {
			return
		}
		if req.BaseVersion == nil {
			WriteError(w, http.StatusUnprocessableEntity, CodeBaseVersionRequired, "改草稿需要带上 base_version", "先 GET 草稿，取 version 作 base_version")
			return
		}
		d, err := a.drafts.Update(r.Context(), viewer, id, *req.BaseVersion, req.Request)
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
	result, signature, drafting, err := a.drafts.Send(r.Context(), viewer, id, req.Version, req.ConfirmThroughSeq, req.Deliver)
	if !a.writeDraftSendError(w, r, err) {
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Result    replyTicketView    `json:"result"`
		Signature string             `json:"signature"`
		Drafting  []draftVersionView `json:"drafting"`
	}{Result: replyTicketView{
		ReplyNo: result.ReplyNo, TimelineSeq: result.TimelineSeq, Deliver: result.Deliver, Author: result.AuthorName,
		WaitingOn: newWaitingOnView(result.WaitingOn),
	}, Signature: signature, Drafting: newDraftVersions(drafting)})
}

func (a *API) writeDraftValidation(w http.ResponseWriter, err error) (error, bool) {
	var conflict *drafts.VersionConflictError
	var sent *drafts.AlreadySentError
	switch {
	case errors.As(err, &conflict):
		WriteErrorExtra(w, http.StatusConflict, CodeDraftVersionConflict, conflict.Error(), "重新读取草稿后基于最新版修改",
			map[string]any{"current_version": conflict.Current})
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

type draftView struct {
	ID          string             `json:"id"`
	Kind        string             `json:"kind"`
	Status      string             `json:"status"`
	Version     int64              `json:"version"`
	Ticket      *draftTicketView   `json:"ticket"`
	Request     drafts.Request     `json:"request"`
	Attachments []attachmentView   `json:"attachments"`
	Author      authorView         `json:"author"`
	Versions    []draftVersionView `json:"versions"`
	Base        draftBaseView      `json:"base"`
	NewEvents   int64              `json:"new_events"`
	PreviewURL  string             `json:"preview_url"`
	URL         string             `json:"url"`
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
	TimelineSeq int64 `json:"timeline_seq"`
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
	return draftView{
		ID: d.ID, Kind: d.Kind, Status: d.Status, Version: d.Version,
		Ticket:  &draftTicketView{Number: d.TicketNumber, Seq: d.TicketSeq, Title: d.TicketTitle, URL: d.TicketURL},
		Request: d.Request, Attachments: draftAttachmentViews(d.Attachments),
		Author: author, Versions: newDraftVersions(d.Versions),
		Base: draftBaseView{TimelineSeq: d.BaseTimelineSeq}, NewEvents: d.NewEvents,
		PreviewURL: d.TicketURL + "#draft", URL: "/api/v1/drafts/" + d.ID,
	}
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
