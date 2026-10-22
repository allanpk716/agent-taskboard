package drafts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

func (m *Manager) SaveBriefRevision(ctx context.Context, viewer identity.Viewer, number string, req Request) (Draft, error) {
	if viewer.Agent == nil || viewer.Agent.DraftMode != "required" {
		panic("brief revision draft requires a required agent")
	}
	full, err := m.tk.ValidateRevise(ctx, viewer, number, tickets.ReviseInput{
		Title: req.Title, BriefMD: req.BriefMD, Fields: req.Fields,
		AttachmentIDs: req.AttachmentIDs, BaseRevision: req.BaseRevision,
	})
	if err != nil {
		return Draft{}, err
	}
	now := m.clk.Now().Format(time.RFC3339Nano)
	buf, _ := json.Marshal(req)
	d := Draft{Kind: "brief_revision", Status: "pending", TicketSeq: full.Seq,
		TicketNumber: full.Number, TicketURL: "/t/" + full.Number,
		MemberID: viewer.Member.ID, MemberName: viewer.Member.Name, Request: req,
		BaseTimelineSeq: lastSeq(full), BaseBriefRevision: full.BriefRevision}
	if viewer.Agent != nil {
		d.AgentID, d.AgentName = viewer.Agent.ID, viewer.Agent.Name
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Draft{}, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO drafts(kind, ticket_seq, member_id, request_json, version, base_timeline_seq, created_at, updated_at)
		VALUES ('brief_revision', ?, ?, ?, 1, ?, ?, ?) RETURNING id`,
		full.Seq, viewer.Member.ID, string(buf), d.BaseTimelineSeq, now, now).Scan(&id)
	if err != nil {
		return Draft{}, err
	}
	if err = insertVersion(ctx, tx, id, 1, req, viewer, now); err != nil {
		return Draft{}, err
	}
	if m.notes != nil {
		if err = m.notes.NotifyDraftTx(ctx, tx, viewer.Member.ID, full.Seq, full.Number, full.Title,
			"你的 Agent · "+viewer.Agent.Name+" 写了说明修订草稿待确认", d.BaseTimelineSeq,
			viewer.Agent.ID, viewer.Agent.Name, "/t/"+full.Number+"#draft"); err != nil {
			return Draft{}, err
		}
	}
	if err = m.reserveBriefDraft(ctx, tx, id, full, viewer, req); err != nil {
		return Draft{}, err
	}
	if err = tx.Commit(); err != nil {
		return Draft{}, err
	}
	if m.notes != nil {
		m.notes.Wake([]int64{viewer.Member.ID})
	}
	d.Attachments, err = m.atts.ByDraft(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	d.ID = "dft_" + fmt.Sprint(id)
	d.Version = 1
	d.Versions = []Version{versionOf(1, viewer, m.clk.Now())}
	return d, nil
}

func (m *Manager) reserveBriefDraft(ctx context.Context, tx *sql.Tx, draftID int64, full tickets.Full,
	viewer identity.Viewer, req Request) error {
	if err := m.atts.ValidateBriefIDsTx(ctx, tx, full.Seq, attachments.PrincipalOf(viewer), req.AttachmentIDs); err != nil {
		return err
	}
	brief := full.BriefMD
	if req.BriefMD != nil {
		brief = *req.BriefMD
	}
	refs := attachments.ParsePostRefs(brief, req.AttachmentIDs)
	pending := make([]attachments.PostRef, 0, len(refs))
	for _, ref := range refs {
		var state string
		var otherDraft any
		err := tx.QueryRowContext(ctx, `SELECT state, draft_id FROM attachments WHERE id = ?`, ref.ID).Scan(&state, &otherDraft)
		if err == nil && state == "pending" {
			pending = append(pending, ref)
		}
	}
	return m.atts.ReserveDraftTx(ctx, tx, draftID, full.Seq, attachments.PrincipalOf(viewer), pending)
}
