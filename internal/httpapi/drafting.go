package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/allanpk716/agent-taskboard/internal/drafts"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

func (a *API) handleReplyDrafting(w http.ResponseWriter, r *http.Request, ref string, rest []string) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "读取起草过程用 GET", "")
		return
	}
	if len(rest) != 1 {
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口", "读取起草过程用 GET /api/v1/tickets/{id}/drafting/{reply_no}")
		return
	}
	replyNo, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil || replyNo < 1 {
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidCursor, "回复号必须是正整数", "")
		return
	}
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok {
		return
	}
	if viewer.Kind == identity.ViewerGuest {
		WriteError(w, http.StatusNotFound, CodeTicketNotFound, "没有这张工单", "")
		return
	}
	full, err := a.tickets.ForViewer(r.Context(), viewer, ref)
	if err != nil {
		a.writeTicketError(w, r, ref, err)
		return
	}
	versions, err := a.drafts.ReplyHistory(r.Context(), full.Seq, replyNo)
	if err != nil {
		if errors.Is(err, drafts.ErrNotFound) {
			WriteError(w, http.StatusNotFound, CodeNotFound, "没有这份起草过程", "只有从草稿发出的回复才有起草过程")
			return
		}
		panic(err)
	}
	views := make([]draftHistoryVersionView, 0, len(versions))
	for _, v := range versions {
		views = append(views, newDraftHistoryVersionView(v))
	}
	writeJSON(w, http.StatusOK, struct {
		Versions []draftHistoryVersionView `json:"versions"`
	}{Versions: views})
}

type draftHistoryVersionView struct {
	Version       int64    `json:"version"`
	BodyMD        string   `json:"body_md"`
	AttachmentIDs []string `json:"attachment_ids"`
	Author        string   `json:"author"`
	AuthorKind    string   `json:"author_kind"`
	At            string   `json:"at"`
}

func newDraftHistoryVersionView(v drafts.HistoryVersion) draftHistoryVersionView {
	author := v.MemberName
	kind := "member"
	if v.AgentID != 0 {
		author += " 的 Agent · " + v.AgentName
		kind = "agent"
	}
	attachmentIDs := v.Request.AttachmentIDs
	if attachmentIDs == nil {
		attachmentIDs = []string{}
	}
	return draftHistoryVersionView{
		Version: v.Version.Version, BodyMD: v.Request.BodyMD, AttachmentIDs: attachmentIDs,
		Author: author, AuthorKind: kind, At: v.At.Format("2006-01-02T15:04:05Z07:00"),
	}
}
