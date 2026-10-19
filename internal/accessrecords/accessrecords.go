// Package accessrecords 记录工单详情查看、附件下载和被拒访问。
package accessrecords

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// Since 是本功能第一批上线日期；上线前的空面板显示“自该日期起记录”。
const Since = "2026-10-19"

const mergeWindow = time.Hour

// Actor 是一条访问记录的署名快照。
type Actor struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	IsAdmin     bool   `json:"is_admin,omitempty"`
	LinkNote    string `json:"link_note,omitempty"`
	EntryMethod string `json:"entry_method,omitempty"`
}

// Record 是访问记录的对外结构。
type Record struct {
	Actor      Actor       `json:"actor"`
	Action     string      `json:"action"`
	Attachment *Attachment `json:"attachment,omitempty"`
	FirstAt    time.Time   `json:"first_at"`
	LastAt     time.Time   `json:"last_at"`
	Count      int         `json:"count"`
}

// Attachment 是下载记录里的附件快照。
type Attachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
}

// Manager 是访问记录的存储入口。
type Manager struct {
	db  *sql.DB
	clk clock.Clock
}

// NewManager 返回访问记录管理器。
func NewManager(db *sql.DB, c clock.Clock) *Manager {
	return &Manager{db: db, clk: c}
}

func actorOf(v identity.Viewer) (kind, name string, memberID, agentID, linkID int64, note, entry string) {
	if v.Kind == identity.ViewerGuest {
		name := v.GuestLink.Note
		if v.GuestLink.ClaimName != "" {
			name += " · " + v.GuestLink.ClaimName
		}
		entry := v.GuestLink.EntryMethod
		if entry == "" {
			entry = "device"
		}
		return "guest", name, 0, 0, v.GuestLink.ID, v.GuestLink.Note, entry
	}
	kind = "member"
	name = v.Member.Name
	memberID = v.Member.ID
	if v.Agent != nil {
		kind = "agent"
		name = v.Member.Name + " 的 Agent · " + v.Agent.Name
		agentID = v.Agent.ID
	} else if v.Member.IsAdmin {
		kind = "admin"
	}
	return kind, name, memberID, agentID, linkID, note, entry
}

// RecordView 记录一次详情查看；同访问者一小时内合并到既有聚合行。
func (m *Manager) RecordView(ctx context.Context, ticketSeq int64, v identity.Viewer) error {
	now := m.clk.Now()
	kind, name, memberID, agentID, linkID, note, entry := actorOf(v)
	var (
		id        int64
		lastRaw   string
		lastAt    time.Time
		linkNull  sql.NullInt64
		noteNull  sql.NullString
		entryNull sql.NullString
	)
	err := m.db.QueryRowContext(ctx, `
		SELECT id, last_at, actor_link_id, actor_link_note, entry_method
		FROM access_records
		WHERE ticket_seq=? AND action='view' AND actor_kind=?
		  AND ((? != 0 AND actor_member_id = ?) OR (? = 0 AND actor_member_id IS NULL))
		  AND ((? != 0 AND actor_agent_id = ?) OR (? = 0 AND actor_agent_id IS NULL))
		  AND ((? != 0 AND actor_link_id = ?) OR (? = 0 AND actor_link_id IS NULL))
		ORDER BY id DESC LIMIT 1`,
		ticketSeq, kind,
		memberID, memberID, memberID,
		agentID, agentID, agentID,
		linkID, linkID, linkID).
		Scan(&id, &lastRaw, &linkNull, &noteNull, &entryNull)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = m.insert(ctx, ticketSeq, kind, name, memberID, agentID, linkID, note, entry, "view", nil, now, now, 1)
		return err
	}
	if err != nil {
		return err
	}
	lastAt, err = time.ParseInLocation(time.RFC3339Nano, lastRaw, clock.Beijing)
	if err != nil {
		return err
	}
	if now.Before(lastAt.Add(mergeWindow)) {
		_, err = m.db.ExecContext(ctx, `
			UPDATE access_records SET last_at=?, count=count+1 WHERE id=?`,
			now.Format(time.RFC3339Nano), id)
		return err
	}
	_, err = m.insert(ctx, ticketSeq, kind, name, memberID, agentID, linkID, note, entry, "view", nil, now, now, 1)
	return err
}

