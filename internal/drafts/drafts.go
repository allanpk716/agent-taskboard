package drafts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

var ErrNotFound = errors.New("没有这份草稿")

type VersionConflictError struct{ Current, Base int64 }

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("草稿已被改到第 %d 版，你基于的是第 %d 版", e.Current, e.Base)
}

type AlreadySentError struct{}

func (*AlreadySentError) Error() string { return "草稿已发出" }

type TicketChangedError struct{ NewEvents, ThroughSeq int64 }

func (e *TicketChangedError) Error() string {
	return fmt.Sprintf("草稿写成后工单有 %d 条新变化", e.NewEvents)
}

type Request struct {
	BodyMD        string   `json:"body_md"`
	AttachmentIDs []string `json:"attachment_ids"`
	Deliver       bool     `json:"deliver"`
}

type Draft struct {
	ID              string
	Kind            string
	Status          string
	TicketSeq       int64
	TicketNumber    string
	TicketTitle     string
	TicketURL       string
	MemberID        int64
	MemberName      string
	AgentID         int64
	AgentName       string
	Request         Request
	Version         int64
	BaseTimelineSeq int64
	NewEvents       int64
	MemberEdited    bool
	SentTimelineSeq int64
	Versions        []Version
	Attachments     []attachments.AttachmentInfo
}

type Version struct {
	Version    int64
	MemberID   int64
	MemberName string
	AgentID    int64
	AgentName  string
	At         time.Time
}

type Manager struct {
	db    *sql.DB
	clk   clock.Clock
	tk    *tickets.Manager
	atts  *attachments.Manager
	notes Notifier
}

func NewManager(db *sql.DB, clk clock.Clock, tk *tickets.Manager, atts *attachments.Manager) *Manager {
	return &Manager{db: db, clk: clk, tk: tk, atts: atts}
}

func (m *Manager) SetNotifier(n Notifier) { m.notes = n }

type Notifier interface {
	NotifyDraftTx(ctx context.Context, tx *sql.Tx, memberID, ticketSeq int64, number, title, text string,
		baseTimelineSeq, agentID int64, agentName, url string) error
	Wake(memberIDs []int64)
}

func (m *Manager) Pending(ctx context.Context, memberID, ticketSeq int64) (Draft, error) {
	var d Draft
	var id int64
	var request string
	err := m.db.QueryRowContext(ctx, `
		SELECT d.id, d.version, d.request_json, d.kind, t.number FROM drafts d
		JOIN tickets t ON t.seq = d.ticket_seq
		WHERE d.ticket_seq = ? AND d.member_id = ? AND d.status = 'pending'`, ticketSeq, memberID).
		Scan(&id, &d.Version, &request, &d.Kind, &d.TicketNumber)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, err
	}
	d.ID = "dft_" + fmt.Sprint(id)
	d.TicketURL = "/t/" + d.TicketNumber
	_ = json.Unmarshal([]byte(request), &d.Request)
	if d.Attachments, err = m.atts.ByDraft(ctx, id); err != nil {
		return Draft{}, err
	}
	return d, nil
}

