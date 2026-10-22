package drafts

import (
	"context"
	"encoding/json"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

type BriefHistoryVersion struct {
	Version
	Snapshot tickets.BriefVersion
}

func (m *Manager) BriefHistory(ctx context.Context, ticketSeq, revision int64) ([]BriefHistoryVersion, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT v.version, v.request_json, v.author_member_id, mm.name,
		       COALESCE(v.author_agent_id, 0), COALESCE(aa.name, ''), v.at
		FROM drafts d
		JOIN timeline_events e ON e.seq = d.sent_timeline_seq AND e.ticket_seq = d.ticket_seq
		JOIN draft_versions v ON v.draft_id = d.id
		JOIN members mm ON mm.id = v.author_member_id
		LEFT JOIN agent_tokens aa ON aa.id = v.author_agent_id
		WHERE d.ticket_seq = ? AND e.revision = ? AND d.status = 'sent' AND e.type = 'brief_revised'
		ORDER BY v.version`, ticketSeq, revision)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type rawVersion struct {
		Version
		Request Request
		request string
	}
	raw := []rawVersion{}
	for rows.Next() {
		var item rawVersion
		var at string
		if err = rows.Scan(&item.Version.Version, &item.request, &item.MemberID, &item.MemberName,
			&item.AgentID, &item.AgentName, &at); err != nil {
			return nil, err
		}
		if item.At, err = time.ParseInLocation(time.RFC3339Nano, at, clock.Beijing); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(item.request), &item.Request); err != nil {
			return nil, err
		}
		raw = append(raw, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, ErrNotFound
	}
	full, err := m.tk.BySeq(ctx, ticketSeq)
	if err != nil {
		return nil, err
	}
	base, err := m.tk.BriefVersion(ctx, ticketSeq, raw[0].Request.BaseRevision)
	if err != nil {
		return nil, err
	}
	out := make([]BriefHistoryVersion, 0, len(raw))
	for _, item := range raw {
		author := tickets.Author{MemberID: item.MemberID, MemberName: item.MemberName,
			AgentID: item.AgentID, AgentName: item.AgentName}
		snapshot := tickets.ApplyDraftSnapshot(base, tickets.ReviseInput{
			Title: item.Request.Title, BriefMD: item.Request.BriefMD, Fields: item.Request.Fields,
			AttachmentIDs: item.Request.AttachmentIDs, BaseRevision: item.Request.BaseRevision,
		}, full.TemplateDef, author, item.Version.Version, item.At)
		out = append(out, BriefHistoryVersion{Version: item.Version, Snapshot: snapshot})
		base = snapshot
	}
	return out, nil
}
