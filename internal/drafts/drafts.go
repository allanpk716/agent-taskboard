package drafts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

var ErrNotFound = errors.New("没有这份草稿")

type VersionConflictError struct {
	Current    int64
	Base       int64
	LastAuthor string
}

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
	BodyMD        string            `json:"body_md"`
	Title         *string           `json:"title,omitempty"`
	BriefMD       *string           `json:"brief_md,omitempty"`
	Fields        map[string]string `json:"fields,omitempty"`
	AttachmentIDs []string          `json:"attachment_ids"`
	Deliver       bool              `json:"deliver"`
	BaseRevision  int64             `json:"base_revision,omitempty"`
}

type CreateRequest struct {
	Kind      string                  `json:"kind"`
	Template  string                  `json:"template"`
	Title     string                  `json:"title"`
	BriefMD   string                  `json:"brief_md"`
	Fields    map[string]string       `json:"fields"`
	Labels    []string                `json:"labels"`
	Assignee  string                  `json:"assignee"`
	GuestLink *CreateGuestLinkRequest `json:"guest_link,omitempty"`
}

type CreateGuestLinkRequest struct {
	Note         string `json:"note"`
	ValidDays    int    `json:"valid_days"`
	ExpiresAt    string `json:"expires_at"`
	CanEditBrief bool   `json:"can_edit_brief"`
}

type Draft struct {
	ID                string
	Kind              string
	Status            string
	TicketSeq         int64
	TicketNumber      string
	TicketTitle       string
	TicketURL         string
	MemberID          int64
	MemberName        string
	AgentID           int64
	AgentName         string
	Request           Request
	CreateRequest     CreateRequest
	Version           int64
	BaseTimelineSeq   int64
	BaseBriefRevision int64
	NewEvents         int64
	MemberEdited      bool
	SentTimelineSeq   int64
	Versions          []Version
	Attachments       []attachments.AttachmentInfo
	Warnings          []tickets.FieldWarning
	SentNumber        string
}

type SendResult struct {
	Reply     tickets.ReplyResult
	Create    *tickets.CreateResult
	Signature string
	Drafting  []Version
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
	db     *sql.DB
	clk    clock.Clock
	tk     *tickets.Manager
	atts   *attachments.Manager
	notes  Notifier
	sendMu sync.Mutex
}

func NewManager(db *sql.DB, clk clock.Clock, tk *tickets.Manager, atts *attachments.Manager) *Manager {
	return &Manager{db: db, clk: clk, tk: tk, atts: atts}
}

func (m *Manager) SetNotifier(n Notifier) { m.notes = n }

type Notifier interface {
	NotifyDraftTx(ctx context.Context, tx *sql.Tx, memberID, ticketSeq int64, number, title, text string,
		baseTimelineSeq, agentID int64, agentName, url string) error
	Wake(memberIDs []int64)
	NotifyCreateDraftTx(ctx context.Context, tx *sql.Tx, memberID, agentID int64, memberName, agentName, title, url string) error
}