func (m *Manager) SaveReply(ctx context.Context, viewer identity.Viewer, number string, req Request) (Draft, error) {
	full, err := m.tk.ForViewer(ctx, viewer, number)
	if err != nil {
		return Draft{}, err
	}
	if viewer.Agent != nil && viewer.Agent.DraftMode != "required" {
		panic("draft mode required")
	}
	if full.Status != "open" {
		return Draft{}, tickets.ErrTicketClosed
	}
	if err = m.tk.ValidateReply(ctx, viewer, number, tickets.ReplyInput{
		BodyMD: req.BodyMD, AttachmentIDs: req.AttachmentIDs, Deliver: req.Deliver,
	}); err != nil {
		return Draft{}, err
	}

	now := m.clk.Now().Format(time.RFC3339Nano)
	buf, _ := json.Marshal(req)
	d := Draft{Kind: "reply", Status: "pending", TicketSeq: full.Seq, TicketNumber: full.Number, TicketURL: "/t/" + full.Number,
		MemberID: viewer.Member.ID, MemberName: viewer.Member.Name, Request: req, BaseTimelineSeq: lastSeq(full)}
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
		VALUES ('reply', ?, ?, ?, 1, ?, ?, ?) RETURNING id`,
		full.Seq, viewer.Member.ID, string(buf), d.BaseTimelineSeq, now, now).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Draft{}, &VersionConflictError{Current: 1, Base: 1}
		}
		return Draft{}, err
	}
	if err = insertVersion(ctx, tx, id, 1, req, viewer, now); err != nil {
		return Draft{}, err
	}
	if m.notes != nil {
		if err = m.notes.NotifyDraftTx(ctx, tx, viewer.Member.ID, full.Seq, full.Number, full.Title,
			"你的 Agent · "+viewer.Agent.Name+" 写了回复草稿待确认", d.BaseTimelineSeq,
			viewer.Agent.ID, viewer.Agent.Name, "/t/"+full.Number+"#draft"); err != nil {
			return Draft{}, err
		}
	}
	if err = m.atts.ReserveDraftTx(ctx, tx, id, full.Seq, attachments.PrincipalOf(viewer),
		attachments.ParsePostRefs(req.BodyMD, req.AttachmentIDs)); err != nil {
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

func versionOf(version int64, viewer identity.Viewer, at time.Time) Version {
	v := Version{Version: version, MemberID: viewer.Member.ID, MemberName: viewer.Member.Name, At: at}
	if viewer.Agent != nil {
		v.AgentID, v.AgentName = viewer.Agent.ID, viewer.Agent.Name
	}
	return v
}

func insertVersion(ctx context.Context, tx *sql.Tx, draftID, version int64, req Request, viewer identity.Viewer, at string) error {
	buf, _ := json.Marshal(req)
	var agentID any
	if viewer.Agent != nil {
		agentID = viewer.Agent.ID
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO draft_versions(draft_id, version, request_json, author_member_id, author_agent_id, at)
		VALUES (?, ?, ?, ?, ?, ?)`, draftID, version, string(buf), viewer.Member.ID, agentID, at)
	return err
}

func (m *Manager) List(ctx context.Context, viewer identity.Viewer) ([]Draft, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT d.id FROM drafts d WHERE d.member_id = ? AND d.status = 'pending' ORDER BY d.id DESC`, viewer.Member.ID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, scanErr
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := []Draft{}
	for _, id := range ids {
		d, err := m.Get(ctx, viewer, id)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func (m *Manager) Signatures(ctx context.Context, ticketSeq int64) (map[int64]string, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT sent_timeline_seq, m.name, member_edited FROM drafts d
		JOIN members m ON m.id = d.member_id
		WHERE ticket_seq = ? AND status = 'sent' AND sent_timeline_seq IS NOT NULL`, ticketSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var seq int64
		var name string
		var edited int
		if err = rows.Scan(&seq, &name, &edited); err != nil {
			return nil, err
		}
		if edited == 1 {
			out[seq] = "经" + name + "修改并确认"
		} else {
			out[seq] = "经" + name + "确认"
		}
	}
	return out, rows.Err()
}

func (m *Manager) ByViewer(ctx context.Context, viewer identity.Viewer, id string) (Draft, error) {
	n, err := parseID(id)
	if err != nil {
		return Draft{}, ErrNotFound
	}
	return m.Get(ctx, viewer, n)
}

func parseID(id string) (int64, error) {
	n, err := fmt.Sscanf(strings.TrimPrefix(id, "dft_"), "%d", new(int64))
	if err != nil || n != 1 {
		return 0, ErrNotFound
	}
	var out int64
	_, err = fmt.Sscanf(strings.TrimPrefix(id, "dft_"), "%d", &out)
	return out, err
}

