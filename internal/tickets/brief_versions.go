package tickets

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
)

// BriefVersion 是说明某一版的完整快照。模板定义仍用工单上的模板快照。
type BriefVersion struct {
	Revision      int64
	Title         string
	BriefMD       string
	Fields        map[string]string
	AttachmentIDs []string
	Author        Author
	At            time.Time
}

var ErrBriefVersionNotFound = errors.New("这个说明版本上线前未存档")

func (m *Manager) insertBriefVersionTx(ctx context.Context, tx *sql.Tx, t Ticket, a Author, at time.Time) error {
	var fieldsJSON, attachmentIDs any
	if t.Fields != nil {
		buf, err := json.Marshal(t.Fields)
		if err != nil {
			return fmt.Errorf("序列化说明版本字段失败：%w", err)
		}
		fieldsJSON = string(buf)
	}
	if t.BriefAttachmentIDs != nil {
		buf, err := json.Marshal(t.BriefAttachmentIDs)
		if err != nil {
			return fmt.Errorf("序列化说明版本附件失败：%w", err)
		}
		attachmentIDs = string(buf)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO brief_versions (
		    ticket_seq, revision, title, brief_md, fields_json, attachment_ids,
		    actor_member_id, actor_agent_id, actor_link_id, actor_link_note,
		    actor_author_name, actor_via_agent, at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Seq, t.BriefRevision, t.Title, t.BriefMD, fieldsJSON, attachmentIDs,
		nullInt(a.MemberID), nullInt(a.AgentID), nullInt(a.LinkID), nullStr(a.LinkNote),
		nullStr(a.AuthorName), boolToInt(a.ViaAgent), at.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("写入说明版本失败：%w", err)
	}
	return nil
}

func (m *Manager) BriefVersions(ctx context.Context, seq int64) ([]BriefVersion, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT v.revision, v.title, v.brief_md, v.fields_json, v.attachment_ids,
		       v.actor_member_id, mm.name, v.actor_agent_id, aa.name,
		       v.actor_link_id, v.actor_link_note, v.actor_author_name,
		       v.actor_via_agent, v.at
		FROM brief_versions v
		LEFT JOIN members mm ON mm.id = v.actor_member_id
		LEFT JOIN agent_tokens aa ON aa.id = v.actor_agent_id
		WHERE v.ticket_seq = ? ORDER BY v.revision`, seq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BriefVersion{}
	for rows.Next() {
		v, err := scanBriefVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (m *Manager) BriefVersion(ctx context.Context, seq, revision int64) (BriefVersion, error) {
	row := m.db.QueryRowContext(ctx, `
		SELECT v.revision, v.title, v.brief_md, v.fields_json, v.attachment_ids,
		       v.actor_member_id, mm.name, v.actor_agent_id, aa.name,
		       v.actor_link_id, v.actor_link_note, v.actor_author_name,
		       v.actor_via_agent, v.at
		FROM brief_versions v
		LEFT JOIN members mm ON mm.id = v.actor_member_id
		LEFT JOIN agent_tokens aa ON aa.id = v.actor_agent_id
		WHERE v.ticket_seq = ? AND v.revision = ?`, seq, revision)
	v, err := scanBriefVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return BriefVersion{}, ErrBriefVersionNotFound
	}
	return v, err
}

type briefVersionScanner interface {
	Scan(dest ...any) error
}

func scanBriefVersion(row briefVersionScanner) (BriefVersion, error) {
	var (
		v          BriefVersion
		fieldsJSON sql.NullString
		attsJSON   sql.NullString
		memberID   sql.NullInt64
		memberName sql.NullString
		agentID    sql.NullInt64
		agentName  sql.NullString
		linkID     sql.NullInt64
		linkNote   sql.NullString
		authorName sql.NullString
		viaAgent   int
		at         string
	)
	if err := row.Scan(&v.Revision, &v.Title, &v.BriefMD, &fieldsJSON, &attsJSON,
		&memberID, &memberName, &agentID, &agentName,
		&linkID, &linkNote, &authorName, &viaAgent, &at); err != nil {
		return BriefVersion{}, err
	}
	if fieldsJSON.Valid && fieldsJSON.String != "" {
		if err := json.Unmarshal([]byte(fieldsJSON.String), &v.Fields); err != nil {
			return BriefVersion{}, fmt.Errorf("解析说明版本字段失败：%w", err)
		}
	}
	v.AttachmentIDs = []string{}
	if attsJSON.Valid && attsJSON.String != "" {
		if err := json.Unmarshal([]byte(attsJSON.String), &v.AttachmentIDs); err != nil {
			return BriefVersion{}, fmt.Errorf("解析说明版本附件失败：%w", err)
		}
	}
	v.Author.MemberID = memberID.Int64
	v.Author.MemberName = memberName.String
	v.Author.AgentID = agentID.Int64
	v.Author.AgentName = agentName.String
	v.Author.LinkID = linkID.Int64
	v.Author.LinkNote = linkNote.String
	v.Author.AuthorName = authorName.String
	v.Author.ViaAgent = viaAgent == 1
	var err error
	if v.At, err = time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing); err != nil {
		return BriefVersion{}, fmt.Errorf("解析说明版本时间 %q 失败：%w", at, err)
	}
	return v, nil
}