// RecordDownload 每次附件下载单独记一条。
func (m *Manager) RecordDownload(ctx context.Context, att attachments.AttachmentInfo, v identity.Viewer) error {
	now := m.clk.Now()
	kind, name, memberID, agentID, linkID, note, entry := actorOf(v)
	_, err := m.insert(ctx, att.TicketSeq, kind, name, memberID, agentID, linkID, note, entry,
		"download", &Attachment{ID: att.Ref(), Filename: att.Filename}, now, now, 1)
	return err
}

// RecordGuestLinkDownload 记录访客签名地址被实际取用。签名地址没有 Bearer
// 凭证，访问者身份来自地址上的访客链接 id。
func (m *Manager) RecordGuestLinkDownload(ctx context.Context, att attachments.AttachmentInfo, linkID int64) error {
	var note string
	if err := m.db.QueryRowContext(ctx, `SELECT note FROM guest_links WHERE id=?`, linkID).Scan(&note); err != nil {
		return err
	}
	now := m.clk.Now()
	_, err := m.insert(ctx, att.TicketSeq, "guest", note, 0, 0, linkID, note, "signed_url",
		"download", &Attachment{ID: att.Ref(), Filename: att.Filename}, now, now, 1)
	return err
}

// RecordGuestLinkDenied 记录访客链接因撤销或过期被拒的访问尝试。
func (m *Manager) RecordGuestLinkDenied(ctx context.Context, ticketSeq, linkID int64, reason string, att ...attachments.AttachmentInfo) error {
	var note string
	if err := m.db.QueryRowContext(ctx, `SELECT note FROM guest_links WHERE id=?`, linkID).Scan(&note); err != nil {
		return err
	}
	now := m.clk.Now()
	var attachment *Attachment
	if len(att) > 0 {
		attachment = &Attachment{ID: att[0].Ref(), Filename: att[0].Filename}
	}
	_, err := m.insert(ctx, ticketSeq, "guest", note, 0, 0, linkID, note, reason, "denied", attachment, now, now, 1)
	return err
}

func (m *Manager) insert(ctx context.Context, ticketSeq int64, kind, name string, memberID, agentID, linkID int64,
	note, entry, action string, att *Attachment, first, last time.Time, count int) (sql.Result, error) {
	var attID *int64
	var attRef, attName *string
	if att != nil {
		id := parseAttachmentID(att.ID)
		ref, filename := att.ID, att.Filename
		attID, attRef, attName = &id, &ref, &filename
	}
	return m.db.ExecContext(ctx, `
		INSERT INTO access_records (
			ticket_seq, actor_kind, actor_name, actor_member_id, actor_agent_id, actor_link_id,
			actor_link_note, entry_method, action, attachment_id, attachment_ref, attachment_filename,
			first_at, last_at, count
		) VALUES (?, ?, ?, NULLIF(?, 0), NULLIF(?, 0), NULLIF(?, 0),
		          ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ticketSeq, kind, name, memberID, agentID, linkID, note, entry, action, attID, attRef, attName,
		first.Format(time.RFC3339Nano), last.Format(time.RFC3339Nano), count)
}

func parseAttachmentID(ref string) int64 {
	var id int64
	_, _ = fmt.Sscanf(strings.TrimPrefix(ref, "att_"), "%d", &id)
	return id
}

// List 按首次时间返回一张工单的访问记录。读取本身不产生记录。
func (m *Manager) List(ctx context.Context, ticketSeq int64) ([]Record, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT actor_kind, actor_name, actor_link_note, entry_method, action,
		       attachment_ref, attachment_filename, first_at, last_at, count
		FROM access_records WHERE ticket_seq=? ORDER BY first_at, id`, ticketSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var (
			r        Record
			linkNote sql.NullString
			entry    sql.NullString
			attRef   sql.NullString
			attName  sql.NullString
			firstRaw string
			lastRaw  string
		)
		if err := rows.Scan(&r.Actor.Kind, &r.Actor.Name, &linkNote, &entry, &r.Action,
			&attRef, &attName, &firstRaw, &lastRaw, &r.Count); err != nil {
			return nil, err
		}
		r.Actor.IsAdmin = r.Actor.Kind == "admin"
		r.Actor.LinkNote = linkNullString(linkNote)
		r.Actor.EntryMethod = linkNullString(entry)
		if attRef.Valid {
			r.Attachment = &Attachment{ID: attRef.String, Filename: attName.String}
		}
		if r.FirstAt, err = time.ParseInLocation(time.RFC3339Nano, firstRaw, clock.Beijing); err != nil {
			return nil, err
		}
		if r.LastAt, err = time.ParseInLocation(time.RFC3339Nano, lastRaw, clock.Beijing); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func linkNullString(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}