func (m *Manager) Pending(ctx context.Context, memberID, ticketSeq int64) (Draft, error) {
	var id int64
	err := m.db.QueryRowContext(ctx, `
		SELECT id FROM drafts WHERE ticket_seq = ? AND member_id = ? AND status = 'pending'`, ticketSeq, memberID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, err
	}
	return m.Get(ctx, identity.Viewer{Kind: identity.ViewerMember, Member: identity.Member{ID: memberID}}, id)
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
		text := "回复草稿"
		if req.Deliver {
			text = "交付草稿"
		}
		if err = m.notes.NotifyDraftTx(ctx, tx, viewer.Member.ID, full.Seq, full.Number, full.Title,
			"你的 Agent · "+viewer.Agent.Name+" 写了"+text+"待确认", d.BaseTimelineSeq,
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

func (m *Manager) SaveCreate(ctx context.Context, viewer identity.Viewer, req CreateRequest) (Draft, error) {
	if viewer.Agent == nil || viewer.Agent.DraftMode != "required" {
		panic("create drafts are saved by required agents")
	}
	input := createInputOf(req, viewer)
	validated, warnings, err := m.tk.ValidateCreate(ctx, input)
	if err != nil {
		return Draft{}, err
	}
	req.Kind, req.Title, req.BriefMD, req.Labels = validated.Kind, validated.Title, validated.BriefMD, validated.Labels
	now := m.clk.Now().Format(time.RFC3339Nano)
	buf, _ := json.Marshal(req)
	d := Draft{Kind: "create", Status: "pending", Request: Request{}, CreateRequest: req,
		MemberID: viewer.Member.ID, MemberName: viewer.Member.Name,
		AgentID: viewer.Agent.ID, AgentName: viewer.Agent.Name}
	d.Warnings = warnings
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Draft{}, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO drafts(kind, ticket_seq, member_id, request_json, version, base_timeline_seq, created_at, updated_at)
		VALUES ('create', NULL, ?, ?, 1, 0, ?, ?) RETURNING id`,
		viewer.Member.ID, string(buf), now, now).Scan(&id)
	if err != nil {
		return Draft{}, err
	}
	if err = insertVersion(ctx, tx, id, 1, req, viewer, now); err != nil {
		return Draft{}, err
	}
	if m.notes != nil {
		if err = m.notes.NotifyCreateDraftTx(ctx, tx, viewer.Member.ID, viewer.Agent.ID,
			viewer.Member.Name, viewer.Agent.Name, req.Title, "/drafts/dft_"+fmt.Sprint(id)); err != nil {
			return Draft{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Draft{}, err
	}
	if m.notes != nil {
		m.notes.Wake([]int64{viewer.Member.ID})
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

func insertVersion(ctx context.Context, tx *sql.Tx, draftID, version int64, req any, viewer identity.Viewer, at string) error {
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
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func lastAuthor(vs []Version) string {
	if len(vs) == 0 {
		return ""
	}
	v := vs[len(vs)-1]
	if v.AgentID != 0 {
		return v.MemberName + " 的 Agent · " + v.AgentName
	}
	return v.MemberName
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
	var ticketSeq sql.NullInt64
	var ticketNumber sql.NullString
	var ticketTitle sql.NullString
	err := m.db.QueryRowContext(ctx, `
		SELECT d.id, d.kind, d.status, d.ticket_seq, d.member_id, d.request_json, d.version,
		       d.base_timeline_seq, d.member_edited, d.created_at, d.updated_at, d.sent_at, d.sent_timeline_seq,
		       m.name, a.id, COALESCE(a.name, ''), t.number, t.title
		FROM drafts d
		JOIN members m ON m.id = d.member_id
		LEFT JOIN agent_tokens a ON a.id = (
			SELECT v.author_agent_id FROM draft_versions v
			WHERE v.draft_id = d.id ORDER BY v.version DESC LIMIT 1)
		LEFT JOIN tickets t ON t.seq = d.ticket_seq
		WHERE d.id = ?`, id).
		Scan(new(int64), &d.Kind, &d.Status, &ticketSeq, &d.MemberID, &request, &d.Version,
			&d.BaseTimelineSeq, &memberEdited, &createdAt, &updatedAt, &sentAt, &sentSeq,
			&d.MemberName, &agentID, &d.AgentName, &ticketNumber, &ticketTitle)
	if errors.Is(err, sql.ErrNoRows) || d.MemberID != viewer.Member.ID {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, err
	}
	d.ID = "dft_" + fmt.Sprint(id)
	d.TicketSeq, d.TicketNumber = ticketSeq.Int64, ticketNumber.String
	d.TicketTitle = ticketTitle.String
	if d.TicketNumber != "" {
		d.TicketURL = "/t/" + d.TicketNumber
	}
	d.AgentID = agentID.Int64
	d.SentTimelineSeq = sentSeq.Int64
	d.MemberEdited = memberEdited == 1
	if d.Kind == "create" {
		_ = json.Unmarshal([]byte(request), &d.CreateRequest)
		if _, warnings, err := m.tk.ValidateCreate(ctx, createInputOf(d.CreateRequest, viewer)); err == nil {
			d.Warnings = warnings
		}
	} else {
		_ = json.Unmarshal([]byte(request), &d.Request)
		d.BaseBriefRevision = d.Request.BaseRevision
	}
	d.Versions, err = m.versions(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	if d.TicketNumber != "" {
		full, err := m.tk.ByRef(ctx, d.TicketNumber)
		if err != nil {
			return Draft{}, err
		}
		for _, e := range full.Timeline {
			if e.Seq > d.BaseTimelineSeq {
				d.NewEvents++
			}
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

func (m *Manager) UpdateCreate(ctx context.Context, viewer identity.Viewer, id string, base int64, req CreateRequest) (Draft, error) {
	d, err := m.ByViewer(ctx, viewer, id)
	if err != nil {
		return Draft{}, err
	}
	if d.Status == "sent" {
		return Draft{}, &AlreadySentError{}
	}
	if base != d.Version {
		return Draft{}, &VersionConflictError{Current: d.Version, Base: base, LastAuthor: lastAuthor(d.Versions)}
	}
	validated, warnings, err := m.tk.ValidateCreate(ctx, createInputOf(req, viewer))
	if err != nil {
		return Draft{}, err
	}
	req.Kind, req.Title, req.BriefMD, req.Labels = validated.Kind, validated.Title, validated.BriefMD, validated.Labels

	now := m.clk.Now().Format(time.RFC3339Nano)
	buf, _ := json.Marshal(req)
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Draft{}, err
	}
	defer tx.Rollback()
	num, _ := parseID(id)
	res, err := tx.ExecContext(ctx, `
		UPDATE drafts SET request_json = ?, version = version + 1, member_edited = member_edited | ?, updated_at = ?
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
	if err = tx.Commit(); err != nil {
		return Draft{}, err
	}
	out, err := m.Get(ctx, viewer, num)
	if err != nil {
		return Draft{}, err
	}
	out.Warnings = warnings
	return out, nil
}

func createInputOf(req CreateRequest, viewer identity.Viewer) tickets.CreateInput {
	in := tickets.CreateInput{
		Kind: req.Kind, Title: req.Title, BriefMD: req.BriefMD, Labels: req.Labels,
		TemplateKey: req.Template, Fields: req.Fields, Assignee: req.Assignee, Creator: viewer,
	}
	if req.GuestLink != nil {
		g := req.GuestLink
		spec := tickets.GuestLinkSpec{Note: g.Note, ValidDays: g.ValidDays, CanEditBrief: g.CanEditBrief}
		if g.ExpiresAt != "" {
			t, err := time.ParseInLocation("2006-01-02", g.ExpiresAt, clock.Beijing)
			if err == nil {
				spec.ExpiresAt = &t
			}
		}
		in.GuestLink = &spec
	}
	return in
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
		return Draft{}, &VersionConflictError{Current: d.Version, Base: base, LastAuthor: lastAuthor(d.Versions)}
	}
	full, err := m.tk.ForViewer(ctx, viewer, d.TicketNumber)
	if err != nil {
		return Draft{}, err
	}
	if full.Status != "open" {
		return Draft{}, tickets.ErrTicketClosed
	}
	if d.Kind == "brief_revision" {
		if _, err = m.tk.ValidateRevise(ctx, viewer, d.TicketNumber, tickets.ReviseInput{
			Title: req.Title, BriefMD: req.BriefMD, Fields: req.Fields,
			AttachmentIDs: req.AttachmentIDs, BaseRevision: req.BaseRevision,
		}); err != nil {
			return Draft{}, err
		}
	} else if err = m.tk.ValidateReply(ctx, viewer, d.TicketNumber, tickets.ReplyInput{
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
		UPDATE drafts SET request_json = ?, version = version + 1, member_edited = member_edited | ?, updated_at = ?
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
	if d.Kind == "brief_revision" {
		if err = m.reserveBriefDraft(ctx, tx, num, full, viewer, req); err != nil {
			return Draft{}, err
		}
	} else if err = m.atts.ReserveDraftTx(ctx, tx, num, full.Seq, attachments.PrincipalOf(viewer),
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

func (m *Manager) Send(ctx context.Context, viewer identity.Viewer, id string, version, throughSeq int64, deliver *bool) (SendResult, error) {
	m.sendMu.Lock()
	defer m.sendMu.Unlock()
	d, err := m.ByViewer(ctx, viewer, id)
	if err != nil {
		return SendResult{}, err
	}
	if version != d.Version {
		return SendResult{}, &VersionConflictError{Current: d.Version, Base: version}
	}
	if d.Status == "sent" {
		return SendResult{}, &AlreadySentError{}
	}
	if d.Kind == "create" {
		return m.sendCreate(ctx, viewer, d)
	}
	full, err := m.tk.ForViewer(ctx, viewer, d.TicketNumber)
	if err != nil {
		return SendResult{}, err
	}
	if full.Status != "open" {
		return SendResult{}, tickets.ErrTicketClosed
	}
	last := lastSeq(full)
	if d.NewEvents > 0 && throughSeq < last {
		return SendResult{}, &TicketChangedError{NewEvents: d.NewEvents, ThroughSeq: last}
	}
	req := d.Request
	memberEdited := d.MemberEdited
	if deliver != nil {
		req.Deliver = *deliver
		memberEdited = memberEdited || *deliver != d.Request.Deliver
	}
	if err = m.tk.ValidateReply(ctx, viewer, d.TicketNumber, tickets.ReplyInput{
		BodyMD: req.BodyMD, AttachmentIDs: req.AttachmentIDs, Deliver: req.Deliver,
	}); err != nil {
		return SendResult{}, err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return SendResult{}, err
	}
	defer tx.Rollback()
	num, _ := parseID(id)
	claimed, err := tx.ExecContext(ctx, `
		UPDATE drafts SET status = 'sent', member_edited = ?, sent_at = ?
		WHERE id = ? AND version = ? AND status = 'pending'`,
		boolInt(memberEdited), m.clk.Now().Format(time.RFC3339Nano), num, d.Version)
	if err != nil {
		return SendResult{}, err
	}
	if n, _ := claimed.RowsAffected(); n == 0 {
		return SendResult{}, &AlreadySentError{}
	}
	actor := tickets.Author{MemberID: d.MemberID, MemberName: d.MemberName}
	for i := len(d.Versions) - 1; i >= 0; i-- {
		if d.Versions[i].AgentID != 0 {
			actor.AgentID, actor.AgentName = d.Versions[i].AgentID, d.Versions[i].AgentName
			break
		}
	}
	result, notified, err := m.tk.ReplyTx(ctx, tx, full, viewer, tickets.ReplyInput{
		BodyMD: req.BodyMD, AttachmentIDs: req.AttachmentIDs, Deliver: req.Deliver, Actor: &actor,
	})
	if err != nil {
		return SendResult{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE drafts SET sent_timeline_seq = ? WHERE id = ?`,
		result.TimelineSeq, num)
	if err != nil {
		return SendResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return SendResult{}, err
	}
	m.tk.Wake(notified)
	if after, lookupErr := m.tk.ByRef(ctx, d.TicketNumber); lookupErr == nil {
		if turn, waitErr := m.tk.WaitingOn(ctx, after); waitErr == nil {
			result.WaitingOn = turn
		}
	}
	signature := signatureOf(d.MemberName, memberEdited)
	return SendResult{Reply: result, Signature: signature, Drafting: d.Versions}, nil
}

func (m *Manager) sendCreate(ctx context.Context, viewer identity.Viewer, d Draft) (SendResult, error) {
	memberEdited := d.MemberEdited
	actor := tickets.Author{MemberID: d.MemberID, MemberName: d.MemberName}
	for i := len(d.Versions) - 1; i >= 0; i-- {
		if d.Versions[i].AgentID != 0 {
			actor.AgentID, actor.AgentName = d.Versions[i].AgentID, d.Versions[i].AgentName
			break
		}
	}
	in := createInputOf(d.CreateRequest, viewer)
	in.Actor = &actor
	prepared, err := m.tk.PrepareCreate(ctx, in)
	if err != nil {
		return SendResult{}, err
	}
	prepared.Input.Actor = &actor
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return SendResult{}, err
	}
	defer tx.Rollback()
	num, _ := parseID(d.ID)
	created, notified, err := m.tk.CreateTx(ctx, tx, prepared)
	if err != nil {
		return SendResult{}, err
	}
	claimed, err := tx.ExecContext(ctx, `
		UPDATE drafts SET status = 'sent', member_edited = ?, sent_at = ?
		WHERE id = ? AND version = ? AND status = 'pending'`,
		boolInt(memberEdited), m.clk.Now().Format(time.RFC3339Nano), num, d.Version)
	if err != nil {
		return SendResult{}, err
	}
	if n, _ := claimed.RowsAffected(); n == 0 {
		return SendResult{}, &AlreadySentError{}
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE drafts SET ticket_seq = ?, sent_timeline_seq = 1 WHERE id = ?`,
		created.Full.Seq, num)
	if err != nil {
		return SendResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return SendResult{}, err
	}
	m.tk.Wake(notified)
	if created.Full, err = m.tk.ByRef(ctx, created.Full.Number); err != nil {
		return SendResult{}, err
	}
	signature := signatureOf(d.MemberName, memberEdited)
	return SendResult{Create: &created, Signature: signature, Drafting: d.Versions}, nil
}

func (m *Manager) SendBriefRevision(ctx context.Context, viewer identity.Viewer, id string, version, throughSeq int64) (t tickets.Ticket, signature string, drafting []Version, err error) {
	m.sendMu.Lock()
	defer m.sendMu.Unlock()
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
	if full.BriefRevision != d.BaseBriefRevision {
		err = &tickets.RevisionConflictError{BaseRevision: d.BaseBriefRevision, CurrentRevision: full.BriefRevision}
		return
	}
	if _, err = m.tk.ValidateRevise(ctx, viewer, d.TicketNumber, tickets.ReviseInput{
		Title: d.Request.Title, BriefMD: d.Request.BriefMD, Fields: d.Request.Fields,
		AttachmentIDs: d.Request.AttachmentIDs, BaseRevision: d.Request.BaseRevision,
	}); err != nil {
		return
	}
	last := lastSeq(full)
	if d.NewEvents > 0 && throughSeq < last {
		err = &TicketChangedError{NewEvents: d.NewEvents, ThroughSeq: last}
		return
	}
	actor := tickets.Author{MemberID: d.MemberID, MemberName: d.MemberName}
	for i := len(d.Versions) - 1; i >= 0; i-- {
		if d.Versions[i].AgentID != 0 {
			actor.AgentID, actor.AgentName = d.Versions[i].AgentID, d.Versions[i].AgentName
			break
		}
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	num, _ := parseID(id)
	claimed, err := tx.ExecContext(ctx, `
		UPDATE drafts SET status = 'sent', sent_at = ?
		WHERE id = ? AND version = ? AND status = 'pending'`,
		m.clk.Now().Format(time.RFC3339Nano), num, version)
	if err != nil {
		return
	}
	if n, _ := claimed.RowsAffected(); n == 0 {
		err = &AlreadySentError{}
		return
	}
	var notified []int64
	t, notified, err = m.tk.ReviseTx(ctx, tx, full, viewer, tickets.ReviseInput{
		Title: d.Request.Title, BriefMD: d.Request.BriefMD, Fields: d.Request.Fields,
		AttachmentIDs: d.Request.AttachmentIDs, BaseRevision: d.Request.BaseRevision, Actor: &actor,
	})
	if err != nil {
		return
	}
	if err = tx.Commit(); err != nil {
		return
	}
	m.tk.Wake(notified)
	after, err := m.tk.ByRef(ctx, d.TicketNumber)
	if err != nil {
		return
	}
	sentSeq := int64(0)
	for _, event := range after.Timeline {
		if event.Type == "brief_revised" && event.Revision == t.BriefRevision {
			sentSeq = event.Seq
		}
	}
	_, err = m.db.ExecContext(ctx, `UPDATE drafts SET sent_timeline_seq = ? WHERE id = ?`, sentSeq, num)
	if err != nil {
		return
	}
	signature = signatureOf(d.MemberName, d.MemberEdited)
	drafting = d.Versions
	return
}

func signatureOf(name string, memberEdited bool) string {
	if memberEdited {
		return "经" + name + "修改并确认"
	}
	return "经" + name + "确认"
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