func (m *Manager) Get(ctx context.Context, viewer identity.Viewer, id int64) (Draft, error) {
	var d Draft
	var request, createdAt, updatedAt string
	var memberEdited int
	var agentID sql.NullInt64
	var sentAt sql.NullString
	var sentSeq sql.NullInt64
	err := m.db.QueryRowContext(ctx, `
		SELECT d.id, d.kind, d.status, d.ticket_seq, d.member_id, d.request_json, d.version,
		       d.base_timeline_seq, d.member_edited, d.created_at, d.updated_at, d.sent_at, d.sent_timeline_seq,
		       m.name, a.id, COALESCE(a.name, ''), t.number, t.title
		FROM drafts d
		JOIN members m ON m.id = d.member_id
		LEFT JOIN agent_tokens a ON a.id = (
			SELECT v.author_agent_id FROM draft_versions v
			WHERE v.draft_id = d.id ORDER BY v.version DESC LIMIT 1)
		JOIN tickets t ON t.seq = d.ticket_seq
		WHERE d.id = ?`, id).
		Scan(new(int64), &d.Kind, &d.Status, &d.TicketSeq, &d.MemberID, &request, &d.Version,
			&d.BaseTimelineSeq, &memberEdited, &createdAt, &updatedAt, &sentAt, &sentSeq,
			&d.MemberName, &agentID, &d.AgentName, &d.TicketNumber, &d.TicketTitle)
	if errors.Is(err, sql.ErrNoRows) || d.MemberID != viewer.Member.ID {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, err
	}
	d.ID = "dft_" + fmt.Sprint(id)
	d.TicketURL = "/t/" + d.TicketNumber
	d.AgentID = agentID.Int64
	d.SentTimelineSeq = sentSeq.Int64
	d.MemberEdited = memberEdited == 1
	_ = json.Unmarshal([]byte(request), &d.Request)
	d.Versions, err = m.versions(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	full, err := m.tk.ByRef(ctx, d.TicketNumber)
	if err != nil {
		return Draft{}, err
	}
	for _, e := range full.Timeline {
		if e.Seq > d.BaseTimelineSeq {
			d.NewEvents++
		}
	}
	if d.Attachments, err = m.atts.ByDraft(ctx, id); err != nil {
		return Draft{}, err
	}
	return d, nil
}

func (m *Manager) versions(ctx context.Context, id int64) ([]Version, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT v.version, v.author_member_id, m.name,
		       COALESCE(v.author_agent_id, 0), COALESCE(a.name, ''), v.at
		FROM draft_versions v
		JOIN members m ON m.id = v.author_member_id
		LEFT JOIN agent_tokens a ON a.id = v.author_agent_id
		WHERE v.draft_id = ? ORDER BY v.version`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		var v Version
		var at string
		if err = rows.Scan(&v.Version, &v.MemberID, &v.MemberName, &v.AgentID, &v.AgentName, &at); err != nil {
			return nil, err
		}
		if v.At, err = time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (m *Manager) Update(ctx context.Context, viewer identity.Viewer, id string, base int64, req Request) (Draft, error) {
	d, err := m.ByViewer(ctx, viewer, id)
	if err != nil {
		return Draft{}, err
	}
	if d.Status == "sent" {
		return Draft{}, &AlreadySentError{}
	}
	if base != d.Version {
		return Draft{}, &VersionConflictError{Current: d.Version, Base: base}
	}
	full, err := m.tk.ForViewer(ctx, viewer, d.TicketNumber)
	if err != nil {
		return Draft{}, err
	}
	if full.Status != "open" {
		return Draft{}, tickets.ErrTicketClosed
	}
	if err = m.tk.ValidateReply(ctx, viewer, d.TicketNumber, tickets.ReplyInput{
		BodyMD: req.BodyMD, AttachmentIDs: req.AttachmentIDs, Deliver: req.Deliver,
	}); err != nil {
		return Draft{}, err
	}
	now := m.clk.Now().Format(time.RFC3339Nano)
	buf, _ := json.Marshal(req)
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Draft{}, err
	}
	defer tx.Rollback()
	num, _ := parseID(id)
	res, err := tx.ExecContext(ctx, `
		UPDATE drafts SET request_json = ?, version = version + 1, member_edited = ?, updated_at = ?
		WHERE id = ? AND version = ? AND status = 'pending'`,
		string(buf), boolInt(viewer.Agent == nil), now, num, base)
	if err != nil {
		return Draft{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Draft{}, &VersionConflictError{Current: d.Version + 1, Base: base}
	}
	if err = insertVersion(ctx, tx, num, d.Version+1, req, viewer, now); err != nil {
		return Draft{}, err
	}
	if err = m.atts.ReleaseDraftTx(ctx, tx, num); err != nil {
		return Draft{}, err
	}
	if err = m.atts.ReserveDraftTx(ctx, tx, num, full.Seq, attachments.PrincipalOf(viewer),
		attachments.ParsePostRefs(req.BodyMD, req.AttachmentIDs)); err != nil {
		return Draft{}, err
	}
	if err = tx.Commit(); err != nil {
		return Draft{}, err
	}
	return m.Get(ctx, viewer, num)
}

func (m *Manager) Delete(ctx context.Context, viewer identity.Viewer, id string) error {
	num, err := parseID(id)
	if err != nil {
		return ErrNotFound
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	fileIDs, err := m.atts.DeleteDraftTx(ctx, tx, num)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM drafts WHERE id = ? AND member_id = ? AND status = 'pending'`, num, viewer.Member.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	m.atts.RemoveFiles(fileIDs)
	return nil
}

func (m *Manager) Send(ctx context.Context, viewer identity.Viewer, id string, version, throughSeq int64, deliver *bool) (result tickets.ReplyResult, signature string, drafting []Version, err error) {
	d, err := m.ByViewer(ctx, viewer, id)
	if err != nil {
		return
	}
	if version != d.Version {
		err = &VersionConflictError{Current: d.Version, Base: version}
		return
	}
	if d.Status == "sent" {
		err = &AlreadySentError{}
		return
	}
	full, err := m.tk.ForViewer(ctx, viewer, d.TicketNumber)
	if err != nil {
		return
	}
	if full.Status != "open" {
		err = tickets.ErrTicketClosed
		return
	}
	last := lastSeq(full)
	if d.NewEvents > 0 && throughSeq < last {
		err = &TicketChangedError{NewEvents: d.NewEvents, ThroughSeq: last}
		return
	}
	req := d.Request
	if deliver != nil {
		req.Deliver = *deliver
	}
	actor := tickets.Author{MemberID: d.MemberID, MemberName: d.MemberName}
	for i := len(d.Versions) - 1; i >= 0; i-- {
		if d.Versions[i].AgentID != 0 {
			actor.AgentID, actor.AgentName = d.Versions[i].AgentID, d.Versions[i].AgentName
			break
		}
	}
	result, err = m.tk.Reply(ctx, viewer, d.TicketNumber, tickets.ReplyInput{
		BodyMD: req.BodyMD, AttachmentIDs: req.AttachmentIDs, Deliver: req.Deliver, Actor: &actor,
	})
	if err != nil {
		return
	}
	num, _ := parseID(id)
	_, err = m.db.ExecContext(ctx, `
		UPDATE drafts SET status = 'sent', sent_at = ?, sent_timeline_seq = ? WHERE id = ? AND version = ? AND status = 'pending'`,
		m.clk.Now().Format(time.RFC3339Nano), result.TimelineSeq, num, d.Version)
	if err != nil {
		return
	}
	signature = "经" + d.MemberName + "确认"
	if d.MemberEdited {
		signature = "经" + d.MemberName + "修改并确认"
	}
	drafting = d.Versions
	return
}

func lastSeq(full tickets.Full) int64 {
	if len(full.Timeline) == 0 {
		return 0
	}
	return full.Timeline[len(full.Timeline)-1].Seq
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
